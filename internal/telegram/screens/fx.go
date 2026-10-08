package screens

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The floating VC/SUP book (docs/adr/0033 6.8 to 6.10; roadmap 2.19 phase 3), drawn for Telegram: the book with
// its depth and the player's orders, the order to confirm, the rate history and the conversion. The player
// types nothing here: the book offers orders at the reference price in preset sizes, and the web has the
// full form.

// Screens of the book.
const (
	ScreenFXBook    = economy.ScreenFXBook
	ScreenFXOrder   = economy.ScreenFXOrder
	ScreenFXHistory = economy.ScreenFXHistory
	ScreenFXConvert = economy.ScreenFXConvert
	ScreenFXRefusal = economy.ScreenFXRefusal
)

// fxPrice writes a price (micro-SUP per unit) as SUP per unit, up to four decimals.
func fxPrice(c Context, micro int64) string {
	whole, frac := micro/economy.FXPriceScale, micro%economy.FXPriceScale
	s := strconv.FormatInt(whole, 10)
	if frac > 0 {
		d := strconv.FormatInt(frac+economy.FXPriceScale, 10)[1:] // six digits
		d = strings.TrimRight(d[:4], "0")
		if d == "" {
			d = "0001" // below a ten-thousandth of a SUP reads as the least there is
		}
		s += "." + d
	}
	return c.numerals().localise(s)
}

func fxRate(c Context, r0, xref int64) string { return liveRate(c, r0, xref) }

// FXBook renders the book screen.
func FXBook(c Context, v economy.FXBookView) *presenter.Response {
	return c.withView(renderFXBook(c, v), ScreenFXBook, v).MarkPrivate()
}

func renderFXBook(c Context, v economy.FXBookView) *presenter.Response {
	a := map[string]any{"name": v.Name, "village": v.Village}
	lines := []string{c.T("fx.book.title", a)}
	if v.Notice != "" {
		lines = append(lines, c.T("fx.book.notice."+v.Notice, nil))
	}
	last := c.T("fx.book.no_trade", nil)
	if v.LastPrice > 0 {
		last = fxPrice(c, v.LastPrice)
	}
	lines = append(lines, body(
		c.T("fx.book.rate", map[string]any{"name": v.Name, "rate": fxRate(c, v.R0, v.XRefPPM), "price": fxPrice(c, v.RefPrice), "last": last}),
		c.T("fx.book.band", map[string]any{"lo": fxPrice(c, v.BandLow), "hi": fxPrice(c, v.BandHigh)}),
		c.T("fx.book.fees", map[string]any{"reserve": PercentFromBPS(c, int(v.ReserveFeeBPS)), "village": PercentFromBPS(c, int(v.VillageFeeBPS)), "name": v.Name}),
	))
	side := func(key string, levels []economy.FXLevel) string {
		if len(levels) == 0 {
			return c.T(key+"_none", nil)
		}
		out := []string{c.T(key, nil)}
		for i, l := range levels {
			if i == 5 {
				break
			}
			out = append(out, c.T("fx.book.level", map[string]any{"units": FormatNumber(c, l.Units), "name": v.Name, "price": fxPrice(c, l.Price)}))
		}
		return body(out...)
	}
	lines = append(lines, side("fx.book.asks", v.Asks), side("fx.book.bids", v.Bids))
	if len(v.MyOrders) > 0 {
		out := []string{c.T("fx.book.mine", nil)}
		for _, o := range v.MyOrders {
			if o.Status != economy.FXStatusOpen {
				continue
			}
			out = append(out, c.T("fx.book.my_order", map[string]any{"no": FormatNumber(c, o.No), "side": c.T("fx.side."+o.Side, nil),
				"units": FormatNumber(c, o.Units-o.Filled), "name": v.Name, "price": fxPrice(c, o.Price)}))
		}
		if len(out) > 1 {
			lines = append(lines, body(out...))
		}
	}
	lines = append(lines, c.T("fx.book.balance", map[string]any{"sup": FormatMoney0(c, v.CashSUP), "units": FormatNumber(c, v.Units), "name": v.Name}))
	kb := keyboards.New()
	for _, u := range v.PresetUnits {
		var row []presenter.Button
		if b, ok := keyboards.Button(c.T("fx.button.buy", map[string]any{"units": FormatNumber(c, u), "name": v.Name}), economy.AddrFXPlace, "", "buy", itoa64(u), itoa64(v.RefPrice)); ok {
			row = append(row, b)
		}
		if b, ok := keyboards.Button(c.T("fx.button.sell", map[string]any{"units": FormatNumber(c, u), "name": v.Name}), economy.AddrFXPlace, "", "sell", itoa64(u), itoa64(v.RefPrice)); ok {
			row = append(row, b)
		}
		kb.Row(row...)
	}
	for _, o := range v.MyOrders {
		if o.Status != economy.FXStatusOpen {
			continue
		}
		if b, ok := keyboards.Button(c.T("fx.button.cancel", map[string]any{"no": FormatNumber(c, o.No)}), economy.AddrFXCancel, o.ID); ok {
			kb.Row(b)
		}
	}
	var row []presenter.Button
	if b, ok := keyboards.Button(c.T("fx.button.history", nil), economy.AddrFXHistory); ok {
		row = append(row, b)
	}
	if b, ok := keyboards.Button(c.T("fx.button.convert", nil), economy.AddrFXConvert); ok {
		row = append(row, b)
	}
	kb.Row(row...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank, RefreshData: economy.AddrFXBook}))
	return c.respond(paragraphs(lines...), kb.Build())
}

