package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The research lab: the technology tree as a company stands on it, one
// technology — research it, share it, or buy a license for it.

// Where a company stands on a technology.
const (
	TechOwned     = "owned"
	TechLicense   = "licensed"
	TechPublic    = "published"
	TechRunning   = "running"
	TechAvailable = "available"
	TechLocked    = "locked"
)

// TechLine is one technology of the tree.
type TechLine struct {
	Tech Named
	// State is where the company stands on it; Mode how it shares it when
	// it owns it.
	State string
	Mode  string
	Price int64
	Cost  int64
	// Missing are the prerequisites it has not unlocked.
	Missing []Named
	// Offers is how many other companies sell licenses for it, for a
	// company that cannot use it: the license market at a glance.
	Offers int
}

// LabView is a company's research lab.
type LabView struct {
	Ref CompanyRef
	// Available is the money the company may spend.
	Available int64
	// Running is the research running now, if any.
	Running *ResearchLine
	Techs   []TechLine
	// Hidden is how many technologies further away are kept out of sight
	// until the company comes closer (docs/adr/0021, section 14).
	Hidden int
}

// ResearchLine is a research running.
type ResearchLine struct {
	Tech     Named
	FinishAt time.Time
	Left     time.Duration
}

// Lab renders the research lab.
func Lab(c Context, v LabView) *presenter.Response {
	head := body(c.T("production.lab_title", map[string]any{"name": v.Ref.Name}),
		c.T("production.available", map[string]any{"amount": FormatMoney(c, v.Available)}))
	running := ""
	if r := v.Running; r != nil {
		running = c.T("production.lab_running", map[string]any{"tech": c.TechName(r.Tech),
			"time": FormatClock(c, r.FinishAt), "duration": FormatDuration(c, r.Left)})
	}
	var lines []string
	var buttons []presenter.Button
	for _, t := range v.Techs {
		args := map[string]any{"tech": c.TechName(t.Tech), "cost": FormatMoney(c, t.Cost)}
		key := "production.tech_line." + t.State
		if t.State == TechOwned {
			key = "production.tech_line.owned_" + t.Mode
			args["price"] = FormatMoney(c, t.Price)
		}
		if len(t.Missing) > 0 {
			names := make([]string, 0, len(t.Missing))
			for _, m := range t.Missing {
				names = append(names, c.TechName(m))
			}
			args["missing"] = c.list(names)
		}
		line := c.T(key, args)
		if t.Offers > 0 {
			line += c.T("production.tech_line_offers", map[string]any{"count": FormatNumber(c, int64(t.Offers))})
		}
		lines = append(lines, line)
		if btn, ok := keyboards.Button(c.T("production.button.tech", map[string]any{"tech": c.TechName(t.Tech)}),
			AddrLab, v.Ref.Code, t.Tech.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	tree := body(lines...)
	if len(lines) == 0 {
		tree = c.T("production.lab_none", nil)
	}
	later := ""
	if v.Hidden > 0 {
		later = c.T("production.lab_later", map[string]any{"count": FormatNumber(c, int64(v.Hidden))})
	}
	kb := keyboards.New()
	kb.Grid(2, buttons...)
	c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code}, AddrLab, v.Ref.Code)
	return c.respond(paragraphs(head, running, tree, later, c.T("production.lab_hint", nil)), kb.Build())
}

// TechOffer is another company that owns a technology, as a would-be
// licensee sees it.
type TechOffer struct {
	Company CompanyRef
	// Price is its license price; zero when it does not license it.
	Price int64
}

// TechRequirement is a prerequisite of a technology and whether the company
// has unlocked it.
type TechRequirement struct {
	Tech Named
	Met  bool
}

// TechNotice is what just happened on the technology screen.
type TechNotice struct {
	// Kind is started, private, license, published or bought.
	Kind    string
	Price   int64
	Company CompanyRef
}

// Tech notice kinds.
const (
	TechNoticeStarted   = "started"
	TechNoticePrivate   = "private"
	TechNoticeLicense   = "license"
	TechNoticePublished = "published"
	TechNoticeBought    = "bought"
)

// TechView is one technology as a company stands on it.
type TechView struct {
	Ref   CompanyRef
	Tech  Named
	State string
	Cost  int64
	// Time is the research's wait on the wall clock.
	Time     time.Duration
	Requires []TechRequirement
	// Skill and Level are what research needs; Best what the company's
	// best member has.
	Skill string
	Level int
	Best  int
	// Unlocks are the components it lets the company make and design with.
	Unlocks []Named
	// Mode and Price: how an owner shares it; Sold the licenses it sold.
	Mode  string
	Price int64
	Sold  int
	// Blocked says why research cannot start: busy, wrong_type, skill,
	// prerequisite, funds; empty when it can.
	Blocked   string
	Available int64
	Running   *ResearchLine
	// Offers are other companies that own it, for a company that lacks it.
	Offers []TechOffer
	// Confirm asks before a publication or a license purchase.
	ConfirmPublish bool
	ConfirmLicense *TechOffer
	Notice         *TechNotice
	// Gap, for research blocked on a skill, is how to close it: recruit a
	// specialist, or train (docs/adr/0027).
	Gap *SkillGap
}

