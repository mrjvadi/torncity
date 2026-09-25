package finance

import "fmt"

// Gold (docs/adr/0026 section 6). The city's gold dealer — the NPC economy —
// quotes one mid price per gram, which moves once per finance period on the
// game clock: a bounded random step, drawn from the period's own number so a
// replayed period draws the same one, plus the pull of the players' net
// demand in the period, the whole move capped, the price held within bounds.
// The dealer sells above the mid and buys below it, by half the spread each
// way, so buying and selling at once always loses the spread.

// GoldRules are the dealer's content.
type GoldRules struct {
	// Min and Max bound the mid price per gram.
	Min, Max int64
	// StepBPS is the largest random move in one period.
	StepBPS int64
	// DemandBPSPerKG moves the price this much per 1000 grams bought net in
	// the period (sold net pulls it down); DemandCapBPS caps that pull.
	DemandBPSPerKG, DemandCapBPS int64
	// SpreadBPS is the gap between the dealer's selling and buying prices.
	SpreadBPS int64
}

// Validate checks the rules.
func (r GoldRules) Validate() error {
	switch {
	case r.Min < 1 || r.Max < r.Min:
		return fmt.Errorf("%w: gold price bounds %d..%d", ErrInvalid, r.Min, r.Max)
	case r.StepBPS < 0 || r.StepBPS > 5000 || r.DemandBPSPerKG < 0 || r.DemandCapBPS < 0 || r.DemandCapBPS > 5000:
		return fmt.Errorf("%w: gold step %d, demand %d cap %d", ErrInvalid, r.StepBPS, r.DemandBPSPerKG, r.DemandCapBPS)
	case r.SpreadBPS < 0 || r.SpreadBPS >= BPSWhole:
		return fmt.Errorf("%w: gold spread %d", ErrInvalid, r.SpreadBPS)
	}
	return nil
}

// Roll draws a number in -10000..10000 from a seed, the same one every time
// (splitmix64).
func Roll(seed uint64) int64 {
	z := seed + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	return int64(z%20001) - 10000
}

// Next is the mid price after one period: roll (-10000..10000) scales the
// random step, netGrams (bought less sold) pulls it.
func (r GoldRules) Next(price, roll, netGrams int64) int64 {
	move := clamp(roll, -BPSWhole, BPSWhole) * r.StepBPS / BPSWhole
	pull := clamp(netGrams*r.DemandBPSPerKG/1000, -r.DemandCapBPS, r.DemandCapBPS)
	move += pull
	delta := price * move / BPSWhole
	if move != 0 && delta == 0 {
		// A move too small for the price in whole units still moves it one.
		delta = 1
		if move < 0 {
			delta = -1
		}
	}
	return clamp(price+delta, r.Min, r.Max)
}

// Quote is what the dealer sells a gram for (buy, what a player pays) and
// buys one at (sell, what a player gets).
func (r GoldRules) Quote(mid int64) (buy, sell int64) {
	half := OfBPS(mid, r.SpreadBPS) / 2
	buy = mid + max(half, 1)
	sell = max(mid-max(half, 1), 1)
	if r.SpreadBPS == 0 {
		return mid, mid
	}
	return buy, sell
}
