package job

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Sentinel errors for an unmet requirement. Each has its own sentinel because
// each needs a different next step from the player — train a skill, take a
// course, apply for a permit — and the outer layer can only say which one if
// the error tells it. Where the detail matters (which skill, what level) the
// sentinel is wrapped by a typed error that errors.As can extract.
var (
	// ErrUnknownTier means a tier index is outside the career.
	ErrUnknownTier = errors.New("job: career has no such tier")

	// ErrLevelTooLow means the character level is below the tier's minimum.
	// The detail is a LevelShortfall.
	ErrLevelTooLow = errors.New("job: character level too low")

	// ErrMissingSkill means a required skill is below the level needed. The
	// detail is a SkillShortfall.
	ErrMissingSkill = errors.New("job: required skill not reached")

	// ErrMissingCertification means a required certification is not held. The
	// detail is a CertificationShortfall.
	ErrMissingCertification = errors.New("job: required certification not held")

	// ErrWorkPermitRequired means the player does not live in the job's city
	// and holds no work permit for it (ADR 0014: a non-resident works only
	// with a permit).
	ErrWorkPermitRequired = errors.New("job: non-residents need a work permit")

	// ErrBelowMinimumWage means an offered rate is under the minimum wage in
	// force where the job is.
	ErrBelowMinimumWage = errors.New("job: pay is below the minimum wage")

	// ErrInvalidRate means a pay rate is negative or beyond MaxBaseSalary.
	ErrInvalidRate = errors.New("job: invalid pay rate")

	// ErrInvalidTime means the caller passed a zero time as now.
	ErrInvalidTime = errors.New("job: time is not set")
)

// LevelShortfall details ErrLevelTooLow.
type LevelShortfall struct {
	Need, Have int
}

func (e LevelShortfall) Error() string {
	return fmt.Sprintf("%v: need level %d, have %d", ErrLevelTooLow, e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrLevelTooLow.
func (e LevelShortfall) Unwrap() error { return ErrLevelTooLow }

// SkillShortfall details ErrMissingSkill: which skill, at what level, and
// where the player stands now.
type SkillShortfall struct {
	Skill      player.SkillCode
	Need, Have int
}

func (e SkillShortfall) Error() string {
	return fmt.Sprintf("%v: %s needs level %d, have %d", ErrMissingSkill, string(e.Skill), e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrMissingSkill.
func (e SkillShortfall) Unwrap() error { return ErrMissingSkill }

// CertificationShortfall details ErrMissingCertification. Code is the course
// that issues the missing certification.
type CertificationShortfall struct {
	Code string
}

func (e CertificationShortfall) Error() string {
	return fmt.Sprintf("%v: %s", ErrMissingCertification, e.Code)
}

// Unwrap lets errors.Is match ErrMissingCertification.
func (e CertificationShortfall) Unwrap() error { return ErrMissingCertification }

// Residency is where the player stands legally in the city of the job. The
// caller works it out from players.residence_city_id and work_permits; this
// package only applies the rule.
type Residency struct {
	// Resident means the player's residence city is the job's city.
	Resident bool
	// WorkPermit means the player holds a valid permit for the job's city.
	WorkPermit bool
}

// Candidate is everything about a player that decides whether they may hold a
// position.
type Candidate struct {
	Stats  player.Stats
	Skills []player.Skill
	// Certifications are course codes of held certifications.
	Certifications []string
	Residency      Residency
}

// Eligibility reports whether a candidate may hold tier of career.
//
// It returns nil, or EVERY unmet requirement joined together with
// errors.Join — not just the first. A player told only "you need programming
// 30" who then trains it and is told "you also need a certificate" has been
// sent on two trips for one answer. errors.Is and errors.As see through the
// join, so a caller can still test for one specific requirement.
//
// The order is fixed: residency, level, skills in authored order,
// certifications in authored order. ErrInvalidCareer and ErrUnknownTier are
// returned alone, because they mean the question itself is broken.
func Eligibility(career Career, tier int, c Candidate) error {
	if err := career.Validate(); err != nil {
		return err
	}
	t, err := career.tier(tier)
	if err != nil {
		return err
	}
	var errs []error
	if !c.Residency.Resident && !c.Residency.WorkPermit {
		errs = append(errs, ErrWorkPermitRequired)
	}
	errs = append(errs, requirementGaps(t, c.Stats, c.Skills, c.Certifications)...)
	return errors.Join(errs...)
}

// requirementGaps lists the level, skill and certification shortfalls for
// holding t. Residency is deliberately not here: a promotion is inside an
// employment the player already holds legally.
func requirementGaps(t Tier, stats player.Stats, skills []player.Skill, certs []string) []error {
	var errs []error
	if stats.Level < t.MinLevel {
		errs = append(errs, LevelShortfall{Need: t.MinLevel, Have: stats.Level})
	}
	levels := skillLevels(skills)
	for _, r := range t.RequiredSkills {
		if have := levels[r.Skill]; have < r.Level {
			errs = append(errs, SkillShortfall{Skill: r.Skill, Need: r.Level, Have: have})
		}
	}
	held := make(map[string]bool, len(certs))
	for _, code := range certs {
		held[code] = true
	}
	for _, code := range t.RequiredCertifications {
		if !held[code] {
			errs = append(errs, CertificationShortfall{Code: code})
		}
	}
	return errs
}

// skillLevels indexes skills by code. A skill listed twice counts at its
// higher level; a missing skill is level zero, matching NewSkill.
func skillLevels(skills []player.Skill) map[player.SkillCode]int {
	out := make(map[player.SkillCode]int, len(skills))
	for _, s := range skills {
		if s.Level > out[s.Code] {
			out[s.Code] = s.Level
		}
	}
	return out
}

// CheckWage rejects a per-shift rate that is invalid or under minimumWage.
//
// The minimum wage is a policy lever held by an office (ADR 0015), so it is an
// argument and never a constant here.
func CheckWage(rate, minimumWage money.Amount) error {
	if rate.IsNegative() || rate.Minor() > MaxBaseSalary {
		return fmt.Errorf("%w: %s", ErrInvalidRate, rate)
	}
	if rate.Minor() < minimumWage.Minor() {
		return fmt.Errorf("%w: offered %s, minimum %s", ErrBelowMinimumWage, rate, minimumWage)
	}
	return nil
}

// Hire checks a candidate against tier of career and an offered per-shift
// rate, and returns the employment that starts at now.
//
// The rate is what the employer offers (jobs.salary); an offer under the
// tier's authored BaseSalary is allowed, because the base is a suggestion to
// NPC employers and a player company sets its own wages. What is not allowed
// is an offer under the minimum wage in force.
func Hire(career Career, tier int, c Candidate, rate money.Amount, policy Policy, now time.Time) (Employment, error) {
	if now.IsZero() {
		return Employment{}, ErrInvalidTime
	}
	if err := policy.Validate(); err != nil {
		return Employment{}, err
	}
	if err := Eligibility(career, tier, c); err != nil {
		return Employment{}, err
	}
	if err := CheckWage(rate, policy.MinimumWage); err != nil {
		return Employment{}, err
	}
	return Employment{
		CareerCode:  career.Code,
		Tier:        tier,
		Rate:        rate,
		Performance: StartingPerformance,
		TierSince:   now,
	}, nil
}
