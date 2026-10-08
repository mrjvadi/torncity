//go:build integration

package tests

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
)

// The floating VC/SUP book (roadmap 2.19 phase 3, ADR 0033 6.8 to 6.10): orders match at the resting price,
// the offered side is escrowed and released, fills are priced by the cumulative rule, the reference rate moves
// only once the window holds enough fills, the circuit breaker follows it, a conversion through SUP is one
// confirm with price protection, and the verifier holds all of it.

type fxEnv struct {
	*laborEnv
	fx      *handlers.FXHandler
	rules   application.FXRules
	support string
	// books are the settlements whose books the test trades on.
	books []string
}

// closeOrders cancels every open order of the test's books, so the escrow is back with its owners before
// the players' ledgers are purged (a purge takes legs off other people's balances too).
func (e *fxEnv) closeOrders() {
	ctx := testCtx(e.t)
	for _, city := range e.books {
		rows, err := e.pool.Raw().Query(ctx, `SELECT id::text, owner_kind, owner_id::text FROM fx_orders WHERE settlement_id = $1::uuid AND status = 'open'`, city)
		if err != nil {
			e.t.Errorf("listing the open orders: %v", err)
			return
		}
		type open struct{ id, kind, owner string }
		var list []open
		for rows.Next() {
			var o open
			if rows.Scan(&o.id, &o.kind, &o.owner) == nil {
				list = append(list, o)
			}
		}
		rows.Close()
		for _, o := range list {
			if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
				_, err := application.CancelFX(ctx, tx, workIDs{e.t}.NewID, application.FXOwner{Kind: o.kind, ID: o.owner}, o.id, time.Now().UTC())
				return err
			}); err != nil {
				e.t.Errorf("closing an order: %v", err)
			}
		}
	}
}

func fxRulesForTests(supportID string) application.FXRules {
	return application.FXRules{
		ReserveFeeBPS: 30, MaxMoveBPS: 2000, MinTrades: 4, Window: 7, OrderTTL: 48 * time.Hour,
		UnitPresets: []int64{1000, 5000}, ConvertSlippageBPS: 100, Period: time.Hour, MinOrderSUP: 1, BookLimit: 50, MaxOpenOrders: 5,
		SupportCityID: supportID,
	}
}

