package content

import (
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/education"
	"github.com/mrjvadi/torncity/internal/domain/job"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// This file holds the content side of work and study: the careers jobs.yml
// authors and the courses education.yml authors.
//
// The line ADR 0004 draws runs through here exactly as it does for cities.
// WHAT a career is — its tiers, what each asks for and pays, what earns a
// promotion — and WHAT a course is — its fee, length, seats and rewards — are
// content, parsed and validated in this package. HOW a shift pays, when a
// promotion is earned, how enrolment and completion work are rules, and they
// stay in internal/domain/job and internal/domain/education, which receive the
// finished values from Career and Course below and never read a file.
//
// The definitions carry json tags equal to their yaml tags because the
// database stores each one as a document (migrations/0011): a career is a
// tree of tiers, requirements and rewards, and flattening that into columns
// would be a second schema to keep in step with this one for no reader's
// benefit.

// Rank names, as a content file writes them. The ladder itself — its order
// and what each rung means to other systems — is the domain's (job.Rank); the
// names are only how an author refers to a rung, and they are also the
// catalogue keys a tier's display title is looked up under.
var rankNames = map[string]job.Rank{
	"entry":      job.RankEntry,
	"skilled":    job.RankSkilled,
	"senior":     job.RankSenior,
	"specialist": job.RankSpecialist,
	"manager":    job.RankManager,
	"executive":  job.RankExecutive,
	"owner":      job.RankOwner,
}

// RankOf returns the domain rank for a rank name, and whether there is one.
func RankOf(name string) (job.Rank, bool) {
	r, ok := rankNames[name]
	return r, ok
}

// SkillLevelDef is one "this skill at this level" requirement.
type SkillLevelDef struct {
	Skill string `yaml:"skill" json:"skill"`
	Level int    `yaml:"level" json:"level"`
}

// SkillXPDef is XP awarded to one skill.
type SkillXPDef struct {
	Skill string `yaml:"skill" json:"skill"`
	XP    int64  `yaml:"xp" json:"xp"`
}

// PromotionDef is what earns the step from a tier to the next one.
type PromotionDef struct {
	// MinPerformance is on the 0..100 scale.
	MinPerformance int `yaml:"min_performance" json:"min_performance"`
	// MinTimeInTier is a Go duration ("48h"); empty means none.
	MinTimeInTier string `yaml:"min_time_in_tier" json:"min_time_in_tier"`
	MinShifts     int    `yaml:"min_shifts" json:"min_shifts"`
}

// TierDef is one rung of a career.
type TierDef struct {
	// Rank is a rank name (see rankNames). Ranks rise strictly within a
	// career, and a career need not use every rank.
	Rank string `yaml:"rank" json:"rank"`
	// Title is the authored display title, the fallback when the catalogue
	// has no career.<code>.<rank> entry.
	Title                  string          `yaml:"title" json:"title"`
	MinLevel               int             `yaml:"min_level" json:"min_level"`
	RequiredSkills         []SkillLevelDef `yaml:"required_skills" json:"required_skills"`
	RequiredCertifications []string        `yaml:"required_certifications" json:"required_certifications"`
	// BaseSalary is pay per full-output shift, in minor units. It is what the
	// base (NPC) employer offers.
	BaseSalary int64 `yaml:"base_salary" json:"base_salary"`
	EnergyCost int   `yaml:"energy_cost" json:"energy_cost"`
	// ShiftDuration is how long one shift lasts, a Go duration ("8h") in
	// GAME time; the player waits it through the game clock (config
	// game.time_scale). Required: the domain refuses a shift of no length.
	ShiftDuration   string       `yaml:"shift_duration" json:"shift_duration"`
	XPPerShift      int64        `yaml:"xp_per_shift" json:"xp_per_shift"`
	SkillXPPerShift []SkillXPDef `yaml:"skill_xp_per_shift" json:"skill_xp_per_shift"`
	Promotion       PromotionDef `yaml:"promotion" json:"promotion"`
}

// CareerDef is one entry of jobs.yml.
type CareerDef struct {
	// Code is the stable identifier. Stored employments name it, so it is
	// never changed after it ships.
	Code string `yaml:"code" json:"code"`
	// Name is the authored display name, the fallback when the catalogue has
	// no career.<code>.name entry.
	Name     string `yaml:"name" json:"name"`
	Category string `yaml:"category" json:"category"`
	// Cities are the codes of the cities whose base employer hires into this
	// career. Empty means every city. A player company will post its own
	// openings later; this list is only the NPC employer's.
	Cities []string  `yaml:"cities" json:"cities"`
	Tiers  []TierDef `yaml:"tiers" json:"tiers"`
}

// OfferedIn reports whether the base employer hires into this career in the
// city with this code.
func (c CareerDef) OfferedIn(cityCode string) bool {
	if len(c.Cities) == 0 {
		return true
	}
	for _, code := range c.Cities {
		if code == cityCode {
			return true
		}
	}
	return false
}

// Career converts the definition to the domain value. The domain's own
// Validate is NOT run here; Pack.Validate runs it, so a content error is
// reported with every other one instead of the first.
func (c CareerDef) Career() (job.Career, error) {
	out := job.Career{Code: c.Code, Category: c.Category}
	for i, t := range c.Tiers {
		rank, ok := RankOf(t.Rank)
		if !ok {
			return job.Career{}, fmt.Errorf("%w: career %q tier %d has unknown rank %q",
				ErrUnknownRank, c.Code, i, t.Rank)
		}
		wait, err := optionalDuration(t.Promotion.MinTimeInTier)
		if err != nil {
			return job.Career{}, fmt.Errorf("%w: career %q tier %d min_time_in_tier: %v",
				ErrInvalidDuration, c.Code, i, err)
		}
		shift, err := optionalDuration(t.ShiftDuration)
		if err != nil {
			return job.Career{}, fmt.Errorf("%w: career %q tier %d shift_duration: %v",
				ErrInvalidDuration, c.Code, i, err)
		}
		tier := job.Tier{
			Rank:                   rank,
			Title:                  t.Title,
			MinLevel:               t.MinLevel,
			RequiredCertifications: append([]string(nil), t.RequiredCertifications...),
			BaseSalary:             money.FromMinor(t.BaseSalary),
			EnergyCost:             t.EnergyCost,
			ShiftDuration:          shift,
			XPPerShift:             t.XPPerShift,
			Promotion: job.PromotionRequirement{
				MinPerformance: t.Promotion.MinPerformance,
				MinTimeInTier:  wait,
				MinShifts:      t.Promotion.MinShifts,
			},
		}
		for _, r := range t.RequiredSkills {
			tier.RequiredSkills = append(tier.RequiredSkills,
				job.SkillRequirement{Skill: player.SkillCode(r.Skill), Level: r.Level})
		}
		for _, r := range t.SkillXPPerShift {
			tier.SkillXPPerShift = append(tier.SkillXPPerShift,
				job.SkillXP{Skill: player.SkillCode(r.Skill), XP: r.XP})
		}
		out.Tiers = append(out.Tiers, tier)
	}
	return out, nil
}

// CourseDef is one entry of education.yml.
type CourseDef struct {
	// Code is the stable identifier and also the certification's identity:
	// a career requires a certification by naming the course that issues it.
	Code string `yaml:"code" json:"code"`
	// Name is the authored display name, the fallback when the catalogue has
	// no course.<code> entry.
	Name        string `yaml:"name" json:"name"`
	Institution string `yaml:"institution" json:"institution"`
	// City is the code of the city the course is taught in; empty means it
	// can be taken from anywhere.
	City string `yaml:"city" json:"city"`
	// Cost is the fee in minor units.
	Cost int64 `yaml:"cost" json:"cost"`
	// Duration is a Go duration ("6h") in GAME time; the player waits it
	// through the game clock (config game.time_scale). No fee shortens it.
	Duration string `yaml:"duration" json:"duration"`
	// Capacity is the number of seats; 0 means unlimited.
	Capacity      int          `yaml:"capacity" json:"capacity"`
	MinLevel      int          `yaml:"min_level" json:"min_level"`
	Prerequisites []string     `yaml:"prerequisites" json:"prerequisites"`
	SkillRewards  []SkillXPDef `yaml:"skill_rewards" json:"skill_rewards"`
	Certifies     bool         `yaml:"certifies" json:"certifies"`
	// Payment optionally narrows the methods its tuition may be paid by
	// (payments.yml, service tuition): [cash] for a cash-only school.
	// Omitted means whatever tuition accepts.
	Payment []string `yaml:"payment,omitempty" json:"payment,omitempty"`
}

// Course converts the definition to the domain value, like CareerDef.Career.
func (c CourseDef) Course() (education.Course, error) {
	length, err := time.ParseDuration(c.Duration)
	if err != nil {
		return education.Course{}, fmt.Errorf("%w: course %q duration %q: %v",
			ErrInvalidDuration, c.Code, c.Duration, err)
	}
	out := education.Course{
		Code:          c.Code,
		Institution:   education.Institution(c.Institution),
		CityCode:      c.City,
		Cost:          money.FromMinor(c.Cost),
		Duration:      length,
		Capacity:      c.Capacity,
		MinLevel:      c.MinLevel,
		Prerequisites: append([]string(nil), c.Prerequisites...),
		Certifies:     c.Certifies,
	}
	for _, r := range c.SkillRewards {
		out.SkillRewards = append(out.SkillRewards,
			education.SkillReward{Skill: player.SkillCode(r.Skill), XP: r.XP})
	}
	return out, nil
}

// optionalDuration parses a duration where empty means zero.
func optionalDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("%s is negative", raw)
	}
	return d, nil
}

