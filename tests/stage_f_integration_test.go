//go:build integration

// Shared harness of the stage F integration tests
// (docs/adr/0024-property-and-politics.md): one city, a clock the test moves,
// the real handlers over the real unit of work, and the cleanup of what a
// city's politics leave behind — seats, policies, proposals, the city's
// clock and budget, and the city-level ledger rows the test caused.
package tests

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// requireStageF skips unless migration 0024 is applied and the content has a
// budget.
func requireStageF(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.proposals') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("proposals does not exist; apply migration 0024 first")
	}
}

// fWorld is one stage F scenario: a city, a clock and the handlers.
type fWorld struct {
	t        *testing.T
	pool     *postgres.Pool
	registry *content.Registry
	snap     *content.Snapshot
	uow      application.UnitOfWork
	cities   application.CityRepository
	city     *application.City
	started  time.Time

	mu    sync.Mutex
	clock time.Time

	gov      *handlers.GovernanceHandler
	leg      *handlers.LegislatureHandler
	civic    *handlers.CityHandler
	property *handlers.PropertyHandler
}

func (w *fWorld) now() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.clock }

func (w *fWorld) advance(d time.Duration) { w.mu.Lock(); w.clock = w.clock.Add(d); w.mu.Unlock() }

// newFWorld builds the world in one shipped city.
func newFWorld(t *testing.T, cityCode string) *fWorld {
	t.Helper()
	pool := requirePostgres(t)
	requireLedger(t, pool)
	requireStageF(t, pool)
	registry := workRegistry(t, pool)
	snap := registry.Current()
	if _, ok := snap.Budget(); !ok {
		t.Skip("the active content has no budget; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, cityCode)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", cityCode, err)
	}
	// Everything the world writes is at or after started, whatever clock it
	// reads: the cleanup finds it by that.
	started := time.Now().UTC().Add(-time.Second)
	w := &fWorld{t: t, pool: pool, registry: registry, snap: snap, cities: cities, city: city,
		uow: postgres.NewUnitOfWork(pool, testDefaultLanguage), clock: started.Add(time.Second), started: started}
	dir := postgres.NewGovernanceDirectory(pool)
	policy := postgres.NewPolicyReader(pool, w.now)
	w.leg = handlers.NewLegislatureHandler(w.uow, workIDs{t}, nil, registry, cities, dir,
		handlers.LegislatureRules{VoteWindow: 48 * time.Hour, ListSize: 8}, time.Hour, w.now)
	w.gov = handlers.NewGovernanceHandler(w.uow, nil, cities, dir, policy,
		handlers.GovernanceSteps{FineDivisor: 100, CoarseDivisor: 10, AllocationStep: 500}, 10, time.Hour, w.now).
		WithLegislature(w.leg, registry)
	w.property = handlers.NewPropertyHandler(w.uow, workIDs{t}, nil, registry, cities, policy, gameScale,
		handlers.PropertyRules{ForeclosurePeriods: 3, EvictionPeriods: 2, MaxOwned: 5, MaxPrice: 100_000_000,
			MaxRent: 1_000_000, RestCooldown: 8 * time.Hour, ListSize: 10}, time.Hour, w.now)
	w.civic = handlers.NewCityHandler(w.uow, workIDs{t}, nil, registry, cities, policy, gameScale, 24*time.Hour,
		time.Hour, w.now).WithProperty(w.property)
	w.cleanupCity()
	return w
}

// resident is a player of the city with cash, cleaned up afterwards.
func (w *fWorld) resident(cash int64) *application.Player {
	w.t.Helper()
	p := crimePlayer(w.t, w.pool, w.city.ID, w.now())
	w.t.Cleanup(func() { purgeWorkFor(w.t, w.pool, p.ID) })
	w.t.Cleanup(w.purgeCity)
	if cash > 0 {
		grantCash(w.t, w.pool, p.ID, cash)
	}
	p.Language = "en"
	return p
}

