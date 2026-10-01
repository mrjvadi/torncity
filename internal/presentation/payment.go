package presentation

// PaymentChoice is what a price screen needs to offer the ways to pay: the
// price, the methods the service takes and the ones that cover it, and the
// player's balances. It is data; each edge words the offer.
type PaymentChoice struct {
	// Amount is the price, in minor units.
	Amount int64
	// Accepted are the methods the service takes, in display order.
	Accepted []string
	// Usable are the accepted methods that cover Amount.
	Usable []string
	// Cash and Bank are the player's balances. An edge shows them only on a
	// screen that is not shared.
	Cash, Bank int64
}

// Takes reports whether the service accepts the method.
func (p PaymentChoice) Takes(m string) bool { return hasMethod(p.Accepted, m) }

// CanPay reports whether the method covers the price.
func (p PaymentChoice) CanPay(m string) bool { return hasMethod(p.Usable, m) }

func hasMethod(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