// fxCleanup takes the book's rows of these settlements away (the fills and the history are append-only in
// production; tests are the only place they are removed).
func fxCleanup(t *testing.T, pool *postgres.Pool, cityIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE fx_trades DISABLE TRIGGER fx_trades_append_only`,
			`ALTER TABLE fx_rate_history DISABLE TRIGGER fx_rate_history_append_only`,
		} {
			_, _ = pool.Raw().Exec(c, stmt)
		}
		for _, id := range cityIDs {
			for _, stmt := range []string{
				`DELETE FROM fx_trades WHERE settlement_id = $1::uuid`,
				`DELETE FROM fx_orders WHERE settlement_id = $1::uuid`,
				`DELETE FROM fx_rate_history WHERE settlement_id = $1::uuid`,
			} {
				_, _ = pool.Raw().Exec(c, stmt, id)
			}
		}
		for _, stmt := range []string{
			`ALTER TABLE fx_trades ENABLE TRIGGER fx_trades_append_only`,
			`ALTER TABLE fx_rate_history ENABLE TRIGGER fx_rate_history_append_only`,
		} {
			_, _ = pool.Raw().Exec(c, stmt)
		}
	})
}

func newFXEnv(t *testing.T) *fxEnv {
	t.Helper()
	l := charteredLaborEnv(t)
	support := cityIDByCode(t, l.pool, "support")
	rules := fxRulesForTests(support)
	h := handlers.NewFXHandler(postgres.NewUnitOfWork(l.pool, testDefaultLanguage), workIDs{t}, postgres.NewCityRepository(l.pool), rules, "support", time.Hour, l.clock.Now)
	fxCleanup(t, l.pool, l.cityID)
	return &fxEnv{laborEnv: l, fx: h, rules: rules, support: support, books: []string{l.cityID}}
}

// trader is a resident with SUP and, when units > 0, that many units bought at the village desk.
func (e *fxEnv) trader(sup, units int64) *application.Player {
	e.t.Helper()
	p := e.resident()
	e.t.Cleanup(func() { purgeLedgerFor(e.t, e.pool, p.ID) })
	e.t.Cleanup(e.closeOrders) // registered after the purge, so it runs before it
	need := sup
	if units > 0 {
		need += units/10 + units/500 + 50 // the desk sells 10 units for a SUP less its fee
	}
	if need > 0 {
		grantCash(e.t, e.pool, p.ID, need)
	}
	if units > 0 {
		buy := units/10 + units/500 + 10
		if _, err := rrc(e.village.CurrencyDesk(testCtx(e.t), e.as(p, "settlement.currency.desk", "currency.desk"),
			handlers.VillageDeskRequest{Side: "buy", Amount: strconv.FormatInt(buy, 10), Confirm: vpres.ResidenceConfirm})); err != nil {
			e.t.Fatal(err)
		}
		if e.holding(p.ID) < units {
			e.t.Fatalf("the trader holds %d units, wanted %d", e.holding(p.ID), units)
		}
	}
	return p
}

func (e *fxEnv) place(p *application.Player, side string, units, price int64) *presentation.Response {
	e.t.Helper()
	r, err := e.fx.Place(testCtx(e.t), e.as(p, "fx.place", "place"), handlers.FXPlaceRequest{
		Settlement: e.cityID, Side: side, Units: strconv.FormatInt(units, 10), Price: strconv.FormatInt(price, 10), Confirm: economy.FXConfirm})
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *fxEnv) refusal(r *presentation.Response) string {
	if r == nil || r.Refusal == nil {
		return ""
	}
	return r.Refusal.Code
}

func (e *fxEnv) supportTreasury() int64 {
	return e.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, e.support)
}

func (e *fxEnv) escrow(owner, currency string) int64 {
	return e.scalar(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'fx_escrow' AND owner_id = $1::uuid AND currency = $2`, owner, currency)
}

func (e *fxEnv) verifyFX() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	i := v.VillageInvariants
	if !i.FXBook || i.FXEscrowMismatched != 0 || i.FXTradeMismatched != 0 || i.FXOrderMismatched != 0 || i.FXRateMismatched != 0 ||
		i.FXTradeLedgerSUP != i.FXTradeRowsSUP || i.FXTradeLedgerVC != i.FXTradeRowsVC || i.FXHistoryGuards != 2 {
		e.t.Errorf("the book does not verify: escrow %d trades %d/%d units %d/%d tradeMis %d orderMis %d rate %d guards %d", i.FXEscrowMismatched, i.FXTradeLedgerSUP, i.FXTradeRowsSUP, i.FXTradeLedgerVC, i.FXTradeRowsVC, i.FXTradeMismatched, i.FXOrderMismatched, i.FXRateMismatched, i.FXHistoryGuards)
	}
	e.verify2b()
}

