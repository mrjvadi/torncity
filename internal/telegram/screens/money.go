package screens

// FormatMoney renders a sum of money for a player: the amount in whole minor
// units, grouped by FormatNumber, inside the catalogue's format.money phrase,
// so the currency word and its position are the translator's.
//
// Every amount a player reads — a balance, a fee, a payment — goes through
// it, so a sum reads the same on every screen.
func FormatMoney(c Context, minor int64) string {
	return c.T("format.money", map[string]any{"amount": FormatNumber(c, minor)})
}
