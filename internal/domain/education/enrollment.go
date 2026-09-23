package education

import (
	"errors"
	"fmt"
	"math/bits"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Sentinel errors for enrolling in and finishing a course. Each unmet
// requirement has its own, because each sends the player somewhere different.
var (
	// ErrAlreadyEnrolled means the player is already following a course. One
	// at a time is the rule that makes study a choice; the enrollments table
	// enforces the same thing with a partial unique index.
	ErrAlreadyEnrolled = errors.New("education: already enrolled in a course")

	// ErrAlreadyCertified means the player already holds the certification
	// this course issues. certifications is UNIQUE(player_id, course_id), and
	// charging a fee for a certificate that cannot be issued would be a trap.
	ErrAlreadyCertified = errors.New("education: already certified by this course")

	// ErrMissingPrerequisite means a prerequisite certification is not held.
	// The detail is a PrerequisiteShortfall.
	ErrMissingPrerequisite = errors.New("education: prerequisite not held")

	// ErrLevelTooLow means the character level is below the course minimum.
	// The detail is a LevelShortfall.
	ErrLevelTooLow = errors.New("education: character level too low")

	// ErrWrongCity means the course is taught in another city. The detail is
	// a CityShortfall.
	ErrWrongCity = errors.New("education: course is taught in another city")

	// ErrCourseFull means every seat is taken.
	ErrCourseFull = errors.New("education: course is full")

	// ErrInvalidSeatCount means the caller reported a negative seat count.
	ErrInvalidSeatCount = errors.New("education: seats taken cannot be negative")

	// ErrInvalidTime means the caller passed a zero time as now.
	ErrInvalidTime = errors.New("education: time is not set")

	// ErrWrongCourse means an enrolment was presented with a course it is not
	// for — the caller looked up the wrong content.
	ErrWrongCourse = errors.New("education: enrollment is not for this course")

	// ErrNotInProgress means the enrolment was already completed or abandoned.
	ErrNotInProgress = errors.New("education: enrollment is not in progress")

	// ErrNotFinished means the course has not run its full duration yet. The
	// detail is a NotFinished carrying the time left.
	ErrNotFinished = errors.New("education: course not finished yet")
)

// PrerequisiteShortfall details ErrMissingPrerequisite. Code is the course
// whose certification is missing — the course to take next.
type PrerequisiteShortfall struct{ Code string }

func (e PrerequisiteShortfall) Error() string {
	return fmt.Sprintf("%v: %s", ErrMissingPrerequisite, e.Code)
}

// Unwrap lets errors.Is match ErrMissingPrerequisite.
func (e PrerequisiteShortfall) Unwrap() error { return ErrMissingPrerequisite }

// LevelShortfall details ErrLevelTooLow.
type LevelShortfall struct{ Need, Have int }

func (e LevelShortfall) Error() string {
	return fmt.Sprintf("%v: need level %d, have %d", ErrLevelTooLow, e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrLevelTooLow.
func (e LevelShortfall) Unwrap() error { return ErrLevelTooLow }

// CityShortfall details ErrWrongCity: where the course is taught.
type CityShortfall struct{ CityCode string }

func (e CityShortfall) Error() string {
	return fmt.Sprintf("%v: taught in %s", ErrWrongCity, e.CityCode)
}

// Unwrap lets errors.Is match ErrWrongCity.
func (e CityShortfall) Unwrap() error { return ErrWrongCity }

// NotFinished details ErrNotFinished.
type NotFinished struct{ Remaining time.Duration }

func (e NotFinished) Error() string {
	return fmt.Sprintf("%v: %s to go", ErrNotFinished, e.Remaining)
}

// Unwrap lets errors.Is match ErrNotFinished.
func (e NotFinished) Unwrap() error { return ErrNotFinished }

// Status is where an enrolment stands, matching enrollments.status. The
// values are stored and must not be renamed once shipped.
type Status string

const (
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusAbandoned  Status = "abandoned"
)

// Enrollment is one player's place on one course, mirroring an enrollments
// row. The zero value is "no enrolment".
type Enrollment struct {
	CourseCode  string
	Status      Status
	StartedAt   time.Time
	CompletesAt time.Time
}

// Active reports whether the enrolment still occupies the player's one slot.
//
// An in-progress enrolment whose time is up is STILL active until Complete
// runs: its rewards have not been granted yet, and letting a new enrolment
// start first would let a crash between the two lose them.
func (e Enrollment) Active() bool { return e.Status == StatusInProgress }

// Applicant is everything about a player that decides whether they may enrol.
type Applicant struct {
	Stats player.Stats
	// CityCode is where the player is right now (players.city_id).
	CityCode string
	// Certifications are course codes of held certifications.
	Certifications []string
	// Current is the player's current enrolment; the zero value means none.
	Current Enrollment
}

// CanEnroll reports whether an applicant may enrol in course while seatsTaken
// of its seats are occupied.
//
// Like job.Eligibility it returns nil or EVERY unmet requirement joined with
// errors.Join, in a fixed order: active enrolment, already certified, level,
// prerequisites in authored order, city, seats. A player should learn
// everything standing in their way from one answer.
//
// Affording the fee is deliberately not checked: the ledger refuses an
// overdraft atomically with recording the enrolment, and a second check here
// would only be a stale copy of that one.
func CanEnroll(course Course, a Applicant, seatsTaken int) error {
	if err := course.Validate(); err != nil {
		return err
	}
	if seatsTaken < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidSeatCount, seatsTaken)
	}

	held := make(map[string]bool, len(a.Certifications))
	for _, c := range a.Certifications {
		held[c] = true
	}

	var errs []error
	if a.Current.Active() {
		errs = append(errs, fmt.Errorf("%w: %s", ErrAlreadyEnrolled, a.Current.CourseCode))
	}
	if course.Certifies && held[course.Code] {
		errs = append(errs, ErrAlreadyCertified)
	}
	if a.Stats.Level < course.MinLevel {
		errs = append(errs, LevelShortfall{Need: course.MinLevel, Have: a.Stats.Level})
	}
	for _, p := range course.Prerequisites {
		if !held[p] {
			errs = append(errs, PrerequisiteShortfall{Code: p})
		}
	}
	if course.CityCode != "" && a.CityCode != course.CityCode {
		errs = append(errs, CityShortfall{CityCode: course.CityCode})
	}
	if course.Capacity > 0 && seatsTaken >= course.Capacity {
		errs = append(errs, ErrCourseFull)
	}
	return errors.Join(errs...)
}

// Enroll starts an applicant on course at now, or refuses with the reasons
// from CanEnroll. It returns the enrolment to record and the fee to charge;
// both belong in one transaction.
//
// CompletesAt is fixed here from the course's duration, which is GAME time,
// mapped to the real wait by the game's one clock (clock.RealWait): a "24h"
// course at a scale of 60 runs 24 real minutes. From this moment the real
// period is data on the enrolment, so progress and completion depend only on
// the enrolment and the wall clock, and a later change of scale or content
// never moves the finish line under a student already enrolled.
func Enroll(course Course, a Applicant, seatsTaken int, now time.Time, clock gametime.Scale) (Enrollment, money.Amount, error) {
	if now.IsZero() {
		return Enrollment{}, money.Amount{}, ErrInvalidTime
	}
	if err := clock.Validate(); err != nil {
		return Enrollment{}, money.Amount{}, err
	}
	if err := CanEnroll(course, a, seatsTaken); err != nil {
		return Enrollment{}, money.Amount{}, err
	}
	return Enrollment{
		CourseCode:  course.Code,
		Status:      StatusInProgress,
		StartedAt:   now,
		CompletesAt: now.Add(clock.RealWait(course.Duration)),
	}, course.Cost, nil
}

// bpsWhole is one hundred percent in basis points.
const bpsWhole = 10_000

// Progress returns how far through its course an enrolment is at now, in
// basis points from 0 to 10000.
//
//	progress = floor(10000 * (now - StartedAt) / (CompletesAt - StartedAt))
//
// clamped to 0..10000. It is a pure function of the enrolment and the clock:
// nothing ticks, nothing is stored. Flooring means 100% is shown only once
// the course is actually finished, never a nanosecond early. A completed
// enrolment is 10000; an abandoned one is 0, because it leads nowhere. The
// product is formed in 128 bits, so a course years long cannot overflow it.
func (e Enrollment) Progress(now time.Time) int {
	switch e.Status {
	case StatusCompleted:
		return bpsWhole
	case StatusInProgress:
	default:
		return 0
	}
	total := e.CompletesAt.Sub(e.StartedAt)
	elapsed := now.Sub(e.StartedAt)
	if total <= 0 || elapsed >= total {
		return bpsWhole
	}
	if elapsed <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(elapsed), bpsWhole)
	q, _ := bits.Div64(hi, lo, uint64(total)) // elapsed < total, so q < 10000
	return int(q)
}

