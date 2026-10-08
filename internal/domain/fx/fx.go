// Package fx is the arithmetic of the VC/SUP order book of a settlement's own money (docs/adr/0033
// sections 6.6 to 6.9, docs/adr/0029 section 4): prices, the settled SUP of a fill, the escrow an order
// holds, the fees, the circuit breaker band and the reference rate x_ref. It is pure: integers only, no
// clock, no storage. The matching itself is the shared engine, internal/domain/market.
//
// UNITS. A settlement's money is traded in whole units, SUP in whole SUP minor units. A price is the
// number of MICRO-SUP one unit costs (PriceScale micro-SUP is one SUP), so the order book of "MKP/SUP"
// quotes 100000 for a unit worth a tenth of a SUP. The reference rate x_ref (parts per million, 1000000
// is 1.00) is the SUP value of a unit relative to the charter, 1/r0 SUP: a trade at price p says
// x = p x r0.
//
// ROUNDING. SUP is coarse (one minor unit), a price is not. The SUP a buy order has paid after some fills
// is floor(its cumulative notional / PriceScale) and a fill moves the difference, so an order never pays
// for more than it was shown, never more than it escrowed, and the rounding of a whole order is under one
// SUP in the seller's disfavour. The fee is taken on the cumulative amount the same way (rounded up on
// the total, so the total never exceeds what the order set aside).
package fx

import (
	"errors"
	"math/bits"
)

// PriceScale is micro-SUP per SUP: the scale of a price.
const PriceScale = 1_000_000

// PPM is the scale of x_ref.
const PPM = 1_000_000

// BPS is the scale of basis points.
const BPS = 10_000

// Pair is the asset id of a currency's book on the shared engine: "<CODE>/SUP".
func Pair(code string) string { return code + "/SUP" }

// ErrOutOfBand means a price further from the reference than the circuit breaker allows.
var ErrOutOfBand = errors.New("fx: the price is outside the circuit breaker's band")

// mulDiv computes a*b/c rounding down with a 128-bit intermediate; ok is false on overflow or c <= 0.
func mulDiv(a, b, c int64) (q int64, rem int64, ok bool) {
	if a < 0 || b < 0 || c <= 0 {
		return 0, 0, false
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return 0, 0, false
	}
	qq, rr := bits.Div64(hi, lo, uint64(c))
	if qq > uint64(1<<63-1) {
		return 0, 0, false
	}
	return int64(qq), int64(rr), true
}

// MulCeil is ceil(a*b/c); 0 when it overflows or is undefined.
func MulCeil(a, b, c int64) int64 {
	q, rem, ok := mulDiv(a, b, c)
	if !ok {
		return 0
	}
	if rem > 0 {
		q++
	}
	return q
}

// MulFloor is floor(a*b/c); 0 when it overflows or is undefined.
func MulFloor(a, b, c int64) int64 {
	q, _, ok := mulDiv(a, b, c)
	if !ok {
		return 0
	}
	return q
}

// RefPrice is the price (micro-SUP per unit) the reference rate stands for: x_ref / r0, rounded to the
// nearest, never below 1. r0 is units per SUP at charter.
func RefPrice(xRefPPM, r0 int64) int64 {
	if xRefPPM <= 0 || r0 <= 0 {
		return 0
	}
	p := (xRefPPM + r0/2) / r0
	return max(p, 1)
}

// XFromPrice is the x_ref a price stands for: price x r0 (parts per million).
func XFromPrice(price, r0 int64) int64 { return price * r0 }

// Band is the lowest and highest price an order may be placed at: the reference price moved by at most
// maxMoveBPS either way (the circuit breaker, reserve.max_move_bps). lo is never below 1.
func Band(refPrice, maxMoveBPS int64) (lo, hi int64) {
	if refPrice <= 0 || maxMoveBPS < 0 {
		return 0, 0
	}
	d := MulCeil(refPrice, maxMoveBPS, BPS)
	return max(refPrice-d, 1), refPrice + d
}

// InBand reports whether price may be placed.
func InBand(price, refPrice, maxMoveBPS int64) bool {
	lo, hi := Band(refPrice, maxMoveBPS)
	return lo > 0 && price >= lo && price <= hi
}

// Notional is quantity x price in micro-SUP; false on overflow.
func Notional(quantity, price int64) (int64, bool) {
	if quantity < 0 || price < 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(uint64(quantity), uint64(price))
	if hi != 0 || lo > uint64(1<<63-1) {
		return 0, false
	}
	return int64(lo), true
}

// SUPOf is the SUP a buyer has paid when the cumulative notional of its fills is cum micro-SUP.
func SUPOf(cumNotional int64) int64 { return cumNotional / PriceScale }

// FeeOf is the fee on a cumulative amount at bps, rounded up on the total.
func FeeOf(cumAmount, bps int64) int64 { return MulCeil(cumAmount, bps, BPS) }

