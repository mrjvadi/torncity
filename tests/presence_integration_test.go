//go:build integration

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/presence"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// Presence (docs/adr/0030, R1) against real PostgreSQL and Redis: the
// heartbeat key expires by itself, activity comes from the player's open
// game_actions row, the «last seen» setting is stored and constrained, and
// the visibility rules give each viewer exactly their tier.

type staticLanguages struct{}

func (staticLanguages) Languages() []string { return []string{"fa", "en"} }

func placePlayer(t *testing.T, pool *postgres.Pool, playerID, cityID, place string) {
	t.Helper()
	if _, err := pool.Raw().Exec(testCtx(t),
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = NULLIF($3, '') WHERE id = $1::uuid`,
		playerID, cityID, place); err != nil {
		t.Fatal(err)
	}
}

func TestPresenceVisibilityAndActivity(t *testing.T) {
	pool := requirePostgres(t)
	rdb := requireRedis(t)
	ctx := testCtx(t)

	home := cityIDByCode(t, pool, "calderis")
	away := cityIDByCode(t, pool, "vantor_reach") // the same country as calderis
	abroad := cityIDByCode(t, pool, "brennhaven")

	viewer, mate, friend, citizen, stranger := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	placePlayer(t, pool, viewer.ID, home, "")
	placePlayer(t, pool, mate.ID, home, "city_centre")
	placePlayer(t, pool, friend.ID, away, "city_centre")
	placePlayer(t, pool, citizen.ID, away, "")
	placePlayer(t, pool, stranger.ID, abroad, "")

	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO friendships (id, player_id, friend_player_id, status, created_at) VALUES ($1::uuid, $2::uuid, $3::uuid, 'accepted', now())`,
		newUUID(t), viewer.ID, friend.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM friendships WHERE player_id = $1::uuid`, viewer.ID)
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM game_actions WHERE actor_id = ANY($1::uuid[])`, []string{friend.ID, mate.ID})
	})
	// The friend is on a journey, the mate on a shift, and a market expiry
	// (nothing a player is visibly doing) must not show as activity.
	for _, a := range []struct{ typ, actor string }{{"travel", friend.ID}, {"work_shift", mate.ID}, {"market_expiry", mate.ID}} {
		if _, err := pool.Raw().Exec(ctx,
			`INSERT INTO game_actions (id, action_type, actor_type, actor_id, payload, status, started_at, finish_at)
			 VALUES ($1::uuid, $2, 'player', $3::uuid, '{}', 'scheduled', now(), now() + interval '1 hour')`,
			newUUID(t), a.typ, a.actor); err != nil {
			t.Fatal(err)
		}
	}

	svc := &application.PresenceService{Store: infraredis.NewPresence(rdb, time.Minute), Repo: postgres.NewPresenceRepository(pool), RosterLimit: 50}
	for _, p := range []*application.Player{viewer, mate, friend, citizen, stranger} {
		if err := svc.Beat(ctx, p.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	// Same settlement: everything, the place included.
	st, err := svc.Status(ctx, viewer.ID, mate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Visible || !st.Online || st.Activity != presence.Working || st.Place != "city_centre" {
		t.Errorf("the settlement's view of a mate: %+v", st.Detail)
	}
	// A friend elsewhere: status and activity, never the place.
	st, _ = svc.Status(ctx, viewer.ID, friend.ID)
	if !st.Visible || !st.Online || st.Activity != presence.Travelling || st.Place != "" {
		t.Errorf("a friend's view: %+v", st.Detail)
	}
	// A fellow citizen who is neither: online, nothing more.
	st, _ = svc.Status(ctx, viewer.ID, citizen.ID)
	if !st.Visible || !st.Online || st.Activity != "" || st.Place != "" {
		t.Errorf("a citizen's view: %+v", st.Detail)
	}

	// Another country, no tie: nothing at all.
	if st, _ = svc.Status(ctx, viewer.ID, stranger.ID); st.Visible {
		t.Errorf("a stranger from another country is visible: %+v", st.Detail)
	}

	// The setting, through the real handler: contacts hides the citizen tier,
	// nobody hides even the friend; the mate's own settlement still sees them.
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	catalog, err := i18n.Load("../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	settings := handlers.NewSettingsHandler(uow, catalog, staticLanguages{}, time.Hour)
	setFor := func(p *application.Player, v string) {
		t.Helper()
		m := validMeta(t)
		m.TelegramUserID, m.TelegramChatID = p.TelegramUserID, p.TelegramUserID
		m.Command, m.Action = "player.presence.set", "presence.set"
		resp, err := settings.SetPresence(ctx, m, handlers.PresenceRequest{Visibility: v})
		if err != nil {
			t.Fatalf("SetPresence(%s): %v", v, err)
		}
		if resp.Text == "" {
			t.Error("the settings screen came back empty")
		}
	}
	setFor(citizen, "contacts")
	if st, _ = svc.Status(ctx, viewer.ID, citizen.ID); st.Visible {
		t.Errorf("a player who chose contacts is visible to a mere citizen: %+v", st.Detail)
	}
	setFor(friend, "nobody")
	if st, _ = svc.Status(ctx, viewer.ID, friend.ID); st.Visible {
		t.Errorf("a player who chose nobody is visible to a friend elsewhere: %+v", st.Detail)
	}
	setFor(mate, "nobody")
	if st, _ = svc.Status(ctx, viewer.ID, mate.ID); !st.Visible || !st.Online || st.Place == "" {
		t.Errorf("a settlement still sees a mate who chose nobody: %+v", st.Detail)
	}
	// ...and the viewer who chose nobody sees nobody else's presence.
	setFor(viewer, "nobody")
	if st, _ = svc.Status(ctx, viewer.ID, mate.ID); st.Visible {
		t.Errorf("a viewer who hid themself still sees others: %+v", st.Detail)
	}
	if got, err := postgres.NewPresenceRepository(pool).Visibility(ctx, viewer.ID); err != nil || got != presence.Nobody {
		t.Errorf("stored setting = %q, %v", got, err)
	}

	// The list: for members only.
	setFor(viewer, "everyone")
	list, err := svc.Players(ctx, viewer.ID, home)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, p := range list.Players {
		ids[p.PlayerID] = true
	}
	if !ids[viewer.ID] || !ids[mate.ID] || ids[friend.ID] || list.Online < 2 {
		t.Errorf("home roster = %+v", list)
	}
	if _, err := svc.Players(ctx, friend.ID, home); !errors.Is(err, application.ErrNotInSettlement) {
		t.Errorf("a non-member read the roster: %v", err)
	}
	civic, err := svc.Civic(ctx, home)
	if err != nil || civic.Online < 2 {
		t.Errorf("civic roster = %+v, %v", civic, err)
	}
	members, err := svc.Memberships(ctx, viewer.ID)
	if err != nil || len(members) != 1 || members[0] != home {
		t.Errorf("memberships = %v, %v", members, err)
	}

	// Constrained in the database itself.
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET presence_visibility = 'friends' WHERE id = $1::uuid`, viewer.ID); err == nil {
		t.Error("the database accepted a setting the CHECK forbids")
	}
	if err := postgres.NewPresenceRepository(pool).SetVisibility(ctx, viewer.ID, "friends"); !errors.Is(err, application.ErrUnsupportedPresenceVisibility) {
		t.Errorf("an invalid setting: %v", err)
	}
	if _, err := settings.SetPresence(ctx, validMetaFor(t, viewer), handlers.PresenceRequest{Visibility: "friends"}); !errors.Is(err, application.ErrUnsupportedPresenceVisibility) {
		t.Errorf("the handler accepted an invalid setting: %v", err)
	}
}

func validMetaFor(t *testing.T, p *application.Player) (m envelope.Metadata) {
	t.Helper()
	m = validMeta(t)
	m.TelegramUserID, m.TelegramChatID = p.TelegramUserID, p.TelegramUserID
	return m
}

// A player who stops beating reads offline by themself, with nothing written
// when they went.
func TestPresenceExpiresWithoutASignOff(t *testing.T) {
	rdb := requireRedis(t)
	ctx := testCtx(t)
	store := infraredis.NewPresence(rdb, time.Second)
	id := newUUID(t)
	if got, _ := store.Online(ctx, []string{id}); got[id] {
		t.Fatal("online before any beat")
	}
	if err := store.Beat(ctx, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Online(ctx, []string{id}); !got[id] {
		t.Fatal("offline right after a beat")
	}
	time.Sleep(1500 * time.Millisecond)
	if got, _ := store.Online(ctx, []string{id}); got[id] {
		t.Fatal("still online after the TTL")
	}
}