// seat seats a player in an office of a place, as an operator would.
func (w *fWorld) seat(p *application.Player, office, jurisdictionID string, seat int) application.Office {
	w.t.Helper()
	var after application.Office
	if err := w.uow.Do(testCtx(w.t), func(ctx context.Context, tx application.Tx) error {
		var err error
		_, after, err = application.AppointToOffice(ctx, tx, office, jurisdictionID, seat, p.ID, w.now())
		return err
	}); err != nil {
		w.t.Fatalf("seating %s in %s %d: %v", p.ID, office, seat, err)
	}
	w.t.Cleanup(func() {
		if _, err := w.pool.Raw().Exec(context.Background(),
			`UPDATE offices SET holder_player_id = NULL, acquired_by = NULL, term_ends_at = NULL WHERE id = $1::uuid`,
			after.ID); err != nil {
			w.t.Errorf("cleanup: vacating a seat: %v", err)
		}
	})
	return after
}

func (w *fWorld) meta(p *application.Player, command string) envelope.Metadata {
	m := validMeta(w.t)
	m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
	m.IdempotencyKey = "it-" + randomToken(w.t, 16)
	return m
}

func (w *fWorld) scheduler(command string) envelope.Metadata {
	m := validMeta(w.t)
	m.TelegramUserID, m.Command = 0, command
	return m
}

func (w *fWorld) ok(what string, resp *presenter.Response, err error) *presenter.Response {
	w.t.Helper()
	if err != nil {
		w.t.Fatalf("%s: %v", what, err)
	}
	if resp == nil {
		w.t.Fatalf("%s: no response", what)
	}
	return resp
}

// do runs a player's command and fails the test on an error.
func (w *fWorld) do(what string, f func() (*presenter.Response, error)) *presenter.Response {
	w.t.Helper()
	resp, err := f()
	return w.ok(what, resp, err)
}

func (w *fWorld) count(query string, args ...any) int { return countRows(w.t, w.pool, query, args...) }

// fundTreasury pays the city's treasury from system_source, as an operator's
// grant does.
func (w *fWorld) fundTreasury(amount int64) {
	w.t.Helper()
	ledger := postgres.NewLedgerRepository(w.pool)
	acct, err := ledger.AccountFor(testCtx(w.t), application.AccountCityTreasury, w.city.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := ledger.Post(testCtx(w.t), application.LedgerTransaction{Reason: application.ReasonAdminGrant,
		Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-amount)},
			{AccountID: acct.ID, Amount: money.FromMinor(amount)},
		}, CreatedAt: w.now()}); err != nil {
		w.t.Fatal(err)
	}
}

// treasury is the city's treasury balance.
func (w *fWorld) treasury() int64 {
	return cashBalance(w.t, w.pool, application.AccountCityTreasury, w.city.ID)
}

// settleCity runs the city's period to its end, once.
func (w *fWorld) settleCity() {
	w.t.Helper()
	ctx := testCtx(w.t)
	if err := w.civic.StartClock(ctx, w.city.ID); err != nil {
		w.t.Fatal(err)
	}
	var action, payload string
	var at time.Time
	if err := w.pool.Raw().QueryRow(ctx, `SELECT g.id::text, g.payload::text, g.finish_at FROM city_clocks c
	   JOIN game_actions g ON g.id = c.action_id WHERE c.city_id = $1::uuid`, w.city.ID).Scan(&action, &payload, &at); err != nil {
		w.t.Fatalf("the city's clock: %v", err)
	}
	if wait := at.Sub(w.now()); wait > 0 {
		w.advance(wait + time.Second)
	}
	req := handlers.CrimeScheduledRequest{ActionID: action, ReferenceID: w.city.ID, Payload: []byte(payload)}
	if _, err := w.civic.Settle(ctx, w.scheduler("city.settle"), req); err != nil {
		w.t.Fatalf("settling the city: %v", err)
	}
	// A redelivery settles nothing twice.
	if _, err := w.civic.Settle(ctx, w.scheduler("city.settle"), req); err != nil {
		w.t.Fatalf("settling the city again: %v", err)
	}
}

// cleanupCity removes, when the test ends, what a city's politics leave:
// proposals and votes, policies, the city's clock and budget periods, their
// scheduled actions and events, and the ledger rows that touched the city's
// treasury since the test began (grants, budget payments, contributions).
// Each player the world makes runs it too, before the player is deleted; it
// is idempotent.
func (w *fWorld) cleanupCity() { w.t.Cleanup(w.purgeCity) }

