package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Survival (docs/adr/0070): thirst, warmth, the weather, the dress and what can be done about them. Private to the player.

// Addresses of the survival screens.
const (
	AddrSurvival = "health:survival"
	AddrDrink    = "health:drink"
	AddrWear     = "health:wear"
	AddrRemove   = "health:remove"
	AddrWarm     = "health:warm"
	AddrCure     = "health:cure"
)

// Survival renders the survival screen.
func Survival(c Context, v SurvivalView) *presenter.Response {
	return c.withView(renderSurvival(c, v), ScreenSurvival, v)
}

func renderSurvival(c Context, v SurvivalView) *presenter.Response {
	kb := keyboards.New()
	title := htmlBold(htmlEscape(c.T("survival.title", nil)))
	if !v.Founded {
		text := c.T("survival.exempt", nil)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrSurvival}))
		return c.respond(paragraphs(title, htmlEscape(text)), kb.Build()).MarkPrivate().AsHTML()
	}
	var lines []string
	if v.Notice != "" {
		lines = append(lines, c.T("survival.notice."+v.Notice, nil))
	}
	lines = append(lines,
		c.T("survival.thirst", map[string]any{"points": FormatNumber(c, int64(v.Thirst)), "state": c.T("survival.thirst_level."+itoa(v.ThirstLevel), nil)}),
		c.T("survival.warmth", map[string]any{"points": FormatNumber(c, int64(v.Warmth)), "state": c.T("survival.warmth_level."+v.WarmthLevel, nil)}))
	if v.Env != nil {
		key, deg := "survival.weather", int64(v.Env.TempC)
		if deg < 0 {
			key, deg = "survival.weather_below", -deg // a minus sign next to a number reads badly: say "below zero"
		}
		w := c.T(key, map[string]any{"temp": FormatNumber(c, deg), "season": c.T("survival.season."+itoa(v.Env.Season), nil)})
		if v.Env.Rain {
			w = body(w, c.T("survival.rain", nil))
		}
		if v.Env.Wind {
			w = body(w, c.T("survival.wind", nil))
		}
		lines = append(lines, w)
	}
	if v.Sheltered {
		lines = append(lines, c.T("survival.sheltered", nil))
	}
	for _, i := range v.Illness {
		key := "survival.ill"
		if i.Treated {
			key = "survival.ill_treated"
		}
		lines = append(lines, body(c.T(key, map[string]any{"kind": c.T("survival.illness."+i.Kind, nil)}), clockLine(c, "survival.ill_until", i.EndsAt)))
	}
	dress := []string{c.T("survival.dress_title", nil)}
	if len(v.Worn) == 0 {
		dress = append(dress, c.T("survival.dress_none", nil))
	}
	for _, g := range v.Worn {
		dress = append(dress, c.T("survival.worn", map[string]any{"slot": c.T("survival.slot."+g.Slot, nil), "item": g.Item.Name,
			"warmth": FormatNumber(c, int64(g.Warmth)), "days": FormatNumber(c, int64(g.Left.Hours()/24))}))
	}
	lines = append(lines, body(dress...))
	// the buttons
	if v.WellOpen {
		addBtn(kb, c.T("survival.button.well", nil), AddrDrink, "well")
	}
	if v.Carried > 0 {
		addBtn(kb, c.T("survival.button.bag", map[string]any{"n": FormatNumber(c, v.Carried)}), AddrDrink, "bag")
	}
	if v.River {
		addBtn(kb, c.T("survival.button.boiled", nil), AddrDrink, "boiled")
		addBtn(kb, c.T("survival.button.river", nil), AddrDrink, "river")
	}
	for _, g := range v.Bag {
		addBtn(kb, c.T("survival.button.wear", map[string]any{"item": g.Item.Name}), AddrWear, g.Item.Code)
	}
	for _, g := range v.Kept {
		addBtn(kb, c.T("survival.button.wear", map[string]any{"item": g.Item.Name}), AddrWear, g.ID)
	}
	for _, g := range v.Worn {
		addBtn(kb, c.T("survival.button.remove", map[string]any{"item": g.Item.Name}), AddrRemove, g.Slot)
	}
	if v.WarmingOpen {
		addBtn(kb, c.T("survival.button.warm", nil), AddrWarm)
	}
	if v.CanCure {
		addBtn(kb, c.T("survival.button.cure", nil), AddrCure)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrSurvival}))
	return c.respond(paragraphs(append([]string{title}, htmlEscapeAll(lines)...)...), kb.Build()).MarkPrivate().AsHTML()
}

func addBtn(kb *keyboards.Builder, label string, parts ...string) {
	if btn, ok := keyboards.Button(label, parts...); ok {
		kb.Row(btn)
	}
}

func htmlEscapeAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = htmlEscape(s)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