// An order rests, a crossing order fills at the resting price, the fee comes out of the sold side, the
// offered side is escrowed and what an order does not use comes back.
func TestTheBookMatchesAtTheRestingPriceAndTheEscrowBalances(t *testing.T) {
	e := newFXEnv(t)
	seller := e.trader(0, 20_000)
	buyer := e.trader(2_000, 0)
	// the reference: 10 units a SUP at charter, x = 1.00: a unit is worth 0.1 SUP = 100000 micro-SUP
	book, err := e.fx.Book(testCtx(t), e.as(seller, "fx.book", "book"), handlers.FXBookRequest{Settlement: e.cityID})
	if err != nil {
		t.Fatal(err)
	}
	var bv economy.FXBookView
	if err := presentation.DecodeView(book.View, &bv); err != nil {
		t.Fatal(err)
	}
	if bv.RefPrice != 100_000 || bv.BandLow != 80_000 || bv.BandHigh != 120_000 || bv.ReserveFeeBPS != 30 || bv.VillageFeeBPS != 30 {
		t.Fatalf("the book's terms: %+v", bv)
	}
	// the ask of a sell: nothing moves
	ask, err := e.fx.Place(testCtx(t), e.as(seller, "fx.place", "place"), handlers.FXPlaceRequest{Settlement: e.cityID, Side: "sell", Units: "10000", Price: "100000"})
	var ov economy.FXOrderView
	if err != nil || presentation.DecodeView(ask.View, &ov) != nil || ov.Stage != economy.FXOrderAsk || ov.Escrow != 10_000 || !ov.CanPlace || ov.WorthSUP != 1000 {
		t.Fatalf("the ask: %+v %v", ov, err)
	}
	h0 := e.holding(seller.ID)
	if r := e.place(seller, "sell", 10_000, 100_000); e.refusal(r) != "" {
		t.Fatalf("placing the sell: %s", e.refusal(r))
	}
	if h0-e.holding(seller.ID) != 10_000 || e.escrow(seller.ID, mustCode(t, e.laborEnv)) != 10_000 {
		t.Errorf("the units are set aside: holding -%d, escrow %d", h0-e.holding(seller.ID), e.escrow(seller.ID, mustCode(t, e.laborEnv)))
	}
	// a buyer at a price above the circuit breaker is refused, and so is one below
	if got := e.refusal(e.place(buyer, "buy", 1000, 130_000)); got != "fx_out_of_band" {
		t.Errorf("a price over the band: %q", got)
	}
	if got := e.refusal(e.place(buyer, "buy", 1000, 79_999)); got != "fx_out_of_band" {
		t.Errorf("a price under the band: %q", got)
	}
	// 4000 units wanted up to 110000: fills at the resting 100000
	cash0, sellerCash0, sup0, village0, vh0 := e.cash(buyer.ID), e.cash(seller.ID), e.supportTreasury(), e.holding(e.cityID), e.holding(buyer.ID)
	if r := e.place(buyer, "buy", 4000, 110_000); e.refusal(r) != "" {
		t.Fatalf("placing the buy: %s", e.refusal(r))
	}
	if got := cash0 - e.cash(buyer.ID); got != 402 {
		t.Errorf("the buyer paid %d SUP, want 400 and the 2 of the fee", got)
	}
	if got := e.cash(seller.ID) - sellerCash0; got != 400 {
		t.Errorf("the seller received %d SUP, want 400", got)
	}
	if got := e.supportTreasury() - sup0; got != 2 {
		t.Errorf("Support's treasury took %d, want the fee 2", got)
	}
	if got := e.holding(buyer.ID) - vh0; got != 3988 {
		t.Errorf("the buyer received %d units, want 4000 less the village's 12", got)
	}
	if got := e.holding(e.cityID) - village0; got != 12 {
		t.Errorf("the village treasury took %d units, want 12", got)
	}
	if e.escrow(buyer.ID, "SUP") != 0 {
		t.Errorf("a filled buy holds nothing: %d", e.escrow(buyer.ID, "SUP"))
	}
	if e.escrow(seller.ID, mustCode(t, e.laborEnv)) != 6000 {
		t.Errorf("the rest of the sell is still set aside: %d", e.escrow(seller.ID, mustCode(t, e.laborEnv)))
	}
	e.verifyFX()

	// cancelling gives the rest back; a second cancel does nothing
	var orderID string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM fx_orders WHERE owner_id = $1::uuid AND status = 'open'`, seller.ID).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if r, err := e.fx.Cancel(testCtx(t), e.as(buyer, "fx.cancel", "cancel"), handlers.FXCancelRequest{Order: orderID}); err != nil || e.refusal(r) != "fx_not_yours" {
		t.Errorf("somebody else's order: %v %v", err, e.refusal(r))
	}
	h1 := e.holding(seller.ID)
	for range 2 {
		if r, err := e.fx.Cancel(testCtx(t), e.as(seller, "fx.cancel", "cancel"), handlers.FXCancelRequest{Order: orderID}); err != nil || e.refusal(r) != "" {
			t.Fatalf("cancelling: %v %v", err, e.refusal(r))
		}
	}
	if e.holding(seller.ID)-h1 != 6000 || e.escrow(seller.ID, mustCode(t, e.laborEnv)) != 0 {
		t.Errorf("the rest came back once: +%d, escrow %d", e.holding(seller.ID)-h1, e.escrow(seller.ID, mustCode(t, e.laborEnv)))
	}
	e.verifyFX()
}

// Orders fill by price then time, partially; one order never trades with its own owner; the rounding of a
// buy never costs more than it escrowed.
func TestPartialFillsGoByPriceAndTheCumulativeRounding(t *testing.T) {
	e := newFXEnv(t)
	s1, s2 := e.trader(0, 3000), e.trader(0, 3000)
	buyer := e.trader(3_000, 0)
	e.place(s1, "sell", 1000, 100_000)
	e.place(s2, "sell", 1000, 95_000)
	c1, c2, bc := e.cash(s1.ID), e.cash(s2.ID), e.cash(buyer.ID)
	if r := e.place(buyer, "buy", 1500, 105_000); e.refusal(r) != "" {
		t.Fatal(e.refusal(r))
	}
	// 1000 at 95000 is 95 SUP, 500 at 100000 is 50: 145 in all, and the fee of the total, 2 (ceil 0.435 = 1)
	if got := e.cash(s2.ID) - c2; got != 95 {
		t.Errorf("the cheaper seller was paid first and in full: %d", got)
	}
	if got := e.cash(s1.ID) - c1; got != 50 {
		t.Errorf("the dearer seller was filled by half: %d", got)
	}
	if got := bc - e.cash(buyer.ID); got != 146 {
		t.Errorf("the buyer paid %d, want 145 and a fee of 1", got)
	}
	var left int64
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT quantity - filled FROM fx_orders WHERE owner_id = $1::uuid AND status = 'open'`, s1.ID).Scan(&left); err != nil || left != 500 {
		t.Errorf("the dearer seller's order rests with %d: %v", left, err)
	}
	// a buy that would meet the owner's own sell cancels the resting order instead of trading with itself
	own := e.place(s1, "buy", 500, 100_000)
	if e.refusal(own) != "" {
		t.Fatal(e.refusal(own))
	}
	if n := e.scalar(`SELECT count(*) FROM fx_trades WHERE buyer_id = seller_id`); n != 0 {
		t.Errorf("a trade with oneself: %d", n)
	}
	if got := e.scalar(`SELECT count(*) FROM fx_orders WHERE owner_id = $1::uuid AND side = 'sell' AND status = 'cancelled'`, s1.ID); got != 1 {
		t.Errorf("the resting sell was cancelled by the self-trade rule: %d", got)
	}
	e.verifyFX()
}

