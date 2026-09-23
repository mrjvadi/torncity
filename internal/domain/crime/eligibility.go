package crime

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/player"
)

// Sentinel errors for an unmet requirement. Each is wrapped by a typed error
// carrying the detail, so the outer layer can say exactly what is missing;
// Eligibility joins every one of them so the player learns them all at once.
var (
	// ErrLevelTooLow means the character level is below the crime's minimum.
	ErrLevelTooLow = errors.New("crime: character level too low")
	// ErrTierTooLow means the criminal experience tier is below the crime's.
	ErrTierTooLow = errors.New("crime: criminal experience too low")
	// ErrMissingSkill means a required skill is below the level needed.
	ErrMissingSkill = errors.New("crime: required skill not reached")
	// ErrMissingCertification means a required certificate is not held.
	ErrMissingCertification = errors.New("crime: required certification not held")
	// ErrMissingTool means a required tool is not carried.
	ErrMissingTool = errors.New("crime: required tool not carried")
	// ErrMissingFacility means the city lacks a facility the crime needs.
	ErrMissingFacility = errors.New("crime: the city lacks a required facility")
)

// LevelShortfall details ErrLevelTooLow.
type LevelShortfall struct{ Need, Have int }

func (e LevelShortfall) Error() string {
	return fmt.Sprintf("%v: need %d, have %d", ErrLevelTooLow, e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrLevelTooLow.
func (e LevelShortfall) Unwrap() error { return ErrLevelTooLow }

// TierShortfall details ErrTierTooLow with tier indexes.
type TierShortfall struct{ Need, Have int }

func (e TierShortfall) Error() string {
	return fmt.Sprintf("%v: need tier %d, have %d", ErrTierTooLow, e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrTierTooLow.
func (e TierShortfall) Unwrap() error { return ErrTierTooLow }

// SkillShortfall details ErrMissingSkill.
type SkillShortfall struct {
	Skill      player.SkillCode
	Need, Have int
}

func (e SkillShortfall) Error() string {
	return fmt.Sprintf("%v: %s needs %d, have %d", ErrMissingSkill, string(e.Skill), e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrMissingSkill.
func (e SkillShortfall) Unwrap() error { return ErrMissingSkill }

// CertificationShortfall details ErrMissingCertification.
type CertificationShortfall struct{ Code string }

func (e CertificationShortfall) Error() string {
	return fmt.Sprintf("%v: %s", ErrMissingCertification, e.Code)
}

// Unwrap lets errors.Is match ErrMissingCertification.
func (e CertificationShortfall) Unwrap() error { return ErrMissingCertification }

// ToolShortfall details ErrMissingTool.
type ToolShortfall struct{ Code string }

func (e ToolShortfall) Error() string { return fmt.Sprintf("%v: %s", ErrMissingTool, e.Code) }

// Unwrap lets errors.Is match ErrMissingTool.
func (e ToolShortfall) Unwrap() error { return ErrMissingTool }

// FacilityShortfall details ErrMissingFacility.
type FacilityShortfall struct{ Code string }

func (e FacilityShortfall) Error() string {
	return fmt.Sprintf("%v: %s", ErrMissingFacility, e.Code)
}

// Unwrap lets errors.Is match ErrMissingFacility.
func (e FacilityShortfall) Unwrap() error { return ErrMissingFacility }

// Candidate is everything about a would-be offender the requirements read.
type Candidate struct {
	Level  int
	Skills []player.Skill
	// Tier is the index of their criminal experience tier.
	Tier           int
	Certifications []string
	Tools          []string
	// Facilities are those of the city they stand in.
	Facilities []string
}

// SkillLevel returns the candidate's level in one skill.
func (c Candidate) SkillLevel(code player.SkillCode) int { return skillLevel(c.Skills, code) }

func skillLevel(skills []player.Skill, code player.SkillCode) int {
	for _, s := range skills {
		if s.Code == code {
			return s.Level
		}
	}
	return 0
}

func contains(list []string, code string) bool {
	for _, c := range list {
		if c == code {
			return true
		}
	}
	return false
}

// Eligibility returns nil when the candidate meets every requirement of the
// crime, and otherwise every unmet requirement joined, in a stable order:
// level, tier, skills, certifications, tools, facilities. Nerve, jail, a
// crime or shift in progress and travelling are the caller's checks: they are
// about the moment, not about who the player is.
func Eligibility(c Crime, cand Candidate) error {
	var errs []error
	r := c.Requirements
	if cand.Level < r.MinLevel {
		errs = append(errs, LevelShortfall{Need: r.MinLevel, Have: cand.Level})
	}
	if cand.Tier < r.MinTier {
		errs = append(errs, TierShortfall{Need: r.MinTier, Have: cand.Tier})
	}
	for _, s := range r.Skills {
		if have := cand.SkillLevel(s.Skill); have < s.Level {
			errs = append(errs, SkillShortfall{Skill: s.Skill, Need: s.Level, Have: have})
		}
	}
	for _, code := range r.Certifications {
		if !contains(cand.Certifications, code) {
			errs = append(errs, CertificationShortfall{Code: code})
		}
	}
	for _, code := range r.Tools {
		if !contains(cand.Tools, code) {
			errs = append(errs, ToolShortfall{Code: code})
		}
	}
	for _, code := range r.Facilities {
		if !contains(cand.Facilities, code) {
			errs = append(errs, FacilityShortfall{Code: code})
		}
	}
	return errors.Join(errs...)
}
