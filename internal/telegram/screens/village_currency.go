package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The charter of the settlement's own money, and what a chartered money shows (docs/adr/0033
// section 6.2; roadmap 2.19 phase 1). The rate is shown as live, never as a peg.

// Screen and address.
const (
	ScreenCurrencyCharter = village.ScreenCurrencyCharter
	AddrCurrencyCharter   = village.AddrCurrencyCharter
)

// CurrencyCharter renders the charter screen.
func CurrencyCharter(c Context, v village.CurrencyCharterView) *presenter.Response {
	return c.withView(renderCurrencyCharter(c, v), ScreenCurrencyCharter, v)
}

// liveRate writes the live rate: how many units of the money one SUP buys now.
func liveRate(c Context, r0, xrefPPM int64) string {
	if r0 <= 0 || xrefPPM <= 0 {
		return ""
	}
	units := currency.Rate{R0: r0, XRefPPM: xrefPPM}.ToLocalNearest(1)
	if units < 10 { // a small rate reads better with a decimal
		hundredths := currency.Rate{R0: r0, XRefPPM: xrefPPM}.ToLocalNearest(100)
		return c.numerals().localise(FormatNumber(c, hundredths/100) + "." + twoDigits(hundredths%100))
	}
	return FormatNumber(c, units)
}

func twoDigits(n int64) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func renderCurrencyCharter(c Context, v village.CurrencyCharterView) *presenter.Response {
	name := map[string]any{"name": v.Name, "village": v.Village}
	var lines []string
	switch v.Stage {
	case village.CharterAsk:
		lines = append(lines,
			c.T("village.currency.title", name),
			c.T("village.currency.ask", map[string]any{
				"name": v.Name, "fee": FormatMoney0(c, v.Fee), "deposit": FormatMoney0(c, v.Deposit), "units": FormatNumber(c, v.Units),
				"treasury": FormatMoney0(c, v.Treasury), "mint": PercentFromBPS(c, int(v.MintFeeBPS)),
			}),
			c.T("village.currency.live", nil))
		if !v.CanPay {
			lines = append(lines, c.T("village.currency.cannot_pay", map[string]any{"need": FormatMoney0(c, v.Fee+v.Deposit)}))
		}
	case village.CharterDone:
		lines = append(lines,
			c.T("village.currency.title", name),
			c.T("village.currency.done", map[string]any{"name": v.Name, "units": FormatNumber(c, v.Units), "pot": FormatMoney0(c, v.PotSUP)}),
			c.T("village.currency.rate", map[string]any{"name": v.Name, "rate": liveRate(c, v.R0, v.XRefPPM)}))
	default:
		lines = append(lines,
			c.T("village.currency.title", name),
			c.T("village.currency.exists", map[string]any{"name": v.Name, "supply": FormatNumber(c, v.Supply), "pot": FormatMoney0(c, v.PotSUP)}),
			c.T("village.currency.rate", map[string]any{"name": v.Name, "rate": liveRate(c, v.R0, v.XRefPPM)}),
			c.T("village.currency.live", nil))
	}
	kb := keyboards.New()
	if v.Stage == village.CharterAsk && v.CanPay {
		if b, ok := keyboards.Button(c.T("village.currency.button.charter", name), AddrCurrencyCharter,
			itoa64(v.R0), itoa64(v.Deposit), village.ResidenceConfirm); ok {
			kb.Row(b)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: village.AddrMoney, RefreshData: AddrCurrencyCharter}))
	return c.respond(paragraphs(lines...), kb.Build())
}

// FormatMoney0 writes an amount in SUP only, never converted: the fee and the deposit of a charter are
// SUP by definition, and the screen that asks for them names the money it is creating beside them.
func FormatMoney0(c Context, minor int64) string {
	return c.T("format.money", map[string]any{"amount": FormatNumber(c, minor)})
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
