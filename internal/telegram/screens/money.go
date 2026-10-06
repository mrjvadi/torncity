package screens

import "github.com/mrjvadi/torncity/internal/domain/currency"

// FormatMoney renders a sum of money for a player: the amount in whole minor
// units, grouped by FormatNumber, inside the catalogue's format.money phrase,
// so the currency word and its position are the translator's.
//
// Every amount a player reads — a balance, a fee, a payment — goes through
// it, so a sum reads the same on every screen.
//
// Amounts are SUP minor units. A viewer whose home settlement has chartered its own money reads
// the amount in that money, at the live rate, with the SUP amount beside it
// (format.money_local); the rate is the settlement's r0 / x_ref and is never a peg.
func FormatMoney(c Context, minor int64) string {
	if m := c.Money; m != nil && m.Name != "" && m.R0 > 0 && m.XRefPPM > 0 {
		abs, sign := minor, int64(1)
		if abs < 0 {
			abs, sign = -abs, -1
		}
		local := sign * currency.Rate{R0: m.R0, XRefPPM: m.XRefPPM}.ToLocalNearest(abs)
		return c.T("format.money_local", map[string]any{
			"amount": FormatNumber(c, local), "name": m.Name, "sup": c.T("format.money", map[string]any{"amount": FormatNumber(c, minor)}),
		})
	}
	return c.T("format.money", map[string]any{"amount": FormatNumber(c, minor)})
}
