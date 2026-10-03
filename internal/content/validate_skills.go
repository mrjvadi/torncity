package content

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/player"
)

// validateSkills checks the skill catalogue (skills.yml): every code is well
// formed and unique and every skill has a name; a test pins that the codes the engine names in
// code (player.SkillCodes) are all present. The catalogue is content: any
// other skill may be added here, with its requires in availability.yml.
func (p *Pack) validateSkills(problems *[]error) {
	seen := make(map[string]struct{}, len(p.Skills))
	for i, s := range p.Skills {
		where := fmt.Sprintf("skills[%d]", i)

		if err := player.Validate(s.SkillCode()); err != nil {
			*problems = append(*problems, fmt.Errorf("%w: %s %q is not a well-formed skill code (lower case letters, digits, underscore)",
				ErrUnknownSkillCode, where, s.Code))
			continue
		}
		if s.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: skill %q", ErrMissingDisplayName, s.Code))
		}
		if _, dup := seen[s.Code]; dup {
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateSkillCode, s.Code, where))
			continue
		}
		seen[s.Code] = struct{}{}
	}
	if len(p.Skills) == 0 {
		return
	}
	p.validateSkillRefs(seen, problems)
}

// validateSkillRefs proves every skill a career, course, crime, recipe or
// recruitment pool names is in the catalogue.
func (p *Pack) validateSkillRefs(known map[string]struct{}, problems *[]error) {
	check := func(where, code string, kind error) {
		if code == "" {
			return
		}
		if _, ok := known[code]; !ok {
			*problems = append(*problems, fmt.Errorf("%w: %w: %s names skill %q, which skills.yml does not list",
				ErrUnknownSkillCode, kind, where, code))
		}
	}
	for _, c := range p.Careers {
		for _, t := range c.Tiers {
			for _, r := range t.RequiredSkills {
				check("career "+c.Code+" required_skills", r.Skill, ErrInvalidCareerContent)
			}
			for _, r := range t.SkillXPPerShift {
				check("career "+c.Code+" skill_xp_per_shift", r.Skill, ErrInvalidCareerContent)
			}
		}
	}
	for _, c := range p.Courses {
		for _, r := range c.SkillRewards {
			check("course "+c.Code+" skill_rewards", r.Skill, ErrInvalidCourseContent)
		}
	}
	for _, c := range p.Crimes {
		for _, r := range c.RequiredSkills {
			check("crime "+c.Code+" required_skills", r.Skill, ErrInvalidCrimeContent)
		}
		for _, w := range c.Success.SkillWeights {
			check("crime "+c.Code+" skill_weights", w.Skill, ErrInvalidCrimeContent)
		}
		for _, x := range c.Reward.SkillXP {
			check("crime "+c.Code+" skill_xp", x.Skill, ErrInvalidCrimeContent)
		}
	}
	for _, b := range p.SettlementBuildings {
		if b.Trains != nil {
			check("settlement building "+b.Code+" trains", b.Trains.Skill, ErrInvalidItemContent)
			if b.Trains.XP < 1 || len(b.Produces) == 0 {
				*problems = append(*problems, fmt.Errorf("%w: settlement building %q trains a skill but is not a workplace or gives no experience",
					ErrInvalidItemContent, b.Code))
			}
		}
	}
	for _, rec := range p.Recruitment {
		for _, s := range rec.Skills {
			check("recruitment pool", s.Skill, ErrInvalidItemContent)
		}
	}
}

// knownSkillCodes is the catalogue's codes; a pack with no skills.yml (a test
// pack) falls back to the codes the engine names.
func (p *Pack) knownSkillCodes() []string {
	var out []string
	if len(p.Skills) == 0 {
		for _, c := range player.SkillCodes() {
			out = append(out, string(c))
		}
		return out
	}
	for _, s := range p.Skills {
		out = append(out, s.Code)
	}
	return out
}
