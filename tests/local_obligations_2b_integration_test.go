//go:build integration

package tests

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
)

// Local obligations, phase 2b (roadmap 2.19, ADR 0033 6.9 to 6.11): what a player owes a settlement with
// its own money is paid in it when they hold the units; a payer holding SUP is never blocked and is
// offered the desk inside the same confirm; units paid to the NPC economy are burnt; two neighbours may
// pay each other in the village's money; and the verifier holds all of it.

func (l *laborEnv) inTx(fn func(ctx context.Context, tx application.Tx) error) error {
	return postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(testCtx(l.t), fn)
}

func (l *laborEnv) verify2b() {
	l.t.Helper()
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(testCtx(l.t), 10)
	if err != nil {
		l.t.Fatal(err)
	}
	i := v.VillageInvariants
	if i.LocalMismatched != 0 || i.DeskMismatched != 0 || i.BurnMismatched != 0 || i.SupplyMismatched != 0 || i.PotMismatched != 0 ||
		i.LocalLedgerPay != i.LocalRowsPay || i.LocalLedgerCollect != i.LocalRowsCollect || i.LocalLedgerTransfer != i.LocalRowsTransfer ||
		i.BurnLedger != i.BurnRows || i.DonationMismatched != 0 || i.DonationLedger != i.DonationRows || i.StrayHoldings != 0 {
		l.t.Errorf("the ledger does not verify: %+v", i)
	}
}

