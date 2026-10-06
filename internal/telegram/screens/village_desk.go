package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The village desk (docs/adr/0033 6.10; roadmap 2.19 phase 2): converting between SUP and the town's money
// at the live rate less the desk's fee, with one confirm and a price limit.

// Screen and addresses.
const (
	ScreenCurrencyDesk = village.ScreenCurrencyDesk
	AddrCurrencyDesk   = village.AddrCurrencyDesk
	AddrCurrencyFee    = village.AddrCurrencyFee
)

// CurrencyDesk renders the desk screen.
func CurrencyDesk(c Context, v village.DeskView) *presenter.Response {
	return c.withView(renderCurrencyDesk(c, v), ScreenCurrencyDesk, v)
}

func renderCurrencyDesk(c Context, v village.DeskView) *presenter.Response {
	name := map[string]any{"name": v.Name, "village": v.Village}
	fee := PercentFromBPS(c, int(v.FeeBPS))
	var lines []string
	kb := keyboards.New()
	switch v.Stage {
	case village.DeskAsk:
		give, get := FormatMoney0(c, v.SUP), FormatNumber(c, v.Units)+" "+v.Name
		if v.Side == "sell" {
			give, get = FormatNumber(c, v.Units)+" "+v.Name, FormatMoney0(c, v.SUP)
		}
		lines = append(lines, c.T("village.desk.title", name),
			c.T("village.desk.ask", map[string]any{"give": give, "get": get, "fee": fee, "slip": PercentFromBPS(c, int(v.SlippageBPS))}))
		if (v.Side == "buy" && !v.CanBuy) || (v.Side == "sell" && !v.CanSell) {
			lines = append(lines, c.T("village.desk.cannot", nil))
		} else {
			out := v.Units
			if v.Side == "sell" {
				out = v.SUP
			}
			if b, ok := keyboards.Button(c.T("village.desk.button.confirm", nil), AddrCurrencyDesk,
				v.Side, itoa64(v.Amount), itoa64(out), village.ResidenceConfirm); ok {
				kb.Row(b)
			}
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrCurrencyDesk, RefreshData: AddrCurrencyDesk}))
	case village.DeskDone:
		give, get := FormatMoney0(c, v.SUP), FormatNumber(c, v.Units)+" "+v.Name
		if v.Side == "sell" {
			give, get = FormatNumber(c, v.Units)+" "+v.Name, FormatMoney0(c, v.SUP)
		}
		lines = append(lines, c.T("village.desk.title", name),
			c.T("village.desk.done", map[string]any{"give": give, "get": get}),
			c.T("village.desk.balance", map[string]any{"sup": FormatMoney0(c, v.CashSUP), "units": FormatNumber(c, v.CashUnits), "name": v.Name}))
		kb.Nav(c.nav(keyboards.Nav{BackData: village.AddrMoney, RefreshData: AddrCurrencyDesk}))
	default:
		lines = append(lines, c.T("village.desk.title", name),
			c.T("village.desk.balance", map[string]any{"sup": FormatMoney0(c, v.CashSUP), "units": FormatNumber(c, v.CashUnits), "name": v.Name}),
			c.T("village.desk.rate", map[string]any{"name": v.Name, "rate": liveRate(c, v.R0, v.XRefPPM), "fee": fee}),
			c.T("village.desk.stock", map[string]any{"units": FormatNumber(c, v.DeskUnits), "name": v.Name, "sup": FormatMoney0(c, v.DeskSUP)}))
		if v.DeskUnits == 0 {
			lines = append(lines, c.T("village.desk.empty", nil))
		}
		for _, n := range v.PresetsSUP {
			if b, ok := keyboards.Button(c.T("village.desk.button.buy", map[string]any{"sup": FormatMoney0(c, n), "name": v.Name}),
				AddrCurrencyDesk, "buy", itoa64(n)); ok {
				kb.Row(b)
			}
		}
		for _, n := range v.PresetsUnits {
			if b, ok := keyboards.Button(c.T("village.desk.button.sell", map[string]any{"units": FormatNumber(c, n), "name": v.Name}),
				AddrCurrencyDesk, "sell", itoa64(n)); ok {
				kb.Row(b)
			}
		}
		if v.CanSetFee {
			var row []presenter.Button
			for _, bps := range []int64{v.MinFeeBPS, 30, 100, v.MaxFeeBPS} {
				if b, ok := keyboards.Button(c.T("village.desk.button.fee", map[string]any{"fee": PercentFromBPS(c, int(bps))}),
					AddrCurrencyFee, itoa64(bps)); ok {
					row = append(row, b)
				}
			}
			kb.Row(row...)
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: village.AddrMoney, RefreshData: AddrCurrencyDesk}))
	}
	return c.respond(paragraphs(lines...), kb.Build())
}
