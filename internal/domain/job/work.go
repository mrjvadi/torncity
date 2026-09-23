package job

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
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

	// ErrShiftInProgress means the player is already working a shift. One
	// at a time: the shift_sessions table enforces the same rule with a
	// partial unique index.
	ErrShiftInProgress = errors.New("job: a shift is already in progress")

	// ErrNoShiftInProgress means there is no shift to settle.
	ErrNoShiftInProgress = errors.New("job: no shift in progress")

	// ErrShiftNotFinished means the shift has not run its length yet. The
	// detail is a ShiftNotFinished carrying the time left.
	ErrShiftNotFinished = errors.New("job: shift not finished yet")
)

// ShiftNotFinished details ErrShiftNotFinished.
type ShiftNotFinished struct{ Remaining time.Duration }

func (e ShiftNotFinished) Error() string {
	return fmt.Sprintf("%v: %s to go", ErrShiftNotFinished, e.Remaining)
}

// Unwrap lets errors.Is match ErrShiftNotFinished.
func (e ShiftNotFinished) Unwrap() error { return ErrShiftNotFinished }

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
	// The window is GAME time, like the shifts it counts; it meets the wall
	// clock through gametime.Scale.RealWait.
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
// at now given the shifts already worked. clock maps the game-time window to
// the real one the recorded start times are compared on.
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
func (p Policy) Fatigue(recent []time.Time, now time.Time, clock gametime.Scale) int {
	if p.FatigueWindow <= 0 {
		return bpsWhole
	}
	k := shiftsInWindow(recent, now, clock.RealWait(p.FatigueWindow))
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
	// window. FinishShift returns it already pruned; the caller just stores
	// it.
	RecentShifts []time.Time
}

// Shift is everything starting a shift of work reads.
type Shift struct {
	Career     Career
	Employment Employment
	Stats      player.Stats
	Skills     []player.Skill
	Policy     Policy
	// Current is the shift the player is already working, the zero value
	// when none. A player works one shift at a time.
	Current Activity
	Now     time.Time
	// Clock maps the tier's shift duration and the fatigue window, both
	// game time, to the real wait.
	Clock gametime.Scale
}

// Activity is one shift from its start to its end: what was fixed when it
// started and is settled when it ends. The zero value is "not working".
type Activity struct {
	// Tier is the tier the shift was started in; its rewards are the ones
	// paid.
	Tier      int
	StartedAt time.Time
	// EndsAt is StartedAt plus the tier's shift duration on the game clock.
	EndsAt time.Time
	// FatigueBPS is the output multiplier the shift runs at, decided when
	// it starts from the shifts before it.
	FatigueBPS int
}

// Active reports whether the activity is a shift in progress.
func (a Activity) Active() bool { return !a.StartedAt.IsZero() }

// Remaining is how long until the shift can be settled, zero once it can.
func (a Activity) Remaining(now time.Time) time.Duration {
	if !a.Active() || !now.Before(a.EndsAt) {
		return 0
	}
	return a.EndsAt.Sub(now)
}

// ShiftStarted is everything starting a shift changes: the energy spent and
// the shift now in progress. The caller persists both or neither, and
// schedules the shift's end at Activity.EndsAt.
type ShiftStarted struct {
	Stats    player.Stats
	Activity Activity
}

// StartShift begins one shift at s.Now.
//
// WHY A SHIFT TAKES TIME. A shift that paid on the press could be pressed as
// fast as a thumb moves. So a shift is an activity: its energy is paid when
// it starts, the player is at work until it ends, and its pay, XP, skill XP
// and performance are settled only then, by FinishShift. How long it lasts
// is content (the tier's ShiftDuration, game time), mapped to the real wait
// by the game's one clock.
//
// ENERGY. The tier's energy cost is paid through player.Stats.SpendEnergy, so
// a shift the player cannot afford is refused with player.ErrNotEnoughEnergy —
// never run for less. A player already working is refused with
// ErrShiftInProgress. A refusal returns a zero ShiftStarted.
//
// FATIGUE is decided here, from the shifts already recorded inside the
// window ending now, and carried on the activity: the shift the player
// started is the shift they are paid for.
func StartShift(s Shift) (ShiftStarted, error) {
	if s.Now.IsZero() {
		return ShiftStarted{}, ErrInvalidTime
	}
	if err := s.Clock.Validate(); err != nil {
		return ShiftStarted{}, err
	}
	if s.Current.Active() {
		return ShiftStarted{}, ErrShiftInProgress
	}
	if err := s.Policy.Validate(); err != nil {
		return ShiftStarted{}, err
	}
	tier, err := employedTier(s.Career, s.Employment)
	if err != nil {
		return ShiftStarted{}, err
	}
	stats, err := s.Stats.SpendEnergy(tier.EnergyCost)
	if err != nil {
		return ShiftStarted{}, err
	}
	return ShiftStarted{
		Stats: stats,
		Activity: Activity{
			Tier:       s.Employment.Tier,
			StartedAt:  s.Now,
			EndsAt:     s.Now.Add(s.Clock.RealWait(tier.ShiftDuration)),
			FatigueBPS: s.Policy.Fatigue(s.Employment.RecentShifts, s.Now, s.Clock),
		},
	}, nil
}

