//go:build integration

// Integration tests of a character's life (migration 0028,
// docs/adr/0025-life-and-legacy.md): the needs drift on the game clock and a
// meal and a night's sleep bring them down; a paid bed is paid once, to the
// city, and verifies; the rank by net worth rises with money and falls with a
// loss, keeping inside the band; the life history is written once per event
// however often it is delivered, and another player sees only its public
// moments; the leaderboards are refreshed once per period; and a typed bio
// goes through the pending input's payload and the filter.
package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/life"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/input"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// purgeLife removes a player's life, its history, its nights — and the
// lodging fees those nights moved, so the ledger still verifies.
func purgeLife(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, stmt := range []string{
		`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`,
		`ALTER TABLE life_history DISABLE TRIGGER life_history_append_only`,
		`ALTER TABLE life_sleeps DISABLE TRIGGER life_sleeps_append_only`,
		`CREATE TEMP TABLE purge_ltx ON COMMIT DROP AS
		   SELECT DISTINCT ledger_transaction_id AS id FROM life_sleeps
		    WHERE player_id = $1::uuid AND ledger_transaction_id IS NOT NULL`,
		`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
		          WHERE transaction_id IN (SELECT id FROM purge_ltx) GROUP BY account_id) d
		  WHERE a.id = d.account_id`,
		`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT id FROM purge_ltx)`,
		`DELETE FROM life_sleeps WHERE player_id = $1::uuid`,
		`DELETE FROM life_history WHERE player_id = $1::uuid`,
		`DELETE FROM life_events WHERE player_id = $1::uuid`,
		`DELETE FROM player_photos WHERE player_id = $1::uuid`,
		`DELETE FROM player_life WHERE player_id = $1::uuid`,
		`DELETE FROM outbox WHERE subject LIKE 'game.event.life.%' AND payload->>'player_id' = $1`,
		`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`,
		`ALTER TABLE life_history ENABLE TRIGGER life_history_append_only`,
		`ALTER TABLE life_sleeps ENABLE TRIGGER life_sleeps_append_only`,
	} {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{playerID}
		}
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(stmt), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

// lifeWorld is a goods world with a life on it.
type lifeWorld struct {
	*goodsWorld
	def  content.LifeDef
	life *handlers.LifeHandler
}

func newLifeWorld(t *testing.T) *lifeWorld {
	t.Helper()
	g := newGoodsWorld(t)
	var ready bool
	if err := g.pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.player_life') IS NOT NULL`).Scan(&ready); err != nil ||
		!ready {
		t.Skip("player_life does not exist; apply migration 0028 first")
	}
	def, ok := g.registry.Current().Life()
	if !ok {
		t.Skip("the active content has no life; run `admin content load`")
	}
	w := &lifeWorld{goodsWorld: g, def: def}
	w.life = handlers.NewLifeHandler(g.uow, workIDs{t}, nil, g.registry, g.cities, postgres.NewPlayerSearchRepository(g.pool),
		gametime.Scale(gameScale), time.Hour, g.clock)
	return w
}

// lifeRow reads a player's life.
func (w *lifeWorld) lifeRow(t *testing.T, playerID string) application.PlayerLife {
	t.Helper()
	var l *application.PlayerLife
	if err := w.uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		var err error
		l, err = tx.Life().Get(ctx, playerID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if l == nil {
		t.Fatalf("player %s has no life", playerID)
	}
	return *l
}

func (w *lifeWorld) ok(t *testing.T, what string) func(*presenter.Response, error) *presenter.Response {
	return func(resp *presenter.Response, err error) *presenter.Response {
		t.Helper()
		return w.check(t, what, resp, err)
	}
}

func (w *lifeWorld) check(t *testing.T, what string, resp *presenter.Response, err error) *presenter.Response {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if resp == nil {
		t.Fatalf("%s: no screen", what)
	}
	return resp
}

func TestNeedsDriftAndAMealAndANightBringThemDown(t *testing.T) {
	w := newLifeWorld(t)
	ctx := testCtx(t)
	scale := gametime.Scale(gameScale)
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, w.clock)
	bag := handlers.NewInventoryHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, scale, crimeRules().Nerve, 10, time.Hour, w.clock)
	p := w.shopper(t, "bazaar", 5_000)

	w.ok(t, "life.me")(w.life.Me(ctx, w.meta(t, p, "life.me")))
	start := w.lifeRow(t, p.ID)
	if life.Points(start.Hunger) != w.def.Needs.Start.Hunger || life.Points(start.Sleep) != w.def.Needs.Start.Sleep {
		t.Fatalf("a new life starts at %d hunger, %d sleep; content says %+v", life.Points(start.Hunger),
			life.Points(start.Sleep), w.def.Needs.Start)
	}
	// Two game days go by: at scale 60, 48 real minutes.
	w.now = w.now.Add(48 * time.Minute)
	w.ok(t, "life.me")(w.life.Me(ctx, w.meta(t, p, "life.me")))
	later := w.lifeRow(t, p.ID)
	wantHunger := min(w.def.Needs.Start.Hunger+int(2*w.def.Needs.HungerPerGameDay), life.MaxPoints)
	wantSleep := min(w.def.Needs.Start.Sleep+int(2*w.def.Needs.SleepPerGameDay), life.MaxPoints)
	if life.Points(later.Hunger) != wantHunger || life.Points(later.Sleep) != wantSleep {
		t.Fatalf("after two game days hunger %d, sleep %d; want %d, %d", life.Points(later.Hunger),
			life.Points(later.Sleep), wantHunger, wantSleep)
	}

	// A loaf of bread lowers hunger by what its content says.
	if _, err := shops.Buy(ctx, w.meta(t, p, "shop.buy"), handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: "1",
		Method: "cash", Nonce: w.nonce(t)}); err != nil {
		t.Fatalf("buying bread: %v", err)
	}
	bread, _ := w.registry.Current().ItemDef("bread")
	var fill int
	for _, e := range bread.Effects {
		if e.Target == "hunger" {
			fill = int(e.Value)
		}
	}
	if fill >= 0 {
		t.Skip("the shipped bread does not feed; the content changed")
	}
	if _, err := bag.Use(ctx, w.meta(t, p, "inventory.use"), handlers.ItemRequest{Item: "bread", Nonce: w.nonce(t)}); err != nil {
		t.Fatalf("eating: %v", err)
	}
	fed := w.lifeRow(t, p.ID)
	if got, want := life.Points(fed.Hunger), max(wantHunger+fill, 0); got != want {
		t.Fatalf("hunger after bread = %d, want %d", got, want)
	}

	// A night on the park bench, where the player stands.
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'park' WHERE id = $1::uuid`, p.ID); err != nil {
		t.Fatal(err)
	}
	bench, _ := w.def.Spot("bench")
	sleepMeta := w.meta(t, p, "life.sleep")
	for i := 0; i < 2; i++ {
		// The same press delivered twice sleeps once.
		w.ok(t, "life.sleep")(w.life.Sleep(ctx, sleepMeta, handlers.LifeRequest{Spot: "bench"}))
	}
	rested := w.lifeRow(t, p.ID)
	if got, want := life.Points(rested.Sleep), max(wantSleep-bench.Rest, 0); got != want {
		t.Fatalf("tiredness after the bench = %d, want %d", got, want)
	}
	if rested.LastSleepAt == nil {
		t.Fatal("the night was not recorded on the life")
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM life_sleeps WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Fatalf("%d nights for one press delivered twice", n)
	}
	// Another night too soon is refused, and changes nothing.
	resp := w.ok(t, "life.sleep again")(w.life.Sleep(ctx, w.meta(t, p, "life.sleep"), handlers.LifeRequest{Spot: "bench"}))
	if !strings.Contains(resp.Text, "life.refused.too_soon") {
		t.Fatalf("a second night at once was not refused: %q", resp.Text)
	}

	// After the cooldown, a hostel bed: the price first, then paid by card,
	// once, into the city's treasury.
	w.now = w.now.Add(scale.RealWait(w.def.Sleep.CooldownDuration()) + time.Second)
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'residential_area' WHERE id = $1::uuid`, p.ID); err != nil {
		t.Fatal(err)
	}
	grant(t, w.pool, application.AccountPlayerBank, p.ID, 1_000)
	hostel, _ := w.def.Spot("hostel")
	price := w.ok(t, "life.sleep hostel")(w.life.Sleep(ctx, w.meta(t, p, "life.sleep"), handlers.LifeRequest{Spot: "hostel"}))
	if !strings.Contains(price.Text, "life.sleep_pay") {
		t.Fatalf("a paid bed did not show its price first: %q", price.Text)
	}
	treasury := w.purse(t, application.AccountCityTreasury, w.city.ID)
	bank := w.purse(t, application.AccountPlayerBank, p.ID)
	pay := w.meta(t, p, "life.sleep")
	for i := 0; i < 2; i++ {
		w.ok(t, "life.sleep hostel card")(w.life.Sleep(ctx, pay, handlers.LifeRequest{Spot: "hostel", Method: "card"}))
	}
	if got := bank - w.purse(t, application.AccountPlayerBank, p.ID); got != hostel.Price {
		t.Fatalf("the bed took %d from the bank, want %d", got, hostel.Price)
	}
	if got := w.purse(t, application.AccountCityTreasury, w.city.ID) - treasury; got != hostel.Price {
		t.Fatalf("the city got %d, want %d", got, hostel.Price)
	}
	verifyLedger(t, w.pool)
}

