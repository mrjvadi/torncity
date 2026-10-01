package economy

import "github.com/mrjvadi/torncity/internal/presentation"

// Payment methods, as the core spells them (internal/domain/payment). They
// travel in a button's address as its last argument.
const (
	MethodCash = "cash"
	MethodCard = "card"
)

// PaymentDeclinedView is a charge nothing the player holds can pay.
type PaymentDeclinedView struct {
	Amount     int64
	Cash, Bank int64
	// Accepted are the methods the service takes; a service taking one
	// method says so, since money in the other purse cannot help.
	Accepted []string
	// Back is the screen the price was on, and BackLabel the stable name of
	// that way back (an identifier an edge words, never a sentence).
	BackLabel string
	Back      presentation.Ref
}
