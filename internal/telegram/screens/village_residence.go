package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Residence: becoming a resident of a village (settlement.join) and going
// back home (settlement.leave). A player has exactly one home; moving it is a
// two-step act (a confirm screen, then the move) and is followed by a
// cool-down (config settlement.residence_cooldown).

// Structured screens of the residence commands (clients).
const (
	ScreenResidenceConfirm = village.ScreenResidenceConfirm
	ScreenResidenceDone    = village.ScreenResidenceDone
)

func residenceKey(v ResidenceView) string {
	if v.Leaving {
		return "leave"
	}
	return "join"
}

func residenceAddr(v ResidenceView) string {
	if v.Leaving {
		return AddrVillageLeave
	}
	return AddrVillageJoin
}

// ResidenceAsk renders the confirm step. Shared in a group, so it names no
// player and no home of anyone's: the move is the presser's own.
func ResidenceAsk(c Context, v ResidenceView) *presenter.Response {
	return c.withView(renderResidenceAsk(c, v), ScreenResidenceConfirm, v)
}

func renderResidenceAsk(c Context, v ResidenceView) *presenter.Response {
	k := residenceKey(v)
	args := map[string]any{"village": v.Village, "home": v.Home, "cooldown": FormatDuration(c, v.Cooldown)}
	text := paragraphs(c.T("village.residence."+k+".ask_title", args), c.T("village.residence."+k+".ask_body", args))
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("village.residence."+k+".button_yes", nil), residenceAddr(v), ResidenceConfirm); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview}))
	return c.respond(text, kb.Build())
}

// ResidenceDone renders the result of the move.
func ResidenceDone(c Context, v ResidenceView) *presenter.Response {
	return c.withView(renderResidenceDone(c, v), ScreenResidenceDone, v)
}

func renderResidenceDone(c Context, v ResidenceView) *presenter.Response {
	k := residenceKey(v)
	args := map[string]any{
		"village": v.Village, "home": v.Home, "cooldown": FormatDuration(c, v.Cooldown),
		"population": FormatNumber(c, v.Population),
	}
	text := paragraphs(c.T("village.residence."+k+".done_title", args), c.T("village.residence."+k+".done_body", args))
	kb := keyboards.New()
	kb.Add(c.T("village.button.overview", nil), AddrVillageOverview)
	kb.Nav(c.nav(keyboards.Nav{}))
	return c.respond(text, kb.Build())
}
