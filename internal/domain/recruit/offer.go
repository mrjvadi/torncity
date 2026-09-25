package recruit

import "fmt"

// Wage is a skill's market: what a specialist expects per period at level 1,
// and what each level above adds, in a city whose cost of living is the
// reference.
type Wage struct {
	Base, PerLevel int64
}

// At is the market wage of a level.
func (w Wage) At(level int) int64 {
	return w.Base + w.PerLevel*int64(max(level, 1)-1)
}

// Expected is what a specialist of level expects per period to work in a
// city whose cost of living is colBPS of the reference, from a pool whose
// scarcity is scarcityBPS: the market wage, scaled by both.
func Expected(w Wage, level int, colBPS, scarcityBPS int64) int64 {
	return max(Scale(w.At(level), colBPS, scarcityBPS), 1)
}

// Offer is a company's package, per specialist.
type Offer struct {
	// Salary and Housing are paid every period.
	Salary, Housing int64
	// Signing is paid once at hire; Relocation pays the move, up to what
	// it costs, once at hire.
	Signing, Relocation int64
	// Term is the contract, in periods.
	Term int
	// Equity is what the phantom shares granted are worth today: paid in
	// cash at the reference price when the contract is completed.
	Equity int64
}

// Validate refuses an offer the rules cannot weigh.
func (o Offer) Validate() error {
	for _, v := range []int64{o.Salary, o.Housing, o.Signing, o.Relocation, o.Equity} {
		if v < 0 || v > MaxWage*MaxTerm {
			return fmt.Errorf("%w: amount %d", ErrInvalid, v)
		}
	}
	if o.Salary < 1 || o.Term < 1 || o.Term > MaxTerm {
		return fmt.Errorf("%w: salary %d term %d", ErrInvalid, o.Salary, o.Term)
	}
	return nil
}

// Preference is what one kind of specialist values: each part of an offer
// weighed in basis points (BPS weighs it at face value), how much an unpaid
// part of the move hurts, and what each period of a longer contract adds
// (up to a cap).
type Preference struct {
	SalaryBPS, HousingBPS, SigningBPS, EquityBPS, MoveBPS int64
	TermPerPeriodBPS, TermCapBPS                          int64
}

// Value is the offer as a candidate with pref, whose move costs moveCost,
// weighs it per period: salary and housing as paid, the one-off parts spread
// over the contract, less the part of the move the offer leaves them to pay,
// raised by the security of a longer contract. Never below zero.
func Value(o Offer, pref Preference, moveCost int64) int64 {
	term := int64(max(o.Term, 1))
	v := Scale(o.Salary, pref.SalaryBPS) + Scale(o.Housing, pref.HousingBPS) +
		Scale(o.Signing, pref.SigningBPS)/term + Scale(o.Equity, pref.EquityBPS)/term
	if short := moveCost - min(o.Relocation, moveCost); short > 0 {
		v -= Scale(short, pref.MoveBPS) / term
	}
	if v <= 0 {
		return 0
	}
	security := min(pref.TermPerPeriodBPS*term, max(pref.TermCapBPS, 0))
	return v + Scale(v, max(security, 0))
}

// Curve turns an offer's value against the expectation into a chance: at or
// below FloorBPS of what they expect nobody applies; at FullBPS or above a
// candidate applies with MaxChanceBPS; in between, in proportion.
type Curve struct {
	FloorBPS, FullBPS, MaxChanceBPS int64
}

// Validate refuses a curve that could not rise.
func (c Curve) Validate() error {
	if c.FloorBPS < 0 || c.FullBPS <= c.FloorBPS || c.MaxChanceBPS < 1 || c.MaxChanceBPS > BPS {
		return fmt.Errorf("%w: curve %d..%d max %d", ErrInvalid, c.FloorBPS, c.FullBPS, c.MaxChanceBPS)
	}
	return nil
}

// RatioBPS is value against expected, in basis points.
func RatioBPS(value, expected int64) int64 {
	if expected <= 0 {
		return 0
	}
	return mulDiv(value, BPS, expected)
}

// Chance is the chance, in basis points, that a candidate who values an
// offer at value against an expectation of expected applies, before the
// factors of where and for whom (Scale).
func (c Curve) Chance(value, expected int64) int64 {
	r := RatioBPS(value, expected)
	switch {
	case r <= c.FloorBPS:
		return 0
	case r >= c.FullBPS:
		return c.MaxChanceBPS
	}
	return mulDiv(c.MaxChanceBPS, r-c.FloorBPS, c.FullBPS-c.FloorBPS)
}

// Move is what moving to the company's city costs a specialist.
type Move struct {
	Base, PerKM, Abroad int64
}

// Cost is the move from a city km away; nothing for a specialist of the
// company's own city, and Abroad on top for one from another country.
func (m Move) Cost(sameCity, sameCountry bool, km int) int64 {
	if sameCity {
		return 0
	}
	c := m.Base + m.PerKM*int64(max(km, 0))
	if !sameCountry {
		c += m.Abroad
	}
	return c
}

// Reputation is what a company's standing adds to a candidate's
// willingness: minBPS for an unknown company, perStarBPS for each star of
// its rating (1..5, 0 unrated), perStaffBPS for each person working for it,
// up to staffCapBPS.
func Reputation(minBPS, perStarBPS, stars, perStaffBPS, staffCapBPS, staff int64) int64 {
	return minBPS + perStarBPS*max(stars, 0) + min(perStaffBPS*max(staff, 0), max(staffCapBPS, 0))
}

// Pick chooses by weight: the index whose share of the total covers roll
// (in [0, BPS)). Weights at or below zero are never picked; with none
// positive it is 0.
func Pick(weights []int64, roll int64) int {
	var total int64
	for _, w := range weights {
		total += max(w, 0)
	}
	if total <= 0 {
		return 0
	}
	at := mulDiv(min(max(roll, 0), BPS-1), total, BPS)
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		if at < w {
			return i
		}
		at -= w
	}
	return len(weights) - 1
}