func (w *fWorld) purgeCity() {
	{
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		tx, err := w.pool.Raw().Begin(ctx)
		if err != nil {
			w.t.Errorf("cleanup: begin: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		places := []string{w.city.JurisdictionID}
		var country string
		_ = tx.QueryRow(ctx, `SELECT COALESCE(parent_id::text, '') FROM jurisdictions WHERE id = $1::uuid`,
			w.city.JurisdictionID).Scan(&country)
		if country != "" {
			places = append(places, country)
		}
		for _, step := range []struct {
			sql  string
			args []any
		}{
			{`ALTER TABLE property_charges DISABLE TRIGGER property_charges_append_only`, nil},
			{`ALTER TABLE rent_payments DISABLE TRIGGER rent_payments_append_only`, nil},
			{`CREATE TEMP TABLE purge_props ON COMMIT DROP AS
			    SELECT id FROM properties WHERE city_id = $1::uuid AND acquired_at >= $2
			    UNION SELECT property_id FROM property_charges WHERE city_id = $1::uuid AND charged_at >= $2`,
				[]any{w.city.ID, w.started}},
			{`DELETE FROM rent_payments WHERE lease_id IN (SELECT id FROM property_leases WHERE property_id IN (SELECT id FROM purge_props))`, nil},
			{`DELETE FROM property_leases WHERE property_id IN (SELECT id FROM purge_props)`, nil},
			{`DELETE FROM property_listings WHERE property_id IN (SELECT id FROM purge_props)`, nil},
			{`DELETE FROM property_charges WHERE property_id IN (SELECT id FROM purge_props)`, nil},
			{`DELETE FROM properties WHERE id IN (SELECT id FROM purge_props)`, nil},
			{`DELETE FROM home_rests WHERE rested_at >= $1`, []any{w.started}},
			{`DELETE FROM outbox WHERE subject LIKE 'game.event.property.%' AND created_at >= $1`, []any{w.started}},
			{`ALTER TABLE border_tariffs DISABLE TRIGGER border_tariffs_append_only`, nil},
			{`DELETE FROM border_tariffs WHERE at >= $1`, []any{w.started}},
			{`ALTER TABLE border_tariffs ENABLE TRIGGER border_tariffs_append_only`, nil},
			{`ALTER TABLE property_charges ENABLE TRIGGER property_charges_append_only`, nil},
			{`ALTER TABLE rent_payments ENABLE TRIGGER rent_payments_append_only`, nil},
			{`ALTER TABLE proposal_votes DISABLE TRIGGER proposal_votes_append_only`, nil},
			{`ALTER TABLE city_budget_periods DISABLE TRIGGER city_budget_periods_append_only`, nil},
			{`ALTER TABLE policy_changes DISABLE TRIGGER policy_changes_append_only`, nil},
			{`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`, nil},
			{`CREATE TEMP TABLE purge_bills ON COMMIT DROP AS
			    SELECT id, close_action_id FROM proposals WHERE jurisdiction_id = ANY($1::uuid[]) AND opened_at >= $2`,
				[]any{places, w.started}},
			{`DELETE FROM proposal_votes WHERE proposal_id IN (SELECT id FROM purge_bills)`, nil},
			{`DELETE FROM proposals WHERE id IN (SELECT id FROM purge_bills)`, nil},
			{`DELETE FROM game_actions WHERE id IN (SELECT close_action_id FROM purge_bills)`, nil},
			{`DELETE FROM policy_changes WHERE jurisdiction_id = ANY($1::uuid[]) AND set_at >= $2`, []any{places, w.started}},
			{`DELETE FROM policy_values WHERE jurisdiction_id = ANY($1::uuid[]) AND set_at >= $2`, []any{places, w.started}},
			{`DELETE FROM city_budget_periods WHERE city_id = $1::uuid AND ended_at >= $2`, []any{w.city.ID, w.started}},
			{`UPDATE city_clocks SET action_id = NULL, next_at = NULL WHERE city_id = $1::uuid`, []any{w.city.ID}},
			{`DELETE FROM game_actions WHERE reference_type = 'city_clocks' AND reference_id = $1::uuid AND started_at >= $2`,
				[]any{w.city.ID, w.started}},
			{`DELETE FROM city_clocks WHERE city_id = $1::uuid`, []any{w.city.ID}},
			{`CREATE TEMP TABLE purge_city_tx ON COMMIT DROP AS
			    SELECT DISTINCT e.transaction_id FROM ledger_entries e
			      JOIN accounts a ON a.id = e.account_id
			     WHERE a.kind = 'city_treasury' AND a.owner_id = $1::uuid AND e.created_at >= $2
			       AND e.reason IN ('admin_grant', 'budget_spending', 'defence_contribution', 'property_purchase',
			                        'property_tax')`, []any{w.city.ID, w.started}},
			{`UPDATE accounts a SET balance = a.balance - d.delta
			    FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
			           WHERE transaction_id IN (SELECT transaction_id FROM purge_city_tx) GROUP BY account_id) d
			   WHERE a.id = d.account_id`, nil},
			{`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_city_tx)`, nil},
			{`DELETE FROM outbox WHERE subject LIKE 'game.event.legislature.%' AND created_at >= $1`, []any{w.started}},
			{`DELETE FROM outbox WHERE payload->>'jurisdiction_id' = ANY($1::text[]) AND created_at >= $2`, []any{places, w.started}},
			{`ALTER TABLE proposal_votes ENABLE TRIGGER proposal_votes_append_only`, nil},
			{`ALTER TABLE city_budget_periods ENABLE TRIGGER city_budget_periods_append_only`, nil},
			{`ALTER TABLE policy_changes ENABLE TRIGGER policy_changes_append_only`, nil},
			{`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`, nil},
		} {
			if _, err := tx.Exec(ctx, step.sql, step.args...); err != nil {
				w.t.Errorf("cleanup %q: %v", firstLine(step.sql), err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			w.t.Errorf("cleanup: commit: %v", err)
		}
	}
}

// verify runs the economy's invariants and fails on any broken one, printing
// stage F's numbers so a test shows they are not trivially zero.
func (w *fWorld) verify() postgres.StageFInvariants {
	w.t.Helper()
	v, err := postgres.NewEconomyAdmin(w.pool).VerifyLedger(testCtx(w.t), 20)
	if err != nil {
		w.t.Fatal(err)
	}
	if !v.OK() {
		w.t.Fatalf("the ledger's invariants are broken: %+v", v.StageFInvariants)
	}
	return v.StageFInvariants
}

// billNo is the number of the latest proposal of a subject in a place.
func (w *fWorld) billNo(jurisdictionID, subject string) int64 {
	w.t.Helper()
	var no int64
	if err := w.pool.Raw().QueryRow(testCtx(w.t), `SELECT no FROM proposals WHERE jurisdiction_id = $1::uuid
	   AND subject = $2 ORDER BY opened_at DESC, no DESC LIMIT 1`, jurisdictionID, subject).Scan(&no); err != nil {
		w.t.Fatalf("no proposal of %s: %v", subject, err)
	}
	return no
}

func (w *fWorld) billStatus(no int64) string {
	w.t.Helper()
	var s string
	if err := w.pool.Raw().QueryRow(testCtx(w.t), `SELECT status FROM proposals WHERE no = $1`, no).Scan(&s); err != nil {
		w.t.Fatal(err)
	}
	return s
}

// vote casts a member's vote; a replay of the same press is returned too.
func (w *fWorld) vote(p *application.Player, no int64, vote string) envelope.Metadata {
	w.t.Helper()
	m := w.meta(p, "law.vote")
	resp, err := w.leg.Vote(testCtx(w.t), m, handlers.LegislatureRequest{No: fmt.Sprint(no), Vote: vote})
	w.ok("vote", resp, err)
	return m
}

// busFare reads the bus fare from the city to another off the options
// screen's departure button.
func busFare(t *testing.T, travel *handlers.TravelHandler, p *application.Player, to string) int64 {
	t.Helper()
	resp, err := travel.Options(testCtx(t), travelMeta(p, "req-options-"+randomToken(t, 6)),
		handlers.TravelOptionsRequest{City: to})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	prefix := "travel:start:" + to + ":bus:"
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, prefix) {
				var fare int64
				fmt.Sscanf(strings.TrimPrefix(b.CallbackData, prefix), "%d", &fare) //nolint:errcheck // a miss is caught below
				if fare > 0 {
					return fare
				}
			}
		}
	}
	t.Fatalf("no priced bus to %s among the options: %q", to, resp.Text)
	return 0
}