func TestNetWorthRankRisesWithMoneyAndFallsWithALoss(t *testing.T) {
	w := newLifeWorld(t)
	ctx := testCtx(t)
	ladder := w.def.Ladder()
	if len(ladder.Ranks) < 3 {
		t.Skip("the ladder has fewer than three ranks")
	}
	second, third := ladder.Ranks[1], ladder.Ranks[2]
	p := w.shopper(t, "bazaar", 0)
	w.ok(t, "life.me")(w.life.Me(ctx, w.meta(t, p, "life.me")))
	if got := w.lifeRow(t, p.ID).Rank; got != ladder.Ranks[0].Code {
		t.Fatalf("a penniless newcomer ranks %q, want %q", got, ladder.Ranks[0].Code)
	}

	// A deposit to the bank lifts them to the third rank.
	grant(t, w.pool, application.AccountPlayerBank, p.ID, third.Min+1_000)
	w.ok(t, "life.me")(w.life.Me(ctx, w.meta(t, p, "life.me")))
	row := w.lifeRow(t, p.ID)
	if row.Rank != third.Code || row.NetWorth != third.Min+1_000 {
		t.Fatalf("after the deposit: rank %q worth %d, want %q %d", row.Rank, row.NetWorth, third.Code, third.Min+1_000)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM life_history WHERE player_id = $1::uuid AND kind = 'rank_up'`, p.ID); n != 1 {
		t.Fatalf("%d rank-up entries, want 1", n)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'player_id' = $2`,
		subjects.Event("life", "rank_changed"), p.ID); n != 1 {
		t.Fatalf("%d rank notices, want 1", n)
	}

	// A loss that leaves them inside the band keeps the rank.
	band := ladder.Keeps(2)
	lose := func(amount int64) {
		t.Helper()
		ledger := postgres.NewLedgerRepository(w.pool)
		acct, err := ledger.AccountFor(ctx, application.AccountPlayerBank, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Post(ctx, transfer(acct.ID, application.SystemSinkAccountID, amount, application.ReasonCostOfLiving)); err != nil {
			t.Fatal(err)
		}
	}
	lose(third.Min + 1_000 - band)
	w.ok(t, "life.me")(w.life.Me(ctx, w.meta(t, p, "life.me")))
	if got := w.lifeRow(t, p.ID).Rank; got != third.Code {
		t.Fatalf("worth %d inside the band (keeps %d) fell to %q", band, band, got)
	}
	// One unit more and they fall to the rank their worth reaches.
	lose(1)
	w.ok(t, "life.me")(w.life.Me(ctx, w.meta(t, p, "life.me")))
	row = w.lifeRow(t, p.ID)
	if want := ladder.Ranks[ladder.Straight(band-1)].Code; row.Rank != want || want == third.Code {
		t.Fatalf("below the band: rank %q, want %q", row.Rank, want)
	}
	if row.Rank != second.Code && ladder.Straight(band-1) == 1 {
		t.Fatalf("fell to %q, want %q", row.Rank, second.Code)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM life_history WHERE player_id = $1::uuid AND kind = 'rank_down'`, p.ID); n != 1 {
		t.Fatalf("%d rank-down entries, want 1", n)
	}
	// Looking again changes nothing.
	w.ok(t, "life.me")(w.life.Me(ctx, w.meta(t, p, "life.me")))
	if n := countRows(t, w.pool, `SELECT count(*) FROM life_history WHERE player_id = $1::uuid AND kind LIKE 'rank_%'`, p.ID); n != 2 {
		t.Fatalf("%d rank entries after looking again, want 2", n)
	}
}

// lifeEvent is an event delivered to the life consumer.
func lifeEvent(t *testing.T, id string, payload map[string]any) *envelope.Envelope {
	t.Helper()
	m := validMeta(t)
	m.RequestID, m.EventID = id, id
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &envelope.Envelope{Metadata: m, Payload: raw}
}

func TestLifeHistoryIsWrittenOnceAndShownByWhoAsks(t *testing.T) {
	w := newLifeWorld(t)
	ctx := testCtx(t)
	p := w.shopper(t, "bazaar", 0)
	other := w.shopper(t, "bazaar", 0)

	hired := lifeEvent(t, "evt-"+randomToken(t, 12), map[string]any{"player_id": p.ID, "career": "retail", "tier": 0,
		"city_id": w.city.ID})
	for i := 0; i < 3; i++ {
		if err := w.life.OnEvent(ctx, hired, subjects.Event("job", "hired")); err != nil {
			t.Fatalf("OnEvent: %v", err)
		}
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM life_history WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Fatalf("one hiring delivered three times wrote %d entries", n)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM life_history WHERE player_id = $1::uuid AND kind = 'first_job'`, p.ID); n != 1 {
		t.Fatal("the first hiring is not the player's first job")
	}
	// A second hiring is an ordinary one.
	if err := w.life.OnEvent(ctx, lifeEvent(t, "evt-"+randomToken(t, 12), map[string]any{"player_id": p.ID,
		"career": "retail", "tier": 0, "city_id": w.city.ID}), subjects.Event("job", "hired")); err != nil {
		t.Fatal(err)
	}
	// A hospital stay is the player's own; a jail term is public.
	if err := w.life.OnEvent(ctx, lifeEvent(t, "evt-"+randomToken(t, 12), map[string]any{"player_id": p.ID,
		"city_id": w.city.ID}), subjects.Event("health", "hospitalised")); err != nil {
		t.Fatal(err)
	}
	stressBefore := w.lifeRow(t, p.ID).Stress
	if err := w.life.OnEvent(ctx, lifeEvent(t, "evt-"+randomToken(t, 12), map[string]any{"player_id": p.ID,
		"city_id": w.city.ID}), subjects.Event("crime", "jailed")); err != nil {
		t.Fatal(err)
	}
	if jailed := w.def.Event(content.LifeJailed); jailed.Stress > 0 && w.lifeRow(t, p.ID).Stress <= stressBefore {
		t.Fatal("jail did not raise stress")
	}
	kinds := func(resp *presenter.Response) string { return resp.Text }
	mine := w.ok(t, "life.history")(w.life.History(ctx, w.meta(t, p, "life.history"), handlers.LifeRequest{}))
	for _, k := range []string{"life.history.first_job", "life.history.hired", "life.history.private", "life.history.jailed"} {
		if !strings.Contains(kinds(mine), k) {
			t.Errorf("the player's own story lacks %s:\n%s", k, mine.Text)
		}
	}
	theirs := w.ok(t, "life.history of another")(w.life.History(ctx, w.meta(t, other, "life.history"),
		handlers.LifeRequest{Code: p.PublicCode}))
	if strings.Contains(theirs.Text, "life.history.private") {
		t.Fatalf("another player saw a private moment:\n%s", theirs.Text)
	}
	if !strings.Contains(theirs.Text, "life.history.jailed") {
		t.Fatalf("another player did not see a public moment:\n%s", theirs.Text)
	}
}

func TestLeaderboardsAreRefreshedOncePerPeriod(t *testing.T) {
	w := newLifeWorld(t)
	ctx := testCtx(t)
	p := w.shopper(t, "bazaar", 0)
	grant(t, w.pool, application.AccountPlayerBank, p.ID, 7_654_321)
	raw := w.pool.Raw()
	// The one clock is the test's for now; what it and its periods were is
	// put back afterwards.
	var before int64
	_ = raw.QueryRow(ctx, `SELECT COALESCE(max(period_no), 0) FROM leaderboard_periods`).Scan(&before)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, stmt := range []string{
			`UPDATE leaderboard_clock SET action_id = NULL, next_at = NULL`,
			`DELETE FROM game_actions WHERE action_type = 'leaderboard_period'`,
			`DELETE FROM leaderboard_clock`,
			`DELETE FROM leaderboard_lines WHERE period_no > $1`,
			`ALTER TABLE leaderboard_periods DISABLE TRIGGER leaderboard_periods_append_only`,
			`DELETE FROM leaderboard_periods WHERE period_no > $1`,
			`ALTER TABLE leaderboard_periods ENABLE TRIGGER leaderboard_periods_append_only`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = []any{before}
			}
			if _, err := raw.Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	if _, err := raw.Exec(ctx, `UPDATE leaderboard_clock SET action_id = NULL, next_at = NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(ctx, `DELETE FROM game_actions WHERE action_type = 'leaderboard_period'`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(ctx, `DELETE FROM leaderboard_clock`); err != nil {
		t.Fatal(err)
	}
	if err := w.life.StartClock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.life.StartClock(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM game_actions WHERE action_type = 'leaderboard_period'`); n != 1 {
		t.Fatalf("starting the clock twice scheduled %d periods", n)
	}
	var period int64
	var actionID string
	if err := raw.QueryRow(ctx, `SELECT period_no, action_id::text FROM leaderboard_clock WHERE id = 1`).Scan(&period, &actionID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(handlers.LeaderboardPayload{PeriodNo: period})
	req := handlers.CrimeScheduledRequest{ActionID: actionID, ReferenceType: application.LeaderboardReference, Payload: payload}
	meta := validMeta(t)
	meta.Command = "life.refresh"
	w.now = w.now.Add(gametime.Scale(gameScale).RealWait(w.def.Leaderboards.PeriodDuration()) + time.Second)
	for i := 0; i < 2; i++ {
		// A period delivered twice is refreshed once.
		if _, err := w.life.Refresh(ctx, meta, req); err != nil {
			t.Fatalf("Refresh #%d: %v", i+1, err)
		}
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM leaderboard_periods WHERE period_no = $1`, period); n != 1 {
		t.Fatalf("period %d refreshed %d times", period, n)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM leaderboard_lines WHERE period_no = $1 AND board = 'richest'
		AND code = $2`, period, p.PublicCode); n != 1 {
		t.Fatalf("the richest board does not list a player worth 7,654,321 (period %d)", period)
	}
	var next int64
	if err := raw.QueryRow(ctx, `SELECT period_no FROM leaderboard_clock WHERE id = 1`).Scan(&next); err != nil || next != period+1 {
		t.Fatalf("the clock is at %d (%v), want %d", next, err, period+1)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM game_actions WHERE action_type = 'leaderboard_period'
		AND status = 'scheduled'`); n < 1 {
		t.Fatal("the next period was not scheduled")
	}
	board := w.ok(t, "life.top")(w.life.Top(ctx, w.meta(t, p, "life.top"), handlers.LifeRequest{Board: "richest"}))
	if !strings.Contains(board.Text, "life.board.mine") {
		t.Fatalf("the player does not find themselves on the board:\n%s", board.Text)
	}
}

func TestBioThroughThePendingInput(t *testing.T) {
	w := newLifeWorld(t)
	ctx := testCtx(t)
	p := w.shopper(t, "bazaar", 0)
	policy, err := groups.LoadPolicy("../configs/commands.yml")
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := policy.Input("life.bio")
	if !ok {
		t.Fatal("configs/commands.yml does not let life.bio ask for text")
	}
	// What the gateway publishes when the player answers the question.
	answer := func(typed string) handlers.LifeRequest {
		t.Helper()
		pending := input.Pending{Command: "life.bio", Field: spec.Field, Text: spec.Text}
		raw, _ := json.Marshal(pending.Build(pending.Clean(typed, 500)))
		var req handlers.LifeRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		return req
	}
	refused := w.ok(t, "life.bio with a link")(w.life.Bio(ctx, w.meta(t, p, "life.bio"), answer("follow me on t.me/somebody")))
	if !strings.Contains(refused.Text, "life.refused.bio_link") {
		t.Fatalf("a bio with a link was not refused: %q", refused.Text)
	}
	if blocked := w.def.Bio.Blocked; len(blocked) > 0 {
		r := w.ok(t, "life.bio with a blocked word")(w.life.Bio(ctx, w.meta(t, p, "life.bio"), answer("I am the "+blocked[0])))
		if !strings.Contains(r.Text, "life.refused.bio_blocked") {
			t.Fatalf("a blocked word got through: %q", r.Text)
		}
	}
	w.ok(t, "life.bio")(w.life.Bio(ctx, w.meta(t, p, "life.bio"), answer("  عاشق   سفر و کتاب  ")))
	if got := w.lifeRow(t, p.ID).Bio; got != "عاشق سفر و کتاب" {
		t.Fatalf("the bio saved is %q", got)
	}
	card := w.ok(t, "life.card of another")(w.life.Card(ctx, w.meta(t, w.shopper(t, "bazaar", 0), "life.card"),
		handlers.LifeRequest{Code: p.PublicCode}))
	if !strings.Contains(card.Text, "life.card.bio") {
		t.Fatalf("another player's card does not show the bio: %q", card.Text)
	}
	w.ok(t, "life.bio clear")(w.life.Bio(ctx, w.meta(t, p, "life.bio"), handlers.LifeRequest{Clear: "yes"}))
	if got := w.lifeRow(t, p.ID).Bio; got != "" {
		t.Fatalf("the bio was not cleared: %q", got)
	}
}
