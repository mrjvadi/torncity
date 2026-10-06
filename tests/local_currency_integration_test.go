//go:build integration

package tests

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// A settlement's own money (roadmap 2.19 phase 1, ADR 0033 section 6): chartered at founding from the
// grant, once for the settlements that already exist without stripping their treasury, or by the head;
// the rate is live, and the verifier holds the pot and the supply.

func testCurrencyRules() application.CurrencyRules {
	return application.CurrencyRules{
		CharterR0:  10,
		Terms:      currency.Terms{Fee: 1000, MinDeposit: 5000, ShareBPS: 5000, Floor: 500},
		MintFeeBPS: 50,
	}
}

// currencyCleanup removes everything a charter leaves that the founding cleanup does not know: the
// ledger of the settlement, its state and issuance rows, the system accounts of its currency and the
// currency itself.
func currencyCleanup(t *testing.T, pool *postgres.Pool, cityID string) {
	t.Helper()
	t.Cleanup(func() {
		c := testCtx(t)
		var code string
		_ = pool.Raw().QueryRow(c, `SELECT currency_code FROM village_currency_state WHERE settlement_id = $1::uuid`, cityID).Scan(&code)
		purgeLedgerFor(t, pool, cityID)
		_, _ = pool.Raw().Exec(c, `DELETE FROM settlement_grants WHERE settlement_id = $1::uuid`, cityID)
		_, _ = pool.Raw().Exec(c, `DELETE FROM currency_issuance_log WHERE settlement_id = $1::uuid`, cityID)
		_, _ = pool.Raw().Exec(c, `DELETE FROM village_currency_state WHERE settlement_id = $1::uuid`, cityID)
		if code != "" && code != "SUP" && code != "NIL" {
			_, _ = pool.Raw().Exec(c, `DELETE FROM accounts WHERE currency = $1 AND kind IN ('system_source', 'system_sink')`, code)
			_, _ = pool.Raw().Exec(c, `DELETE FROM currencies WHERE code = $1`, code)
		}
		_, _ = pool.Raw().Exec(c, `DELETE FROM audit_logs WHERE action LIKE 'currency.%' AND new_value::text LIKE '%' || $1 || '%'`, cityID)
	})
}

