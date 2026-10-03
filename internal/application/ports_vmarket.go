package application

// The village book's money (storage and market audit P3; docs/adr/0040 section
// 5.4). Both reasons are transfers to the settlement treasury, so the supply is
// unchanged: the listing fee from the player's purse, the dues out of the
// seller's proceeds in the trade.
const (
	// ReasonVillageMarketListingFee is paid once when an order takes a stall on
	// the village book, from the player's cash or card to the treasury. It is
	// not refunded when the order is cancelled or ends.
	ReasonVillageMarketListingFee Reason = "village_market_listing_fee"
	// ReasonVillageMarketDues is the toll on a trade in the village book, out of
	// the buyer's escrow (the seller's proceeds less the dues) to the treasury.
	ReasonVillageMarketDues Reason = "village_market_dues"
)

func init() {
	knownReasons[ReasonVillageMarketListingFee] = struct{}{}
	knownReasons[ReasonVillageMarketDues] = struct{}{}
}
