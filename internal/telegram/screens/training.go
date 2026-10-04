package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TrainingHome renders the training screen: the player's own condition and the
// places they can train at (docs/research/2026-10-03-activities-audit.md section 7).
func TrainingHome(c Context, v life.TrainingHomeView) *presenter.Response {
	return c.withView(renderTrainingHome(c, v), life.ScreenTrainingHome, v)
}

func renderTrainingHome(c Context, v life.TrainingHomeView) *presenter.Response {
	state := body(
		c.T("training.energy", map[string]any{"energy": FormatNumber(c, int64(v.Energy)), "max": FormatNumber(c, int64(v.MaxEnergy)),
			"cost": FormatNumber(c, int64(v.EnergyCost))}),
		c.T("training.stamina", map[string]any{"stamina": FormatNumber(c, int64(v.Stamina))}),
		c.T("training.strength", map[string]any{"level": FormatNumber(c, int64(v.StrengthLevel))}),
	)
	kb := keyboards.New()
	lines := []string{c.T("training.venues", nil)}
	for _, venue := range v.Venues {
		args := map[string]any{"venue": c.T("training.venue."+venue.Code, nil),
			"percent": FormatNumber(c, venue.EfficiencyBPS/100), "fee": FormatMoney(c, venue.Fee)}
		switch {
		case venue.Available && venue.Unkept:
			lines = append(lines, c.T("training.venue_unkept", args))
		case venue.Available && venue.Fee > 0:
			lines = append(lines, c.T("training.venue_line_fee", args))
		case venue.Available:
			lines = append(lines, c.T("training.venue_line", args))
		default:
			args["building"] = c.SettlementBuildingName(*venue.Missing)
			lines = append(lines, c.T("training.venue_missing", args))
		}
		if venue.Available {
			if btn, ok := keyboards.Button(c.T("training.button.start", args), life.AddrTrainingStart, venue.Code); ok {
				kb.Row(btn)
			}
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: life.AddrActivitiesHub, RefreshData: life.AddrTrainingHome}))
	return c.respond(paragraphs(c.T("training.title", nil), state, body(lines...)), kb.Build())
}

// Trained renders a finished session.
func Trained(c Context, v life.TrainedView) *presenter.Response {
	return c.withView(renderTrained(c, v), life.ScreenTrained, v)
}

func renderTrained(c Context, v life.TrainedView) *presenter.Response {
	lines := []string{
		c.T("training.done", map[string]any{"venue": c.T("training.venue."+v.Venue, nil)}),
		c.T("training.gain_stamina", map[string]any{"stamina": FormatNumber(c, int64(v.Stamina))}),
		c.T("training.gain_strength", map[string]any{"xp": FormatNumber(c, v.StrengthXP)}),
	}
	if v.MaxEnergyAdded > 0 {
		lines = append(lines, c.T("training.gain_max_energy", map[string]any{"added": FormatNumber(c, int64(v.MaxEnergyAdded))}))
	}
	if v.StrengthLevel > 0 {
		lines = append(lines, c.T("training.level_up", map[string]any{"level": FormatNumber(c, int64(v.StrengthLevel))}))
	}
	if v.Fee > 0 {
		lines = append(lines, c.T("training.paid", map[string]any{"fee": FormatMoney(c, v.Fee)}))
	}
	lines = append(lines, c.T("training.energy_left", map[string]any{"energy": FormatNumber(c, int64(v.Energy)), "max": FormatNumber(c, int64(v.MaxEnergy))}))
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("training.button.again", nil), life.AddrTrainingHome); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: life.AddrActivitiesHub}))
	return c.respond(body(lines...), kb.Build())
}
