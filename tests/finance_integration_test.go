//go:build integration

// Integration tests of finance (migration 0029, docs/adr/0026-finance.md): a
// loan approved by the credit score, repaid once per finance period, a missed
// instalment lowering the score, a mortgage default repossessing its home; a
// health policy paying a hospital treatment once; a company listed, its
// shares traded on its book and a dividend paid once and balanced; gold
// bought and sold at the moving price. Every scenario leaves the ledger
// verifying.
package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// financeWorld is one scenario's clock, content and handler.
type financeWorld struct {
	t        *testing.T
	pool     *postgres.Pool
	registry *content.Registry
	def      content.FinanceDef
	city     *application.City
	country  string
	uow      application.UnitOfWork
	started  time.Time

	mu    sync.Mutex
	clock time.Time

	fin *handlers.FinanceHandler
}

func (w *financeWorld) now() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.clock }

func (w *financeWorld) advance(d time.Duration) { w.mu.Lock(); w.clock = w.clock.Add(d); w.mu.Unlock() }

// newFinanceWorld builds the world in ostmarch. tune changes the finance
// content the scenario runs on (a younger listing age, say); nil keeps the
// loaded content.
func newFinanceWorld(t *testing.T, tune func(*content.FinanceDef)) *financeWorld {
	t.Helper()
	pool := requirePostgres(t)
	requireLedger(t, pool)
	var ready bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.loans') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("loans does not exist; apply migration 0029 first")
	}
	pack, err := postgres.NewContentStore(pool).LoadActive(testCtx(t))
	if err != nil {
		t.Skipf("no active content: %v", err)
	}
	if len(pack.Finance) == 0 {
		t.Skip("the active content has no finance; run `admin content load`")
	}
	if tune != nil {
		tune(&pack.Finance[0])
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		t.Fatal(err)
	}
	registry := content.NewRegistry()
	registry.Swap(snap)
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	w := &financeWorld{t: t, pool: pool, registry: registry, def: pack.Finance[0], city: city,
		uow: postgres.NewUnitOfWork(pool, testDefaultLanguage), clock: time.Now().UTC()}
	w.started = w.clock.Add(-time.Second)
	if err := pool.Raw().QueryRow(ctx, `SELECT parent_id::text FROM jurisdictions WHERE id = $1::uuid`,
		city.JurisdictionID).Scan(&w.country); err != nil {
		t.Fatal(err)
	}
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	w.fin = handlers.NewFinanceHandler(w.uow, workIDs{t}, catalog, registry, cities, postgres.NewPolicyReader(pool, nil),
		gametime.Scale(gameScale), handlers.FinanceLimits{OrderTTL: 168 * time.Hour, MaxOpen: 10}, time.Hour, w.now)
	w.resetClock()
	t.Cleanup(w.purge)
	return w
}

// resetClock takes the finance clock back to nothing, so the scenario
// starts it.
func (w *financeWorld) resetClock() {
	w.t.Helper()
	for _, stmt := range []string{
		`UPDATE finance_clock SET action_id = NULL, next_at = NULL`,
		`DELETE FROM game_actions WHERE action_type = 'finance_period'`,
		`DELETE FROM finance_clock`,
	} {
		if _, err := w.pool.Raw().Exec(testCtx(w.t), stmt); err != nil {
			w.t.Fatal(err)
		}
	}
}

