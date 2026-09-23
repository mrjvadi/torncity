// Package education holds the rules of study: what a course is, who may
// enrol in one, how far along an enrolment is at any moment, and what
// finishing it awards.
//
// WHY THIS IS A SYSTEM AND NOT A SHOP. 11_EDUCATION.md asks for study that
// unlocks jobs, raises salary, feeds skills and gates promotion. That only
// works if a qualification costs something a player cannot simply buy past:
// time. So a course here has a duration that no amount of money shortens, a
// player may follow only ONE course at a time, and a certificate is issued
// once, at the end, never pro rata. Those are rules, tested here with time as
// an argument and no clock running.
//
// WHICH SIDE OF THE RULE/CONTENT LINE (ADR 0004). What courses exist, what
// they cost, how long they take, how many seats they have, what they require
// and what they award are CONTENT: they arrive as a parsed Course and this
// package never reads a file. The kinds of institution are a closed set,
// because other systems branch on them — a company-run course is paid to that
// company. The single-active-enrolment rule, the progress formula and the
// completion rule are code.
//
// A certification is identified by the course that issued it, exactly as
// certifications.course_id does in docs/database.md. The job package names
// its required certifications by those same course codes, which is the whole
// contract between the two packages; neither imports the other.
//
// Every function is pure and every value type uses value receivers.
package education

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// ErrInvalidCourse means authored course content is unusable. It is a content
// problem, reported apart from player-facing refusals so it can be alerted on.
var ErrInvalidCourse = errors.New("education: invalid course definition")

// Institution is the kind of body offering a course, matching
// courses.institution_type.
type Institution string

const (
	InstitutionUniversity     Institution = "university"
	InstitutionTrainingCenter Institution = "training_center"
	InstitutionCompany        Institution = "company"
)

var institutions = []Institution{InstitutionUniversity, InstitutionTrainingCenter, InstitutionCompany}

// Institutions returns every kind of institution. The slice is a copy.
func Institutions() []Institution {
	out := make([]Institution, len(institutions))
	copy(out, institutions)
	return out
}

// Validate rejects an institution outside the closed set.
func (i Institution) Validate() error {
	for _, known := range institutions {
		if known == i {
			return nil
		}
	}
	return fmt.Errorf("%w: unknown institution %q", ErrInvalidCourse, string(i))
}

// Bounds on authored course content. They reject typos at load and keep
// CompletesAt far inside what time.Time can represent.
const (
	MaxCost        = 1_000_000_000_000_000
	MaxDuration    = 5 * 365 * 24 * time.Hour
	MaxCapacity    = 1_000_000
	MaxSkillReward = 1_000_000_000
)

// SkillReward is XP awarded to one skill on completion.
type SkillReward struct {
	Skill player.SkillCode
	XP    int64
}

// Course is one course definition, mirroring the courses table.
type Course struct {
	Code        string
	Institution Institution
	// CityCode is where the course is taught; a player must be there to
	// enrol. Empty means it can be taken from anywhere.
	CityCode string
	// Cost is the fee in minor units. Charging it is the ledger's job, done
	// in the same transaction as recording the enrolment.
	Cost money.Amount
	// Duration is how long the course runs. It is fixed onto an enrolment at
	// the moment of enrolling (see Enroll), so re-authoring it later never
	// moves the finish line under a student already enrolled.
	Duration time.Duration
	// Capacity is the number of seats; zero means unlimited (NULL in
	// courses.capacity).
	Capacity int
	// MinLevel is the character level needed to enrol.
	MinLevel int
	// Prerequisites are course codes whose certifications must be held.
	Prerequisites []string
	// SkillRewards are awarded on completion, in authored order.
	SkillRewards []SkillReward
	// Certifies means completing the course issues its certification. A
	// course that does not certify still awards its skills.
	Certifies bool
}

// Validate reports whether a course is usable content. Every entry point in
// this package calls it again.
func (c Course) Validate() error {
	if c.Code == "" {
		return fmt.Errorf("%w: empty code", ErrInvalidCourse)
	}
	if err := c.Institution.Validate(); err != nil {
		return fmt.Errorf("%w (course %q)", err, c.Code)
	}
	if c.Cost.IsNegative() || c.Cost.Minor() > MaxCost {
		return fmt.Errorf("%w: %q costs %s", ErrInvalidCourse, c.Code, c.Cost)
	}
	if c.Duration <= 0 || c.Duration > MaxDuration {
		// A zero-length course would certify on the spot, which is buying a
		// qualification — exactly what this package exists to prevent.
		return fmt.Errorf("%w: %q lasts %s", ErrInvalidCourse, c.Code, c.Duration)
	}
	if c.Capacity < 0 || c.Capacity > MaxCapacity {
		return fmt.Errorf("%w: %q has capacity %d", ErrInvalidCourse, c.Code, c.Capacity)
	}
	if c.MinLevel < 0 || c.MinLevel > player.MaxLevel {
		return fmt.Errorf("%w: %q min level %d", ErrInvalidCourse, c.Code, c.MinLevel)
	}
	seen := make(map[string]bool, len(c.Prerequisites))
	for _, p := range c.Prerequisites {
		if p == "" || p == c.Code || seen[p] {
			return fmt.Errorf("%w: %q has empty, self or repeated prerequisite %q", ErrInvalidCourse, c.Code, p)
		}
		seen[p] = true
	}
	rewarded := make(map[player.SkillCode]bool, len(c.SkillRewards))
	for _, r := range c.SkillRewards {
		if err := player.Validate(r.Skill); err != nil {
			return fmt.Errorf("%w: %q: %v", ErrInvalidCourse, c.Code, err)
		}
		if rewarded[r.Skill] {
			return fmt.Errorf("%w: %q rewards %q twice", ErrInvalidCourse, c.Code, string(r.Skill))
		}
		rewarded[r.Skill] = true
		if r.XP <= 0 || r.XP > MaxSkillReward {
			return fmt.Errorf("%w: %q rewards %q with %d xp", ErrInvalidCourse, c.Code, string(r.Skill), r.XP)
		}
	}
	return nil
}
