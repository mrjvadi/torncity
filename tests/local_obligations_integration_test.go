//go:build integration

package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Local obligations and the desk (roadmap 2.19 phase 2, ADR 0033 6.8 to 6.10): a player's wage is paid
// in the settlement's own money, the desk converts at the live rate less its fee, a payment the money
// cannot cover settles in SUP as before, and the verifier holds all of it.

func localRules() application.CurrencyRules {
	r := testCurrencyRules()
	r.DeskSlippageBPS = 100
	r.DeskPresets = []int64{100, 500, 2000}
	return r
}

// charteredLaborEnv is a labour environment whose settlement has chartered its money (treasury 60000 SUP:
// the fee 1000 and a deposit of 5000 leave 54000 SUP and 49,750 units).
func charteredLaborEnv(t *testing.T) *laborEnv {
	t.Helper()
	l := newLaborEnv(t)
	ctx := testCtx(t)
	currencyCleanup(t, l.pool, l.cityID)
	t.Cleanup(func() {
		c := testCtx(t)
		_, _ = l.pool.Raw().Exec(c, `ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`)
		_, _ = l.pool.Raw().Exec(c, `DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`, l.cityID)
		_, _ = l.pool.Raw().Exec(c, `ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`)
		_, _ = l.pool.Raw().Exec(c, `DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`, l.cityID)
		for _, tbl := range []string{"village_storage_days", "village_shop_days", "settlement_material_purchases"} {
			_, _ = l.pool.Raw().Exec(c, `DELETE FROM `+tbl+` WHERE settlement_id = $1::uuid`, l.cityID)
		}
		_, _ = l.pool.Raw().Exec(c, `DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`, l.cityID)
		_, _ = l.pool.Raw().Exec(c, `DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`, l.cityID)
	})
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		res, err := application.AutoCharter(ctx, tx, workIDs{t}.NewID, localRules(), l.cityID, false, "test", time.Now().UTC())
		if err != nil || !res.Done {
			t.Fatalf("chartering the test settlement: %+v %v", res, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	l.village.WithCurrencyRules(localRules())
	return l
}

func (l *laborEnv) holding(playerOrCity string) int64 {
	return l.scalar(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'foreign_holding' AND owner_id = $1::uuid`, playerOrCity)
}

func (l *laborEnv) cash(playerID string) int64 {
	return l.scalar(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'player_cash' AND owner_id = $1::uuid`, playerID)
}

func localPaymentRows(t *testing.T, l *laborEnv) int64 {
	return l.scalar(`SELECT count(*) FROM local_payments WHERE settlement_id = $1::uuid`, l.cityID)
}

// A player's wage at a workplace is paid in the settlement's own money, from the treasury's holding; when
// the treasury holds too few units it is paid in SUP as before.
func TestAPlayersWageIsPaidInTheSettlementMoney(t *testing.T) {
	l := charteredLaborEnv(t)
	ctx := testCtx(t)
	camp := newUUID(t)
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, camp, l.cityID); err != nil {
		t.Fatal(err)
	}
	// food in the village so the worker is fed; the camp's goods need room
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "wheat", Qty: 10, ToOrg: application.SettlementOrg(l.cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	worker := l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, worker.ID) })
	work := func() {
		t.Helper()
		if _, err := rrc(l.village.Work(ctx, l.as(worker, "settlement.work", "work"), handlers.VillageWorkRequest{ID: camp})); err != nil {
			t.Fatal(err)
		}
		l.clock.Advance(time.Hour)
		for _, s := range l.workingShifts(camp) {
			l.end(s)
		}
	}
	cash0, treasury0 := l.cash(worker.ID), l.holding(l.cityID)
	work()
	// the camp's wage is 40 SUP: ceil(40 x 10) = 400 units, in the player's holding, from the treasury's
	if got := l.holding(worker.ID); got != 400 {
		t.Errorf("the worker holds %d units, want 400 (40 SUP at 10 per SUP)", got)
	}
	if got := treasury0 - l.holding(l.cityID); got != 400 {
		t.Errorf("the treasury's holding fell by %d, want 400", got)
	}
	if got := l.cash(worker.ID); got != cash0 {
		t.Errorf("the worker's SUP changed (%d -> %d): the wage was paid in the local money", cash0, got)
	}
	if got := localPaymentRows(t, l); got != 1 {
		t.Errorf("one local payment row, %d", got)
	}
	var wagePaid int64
	if err := l.pool.Raw().QueryRow(ctx, `SELECT wage_paid FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'done'`, camp).Scan(&wagePaid); err != nil || wagePaid != 40 {
		t.Errorf("the shift row keeps the SUP wage: %d %v", wagePaid, err)
	}
	// the treasury runs out of units: the next wage is SUP
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		src, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, l.cityID, mustCode(t, l))
		if err != nil {
			return err
		}
		dst, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, worker.ID, mustCode(t, l))
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonFXDeskLocal,
			Entries: []application.LedgerEntry{{AccountID: src.ID, Amount: money.FromMinor(-src.Balance.Minor())}, {AccountID: dst.ID, Amount: src.Balance}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cash1 := l.cash(worker.ID)
	work()
	if got := l.cash(worker.ID) - cash1; got != 40 {
		t.Errorf("with no units in the treasury the wage is 40 SUP, got %d", got)
	}
	if got := localPaymentRows(t, l); got != 1 {
		t.Errorf("no second local payment row: %d", got)
	}
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	i := v.VillageInvariants
	if i.WageMismatched != 0 || i.LocalMismatched != 0 || i.DeskMismatched != 0 || i.LocalLedgerPay != i.LocalRowsPay || i.WageLedger != i.WageRows {
		t.Errorf("the ledger and the rows disagree: %+v", i)
	}
}

