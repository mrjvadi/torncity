package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// SkillLine is one trainable skill as the list shows it.
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
}

// SkillsView is the player's whole skill list. It is short by construction —
// the set of skills is closed in the domain — so it does not paginate.
type SkillsView struct {
	Lines []SkillLine
}

// Skills renders the skill list.
func Skills(c Context, v SkillsView) *presenter.Response {
	lines := make([]string, 0, len(v.Lines)+2)
	lines = append(lines, c.T("skills.title", nil))
	lines = append(lines, "")

	if len(v.Lines) == 0 {
		lines = append(lines, c.T("skills.empty", nil))
	}
	for _, line := range v.Lines {
		key := "skills.line"
		if line.Level == 0 && line.XP == 0 {
			key = "skills.line_untrained"
		}
		lines = append(lines, c.T(key, map[string]any{
			// The skill's NAME is content keyed on its code, so a skill
			// reads as a word in the player's language rather than as the
			// identifier the database stores.
			"skill":   c.T("skill."+line.Code, nil),
			"level":   line.Level,
			"xp":      line.XP,
			"next":    line.Next,
			"percent": line.Percent,
		}))
	}

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrSkills}))

	return c.respond(body(lines...), kb.Build())
}
