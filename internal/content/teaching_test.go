package content

import "testing"

// The teaching network is coherent (docs/adr/0071): every craft has a journeyman course that certifies and rewards the craft's own skill,
// a workshop that trains that skill, and the learning numbers rise with the master.
func TestShippedTeachingIsCoherent(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	cd, ok := snap.Crafting()
	if !ok || len(cd.Apprenticeship.Crafts) < 13 {
		t.Fatalf("crafting.yml carries the teaching and the thirteen crafts: %d", len(cd.Apprenticeship.Crafts))
	}
	for _, c := range cd.Apprenticeship.Crafts {
		course, ok := snap.CourseDef(c.Course)
		if !ok || !course.Certifies {
			t.Errorf("%s: course %s must certify", c.Craft, c.Course)
			continue
		}
		rewards := false
		for _, r := range course.SkillRewards {
			rewards = rewards || r.Skill == c.Craft
		}
		if !rewards {
			t.Errorf("%s: the course %s rewards no xp of the craft", c.Craft, c.Course)
		}
		trained := false
		for _, w := range c.Workplaces {
			d, ok := snap.SettlementBuildingDef(w)
			if !ok {
				t.Errorf("%s: workshop %s is no building", c.Craft, w)
				continue
			}
			trained = trained || (d.Trains != nil && d.Trains.Skill == c.Craft)
		}
		if !trained {
			t.Errorf("%s: no workshop of the craft trains the skill by working", c.Craft)
		}
		if got, back := cd.Apprenticeship.CraftOfCourse(c.Course); !back || got.Craft != c.Craft {
			t.Errorf("%s: the course does not lead back to the craft", c.Craft)
		}
	}
	a := cd.Apprenticeship
	if a.LearningBPS(3) <= a.LearningBPS(0) || a.LearningBPS(100) != a.LearningCapBPS || a.LearningBPS(0) < 10_000 {
		t.Errorf("a better master teaches faster, up to the cap: %d %d", a.LearningBPS(0), a.LearningBPS(3))
	}
	if cd.Teaching.TeacherXPBPS(0) != 10_000 || cd.Teaching.TeacherXPBPS(2) != 10_200 || cd.Teaching.TeacherXPBPS(1000) != 10_000+cd.Teaching.XPBonusCapBPS {
		t.Errorf("the teacher's skill adds a little and stops at the cap")
	}
	if snap.TeachingMinLevel() != 3 {
		t.Errorf("the least level to teach a craft is the content's: %d", snap.TeachingMinLevel())
	}
}
