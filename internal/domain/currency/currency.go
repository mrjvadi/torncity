// Package currency is the arithmetic of a settlement's own money (ADR 0033 section 6): the live
// rate, issuance against a deposit, what a viewer sees, and what a charter costs. It is pure:
// integers only, no clock, no storage.
//
// The rate is NEVER a peg. A currency has a charter rate r0 (units per SUP at charter, only the
// scale of its numbers) and a reference rate x_ref (what one unit is worth in SUP relative to
// charter, 1.00 until a book trades, then the book's average). The live rate is
//
//	units per SUP = r0 / x_ref
//
// and everything converts through it; amounts are integer minor units and every conversion says
// which way it rounds (a price a payer owes rounds up, what a holder receives rounds down, what a
// screen shows rounds to the nearest).
package currency

import (
	"errors"
	"math/bits"
)

// PPM is the scale of x_ref: 1_000_000 parts per million is 1.00.
const PPM = 1_000_000

// BPS is the scale of basis points.
const BPS = 10_000

// Rate is a settlement currency's live rate.
type Rate struct {
	// R0 is units of the currency per 1 SUP at charter.
	R0 int64
	// XRefPPM is the reference rate in parts per million (1_000_000 = 1.00).
	XRefPPM int64
}

// ErrBadRate means a rate with a non-positive part.
var ErrBadRate = errors.New("currency: a rate needs a positive charter rate and reference rate")

// Valid reports whether the rate can convert anything.
func (r Rate) Valid() bool { return r.R0 > 0 && r.XRefPPM > 0 }

// Num and Den express units per SUP as the exact fraction Num/Den: what a client multiplies and
// divides by (Num = R0 x PPM, Den = XRefPPM).
func (r Rate) Num() int64 { return r.R0 * PPM }
func (r Rate) Den() int64 { return r.XRefPPM }