// employedTier checks a career and an employment belong together and
// returns the employment's tier.
func employedTier(c Career, e Employment) (Tier, error) {
	if err := c.Validate(); err != nil {
		return Tier{}, err
	}
	if e.CareerCode != c.Code {
		return Tier{}, fmt.Errorf("%w: employment %q, career %q", ErrWrongCareer, e.CareerCode, c.Code)
	}
	if e.Rate.IsNegative() || e.Rate.Minor() > MaxBaseSalary {
		return Tier{}, fmt.Errorf("%w: %s", ErrInvalidRate, e.Rate)
	}
	return c.tier(e.Tier)
}

// Finish is everything settling a shift reads: the shift in progress, and
// the job and the player as they are when it ends.
type Finish struct {
	Career     Career
	Employment Employment
	Stats      player.Stats
	Skills     []player.Skill
	Policy     Policy
	Activity   Activity
	Now        time.Time
	Clock      gametime.Scale
}

// ShiftResult is everything settling a shift changes. The caller persists
// all of it or none of it.
type ShiftResult struct {
	// Stats has the XP added. Energy was spent when the shift started.
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

// FinishShift settles a shift that has run its course.
//
// Before EndsAt it is refused with a ShiftNotFinished carrying the time left:
// nothing is paid pro rata. The rewards are those of the tier the shift was
// started in.
//
// PAY. The shift pays the employment's rate, raised to the minimum wage in
// force if policy moved above it since hiring, scaled by the fatigue fixed
// when the shift started:
//
//	pay = floor(max(rate, minimumWage) * fatigueBPS / 10000)
//
// Integers throughout, via a 128-bit intermediate so no rate can overflow.
// Flooring rounds against the employee by under one minor unit, and only on a
// fatigued shift: at full output the multiplier is exactly one.
//
// XP AND SKILL XP scale by the same fatigue multiplier with the same rounding.
// PERFORMANCE moves as described at perfBelowRequirement and is clamped to
// 0..MaxPerformance. The shift is recorded in the fatigue history at the
// moment it STARTED.
func FinishShift(f Finish) (ShiftResult, error) {
	if f.Now.IsZero() {
		return ShiftResult{}, ErrInvalidTime
	}
	if err := f.Clock.Validate(); err != nil {
		return ShiftResult{}, err
	}
	if !f.Activity.Active() {
		return ShiftResult{}, ErrNoShiftInProgress
	}
	if f.Now.Before(f.Activity.EndsAt) {
		return ShiftResult{}, ShiftNotFinished{Remaining: f.Activity.EndsAt.Sub(f.Now)}
	}
	fatigue := f.Activity.FatigueBPS
	if fatigue < 1 || fatigue > bpsWhole {
		return ShiftResult{}, fmt.Errorf("%w: fatigue %d bps", ErrInvalidPolicy, fatigue)
	}
	if err := f.Policy.Validate(); err != nil {
		return ShiftResult{}, err
	}
	if _, err := employedTier(f.Career, f.Employment); err != nil {
		return ShiftResult{}, err
	}
	tier, err := f.Career.tier(f.Activity.Tier)
	if err != nil {
		return ShiftResult{}, err
	}

	rate := f.Employment.Rate.Minor()
	if floor := f.Policy.MinimumWage.Minor(); rate < floor {
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

	delta := performanceDelta(tier, f.Skills, fatigue < bpsWhole)
	stats, ups := f.Stats.AddXP(xp)
	window := f.Clock.RealWait(f.Policy.FatigueWindow)

	return ShiftResult{
		Stats:            stats,
		LevelUps:         ups,
		Employment:       f.Employment.afterShift(delta, f.Activity.StartedAt, window),
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