// buildJobs converts the pack's careers and courses into the domain values a
// snapshot hands out. The pack has been validated, so a conversion failure
// here means a rule in validate_jobs.go has drifted from the conversion.
func (s *Snapshot) buildJobs(p *Pack) error {
	s.careers = append([]CareerDef(nil), p.Careers...)
	s.courses = append([]CourseDef(nil), p.Courses...)
	s.career = make(map[string]job.Career, len(p.Careers))
	s.course = make(map[string]education.Course, len(p.Courses))
	for _, c := range p.Careers {
		career, err := c.Career()
		if err != nil {
			return err
		}
		s.career[c.Code] = career
	}
	for _, c := range p.Courses {
		course, err := c.Course()
		if err != nil {
			return err
		}
		s.course[c.Code] = course
	}
	return nil
}

// Careers returns the career definitions of this version, in file order. The
// slice is a copy.
func (s *Snapshot) Careers() []CareerDef { return append([]CareerDef(nil), s.careers...) }

// CareerDef returns the definition of one career.
func (s *Snapshot) CareerDef(code string) (CareerDef, bool) {
	for _, c := range s.careers {
		if c.Code == code {
			return c, true
		}
	}
	return CareerDef{}, false
}

// Career returns one career as the domain value its rules take. The value
// shares its slices with the snapshot; the domain's functions only read them.
func (s *Snapshot) Career(code string) (job.Career, bool) {
	c, ok := s.career[code]
	return c, ok
}

// Courses returns the course definitions of this version, in file order. The
// slice is a copy.
func (s *Snapshot) Courses() []CourseDef { return append([]CourseDef(nil), s.courses...) }

// CourseDef returns the definition of one course.
func (s *Snapshot) CourseDef(code string) (CourseDef, bool) {
	for _, c := range s.courses {
		if c.Code == code {
			return c, true
		}
	}
	return CourseDef{}, false
}

// Course returns one course as the domain value its rules take, sharing its
// slices with the snapshot like Career.
func (s *Snapshot) Course(code string) (education.Course, bool) {
	c, ok := s.course[code]
	return c, ok
}