// The reference rate stays until the window holds enough fills, then moves a window's share of the gap
// toward the traded price; the desk and the circuit breaker follow it; the history is appended once.
func TestTheReferenceRateMovesOnlyOnceEnoughTradesHappen(t *testing.T) {
	e := newFXEnv(t)
	ctx := testCtx(t)
	seller := e.trader(0, 20_000)
	buyer := e.trader(3_000, 0)
	e.place(seller, "sell", 10_000, 80_000)
	start := e.clock.Now()
	for range 3 {
		if r := e.place(buyer, "buy", 1000, 85_000); e.refusal(r) != "" {
			t.Fatal(e.refusal(r))
		}
	}
	e.clock.Advance(time.Hour)
	settle := func(period int64, from, to time.Time) (bool, int64) {
		var fresh bool
		var x int64
		if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
			var err error
			fresh, x, err = application.SettleFXRate(ctx, tx, e.rules, e.cityID, period, from, to, to)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return fresh, x
	}
	mid := e.clock.Now()
	if fresh, x := settle(1, start, mid); !fresh || x != 1_000_000 {
		t.Errorf("three fills are under the minimum of four: the reference stays: %v %d", fresh, x)
	}
	if fresh, _ := settle(1, start, mid); fresh {
		t.Error("a period has one reading: the repeat writes nothing")
	}
	// one more fill: four in the window; a window of seven periods moves a seventh of the gap per period of
	// trading at 0.80: (5 x 1.00 + 0.80 + 0.80) / 7 = 0.942857
	if r := e.place(buyer, "buy", 1000, 85_000); e.refusal(r) != "" {
		t.Fatal(e.refusal(r))
	}
	e.clock.Advance(time.Hour)
	end := e.clock.Now()
	fresh, x := settle(2, mid, end)
	if !fresh || x != 942_857 {
		t.Fatalf("the reference moved to %d (fresh %v), want 942857", x, fresh)
	}
	var stored int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT x_ref_ppm FROM village_currency_state WHERE settlement_id = $1::uuid`, e.cityID).Scan(&stored); err != nil || stored != x {
		t.Errorf("the money carries the new reference: %d %v", stored, err)
	}
	if n := e.scalar(`SELECT count(*) FROM fx_rate_history WHERE settlement_id = $1::uuid`, e.cityID); n != 2 {
		t.Errorf("two readings in the history: %d", n)
	}
	// the desk reads the new reference
	desk, err := e.village.CurrencyDesk(ctx, e.as(buyer, "settlement.currency.desk", "currency.desk"), handlers.VillageDeskRequest{})
	var dv vpres.DeskView
	if err != nil || presentation.DecodeView(desk.View, &dv) != nil || dv.XRefPPM != 942_857 {
		t.Errorf("the desk shows x_ref %d: %v", dv.XRefPPM, err)
	}
	// and so does the circuit breaker: 120000 was inside the old band, it is over the new one (94286 + 20%)
	if got := e.refusal(e.place(buyer, "buy", 1000, 120_000)); got != "fx_out_of_band" {
		t.Errorf("the band follows the reference: %q", got)
	}
	// the history cannot be rewritten
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE fx_rate_history SET x_ref_after = 1 WHERE settlement_id = $1::uuid`, e.cityID); err == nil {
		t.Error("the history is append-only")
	}
	hv, err := e.fx.History(ctx, e.as(buyer, "fx.history", "history"), handlers.FXBookRequest{Settlement: e.cityID})
	var hist economy.FXHistoryView
	if err != nil || presentation.DecodeView(hv.View, &hist) != nil || len(hist.Periods) != 2 || hist.Periods[0].XRefAfter != 942_857 || hist.Periods[0].PeriodNo != 2 {
		t.Errorf("the history screen: %+v %v", hist.Periods, err)
	}
	e.verifyFX()
}

