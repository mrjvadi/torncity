package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The reserve of a settlement's money and the head's tools over it (docs/adr/0033 6.3 to 6.8, 6.13, 7; roadmap
// 2.19 phase 4), drawn for Telegram.

// Screen and address.
const (
	ScreenCurrencyReserve = village.ScreenCurrencyReserve
	AddrCurrencyReserve   = village.AddrCurrencyReserve
)

// CurrencyReserve renders the reserve screen.
func CurrencyReserve(c Context, v village.ReserveView) *presenter.Response {
	return c.withView(renderCurrencyReserve(c, v), ScreenCurrencyReserve, v)
}

// ppmIndex writes an index in parts per million as a percent of its charter value.
func ppmIndex(c Context, ppm int64) string { return PercentFromBPS(c, int(ppm/100)) }

func signedBPS(c Context, bps int64) string {
	if bps < 0 {
		return "-" + PercentFromBPS(c, int(-bps))
	}
	return PercentFromBPS(c, int(bps))
}

func renderCurrencyReserve(c Context, v village.ReserveView) *presenter.Response {
	a := map[string]any{"name": v.Name, "village": v.Village, "symbol": v.Symbol}
	lines := []string{c.T("village.reserve.title", a)}
	kb := keyboards.New()
	switch v.Stage {
	case village.ReserveAsk, village.ReserveDone:
		args := map[string]any{"name": v.Name, "amount": FormatNumber(c, v.Amount), "sup": FormatMoney0(c, v.Amount), "out": FormatNumber(c, v.Out),
			"outsup": FormatMoney0(c, v.Out), "price": fxPrice(c, v.Price), "notice": FormatNumber(c, v.WithdrawNoticeHours), "delay": FormatNumber(c, v.DelayHours),
			"fee": PercentFromBPS(c, int(v.MintFeeBPS))}
		lines = append(lines, c.T("village.reserve."+v.Stage+"."+v.Action, args))
		if v.Stage == village.ReserveAsk {
			if v.Reason != "" {
				lines = append(lines, c.T("village.reserve.cannot."+v.Reason, nil))
			} else if b, ok := keyboards.Button(c.T("village.reserve.button.confirm", nil), AddrCurrencyReserve, v.Action, itoa64(v.Amount), itoa64(v.Price), "", village.ResidenceConfirm); ok {
				kb.Row(b)
			}
			kb.Nav(c.nav(keyboards.Nav{BackData: AddrCurrencyReserve, RefreshData: AddrCurrencyReserve}))
		} else {
			kb.Nav(c.nav(keyboards.Nav{BackData: village.AddrMoney, RefreshData: AddrCurrencyReserve}))
		}
		return c.respond(paragraphs(lines...), kb.Build())
	}
	lines = append(lines, c.T("village.reserve.status."+v.Status, nil))
	lines = append(lines, body(
		c.T("village.reserve.valuation", map[string]any{"pot": FormatMoney0(c, v.PotSUP), "basis": FormatMoney0(c, v.Basis), "excess": FormatMoney0(c, v.Excess)}),
		coverageLine(c, v),
		c.T("village.reserve.supply", map[string]any{"supply": FormatNumber(c, v.Supply), "name": v.Name, "stab": FormatNumber(c, v.Stabilisation),
			"minted": FormatNumber(c, v.Minted), "burnt": FormatNumber(c, v.Burnt)}),
	))
	lines = append(lines, c.T("village.reserve.levers", map[string]any{"mint": PercentFromBPS(c, int(v.MintFeeBPS)), "fx": PercentFromBPS(c, int(v.ReserveFeeBPS)),
		"move": PercentFromBPS(c, int(v.MaxMoveBPS)), "hours": FormatNumber(c, v.WithdrawNoticeHours)}))
	if v.Status == "chartered" {
		lines = append(lines, c.T("village.reserve.limits", map[string]any{"cap": PercentFromBPS(c, int(v.CapBPS)), "floor": PercentFromBPS(c, int(v.FloorBPS)),
			"delay": FormatNumber(c, v.DelayHours), "buy": FormatMoney0(c, v.BuyBudgetSUP), "sell": FormatNumber(c, v.SellBudgetUnits), "name": v.Name}))
	}
	if m := v.Macro; m != nil {
		lines = append(lines, c.T("village.reserve.macro", map[string]any{"no": FormatNumber(c, m.PeriodNo), "tr": ppmIndex(c, m.TradablePPM),
			"nt": ppmIndex(c, m.NonTradablePPM), "price": ppmIndex(c, m.PricePPM), "pi": signedBPS(c, m.PiLocalBPS), "growth": signedBPS(c, m.SupplyGrowthBPS)}))
	} else {
		lines = append(lines, c.T("village.reserve.macro_none", nil))
	}
	if v.WindDownEndsAt != nil && v.Status == "wind_down" {
		lines = append(lines, c.T("village.reserve.wind", map[string]any{"until": FormatDate(c, *v.WindDownEndsAt), "units": FormatNumber(c, v.MyUnits), "name": v.Name,
			"share": FormatMoney0(c, v.MyShare)}))
	}
	var log []string
	for _, i := range v.Interventions {
		log = append(log, c.T("village.reserve.log_intervention", map[string]any{"side": c.T("fx.side."+i.Side, nil), "units": FormatNumber(c, i.Units), "name": v.Name,
			"price": fxPrice(c, i.Price), "status": c.T("village.reserve.state."+i.Status, nil)}))
	}
	for _, w := range v.Withdrawals {
		log = append(log, c.T("village.reserve.log_withdrawal", map[string]any{"sup": FormatMoney0(c, w.SUP), "status": c.T("village.reserve.state."+w.Status, nil),
			"when": FormatDate(c, w.ExecuteAfter)}))
	}
	if len(log) > 0 {
		lines = append(lines, c.T("village.reserve.log", nil)+"\n"+body(log...))
	}
	btn := func(labelKey string, args map[string]any, parts ...string) {
		if b, ok := keyboards.Button(c.T(labelKey, args), append([]string{AddrCurrencyReserve}, parts...)...); ok {
			kb.Row(b)
		}
	}
	if v.Status == "chartered" {
		if v.CanIssue {
			for _, n := range v.Presets {
				btn("village.reserve.button.issue", map[string]any{"sup": FormatMoney0(c, n)}, village.ReserveActionIssue, itoa64(n))
			}
			if v.TreasuryUnits > 0 {
				btn("village.reserve.button.burn", map[string]any{"units": FormatNumber(c, v.TreasuryUnits/10+1), "name": v.Name}, village.ReserveActionBurn, itoa64(v.TreasuryUnits/10+1))
			}
			btn("village.reserve.button.retire", nil, village.ReserveActionRetire, "1")
		}
		if v.CanPolicy {
			ref := refPrice(v)
			for _, n := range v.Presets {
				btn("village.reserve.button.buy", map[string]any{"units": FormatNumber(c, n*v.R0), "name": v.Name}, village.ReserveActionBuy, itoa64(n*v.R0), itoa64(ref))
			}
			if v.TreasuryUnits > 0 {
				btn("village.reserve.button.sell", map[string]any{"units": FormatNumber(c, v.TreasuryUnits/10+1), "name": v.Name}, village.ReserveActionSell, itoa64(v.TreasuryUnits/10+1), itoa64(ref))
			}
			if free := v.Excess - v.PendingWithdrawals; free > 0 {
				btn("village.reserve.button.withdraw", map[string]any{"sup": FormatMoney0(c, free)}, village.ReserveActionWithdraw, itoa64(free))
			}
			for _, i := range v.Interventions {
				if i.Status == "pending" {
					btn("village.reserve.button.cancel", nil, village.ReserveActionCancel, "", "", i.ID)
				}
			}
			for _, w := range v.Withdrawals {
				if w.Status == "pending" {
					btn("village.reserve.button.cancel", nil, village.ReserveActionCancel, "", "", w.ID)
				}
			}
		}
	}
	if v.CanClaim {
		btn("village.reserve.button.claim", map[string]any{"share": FormatMoney0(c, v.MyShare)}, village.ReserveActionClaim, itoa64(v.MyUnits))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: village.AddrMoney, RefreshData: AddrCurrencyReserve}))
	return c.respond(paragraphs(lines...), kb.Build())
}

func coverageLine(c Context, v village.ReserveView) string {
	if !v.CoverageKnown {
		return c.T("village.reserve.coverage_none", nil)
	}
	return c.T("village.reserve.coverage", map[string]any{"cap": FormatMoney0(c, v.MarketCapSUP), "coverage": PercentFromBPS(c, int(v.CoverageBPS))})
}

func refPrice(v village.ReserveView) int64 {
	if v.R0 <= 0 || v.XRefPPM <= 0 {
		return 0
	}
	return max((v.XRefPPM+v.R0/2)/v.R0, 1)
}
