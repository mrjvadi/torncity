package job

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// More sentinel errors, for working a shift.
var (
	// ErrWrongCareer means an employment was presented with a career it does
	// not belong to — the caller looked up the wrong content.
	ErrWrongCareer = errors.New("job: employment is not in this career")

	// ErrInvalidPolicy means a policy's numbers are unusable.
	ErrInvalidPolicy = errors.New("job: invalid policy")
)

// StartingPerformance is where a new hire's performance starts, matching the
// employees.performance column default. Halfway leaves room to show both a
// good start and a bad one.
const StartingPerformance = 50

// Performance rule. Each shift moves performance by a small step decided by
// how far the employee's weakest required skill sits above what the tier asks:
//
//	below a requirement         -3   (content was raised past them; they struggle)
//	at or up to 9 levels above  +1
//	10 or more levels above     +2   (comfortably overqualified)
//	fatigued shift              -2 on top of the above
//
// A tier with no required skills counts as "at the requirement": +1. The steps
// are small on purpose: performance is a record of a habit, and one good or
// bad day should not decide a promotion.
const (
	perfBelowRequirement = -3
	perfQualified        = 1
	perfExpert           = 2
	perfExpertMargin     = 10
	perfFatiguePenalty   = 2
)

// Policy is the set of levers, held by offices under ADR 0015, that decide how
// work pays in one jurisdiction. None of them is a constant in this package.
type Policy struct {
	// MinimumWage is the least one shift may pay, in minor units.
	MinimumWage money.Amount

	// FatigueWindow and FatigueFreeShifts define fatigue: within any window
	// of FatigueWindow, the first FatigueFreeShifts shifts are at full output
	// and every one after that yields less. A zero window disables fatigue.
	FatigueWindow     time.Duration
	FatigueFreeShifts int
}

// Validate reports whether a policy is usable.
func (p Policy) Validate() error {
	if p.MinimumWage.IsNegative() || p.MinimumWage.Minor() > MaxBaseSalary {
		return fmt.Errorf("%w: minimum wage %s", ErrInvalidPolicy, p.MinimumWage)
	}
	if p.FatigueWindow < 0 {
		return fmt.Errorf("%w: fatigue window %s", ErrInvalidPolicy, p.FatigueWindow)
	}
	if p.FatigueFreeShifts < 0 || p.FatigueFreeShifts > MaxRequiredShifts {
		return fmt.Errorf("%w: fatigue free shifts %d", ErrInvalidPolicy, p.FatigueFreeShifts)
	}
	if p.FatigueWindow > 0 && p.FatigueFreeShifts == 0 {
		// Zero free shifts would make the very first shift of a fresh day a
		// fatigued one, which is a typo, not a policy.
		return fmt.Errorf("%w: fatigue window set with no free shifts", ErrInvalidPolicy)
	}
	return nil
}

// Fatigue returns the output multiplier, in basis points, for a shift started
// at now given the shifts already worked.
//
// Let k be how many earlier shifts fall inside the window ending at now, and
// N the free shifts. While k < N the shift is at full output. Past that, with
// e = k - N + 1 excess shifts including this one, output is
//
//	10000 / (1 + e)   →   50%, 33%, 25%, 20%, …
//
// A harmonic curve rather than halving: it makes each extra shift clearly
// worse than the last while falling slowly enough that a player who works one
// shift too many is not wiped out — but grinding is never worth it. The
// multiplier is floored at 1 bps, so the curve itself never reaches zero even
// for an absurd number of shifts.
//
// A recorded shift later than now counts as inside the window. That only
// happens after a clock correction, and counting it is the conservative
// choice: ignoring it would let a clock step reset fatigue.
func (p Policy) Fatigue(recent []time.Time, now time.Time) int {
	if p.FatigueWindow <= 0 {
		return bpsWhole
	}
	k := shiftsInWindow(recent, now, p.FatigueWindow)
	if k < p.FatigueFreeShifts {
		return bpsWhole
	}
	e := k - p.FatigueFreeShifts + 1
	if m := bpsWhole / (1 + e); m > 0 {
		return m
	}
	return 1
}

func shiftsInWindow(recent []time.Time, now time.Time, window time.Duration) int {
	start := now.Add(-window)
	n := 0
	for _, t := range recent {
		if t.After(start) {
			n++
		}
	}
	return n
}

// Employment is one player's standing in one position, mirroring the
// employees row plus what the rules here need to remember between shifts.
type Employment struct {
	CareerCode string
	// Tier is an index into Career.Tiers.
	Tier int
	// Rate is the agreed pay for one full-output shift, in minor units.
	Rate money.Amount
	// Performance is on the 0..MaxPerformance scale.
	Performance int
	// TierSince is when the employee reached the current tier.
	TierSince time.Time
	// ShiftsInTier counts shifts worked since TierSince.
	ShiftsInTier int
	// RecentShifts are the start times of shifts still inside the fatigue
	// window. Work returns it already pruned; the caller just stores it.
	RecentShifts []time.Time
}

// Shift is everything a shift of work reads.
type Shift struct {
	Career     Career
	Employment Employment
	Stats      player.Stats
	Skills     []player.Skill
	Policy     Policy
	Now        time.Time
}

