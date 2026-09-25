//go:build integration

// Integration test of factions (migration 0023,
// docs/adr/0023-health-missions-factions.md), through the real handlers,
// unit of work and ledger, on the game clock the game ships with.
//
//	A player founds a faction at city hall — a double press founds one and
//	pays the fee once. They invite a second player, who accepts. The
//	member puts money in the faction's bank (once for a double press) and
//	may not take any out; the leader may. Both stand at the industrial
//	zone: the leader plans a warehouse heist, the member joins, the leader
//	launches it, and the scheduler's end, delivered twice, settles it once:
//	the take split into the bank's cut and the crew's shares by rank, not a
//	unit lost.
package tests

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/health"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// luckyIDs hands out ids on which an organised crime's first roll — its
// success — comes up well under any chance, so the test sees the split.
type luckyIDs struct{ t *testing.T }

func (g luckyIDs) NewID() string {
	for {
		id := newUUID(g.t)
		if health.Mix(health.Seed(id), 1)%10_000 < 100 {
			return id
		}
	}
}

func TestFactionBankAndOrganisedCrime(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	requireStageE(t, pool)
	registry := crimeRegistry(t, pool)
	snap := registry.Current()
	def, has := snap.Faction()
	if !has || len(def.OrganisedCrimes) == 0 {
		t.Skip("the active content has no factions; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, healthCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", healthCity, err)
	}
	var clockMu sync.Mutex
	clock := time.Now().UTC()
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }

	restoreNPCProceeds(t, pool)
	leader, member := crimePlayer(t, pool, city.ID, now()), crimePlayer(t, pool, city.ID, now())
	t.Cleanup(func() { purgeStageE(t, pool, leader.ID, member.ID) })
	for _, p := range []*application.Player{leader, member} {
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_hall', place_since = $2 WHERE id = $1::uuid`,
			p.ID, now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	grantCash(t, pool, leader.ID, 100_000)
	grantCash(t, pool, member.ID, 10_000)

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	crimes := handlers.NewCrimeHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, &crimeDice{}, crimeRules(),
		time.Hour, now)
	h := handlers.NewFactionsHandler(uow, luckyIDs{t}, nil, registry, cities, postgres.NewPlayerSearchRepository(pool), gameScale,
		handlers.FactionRules{NameMin: 3, NameMax: 24, MaxMembers: 30, MaxPending: 10, ListSize: 10, Limits: limits},
		crimes, time.Hour, now)
	ledger := postgres.NewLedgerRepository(pool)
	balance := func(kind application.AccountKind, owner string) int64 {
		acct, err := ledger.AccountFor(testCtx(t), kind, owner)
		if err != nil {
			t.Fatal(err)
		}
		return acct.Balance.Minor()
	}
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	ok := func(what string, resp *presenter.Response, err error, want ...string) *presenter.Response {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if resp == nil {
			t.Fatalf("%s: no response", what)
		}
		for _, w := range want {
			if !strings.Contains(resp.Text, w) {
				t.Fatalf("%s: %q does not say %q", what, resp.Text, w)
			}
		}
		return resp
	}

	// 1. Founded once, the fee paid once, to the city.
	found := metaAs(leader, "faction.found")
	for range 2 {
		resp, err := h.Found(ctx, found, handlers.FactionCmd{Name: "Night Owls", Method: "cash"})
		ok("found", resp, err)
	}
	var factionID, factionCode string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM factions WHERE leader_id = $1::uuid AND status = 'active'`,
		leader.ID).Scan(&factionID, &factionCode); err != nil {
		t.Fatalf("no faction was founded: %v", err)
	}
	if got := balance(application.AccountPlayerCash, leader.ID); got != 100_000-def.FoundingFee {
		t.Fatalf("the founder has %d cash, want %d after one fee", got, 100_000-def.FoundingFee)
	}

	// 2. Invited and accepted.
	var memberCode string
	if err := pool.Raw().QueryRow(ctx, `SELECT public_code FROM players WHERE id = $1::uuid`, member.ID).Scan(&memberCode); err != nil {
		t.Fatal(err)
	}
	resp, err := h.Invite(ctx, metaAs(leader, "faction.invite"), handlers.FactionCmd{To: memberCode})
	ok("invite", resp, err)
	var requestNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM faction_requests WHERE faction_id = $1::uuid AND player_id = $2::uuid`,
		factionID, member.ID).Scan(&requestNo); err != nil {
		t.Fatalf("no invitation: %v (%s)", err, resp.Text)
	}
	resp, err = h.Answer(ctx, metaAs(member, "faction.answer"), handlers.FactionCmd{No: strconv.FormatInt(requestNo, 10),
		Verdict: "accept"})
	ok("accept", resp, err)
	if n := countRows(t, pool, `SELECT count(*) FROM faction_members WHERE faction_id = $1::uuid`, factionID); n != 2 {
		t.Fatalf("the faction has %d members, want 2", n)
	}

	// 3. The bank: a deposit once for a double press; a member may not take
	//    money out, the leader may.
	deposit := metaAs(member, "faction.deposit")
	for range 2 {
		resp, err = h.Deposit(ctx, deposit, handlers.FactionCmd{Amount: "3000", Method: "cash"})
		ok("deposit", resp, err)
	}
	if got := balance(application.AccountFactionTreasury, factionID); got != 3000 {
		t.Fatalf("the faction bank holds %d, want 3000 once", got)
	}
	resp, err = h.Withdraw(ctx, metaAs(member, "faction.withdraw"), handlers.FactionCmd{Amount: "1000"})
	ok("a member withdraws", resp, err, "faction.refused.")
	bankBefore := balance(application.AccountPlayerBank, leader.ID)
	resp, err = h.Withdraw(ctx, metaAs(leader, "faction.withdraw"), handlers.FactionCmd{Amount: "1000"})
	ok("the leader withdraws", resp, err)
	if got := balance(application.AccountPlayerBank, leader.ID) - bankBefore; got != 1000 {
		t.Fatalf("the leader's bank got %d, want 1000", got)
	}
	if got := balance(application.AccountFactionTreasury, factionID); got != 2000 {
		t.Fatalf("the faction bank holds %d after the withdrawal, want 2000", got)
	}

	// 4. An organised crime at the industrial zone.
	for _, p := range []*application.Player{leader, member} {
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'industrial_zone', place_since = $2 WHERE id = $1::uuid`,
			p.ID, now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	resp, err = h.Plan(ctx, metaAs(leader, "faction.plan"), handlers.FactionCmd{Crime: "warehouse_heist"})
	ok("plan", resp, err)
	var opID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM faction_operations WHERE faction_id = $1::uuid AND status = 'gathering'`,
		factionID).Scan(&opID); err != nil {
		t.Fatalf("no crime gathers: %v (%s)", err, resp.Text)
	}
	resp, err = h.Launch(ctx, metaAs(leader, "faction.launch"), handlers.FactionCmd{})
	ok("launch alone", resp, err, "faction.refused.")
	resp, err = h.Join(ctx, metaAs(member, "faction.join"))
	ok("join", resp, err)
	launch := metaAs(leader, "faction.launch")
	for range 2 {
		resp, err = h.Launch(ctx, launch, handlers.FactionCmd{})
		ok("launch", resp, err)
	}
	var (
		action  string
		resolve time.Time
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT game_action_id::text, resolves_at FROM faction_operations
	  WHERE id = $1::uuid AND status = 'running'`, opID).Scan(&action, &resolve); err != nil {
		t.Fatalf("the crime is not running: %v", err)
	}
	cash := map[string]int64{leader.ID: balance(application.AccountPlayerCash, leader.ID),
		member.ID: balance(application.AccountPlayerCash, member.ID)}
	advance(resolve.Sub(now()) + time.Second)
	for range 2 {
		m := validMeta(t)
		m.TelegramUserID, m.Command = 0, "faction.resolve"
		if _, err := h.Resolve(ctx, m, handlers.FactionScheduledRequest{ActionID: action, ActorID: leader.ID,
			ReferenceID: opID}); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	var (
		status    string
		take, cut int64
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT status, take, faction_cut FROM faction_operations WHERE id = $1::uuid`, opID).
		Scan(&status, &take, &cut); err != nil {
		t.Fatal(err)
	}
	if status != application.HeistSucceeded || take <= 0 {
		t.Fatalf("the heist ended %s with %d; want a success with a take", status, take)
	}
	if want := take * int64(def.CrimeCutBPS) / 10_000; cut < want || cut > want+1 {
		t.Errorf("the bank's cut is %d of %d, want about %d", cut, take, want)
	}
	shares := map[string]int64{}
	rows, err := pool.Raw().Query(ctx, `SELECT player_id::text, share FROM faction_operation_crew WHERE operation_id = $1::uuid`, opID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var s int64
		if err := rows.Scan(&id, &s); err != nil {
			t.Fatal(err)
		}
		shares[id] = s
	}
	rows.Close()
	if len(shares) != 2 || shares[leader.ID]+shares[member.ID]+cut != take {
		t.Fatalf("shares %v and cut %d do not add up to the take %d", shares, cut, take)
	}
	if shares[leader.ID] <= shares[member.ID] {
		t.Errorf("the leader's share %d is not above the member's %d", shares[leader.ID], shares[member.ID])
	}
	for id, before := range cash {
		if got := balance(application.AccountPlayerCash, id) - before; got != shares[id] {
			t.Errorf("a crew member got %d cash, want their share %d once", got, shares[id])
		}
	}
	if got := balance(application.AccountFactionTreasury, factionID); got != 2000+cut {
		t.Errorf("the faction bank holds %d, want %d", got, 2000+cut)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'faction_code' = $2`,
		subjects.Event("faction", "crime_resolved"), factionCode); n != 1 {
		t.Errorf("faction.crime_resolved events = %d, want 1", n)
	}
	verifyLedger(t, pool)
}