// FXOrder renders the order screen.
func FXOrder(c Context, v economy.FXOrderView) *presenter.Response {
	return c.withView(renderFXOrder(c, v), ScreenFXOrder, v).MarkPrivate()
}

func renderFXOrder(c Context, v economy.FXOrderView) *presenter.Response {
	a := map[string]any{"name": v.Name, "units": FormatNumber(c, v.Units), "price": fxPrice(c, v.Price), "side": c.T("fx.side."+v.Side, nil),
		"worth": FormatMoney0(c, v.WorthSUP), "fee": PercentFromBPS(c, int(v.FeeBPS)), "lo": fxPrice(c, v.BandLow), "hi": fxPrice(c, v.BandHigh)}
	kb := keyboards.New()
	if v.Stage == economy.FXOrderAsk {
		escrow := FormatMoney0(c, v.Escrow)
		if v.Side == economy.FXSideSell {
			escrow = FormatUnits(c, v.Escrow, v.Name)
		}
		a["escrow"] = escrow
		lines := []string{c.T("fx.order.title", a), c.T("fx.order.ask", a), c.T("fx.order.escrow", a)}
		if v.Crosses {
			lines = append(lines, c.T("fx.order.crosses", nil))
		}
		if v.CanPlace {
			if b, ok := keyboards.Button(c.T("fx.button.confirm", nil), economy.AddrFXPlace, "", v.Side, itoa64(v.Units), itoa64(v.Price), economy.FXConfirm); ok {
				kb.Row(b)
			}
		} else {
			lines = append(lines, c.T("fx.order.cannot", nil))
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: economy.AddrFXBook, RefreshData: economy.AddrFXBook}))
		return c.respond(paragraphs(lines...), kb.Build())
	}
	a["filled"] = FormatNumber(c, v.Order.Filled)
	a["moved"] = FormatNumber(c, v.UnitsMoved)
	a["sup"] = FormatMoney0(c, v.SUPMoved)
	lines := []string{c.T("fx.order.title", a)}
	switch {
	case v.Order.Filled == 0 && v.Rested:
		lines = append(lines, c.T("fx.order.rests", a))
	case v.Rested:
		lines = append(lines, c.T("fx.order.part", a))
	default:
		lines = append(lines, c.T("fx.order.filled", a))
	}
	lines = append(lines, c.T("fx.book.balance", map[string]any{"sup": FormatMoney0(c, v.CashSUP), "units": FormatNumber(c, v.UnitsHeld), "name": v.Name}))
	kb.Nav(c.nav(keyboards.Nav{BackData: economy.AddrFXBook, RefreshData: economy.AddrFXBook}))
	return c.respond(paragraphs(lines...), kb.Build())
}

// FXHistory renders the rate history.
func FXHistory(c Context, v economy.FXHistoryView) *presenter.Response {
	return c.withView(renderFXHistory(c, v), ScreenFXHistory, v)
}

func renderFXHistory(c Context, v economy.FXHistoryView) *presenter.Response {
	a := map[string]any{"name": v.Name, "village": v.Village}
	lines := []string{c.T("fx.history.title", a),
		c.T("fx.history.now", map[string]any{"name": v.Name, "rate": fxRate(c, v.R0, v.XRefPPM)}),
		c.T("fx.history.rule", map[string]any{"min": FormatNumber(c, v.MinTrades), "window": FormatNumber(c, v.WindowPeriods)})}
	if len(v.Periods) == 0 {
		lines = append(lines, c.T("fx.history.none", nil))
	}
	for i, p := range v.Periods {
		if i == 10 {
			break
		}
		lines = append(lines, c.T("fx.history.line", map[string]any{"no": FormatNumber(c, p.PeriodNo), "trades": FormatNumber(c, p.Trades),
			"units": FormatNumber(c, p.VolumeUnits), "name": v.Name, "price": fxPrice(c, p.VWAP),
			"before": fxRate(c, v.R0, p.XRefBefore), "after": fxRate(c, v.R0, p.XRefAfter)}))
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: economy.AddrFXBook, RefreshData: economy.AddrFXHistory}))
	return c.respond(paragraphs(lines...), kb.Build())
}

