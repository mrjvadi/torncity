package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/moneyvalue"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file is the settlement's money value panel (docs/adr/0046-bags-
// merchants-currency-exchange.md section 7, phase M3). It is a READING and a
// quote, never an act: it moves no money, opens no order and writes nothing
// (the only write on the way is the shop's morning delivery, which any look at
// the shop settles and which has its own fence). Nil never becomes settlement
// money (ADR 0029 10.4).
//
// # What is real, and what is not built (as built)
//
// A village's own currency is, today, a name, a code and a symbol it reserved
// at its founding (village_currency_reservations); it is not issued, has no
// reserve and no market (ADR 0033 sections 6 and 7, roadmap 2.19). So the panel
// does not invent a market rate, a reserve or a cover: those parts of the view
// are absent and the view says so (Market, Reserve = "none"). What the village
// really works in is the neutral currency, and for that the panel gives the
// figures the ledger and the shop really hold:
//
//   - the quote: nil_per_unit = (x_ref / r0) / nil_unit_sup, which for the neutral
//     currency (x_ref = r0) is 1 / nil_unit_sup per unit, and the worth in Nil of
//     round amounts, the treasury and the village's output;
//   - the treasury, from the ledger;
//   - the output: the goods the village's workplaces made in the last
//     merchant.output_days game days, at reference prices, from the finished
//     production shifts;
//   - the basket: the fixed list of the shop's tradable lines, read at the prices
//     the shop charges against the reference prices (ADR 0046 7.3: the basket
//     is what makes two villages' money comparable). The index is what the basket
//     costs here as a share of the reference.
//
// The quote function is moneyvalue.NilPerUnit, which already takes a market
// rate and a charter rate, so the day the settlement currency has a book the
// panel quotes it with the same formula.

// Money handles settlement.money: what the village's money is worth.
func (h *VillageHandler) Money(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	lang := meta.Language
	var view village.MoneyView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if view, err = h.moneyView(ctx, tx, s, h.now()); err != nil {
			return err
		}
		if view.CanCharter {
			// only the holder of currency.charter is offered the charter
			view.CanCharter, err = h.mayVillage(ctx, tx, s, p.ID, charter.CurrencyCharter)
		}
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.VillageMoney(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) moneyView(ctx context.Context, tx application.Tx, s application.FoundedSettlement, now time.Time) (village.MoneyView, error) {
	snap := h.content.Current()
	if !h.shop.enabled() || h.shop.NilUnitSup < 1 {
		return village.MoneyView{}, errors.Internal(stderrors.New("handlers: the money panel is not wired"))
	}
	if _, err := h.SettleShopDay(ctx, tx, s, now); err != nil {
		return village.MoneyView{}, err
	}
	st, err := h.shopStateOf(ctx, tx, snap, s)
	if err != nil {
		return village.MoneyView{}, err
	}
	nilPer, err := moneyvalue.NeutralNilPerUnit(h.shop.NilUnitSup)
	if err != nil {
		return village.MoneyView{}, err
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return village.MoneyView{}, err
	}
	cur := village.MoneyCurrency{Code: s.Currency.Code, Name: s.Currency.Name, Symbol: s.Currency.Symbol}
	var chartered *village.MoneyChartered
	canCharter := false
	if st, err := tx.Currency().State(ctx, s.CityID); err != nil {
		return village.MoneyView{}, err
	} else if st != nil {
		cur.Issued = true
		chartered = &village.MoneyChartered{R0: st.R0, XRefPPM: st.XRefPPM, Supply: st.Supply(), PotSUP: st.DepositedSUP - st.ReleasedSUP}
		if holding, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, s.CityID, st.Code); err == nil {
			chartered.TreasuryUnits = holding.Balance.Minor()
		}
	} else if res, err := tx.Currency().Reservation(ctx, s.CityID); err != nil {
		return village.MoneyView{}, err
	} else if res != nil && h.currencyRules.Enabled() {
		canCharter = true
	}
	nilOf := func(amount int64) int64 {
		v, err := moneyvalue.NilOf(amount, nilPer)
		if err != nil {
			return 0
		}
		return v
	}
	view := village.MoneyView{
		Village: s.Name, Market: village.MoneyNone, Reserve: village.MoneyNone,
		Currency: cur, Chartered: chartered, CanCharter: canCharter,
		NilUnitSup: h.shop.NilUnitSup, NilPerUnitMicro: nilPer,
		Treasury: treasury, TreasuryNilMicro: nilOf(treasury), Residents: st.residents, OutputDays: h.shop.OutputDays,
	}
	for _, a := range h.shop.NilExamples {
		view.Examples = append(view.Examples, village.NilExample{Amount: a, NilMicro: nilOf(a)})
	}

	// The output: what the workplaces made in the window, at reference prices.
	window := gametime.Scale(h.shop.Clock.Scale).RealWait(time.Duration(h.shop.OutputDays) * gametime.Day)
	made, err := tx.VillageShop().Produced(ctx, s.CityID, now.Add(-window))
	if err != nil {
		return view, err
	}
	for code, qty := range made {
		if ref, ok := snap.ReferencePrice(code); ok {
			view.Output += qty * ref
		}
	}
	view.OutputNilMicro = nilOf(view.Output)

	// The basket: the tradable lines of the shop, at what the shop charges now.
	capBPS, _, err := h.shopTerms(ctx, tx, s)
	if err != nil {
		return view, err
	}
	rows, err := tx.VillageShop().Lines(ctx, s.CityID)
	if err != nil {
		return view, err
	}
	stock := map[string]application.VillageShopLine{}
	for _, r := range rows {
		stock[r.Line] = r
	}
	open := map[string]shopOpenLine{}
	for _, ol := range st.open {
		open[ol.line.Code] = ol
	}
	var basket []moneyvalue.BasketLine
	for _, l := range st.def.Lines {
		if !l.Tradable {
			continue
		}
		rl, ok := snap.ShopLineRule(l)
		if !ok {
			continue
		}
		line := village.MoneyBasketLine{Item: h.shopNameOf(snap, l.Item), Kind: shopKindOf(snap, l.Item), WeekMilli: l.PerHeadMilli * 7, Reference: rl.Ref}
		if ol, here := open[l.Item]; here && stock[l.Item].Stock > 0 {
			line.OnShelf = true
			line.Price = st.rules.Price(ol.line, stock[l.Item].SoldToday, capBPS)
		}
		view.Basket = append(view.Basket, line)
		basket = append(basket, moneyvalue.BasketLine{WeightMilli: line.WeekMilli, Ref: line.Reference, Price: line.Price})
	}
	sort.SliceStable(view.Basket, func(i, j int) bool { return view.Basket[i].Item.Code < view.Basket[j].Item.Code })
	reading := moneyvalue.ReadBasket(basket)
	view.IndexBPS, view.CoverBPS = reading.IndexBPS, reading.CoverBPS
	return view, nil
}