func mustCode(t *testing.T, l *laborEnv) string {
	t.Helper()
	var code string
	if err := l.pool.Raw().QueryRow(testCtx(t), `SELECT currency_code FROM village_currency_state WHERE settlement_id = $1::uuid`, l.cityID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	return code
}

// The desk: buy and sell at the live rate less the fee, one atomic confirm with price protection, a refusal
// when the treasury holds no units, and the head's fee lever.
func TestTheDeskConvertsAtTheLiveRateLessItsFee(t *testing.T) {
	l := charteredLaborEnv(t)
	ctx := testCtx(t)
	buyer := l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, buyer.ID) })
	grantCash(t, l.pool, buyer.ID, 5000)
	call := func(p *application.Player, req handlers.VillageDeskRequest) (vpres.DeskView, error) {
		t.Helper()
		m := l.as(p, "settlement.currency.desk", "currency.desk")
		resp, err := l.village.CurrencyDesk(ctx, m, req)
		if err != nil {
			return vpres.DeskView{}, err
		}
		var v vpres.DeskView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		if resp.Refusal != nil {
			return v, &deskRefusal{resp.Refusal.Code}
		}
		return v, nil
	}
	if v, err := call(buyer, handlers.VillageDeskRequest{}); err != nil || v.Stage != vpres.DeskMenu || v.FeeBPS != 30 || v.R0 != 10 || v.DeskUnits != 49_750 || len(v.PresetsSUP) != 3 {
		t.Fatalf("the menu: %+v %v", v, err)
	}
	// the quote of a buy: 1000 SUP, fee ceil(3) = 3, 997 x 10 = 9970 units
	ask, err := call(buyer, handlers.VillageDeskRequest{Side: "buy", Amount: "1000"})
	if err != nil || ask.Stage != vpres.DeskAsk || ask.Units != 9970 || ask.Fee != 3 || !ask.CanBuy {
		t.Fatalf("the ask: %+v %v", ask, err)
	}
	// price protection: a quote the desk can no longer give is refused
	if _, err := call(buyer, handlers.VillageDeskRequest{Side: "buy", Amount: "1000", Quote: "20000", Confirm: vpres.ResidenceConfirm}); err == nil || !strings.Contains(err.Error(), "desk_moved") {
		t.Errorf("a price that moved must be refused: %v", err)
	}
	cash0, tSUP0 := l.cash(buyer.ID), l.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, l.cityID)
	done, err := call(buyer, handlers.VillageDeskRequest{Side: "buy", Amount: "1000", Quote: "9970", Confirm: vpres.ResidenceConfirm})
	if err != nil || done.Stage != vpres.DeskDone {
		t.Fatalf("the conversion: %+v %v", done, err)
	}
	if got := l.holding(buyer.ID); got != 9970 {
		t.Errorf("the buyer holds %d units, want 9970", got)
	}
	if got := cash0 - l.cash(buyer.ID); got != 1000 {
		t.Errorf("the buyer paid %d SUP, want 1000", got)
	}
	if got := l.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, l.cityID) - tSUP0; got != 1000 {
		t.Errorf("the treasury took %d SUP, want 1000", got)
	}
	// selling back 5000 units: fee ceil(15) = 15, 4985 units = 498.5 SUP, floor 498
	sell, err := call(buyer, handlers.VillageDeskRequest{Side: "sell", Amount: "5000"})
	if err != nil || sell.SUP != 498 || sell.Fee != 15 {
		t.Fatalf("the sell quote: %+v %v", sell, err)
	}
	if _, err := call(buyer, handlers.VillageDeskRequest{Side: "sell", Amount: "5000", Quote: "498", Confirm: vpres.ResidenceConfirm}); err != nil {
		t.Fatalf("selling: %v", err)
	}
	if got := l.holding(buyer.ID); got != 4970 {
		t.Errorf("the seller holds %d units, want 4970", got)
	}
	// a round trip loses the fee: 1000 SUP in, 498 out for 5000 of 9970 units
	// the head's lever: only the holder of currency.charter, within 10..300
	setFee := func(p *application.Player, bps string) bool {
		t.Helper()
		r, err := l.village.CurrencyDeskFee(ctx, l.as(p, "settlement.currency.fee", "currency.fee"), handlers.VillageDeskFeeRequest{BPS: bps})
		return err == nil && r != nil && r.Refusal == nil
	}
	if setFee(l.head, "5") {
		t.Error("a fee under 10 bps is refused")
	}
	if !setFee(l.head, "100") {
		t.Error("the head sets the fee at 100 bps")
	}
	if setFee(buyer, "50") {
		t.Error("a resident without currency.charter must not set the fee")
	}
	if v, err := call(buyer, handlers.VillageDeskRequest{Side: "buy", Amount: "1000"}); err != nil || v.FeeBPS != 100 || v.Units != 9900 {
		t.Errorf("at 100 bps 1000 SUP buys 990 SUP x 10 = 9900 units: %+v %v", v, err)
	}
	// the desk runs out of units: it says so and does not mint
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		code := mustCode(t, l)
		src, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, l.cityID, code)
		if err != nil {
			return err
		}
		dst, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, l.head.ID, code)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonFXDeskLocal,
			Entries: []application.LedgerEntry{{AccountID: src.ID, Amount: money.FromMinor(-src.Balance.Minor())}, {AccountID: dst.ID, Amount: src.Balance}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, l.head.ID) })
	if _, err := call(buyer, handlers.VillageDeskRequest{Side: "buy", Amount: "100", Confirm: vpres.ResidenceConfirm}); err == nil || !strings.Contains(err.Error(), "desk_empty") {
		t.Errorf("an empty desk says so: %v", err)
	}
	var minted int64
	if err := l.pool.Raw().QueryRow(ctx, `SELECT minted_units FROM village_currency_state WHERE settlement_id = $1::uuid`, l.cityID).Scan(&minted); err != nil || minted != 49_750 {
		t.Errorf("the desk never mints: %d %v", minted, err)
	}
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	i := v.VillageInvariants
	if i.DeskMismatched != 0 || i.SupplyMismatched != 0 || i.PotMismatched != 0 || i.StrayHoldings != 0 {
		t.Errorf("the desk's flows do not verify: %+v", i)
	}
}