// FXConvert renders the conversion screen.
func FXConvert(c Context, v economy.FXConvertView) *presenter.Response {
	return c.withView(renderFXConvert(c, v), ScreenFXConvert, v).MarkPrivate()
}

func fxCode(c Context, v economy.FXConvertView, code string) string {
	if code == "SUP" {
		return c.T("fx.convert.sup", nil)
	}
	for _, l := range v.Legs {
		if l.Code == code {
			return l.Name
		}
	}
	for _, h := range v.Holdings {
		if h.Code == code {
			return h.Name
		}
	}
	return code
}

func renderFXConvert(c Context, v economy.FXConvertView) *presenter.Response {
	kb := keyboards.New()
	switch v.Stage {
	case economy.FXConvertAsk:
		a := map[string]any{"from": fxCode(c, v, v.From), "to": fxCode(c, v, v.To), "amount": FormatNumber(c, v.Amount), "out": FormatNumber(c, v.Out),
			"min": FormatNumber(c, v.MinOut), "slip": PercentFromBPS(c, int(v.SlippageBPS))}
		lines := []string{c.T("fx.convert.title", nil), c.T("fx.convert.ask", a)}
		for _, l := range v.Legs {
			la := map[string]any{"name": l.Name, "sup": FormatMoney0(c, l.SUPIn+l.SUPOut), "units": FormatNumber(c, l.UnitsIn+l.UnitsOut), "fee": FormatNumber(c, l.Fee)}
			key := "fx.convert.leg_sell"
			if l.Side == economy.FXSideBuy {
				key = "fx.convert.leg_buy"
			}
			lines = append(lines, c.T(key, la))
		}
		if v.Complete && v.Out > 0 {
			if b, ok := keyboards.Button(c.T("fx.button.convert_confirm", nil), economy.AddrFXConvert, v.From, v.To, itoa64(v.Amount), itoa64(v.Out), economy.FXConfirm); ok {
				kb.Row(b)
			}
		} else {
			lines = append(lines, c.T("fx.convert.cannot", nil))
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: economy.AddrFXConvert, RefreshData: economy.AddrFXConvert}))
		return c.respond(paragraphs(lines...), kb.Build())
	case economy.FXConvertDone:
		a := map[string]any{"from": fxCode(c, v, v.From), "to": fxCode(c, v, v.To), "gave": FormatNumber(c, v.Gave), "got": FormatNumber(c, v.Got)}
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank, RefreshData: economy.AddrFXConvert}))
		return c.respond(paragraphs(c.T("fx.convert.title", nil), c.T("fx.convert.done", a)), kb.Build())
	}
	lines := []string{c.T("fx.convert.title", nil), c.T("fx.convert.balance", map[string]any{"sup": FormatMoney0(c, v.CashSUP)})}
	for _, h := range v.Holdings {
		lines = append(lines, c.T("fx.convert.holding", map[string]any{"units": FormatNumber(c, h.Units), "name": h.Name}))
		if b, ok := keyboards.Button(c.T("fx.button.sell_all", map[string]any{"name": h.Name}), economy.AddrFXConvert, h.Code, "SUP", itoa64(h.Units)); ok {
			kb.Row(b)
		}
		if v.CashSUP > 0 {
			if b, ok := keyboards.Button(c.T("fx.button.buy_all", map[string]any{"name": h.Name}), economy.AddrFXConvert, "SUP", h.Code, itoa64(v.CashSUP)); ok {
				kb.Row(b)
			}
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank, RefreshData: economy.AddrFXConvert}))
	return c.respond(paragraphs(lines...), kb.Build())
}

// FXRefusal renders a refused request.
func FXRefusal(c Context, v economy.FXRefusalView) *presenter.Response {
	return c.withView(renderFXRefusal(c, v), ScreenFXRefusal, v).MarkPrivate()
}

func renderFXRefusal(c Context, v economy.FXRefusalView) *presenter.Response {
	text := c.T("fx.refused."+v.Kind, map[string]any{"min": fxPrice(c, v.Min), "max": fxPrice(c, v.Max), "least": FormatMoney0(c, v.Min)})
	kb := keyboards.New()
	back := economy.AddrFXBook
	if v.Back.Command != "" {
		back = keyboards.Data(v.Back.Address())
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(text, kb.Build())
}
