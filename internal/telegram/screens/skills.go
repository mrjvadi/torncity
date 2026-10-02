package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// trained reports whether the player has put anything into this skill yet.
func skillTrained(l SkillLine) bool { return l.Level > 0 || l.XP > 0 }

// Skills renders the skill list.
//
// Only skills the player has trained are shown. Nine rows of "level 0"
// describe the whole game to someone who has not played it yet, which reads
// as a wall of things they have failed at; a skill appears here the moment it
// has any XP, and until then the screen says in one line how skills are
// gained.
func Skills(c Context, v SkillsView) *presenter.Response {
	return c.withView(renderSkills(c, v), ScreenSkills, v)
}

func renderSkills(c Context, v SkillsView) *presenter.Response {
	rows := make([]string, 0, len(v.Lines))
	for _, line := range v.Lines {
		if !skillTrained(line) {
			continue
		}
		// The skill's NAME is content keyed on its code, so a skill reads as
		// a word in the player's language rather than as the identifier the
		// database stores.
		args := map[string]any{
			"skill":   c.T("skill."+line.Code, nil),
			"level":   line.Level,
			"percent": line.Percent,
		}
		key := "skills.line"
		if line.Max {
			key = "skills.line_max"
		}
		rows = append(rows, c.T(key, args))
	}

	content := body(rows...)
	if len(rows) == 0 {
		content = c.T("skills.empty", nil)
	}

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrSkills}))

	return c.respond(paragraphs(c.T("skills.title", nil), content), kb.Build())
}