// Tech renders one technology: research it, share it, or license it.
func Tech(c Context, v TechView) *presenter.Response {
	name := c.TechName(v.Tech)
	notice := ""
	if n := v.Notice; n != nil {
		notice = c.T("production.tech_notice."+n.Kind, map[string]any{"tech": name, "price": FormatMoney(c, n.Price),
			"company": n.Company.Name})
	}
	head := body(c.T("production.tech_title", map[string]any{"tech": name}), c.T("production.tech_state."+v.State, nil))
	facts := []string{c.T("production.tech_cost", map[string]any{"cost": FormatMoney(c, v.Cost), "duration": FormatDuration(c, v.Time)})}
	if v.Skill != "" {
		facts = append(facts, c.T("production.tech_skill", map[string]any{"skill": c.SkillName(v.Skill),
			"level": FormatNumber(c, int64(v.Level)), "best": FormatNumber(c, int64(v.Best))}))
	}
	for _, r := range v.Requires {
		key := "production.tech_requires_missing"
		if r.Met {
			key = "production.tech_requires_met"
		}
		facts = append(facts, c.T(key, map[string]any{"tech": c.TechName(r.Tech)}))
	}
	if len(v.Unlocks) > 0 {
		names := make([]string, 0, len(v.Unlocks))
		for _, u := range v.Unlocks {
			names = append(names, c.ComponentName(u))
		}
		facts = append(facts, c.T("production.tech_unlocks", map[string]any{"components": c.list(names)}))
	}
	var share string
	if v.State == TechOwned {
		share = body(c.T("production.tech_mode."+v.Mode, map[string]any{"price": FormatMoney(c, v.Price)}),
			c.T("production.tech_sold", map[string]any{"count": FormatNumber(c, int64(v.Sold))}))
	}
	running := ""
	if r := v.Running; r != nil && v.State == TechRunning {
		running = c.T("production.lab_running", map[string]any{"tech": name,
			"time": FormatClock(c, r.FinishAt), "duration": FormatDuration(c, r.Left)})
	}
	blocked := ""
	if (v.Blocked != "" && v.State == TechAvailable) || v.State == TechLocked {
		kind := v.Blocked
		if kind == "" {
			kind = ProductionRefusedPrerequisite
		}
		blocked = c.T("production.tech_blocked."+kind, map[string]any{"skill": c.SkillName(v.Skill),
			"level": FormatNumber(c, int64(v.Level)), "need": FormatMoney(c, v.Cost), "money": FormatMoney(c, v.Available)})
	}
	kb := keyboards.New()
	if v.Blocked == ProductionRefusedSkill && v.State == TechAvailable {
		blocked = body(blocked, c.skillGap(kb, v.Gap))
	}
	var offers string
	switch {
	case v.ConfirmPublish:
		offers = c.T("production.tech_publish_confirm", map[string]any{"tech": name})
		kb.Add(c.T("production.button.publish_confirm", nil), AddrTechMode, v.Ref.Code, v.Tech.Code, TechPublished, "0", ProductionConfirm)
	case v.ConfirmLicense != nil:
		o := v.ConfirmLicense
		offers = c.T("production.tech_license_confirm", map[string]any{"tech": name, "company": o.Company.Name,
			"price": FormatMoney(c, o.Price), "money": FormatMoney(c, v.Available)})
		kb.Add(c.T("production.button.license_confirm", map[string]any{"price": FormatMoney(c, o.Price)}),
			AddrLicense, v.Ref.Code, v.Tech.Code, o.Company.Code, ProductionConfirm)
	case v.State == TechOwned:
		var row []presenter.Button
		if v.Mode != TechPublished {
			if v.Mode != TechPrivate {
				if btn, ok := keyboards.Button(c.T("production.button.mode_private", nil), AddrTechMode, v.Ref.Code, v.Tech.Code, TechPrivate); ok {
					row = append(row, btn)
				}
			}
			if btn, ok := askButton(c.T("production.button.mode_license", nil), commandTechMode, v.Ref.Code, v.Tech.Code, TechLicensed); ok {
				row = append(row, btn)
			}
			if btn, ok := keyboards.Button(c.T("production.button.mode_publish", nil), AddrTechMode, v.Ref.Code, v.Tech.Code, TechPublished); ok {
				row = append(row, btn)
			}
		}
		kb.Grid(2, row...)
	case v.State == TechAvailable && v.Blocked == "":
		kb.Add(c.T("production.button.research", map[string]any{"cost": FormatMoney(c, v.Cost)}), AddrResearch, v.Ref.Code, v.Tech.Code)
	}
	if v.State != TechOwned && v.State != TechLicense && v.State != TechPublic && !v.ConfirmPublish && v.ConfirmLicense == nil {
		var lines []string
		var buy []presenter.Button
		for _, o := range v.Offers {
			if o.Price <= 0 {
				continue
			}
			lines = append(lines, c.T("production.tech_offer", map[string]any{"company": o.Company.Name,
				"price": FormatMoney(c, o.Price)}))
			if btn, ok := keyboards.Button(c.T("production.button.license", map[string]any{"company": o.Company.Name}),
				AddrLicense, v.Ref.Code, v.Tech.Code, o.Company.Code); ok {
				buy = append(buy, btn)
			}
		}
		if len(lines) > 0 {
			offers = body(append([]string{c.T("production.tech_offers", nil)}, lines...)...)
		}
		kb.Grid(1, buy...)
	}
	c.productionNav(kb, []string{AddrLab, v.Ref.Code}, AddrLab, v.Ref.Code, v.Tech.Code)
	return c.respond(paragraphs(notice, head, body(facts...), share, running, blocked, offers), kb.Build())
}
