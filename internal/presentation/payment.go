package presentation

// PaymentChoice is what a price screen needs to offer the ways to pay one
// charge: the price, the methods the service takes, the ones that cover it,
// and what the player holds. Data only; each edge words the methods itself.
type PaymentChoice struct {
	// Amount is the price, in minor units.
	Amount int64
	// Accepted are the methods the service takes, in display order.
	Accepted []string
	// Usable are the accepted methods that cover Amount.
	Usable []string
	// Cash and Bank are the player's balances. Shown only on a screen
	// that is not shared.
	Cash, Bank int64
}

// Accepts reports whether the service takes the method.
func (p PaymentChoice) Accepts(m string) bool { return hasMethod(p.Accepted, m) }

// UsableBy reports whether the method is accepted and covers the price.
func (p PaymentChoice) UsableBy(m string) bool { return hasMethod(p.Usable, m) }

func hasMethod(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
