//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The national levy (ADR 0022) is not taken from founded settlements, and what it took before the fix is returned once
// (migration 0135, reason levy_refund).

func countryOf(t *testing.T, pool *postgres.Pool, cityID string) string {
	t.Helper()
	var country string
	err := postgres.NewUnitOfWork(pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		var err error
		country, err = tx.Diplomacy().CountryOfCity(ctx, cityID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if country == "" {
		t.Skip("the founded settlement sits under no country in this database")
	}
	return country
}

// A defence period of the country the settlement sits under takes nothing from its treasury, whatever came in.
func TestFoundedSettlementIsNeverLevied(t *testing.T) {
	e := newLaborEnv(t)
	pool := e.pool
	requireMilitary(t, pool)
	registry := companyRegistry(t, pool)
	country := countryOf(t, pool, e.cityID)
	purgeMilitary(t, pool, []string{country}, nil)
	t.Cleanup(func() { purgeMilitary(t, pool, []string{country}, nil) })
	clock := &testClock{now: time.Now().UTC()}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	forces := handlers.NewMilitaryHandler(uow, workIDs{t}, nil, registry, postgres.NewCityRepository(pool), postgres.NewPolicyReader(pool, clock.Now),
		gametime.Scale(gameScale), handlers.MilitaryRules{Period: 24 * time.Hour, ReadinessLossBPS: 1000, ReadinessRecoveryBPS: 500, ReferenceRadarKM: 150},
		time.Hour, clock.Now)
	ctx := testCtx(t)
	if err := forces.StartClocks(ctx); err != nil {
		t.Fatal(err)
	}
	// money comes into the treasury during the period: a gift, as the real ones did
	seedTreasury(t, pool, e.cityID, 50_000)
	before := treasuryOf(t, pool, e.cityID)
	var action string
	var next time.Time
	var no int64
	if err := pool.Raw().QueryRow(ctx, `SELECT action_id::text, next_at, period_no FROM military_clocks WHERE country_id = $1::uuid`, country).Scan(&action, &next, &no); err != nil {
		t.Fatal(err)
	}
	clock.Advance(next.Sub(clock.Now()) + time.Second)
	m := validMeta(t)
	m.TelegramUserID, m.Command = 0, "military.settle"
	for i := 0; i < 2; i++ { // twice: a redelivery changes nothing
		if _, err := forces.Settle(ctx, m, handlers.CrimeScheduledRequest{ActionID: action, ReferenceID: country,
			Payload: []byte(`{"country_id":"` + country + `","period_no":` + itoa(no) + `}`)}); err != nil {
			t.Fatalf("settle: %v", err)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM military_periods WHERE country_id = $1::uuid AND period_no = $2`, country, no); n != 1 {
		t.Fatalf("the period was not settled once: %d", n)
	}
	if got := treasuryOf(t, pool, e.cityID); got != before {
		t.Errorf("a founded settlement's treasury was levied: %d -> %d", before, got)
	}
	if n := e.scalar(`SELECT count(*) FROM ledger_entries l JOIN accounts a ON a.id = l.account_id
		WHERE a.kind = 'city_treasury' AND a.owner_id = $1::uuid AND l.reason IN ('national_levy', 'war_levy')`, e.cityID); n != 0 {
		t.Errorf("%d levy entries on a founded settlement", n)
	}
}

// The refund returns exactly what each settlement paid, from the national accounts in proportion and the rest from the
// system source, and a second run pays nothing.
func TestRefundNationalLevy(t *testing.T) {
	e := newLaborEnv(t)
	pool := e.pool
	requireMilitary(t, pool)
	country := countryOf(t, pool, e.cityID)
	meta2, founder2 := e.group(t)
	foundVillage(t, pool, e.h, meta2)
	var city2 string
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta2.TelegramChatID).Scan(&city2); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { purgeLedgerFor(t, pool, founder2.ID) })
	if countryOf(t, pool, city2) != country {
		t.Skip("the two settlements sit under different countries")
	}
	purgeMilitary(t, pool, []string{country}, nil)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM levy_refunds WHERE settlement_id = ANY($1::uuid[])`, []string{e.cityID, city2})
		purgeMilitary(t, pool, []string{country}, nil)
	})
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	ctx := testCtx(t)
	seedTreasury(t, pool, e.cityID, 10_000)
	seedTreasury(t, pool, city2, 10_000)

	// the history: the levy took 1000 and 3000; the country spent some and appropriated some, so it holds 1700 + 600
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		l := tx.Ledger()
		state, err := l.AccountFor(ctx, application.AccountStateTreasury, country)
		if err != nil {
			return err
		}
		fund, err := l.AccountFor(ctx, application.AccountDefenceFund, country)
		if err != nil {
			return err
		}
		for city, amount := range map[string]int64{e.cityID: 1000, city2: 3000} {
			acct, err := l.AccountFor(ctx, application.AccountCityTreasury, city)
			if err != nil {
				return err
			}
			if _, err := l.Post(ctx, application.LedgerTransaction{Reason: application.ReasonNationalLevy, CreatedAt: time.Now().UTC(),
				Entries: []application.LedgerEntry{{AccountID: acct.ID, Amount: money.FromMinor(-amount)}, {AccountID: state.ID, Amount: money.FromMinor(amount)}}}); err != nil {
				return err
			}
		}
		for _, move := range []struct {
			reason   application.Reason
			from, to string
			amount   int64
		}{
			{application.ReasonPenalty, state.ID, application.SystemSinkAccountID, 1700},
			{application.ReasonDefenceAppropriation, state.ID, fund.ID, 600},
		} {
			if _, err := l.Post(ctx, application.LedgerTransaction{Reason: move.reason, CreatedAt: time.Now().UTC(),
				Entries: []application.LedgerEntry{{AccountID: move.from, Amount: money.FromMinor(-move.amount)}, {AccountID: move.to, Amount: money.FromMinor(move.amount)}}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	balance := func(kind application.AccountKind, owner string) int64 {
		t.Helper()
		var n int64
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			acct, err := tx.Ledger().AccountFor(ctx, kind, owner)
			if err != nil {
				return err
			}
			b, err := tx.Ledger().Balance(ctx, acct.ID)
			n = b.Minor()
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	a0, b0 := treasuryOf(t, pool, e.cityID), treasuryOf(t, pool, city2)
	if balance(application.AccountStateTreasury, country) != 1700 || balance(application.AccountDefenceFund, country) != 600 {
		t.Fatalf("the fixture is wrong: state %d fund %d", balance(application.AccountStateTreasury, country), balance(application.AccountDefenceFund, country))
	}

	ops := operator.Ops{Pool: pool, Language: testDefaultLanguage}
	refunds, err := ops.RefundNationalLevy(ctx, operator.Actor{Name: "test", Reason: "refund the levy in the test"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]application.LevyRefund{}
	for _, r := range refunds {
		got[r.SettlementID] = r
	}
	ra, rb := got[e.cityID], got[city2]
	if ra.Levy != 1000 || rb.Levy != 3000 {
		t.Fatalf("refunds %+v", refunds)
	}
	if ra.FromStateTreasury+ra.FromDefenceFund+ra.FromSource != 1000 || rb.FromStateTreasury+rb.FromDefenceFund+rb.FromSource != 3000 {
		t.Errorf("a refund's sources do not add up: %+v %+v", ra, rb)
	}
	if ra.FromStateTreasury+rb.FromStateTreasury != 1700 || ra.FromDefenceFund+rb.FromDefenceFund != 600 || ra.FromSource+rb.FromSource != 1700 {
		t.Errorf("the national accounts should be drained (1700 and 600) and the shortfall 1700 come from the source: %+v %+v", ra, rb)
	}
	if treasuryOf(t, pool, e.cityID)-a0 != 1000 || treasuryOf(t, pool, city2)-b0 != 3000 {
		t.Errorf("the treasuries did not get back exactly what the levy took")
	}
	if balance(application.AccountStateTreasury, country) != 0 || balance(application.AccountDefenceFund, country) != 0 {
		t.Errorf("the national accounts were not drained")
	}
	again, err := ops.RefundNationalLevy(ctx, operator.Actor{Name: "test", Reason: "a second run"})
	if err != nil || len(again) != 0 {
		t.Errorf("a second run refunded %d settlements (%v)", len(again), err)
	}
	if treasuryOf(t, pool, e.cityID)-a0 != 1000 {
		t.Errorf("a second run paid again")
	}
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.LevyRefunds || s.LevyRefundLedger != s.LevyRefundRows || s.LevyRefundMismatched != 0 {
		t.Errorf("the levy refund invariants do not hold: %+v", s)
	}
}
