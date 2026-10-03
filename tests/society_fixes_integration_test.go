//go:build integration

// Integration tests of the society fixes (branch fix/society): the founding
// minimum of a faction, and the friends' actions (profile, pay, invite to my
// faction, remove).
package tests

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
)

func societyFixture(t *testing.T) (pool *postgres.Pool, city *application.City, snapOK bool) {
	t.Helper()
	pool = requirePostgres(t)
	requireLedger(t, pool)
	requireStageE(t, pool)
	registry := crimeRegistry(t, pool)
	if _, has := registry.Current().Faction(); !has {
		t.Skip("the active content has no factions; run `admin content load`")
	}
	c, err := postgres.NewCityRepository(pool).ByCode(testCtx(t), healthCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", healthCity, err)
	}
	return pool, c, true
}

func actionAddresses(r *presentation.Response) map[string]bool {
	out := map[string]bool{}
	for _, a := range r.Actions {
		out[a.Address()] = true
	}
	return out
}

// A faction needs MinFounders residents in the settlement: one short is
// refused (with the count), at the number it is founded.
func TestFactionNeedsFounders(t *testing.T) {
	pool, city, _ := societyFixture(t)
	ctx := testCtx(t)
	registry := crimeRegistry(t, pool)
	var mu sync.Mutex
	clock := time.Now().UTC()
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }

	restoreNPCProceeds(t, pool)
	leader := crimePlayer(t, pool, city.ID, now())
	t.Cleanup(func() { purgeStageE(t, pool, leader.ID) })
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_hall', place_since = $2 WHERE id = $1::uuid`,
		leader.ID, now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, leader.ID, 100_000)

	var residents int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM players WHERE residence_city_id = $1::uuid AND status = 'active'`, city.ID).
		Scan(&residents); err != nil {
		t.Fatal(err)
	}

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	cities := postgres.NewCityRepository(pool)
	crimes := handlers.NewCrimeHandler(uow, workIDs{t}, nil, registry, cities, postgres.NewPolicyReader(pool, nil), gameScale,
		&crimeDice{}, crimeRules(), time.Hour, now)
	// One more resident than there are: the leader's own settlement is one short.
	h := handlers.NewFactionsHandler(uow, luckyIDs{t}, nil, registry, cities, postgres.NewPlayerSearchRepository(pool), gameScale,
		handlers.FactionRules{NameMin: 3, NameMax: 24, MaxMembers: 30, MaxPending: 10, ListSize: 10, MinFounders: residents + 1, Limits: limits},
		crimes, time.Hour, now)
	meta := validMeta(t)
	meta.TelegramUserID, meta.PlayerID, meta.Command, meta.Language = leader.TelegramUserID, leader.ID, "faction.found", "en"
	meta.IdempotencyKey = "it-" + randomToken(t, 16)

	resp, err := h.Found(ctx, meta, handlers.FactionCmd{Name: "Night Owls", Method: "cash"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Refusal == nil || resp.Refusal.Code != "faction_too_few" {
		t.Fatalf("founding one resident short gave %+v, want the faction_too_few refusal", resp)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM factions WHERE leader_id = $1::uuid`, leader.ID); n != 0 {
		t.Fatalf("a faction was founded one resident short")
	}
	// The list says how far the settlement is.
	list, err := h.List(ctx, meta, handlers.FactionCmd{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(keysOf(actionAddresses(list)), " "), "faction:found") {
		t.Fatalf("the list offers founding while it is locked: %v", actionAddresses(list))
	}

	// One more resident: the minimum is met.
	other := crimePlayer(t, pool, city.ID, now())
	t.Cleanup(func() { purgeStageE(t, pool, other.ID) })
	meta.IdempotencyKey = "it-" + randomToken(t, 16)
	resp, err = h.Found(ctx, meta, handlers.FactionCmd{Name: "Night Owls", Method: "cash"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Refusal != nil {
		t.Fatalf("founding at the minimum was refused: %+v", resp.Refusal)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM factions WHERE leader_id = $1::uuid AND status = 'active'`, leader.ID); n != 1 {
		t.Fatalf("the faction was not founded at the minimum")
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Friends: the card offers pay and remove, invite only to a faction member
// with the right (and a friend in no faction); removing asks first and is
// idempotent; a non-friend has no card.
func TestFriendActions(t *testing.T) {
	pool, city, _ := societyFixture(t)
	ctx := testCtx(t)
	registry := crimeRegistry(t, pool)
	var mu sync.Mutex
	clock := time.Now().UTC()
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }

	restoreNPCProceeds(t, pool)
	a, b := crimePlayer(t, pool, city.ID, now()), crimePlayer(t, pool, city.ID, now())
	t.Cleanup(func() { purgeStageE(t, pool, a.ID, b.ID) })
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_hall', place_since = $2 WHERE id = $1::uuid`,
		a.ID, now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, a.ID, 100_000)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM friendships WHERE player_id = ANY($1::uuid[]) OR friend_player_id = ANY($1::uuid[])`,
			[]string{a.ID, b.ID})
	})

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	social := handlers.NewSocialHandler(uow, workIDs{t}, nil, postgres.NewPlayerSearchRepository(pool), 10, time.Hour, now).
		WithFactions(registry)
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	var bCode string
	if err := pool.Raw().QueryRow(ctx, `SELECT public_code FROM players WHERE id = $1::uuid`, b.ID).Scan(&bCode); err != nil {
		t.Fatal(err)
	}

	// Not friends yet: no card.
	if _, err := social.FriendView(ctx, metaAs(a, "social.friend.view"), handlers.FriendActRequest{Player: b.ID}); err == nil {
		t.Fatal("a stranger has a friend card")
	}
	if _, err := social.FriendAdd(ctx, metaAs(a, "social.friend.add"), handlers.FriendRequest{Player: b.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := social.FriendAccept(ctx, metaAs(b, "social.friend.accept"), handlers.FriendRequest{Player: a.ID}); err != nil {
		t.Fatal(err)
	}

	// The list carries the friend's code and a card to open.
	list, err := social.FriendList(ctx, metaAs(a, "social.friend.list"), handlers.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !actionAddresses(list)["social:friend.view:"+b.ID] {
		t.Fatalf("the list has no card for the friend: %v", actionAddresses(list))
	}

	// The card: pay and remove, no invite (a is in no faction).
	card, err := social.FriendView(ctx, metaAs(a, "social.friend.view"), handlers.FriendActRequest{Player: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	addrs := actionAddresses(card)
	if !addrs["bank:pay:"+bCode] || !addrs["social:friend.remove:"+b.ID] {
		t.Fatalf("the card lacks pay or remove: %v", addrs)
	}
	if addrs["faction:invite:"+bCode] {
		t.Fatalf("the card offers an invitation to someone in no faction: %v", addrs)
	}

	// a founds a faction (leader holds the invite right): now the card offers it.
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	cities := postgres.NewCityRepository(pool)
	crimes := handlers.NewCrimeHandler(uow, workIDs{t}, nil, registry, cities, postgres.NewPolicyReader(pool, nil), gameScale,
		&crimeDice{}, crimeRules(), time.Hour, now)
	fh := handlers.NewFactionsHandler(uow, luckyIDs{t}, nil, registry, cities, postgres.NewPlayerSearchRepository(pool), gameScale,
		handlers.FactionRules{NameMin: 3, NameMax: 24, MaxMembers: 30, MaxPending: 10, ListSize: 10, Limits: limits}, crimes, time.Hour, now)
	if resp, err := fh.Found(ctx, metaAs(a, "faction.found"), handlers.FactionCmd{Name: "Friends Club", Method: "cash"}); err != nil || resp.Refusal != nil {
		t.Fatalf("founding: %v %+v", err, resp)
	}
	card, err = social.FriendView(ctx, metaAs(a, "social.friend.view"), handlers.FriendActRequest{Player: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !actionAddresses(card)["faction:invite:"+bCode] {
		t.Fatalf("the leader's card does not offer the invitation: %v", actionAddresses(card))
	}
	// The invitation the card points at is the existing, permissioned, idempotent faction.invite.
	if resp, err := fh.Invite(ctx, metaAs(a, "faction.invite"), handlers.FactionCmd{To: bCode}); err != nil || resp.Refusal != nil {
		t.Fatalf("faction.invite from the card: %v %+v", err, resp)
	}

	// Removing asks first and changes nothing.
	ask, err := social.FriendRemove(ctx, metaAs(a, "social.friend.remove"), handlers.FriendActRequest{Player: b.ID})
	if err != nil || ask.Screen != "friend_remove_ask" {
		t.Fatalf("remove without confirm: %v %v", err, ask)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM friendships WHERE player_id = $1::uuid AND friend_player_id = $2::uuid`, a.ID, b.ID); n != 1 {
		t.Fatalf("asking removed the friendship")
	}
	confirm := metaAs(a, "social.friend.remove")
	for range 2 { // a double press removes once and does not fail
		done, err := social.FriendRemove(ctx, confirm, handlers.FriendActRequest{Player: b.ID, Confirm: "yes"})
		if err != nil || done.Screen != "friend_removed" {
			t.Fatalf("remove: %v %v", err, done)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM friendships WHERE player_id = $1::uuid AND friend_player_id = $2::uuid`, a.ID, b.ID); n != 0 {
		t.Fatalf("the friendship is still there")
	}
}