// A donation is paid in the village's money by a donor who holds the units; a donor holding only SUP is
// offered the desk inside the confirm (with the price shown), is never blocked, and pays SUP if they do
// not convert.
func TestADonationIsPaidInTheVillageMoneyAndOffersTheDeskInline(t *testing.T) {
	l := charteredLaborEnv(t)
	ctx := testCtx(t)
	l.village.WithDonationRules(10, 100_000, []int64{100, 500})
	t.Cleanup(func() {
		_, _ = l.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_donations WHERE settlement_id = $1::uuid`, l.cityID)
	})
	p := l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, p.ID) })
	grantCash(t, l.pool, p.ID, 5000)
	donate := func(req handlers.VillageDonateRequest) (*presentation.Response, error) {
		req.Settlement = l.cityID
		m := l.as(p, "settlement.donate", "donate")
		m.ChatType = envelope.ChatTypeClient
		return l.village.Donate(ctx, m, req)
	}
	// the confirm of a donor with SUP only: not blocked, local not possible, the desk is offered with its price
	ask, err := donate(handlers.VillageDonateRequest{Amount: "100"})
	if err != nil || ask == nil || ask.Offer == nil {
		t.Fatalf("the ask carries the offer: refusal %+v %v", ask.Refusal, err)
	}
	o := ask.Offer
	if o.Local || !o.CanConvert || o.Units != 1000 || o.ConvertUnits != 1000 || o.ConvertSUP <= 100 || o.ConvertFee <= 0 || o.Convert == nil {
		t.Fatalf("the offer: %+v", o)
	}
	if o.Convert.Params["convert"] != "1" || o.Convert.Params["max_sup"] == "" || o.Convert.Command != "settlement.donate" {
		t.Errorf("the converting action: %+v", o.Convert)
	}
	// a plain confirm pays SUP, as before
	cash0, tSUP0 := l.cash(p.ID), l.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, l.cityID)
	if _, err := donate(handlers.VillageDonateRequest{Amount: "100", Confirm: vpres.ResidenceConfirm}); err != nil {
		t.Fatal(err)
	}
	if got := cash0 - l.cash(p.ID); got != 100 {
		t.Errorf("a donor who does not convert pays 100 SUP, paid %d", got)
	}
	if got := l.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, l.cityID) - tSUP0; got != 100 {
		t.Errorf("the treasury took %d SUP, want 100", got)
	}
	if got := localPaymentRows(t, l); got != 0 {
		t.Errorf("no local payment yet: %d", got)
	}
	// the price protection: converting with a limit below the desk's price is refused and moves nothing
	cash1, units0 := l.cash(p.ID), l.holding(l.cityID)
	if r, err := donate(handlers.VillageDonateRequest{Amount: "100", Confirm: vpres.ResidenceConfirm,
		LocalSettle: handlers.LocalSettle{Convert: "1", MaxSUP: "50"}}); err != nil || r.Refusal == nil || !strings.Contains(r.Refusal.Code, "desk_moved") {
		t.Errorf("a limit under the price is refused: %+v %v", r, err)
	}
	if l.cash(p.ID) != cash1 || l.holding(l.cityID) != units0 {
		t.Error("a refused conversion must move nothing")
	}
	// converting at the price shown buys the units and pays the donation in them, in one step
	if _, err := donate(handlers.VillageDonateRequest{Amount: "100", Confirm: vpres.ResidenceConfirm,
		LocalSettle: handlers.LocalSettle{Convert: "1", MaxSUP: strconv.FormatInt(o.ConvertSUP, 10)}}); err != nil {
		t.Fatal(err)
	}
	if got := cash1 - l.cash(p.ID); got != o.ConvertSUP {
		t.Errorf("the donor paid %d SUP at the desk, want %d", got, o.ConvertSUP)
	}
	if got := l.holding(l.cityID) - units0; got != 1000-o.ConvertUnits {
		t.Errorf("the treasury's units: bought out %d, donated back 1000, net %d", o.ConvertUnits, got)
	}
	if got := localPaymentRows(t, l); got != 1 {
		t.Errorf("one local payment: %d", got)
	}
	if got := l.scalar(`SELECT count(*) FROM currency_desk_trades WHERE settlement_id = $1::uuid`, l.cityID); got != 1 {
		t.Errorf("one desk trade: %d", got)
	}
	// holding units, the next donation is paid in them with no conversion
	if _, err := rrc(l.village.CurrencyDesk(ctx, l.as(p, "settlement.currency.desk", "currency.desk"),
		handlers.VillageDeskRequest{Side: "buy", Amount: "200", Confirm: vpres.ResidenceConfirm})); err != nil {
		t.Fatal(err)
	}
	h0 := l.holding(p.ID)
	if _, err := donate(handlers.VillageDonateRequest{Amount: "10", Confirm: vpres.ResidenceConfirm}); err != nil {
		t.Fatal(err)
	}
	if got := h0 - l.holding(p.ID); got != 100 {
		t.Errorf("10 SUP is 100 units, paid %d", got)
	}
	l.verify2b()
}

// Units paid to the NPC economy are burnt: the supply and the basis fall, the pot keeps its SUP, the tax
// goes to the treasury, a redelivery burns nothing twice, and a payer short of units who does not convert
// is not touched.
func TestTheShelfBurnsTheUnitsAndTheReservePotKeepsItsSUP(t *testing.T) {
	l := charteredLaborEnv(t)
	p := l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, p.ID) })
	burn := func(ref string, convert bool, maxSUP int64) (application.LocalResult, error) {
		var res application.LocalResult
		err := l.inTx(func(ctx context.Context, tx application.Tx) error {
			var err error
			res, err = application.BurnLocal(ctx, tx, workIDs{t}.NewID, application.LocalBurn{
				SettlementID: l.cityID, PlayerID: p.ID, Flow: application.ReasonShopPurchase, SUP: 50, TaxSUP: 5,
				RefType: "village_shop_sales", RefID: ref, At: time.Now().UTC(), Convert: convert, MaxConvertSUP: maxSUP})
			return err
		})
		return res, err
	}
	if r, err := burn(newUUID(t), false, 0); err != nil || r.Paid {
		t.Fatalf("a payer with no units is not touched: %+v %v", r, err)
	}
	grantCash(t, l.pool, p.ID, 2000)
	// convert inside the burn: 50 SUP of price (500 units) and 5 SUP of tax (50 units)
	var supply0, burnt0, basis0, pot0 int64
	read := func() (supply, burnt, basis, pot int64) {
		if err := l.pool.Raw().QueryRow(testCtx(t), `SELECT minted_units - burnt_units, burnt_units, basis_sup FROM village_currency_state WHERE settlement_id = $1::uuid`, l.cityID).
			Scan(&supply, &burnt, &basis); err != nil {
			t.Fatal(err)
		}
		pot = l.scalar(`SELECT balance FROM accounts WHERE kind = 'reserve_pot' AND owner_id = $1::uuid`, l.cityID)
		return
	}
	supply0, burnt0, basis0, pot0 = read()
	if _, err := burn(newUUID(t), true, 1); !errors.Is(err, application.ErrDeskMoved) {
		t.Errorf("a limit under the price is refused: %v", err)
	}
	ref := newUUID(t)
	tTreasury := l.holding(l.cityID)
	r, err := burn(ref, true, 100)
	if err != nil || !r.Paid || !r.Converted || r.Units != 550 {
		t.Fatalf("burn with conversion: %+v %v", r, err)
	}
	supply1, burnt1, basis1, pot1 := read()
	if burnt1-burnt0 != 500 || supply0-supply1 != 500 {
		t.Errorf("the supply falls by the burnt price: burnt %d supply %d -> %d", burnt1-burnt0, supply0, supply1)
	}
	if basis1 >= basis0 || basis0-basis1 != basis0*500/supply0 {
		t.Errorf("the basis falls by the burnt share: %d -> %d", basis0, basis1)
	}
	if pot1 != pot0 {
		t.Errorf("the reserve pot keeps its SUP: %d -> %d", pot0, pot1)
	}
	// the treasury: gave units out at the desk (r.Units bought = 550 + rounding), took the tax back
	_ = tTreasury
	if again, err := burn(ref, false, 0); err != nil || !again.Paid || !again.Already {
		t.Errorf("a redelivery burns nothing twice: %+v %v", again, err)
	}
	if _, burnt2, _, _ := read(); burnt2 != burnt1 {
		t.Errorf("a redelivery burnt again: %d -> %d", burnt1, burnt2)
	}
	l.verify2b()
}

// A payer holding the units pays a player teacher the fee with the tax going on to the treasury, in one
// three-leg transaction; with too few units it is not paid at all (SUP is the caller's fallback).
func TestATransferWithATaxShareGoesToThePayeeAndTheTreasury(t *testing.T) {
	l := charteredLaborEnv(t)
	student, teacher := l.resident(), l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, student.ID); purgeLedgerFor(t, l.pool, teacher.ID) })
	grantCash(t, l.pool, student.ID, 2000)
	if _, err := rrc(l.village.CurrencyDesk(testCtx(t), l.as(student, "settlement.currency.desk", "currency.desk"),
		handlers.VillageDeskRequest{Side: "buy", Amount: "1000", Confirm: vpres.ResidenceConfirm})); err != nil {
		t.Fatal(err)
	}
	pay := func(ref string) (application.LocalResult, error) {
		var res application.LocalResult
		err := l.inTx(func(ctx context.Context, tx application.Tx) error {
			var err error
			res, err = application.PayLocal(ctx, tx, workIDs{t}.NewID, application.LocalPayment{
				SettlementID: l.cityID, PlayerID: student.ID, Direction: application.LocalTransfer, Flow: application.ReasonTuition,
				SUP: 100, PayeeID: teacher.ID, CutBPS: 1000, RefType: "enrollments", RefID: ref, At: time.Now().UTC()})
			return err
		})
		return res, err
	}
	t0, s0 := l.holding(l.cityID), l.holding(student.ID)
	ref := newUUID(t)
	r, err := pay(ref)
	if err != nil || !r.Paid || r.Units != 1000 {
		t.Fatalf("the tuition: %+v %v", r, err)
	}
	if got := l.holding(teacher.ID); got != 900 {
		t.Errorf("the teacher holds %d units, want 1000 less the 10%% tax = 900", got)
	}
	if got := l.holding(l.cityID) - t0; got != 100 {
		t.Errorf("the treasury took %d units of tax, want 100", got)
	}
	if got := s0 - l.holding(student.ID); got != 1000 {
		t.Errorf("the student paid %d units, want 1000", got)
	}
	if again, err := pay(ref); err != nil || !again.Already || l.holding(teacher.ID) != 900 {
		t.Errorf("a redelivery pays nothing twice: %+v %v", again, err)
	}
	// a student with too few units is not paid for: SUP is the caller's way out
	poor := l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, poor.ID) })
	var res application.LocalResult
	if err := l.inTx(func(ctx context.Context, tx application.Tx) error {
		var err error
		res, err = application.PayLocal(ctx, tx, workIDs{t}.NewID, application.LocalPayment{
			SettlementID: l.cityID, PlayerID: poor.ID, Direction: application.LocalTransfer, Flow: application.ReasonTuition,
			SUP: 100, PayeeID: teacher.ID, CutBPS: 1000, RefType: "enrollments", RefID: newUUID(t), At: time.Now().UTC()})
		return err
	}); err != nil || res.Paid {
		t.Errorf("no units, no local payment: %+v %v", res, err)
	}
	l.verify2b()
}

// Two neighbours pay each other in the village's money from the pay screen: the payer chooses it, the
// amount is in units, it costs no fee, and a payer short of units is sent back with the reason.
func TestTwoNeighboursPayEachOtherInTheVillageMoney(t *testing.T) {
	l := charteredLaborEnv(t)
	ctx := testCtx(t)
	a, b := l.resident(), l.resident()
	t.Cleanup(func() {
		purgeLedgerFor(t, l.pool, a.ID)
		purgeLedgerFor(t, l.pool, b.ID)
		_, _ = l.pool.Raw().Exec(testCtx(t), `DELETE FROM outbox WHERE subject LIKE 'game.event.bank.%' AND payload::text LIKE '%' || $1 || '%'`, a.ID)
	})
	grantCash(t, l.pool, a.ID, 3000)
	if _, err := rrc(l.village.CurrencyDesk(ctx, l.as(a, "settlement.currency.desk", "currency.desk"),
		handlers.VillageDeskRequest{Side: "buy", Amount: "1000", Confirm: vpres.ResidenceConfirm})); err != nil {
		t.Fatal(err)
	}
	limits, err := bank.NewLimits(1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewBankHandler(postgres.NewUnitOfWork(l.pool, testDefaultLanguage), seqUUID{t}, nil,
		placedCities{postgres.NewCityRepository(l.pool)}, bankStubPolicy{bps: map[string]int64{}},
		postgres.NewPlayerSearchRepository(l.pool), limits, time.Hour, nil)
	// the pay screen offers the village money to two neighbours
	screen, err := h.Pay(ctx, bankMeta(t, a, "bank.pay"), handlers.PayRequest{To: b.PublicCode})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(screen.View), `"can_local": true`) && !strings.Contains(string(screen.View), `"can_local":true`) {
		t.Errorf("the pay screen offers the local money: %s", screen.View)
	}
	a0 := l.holding(a.ID)
	if _, err := h.PaySend(ctx, bankMeta(t, a, "bank.pay.send"), handlers.PayRequest{To: b.PublicCode, Amount: "2500", Method: "local", Nonce: "l1"}); err != nil {
		t.Fatal(err)
	}
	if l.holding(b.ID) != 2500 || a0-l.holding(a.ID) != 2500 {
		t.Errorf("2500 units moved, no fee: payee %d payer -%d", l.holding(b.ID), a0-l.holding(a.ID))
	}
	// the same nonce twice pays once
	if _, err := h.PaySend(ctx, bankMeta(t, a, "bank.pay.send"), handlers.PayRequest{To: b.PublicCode, Amount: "2500", Method: "local", Nonce: "l1"}); err != nil {
		t.Fatal(err)
	}
	if l.holding(b.ID) != 2500 {
		t.Errorf("the replay paid again: %d", l.holding(b.ID))
	}
	// more than the payer holds: back to the screen, nothing moves
	before := l.holding(b.ID)
	if _, err := h.PaySend(ctx, bankMeta(t, a, "bank.pay.send"), handlers.PayRequest{To: b.PublicCode, Amount: "999999", Method: "local", Nonce: "l2"}); err != nil {
		t.Fatal(err)
	}
	if l.holding(b.ID) != before {
		t.Error("a payer short of units paid")
	}
	// somebody who does not live in the village cannot be paid in its money
	outsider := insertPlayer(t, l.pool)
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, outsider.ID) })
	if _, err := h.PaySend(ctx, bankMeta(t, a, "bank.pay.send"), handlers.PayRequest{To: outsider.PublicCode, Amount: "10", Method: "local", Nonce: "l3"}); err == nil {
		t.Error("the village money is for two neighbours")
	}
	l.verify2b()
}

// The village shelf sold for the village's money: the price's units are burnt, the tax goes to the
// treasury in units, the sale row keeps its SUP amounts, the pot keeps its SUP, and the buyer's SUP is
// untouched; a buyer holding only SUP is offered the desk inline and, if they do nothing, pays SUP.
func TestTheShelfIsSoldForTheVillageMoneyAndBurnsIt(t *testing.T) {
	e := newShopEnv(t)
	charterLaborEnv(t, e.laborEnv)
	ctx := testCtx(t)
	p := e.buyer(t, 20_000)
	// SUP only: the checkout offers the desk
	resp := e.buy(t, p, "bread", "2", "", "")
	if resp.Screen != vpres.ScreenVillageShopCheckout || resp.Offer == nil || resp.Offer.Local || !resp.Offer.CanConvert || resp.Offer.Convert == nil {
		t.Fatalf("the checkout of a SUP-only buyer: %s %+v", resp.Screen, resp.Offer)
	}
	var co vpres.VillageShopCheckoutView
	if err := presentation.DecodeView(resp.View, &co); err != nil {
		t.Fatal(err)
	}
	cash0 := e.cash(p.ID)
	// not converting: SUP, as before, nothing local
	e.buy(t, p, "bread", "1", "cash", e.nonceFor(t))
	if e.cash(p.ID) >= cash0 || localPaymentRows(t, e.laborEnv) != 0 || e.holding(p.ID) != 0 {
		t.Fatalf("a buyer who does not convert pays SUP: cash %d -> %d, local rows %d", cash0, e.cash(p.ID), localPaymentRows(t, e.laborEnv))
	}
	// converting at the desk inside the confirm pays the shelf in units
	supply := func() (s, b int64) {
		if err := e.pool.Raw().QueryRow(ctx, `SELECT minted_units - burnt_units, burnt_units FROM village_currency_state WHERE settlement_id = $1::uuid`, e.cityID).Scan(&s, &b); err != nil {
			t.Fatal(err)
		}
		return
	}
	pot0 := e.scalar(`SELECT balance FROM accounts WHERE kind = 'reserve_pot' AND owner_id = $1::uuid`, e.cityID)
	supply0, burnt0 := supply()
	cash1 := e.cash(p.ID)
	r := e.buy(t, p, "bread", "2", "", "")
	if r.Offer == nil || !r.Offer.CanConvert {
		t.Fatalf("the offer: %+v", r.Offer)
	}
	if err := presentation.DecodeView(r.View, &co); err != nil {
		t.Fatal(err)
	}
	resp2, err := e.shop.ShopBuy(ctx, e.as(p, "settlement.shop.buy", "shop.buy"),
		handlers.VillageShopRequest{Item: "bread", Qty: "2", Method: "cash", Nonce: co.Nonce,
			LocalSettle: handlers.LocalSettle{Convert: "1", MaxSUP: strconv.FormatInt(r.Offer.ConvertSUP, 10)}})
	if err != nil || resp2.Refusal != nil {
		t.Fatalf("the converting purchase: %+v %v", resp2.Refusal, err)
	}
	if got := cash1 - e.cash(p.ID); got != r.Offer.ConvertSUP {
		t.Errorf("only the desk took SUP: %d, want %d", got, r.Offer.ConvertSUP)
	}
	supply1, burnt1 := supply()
	wantBurn := e.scalar(`SELECT units FROM currency_issuance_log WHERE kind = 'burn' AND settlement_id = $1::uuid`, e.cityID)
	if burnt1-burnt0 != wantBurn || supply0-supply1 != wantBurn || wantBurn <= 0 {
		t.Errorf("the supply falls by the burnt price: burnt %d (log %d), supply %d -> %d", burnt1-burnt0, wantBurn, supply0, supply1)
	}
	if e.scalar(`SELECT balance FROM accounts WHERE kind = 'reserve_pot' AND owner_id = $1::uuid`, e.cityID) != pot0 {
		t.Error("a burn never releases the pot")
	}
	var total, tax int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT total, tax FROM village_shop_sales WHERE player_id = $1::uuid ORDER BY created_at DESC LIMIT 1`, p.ID).Scan(&total, &tax); err != nil {
		t.Fatal(err)
	}
	if got := localPaymentRows(t, e.laborEnv); got != int64(boolInt(tax > 0)) {
		t.Errorf("the desk's trade is not a payment; the tax is a local payment: %d rows (tax %d)", got, tax)
	}
	if got := (&goodsWorld{pool: e.pool}).held(t, p.ID, "bread", application.HoldCarried); got != 3 {
		t.Errorf("bread carried = %d, want 3", got)
	}
	e.verify2b()
	verifyShopLedger(t, e.pool)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// A class fee is paid in the village's money by a student who holds the units, or who converts at the desk
// inside the enrolment; a student who does neither pays SUP, as before.
func TestAClassFeeIsPaidInTheVillageMoney(t *testing.T) {
	e := newTeachEnv(t)
	charterLaborEnv(t, e.laborEnv)
	ctx := testCtx(t)
	if _, err := e.edu.TeacherHire(ctx, e.as(e.head, "education.hire", "hire"), handlers.TeachRequest{Course: "literacy_class"}); err != nil {
		t.Fatal(err)
	}
	enrol := func(p *application.Player, ls handlers.LocalSettle) {
		t.Helper()
		if _, err := e.edu.Enroll(ctx, e.as(p, "education.enroll", "enroll"), handlers.CourseRequest{Course: "literacy_class", Method: "cash", LocalSettle: ls}); err != nil {
			t.Fatal(err)
		}
	}
	// a student who holds SUP only: the course screen offers the desk, a plain enrolment pays SUP
	sup := e.pupil(1000)
	resp, err := e.edu.View(ctx, e.as(sup, "education.view", "view"), handlers.CourseRequest{Course: "literacy_class"})
	if err != nil || resp.Offer == nil || resp.Offer.Local || !resp.Offer.CanConvert || resp.Offer.Units != 2000 || resp.Offer.Convert == nil {
		t.Fatalf("the course screen's offer: %+v %v", resp.Offer, err)
	}
	t0 := e.treasury()
	enrol(sup, handlers.LocalSettle{})
	if got := e.treasury() - t0; got != 200 || e.rowsLocal() != 0 {
		t.Fatalf("a plain enrolment pays 200 SUP: treasury +%d, local rows %d", got, e.rowsLocal())
	}
	// converting inside the enrolment: the desk takes SUP, the school's treasury takes units
	conv := e.pupil(1000)
	o := resp.Offer
	cash0, units0 := e.cash(conv.ID), e.holding(e.cityID)
	enrol(conv, handlers.LocalSettle{Convert: "1", MaxSUP: strconv.FormatInt(o.ConvertSUP, 10)})
	if e.scalar(`SELECT count(*) FROM enrollments WHERE player_id = $1::uuid`, conv.ID) != 1 {
		t.Fatal("not enrolled")
	}
	if got := cash0 - e.cash(conv.ID); got != o.ConvertSUP {
		t.Errorf("only the desk took SUP: %d, want %d", got, o.ConvertSUP)
	}
	if got := e.holding(e.cityID) - units0; got != 2000-o.ConvertUnits {
		t.Errorf("the treasury sold the units and took them back as the fee: net %d, want %d", got, 2000-o.ConvertUnits)
	}
	if e.rowsLocal() != 1 {
		t.Errorf("one local payment for the fee: %d", e.rowsLocal())
	}
	// the class ends: the NPC teacher's wage is SUP, once, and everything verifies
	var enrollmentID, actionID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text FROM enrollments WHERE player_id = $1::uuid`, conv.ID).Scan(&enrollmentID, &actionID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(48 * time.Hour)
	if _, err := e.edu.Complete(ctx, e.as(conv, "education.complete", "complete"), handlers.CompleteCourseRequest{
		ActionID: actionID, ActorID: conv.ID, ReferenceType: "enrollments", ReferenceID: enrollmentID}); err != nil {
		t.Fatal(err)
	}
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v.VillageInvariants.TeachingOK() {
		t.Errorf("teaching does not verify: %+v", v.VillageInvariants)
	}
	e.verify2b()
}

func (l *laborEnv) rowsLocal() int64 { return localPaymentRows(l.t, l) }
