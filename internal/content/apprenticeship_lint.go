package content

// apprenticeship checks the teaching and apprenticeship numbers of the crafting block (docs/adr/0071).
func (l *schemaLint) apprenticeship(d CraftingDef) {
	t, a := d.Teaching, d.Apprenticeship
	if t.MinCraftLevel == 0 && a.MasterMinLevel == 0 && len(a.Crafts) == 0 {
		return // a trimmed pack with no teaching block (the shipped content has one: TestShippedTeachingIsCoherent)
	}
	if t.MinCraftLevel < 1 || t.NPCLevel < 1 || t.NPCLevel > t.MinCraftLevel+5 || t.XPPerLevelBPS < 0 || t.XPBonusCapBPS < 0 || t.XPBonusCapBPS > 10_000 ||
		t.DirectoryKM < 1 || t.DirectoryMax < 1 {
		l.bad("crafting.teaching: a level, a share or the directory reach is out of range")
	}
	if a.MasterMinLevel < 1 || a.MaxPerMaster < 1 || a.NPCMasterLevel < 1 || a.TermMin < 1 || a.TermMax < a.TermMin || a.TermDefault < a.TermMin ||
		a.TermDefault > a.TermMax || a.WageShareMinBPS < 0 || a.WageShareMinBPS > 10_000 || a.WageShareDefaultBPS < a.WageShareMinBPS ||
		a.WageShareDefaultBPS > 10_000 || a.FeeMax < 0 || a.LearningBaseBPS < 10_000 || a.LearningCapBPS < a.LearningBaseBPS || a.LearningPerLevelBPS < 0 ||
		a.ExamShifts < 1 || len(a.DismissReasons) == 0 || len(a.Crafts) == 0 {
		l.bad("crafting.apprenticeship: a term, a share, a level, the learning bonus or the exam is out of range")
	}
	courses := map[string]bool{}
	for _, c := range l.p.Courses {
		courses[c.Code] = c.Certifies
	}
	seen := map[string]bool{}
	if len(l.p.Courses) == 0 {
		return
	}
	for _, c := range a.Crafts {
		key := "crafting.apprenticeship.crafts[" + c.Craft + "]"
		switch {
		case seen[c.Craft] || !l.skills[c.Craft]:
			l.bad("%s: unknown or repeated skill", key)
		case !courses[c.Course]:
			l.bad("%s: the course %q does not exist or does not certify", key, c.Course)
		case len(c.Workplaces) == 0:
			l.bad("%s: a craft has at least one workshop", key)
		}
		seen[c.Craft] = true
		for _, w := range c.Workplaces {
			if !l.buildingKnown(w) {
				l.bad("%s: workshop %q is not a settlement building", key, w)
			}
		}
	}
}

func (l *schemaLint) buildingKnown(code string) bool {
	for _, b := range l.p.SettlementBuildings {
		if b.Code == code {
			return true
		}
	}
	return false
}