// A sale of units for SUP, a purchase with SUP, and a conversion between two villages' moneys through SUP:
// one confirm, price protection, nothing moved when the book cannot give what was quoted.
func TestAConversionThroughSUPIsOneConfirmWithPriceProtection(t *testing.T) {
	e := newFXEnv(t)
	ctx := testCtx(t)
	// a second village with its own money
	meta2, _ := e.group(t)
	foundVillage(t, e.pool, e.h, meta2)
	var city2 string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta2.TelegramChatID).Scan(&city2); err != nil {
		t.Fatal(err)
	}
	seedTreasury(t, e.pool, city2, 60_000)
	currencyCleanup(t, e.pool, city2)
	fxCleanup(t, e.pool, city2)
	e.books = append(e.books, city2)
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, city2) })
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		res, err := application.AutoCharter(ctx, tx, workIDs{t}.NewID, localRules(), city2, false, "test", time.Now().UTC())
		if err != nil || !res.Done {
			t.Fatalf("chartering the second village: %+v %v", res, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code1 := mustCode(t, e.laborEnv)
	var code2 string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT currency_code FROM village_currency_state WHERE settlement_id = $1::uuid`, city2).Scan(&code2); err != nil {
		t.Fatal(err)
	}
	// a market maker on each book: bids for the first money, asks for the second
	maker := e.trader(20_000, 0)
	e.place(maker, "buy", 20_000, 100_000)
	maker2 := e.trader(0, 0)
	grantUnits(t, e.pool, maker2.ID, city2, code2, 30_000)
	if r, err := e.fx.Place(ctx, e.as(maker2, "fx.place", "place"), handlers.FXPlaceRequest{Settlement: city2, Side: "sell", Units: "30000", Price: "100000", Confirm: economy.FXConfirm}); err != nil || e.refusal(r) != "" {
		t.Fatalf("the second book's ask: %v %s", err, e.refusal(r))
	}
	traveller := e.trader(0, 5000)
	convert := func(from, to string, amount int64, quote string, confirm bool) (economy.FXConvertView, string) {
		t.Helper()
		req := handlers.FXConvertRequest{From: from, To: to, Amount: strconv.FormatInt(amount, 10), Quote: quote}
		if confirm {
			req.Confirm = economy.FXConfirm
		}
		r, err := e.fx.Convert(ctx, e.as(traveller, "fx.convert", "convert"), req)
		if err != nil {
			t.Fatal(err)
		}
		var v economy.FXConvertView
		if r.Refusal == nil {
			if err := presentation.DecodeView(r.View, &v); err != nil {
				t.Fatal(err)
			}
		}
		return v, e.refusal(r)
	}
	// units of the first money to units of the second, through SUP: two legs, two fees
	ask, ref := convert(code1, code2, 5000, "", false)
	if ref != "" || ask.Stage != economy.FXConvertAsk || !ask.Complete || len(ask.Legs) != 2 || ask.Out <= 0 {
		t.Fatalf("the quote: %+v %s", ask, ref)
	}
	// the sale brings 500 SUP; the purchase of the second money with it: 500 less the fee (1), 10 units a SUP
	if ask.Out < 4900 || ask.Out > 5000 {
		t.Errorf("about 5000 units of the second money for 5000 of the first: %d", ask.Out)
	}
	units1, units2, cash := holdingOf(t, e.pool, traveller.ID, code1), holdingOf(t, e.pool, traveller.ID, code2), e.cash(traveller.ID)
	// price protection: the book changes (the only bid is cancelled) between the quote and the confirm
	var bid string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM fx_orders WHERE owner_id = $1::uuid AND status = 'open'`, maker.ID).Scan(&bid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.fx.Cancel(ctx, e.as(maker, "fx.cancel", "cancel"), handlers.FXCancelRequest{Order: bid}); err != nil {
		t.Fatal(err)
	}
	if _, ref := convert(code1, code2, 5000, strconv.FormatInt(ask.Out, 10), true); ref == "" {
		t.Error("with no bid the conversion is refused")
	}
	if holdingOf(t, e.pool, traveller.ID, code1) != units1 || holdingOf(t, e.pool, traveller.ID, code2) != units2 || e.cash(traveller.ID) != cash {
		t.Error("a refused conversion moved nothing")
	}
	// the bid comes back, and the conversion goes through at the price shown
	e.place(maker, "buy", 20_000, 100_000)
	done, ref := convert(code1, code2, 5000, strconv.FormatInt(ask.Out, 10), true)
	if ref != "" || done.Stage != economy.FXConvertDone || done.Got < done.Gave*98/100 {
		t.Fatalf("the conversion: %+v %s", done, ref)
	}
	if units1-holdingOf(t, e.pool, traveller.ID, code1) != 5000 {
		t.Errorf("5000 units of the first money left: %d", units1-holdingOf(t, e.pool, traveller.ID, code1))
	}
	if got := holdingOf(t, e.pool, traveller.ID, code2) - units2; got != done.Got {
		t.Errorf("the second money arrived: %d, said %d", got, done.Got)
	}
	// SUP is only passed through: the traveller's SUP did not change
	if e.cash(traveller.ID) != cash {
		t.Errorf("SUP went through and came out: %d -> %d", cash, e.cash(traveller.ID))
	}
	// a plain sale for SUP and a plain purchase with SUP
	if v, ref := convert(code2, "SUP", 1000, "", false); ref != "" || len(v.Legs) != 1 {
		t.Errorf("selling to SUP has one leg: %+v %s", v, ref)
	}
	e.verifyFX()
	_ = strings.TrimSpace
}

func grantUnits(t *testing.T, pool *postgres.Pool, playerID, cityID, code string, units int64) {
	t.Helper()
	ctx := testCtx(t)
	if err := postgres.NewUnitOfWork(pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		from, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, cityID, code)
		if err != nil {
			return err
		}
		to, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, playerID, code)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonFXDeskLocal, Entries: []application.LedgerEntry{
			{AccountID: from.ID, Amount: moneyFromMinor(-units)}, {AccountID: to.ID, Amount: moneyFromMinor(units)}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { purgeLedgerFor(t, pool, playerID) })
}

