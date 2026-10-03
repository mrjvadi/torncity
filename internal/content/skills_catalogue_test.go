package content

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/player"
)

// TestShippedSkillCatalogueHasTheEngineCodes: the codes some rule branches on
// (player.SkillCodes) must be in skills.yml, or a branch would read a skill
// nobody can train. The other skills are free content.
func TestShippedSkillCatalogueHasTheEngineCodes(t *testing.T) {
	p := shippedPack(t)
	have := map[string]bool{}
	for _, s := range p.Skills {
		have[s.Code] = true
	}
	for _, c := range player.SkillCodes() {
		if !have[string(c)] {
			t.Errorf("skills.yml is missing %q, which the engine names in code", c)
		}
	}
	for _, code := range []string{"carpentry", "masonry", "smithing", "farming", "herbalism"} {
		if !have[code] {
			t.Errorf("skills.yml is missing the village skill %q", code)
		}
	}
}

// TestSkillReferencesMustBeListed: a course naming a skill the catalogue does
// not list is refused.
func TestSkillReferencesMustBeListed(t *testing.T) {
	p := shippedPack(t)
	p.Courses[0].SkillRewards = append(p.Courses[0].SkillRewards, SkillXPDef{Skill: "alchemy", XP: 1})
	if err := p.Validate(); err == nil {
		t.Fatal("a course rewarding an unlisted skill must be refused")
	}
}
