package content

import (
	"errors"
	"testing"
)

func TestShippedRecruitmentIsUsable(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Recruitment) != 1 {
		t.Fatalf("recruitment sections = %d, want 1", len(pack.Recruitment))
	}
	d := pack.Recruitment[0]
	if _, ok := d.Skill("engineering"); !ok {
		t.Fatal("no city has engineers: the research lab's gap cannot be recruited")
	}
	if d.MaxLevel() < 3 || d.LevelShare(3) == 0 {
		t.Fatalf("no engineer of level 3 exists (max level %d)", d.MaxLevel())
	}
	if d.ColBPS(d.ReferenceCostOfLiving) != 10_000 || d.EducationBPS("nowhere") != 10_000 {
		t.Fatal("the reference city is not the standard")
	}
}

func TestRecruitmentValidationRefusesNonsense(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, spoil := range map[string]func(*RecruitmentDef){
		"unknown skill":       func(d *RecruitmentDef) { d.Skills[0].Skill = "alchemy" },
		"levels over whole":   func(d *RecruitmentDef) { d.Levels[0].ShareBPS = 9_000 },
		"flat curve":          func(d *RecruitmentDef) { d.Acceptance.FullBPS = d.Acceptance.FloorBPS },
		"unknown city":        func(d *RecruitmentDef) { d.Cities[0].City = "atlantis" },
		"no preference":       func(d *RecruitmentDef) { d.Preferences = nil },
		"five salary presets": func(d *RecruitmentDef) { d.Presets.SalaryBPS = []int64{1, 2, 3, 4, 5} },
		"patient forever":     func(d *RecruitmentDef) { d.Staff.UnpaidPeriods = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			p := *pack
			d := pack.Recruitment[0]
			d.Skills = append([]RecruitSkillDef(nil), d.Skills...)
			d.Levels = append([]RecruitLevelDef(nil), d.Levels...)
			d.Cities = append([]RecruitCityDef(nil), d.Cities...)
			spoil(&d)
			p.Recruitment = []RecruitmentDef{d}
			var problems []error
			p.validateRecruitment(&problems)
			if len(problems) == 0 || !errors.Is(problems[0], ErrInvalidRecruitmentContent) {
				t.Fatalf("validation = %v, want a recruitment problem", problems)
			}
		})
	}
}
