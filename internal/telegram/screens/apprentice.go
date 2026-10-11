package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The teaching network (docs/adr/0071): one's apprenticeships, the directory of teachers of a craft, and the refusals. All private.

// Addresses of the teaching screens.
const (
	AddrApprentices = "settlement:apprentice.mine"
	AddrApprAccept  = "settlement:apprentice.accept"
	AddrApprLeave   = "settlement:apprentice.leave"
	AddrApprDismiss = "settlement:apprentice.dismiss"
	AddrApprExam    = "settlement:apprentice.exam"
	AddrApprJoin    = "settlement:apprentice.join"
	AddrTeachers    = "education:teachers"
)

// Apprentices renders one's apprenticeships.
func Apprentices(c Context, v ApprenticesView) *presenter.Response {
	return c.withView(renderApprentices(c, v), ScreenApprentices, v)
}

func renderApprentices(c Context, v ApprenticesView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{}
	if v.Notice != "" {
		lines = append(lines, c.T("apprentice.notice."+v.Notice, nil))
	}
	if len(v.Lines) == 0 {
		lines = append(lines, c.T("apprentice.none", nil))
	}
	for _, l := range v.Lines {
		who := c.T("apprentice.npc_master", nil)
		if !l.NPC && l.With.Name != "" {
			who = l.With.Name
		}
		head := c.T("apprentice.line."+l.Role, map[string]any{"craft": c.T("apprentice.craft."+l.Craft, nil), "who": who, "shop": l.Workshop.Name})
		terms := c.T("apprentice.terms", map[string]any{"days": FormatNumber(c, int64(l.TermDays)), "fee": FormatMoney(c, l.Fee),
			"share": PercentFromBPS(c, l.WageShareBPS)})
		state := c.T("apprentice.status."+l.Status, nil)
		if l.Status == "active" {
			state = body(state, clockLine(c, "apprentice.ends_at", l.EndsAt),
				c.T("apprentice.progress", map[string]any{"shifts": FormatNumber(c, int64(l.Shifts)), "need": FormatNumber(c, int64(l.ExamShifts))}))
		}
		if l.Certified {
			state = body(state, c.T("apprentice.certified", nil))
		}
		lines = append(lines, body(head, terms, state))
		if l.CanAccept {
			addBtn(kb, c.T("apprentice.button.accept", map[string]any{"fee": FormatMoney(c, l.Fee)}), AddrApprAccept, l.ID)
		}
		if l.CanExam {
			addBtn(kb, c.T("apprentice.button.exam", nil), AddrApprExam, l.ID, "pass")
			if l.Role == "master" {
				addBtn(kb, c.T("apprentice.button.exam_fail", nil), AddrApprExam, l.ID, "fail")
			}
		}
		if l.CanLeave {
			addBtn(kb, c.T("apprentice.button.leave", nil), AddrApprLeave, l.ID)
		}
		if l.CanDismiss {
			addBtn(kb, c.T("apprentice.button.dismiss", nil), AddrApprDismiss, l.ID, "idle")
		}
	}
	for _, j := range v.CanJoin {
		addBtn(kb, c.T("apprentice.button.join", map[string]any{"craft": c.T("apprentice.craft."+j.Craft, nil), "shop": j.Workshop.Name,
			"level": FormatNumber(c, int64(j.UpTo))}), AddrApprJoin, j.Workshop.Code)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrApprentices}))
	title := htmlBold(htmlEscape(c.T("apprentice.title", nil)))
	return c.respond(paragraphs(append([]string{title}, htmlEscapeAll(lines)...)...), kb.Build()).MarkPrivate().AsHTML()
}

// Teachers renders the directory of teachers of a craft.
func Teachers(c Context, v TeachersView) *presenter.Response {
	return c.withView(renderTeachers(c, v), ScreenTeachers, v)
}

func renderTeachers(c Context, v TeachersView) *presenter.Response {
	kb := keyboards.New()
	craft := c.T("apprentice.craft."+v.Craft, nil)
	lines := []string{c.T("teachers.intro", map[string]any{"craft": craft, "course": v.Course.Name})}
	if len(v.Entries) == 0 {
		lines = append(lines, c.T("teachers.none", nil))
	}
	for _, e := range v.Entries {
		args := map[string]any{"place": c.CityName(e.Settlement.Code, e.Settlement.Name), "km": FormatNumber(c, int64(e.KM)), "who": e.Teacher.Name,
			"level": FormatNumber(c, int64(e.CraftLevel)), "up_to": FormatNumber(c, int64(e.UpTo)), "fee": FormatMoney(c, e.Fee),
			"seats": FormatNumber(c, int64(e.FreeSeats))}
		key := "teachers.entry." + e.Kind
		if e.Here {
			key += "_here"
		}
		lines = append(lines, c.T(key, args))
	}
	for _, cr := range v.Crafts {
		if cr != v.Craft {
			addBtn(kb, c.T("apprentice.craft."+cr, nil), AddrTeachers, cr)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: keyboards.Data(AddrTeachers, v.Craft)}))
	title := htmlBold(htmlEscape(c.T("teachers.title", nil)))
	return c.respond(paragraphs(append([]string{title}, htmlEscapeAll(lines)...)...), kb.Build()).MarkPrivate().AsHTML()
}

// ApprenticeRefusal renders a refused apprenticeship command.
func ApprenticeRefusal(c Context, v ApprenticeRefusalView) *presenter.Response {
	kb := keyboards.New()
	addBtn(kb, c.T("apprentice.button.mine", nil), AddrApprentices)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.withView(c.respond(htmlEscape(c.T("apprentice.refused."+v.Kind, nil)), kb.Build()).MarkPrivate().AsHTML(), ScreenApprenticeRefusal, v)
}