// BuyEscrow is what a buy order of quantity units at price sets aside in SUP: the most its fills can
// come to (floor of quantity x price) plus the most the fee on that can come to. Zero if it overflows.
func BuyEscrow(quantity, price, feeBPS int64) int64 {
	n, ok := Notional(quantity, price)
	if !ok {
		return 0
	}
	sup := SUPOf(n)
	return sup + FeeOf(sup, feeBPS)
}

// Fill is what one fill moves, computed from the cumulative state of the two orders before it.
type Fill struct {
	// SUP is the SUP the buyer pays the seller; SUPFee the fee on top of it (to Support's treasury).
	SUP, SUPFee int64
	// Units is the quantity; UnitsFee the part of it the village treasury keeps; the buyer receives
	// Units - UnitsFee.
	Units, UnitsFee int64
	// Notional is quantity x price in micro-SUP (the buyer's cumulative grows by it).
	Notional int64
}

// FillOf prices one fill of qty units at price, given the buy order's cumulative notional before it and the
// fee rate it carries (the reserve's fee on selling SUP) and the sell order's filled quantity before it and
// its fee rate (the village's fee on selling VC).
func FillOf(qty, price, buyCumNotional, buyFeeBPS, sellFilledBefore, sellFeeBPS int64) (Fill, bool) {
	n, ok := Notional(qty, price)
	if !ok || buyCumNotional > (1<<62) {
		return Fill{}, false
	}
	supBefore, supAfter := SUPOf(buyCumNotional), SUPOf(buyCumNotional+n)
	sup := supAfter - supBefore
	supFee := FeeOf(supAfter, buyFeeBPS) - FeeOf(supBefore, buyFeeBPS)
	unitsFee := FeeOf(sellFilledBefore+qty, sellFeeBPS) - FeeOf(sellFilledBefore, sellFeeBPS)
	return Fill{SUP: sup, SUPFee: supFee, Units: qty, UnitsFee: unitsFee, Notional: n}, true
}

// Observation is one period's reading of the book.
type Observation struct {
	// Trades is how many fills happened in the period; VolumeUnits and Notional their units and
	// micro-SUP; ValuePPM is the period's value of x: the volume-weighted price x r0 if it traded, else the
	// previous period's value carried forward.
	Trades               int64
	VolumeUnits          int64
	Notional             int64
	ValuePPM             int64
	HadTrades            bool
	XRefBefore, XRefNext int64
}

// VWAPPrice is the volume-weighted price of a period (micro-SUP per unit, rounded to the nearest); 0 with no volume.
func VWAPPrice(volumeUnits, notional int64) int64 {
	if volumeUnits <= 0 {
		return 0
	}
	return (notional + volumeUnits/2) / volumeUnits
}

// NextRef is the reference rate after a period: the time-weighted average of the last `window` periods'
// values (a period without trades carries the previous value), but only once at least minTrades fills fell
// in the window; before that the reference stays where it is. recent holds the periods' ValuePPM, oldest
// first, the period just closed last. A currency younger than the window has fewer readings: the missing
// ones are the value it started the window with (padWith: the reference before the oldest reading), so the
// reference moves a window's share of the gap per period of trading, never all at once. trades is the
// fills in the window. It returns the new x_ref and whether it moved.
func NextRef(current int64, recent []int64, padWith, trades, minTrades int64, window int) (int64, bool) {
	if len(recent) == 0 || trades < minTrades || minTrades < 0 || window < 1 || padWith <= 0 {
		return current, false
	}
	if len(recent) > window {
		recent = recent[len(recent)-window:]
	}
	sum := int64(window-len(recent)) * padWith
	for _, v := range recent {
		if v <= 0 {
			return current, false
		}
		sum += v
	}
	next := (sum + int64(window)/2) / int64(window)
	if next <= 0 {
		return current, false
	}
	return next, next != current
}

// ParsePrice reads a price a player typed: a whole number is micro-SUP per unit; a number with a decimal
// point is SUP per unit ("0.085" is 85000), up to six decimals.
func ParsePrice(text string) (int64, bool) {
	var whole, frac int64
	var digits int
	seenDot, any := false, false
	for _, r := range text {
		switch {
		case r == ' ' || r == ',' || r == '_':
		case r == '.' && !seenDot:
			seenDot = true
		case r >= '0' && r <= '9':
			any = true
			if !seenDot {
				whole = whole*10 + int64(r-'0')
				if whole > 1<<40 {
					return 0, false
				}
				continue
			}
			if digits < 6 {
				frac = frac*10 + int64(r-'0')
				digits++
			}
		default:
			return 0, false
		}
	}
	if !any {
		return 0, false
	}
	if !seenDot {
		return whole, whole > 0
	}
	for ; digits < 6; digits++ {
		frac *= 10
	}
	p := whole*PriceScale + frac
	return p, p > 0
}
