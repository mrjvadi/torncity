package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «پژوهش» (docs/adr/0048), drawn for Telegram: the settlement's slots, the research buildings with their scholars,
// the projects running with the pace each started at, the sharing pacts and the breakthrough progress. The acts are
// buttons: take or leave a scholar's post, offer, answer or end a pact.

// Screen and address.
const (
	ScreenResearchDesk = village.ScreenResearch
	AddrResearchDesk   = village.AddrResearchDesk
)

// ResearchBoardView is the research desk.
type ResearchBoardView = village.ResearchBoardView

// ResearchBoard renders the research desk.
func ResearchBoard(c Context, v village.ResearchBoardView) *presenter.Response {
	return c.withView(renderResearchBoard(c, v), ScreenResearchDesk, v)
}

// researchFieldName is a field of work as a player reads it.
func (c Context) researchFieldName(code string) string {
	return c.named("research.field."+code, code)
}

// researchNotes says what moved a project's price and pace away from the plain one, in a few short phrases.
func researchNotes(c Context, speed, ahead, discount, share int64) string {
	var notes []string
	if speed > 10_000 {
		notes = append(notes, c.T("research.note.speed", map[string]any{"percent": PercentFromBPS(c, int(speed-10_000))}))
	}
	if ahead > 10_000 {
		notes = append(notes, c.T("research.note.ahead", map[string]any{"percent": PercentFromBPS(c, int(ahead-10_000))}))
	}
	if discount > 0 {
		notes = append(notes, c.T("research.note.discount", map[string]any{"percent": PercentFromBPS(c, int(discount))}))
	}
	if share > 0 {
		notes = append(notes, c.T("research.note.share", map[string]any{"percent": PercentFromBPS(c, int(share))}))
	}
	return c.list(notes)
}

