// Package job holds the rules of employment: who may take a position, what a
// shift of work costs and earns, when an employee has earned a promotion, and
// how a pay period turns into payments with tax withheld.
//
// WHY JOBS ARE RULES AND NOT A SALARY BUTTON. 03_JOBS.md is explicit that a
// job is one of the pillars of the economy and not a button that dispenses
// money. That is why a shift here costs energy that is refused rather than
// clamped, why output falls off when a player grinds shift after shift, why
// performance moves with how far the player's skills exceed what the position
// asks for, and why a promotion has to be earned against several requirements
// at once. Each of those is a rule, and each is tested here without a
// database, a broker or a running clock: time always arrives as an argument.
//
// WHICH SIDE OF THE RULE/CONTENT LINE (ADR 0004). Everything that describes a
// career — its code, category, tier titles, which skills and certifications
// each tier needs, the base salary, the energy a shift costs, the XP it
// awards and what a promotion requires — is CONTENT. It arrives here as an
// already-parsed Career value and this package never reads a file. The
// progression ladder itself (Entry → Skilled → Senior → Specialist → Manager →
// Executive → Owner) is a RULE, because other systems branch on it: only an
// Owner founds a company, a Manager's performance feeds a department.
//
// WHICH VALUES ARE POLICY (ADR 0015). The minimum wage, the income tax rate and
// the fatigue window are decisions a player holding an office may change.
// They are never constants in this package: they arrive as a Policy or as an
// explicit argument on every call, so the same rule serves every city and
// every mayor. A city job such as police is paid from the treasury; which
// account pays is the caller's decision, and nothing here depends on it.
//
// Every function is pure and every value type uses value receivers. A refused
// action returns a zero result and leaves its inputs untouched, so a caller
// that ignores the error cannot half-apply a shift.
package job

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// ErrInvalidCareer means authored career content is unusable. It is a content
// problem, not a player mistake, and it is reported separately so it can be
// alerted on rather than shown to a player as "you cannot do that".
var ErrInvalidCareer = errors.New("job: invalid career definition")

// Rank is a rung on the career ladder from 03_JOBS.md. The set is closed and
// ordered: a higher value is a more senior position. The numbers are stored,
// so they must never be renumbered once shipped.
type Rank int

const (
	RankEntry      Rank = 1
	RankSkilled    Rank = 2
	RankSenior     Rank = 3
	RankSpecialist Rank = 4
	RankManager    Rank = 5
	RankExecutive  Rank = 6
	RankOwner      Rank = 7
)

// Validate rejects a rank outside the ladder.
func (r Rank) Validate() error {
	if r < RankEntry || r > RankOwner {
		return fmt.Errorf("%w: rank %d is not on the career ladder", ErrInvalidCareer, int(r))
	}
	return nil
}

// Bounds on authored career content. Like travel's tariff bounds they reject
// an obvious typo at load, and they keep every figure a formula here touches
// far from the edge of int64. The formulas are overflow-safe on their own
// (see mulDiv); these bounds are what make a content mistake visible instead
// of quietly saturating.
const (
	// MaxBaseSalary is the most one shift may be authored to pay, in minor
	// units.
	MaxBaseSalary = 1_000_000_000_000_000

	// MaxEnergyCost is the most energy a shift may be authored to cost. It is
	// generous next to player.DefaultMaxEnergy on purpose: a bar can grow.
	MaxEnergyCost = 10_000

	// MaxXPPerShift bounds both character XP and each skill's XP per shift.
	MaxXPPerShift = 1_000_000_000

	// MaxPerformance is the top of the performance scale, matching
	// employees.performance (0..100).
	MaxPerformance = 100

	// MaxRequiredShifts bounds a promotion's shift requirement.
	MaxRequiredShifts = 1_000_000
)

// SkillRequirement is a minimum level in one skill.
type SkillRequirement struct {
	Skill player.SkillCode
	Level int
}

// SkillXP is experience awarded to one skill.
type SkillXP struct {
	Skill player.SkillCode
	XP    int64
}

// PromotionRequirement is what an employee must show in a tier before moving
// to the next one. The next tier's own entry requirements (skills,
// certifications, level) apply on top; see Promotion.
type PromotionRequirement struct {
	// MinPerformance is on the 0..MaxPerformance scale.
	MinPerformance int
	// MinTimeInTier is how long the employee must have held this tier.
	MinTimeInTier time.Duration
	// MinShifts is how many shifts must have been worked in this tier.
	MinShifts int
}

// Tier is one position within a career: its title, what it takes to hold it,
// and what a shift of it costs and earns. All of it is content.
type Tier struct {
	Rank  Rank
	Title string

	// MinLevel is the character level needed to hold this tier.
	MinLevel int
	// RequiredSkills are checked and reported in authored order, so the
	// player sees their shortfalls in the order the content author chose.
	RequiredSkills []SkillRequirement
	// RequiredCertifications are course codes: a certification is identified
	// by the course that issued it (certifications.course_id).
	RequiredCertifications []string

	// BaseSalary is what one shift pays at this tier before policy and
	// fatigue, in minor units. An employer may offer more; see Hire.
	BaseSalary money.Amount
	// EnergyCost is what one shift costs. Zero is allowed.
	EnergyCost int
	// XPPerShift is character XP for a full-output shift.
	XPPerShift int64
	// SkillXPPerShift is skill XP for a full-output shift.
	SkillXPPerShift []SkillXP

	// Promotion is what it takes to leave this tier for the next one. It is
	// ignored on the last tier of a career.
	Promotion PromotionRequirement
}

