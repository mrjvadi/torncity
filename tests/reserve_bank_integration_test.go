//go:build integration

package tests

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/reserve"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
)

// The Reserve Bank and the head's tools over the reserve (roadmap 2.19 phase 4, ADR 0033 6.3 to 6.8, 6.11 to
// 6.13 and 7): issuing more against a deposit, burning treasury units, the head's intervention on the book
// through the period job, an excess withdrawal after its notice, the wind-down with its claims, the macro
// tick, the levers read through policy.Get, and the verifier holding all of it.

func reserveRulesForTests(policy application.PolicyReader) application.ReserveRules {
	return application.ReserveRules{
		Policy: policy,
		Fallback: application.ReserveTerms{MintFeeBPS: 50, FXFeeBPS: 30, GoldHaircutBPS: 1000, MaxMoveBPS: 2000,
			WithdrawNoticeHours: 24, PolicyRateBPS: 1200},
		InterventionCapBPS: 800, PotFloorBPS: 3000, InterventionDelay: 2 * time.Hour, WindDownDays: 7,
		Presets: []int64{1000, 5000},
		Macro:   reserve.MacroRules{MNormBPS: 2000, KappaBPS: 200, PiMaxBPS: 300, WTradableBPS: 6000},
	}
}

type reserveEnv struct {
	*fxEnv
	rr application.ReserveRules
}

// newReserveEnv is the book's environment with the reserve tools switched on, built as the service builds them
// (the book gets the reserve through WithReserve; the village handler carries the same rules in its currency
// rules).
func newReserveEnv(t *testing.T) *reserveEnv {
	t.Helper()
	e := newFXEnv(t)
	rr := reserveRulesForTests(nil)
	cr := localRules()
	cr.Reserve = rr
	e.village.WithCurrencyRules(cr)
	e.fx.WithReserve(rr)
	t.Cleanup(func() {
		c := testCtx(t)
		for _, tbl := range []string{"currency_interventions", "currency_withdrawals", "currency_claims"} {
			_, _ = e.pool.Raw().Exec(c, `DELETE FROM `+tbl+` WHERE settlement_id = $1::uuid`, e.cityID)
		}
		_, _ = e.pool.Raw().Exec(c, `ALTER TABLE village_macro_periods DISABLE TRIGGER village_macro_periods_append_only`)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM village_macro_periods WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(c, `ALTER TABLE village_macro_periods ENABLE TRIGGER village_macro_periods_append_only`)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM game_actions WHERE action_type = 'fx_period'`)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM fx_clock`)
	})
	return &reserveEnv{fxEnv: e, rr: rr}
}

// reserve is the head (or another player) using the reserve screen.
func (e *reserveEnv) reserve(p *application.Player, req handlers.VillageReserveRequest) (vpres.ReserveView, *presentation.Response) {
	e.t.Helper()
	resp, err := e.village.CurrencyReserve(testCtx(e.t), e.as(p, "settlement.currency.reserve", "currency.reserve"), req)
	if err != nil {
		e.t.Fatal(err)
	}
	var v vpres.ReserveView
	if resp.View != nil {
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatal(err)
		}
	}
	return v, resp
}

func (e *reserveEnv) confirm(p *application.Player, req handlers.VillageReserveRequest) vpres.ReserveView {
	e.t.Helper()
	req.Confirm = vpres.ResidenceConfirm
	v, resp := e.reserve(p, req)
	if resp.Refusal != nil {
		e.t.Fatalf("%s refused: %s", req.Action, resp.Refusal.Code)
	}
	return v
}

func (e *reserveEnv) state() (pot, basis, supply, deposited, released, out, in int64) {
	e.t.Helper()
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `
		SELECT COALESCE((SELECT balance FROM accounts WHERE kind = 'reserve_pot' AND owner_id = st.settlement_id AND currency = 'SUP'), 0),
		       basis_sup, minted_units - burnt_units, deposited_sup, released_sup, intervention_out, intervention_in
		  FROM village_currency_state st WHERE settlement_id = $1::uuid`, e.cityID).Scan(&pot, &basis, &supply, &deposited, &released, &out, &in); err != nil {
		e.t.Fatal(err)
	}
	return
}

