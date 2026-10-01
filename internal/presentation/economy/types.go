// Package economy is the economy and finance area's neutral contract
// (docs/adr/0039-presentation-split.md): the views of the bank, paying a
// player, loans, savings, insurance, the gold dealer, the stock exchange, the
// item market, the city shops, the auction house and the city budget, and the
// constructors that turn a view into a neutral response. It imports no edge
// and holds no wording.
package economy

import "github.com/mrjvadi/torncity/internal/presentation"

// The shared pieces of a view, as the core names them.
type (
	// Named is a content entry: its code and its authored name.
	Named = presentation.Named
	// PaymentChoice is the ways to pay one charge.
	PaymentChoice = presentation.PaymentChoice
	// GovPlace is a city or a country, by code.
	GovPlace = presentation.GovPlace
	// Way is the walk to the place a service is at.
	Way = presentation.Way
)