// Career is a whole career path: a code, a category and its tiers from the
// most junior upward. It mirrors the careers table plus the per-tier data that
// jobs.yml authors.
type Career struct {
	Code string
	// Category is content (technology, medical, engineering, …). It is not a
	// closed set here because no rule in this package branches on it.
	Category string
	// Tiers are ordered from most junior to most senior. A career need not
	// cover every rank — a taxi driver may never become an Executive — but
	// ranks must strictly increase.
	Tiers []Tier
}

// Validate reports whether a career is usable content. It is what a content
// loader calls before publishing, and every entry point in this package calls
// it again, because the safety of the formulas relies on it.
func (c Career) Validate() error {
	if c.Code == "" {
		return fmt.Errorf("%w: empty code", ErrInvalidCareer)
	}
	if c.Category == "" {
		return fmt.Errorf("%w: %q has no category", ErrInvalidCareer, c.Code)
	}
	if len(c.Tiers) == 0 {
		return fmt.Errorf("%w: %q has no tiers", ErrInvalidCareer, c.Code)
	}
	var prev Rank
	for i, t := range c.Tiers {
		if err := t.validate(); err != nil {
			return fmt.Errorf("%w (career %q, tier %d)", err, c.Code, i)
		}
		if t.Rank <= prev {
			return fmt.Errorf("%w: %q tier %d rank %d does not rise above %d",
				ErrInvalidCareer, c.Code, i, int(t.Rank), int(prev))
		}
		prev = t.Rank
	}
	return nil
}

func (t Tier) validate() error {
	if err := t.Rank.Validate(); err != nil {
		return err
	}
	if t.Title == "" {
		return fmt.Errorf("%w: empty title", ErrInvalidCareer)
	}
	if t.MinLevel < 0 || t.MinLevel > player.MaxLevel {
		return fmt.Errorf("%w: min level %d", ErrInvalidCareer, t.MinLevel)
	}
	seen := make(map[player.SkillCode]bool, len(t.RequiredSkills))
	for _, r := range t.RequiredSkills {
		if err := player.Validate(r.Skill); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidCareer, err)
		}
		if seen[r.Skill] {
			return fmt.Errorf("%w: skill %q required twice", ErrInvalidCareer, string(r.Skill))
		}
		seen[r.Skill] = true
		if r.Level < 1 || r.Level > player.MaxSkillLevel {
			return fmt.Errorf("%w: skill %q level %d", ErrInvalidCareer, string(r.Skill), r.Level)
		}
	}
	certs := make(map[string]bool, len(t.RequiredCertifications))
	for _, c := range t.RequiredCertifications {
		if c == "" || certs[c] {
			return fmt.Errorf("%w: empty or repeated certification %q", ErrInvalidCareer, c)
		}
		certs[c] = true
	}
	if t.BaseSalary.IsNegative() || t.BaseSalary.Minor() > MaxBaseSalary {
		return fmt.Errorf("%w: base salary %s", ErrInvalidCareer, t.BaseSalary)
	}
	if t.EnergyCost < 0 || t.EnergyCost > MaxEnergyCost {
		return fmt.Errorf("%w: energy cost %d", ErrInvalidCareer, t.EnergyCost)
	}
	if t.XPPerShift < 0 || t.XPPerShift > MaxXPPerShift {
		return fmt.Errorf("%w: xp per shift %d", ErrInvalidCareer, t.XPPerShift)
	}
	rewarded := make(map[player.SkillCode]bool, len(t.SkillXPPerShift))
	for _, s := range t.SkillXPPerShift {
		if err := player.Validate(s.Skill); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidCareer, err)
		}
		if rewarded[s.Skill] {
			return fmt.Errorf("%w: skill %q rewarded twice", ErrInvalidCareer, string(s.Skill))
		}
		rewarded[s.Skill] = true
		if s.XP < 0 || s.XP > MaxXPPerShift {
			return fmt.Errorf("%w: skill %q xp %d", ErrInvalidCareer, string(s.Skill), s.XP)
		}
	}
	p := t.Promotion
	if p.MinPerformance < 0 || p.MinPerformance > MaxPerformance {
		return fmt.Errorf("%w: promotion performance %d", ErrInvalidCareer, p.MinPerformance)
	}
	if p.MinTimeInTier < 0 {
		return fmt.Errorf("%w: promotion time %s", ErrInvalidCareer, p.MinTimeInTier)
	}
	if p.MinShifts < 0 || p.MinShifts > MaxRequiredShifts {
		return fmt.Errorf("%w: promotion shifts %d", ErrInvalidCareer, p.MinShifts)
	}
	return nil
}

// tier returns tier i of a validated career, or ErrUnknownTier.
func (c Career) tier(i int) (Tier, error) {
	if i < 0 || i >= len(c.Tiers) {
		return Tier{}, fmt.Errorf("%w: %q has no tier %d", ErrUnknownTier, c.Code, i)
	}
	return c.Tiers[i], nil
}