// ShiftResult is everything a shift of work changes. The caller persists all
// of it or none of it.
type ShiftResult struct {
	// Stats has the energy spent and the XP added.
	Stats    player.Stats
	LevelUps []player.LevelUp
	// Employment has performance applied and this shift recorded.
	Employment Employment

	Pay money.Amount
	XP  int64
	// SkillXP is what to award to each skill, in authored order, with
	// player.Skill.AddSkillXP. Skills live in their own rows, so applying it
	// is the caller's job.
	SkillXP []SkillXP

	PerformanceDelta int
	// FatigueBPS is the output multiplier this shift ran at, for the UI.
	FatigueBPS int
}

// Work performs one shift.
//
// ENERGY. The tier's energy cost is paid through player.Stats.SpendEnergy, so
// a shift the player cannot afford is refused with player.ErrNotEnoughEnergy —
// never run for less. A refused shift returns a zero ShiftResult: nothing is
// paid, awarded, or recorded.
//
// PAY. The shift pays the employment's rate, raised to the minimum wage in
// force if policy moved above it since hiring, scaled by fatigue:
//
//	pay = floor(max(rate, minimumWage) * fatigueBPS / 10000)
//
// Integers throughout, via a 128-bit intermediate so no rate can overflow.
// Flooring rounds against the employee by under one minor unit, and only on a
// fatigued shift: at full output the multiplier is exactly one.
//
// XP AND SKILL XP scale by the same fatigue multiplier with the same rounding.
// PERFORMANCE moves as described at perfBelowRequirement and is clamped to
// 0..MaxPerformance.
func Work(s Shift) (ShiftResult, error) {
	if s.Now.IsZero() {
		return ShiftResult{}, ErrInvalidTime
	}
	if err := s.Policy.Validate(); err != nil {
		return ShiftResult{}, err
	}
	if err := s.Career.Validate(); err != nil {
		return ShiftResult{}, err
	}
	if s.Employment.CareerCode != s.Career.Code {
		return ShiftResult{}, fmt.Errorf("%w: employment %q, career %q",
			ErrWrongCareer, s.Employment.CareerCode, s.Career.Code)
	}
	tier, err := s.Career.tier(s.Employment.Tier)
	if err != nil {
		return ShiftResult{}, err
	}
	if s.Employment.Rate.IsNegative() || s.Employment.Rate.Minor() > MaxBaseSalary {
		return ShiftResult{}, fmt.Errorf("%w: %s", ErrInvalidRate, s.Employment.Rate)
	}

	stats, err := s.Stats.SpendEnergy(tier.EnergyCost)
	if err != nil {
		return ShiftResult{}, err
	}

	fatigue := s.Policy.Fatigue(s.Employment.RecentShifts, s.Now)

	rate := s.Employment.Rate.Minor()
	if floor := s.Policy.MinimumWage.Minor(); rate < floor {
		rate = floor
	}
	pay, err := mulDiv(rate, int64(fatigue), bpsWhole)
	if err != nil {
		return ShiftResult{}, err
	}
	xp, err := mulDiv(tier.XPPerShift, int64(fatigue), bpsWhole)
	if err != nil {
		return ShiftResult{}, err
	}
	var skillXP []SkillXP
	for _, r := range tier.SkillXPPerShift {
		v, err := mulDiv(r.XP, int64(fatigue), bpsWhole)
		if err != nil {
			return ShiftResult{}, err
		}
		skillXP = append(skillXP, SkillXP{Skill: r.Skill, XP: v})
	}

	delta := performanceDelta(tier, s.Skills, fatigue < bpsWhole)
	stats, ups := stats.AddXP(xp)

	return ShiftResult{
		Stats:            stats,
		LevelUps:         ups,
		Employment:       s.Employment.afterShift(delta, s.Now, s.Policy.FatigueWindow),
		Pay:              money.FromMinor(pay),
		XP:               xp,
		SkillXP:          skillXP,
		PerformanceDelta: delta,
		FatigueBPS:       fatigue,
	}, nil
}

// performanceDelta applies the performance rule documented above.
func performanceDelta(t Tier, skills []player.Skill, fatigued bool) int {
	delta := perfQualified
	if len(t.RequiredSkills) > 0 {
		levels := skillLevels(skills)
		margin := math.MaxInt
		for _, r := range t.RequiredSkills {
			if m := levels[r.Skill] - r.Level; m < margin {
				margin = m
			}
		}
		switch {
		case margin < 0:
			delta = perfBelowRequirement
		case margin >= perfExpertMargin:
			delta = perfExpert
		}
	}
	if fatigued {
		delta -= perfFatiguePenalty
	}
	return delta
}

// clampPerformance keeps a performance value on the 0..MaxPerformance scale.
func clampPerformance(p int) int {
	switch {
	case p < 0:
		return 0
	case p > MaxPerformance:
		return MaxPerformance
	}
	return p
}

// afterShift records a shift at now: performance moved, the shift counted, and
// the fatigue history pruned to what the window still needs. It builds a new
// slice so the caller's RecentShifts is never written through.
func (e Employment) afterShift(delta int, now time.Time, window time.Duration) Employment {
	next := e
	next.Performance = clampPerformance(clampPerformance(e.Performance) + delta)
	if next.ShiftsInTier < math.MaxInt {
		next.ShiftsInTier++
	}
	next.RecentShifts = nil
	if window > 0 {
		start := now.Add(-window)
		kept := make([]time.Time, 0, len(e.RecentShifts)+1)
		for _, t := range e.RecentShifts {
			if t.After(start) {
				kept = append(kept, t)
			}
		}
		next.RecentShifts = append(kept, now)
	}
	return next
}