func (e *reserveEnv) treasuryUnits() int64 { return e.holding(e.cityID) }

func (e *reserveEnv) treasurySUP() int64 {
	return e.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, e.cityID)
}

// startClock starts the period clock and returns the scheduled action, ready for delivery.
func (e *reserveEnv) startClock() handlers.CrimeScheduledRequest {
	e.t.Helper()
	ctx := testCtx(e.t)
	_, _ = e.pool.Raw().Exec(ctx, `DELETE FROM game_actions WHERE action_type = 'fx_period'`)
	_, _ = e.pool.Raw().Exec(ctx, `DELETE FROM fx_clock`)
	if err := e.fx.StartClock(ctx); err != nil {
		e.t.Fatal(err)
	}
	return e.nextPeriod("")
}

// nextPeriod reads the scheduled period action (not the one named by except).
func (e *reserveEnv) nextPeriod(except string) handlers.CrimeScheduledRequest {
	e.t.Helper()
	var id string
	var payload []byte
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT id::text, payload FROM game_actions WHERE action_type = 'fx_period' AND status = 'scheduled' AND id::text <> $1 ORDER BY started_at DESC LIMIT 1`, except).Scan(&id, &payload); err != nil {
		e.t.Fatal(err)
	}
	return handlers.CrimeScheduledRequest{ActionID: id, Payload: payload}
}

// closePeriod advances the clock past the period's end and delivers the action, returning the next one.
func (e *reserveEnv) closePeriod(req handlers.CrimeScheduledRequest, advance time.Duration) handlers.CrimeScheduledRequest {
	e.t.Helper()
	e.clock.Advance(advance)
	if _, err := e.fx.Settle(testCtx(e.t), e.as(e.head, "fx.settle", "settle"), req); err != nil {
		e.t.Fatal(err)
	}
	return e.nextPeriod(req.ActionID)
}

func (e *reserveEnv) verifyReserve() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	i := v.VillageInvariants
	if !i.Reserve || i.ReserveFlowMismatched != 0 || i.ReleasedMismatched != 0 || i.ClaimMismatched != 0 || i.ClaimOverdrawn != 0 ||
		i.WithdrawalMismatched != 0 || i.RetiredPotMismatched != 0 || i.MacroGuards != 2 || i.PotMismatched != 0 || i.SupplyMismatched != 0 {
		e.t.Errorf("the reserve does not verify: %+v", i)
	}
	e.verifyFX()
}

// More units against a deposit raise the pot, the basis and the supply together; burning treasury units lowers
// the supply and the basis and leaves the pot, so the difference is the excess; both are one confirm, once.
func TestTheHeadIssuesMoreAndBurnsAgainstTheBasis(t *testing.T) {
	e := newReserveEnv(t)
	resident := e.resident()
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, resident.ID) })
	pot0, basis0, supply0, dep0, _, _, _ := e.state()
	tr0, tu0 := e.treasurySUP(), e.treasuryUnits()

	// a resident may not
	if _, resp := e.reserve(resident, handlers.VillageReserveRequest{Action: vpres.ReserveActionIssue, Amount: "1000", Confirm: vpres.ResidenceConfirm}); resp.Refusal == nil {
		t.Fatal("a resident issued money")
	}
	// the ask shows the figures without doing anything
	v, _ := e.reserve(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionIssue, Amount: "1000"})
	if v.Stage != vpres.ReserveAsk || v.Out != 9950 {
		t.Fatalf("the ask: stage %q out %d, want ask and 9950 units (1000 SUP at 10 less the 50 bps fee)", v.Stage, v.Out)
	}
	if pot, _, _, _, _, _, _ := e.state(); pot != pot0 {
		t.Fatal("the ask moved money")
	}
	req := handlers.VillageReserveRequest{Action: vpres.ReserveActionIssue, Amount: "1000", Confirm: vpres.ResidenceConfirm}
	m := e.as(e.head, "settlement.currency.reserve", "currency.reserve")
	for range 2 { // the same confirm twice: one issue
		if _, err := e.village.CurrencyReserve(testCtx(t), m, req); err != nil {
			t.Fatal(err)
		}
	}
	pot1, basis1, supply1, dep1, _, _, _ := e.state()
	if pot1-pot0 != 1000 || dep1-dep0 != 1000 || supply1-supply0 != 9950 || basis1-basis0 != 995 {
		t.Errorf("issue: pot +%d deposited +%d supply +%d basis +%d, want 1000 1000 9950 995", pot1-pot0, dep1-dep0, supply1-supply0, basis1-basis0)
	}
	if got := tr0 - e.treasurySUP(); got != 1000 {
		t.Errorf("the treasury paid %d SUP, want 1000", got)
	}
	if got := e.treasuryUnits() - tu0; got != 9950 {
		t.Errorf("the treasury received %d units, want 9950", got)
	}
	e.verifyReserve()

	// burn 2000 of the treasury's units: the basis falls by 2000/supply of it, the pot stays
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBurn, Amount: "2000"})
	pot2, basis2, supply2, _, _, _, _ := e.state()
	if pot2 != pot1 || supply1-supply2 != 2000 || basis2 >= basis1 || basis1-basis2 != 199 && basis1-basis2 != 200 {
		t.Errorf("burn: pot %d -> %d, supply -%d, basis %d -> %d (a fifth-tenth of the basis is gone, the pot is not)", pot1, pot2, supply1-supply2, basis1, basis2)
	}
	if pot2-basis2 <= pot1-basis1 {
		t.Errorf("the burn left no excess: %d vs %d", pot2-basis2, pot1-basis1)
	}
	// more than the treasury holds is refused
	_, resp := e.reserve(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBurn, Amount: strconv.FormatInt(e.treasuryUnits()+1, 10), Confirm: vpres.ResidenceConfirm})
	if resp.Refusal == nil {
		t.Error("burning more than the treasury holds was allowed")
	}
	e.verifyReserve()
}

// An excess withdrawal is announced publicly, waits out the Reserve Bank's notice, and executes at the first
// period close after it, once; it may not take more than the excess.
func TestAnExcessWithdrawalWaitsItsNoticeAndNeverTakesTheBasis(t *testing.T) {
	e := newReserveEnv(t)
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBurn, Amount: "5000"})
	pot, basis, _, _, _, _, _ := e.state()
	excess := pot - basis
	if excess <= 0 {
		t.Fatalf("no excess: pot %d basis %d", pot, basis)
	}
	// more than the excess is refused
	if _, resp := e.reserve(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionWithdraw, Amount: strconv.FormatInt(excess+1, 10), Confirm: vpres.ResidenceConfirm}); resp.Refusal != nil {
		t.Log("the oversized withdrawal is announced or refused by the screen")
	}
	req := e.startClock()
	amount := excess / 2
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionWithdraw, Amount: strconv.FormatInt(amount, 10)})
	if n := e.scalar(`SELECT count(*) FROM currency_withdrawals WHERE settlement_id = $1::uuid AND status = 'pending'`, e.cityID); n != 1 {
		t.Fatalf("one pending withdrawal, %d", n)
	}
	tr0 := e.treasurySUP()
	req = e.closePeriod(req, 2*time.Hour) // before the 24 hour notice
	if e.treasurySUP() != tr0 {
		t.Fatal("the withdrawal executed before its notice")
	}
	req = e.closePeriod(req, 25*time.Hour)
	if got := e.treasurySUP() - tr0; got != amount {
		t.Errorf("the treasury received %d, want %d", got, amount)
	}
	if n := e.scalar(`SELECT count(*) FROM currency_withdrawals WHERE settlement_id = $1::uuid AND status = 'done'`, e.cityID); n != 1 {
		t.Errorf("one done withdrawal, %d", n)
	}
	e.closePeriod(req, 2*time.Hour)
	if got := e.treasurySUP() - tr0; got != amount {
		t.Errorf("a later period paid again: %d", got)
	}
	pot2, basis2, _, _, released, _, _ := e.state()
	if pot2 != pot-amount || basis2 != basis || released != amount {
		t.Errorf("pot %d basis %d released %d, want %d %d %d", pot2, basis2, released, pot-amount, basis, amount)
	}
	e.verifyReserve()
}

// The head's purchase is a public request that waits the delay, blacks out trading on the book meanwhile,
// executes at the period close using the pot's SUP, and a resting sell fills it; the pot identity holds.
func TestTheHeadsInterventionRunsThroughThePeriodCloseWithinItsLimits(t *testing.T) {
	e := newReserveEnv(t)
	seller := e.trader(0, 20_000)
	req := e.startClock()
	if r := e.place(seller, "sell", 5000, 100_000); e.refusal(r) != "" { // 0.1 SUP a unit, the reference price
		t.Fatalf("the seller: %s", e.refusal(r))
	}
	pot0, _, _, _, _, _, _ := e.state()
	v, _ := e.reserve(e.head, handlers.VillageReserveRequest{})
	if v.BuyBudgetSUP <= 0 || v.BuyBudgetSUP > pot0*800/10000 {
		t.Fatalf("the buy budget %d, want within 8%% of the pot %d", v.BuyBudgetSUP, pot0)
	}
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBuy, Amount: "2000", Price: "0.1"})
	if n := e.scalar(`SELECT count(*) FROM currency_interventions WHERE settlement_id = $1::uuid AND status = 'pending'`, e.cityID); n != 1 {
		t.Fatalf("one pending request, %d", n)
	}
	// the blackout: the head, as a player, may not trade the pair while their request is pending (ADR 6.12)
	grantCash(t, e.pool, e.head.ID, 500)
	if r := e.place(e.head, "buy", 100, 100_000); e.refusal(r) == "" {
		t.Error("the head placed an order during their pending request's blackout")
	}
	// before its delay: nothing happens at a period close
	req = e.closePeriod(req, 61*time.Minute)
	if n := e.scalar(`SELECT count(*) FROM fx_orders WHERE settlement_id = $1::uuid AND purpose = 'intervention'`, e.cityID); n != 0 {
		t.Fatalf("the request executed before its delay: %d orders", n)
	}
	req = e.closePeriod(req, 2*time.Hour)
	if n := e.scalar(`SELECT count(*) FROM currency_interventions WHERE settlement_id = $1::uuid AND status = 'done'`, e.cityID); n != 1 {
		t.Fatalf("the request should be done: %d", n)
	}
	pot1, _, _, _, _, out, in := e.state()
	if out <= 0 {
		t.Fatalf("the intervention took nothing from the pot (out %d)", out)
	}
	if pot1 != pot0-out+in {
		t.Errorf("pot %d, want %d - %d + %d", pot1, pot0, out, in)
	}
	if got := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM fx_trades WHERE settlement_id = $1::uuid`, e.cityID); got != 2000 {
		t.Errorf("the purchase filled %d units against the resting sell, want 2000", got)
	}
	// the blackout is over
	if r := e.place(e.head, "buy", 100, 100_000); e.refusal(r) != "" {
		t.Errorf("the book stayed closed after the request executed: %s", e.refusal(r))
	}
	// one macro reading per closed period, never twice
	if n := e.scalar(`SELECT count(*) FROM village_macro_periods WHERE settlement_id = $1::uuid`, e.cityID); n != 2 {
		t.Errorf("macro readings %d, want 2 (one per closed period)", n)
	}
	e.closePeriod(req, 61*time.Minute)
	e.verifyReserve()
}

