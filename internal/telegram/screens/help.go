package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Help answers a message the game did not understand: an unknown or
// misspelled command, a button from an older version of the game, or plain
// text. The gateway sends it instead of publishing anything, because an
// unknown command published to the command stream reaches no one and the
// player would otherwise get no answer at all.
//
// It says so in one line, lists what can be typed, and puts the main screens
// one press away, so the way forward is a button rather than another guess.
func Help(c Context) *presenter.Response {
	profile, _ := keyboards.Button(c.T("button.profile", nil), AddrProfile)
	worldMap, _ := keyboards.Button(c.T("button.map", nil), AddrMap)
	job, _ := keyboards.Button(c.T("job.button.my_job", nil), AddrJobStatus)
	study, _ := keyboards.Button(c.T("education.button.open", nil), AddrEducation)
	bank, _ := keyboards.Button(c.T("button.bank", nil), AddrBank)
	skills, _ := keyboards.Button(c.T("button.skills", nil), AddrSkills)
	social, _ := keyboards.Button(c.T("button.social", nil), AddrFriendList)
	settings, _ := keyboards.Button(c.T("button.settings", nil), AddrSettings)

	kb := keyboards.New()
	kb.Row(profile, worldMap)
	kb.Row(job, study)
	kb.Row(bank, skills)
	kb.Row(social, settings)

	return c.respond(paragraphs(c.T("help.unknown", nil), c.T("help.body", nil)), kb.Build())
}
