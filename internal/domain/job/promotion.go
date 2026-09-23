package job

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

// Sentinel errors for a promotion that is not yet earned. Like the
// eligibility errors, each points at a different thing to do next.
var (
	// ErrTopTier means the employee already holds the career's last tier.
	ErrTopTier = errors.New("job: already at the top of this career")

	// ErrPerformanceTooLow means performance is under the tier's promotion
	// bar. The detail is a PerformanceShortfall.
	ErrPerformanceTooLow = errors.New("job: performance below the promotion bar")

	// ErrTooSoon means the employee has not held the tier long enough. The
	// detail is a TimeShortfall.
	ErrTooSoon = errors.New("job: not long enough in this tier")

	// ErrNotEnoughShifts means too few shifts were worked in the tier. The
	// detail is a ShiftShortfall.
	ErrNotEnoughShifts = errors.New("job: not enough shifts worked in this tier")
)

// PerformanceShortfall details ErrPerformanceTooLow.
type PerformanceShortfall struct{ Need, Have int }

func (e PerformanceShortfall) Error() string {
	return fmt.Sprintf("%v: need %d, have %d", ErrPerformanceTooLow, e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrPerformanceTooLow.
func (e PerformanceShortfall) Unwrap() error { return ErrPerformanceTooLow }

// TimeShortfall details ErrTooSoon. Remaining is the REAL time until the
// requirement is met, so the UI can say "eligible in 3 minutes".
type TimeShortfall struct{ Remaining time.Duration }

func (e TimeShortfall) Error() string {
	return fmt.Sprintf("%v: %s to go", ErrTooSoon, e.Remaining)
}

// Unwrap lets errors.Is match ErrTooSoon.
func (e TimeShortfall) Unwrap() error { return ErrTooSoon }

// ShiftShortfall details ErrNotEnoughShifts.
type ShiftShortfall struct{ Need, Have int }

func (e ShiftShortfall) Error() string {
	return fmt.Sprintf("%v: need %d, have %d", ErrNotEnoughShifts, e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrNotEnoughShifts.
func (e ShiftShortfall) Unwrap() error { return ErrNotEnoughShifts }

// Promotion reports whether an employee has earned the next tier of career,
// and if not, why.
//
// Two sets of requirements apply together:
//
//  1. the CURRENT tier's promotion bar — performance, time in tier, shifts
//     worked — which says the employee has proven themselves where they are;
//  2. the NEXT tier's entry requirements — level, skills, certifications —
//     which say they can do the job above.
//
// Residency is not rechecked: the employee already works here legally.
//
// Every boundary is inclusive: exactly the required performance, time or
// shift count qualifies. When not eligible, reason joins every unmet
// requirement (see Eligibility for why all of them), bar first, then entry.
// A broken question — invalid career, wrong career, unknown tier, zero time —
// returns false with that error alone.
func Promotion(career Career, e Employment, c Candidate, now time.Time, clock gametime.Scale) (eligible bool, reason error) {
	if now.IsZero() {
		return false, ErrInvalidTime
	}
	if err := clock.Validate(); err != nil {
		return false, err
	}
	if err := career.Validate(); err != nil {
		return false, err
	}
	if e.CareerCode != career.Code {
		return false, fmt.Errorf("%w: employment %q, career %q", ErrWrongCareer, e.CareerCode, career.Code)
	}
	current, err := career.tier(e.Tier)
	if err != nil {
		return false, err
	}
	if e.Tier == len(career.Tiers)-1 {
		return false, ErrTopTier
	}
	next := career.Tiers[e.Tier+1]

	var errs []error
	bar := current.Promotion
	if perf := clampPerformance(e.Performance); perf < bar.MinPerformance {
		errs = append(errs, PerformanceShortfall{Need: bar.MinPerformance, Have: perf})
	}
	// The bar is game time; the tier clock is the wall clock. A TierSince in
	// the future (a clock correction) is simply "not yet".
	if need, held := clock.RealWait(bar.MinTimeInTier), now.Sub(e.TierSince); held < need {
		errs = append(errs, TimeShortfall{Remaining: need - held})
	}
	if e.ShiftsInTier < bar.MinShifts {
		errs = append(errs, ShiftShortfall{Need: bar.MinShifts, Have: e.ShiftsInTier})
	}
	errs = append(errs, requirementGaps(next, c.Stats, c.Skills, c.Certifications)...)

	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}
	return true, nil
}

// Promote moves an eligible employee up one tier at now, or refuses with the
// reason from Promotion.
//
// The rate becomes the higher of the current rate and the new tier's base
// salary: a promotion never cuts pay. Performance carries over, because it is
// a record of how this person works rather than of one position, and so does
// the fatigue history, because the same body works the next shift. The tier
// clock and shift count restart.
func Promote(career Career, e Employment, c Candidate, now time.Time, clock gametime.Scale) (Employment, error) {
	ok, reason := Promotion(career, e, c, now, clock)
	if !ok {
		return Employment{}, reason
	}
	next := e
	next.Tier = e.Tier + 1
	next.TierSince = now
	next.ShiftsInTier = 0
	if base := career.Tiers[next.Tier].BaseSalary; base.Minor() > e.Rate.Minor() {
		next.Rate = base
	}
	if e.RecentShifts != nil {
		next.RecentShifts = append([]time.Time(nil), e.RecentShifts...)
	}
	return next, nil
}
