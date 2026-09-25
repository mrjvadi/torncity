// Package budget holds the rules of spending a city's treasury: every city
// period, once, the budget — a share of the treasury's balance — is divided
// among the lines by the city's allocation, each line is paid its part, and
// what a line was paid buys an effect for the period that follows: the
// police solve more cases, the city hospital charges less, public transport
// is cheaper, war damage is repaired, the city's shops see more customers. A
// defence line's money goes to the country's defence fund instead.
//
// Which lines exist, what each buys and how much buys all of it are CONTENT
// (configs/content/budget.yml); how the budget is divided is the city's
// POLICY (an allocation lever). The package reads no clock and no file.
//
// # Bounded
//
// Every effect is bounded twice: by the line's max_bps, whatever is spent,
// and by the whole the allocation may divide. Spending is bounded by the
// treasury: the budget is spend_share_bps of what it holds, never more, and
// an allocation summing to less than the whole leaves the rest unspent.
package budget

import (
	"errors"
	"fmt"
)

// Effects: the closed set of what a line's spending does. Code reads each.
const (
	// EffectInvestigation raises the chance the police solve a reported
	// case: added to the police chief's investigation effort.
	EffectInvestigation = "investigation"
	// EffectHospitalPrice lowers what the city hospital charges.
	EffectHospitalPrice = "hospital_price"
	// EffectTransitFare lowers the share of the full fare a rider pays on
	// public transport.
	EffectTransitFare = "transit_fare"
	// EffectCourseFee lowers the fee of every course taken in the city.
	EffectCourseFee = "course_fee"
	// EffectWarRepair repairs the city's war damage: that many bps of
	// damage come off at the settlement, on top of healing with time.
	EffectWarRepair = "war_repair"
	// EffectNPCDemand raises what the city's population spends at its
	// companies in a period.
	EffectNPCDemand = "npc_demand"
	// EffectDefenceFund pays the line's money into the country's defence
	// fund; it buys no bps.
	EffectDefenceFund = "defence_fund"
)

// Effects lists every effect, in a fixed order.
var Effects = []string{EffectInvestigation, EffectHospitalPrice, EffectTransitFare, EffectCourseFee,
	EffectWarRepair, EffectNPCDemand, EffectDefenceFund}

// ErrInvalidRules means the budget content is unusable.
var ErrInvalidRules = errors.New("budget: invalid rules")

// BasisPoints is the whole.
const BasisPoints = 10000

// Line is one line of the budget.
type Line struct {
	Code   string
	Effect string
	// FullAt is the spending in one period that buys the whole effect;
	// MaxBPS is the whole effect.
	FullAt int64
	MaxBPS int64
}

// Rules are the budget's content.
type Rules struct {
	// SpendShareBPS is the most of the treasury one period spends.
	SpendShareBPS int64
	Lines         []Line
}

// Validate checks the rules.
func (r Rules) Validate() error {
	if r.SpendShareBPS < 1 || r.SpendShareBPS > BasisPoints {
		return fmt.Errorf("%w: spend_share_bps %d is outside 1..%d", ErrInvalidRules, r.SpendShareBPS, BasisPoints)
	}
	if len(r.Lines) == 0 {
		return fmt.Errorf("%w: no lines", ErrInvalidRules)
	}
	seen := map[string]bool{}
	for _, l := range r.Lines {
		if l.Code == "" || seen[l.Code] {
			return fmt.Errorf("%w: line %q is empty or repeated", ErrInvalidRules, l.Code)
		}
		seen[l.Code] = true
		known := false
		for _, e := range Effects {
			known = known || e == l.Effect
		}
		if !known {
			return fmt.Errorf("%w: line %q has unknown effect %q", ErrInvalidRules, l.Code, l.Effect)
		}
		if l.Effect == EffectDefenceFund {
			if l.FullAt != 0 || l.MaxBPS != 0 {
				return fmt.Errorf("%w: line %q pays the defence fund and buys no bps", ErrInvalidRules, l.Code)
			}
			continue
		}
		if l.FullAt < 1 || l.MaxBPS < 1 || l.MaxBPS > BasisPoints {
			return fmt.Errorf("%w: line %q: full_at %d must be positive and max_bps %d within 1..%d",
				ErrInvalidRules, l.Code, l.FullAt, l.MaxBPS, BasisPoints)
		}
	}
	return nil
}

// Paid is one line's part of a period.
type Paid struct {
	Code      string
	Effect    string
	ShareBPS  int64
	Spent     int64
	EffectBPS int64
}

// Period is one period's budget: what the treasury held, what the budget
// could spend, each line's part, and the total.
type Period struct {
	Treasury  int64
	Spendable int64
	Spent     int64
	// Defence is the part paid into the defence fund.
	Defence int64
	Lines   []Paid
}

// Spend divides one period's budget by the allocation. shares names a line's
// share of the budget in bps; a line it does not name has none. A share of a
// code that is no line is ignored (the resolver has already refused one). It
// never spends more than Spendable, which is never more than the treasury.
func Spend(r Rules, treasury int64, shares map[string]int64) Period {
	p := Period{Treasury: max(treasury, 0)}
	p.Spendable = p.Treasury * r.SpendShareBPS / BasisPoints
	left := p.Spendable
	for _, l := range r.Lines {
		share := min(max(shares[l.Code], 0), BasisPoints)
		spent := min(p.Spendable*share/BasisPoints, left)
		left -= spent
		paid := Paid{Code: l.Code, Effect: l.Effect, ShareBPS: share, Spent: spent, EffectBPS: EffectOf(l, spent)}
		p.Spent += spent
		if l.Effect == EffectDefenceFund {
			p.Defence += spent
		}
		p.Lines = append(p.Lines, paid)
	}
	return p
}

// EffectOf is what spending buys on a line: the whole effect at FullAt or
// more, a proportional part below it.
func EffectOf(l Line, spent int64) int64 {
	if l.Effect == EffectDefenceFund || spent <= 0 || l.FullAt <= 0 {
		return 0
	}
	if spent >= l.FullAt {
		return l.MaxBPS
	}
	return l.MaxBPS * spent / l.FullAt
}

// Lower applies a reduction effect to an amount: bps off, never below zero.
func Lower(amount, effectBPS int64) int64 {
	effectBPS = min(max(effectBPS, 0), BasisPoints)
	return amount - amount*effectBPS/BasisPoints
}

// Raise applies an increase effect to an amount: bps on top.
func Raise(amount, effectBPS int64) int64 {
	effectBPS = max(effectBPS, 0)
	return amount + amount*effectBPS/BasisPoints
}
