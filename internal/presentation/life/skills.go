package life

// SkillLine is one skill as the list shows it.
//
// Percent is progress toward the NEXT level, worked out from the domain's
// curve by the use case. The screen does not compute it: how far along a
// skill is, is a rule, and a second copy of that rule living in a layout
// function is a second answer waiting to disagree with the first.
type SkillLine struct {
	// Code is the domain skill code; the label is looked up from it.
	Code    string
	Level   int
	XP      int64
	Next    int64
	Percent int
	// Max says the skill is at the top of its curve and has no next level.
	Max bool
}

// SkillsView is the player's skill list. It is short by construction — the
// set of skills is closed in the domain — so it does not paginate.
type SkillsView struct {
	Lines []SkillLine
}
