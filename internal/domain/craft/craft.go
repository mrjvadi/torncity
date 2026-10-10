// Package craft is the pure part of the tool tiers and the home crafting (docs/adr/0068): which tool a shift takes, how much
// of its output a worker with a lesser tool still makes, how much slower a better tool wears, what a batch yields at home.
// No I/O, no clock, no randomness.
package craft

import "sort"

// BPS is one hundred percent in basis points.
const BPS = 10_000

// Tier is one rung of the tool ladder: the item that stands for it.
type Tier struct {
	Tier int
	Item string
}

// Tools is the ladder and its numbers (content crafting.yml).
type Tools struct {
	Items []Tier
	// ShortBPS is the share of its output a worker keeps for each tier his best tool is below the work's need.
	ShortBPS int64
	// WearDivisor is how many times slower a tool wears for each tier it is above the work's need.
	WearDivisor int64
}

func (t Tools) sorted() []Tier {
	out := append([]Tier(nil), t.Items...)
	sort.Slice(out, func(i, j int) bool { return out[i].Tier < out[j].Tier })
	return out
}

// Best is the highest tier of which the stock holds a tool.
func (t Tools) Best(units map[string]int64) (tier int, ok bool) {
	for _, x := range t.sorted() {
		if units[x.Item] >= 1 {
			tier, ok = x.Tier, true
		}
	}
	return tier, ok
}

// Pick chooses the tool a due wear takes for work that needs tier need: the lowest tier that is enough, else the best of the
// lesser ones.
func (t Tools) Pick(units map[string]int64, need int) (item string, tier int, ok bool) {
	var below *Tier
	for _, x := range t.sorted() {
		x := x
		if units[x.Item] < 1 {
			continue
		}
		if x.Tier >= need {
			return x.Item, x.Tier, true
		}
		below = &x
	}
	if below != nil {
		return below.Item, below.Tier, true
	}
	return "", 0, false
}

// Factor is the share of its output a worker keeps with a best tool of tier have for work that needs tier need.
func (t Tools) Factor(have, need int) int64 {
	f := int64(BPS)
	for i := have; i < need; i++ {
		f = f * t.ShortBPS / BPS
	}
	return f
}

// Wear is the wear of one shift (ten-thousandths of a tool) with a tool of tier tier for work that needs tier need: a better
// tool wears WearDivisor times slower for each tier above the need, at least one ten-thousandth.
func (t Tools) Wear(base int64, tier, need int) int64 {
	d := t.WearDivisor
	if d < 1 {
		d = 1
	}
	for i := need; i < tier; i++ {
		base /= d
	}
	return max(base, 1)
}

// HomeYield is the share of a batch's outputs a home station makes: the loss is the price of a small bench.
func HomeYield(qty, bps, carry int64) (made, rest int64) {
	x := qty*bps + carry
	return x / BPS, x % BPS
}