// purge removes everything finance wrote since the scenario began — every
// row, and every ledger transaction since then with its effect on every
// balance reversed — and gives the gold back to the dealer. It runs before
// each player's own cleanup, and again at the end; a second run finds
// nothing.
func (w *financeWorld) purge() {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := w.pool.Raw().Begin(ctx)
	if err != nil {
		w.t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	appendOnly := []string{"ledger_entries", "finance_periods", "bank_fundings", "loan_periods", "credit_events",
		"savings_interest", "insurance_premiums", "insurance_claims", "stock_listings", "share_trades", "dividends",
		"dividend_payments", "gold_prices", "gold_trades"}
	var steps []string
	for _, t := range appendOnly {
		steps = append(steps, `ALTER TABLE `+t+` DISABLE TRIGGER `+t+`_append_only`)
	}
	steps = append(steps,
		// Every transaction since the scenario began is the scenario's:
		// integration tests run one at a time.
		`CREATE TEMP TABLE purge_ftx ON COMMIT DROP AS SELECT DISTINCT transaction_id FROM ledger_entries
		  WHERE created_at >= $1`,
		`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
		          WHERE transaction_id IN (SELECT transaction_id FROM purge_ftx) GROUP BY account_id) d
		  WHERE a.id = d.account_id`,
		`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_ftx)`,
		`DELETE FROM dividend_payments WHERE dividend_id IN (SELECT id FROM dividends WHERE declared_at >= $1)`,
		`DELETE FROM dividends WHERE declared_at >= $1`,
		`DELETE FROM share_trades WHERE created_at >= $1`,
		`DELETE FROM share_orders WHERE created_at >= $1`,
		`UPDATE companies SET listed_at = NULL WHERE id IN (SELECT company_id FROM stock_listings WHERE listed_at >= $1)`,
		`DELETE FROM stock_listings WHERE listed_at >= $1`,
		`UPDATE gold_dealer SET stock = stock + COALESCE((SELECT SUM(CASE side WHEN 'buy' THEN grams ELSE -grams END)
		   FROM gold_trades WHERE traded_at >= $1), 0)`,
		`UPDATE gold_holdings h SET grams = h.grams - d.net FROM (SELECT player_id, SUM(CASE side WHEN 'buy' THEN grams
		   ELSE -grams END) AS net FROM gold_trades WHERE traded_at >= $1 GROUP BY player_id) d WHERE h.player_id = d.player_id`,
		`DELETE FROM gold_trades WHERE traded_at >= $1`,
		`DELETE FROM gold_holdings WHERE grams = 0`,
		`DELETE FROM gold_prices WHERE set_at >= $1`,
		`DELETE FROM insurance_claims WHERE claimed_at >= $1`,
		`DELETE FROM insurance_premiums WHERE paid_at >= $1`,
		`DELETE FROM insurance_policies WHERE started_at >= $1`,
		`DELETE FROM savings_interest WHERE paid_at >= $1`,
		`DELETE FROM savings_accounts WHERE updated_at >= $1`,
		`DELETE FROM credit_events WHERE at >= $1`,
		`DELETE FROM loan_periods WHERE at >= $1`,
		`DELETE FROM loans WHERE opened_at >= $1`,
		`DELETE FROM bank_fundings WHERE funded_at >= $1`,
		`UPDATE finance_clock SET action_id = NULL, next_at = NULL`,
		`DELETE FROM game_actions WHERE action_type = 'finance_period'`,
		`DELETE FROM finance_clock`,
		`DELETE FROM finance_periods WHERE settled_at >= $1`,
		`DELETE FROM portfolio_marks WHERE marked_at >= $1`,
		`DELETE FROM outbox WHERE (subject LIKE 'game.event.loan.%' OR subject LIKE 'game.event.insurance.%'
		   OR subject LIKE 'game.event.stock.%') AND created_at >= $1`,
	)
	for _, t := range appendOnly {
		steps = append(steps, `ALTER TABLE `+t+` ENABLE TRIGGER `+t+`_append_only`)
	}
	for _, stmt := range steps {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{w.started}
		}
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			w.t.Errorf("cleanup %q: %v", firstLine(stmt), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		w.t.Errorf("cleanup: commit: %v", err)
	}
}

// resident is a player living in ostmarch for a month, with money in the
// bank.
func (w *financeWorld) resident(bank int64) *application.Player {
	w.t.Helper()
	p := crimePlayer(w.t, w.pool, w.city.ID, w.now())
	// Finance's rows go before the player's: cleanups run last first.
	w.t.Cleanup(w.purge)
	if _, err := w.pool.Raw().Exec(testCtx(w.t), `UPDATE players SET language = 'en' WHERE id = $1::uuid`, p.ID); err != nil {
		w.t.Fatal(err)
	}
	if bank > 0 {
		grant(w.t, w.pool, application.AccountPlayerBank, p.ID, bank)
	}
	return p
}

func (w *financeWorld) meta(p *application.Player, command string) envelope.Metadata {
	m := validMeta(w.t)
	m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
	m.IdempotencyKey = "it-" + randomToken(w.t, 16)
	return m
}

func (w *financeWorld) ok(what string, resp *presenter.Response, err error, want ...string) *presenter.Response {
	w.t.Helper()
	if err != nil {
		w.t.Fatalf("%s: %v", what, err)
	}
	if resp == nil {
		w.t.Fatalf("%s: no screen", what)
	}
	for _, s := range want {
		if !strings.Contains(resp.Text, s) {
			w.t.Fatalf("%s: the screen does not say %q:\n%s", what, s, resp.Text)
		}
	}
	return resp
}

func (w *financeWorld) balance(kind application.AccountKind, owner string) int64 {
	return cashBalance(w.t, w.pool, kind, owner)
}

func (w *financeWorld) count(query string, args ...any) int { return countRows(w.t, w.pool, query, args...) }

// fundCountry puts money in the country's national treasury, as an
// operator's grant does, for the treasury to fund its bank.
func (w *financeWorld) fundCountry(amount int64) {
	w.t.Helper()
	ledger := postgres.NewLedgerRepository(w.pool)
	acct, err := ledger.AccountFor(testCtx(w.t), application.AccountStateTreasury, w.country)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := ledger.Post(testCtx(w.t), transfer(application.SystemSourceAccountID, acct.ID, amount,
		application.ReasonAdminGrant)); err != nil {
		w.t.Fatal(err)
	}
}

// settle runs the finance clock's current period — delivered twice, as a
// redelivery would — and returns its number.
func (w *financeWorld) settle() int64 {
	w.t.Helper()
	ctx := testCtx(w.t)
	if err := w.fin.StartClock(ctx); err != nil {
		w.t.Fatal(err)
	}
	var (
		period   int64
		actionID string
		next     time.Time
	)
	if err := w.pool.Raw().QueryRow(ctx, `SELECT period_no, action_id::text, next_at FROM finance_clock WHERE id = 1`).
		Scan(&period, &actionID, &next); err != nil {
		w.t.Fatal(err)
	}
	if d := next.Sub(w.now()); d > 0 {
		w.advance(d + time.Second)
	}
	payload, _ := json.Marshal(handlers.FinancePayload{PeriodNo: period})
	req := handlers.CrimeScheduledRequest{ActionID: actionID, ReferenceType: application.FinanceReference, Payload: payload}
	meta := validMeta(w.t)
	meta.TelegramUserID, meta.Command = 0, "finance.settle"
	for i := 0; i < 2; i++ {
		if _, err := w.fin.Settle(ctx, meta, req); err != nil {
			w.t.Fatalf("settling period %d (#%d): %v", period, i+1, err)
		}
	}
	return period
}

// verify runs `admin economy verify`'s checks.
func (w *financeWorld) verify() {
	w.t.Helper()
	v, err := postgres.NewEconomyAdmin(w.pool).VerifyLedger(testCtx(w.t), 10)
	if err != nil {
		w.t.Fatal(err)
	}
	if !v.Finance || !v.FinanceInvariants.OK() {
		w.t.Fatalf("finance does not verify: %+v", v.FinanceInvariants)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) > 0 || len(v.Drifted) > 0 {
		w.t.Fatalf("the ledger does not verify: sum %s, %d unbalanced, %d drifted", v.LedgerSum, len(v.Unbalanced), len(v.Drifted))
	}
}
