package village

import "github.com/mrjvadi/torncity/internal/presentation"

// The clearing order (docs/adr/0065): the head, or the owner of a lot, orders the trees and rocks of a lot cleared; the crews of the
// village take the ordered lots first.
const (
	ScreenClearOrder = "clear_order"
	AddrClearOrder   = "settlement:clear.order"
	AddrClearCancel  = "settlement:clear.cancel"
)

// ClearOrderView is the answer of an order or its cancellation: the lot, what stands on it, what is ordered now.
type ClearOrderView struct {
	Village string
	X, Y    int
	Trees   int
	Rocks   int
	// OrderTrees and OrderRocks say what is ordered cleared now; both false after a cancellation.
	OrderTrees bool
	OrderRocks bool
	// Cancelled says the order was taken back.
	Cancelled bool
	// Private says the lot is a citizen's: the yield is his and he pays the crew.
	Private bool
}

var screenClearOrder = presentation.Define[ClearOrderView](ScreenClearOrder, "village")

// ClearOrder is the screen.
func ClearOrder(c presentation.Ctx, v ClearOrderView) *presentation.Response {
	return screenClearOrder.Response(c.Lang, v, back(AddrLand))
}
