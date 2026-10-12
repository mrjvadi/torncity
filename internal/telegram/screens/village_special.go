package screens

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Special projects, intelligence between settlements and the licence market (docs/adr/0077), drawn for Telegram. The long forms (a pledge's
// amount, a bid, a requirement's terms) are written in the Mini App; here the group sees the state and presses the short acts.

// isSpecialRefusal reports a refusal kind of the project, intelligence and licence screens.
func isSpecialRefusal(kind string) bool {
	for _, p := range []string{"project_", "intel_", "licence_"} {
		if strings.HasPrefix(kind, p) {
			return true
		}
	}
	return false
}

const (
	ScreenProject     = village.ScreenProject
	AddrProject       = village.AddrProject
	ScreenIntel       = village.ScreenIntel
	AddrIntel         = village.AddrIntel
	ScreenLicence     = village.ScreenLicence
	AddrLicenceMarket = village.AddrLicence
)

func specialNotice(c Context, notice string, blocks []string) []string {
	if notice != "" {
		return append(blocks, c.T("special.notice."+notice, nil))
	}
	return blocks
}

// Project renders the project screen.
func Project(c Context, v village.ProjectView) *presenter.Response {
	return c.withView(renderProject(c, v), ScreenProject, v)
}

func renderProject(c Context, v village.ProjectView) *presenter.Response {
	kb := keyboards.New()
	blocks := specialNotice(c, v.Notice, []string{c.T("special.project.title", map[string]any{"name": v.Village})})
	if !v.Open {
		blocks = append(blocks, c.T("special.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrProject}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	if len(v.Projects) == 0 {
		blocks = append(blocks, c.T("special.project.none", nil))
	}
	for _, p := range v.Projects {
		head := c.T("special.project.line", map[string]any{"name": p.Kind.Name, "stage": strconv.Itoa(p.Stage), "stages": strconv.Itoa(p.Stages),
			"status": c.T("special.project.status."+p.Status, nil), "fund": FormatMoney(c, p.Fund)})
		var ls []string
		if p.Status == "active" {
			ls = append(ls, c.T("special.project.waits."+waitKey(p.Waiting), nil))
			ls = append(ls, c.T("special.project.hours", map[string]any{"have": FormatNumber(c, p.Hours), "need": FormatNumber(c, p.NeedHour)}))
			for _, m := range p.Materials {
				ls = append(ls, c.T("special.project.material", map[string]any{"item": m.Item, "have": FormatNumber(c, m.Have), "need": FormatNumber(c, m.Need)}))
			}
		}
		if p.Status == "finished" {
			key := "special.project.kept"
			if !p.Held {
				key = "special.project.idle"
			}
			ls = append(ls, c.T(key, nil))
		}
		blocks = append(blocks, head+"\n"+body(ls...))
		if p.MayWork {
			addBtn(kb, c.T("special.button.work", map[string]any{"name": p.Kind.Name}), AddrProject, village.ProjectActionWork, p.ID)
		}
		if p.MayCancel {
			addBtn(kb, c.T("special.button.cancel", map[string]any{"name": p.Kind.Name}), AddrProject, village.ProjectActionCancel, p.ID)
		}
	}
	if v.MayStart {
		for _, k := range v.Kinds {
			if k.Available {
				addBtn(kb, c.T("special.button.start", map[string]any{"name": k.Name}), AddrProject, village.ProjectActionStart, k.Code)
			}
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrProject}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

func waitKey(w string) string {
	if w == "" {
		return "ready"
	}
	return w
}

// Intel renders the intelligence screen.
func Intel(c Context, v village.IntelView) *presenter.Response {
	return c.withView(renderIntel(c, v), ScreenIntel, v)
}

func renderIntel(c Context, v village.IntelView) *presenter.Response {
	kb := keyboards.New()
	blocks := specialNotice(c, v.Notice, []string{c.T("special.intel.title", map[string]any{"name": v.Village})})
	if !v.Open {
		blocks = append(blocks, c.T("special.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrIntel}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	state := "special.intel.no_bureau"
	switch {
	case v.BureauOpen:
		state = "special.intel.bureau_open"
	case v.BureauBuilt:
		state = "special.intel.bureau_closed"
	}
	blocks = append(blocks, c.T(state, nil), c.T("special.intel.counter."+v.Counter, nil))
	for _, a := range v.Offers {
		blocks = append(blocks, c.T("special.intel.offer", map[string]any{"sponsor": a.Sponsor.Name, "target": a.Target.Name}))
		addBtn(kb, c.T("special.button.accept", nil), AddrIntel, village.IntelActionAccept, a.ID)
		addBtn(kb, c.T("special.button.refuse", nil), AddrIntel, village.IntelActionRefuse, a.ID)
	}
	for _, a := range v.Agents {
		blocks = append(blocks, c.T("special.intel.agent", map[string]any{"player": a.Player.Name, "target": a.Target.Name, "status": c.T("special.intel.status."+a.Status, nil), "days": strconv.FormatInt(a.CoverDays, 10)}))
		if a.MayOperate {
			for _, op := range v.Ops {
				if op.Available && (op.Kind == "survey" || v.SabotageOpen) && op.Kind == "survey" {
					addBtn(kb, c.T("special.button.op", map[string]any{"name": op.Name}), AddrIntel, village.IntelActionOp, a.ID, op.Code)
				}
			}
		}
		if a.MayRecall {
			addBtn(kb, c.T("special.button.recall", nil), AddrIntel, village.IntelActionRecall, a.ID)
		}
	}
	for _, r := range v.Reports {
		var fs []string
		for _, f := range r.Fields {
			fs = append(fs, c.T("special.intel.field", map[string]any{"metric": c.T("special.intel.metric."+f.Metric, nil), "band": c.T("special.intel.band."+f.Band, nil)}))
		}
		old := ""
		if r.Old {
			old = c.T("special.intel.old", nil)
		}
		blocks = append(blocks, c.T("special.intel.report", map[string]any{"target": r.Target.Name, "confidence": c.T("special.intel.confidence."+r.Confidence, nil), "at": FormatDate(c, r.TakenAt), "old": old})+"\n"+body(fs...))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrIntel}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// Licence renders the licence screen.
func Licence(c Context, v village.LicenceView) *presenter.Response {
	return c.withView(renderLicence(c, v), ScreenLicence, v)
}

func renderLicence(c Context, v village.LicenceView) *presenter.Response {
	kb := keyboards.New()
	blocks := specialNotice(c, v.Notice, []string{c.T("special.licence.title", map[string]any{"name": v.Village})})
	if !v.Open {
		blocks = append(blocks, c.T("special.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrLicenceMarket}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	var ks []string
	for _, k := range v.Kinds {
		if !k.Required {
			continue
		}
		ks = append(ks, c.T("special.licence.kind", map[string]any{"name": k.Name, "mode": c.T("special.licence.mode."+k.Mode, nil), "fee": FormatMoney(c, max(k.Fee, k.Reserve)), "days": strconv.Itoa(k.TermDays)}))
	}
	if len(ks) > 0 {
		blocks = append(blocks, c.T("special.licence.required", nil)+"\n"+body(ks...))
	}
	var ms []string
	for _, l := range v.Mine {
		ms = append(ms, c.T("special.licence.mine", map[string]any{"no": strconv.FormatInt(l.No, 10), "kind": l.Kind, "until": FormatDate(c, l.TermEnd)}))
		if l.MayRenew {
			addBtn(kb, c.T("special.button.renew", map[string]any{"no": strconv.FormatInt(l.No, 10)}), AddrLicenceMarket, village.LicenceActionRenew, l.ID)
		}
	}
	if len(ms) > 0 {
		blocks = append(blocks, c.T("special.licence.mine_head", nil)+"\n"+body(ms...))
	}
	for _, a := range v.Applications {
		blocks = append(blocks, c.T("special.licence.application", map[string]any{"kind": a.Kind, "holder": a.Holder.Name, "fee": FormatMoney(c, a.Fee)}))
		if a.MayDecide {
			addBtn(kb, c.T("special.button.issue", nil), AddrLicenceMarket, village.LicenceActionIssue, a.ID)
			addBtn(kb, c.T("special.button.decline", nil), AddrLicenceMarket, village.LicenceActionDecline, a.ID)
		}
	}
	for _, a := range v.Auctions {
		if a.Status == "open" {
			blocks = append(blocks, c.T("special.licence.auction", map[string]any{"kind": a.Kind, "reserve": FormatMoney(c, a.Reserve), "closes": FormatDate(c, a.ClosesAt)}))
		}
	}
	for _, b := range v.Board {
		blocks = append(blocks, c.T("special.licence.listing", map[string]any{"no": strconv.FormatInt(b.Licence.No, 10), "kind": b.Licence.Kind, "price": FormatMoney(c, b.Price)}))
		if b.MayBuy {
			addBtn(kb, c.T("special.button.buy", map[string]any{"price": FormatMoney(c, b.Price)}), AddrLicenceMarket, village.LicenceActionBuy, b.ID)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrLicenceMarket}))
	return c.respond(paragraphs(blocks...), kb.Build())
}
