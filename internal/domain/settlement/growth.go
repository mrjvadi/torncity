package settlement

// Land: the grid grows by one step at a time (docs/adr/0033 section 8.4, as
// the owner corrected it: land is bought, never capped by tier). A step adds
// one column on the east edge and one row on the north edge, so the side
// grows by one and every stored lot coordinate stays valid (GridCentreGrown).

// GrowthLots is how many lots a step from a grid of the given side adds.
func GrowthLots(side int) int { return 2*side + 1 }

// GrowthPrice is what a step from a grid of the given side costs when
// expansions earlier steps have already been bought: the lots gained at the
// lot price, dearer by stepBPS per earlier expansion. Integer, no float.
func GrowthPrice(side, expansions int, lotPrice, stepBPS int64) int64 {
	base := int64(GrowthLots(side)) * lotPrice
	return base * (10_000 + stepBPS*int64(expansions)) / 10_000
}