// mulDiv computes a*b/c with a 128-bit intermediate, rounding down; ok is false on overflow of the
// result or a zero divisor. Negative inputs are not accepted.
func mulDiv(a, b, c int64) (q, rem int64, ok bool) {
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

// ToLocalFloor is what a holder receives for sup SUP: rounded down.
func (r Rate) ToLocalFloor(sup int64) int64 {
	if !r.Valid() {
		return 0
	}
	q, _, _ := mulDiv(sup, r.Num(), r.Den())
	return q
}

// ToLocalCeil is what a payer owes in the currency for a price of sup SUP: rounded up.
func (r Rate) ToLocalCeil(sup int64) int64 {
	if !r.Valid() {
		return 0
	}
	q, rem, ok := mulDiv(sup, r.Num(), r.Den())
	if ok && rem > 0 {
		q++
	}
	return q
}

// ToLocalNearest is what a screen shows for sup SUP: rounded to the nearest unit (halves up).
func (r Rate) ToLocalNearest(sup int64) int64 {
	if !r.Valid() {
		return 0
	}
	q, rem, ok := mulDiv(sup, r.Num(), r.Den())
	if ok && rem*2 >= r.Den() {
		q++
	}
	return q
}

// ToSUPFloor is what units of the currency are worth in SUP, rounded down.
func (r Rate) ToSUPFloor(units int64) int64 {
	if !r.Valid() {
		return 0
	}
	q, _, _ := mulDiv(units, r.Den(), r.Num())
	return q
}

// MintResult is what a deposit buys.
type MintResult struct {
	// Units are the units issued (floor).
	Units int64
	// BasisSUP is the deposit's value that backs them: the deposit less the mint fee.
	BasisSUP int64
	// FeeSUP is the mint fee, which stays in the pot as excess.
	FeeSUP int64
}

// Mint prices a deposit at the reference rate: units = floor(deposit x r0 / x_ref x (1 - fee)); a
// deposit buys exactly its market value in units, so there is no free money (ADR 0033 6.4). The
// fee stays in the pot, outside the basis.
func Mint(depositSUP int64, r Rate, mintFeeBPS int64) (MintResult, error) {
	if !r.Valid() || depositSUP < 0 || mintFeeBPS < 0 || mintFeeBPS > BPS {
		return MintResult{}, ErrBadRate
	}
	net, _, ok := mulDiv(depositSUP, BPS-mintFeeBPS, BPS)
	if !ok {
		return MintResult{}, ErrBadRate
	}
	// the units come from the whole deposit in one step (a rounded net would lose a unit of SUP)
	units, _, ok := mulDiv(depositSUP*(BPS-mintFeeBPS), r.Num(), r.Den()*BPS)
	if !ok {
		return MintResult{}, ErrBadRate
	}
	return MintResult{Units: units, BasisSUP: net, FeeSUP: depositSUP - net}, nil
}

// Terms are the money rules of a charter (config currency.*).
type Terms struct {
	// Fee is the charter fee (a sink), MinDeposit the smallest first deposit.
	Fee, MinDeposit int64
	// ShareBPS is how much of the treasury beyond the fee an automatic charter of an existing
	// settlement may deposit; Floor the least deposit worth chartering for.
	ShareBPS, Floor int64
}

// Plan is what a charter will cost.
type Plan struct {
	Fee, Deposit int64
	// OK is false when the treasury cannot pay a charter worth making: the head keeps the offer.
	OK bool
}

// PlanAuto decides an automatic charter from the treasury (the owner's rule of 2026-10-06):
//   - at founding, with a treasury that covers the fee and the minimum deposit, pay both in full;
//   - otherwise (an existing settlement, or a founding grant that does not cover both) charge the
//     fee and deposit min(MinDeposit, (treasury - fee) x ShareBPS / 10000), so the treasury is
//     never stripped of the SUP it still needs for materials;
//   - below Floor the charter is skipped and the head is left the offer.
func PlanAuto(treasury int64, t Terms, atFounding bool) Plan {
	if treasury < t.Fee || t.Fee < 0 || t.MinDeposit <= 0 {
		return Plan{}
	}
	if atFounding && treasury >= t.Fee+t.MinDeposit {
		return Plan{Fee: t.Fee, Deposit: t.MinDeposit, OK: true}
	}
	share, _, ok := mulDiv(treasury-t.Fee, t.ShareBPS, BPS)
	if !ok {
		return Plan{}
	}
	deposit := min(t.MinDeposit, share)
	if deposit < t.Floor || deposit <= 0 {
		return Plan{}
	}
	return Plan{Fee: t.Fee, Deposit: deposit, OK: true}
}

// RateOptions are the charter rates the ADR offers a head choosing by hand (units per SUP).
var RateOptions = []int64{1, 10, 100}

// ValidR0 reports whether r0 is one of the options.
func ValidR0(r0 int64) bool {
	for _, o := range RateOptions {
		if o == r0 {
			return true
		}
	}
	return false
}

// Desk fee bounds (ADR 0033 6.8: village.fx_fee_bps, 10 to 300 bps, default 30).
const (
	MinDeskFeeBPS     = 10
	MaxDeskFeeBPS     = 300
	DefaultDeskFeeBPS = 30
)

// ValidDeskFee reports whether a head may set this fee.
func ValidDeskFee(bps int64) bool { return bps >= MinDeskFeeBPS && bps <= MaxDeskFeeBPS }

// DeskBuy is what a player gets when they pay supIn SUP to the desk for the currency's units: the
// desk keeps the fee out of the SUP (rounded up), and the units are the rest at the live rate
// (rounded down).
func DeskBuy(supIn int64, r Rate, feeBPS int64) (units, feeSUP int64) {
	if supIn <= 0 || !r.Valid() || feeBPS < 0 || feeBPS > BPS {
		return 0, 0
	}
	fee, rem, ok := mulDiv(supIn, feeBPS, BPS)
	if !ok {
		return 0, 0
	}
	if rem > 0 {
		fee++
	}
	return r.ToLocalFloor(supIn - fee), fee
}

// DeskSell is the SUP a player gets when they give unitsIn units to the desk: the desk keeps the fee out
// of the units (rounded up), and the SUP is the rest at the live rate (rounded down). feeUnits is what
// the treasury keeps of the units.
func DeskSell(unitsIn int64, r Rate, feeBPS int64) (supOut, feeUnits int64) {
	if unitsIn <= 0 || !r.Valid() || feeBPS < 0 || feeBPS > BPS {
		return 0, 0
	}
	fee, rem, ok := mulDiv(unitsIn, feeBPS, BPS)
	if !ok {
		return 0, 0
	}
	if rem > 0 {
		fee++
	}
	return r.ToSUPFloor(unitsIn - fee), fee
}