// deskRefusal carries a refusal code out of the helper above as an error.
type deskRefusal struct{ code string }

func (e *deskRefusal) Error() string { return e.code }

// A fee a player owes the treasury is paid in the local money when the player holds the units, and not
// otherwise (the payer is never converted silently).
func TestAFeeIsCollectedInTheLocalMoneyOnlyFromWhoHoldsIt(t *testing.T) {
	l := charteredLaborEnv(t)
	ctx := testCtx(t)
	p := l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, p.ID) })
	pay := func(ref string) application.LocalResult {
		t.Helper()
		var res application.LocalResult
		if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
			var err error
			res, err = application.PayLocal(ctx, tx, workIDs{t}.NewID, application.LocalPayment{SettlementID: l.cityID, PlayerID: p.ID,
				Direction: application.LocalCollect, Flow: application.ReasonTrainingFee, SUP: 30, RefType: "training", RefID: ref, At: time.Now().UTC()})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return res
	}
	if r := pay(newUUID(t)); r.Paid {
		t.Error("a player holding no units pays in SUP, never converted")
	}
	// the player buys units at the desk, then the fee is collected in them: 30 SUP = 300 units
	grantCash(t, l.pool, p.ID, 1000)
	if _, err := rrc(l.village.CurrencyDesk(ctx, l.as(p, "settlement.currency.desk", "currency.desk"), handlers.VillageDeskRequest{Side: "buy", Amount: "500", Confirm: vpres.ResidenceConfirm})); err != nil {
		t.Fatal(err)
	}
	t0 := l.holding(l.cityID)
	ref := newUUID(t)
	r := pay(ref)
	if !r.Paid || r.Units != 300 || l.holding(l.cityID)-t0 != 300 {
		t.Errorf("the fee in units: %+v, treasury +%d", r, l.holding(l.cityID)-t0)
	}
	if again := pay(ref); !again.Paid || !again.Already || l.holding(l.cityID)-t0 != 300 {
		t.Errorf("a redelivery pays nothing twice: %+v", again)
	}
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := v.VillageInvariants; i.LocalMismatched != 0 || i.LocalLedgerCollect != i.LocalRowsCollect || i.DeskMismatched != 0 {
		t.Errorf("the local payments do not verify: %+v", i)
	}
}