func holdingOf(t *testing.T, pool *postgres.Pool, owner, code string) int64 {
	t.Helper()
	var n int64
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'foreign_holding' AND owner_id = $1::uuid AND currency = $2`, owner, code).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The book's period ends once, whoever delivers it: it expires the orders past their time, writes each
// money's reading and schedules the next period; a second delivery of the same period does nothing.
func TestThePeriodClockClosesEachPeriodOnceAndExpiresOrders(t *testing.T) {
	e := newFXEnv(t)
	ctx := testCtx(t)
	t.Cleanup(func() {
		c := testCtx(t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM game_actions WHERE action_type = 'fx_period'`)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM fx_clock`)
	})
	seller := e.trader(0, 5000)
	if r := e.place(seller, "sell", 1000, 100_000); e.refusal(r) != "" {
		t.Fatal(e.refusal(r))
	}
	if err := e.fx.StartClock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.fx.StartClock(ctx); err != nil { // idempotent: a second start schedules nothing
		t.Fatal(err)
	}
	if n := e.scalar(`SELECT count(*) FROM game_actions WHERE action_type = 'fx_period' AND status = 'scheduled'`); n != 1 {
		t.Fatalf("one scheduled period: %d", n)
	}
	var actionID string
	var payload []byte
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text, payload FROM game_actions WHERE action_type = 'fx_period' ORDER BY started_at DESC LIMIT 1`).Scan(&actionID, &payload); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(49 * time.Hour) // the order's life (48h) and the period (1h) are over
	req := handlers.CrimeScheduledRequest{ActionID: actionID, Payload: payload}
	deliver := func() {
		t.Helper()
		if _, err := e.fx.Settle(ctx, e.as(seller, "fx.settle", "settle"), req); err != nil {
			t.Fatal(err)
		}
	}
	deliver()
	deliver() // another replica's delivery of the same period
	if got := e.scalar(`SELECT period_no FROM fx_clock WHERE id = 1`); got != 2 {
		t.Errorf("the clock is at period %d, want 2", got)
	}
	if n := e.scalar(`SELECT count(*) FROM fx_rate_history WHERE settlement_id = $1::uuid`, e.cityID); n != 1 {
		t.Errorf("one reading for period 1: %d", n)
	}
	if n := e.scalar(`SELECT count(*) FROM fx_orders WHERE owner_id = $1::uuid AND status = 'expired' AND escrow_left = 0`, seller.ID); n != 1 {
		t.Errorf("the order expired and let go of its escrow: %d", n)
	}
	if e.escrow(seller.ID, mustCode(t, e.laborEnv)) != 0 {
		t.Error("the escrow is back")
	}
	if n := e.scalar(`SELECT count(*) FROM game_actions WHERE action_type = 'fx_period' AND status = 'scheduled' AND id <> $1::uuid`, actionID); n != 1 {
		t.Errorf("the next period is scheduled once: %d", n)
	}
	e.verifyFX()
}