// Remaining is how long until an in-progress enrolment can be completed, or
// zero if it already can or is not in progress.
func (e Enrollment) Remaining(now time.Time) time.Duration {
	if e.Status != StatusInProgress || !now.Before(e.CompletesAt) {
		return 0
	}
	return e.CompletesAt.Sub(now)
}

// Rewards is what completing a course grants.
type Rewards struct {
	// SkillXP is to be applied with player.Skill.AddSkillXP, in order.
	SkillXP []SkillReward
	// Certification is the course code to record in certifications, or
	// empty if the course does not certify.
	Certification string
}

// Complete finishes an enrolment at now and returns what it grants.
//
// The boundary is inclusive: at exactly CompletesAt the course is done. Before
// it, the refusal is a NotFinished carrying the time left. Nothing is granted
// pro rata: a course is a whole qualification or none.
func Complete(course Course, e Enrollment, now time.Time) (Enrollment, Rewards, error) {
	if now.IsZero() {
		return Enrollment{}, Rewards{}, ErrInvalidTime
	}
	if err := course.Validate(); err != nil {
		return Enrollment{}, Rewards{}, err
	}
	if e.CourseCode != course.Code {
		return Enrollment{}, Rewards{}, fmt.Errorf("%w: enrollment %q, course %q", ErrWrongCourse, e.CourseCode, course.Code)
	}
	if e.Status != StatusInProgress {
		return Enrollment{}, Rewards{}, fmt.Errorf("%w: %s", ErrNotInProgress, e.Status)
	}
	if now.Before(e.CompletesAt) {
		return Enrollment{}, Rewards{}, NotFinished{Remaining: e.CompletesAt.Sub(now)}
	}

	done := e
	done.Status = StatusCompleted
	r := Rewards{}
	if len(course.SkillRewards) > 0 {
		r.SkillXP = append([]SkillReward(nil), course.SkillRewards...)
	}
	if course.Certifies {
		r.Certification = course.Code
	}
	return done, r, nil
}

// Abandon ends an in-progress enrolment without reward, freeing the player's
// slot. Whether any of the fee is refunded is a policy decision for the
// institution, not a rule of this package, so nothing is computed here.
func Abandon(e Enrollment) (Enrollment, error) {
	if e.Status != StatusInProgress {
		return Enrollment{}, fmt.Errorf("%w: %s", ErrNotInProgress, e.Status)
	}
	out := e
	out.Status = StatusAbandoned
	return out, nil
}
