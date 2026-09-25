package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Defence licences (docs/adr/0022-military-and-diplomacy.md section 2.14):
// a company's licence and its application for a contractor licence, the
// public registry of a country's licences with the defence minister's
// decisions, and what the owner, the minister and the city groups are told.

// Callback addresses of the defence licences.
const (
	AddrCompanyDefence = "company:defence"
	AddrLicences       = "military:licences"
	AddrLicence        = "military:licence"
)

// The minister's verdicts on a licence.
const (
	LicenceApprove = "approve"
	LicenceReject  = "reject"
	LicenceRevoke  = "revoke"
)

// CompanyRefusedDefence refuses founding a company of the defence sector
// without a defence licence.
const CompanyRefusedDefence = "defence"

// LicenceEntry is one licence or application as the screens show it.
type LicenceEntry struct {
	No      int64
	Company CompanyRef
	// Kind is manufacturer or contractor; Basis what it rests on; Status
	// where it stands (as settled now: a revocation past its notice is
	// revoked).
	Kind   string
	Basis  string
	Status string
	// EffectiveAt is when a revocation takes effect.
	EffectiveAt time.Time
}

// licenceStatusLine is a licence's status in one line.
func (c Context) licenceStatusLine(e LicenceEntry) string {
	return c.T("defence.status."+e.Status, map[string]any{"time": FormatClock(c, e.EffectiveAt)})
}

// CompanyDefenceView is a company's defence licence screen.
type CompanyDefenceView struct {
	Ref CompanyRef
	// Licence is its latest licence or application, nil for none.
	Licence *LicenceEntry
	// Manufacturer is a company of the defence sector; otherwise it is a
	// civilian company that may become a contractor.
	Manufacturer bool
	// Owned and Tier are its standing in technology: technologies of its
	// own, and the deepest tier among them; MinTechs and MinTier what an
	// application asks.
	Owned, Tier       int
	MinTechs, MinTier int
	// CanApply is an owner whose company may apply now.
	CanApply bool
	// Applied is set just after the application went in.
	Applied bool
	// NoMinister says nobody holds or acts for the defence minister's
	// seat: an application waits.
	NoMinister bool
}

// CompanyDefence renders a company's defence licence screen.
func CompanyDefence(c Context, v CompanyDefenceView) *presenter.Response {
	head := c.T("defence.company_title", map[string]any{"name": v.Ref.Name})
	status := c.T("defence.company_none", nil)
	if v.Licence != nil {
		status = c.licenceStatusLine(*v.Licence)
	}
	kb := keyboards.New()
	var explain, standing, how string
	if v.Manufacturer {
		explain = c.T("defence.company_manufacturer", nil)
	} else {
		explain = c.T("defence.company_contractor", nil)
		mark := func(ok bool) string {
			if ok {
				return "✅"
			}
			return "❌"
		}
		standing = body(c.T("defence.standing_title", nil),
			c.T("defence.standing_techs", map[string]any{"mark": mark(v.Owned >= v.MinTechs),
				"min": FormatNumber(c, int64(v.MinTechs)), "have": FormatNumber(c, int64(v.Owned))}),
			c.T("defence.standing_tier", map[string]any{"mark": mark(v.Tier >= v.MinTier),
				"min": FormatNumber(c, int64(v.MinTier)), "have": FormatNumber(c, int64(v.Tier))}))
		switch {
		case v.CanApply:
			how = c.T("defence.apply_how", nil)
			kb.Add(c.T("defence.button.apply", nil), AddrCompanyDefence, v.Ref.Code, ProductionConfirm)
		case v.Licence == nil || v.Licence.Status == "rejected" || v.Licence.Status == "revoked":
			how = c.T("defence.apply_not_yet", nil)
			kb.Add(c.T("production.button.lab", nil), AddrLab, v.Ref.Code)
		}
	}
	notice := ""
	if v.Applied {
		notice = c.T("defence.applied", nil)
		if v.NoMinister {
			notice = body(notice, c.T("defence.no_minister", nil))
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompanyManage, v.Ref.Code),
		RefreshData: keyboards.Data(AddrCompanyDefence, v.Ref.Code)}))
	return c.respond(paragraphs(notice, body(head, status), explain, standing, how), kb.Build()).MarkPrivate()
}

// LicencesView is a country's public registry of defence licences.
type LicencesView struct {
	Country GovPlace
	Pending []LicenceEntry
	InForce []LicenceEntry
	Ended   []LicenceEntry
	// CanDecide is the viewer who decides for the defence minister, in
	// their private chat.
	CanDecide bool
	// Notice is the verdict the minister just gave (approve, reject,
	// revoke) on NoticeCompany.
	Notice        string
	NoticeCompany string
	// Confirm is a licence the minister is about to revoke, and Notice
	// the notice it will run.
	Confirm      *LicenceEntry
	RevokeNotice time.Duration
}