// A booted service builds the book from config, which names Support by its code only: the clock must still
// start and an order must still be taken (the live boot of 2026-10-08 left both off).
func TestTheBookWorksWhenSupportIsNamedOnlyByItsCode(t *testing.T) {
	e := newFXEnv(t)
	ctx := testCtx(t)
	t.Cleanup(func() {
		c := testCtx(t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM game_actions WHERE action_type = 'fx_period'`)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM fx_clock`)
	})
	_, _ = e.pool.Raw().Exec(ctx, `DELETE FROM game_actions WHERE action_type = 'fx_period'`)
	_, _ = e.pool.Raw().Exec(ctx, `DELETE FROM fx_clock`)
	booted := fxRulesForTests("")
	e.fx = handlers.NewFXHandler(postgres.NewUnitOfWork(e.pool, testDefaultLanguage), workIDs{t}, postgres.NewCityRepository(e.pool), booted, "support", time.Hour, e.clock.Now)
	if err := e.fx.StartClock(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.scalar(`SELECT count(*) FROM game_actions WHERE action_type = 'fx_period' AND status = 'scheduled'`); n != 1 {
		t.Fatalf("the clock should start from the code alone: %d periods scheduled", n)
	}
	seller := e.trader(0, 5000)
	if r := e.place(seller, "sell", 1000, 100_000); e.refusal(r) != "" {
		t.Fatalf("an order should be taken: %s", e.refusal(r))
	}
}