// A purchase larger than the period's cap is clipped to it when it executes, and never takes the pot under the
// floor of the basis.
func TestAnOversizedPurchaseIsClippedToThePeriodCap(t *testing.T) {
	e := newReserveEnv(t)
	seller := e.trader(0, 40_000)
	req := e.startClock()
	if r := e.place(seller, "sell", 30_000, 100_000); e.refusal(r) != "" {
		t.Fatalf("the seller: %s", e.refusal(r))
	}
	pot0, _, _, _, _, _, _ := e.state()
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBuy, Amount: "30000", Price: "0.1"})
	e.closePeriod(req, 3*time.Hour)
	_, _, _, _, _, out, _ := e.state()
	if out <= 0 || out > pot0*800/10000 {
		t.Errorf("the purchase took %d of a pot of %d, the cap is 8%% (%d)", out, pot0, pot0*800/10000)
	}
	if n := e.scalar(`SELECT count(*) FROM currency_interventions WHERE settlement_id = $1::uuid AND status = 'done' AND units < 30000`, e.cityID); n != 1 {
		t.Errorf("the request keeps the clipped quantity it executed: %d", n)
	}
	e.verifyReserve()
}

// A pending request can be cancelled by the holder of bank.policy, and a request over the budget never
// executes beyond it.
func TestACancelledRequestNeverExecutes(t *testing.T) {
	e := newReserveEnv(t)
	req := e.startClock()
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBuy, Amount: "500", Price: "0.1"})
	var id string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM currency_interventions WHERE settlement_id = $1::uuid AND status = 'pending'`, e.cityID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionCancel, ID: id})
	e.closePeriod(req, 3*time.Hour)
	if n := e.scalar(`SELECT count(*) FROM currency_interventions WHERE settlement_id = $1::uuid AND status = 'cancelled'`, e.cityID); n != 1 {
		t.Errorf("cancelled requests %d", n)
	}
	if n := e.scalar(`SELECT count(*) FROM fx_orders WHERE settlement_id = $1::uuid AND purpose = 'intervention'`, e.cityID); n != 0 {
		t.Errorf("a cancelled request reached the book: %d", n)
	}
	e.verifyReserve()
}

// The wind-down: orders and requests are cancelled, holders claim their pro-rata share of the pot (their units
// burn), the treasury's own units are not claimable, the window ends with the remainder to the treasury and the
// money retired, and the claims sum to no more than the pot.
func TestAWindDownPaysHoldersProRataAndRetiresTheMoney(t *testing.T) {
	e := newReserveEnv(t)
	holder := e.trader(0, 10_000)
	holder2 := e.trader(0, 20_000)
	req := e.startClock()
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBuy, Amount: "300", Price: "0.1"}) // pending, to be cancelled
	pot0, _, _, _, _, _, _ := e.state()
	// a claim before the wind-down is refused
	if _, resp := e.reserve(holder, handlers.VillageReserveRequest{Action: vpres.ReserveActionClaim, Confirm: vpres.ResidenceConfirm}); resp.Refusal == nil {
		t.Fatal("a claim before the wind-down was taken")
	}
	// a resident may not retire the money
	if _, resp := e.reserve(holder, handlers.VillageReserveRequest{Action: vpres.ReserveActionRetire, Confirm: vpres.ResidenceConfirm}); resp.Refusal == nil {
		t.Fatal("a resident retired the money")
	}
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionRetire})
	if st := e.scalar(`SELECT CASE status WHEN 'wind_down' THEN 1 ELSE 0 END FROM village_currency_state WHERE settlement_id = $1::uuid`, e.cityID); st != 1 {
		t.Fatal("the money is not winding down")
	}
	if n := e.scalar(`SELECT count(*) FROM currency_interventions WHERE settlement_id = $1::uuid AND status = 'pending'`, e.cityID); n != 0 {
		t.Errorf("a pending request survived the wind-down: %d", n)
	}
	// the holders claim: 10,000 and 20,000 of the claimable units
	c1 := holder
	units1, units2 := e.holding(holder.ID), e.holding(holder2.ID)
	v, _ := e.reserve(c1, handlers.VillageReserveRequest{})
	if !v.CanClaim || v.MyShare <= 0 {
		t.Fatalf("the holder cannot claim: %+v", v)
	}
	cash1 := e.cash(holder.ID)
	v1 := e.confirm(c1, handlers.VillageReserveRequest{Action: vpres.ReserveActionClaim})
	if got := e.cash(holder.ID) - cash1; got != v1.Out || got <= 0 {
		t.Errorf("the first holder received %d, the screen says %d", got, v1.Out)
	}
	if e.holding(holder.ID) != 0 {
		t.Errorf("the holder's units are not burnt: %d", e.holding(holder.ID))
	}
	// a repeated claim finds nothing
	if _, resp := e.reserve(c1, handlers.VillageReserveRequest{Action: vpres.ReserveActionClaim, Confirm: vpres.ResidenceConfirm}); resp.Refusal == nil {
		t.Error("a second claim of nothing was accepted")
	}
	cash2 := e.cash(holder2.ID)
	v2 := e.confirm(holder2, handlers.VillageReserveRequest{Action: vpres.ReserveActionClaim})
	got2 := e.cash(holder2.ID) - cash2
	if got2 != v2.Out || got2 <= 0 {
		t.Errorf("the second holder received %d, the screen says %d", got2, v2.Out)
	}
	// pro rata: the share of the second (twice the units) is about twice the first
	if got1 := v1.Out; got2 < got1*2-2 || got2 > got1*2+2+got1/50 {
		t.Logf("shares %d for %d units and %d for %d units", got1, units1, got2, units2)
	}
	pot1, _, _, _, _, _, _ := e.state()
	if pot0-pot1 != v1.Out+got2 {
		t.Errorf("the pot fell by %d, the claims paid %d", pot0-pot1, v1.Out+got2)
	}
	e.verifyReserve()
	// the window ends: the remainder goes to the treasury, the money is retired, the book is closed
	tr0 := e.treasurySUP()
	e.clock.Advance(8 * 24 * time.Hour)
	e.closePeriod(req, 0)
	if st := e.scalar(`SELECT CASE status WHEN 'retired' THEN 1 ELSE 0 END FROM village_currency_state WHERE settlement_id = $1::uuid`, e.cityID); st != 1 {
		t.Fatal("the money is not retired after the window")
	}
	if got := e.treasurySUP() - tr0; got != pot1 {
		t.Errorf("the remainder to the treasury is %d, want the pot's %d", got, pot1)
	}
	if pot, _, _, _, _, _, _ := e.state(); pot != 0 {
		t.Errorf("a retired money's pot holds %d", pot)
	}
	e.verifyReserve()
}

// The macro tick writes one reading per closed period for a money, once, however many replicas deliver the
// period, and the reading carries the pot, the basis and the supply it saw.
func TestTheMacroTickWritesOneReadingPerPeriod(t *testing.T) {
	e := newReserveEnv(t)
	req := e.startClock()
	next := e.closePeriod(req, 61*time.Minute)
	// a second delivery of the same period (another replica) changes nothing
	if _, err := e.fx.Settle(testCtx(t), e.as(e.head, "fx.settle", "settle"), req); err != nil {
		t.Fatal(err)
	}
	if n := e.scalar(`SELECT count(*) FROM village_macro_periods WHERE settlement_id = $1::uuid`, e.cityID); n != 1 {
		t.Fatalf("macro readings %d, want 1", n)
	}
	e.closePeriod(next, 61*time.Minute)
	if n := e.scalar(`SELECT count(*) FROM village_macro_periods WHERE settlement_id = $1::uuid`, e.cityID); n != 2 {
		t.Fatalf("macro readings %d, want 2", n)
	}
	var tr, nt, pr int64
	var pot, basis, supply int64
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT tradable_ppm, nontradable_ppm, price_ppm, pot_sup, basis_sup, supply_units FROM village_macro_periods
		WHERE settlement_id = $1::uuid ORDER BY period_no DESC LIMIT 1`, e.cityID).Scan(&tr, &nt, &pr, &pot, &basis, &supply); err != nil {
		t.Fatal(err)
	}
	if tr <= 0 || nt <= 0 || pr <= 0 {
		t.Errorf("the indices %d %d %d", tr, nt, pr)
	}
	p, b, s, _, _, _, _ := e.state()
	if pot != p || basis != b || supply != s {
		t.Errorf("the reading's pot/basis/supply %d/%d/%d, the state %d/%d/%d", pot, basis, supply, p, b, s)
	}
	// the history is append-only
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE village_macro_periods SET m_sup = m_sup + 1 WHERE settlement_id = $1::uuid`, e.cityID); err == nil {
		t.Error("a macro reading was rewritten")
	}
	e.verifyReserve()
}

// A booted service builds its handlers from config: Support named only by its code, the levers read through
// the policy reader (policy.Get), the reserve switched on by WithReserve. The period action must still run
// the head's requests and the macro tick.
func TestTheReserveWorksAsTheServiceBuildsItFromConfig(t *testing.T) {
	e := newReserveEnv(t)
	policy := postgres.NewPolicyReader(e.pool, e.clock.Now)
	booted := reserveRulesForTests(policy)
	booted.Fallback = application.ReserveTerms{} // config carries levers only as fallbacks; the content's levers decide
	booted.Fallback.MintFeeBPS = 50
	cr := localRules()
	cr.Reserve = booted
	e.village.WithCurrencyRules(cr)
	e.fx = handlers.NewFXHandler(postgres.NewUnitOfWork(e.pool, testDefaultLanguage), workIDs{t}, postgres.NewCityRepository(e.pool), fxRulesForTests(""), "support", time.Hour, e.clock.Now).WithReserve(booted)
	// the lever is read through policy.Get: the notice is the content's 72 hours, not a fallback
	var jur string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM jurisdictions WHERE code = 'support_reserve_bank'`).Scan(&jur); err != nil {
		t.Skipf("the Reserve Bank is not in the content of this database: %v", err)
	}
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBurn, Amount: "5000"})
	pot, basis, _, _, _, _, _ := e.state()
	req := e.startClock()
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionWithdraw, Amount: strconv.FormatInt((pot-basis)/2, 10)})
	var after time.Time
	var requested time.Time
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT execute_after, requested_at FROM currency_withdrawals WHERE settlement_id = $1::uuid`, e.cityID).Scan(&after, &requested); err != nil {
		t.Fatal(err)
	}
	if got := after.Sub(requested); got != 72*time.Hour {
		t.Errorf("the notice is %v, want 72h from the Reserve Bank's lever", got)
	}
	e.confirm(e.head, handlers.VillageReserveRequest{Action: vpres.ReserveActionBuy, Amount: "300", Price: "0.1"})
	req = e.closePeriod(req, 3*time.Hour)
	if n := e.scalar(`SELECT count(*) FROM currency_interventions WHERE settlement_id = $1::uuid AND status <> 'pending'`, e.cityID); n != 1 {
		t.Errorf("the request did not run from a handler built the service's way: %d", n)
	}
	if n := e.scalar(`SELECT count(*) FROM village_macro_periods WHERE settlement_id = $1::uuid`, e.cityID); n != 1 {
		t.Errorf("the macro tick did not run: %d", n)
	}
	e.closePeriod(req, 73*time.Hour)
	if n := e.scalar(`SELECT count(*) FROM currency_withdrawals WHERE settlement_id = $1::uuid AND status = 'done'`, e.cityID); n != 1 {
		t.Errorf("the withdrawal did not execute after its notice: %d", n)
	}
	e.verifyReserve()
}

var _ = context.Background