// Licences renders the registry.
func Licences(c Context, v LicencesView) *presenter.Response {
	kb := keyboards.New()
	country := c.PlaceName(v.Country)
	back := keyboards.Nav{BackData: keyboards.Data(AddrLicences, v.Country.Code)}
	if e := v.Confirm; e != nil {
		kb.Add(c.T("defence.button.revoke_confirm", nil), AddrLicence, strconv.FormatInt(e.No, 10), LicenceRevoke,
			MilitaryConfirm)
		kb.Nav(c.nav(back))
		return c.respond(c.T("defence.revoke_confirm", map[string]any{"company": e.Company.Name,
			"notice": FormatDuration(c, v.RevokeNotice)}), kb.Build())
	}
	decide := v.CanDecide && !c.Shared
	line := func(e LicenceEntry) string {
		return c.T("defence.registry_line", map[string]any{"company": e.Company.Name,
			"type": c.CompanyTypeName(e.Company.Type), "kind": c.T("defence.kind."+e.Kind, nil),
			"status": c.licenceStatusLine(e)})
	}
	var sections []string
	if len(v.Pending) > 0 {
		lines := []string{c.T("defence.registry_pending", nil)}
		for _, e := range v.Pending {
			lines = append(lines, line(e))
			if decide {
				no := strconv.FormatInt(e.No, 10)
				yes, _ := keyboards.Button(c.T("defence.button.approve", map[string]any{"company": e.Company.Name}),
					AddrLicence, no, LicenceApprove)
				nope, _ := keyboards.Button(c.T("defence.button.reject", nil), AddrLicence, no, LicenceReject)
				kb.Row(yes, nope)
			}
		}
		sections = append(sections, body(lines...))
	}
	if len(v.InForce) > 0 {
		lines := []string{c.T("defence.registry_in_force", nil)}
		var revoke []presenter.Button
		for _, e := range v.InForce {
			lines = append(lines, line(e))
			if decide && e.Status == "active" {
				if btn, ok := keyboards.Button(c.T("defence.button.revoke", map[string]any{"company": e.Company.Name}),
					AddrLicence, strconv.FormatInt(e.No, 10), LicenceRevoke); ok {
					revoke = append(revoke, btn)
				}
			}
		}
		kb.Grid(2, revoke...)
		sections = append(sections, body(lines...))
	}
	if len(v.Ended) > 0 {
		lines := []string{c.T("defence.registry_ended", nil)}
		for _, e := range v.Ended {
			lines = append(lines, line(e))
		}
		sections = append(sections, body(lines...))
	}
	if len(sections) == 0 {
		sections = append(sections, c.T("defence.registry_none", nil))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMinistry, v.Country.Code),
		RefreshData: keyboards.Data(AddrLicences, v.Country.Code)}))
	notice := ""
	if v.Notice != "" {
		notice = c.T("defence.verdict."+v.Notice, map[string]any{"company": v.NoticeCompany})
	}
	parts := append([]string{notice, c.T("defence.registry_title", map[string]any{"country": country})}, sections...)
	parts = append(parts, c.T("defence.registry_hint", nil))
	return c.respond(paragraphs(parts...), kb.Build())
}

// LicenceNoticeView is a private notice about a licence.
type LicenceNoticeView struct {
	// Kind is applied (to the minister), approved, rejected or revoked
	// (to the owner).
	Kind    string
	Company CompanyRef
	Country GovPlace
	// EffectiveAt is when a revocation takes effect.
	EffectiveAt time.Time
}

// LicenceNotice renders a private notice about a licence.
func LicenceNotice(c Context, v LicenceNoticeView) *presenter.Response {
	kb := keyboards.New()
	if v.Kind == "applied" {
		kb.Add(c.T("defence.button.registry", nil), AddrLicences, v.Country.Code)
	} else {
		kb.Add(c.T("defence.button.company", nil), AddrCompanyDefence, v.Company.Code)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("defence.notice."+v.Kind, map[string]any{"company": v.Company.Name,
		"country": c.PlaceName(v.Country), "time": FormatClock(c, v.EffectiveAt)}), kb.Build())
}

// LicenceAnnouncement is a line in the groups of a country's cities: a
// contractor licence granted, or a licence revoked.
func LicenceAnnouncement(c Context, kind, company string, country GovPlace, effective time.Time) string {
	return c.T("defence.announce."+kind, map[string]any{"company": company, "country": c.PlaceName(country),
		"time": FormatClock(c, effective)})
}
