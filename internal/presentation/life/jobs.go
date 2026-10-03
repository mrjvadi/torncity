package life

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Callback addresses of the work and study screens.
const (
	AddrJobView     = "job:view"
	AddrJobApply    = "job:apply"
	AddrJobWork     = "job:work"
	AddrJobPromote  = "job:promote"
	AddrJobQuit     = "job:quit"
	AddrCourseView  = "education:view"
	AddrCourseEnrol = "education:enroll"
	// Teaching (education.hire, education.teach, education.unteach).
	AddrCourseHire  = "education:hire"
	AddrCourseTeach = "education:teach"
	AddrCourseLeave = "education:unteach"
)

// QuitConfirmation is the argument that turns job.quit from "are you sure"
// into the resignation itself.
const QuitConfirmation = "yes"

// JobStatusView is the player's job.
type JobStatusView struct {
	// Employed is false for a player with no job; nothing else is set then.
	Employed bool
	Job      JobRef
	// Employer is the company the job is at; empty at the city's base
	// employer.
	Employer string
	// CityCode and City are where the job is.
	CityCode string
	City     string
	// Pay is what a full-output shift pays now, minimum wage applied.
	Pay        int64
	EnergyCost int
	Energy     int
	MaxEnergy  int
	// Performance is on the 0..100 scale.
	Performance  int
	ShiftsInTier int
	TotalEarned  int64
	// AtWorkplace is whether the player stands in the job's city and is not
	// travelling, so a shift can be worked from here.
	AtWorkplace bool
	// TopTier means there is no next position.
	TopTier bool
	// ShiftLength is how long one shift of this position takes, the real
	// wait on the game clock.
	ShiftLength time.Duration
	// Workplace is the place of the city the job is worked at, when the
	// city has places; WalkToWork the walk there from where the player
	// stands, zero when they are there. The start button walks first.
	Workplace  Named
	WalkToWork time.Duration
	// Shift is the shift in progress, nil when the player is not working.
	Shift *ShiftProgress
	// Next is the next position; PromotionReady says it has been earned and
	// Missing lists what is still needed otherwise.
	Next           JobRef
	PromotionReady bool
	Missing        []Requirement
}

// ShiftProgress is a shift the player is working.
type ShiftProgress struct {
	// Remaining is the real time until it ends.
	Remaining time.Duration
	// EndsAt is when it ends.
	EndsAt time.Time
}

// JobOpening is one position offered in the city.
type JobOpening struct {
	Job JobRef
	// Pay is what a shift of it pays.
	Pay int64
	// Eligible is whether the player meets every requirement now.
	Eligible bool
}

// CompanyJobOpening is an opening of a player company in the city.
type CompanyJobOpening struct {
	No       int64
	Company  string
	Job      JobRef
	Pay      int64
	Eligible bool
}

// JobOpeningsView is the openings in the player's city, one page of them.
type JobOpeningsView struct {
	// Companies are the openings of the city's player companies, shown
	// beside the base employer's on the first page.
	Companies []CompanyJobOpening
	CityCode  string
	City      string
	// Travelling means the player is between cities; nothing is listed.
	Travelling bool
	// Employed means the player already works; Current is their position.
	Employed bool
	Current  JobRef
	Openings []JobOpening
	// Gaps are the careers this place does not employ: where they are had and
	// what this place lacks to employ them itself («not here», CLAUDE.md section 2).
	Gaps  []JobGap
	Page  int
	Pages int
}

// JobGap is a career the place the player stands in does not employ.
type JobGap struct {
	Job JobRef
	// Nearest is the place that has it; nil when none is known.
	Nearest *presentation.Named
	// NearestTrip is the way to Nearest (fare and wait); nil when unknown.
	NearestTrip *presentation.TripHint
	// Needs is what this place lacks: research, buildings.
	Needs []presentation.CourseNeed
}

// JobDetailView is one opening in detail.
type JobDetailView struct {
	Job          JobRef
	CityCode     string
	City         string
	Pay          int64
	EnergyCost   int
	Requirements []Requirement
	// CanApply is whether the application would be accepted now.
	CanApply bool
	// Employed means the player already has a job, which is why they
	// cannot apply even when they qualify.
	Employed bool
}

// JobHiredView is a successful application.
type JobHiredView struct {
	Job JobRef
	// Employer is the company hired at; empty at the base employer.
	Employer string
	CityCode string
	City     string
	Pay      int64
}

// SkillGain is XP a skill received, and the level it reached if it rose.
type SkillGain struct {
	Skill string
	XP    int64
	// Level is the new level when the skill levelled up, else 0.
	Level int
}

// ShiftStartedView is a shift that has just begun.
type ShiftStartedView struct {
	Job JobRef
	// Duration is the real wait until the shift ends; EndsAt is when.
	Duration time.Duration
	EndsAt   time.Time
	// FatigueBPS is the output the shift runs at; below 10000 it is tired.
	FatigueBPS int
	// Energy and MaxEnergy are what is left after paying for it.
	Energy    int
	MaxEnergy int
}

// ShiftWorkedView is the outcome of one shift.
type ShiftWorkedView struct {
	Gross, Tax, Net  int64
	XP               int64
	Skills           []SkillGain
	Performance      int
	PerformanceDelta int
	// FatigueBPS is the output the shift ran at; below 10000 it was tired.
	FatigueBPS int
	// Level is the highest level reached through this shift's XP, else 0.
	Level     int
	Energy    int
	MaxEnergy int
	// Injury is an accident at work, nil for none (docs/adr/0023).
	Injury *InjuryView
}

// JobPromotedView is a promotion.
type JobPromotedView struct {
	Job JobRef
	Pay int64
}