func TestFoundingChartersTheSettlementMoney(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	e.h.WithFoundingGrant(10_000).WithCurrencyRules(testCurrencyRules())
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	currencyCleanup(t, pool, cityID)

	// the founding grant pays both the fee and the minimum deposit
	if got := treasuryOf(t, pool, cityID); got != 4000 {
		t.Errorf("treasury after founding = %d, want 10000 - 1000 fee - 5000 deposit = 4000", got)
	}
	var code string
	var r0, minted, deposited, xref int64
	if err := pool.Raw().QueryRow(ctx, `SELECT currency_code, r0, minted_units, deposited_sup, x_ref_ppm FROM village_currency_state WHERE settlement_id = $1::uuid`, cityID).
		Scan(&code, &r0, &minted, &deposited, &xref); err != nil {
		t.Fatalf("the founding left no chartered money: %v", err)
	}
	if r0 != 10 || deposited != 5000 || minted != 49_750 || xref != 1_000_000 {
		t.Errorf("state: r0 %d deposited %d minted %d x_ref %d, want 10 5000 49750 1000000", r0, deposited, minted, xref)
	}
	bal := func(kind, owner string) int64 {
		var n int64
		if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = $1 AND owner_id = $2::uuid AND currency = $3`, kind, owner, map[string]string{"reserve_pot": "SUP", "foreign_holding": code}[kind]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := bal("reserve_pot", cityID); got != 5000 {
		t.Errorf("the reserve pot holds %d, want the 5000 deposited", got)
	}
	if got := bal("foreign_holding", cityID); got != 49_750 {
		t.Errorf("the treasury holds %d units, want 49750", got)
	}
	// a second charter writes nothing (the state row is the fence)
	if err := postgres.NewUnitOfWork(pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		res, err := application.AutoCharter(ctx, tx, workIDs{t}.NewID, testCurrencyRules(), cityID, false, "test", time.Now().UTC())
		if err != nil || res.Done || res.Reason != "chartered" {
			t.Errorf("a second charter must be a no-op: %+v %v", res, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// the founder reads amounts in the new money, at the live rate
	m, err := postgres.NewDisplayResolver(pool, 0).Display(ctx, founder.ID)
	if err != nil || m == nil || m.R0 != 10 || m.XRefPPM != 1_000_000 || m.RateNum != 10_000_000 || m.RateDen != 1_000_000 || m.Name == "" {
		t.Errorf("the founder's display currency: %+v %v", m, err)
	}
	if v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10); err != nil {
		t.Fatal(err)
	} else if !v.VillageInvariants.Currencies || !v.VillageInvariants.WorkNodesOK() || v.VillageInvariants.PotMismatched+v.VillageInvariants.SupplyMismatched+v.VillageInvariants.IssuanceMismatched+v.VillageInvariants.StrayHoldings != 0 {
		t.Errorf("the currency invariants do not hold: %+v", v.VillageInvariants)
	}
}

// An existing settlement is chartered once without stripping its treasury (Marco Polo's case: 6205).
func TestTheOneOffCharterOfAnExistingSettlementKeepsItsTreasury(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	meta, _ := e.group(t)
	foundVillage(t, pool, e.h, meta) // the handler has no currency rules: founded without a charter
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	currencyCleanup(t, pool, cityID)
	seedTreasury(t, pool, cityID, 6205)
	ops := operator.Ops{Pool: pool, Language: testDefaultLanguage}
	actor := operator.Actor{Name: "tester", Reason: "the owner asked for the settlements' own money", At: time.Now().UTC()}
	reports, err := ops.CharterCurrencies(ctx, testCurrencyRules(), actor)
	if err != nil {
		t.Fatal(err)
	}
	var mine *operator.CurrencyCharterReport
	for i := range reports {
		if reports[i].SettlementID == cityID {
			mine = &reports[i]
		}
	}
	if mine == nil || !mine.Result.Done || mine.Result.Fee != 1000 || mine.Result.Deposit != 2602 || mine.Result.Units != 25_889 {
		t.Fatalf("Marco Polo's charter: %+v", mine)
	}
	if mine.TreasuryAfter != 6205-1000-2602 {
		t.Errorf("the treasury keeps 2603: %+v", mine)
	}
	// a rerun charters nothing and the audit row names the figures
	again, err := ops.CharterCurrencies(ctx, testCurrencyRules(), actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if r.SettlementID == cityID {
			t.Errorf("a rerun must not touch a chartered settlement: %+v", r)
		}
	}
	var n int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'currency.charter' AND new_value->>'settlement' = $1 AND new_value->>'done' = 'true'`, cityID).Scan(&n); err != nil || n != 1 {
		t.Errorf("one audit row for the charter: %d %v", n, err)
	}
	if v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10); err != nil {
		t.Fatal(err)
	} else if !v.VillageInvariants.Currencies || v.VillageInvariants.PotMismatched+v.VillageInvariants.SupplyMismatched+v.VillageInvariants.IssuanceMismatched+v.VillageInvariants.StrayHoldings != 0 {
		t.Errorf("the currency invariants do not hold: %+v", v.VillageInvariants)
	}
}

// The head's path: the offer, the confirm, and a second confirm that changes nothing.
func TestTheHeadChartersTheMoneyByHandAndOnlyOnce(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).WithCurrencyRules(testCurrencyRules())
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	currencyCleanup(t, pool, cityID)
	seedTreasury(t, pool, cityID, 8000)
	call := func(confirm string) vpres.CurrencyCharterView {
		t.Helper()
		m := asPlayer(meta, founder)
		m.Command = "settlement.currency.charter"
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		resp, err := village.CurrencyCharter(ctx, m, handlers.VillageCurrencyRequest{Confirm: confirm})
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.CurrencyCharterView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	ask := call("")
	if ask.Stage != vpres.CharterAsk || ask.Fee != 1000 || ask.Deposit != 5000 || ask.Units != 49_750 || !ask.CanPay {
		t.Errorf("the offer: %+v", ask)
	}
	done := call(vpres.ResidenceConfirm)
	if done.Stage != vpres.CharterDone || done.Units != 49_750 || done.XRefPPM != 1_000_000 {
		t.Errorf("the charter: %+v", done)
	}
	if got := treasuryOf(t, pool, cityID); got != 2000 {
		t.Errorf("treasury %d, want 8000 - 6000", got)
	}
	again := call(vpres.ResidenceConfirm)
	if again.Stage != vpres.CharterExists || treasuryOf(t, pool, cityID) != 2000 {
		t.Errorf("a second confirm changes nothing: %+v", again)
	}
}
