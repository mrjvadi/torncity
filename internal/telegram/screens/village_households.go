package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «خانوارها» (docs/adr/0073), drawn for Telegram: who lives in the settlement as an NPC household, how content they are, what
// they lack, the news of who came and went, and the buttons of the head (admission, the tax on a wage) and of a dwelling's owner.

// Screen and address.
const (
	ScreenHouseholds = village.ScreenHouseholds
	AddrHouseholds   = village.AddrHouseholds
)

// HouseholdsView is the households screen.
type HouseholdsView = village.HouseholdsView

// Households renders the households screen.
func Households(c Context, v village.HouseholdsView) *presenter.Response {
	return c.withView(renderHouseholds(c, v), ScreenHouseholds, v)
}

func renderHouseholds(c Context, v village.HouseholdsView) *presenter.Response {
	kb := keyboards.New()
	head := c.T("households.title", map[string]any{"name": v.Name})
	if v.Phase == village.HouseholdPhaseNone || v.Phase == village.HouseholdPhaseOff {
		return c.respond(paragraphs(head, c.T("households.none", nil)), householdsNav(c, kb))
	}
	lines := []string{
		c.T("households.counts", map[string]any{"living": FormatNumber(c, v.Living), "persons": FormatNumber(c, v.Persons),
			"earners": FormatNumber(c, v.Earners), "homes": FormatNumber(c, v.Homes)}),
		c.T("households.mood", map[string]any{"percent": PercentFromBPS(c, int(v.MoodBPS))}),
	}
	if v.Worst != "" {
		lines = append(lines, c.T("households.worst", map[string]any{"need": c.T("households.need."+v.Worst, nil)}))
	}
	phase := c.T("households.phase."+v.Phase, map[string]any{"target": FormatNumber(c, v.TargetPool)})
	if v.IncomeTaxBPS > 0 {
		lines = append(lines, c.T("households.tax", map[string]any{"percent": PercentFromBPS(c, int(v.IncomeTaxBPS))}))
	}
	admit := c.T("households.admission.open", nil)
	if !v.Open {
		admit = c.T("households.admission.closed", nil)
	}
	var news []string
	for _, n := range v.News {
		if n.Kind == "left" || n.Kind == "notice" {
			if n.Detail != "" {
				n.Detail = c.T("households.need."+n.Detail, nil)
			}
		}
		news = append(news, c.T("households.news."+n.Kind, map[string]any{"detail": n.Detail, "time": FormatClock(c, n.At), "date": FormatDate(c, n.At)}))
	}
	if v.MayAdmit {
		key, label := "households.button.admit", village.HouseholdActionAdmit
		if v.Open {
			key, label = "households.button.close", village.HouseholdActionClose
		}
		if b, ok := keyboards.Button(c.T(key, nil), AddrHouseholds, label); ok {
			kb.Row(b)
		}
	}
	if v.MayTax && v.Phase == village.HouseholdPhaseLive {
		var row []presenter.Button
		for _, p := range v.TaxPresets {
			if b, ok := keyboards.Button(c.T("households.button.tax", map[string]any{"percent": PercentFromBPS(c, int(p))}), AddrHouseholds, village.HouseholdActionTax, strconv.FormatInt(p, 10)); ok {
				row = append(row, b)
			}
		}
		kb.Row(row...)
	}
	var dwell []string
	for _, d := range v.Dwellings {
		name := c.SettlementBuildingName(d.Building)
		if d.Open {
			dwell = append(dwell, c.T("households.dwelling.open", map[string]any{"building": name, "rent": FormatMoney(c, d.Rent), "tenants": FormatNumber(c, d.Tenants)}))
			if b, ok := keyboards.Button(c.T("households.button.rent_close", map[string]any{"building": name}), AddrHouseholds, village.HouseholdActionRentClose, d.Building.Code); ok {
				kb.Row(b)
			}
			continue
		}
		dwell = append(dwell, c.T("households.dwelling.closed", map[string]any{"building": name, "slots": FormatNumber(c, d.Slots)}))
		var row []presenter.Button
		for _, p := range v.RentPresets {
			if b, ok := keyboards.Button(c.T("households.button.rent_open", map[string]any{"rent": FormatMoney(c, p)}), AddrHouseholds, village.HouseholdActionRentOpen, d.Building.Code, strconv.FormatInt(p, 10)); ok {
				row = append(row, b)
			}
		}
		kb.Row(row...)
	}
	blocks := []string{head, phase, body(lines...), admit}
	if len(news) > 0 {
		blocks = append(blocks, c.T("households.news_title", nil)+"\n"+body(news...))
	}
	if len(dwell) > 0 {
		blocks = append(blocks, c.T("households.dwellings_title", nil)+"\n"+body(dwell...))
	}
	return c.respond(paragraphs(blocks...), householdsNav(c, kb))
}

// householdsNav closes the keyboard with the back and refresh row.
func householdsNav(c Context, kb *keyboards.Builder) *presenter.Keyboard {
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrSettlementWho, RefreshData: AddrHouseholds}))
	return kb.Build()
}
