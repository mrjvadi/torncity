// Package achievement holds the rules of achievements: a player earns one
// when enough of the game's own events of its kind are theirs — the first
// shift worked, a company founded, an election won, ten crimes pulled off —
// once, ever. Its cash, when it has any, enters the economy (ADR 0009
// achievement_reward), capped twice a UTC day, per player and for everyone;
// what the caps keep back is not paid later.
//
// Which achievements exist, what each counts and gives is CONTENT
// (configs/content/achievements.yml); which events there are to count is a
// closed set in code, because code reads each one. The package reads no
// clock and no file.
package achievement

import (
	"errors"
	"fmt"
)

// Kinds of event an achievement may count.
const (
	ShiftWorked     = "shift_worked"
	CompanyFounded  = "company_founded"
	ElectionWon     = "election_won"
	CrimeSucceeded  = "crime_succeeded"
	PropertyBought  = "property_bought"
	JourneyMade     = "journey_made"
	CourseCompleted = "course_completed"
	LawPassed       = "law_passed"
)

// Kinds lists every kind.
var Kinds = []string{ShiftWorked, CompanyFounded, ElectionWon, CrimeSucceeded, PropertyBought, JourneyMade,
	CourseCompleted, LawPassed}

// ErrInvalid means an achievement the rules cannot use.
var ErrInvalid = errors.New("achievement: invalid")

// Achievement is one achievement.
type Achievement struct {
	Code  string
	Event string
	// Count is how many events of its kind earn it.
	Count int64
	// Reward is the cash it pays, minor units; zero for none.
	Reward int64
}

// Validate checks an achievement.
func (a Achievement) Validate() error {
	known := false
	for _, k := range Kinds {
		known = known || k == a.Event
	}
	switch {
	case a.Code == "":
		return fmt.Errorf("%w: an achievement with no code", ErrInvalid)
	case !known:
		return fmt.Errorf("%w: %q counts unknown event %q", ErrInvalid, a.Code, a.Event)
	case a.Count < 1 || a.Count > 1_000_000:
		return fmt.Errorf("%w: %q count %d", ErrInvalid, a.Code, a.Count)
	case a.Reward < 0 || a.Reward > 10_000_000:
		return fmt.Errorf("%w: %q reward %d", ErrInvalid, a.Code, a.Reward)
	}
	return nil
}

// Reached reports whether a count of events earns the achievement.
func (a Achievement) Reached(count int64) bool { return count >= a.Count }

// CapCash is what of a reward is paid, and what withheld, when the player
// may still receive playerLeft today and the whole economy economyLeft.
func CapCash(cash, playerLeft, economyLeft int64) (paid, withheld int64) {
	if cash <= 0 {
		return 0, 0
	}
	paid = min(cash, max(playerLeft, 0), max(economyLeft, 0))
	return paid, cash - paid
}