func renderResearchBoard(c Context, v village.ResearchBoardView) *presenter.Response {
	kb := keyboards.New()
	head := body(
		c.T("research.title", map[string]any{"name": v.Name}),
		c.T("research.capacity", map[string]any{"running": FormatNumber(c, int64(v.Running)), "capacity": FormatNumber(c, int64(v.Capacity))}),
		c.T("research.frontier", map[string]any{"frontier": FormatNumber(c, int64(v.Frontier))}),
	)

	var slots []string
	for _, s := range v.Slots {
		if s.Ref == "free" {
			slots = append(slots, c.T("research.slot.free", map[string]any{"used": FormatNumber(c, int64(s.Used)), "capacity": FormatNumber(c, int64(s.Capacity))}))
			continue
		}
		slots = append(slots, c.T("research.slot.building", map[string]any{"building": c.SettlementBuildingName(s.Building),
			"used": FormatNumber(c, int64(s.Used)), "capacity": FormatNumber(c, int64(s.Capacity)),
			"bonus": PercentFromBPS(c, int(s.BonusBPS)), "staff": PercentFromBPS(c, int(s.StaffBPS))}))
	}

	var houses []string
	if len(v.Personal) > 0 {
		houses = append(houses, c.T("village.work.personal_warn", map[string]any{"needs": personalList(c, v.Personal), "until": FormatDate(c, v.PersonalUntil)}))
	}
	for _, b := range v.Buildings {
		name := c.SettlementBuildingName(b.Building)
		args := map[string]any{"building": name, "players": FormatNumber(c, int64(b.Players)), "npcs": FormatNumber(c, int64(b.NPCs)),
			"needed": FormatNumber(c, int64(b.Needed)), "posts": FormatNumber(c, int64(b.Posts)), "wage": FormatMoney(c, b.Wage)}
		if b.Open {
			houses = append(houses, c.T("research.building.open", args))
		} else if b.Idle != "" {
			args["reason"] = c.T("research.idle."+b.Idle, nil)
			houses = append(houses, c.T("research.building.idle", args))
		} else {
			houses = append(houses, c.T("research.building.waiting", args))
		}
		var ups []string
		for _, u := range b.Upkeep {
			args := map[string]any{"item": c.named("component."+u.Item.Code, u.Item.Name), "qty": FormatNumber(c, u.Qty), "have": FormatNumber(c, u.Have)}
			key := "research.upkeep_item"
			if u.StandIn.Code != "" && !v.StandInUntil.IsZero() {
				key = "research.upkeep_item_stand_in"
				args["stand"], args["stand_have"], args["until"] = c.named("component."+u.StandIn.Code, u.StandIn.Name), FormatNumber(c, u.StandInHave), FormatDate(c, v.StandInUntil)
			}
			ups = append(ups, c.T(key, args))
		}
		if len(ups) > 0 {
			houses = append(houses, c.T("research.building.upkeep", map[string]any{"items": c.list(ups)}))
		}
		switch {
		case b.Mine:
			if btn, ok := keyboards.Button(c.T("research.button.leave", nil), AddrResearchDesk, village.ResearchActionLeave); ok {
				kb.Row(btn)
			}
		case b.CanTake:
			if btn, ok := keyboards.Button(c.T("research.button.post", map[string]any{"building": name}), AddrResearchDesk, village.ResearchActionPost, b.ID); ok {
				kb.Row(btn)
			}
		}
	}
	if len(v.Buildings) == 0 {
		houses = append(houses, c.T("research.no_buildings", nil))
	}

	var projects []string
	for _, p := range v.Projects {
		where := c.T("research.where_free", nil)
		if p.Slot != "free" {
			where = c.SettlementBuildingName(p.Building)
		}
		line := c.T("research.project", map[string]any{"knowledge": c.SettlementKnowledgeName(p.Knowledge), "where": where,
			"time": FormatClock(c, p.FinishAt), "duration": FormatDuration(c, p.Left), "speed": PercentFromBPS(c, int(p.SpeedBPS))})
		if notes := researchNotes(c, 0, p.AheadBPS, p.DiscountBPS, p.ShareBPS); notes != "" {
			line += " (" + notes + ")"
		}
		projects = append(projects, line)
	}

	var pacts []string
	for _, p := range v.Pacts {
		pacts = append(pacts, c.T("research.pact."+p.State, map[string]any{"partner": p.Partner.Name}))
		if !v.MayShare {
			continue
		}
		switch p.State {
		case village.ResearchPactIncoming:
			if btn, ok := keyboards.Button(c.T("research.button.accept", map[string]any{"partner": p.Partner.Name}), AddrResearchDesk, village.ResearchActionAccept, p.ID); ok {
				kb.Row(btn)
			}
			if btn, ok := keyboards.Button(c.T("research.button.decline", nil), AddrResearchDesk, village.ResearchActionDecline, p.ID); ok {
				kb.Row(btn)
			}
		default:
			if btn, ok := keyboards.Button(c.T("research.button.end", map[string]any{"partner": p.Partner.Name}), AddrResearchDesk, village.ResearchActionEnd, p.ID); ok {
				kb.Row(btn)
			}
		}
	}
	if v.MayShare {
		var buttons []presenter.Button
		for _, n := range v.Neighbours {
			if btn, ok := keyboards.Button(c.T("research.button.propose", map[string]any{"partner": n.Name}), AddrResearchDesk, village.ResearchActionPropose, n.Code); ok {
				buttons = append(buttons, btn)
			}
		}
		kb.Grid(2, buttons...)
	}

	var exp []string
	for _, e := range v.Experience {
		exp = append(exp, c.T("research.experience_line", map[string]any{"field": c.researchFieldName(e.Field), "points": FormatNumber(c, e.Points)}))
	}

	sections := []string{head, titled(c, "research.slots_title", slots), titled(c, "research.buildings_title", houses), titled(c, "research.projects_title", projects)}
	if len(pacts) > 0 || v.MayShare {
		pactHead := c.T("research.pacts_title", map[string]any{"cap": PercentFromBPS(c, int(v.ShareCapBPS))})
		if len(pacts) == 0 {
			pacts = append(pacts, c.T("research.no_pacts", nil))
		}
		sections = append(sections, pactHead+"\n"+body(pacts...))
	}
	if len(exp) > 0 {
		sections = append(sections, titled(c, "research.experience_title", exp))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrKnowledgeList, RefreshData: AddrResearchDesk}))
	return c.respond(paragraphs(sections...), kb.Build())
}

// titled is a heading over a list of lines; nothing when the list is empty.
func titled(c Context, key string, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return c.T(key, nil) + "\n" + body(lines...)
}
