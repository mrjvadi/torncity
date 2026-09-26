package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// SpecialistLine is one specialist of a company.
type SpecialistLine struct {
	No       int64
	NameSeed int
	Skill    string
	Level    int
	Home     Named
	// Salary and Housing are paid every period.
	Salary, Housing int64
	Served, Term    int
	// Expiring is a completed contract waiting to be renewed.
	Expiring bool
	// Underpaid is paid below the market now; UnderpaidLeft and UnpaidLeft
	// are the periods before they leave for it (0 when not so).
	Underpaid                 bool
	UnderpaidLeft, UnpaidLeft int
	// MarketDue is what matching the market would pay per period.
	MarketDue int64
	Shares    int64
}

// SpecialistsView is a company's specialists.
type SpecialistsView struct {
	Ref   CompanyRef
	Lines []SpecialistLine
	Max   int
	// Confirm is the specialist and the act ("dismiss") asked about.
	Confirm    *SpecialistLine
	ConfirmAct string
	// Notice is a notice kind (recruit.staff_notice.<kind>) about
	// NoticeSeed, "" for none.
	Notice     string
	NoticeSeed int
}

func (c Context) specialistLine(l SpecialistLine) string {
	args := map[string]any{"name": c.SpecialistName(l.NameSeed), "what": c.skillLevel(l.Skill, l.Level),
		"city": c.CityName(l.Home.Code, l.Home.Name), "salary": FormatMoney(c, l.Salary),
		"housing": FormatMoney(c, l.Housing), "served": FormatNumber(c, int64(l.Served)),
		"term": FormatNumber(c, int64(l.Term)), "market": FormatMoney(c, l.MarketDue),
		"left": FormatNumber(c, int64(l.UnderpaidLeft)), "unpaid": FormatNumber(c, int64(l.UnpaidLeft)),
		"shares": FormatNumber(c, l.Shares)}
	lines := []string{c.T("recruit.staff.line", args)}
	if l.Housing > 0 {
		lines = append(lines, c.T("recruit.staff.pay_housing", args))
	} else {
		lines = append(lines, c.T("recruit.staff.pay", args))
	}
	if l.Expiring {
		lines = append(lines, c.T("recruit.staff.expiring", args))
	} else {
		lines = append(lines, c.T("recruit.staff.contract", args))
	}
	if l.Shares > 0 {
		lines = append(lines, c.T("recruit.staff.shares", args))
	}
	if l.Underpaid {
		lines = append(lines, c.T("recruit.staff.underpaid", args))
	}
	if l.UnpaidLeft > 0 {
		lines = append(lines, c.T("recruit.staff.unpaid", args))
	}
	return body(lines...)
}

// Specialists renders a company's specialists, each with the owner's
// choices: renew a completed contract, match the market, part ways.
func Specialists(c Context, v SpecialistsView) *presenter.Response {
	return c.withView(renderSpecialists(c, v), ScreenSpecialists, v)
}

func renderSpecialists(c Context, v SpecialistsView) *presenter.Response {
	notice := ""
	if v.Notice != "" {
		notice = c.T("recruit.staff_notice."+v.Notice, map[string]any{"name": c.SpecialistName(v.NoticeSeed)})
	}
	head := c.T("recruit.staff_title", map[string]any{"name": v.Ref.Name, "count": FormatNumber(c, int64(len(v.Lines))),
		"max": FormatNumber(c, int64(v.Max))})
	kb := keyboards.New()
	var blocks []string
	if v.Confirm != nil {
		blocks = append(blocks, c.specialistLine(*v.Confirm),
			c.T("recruit.staff.confirm_"+v.ConfirmAct, map[string]any{"name": c.SpecialistName(v.Confirm.NameSeed)}))
		kb.Add(c.T("recruit.button.dismiss_confirm", nil), AddrSpecialist, strconv.FormatInt(v.Confirm.No, 10),
			SpecialistDismiss, RecruitConfirm)
	} else {
		for _, l := range v.Lines {
			blocks = append(blocks, c.specialistLine(l))
			no := strconv.FormatInt(l.No, 10)
			name := c.SpecialistName(l.NameSeed)
			var row []presenter.Button
			if l.Expiring {
				b, _ := keyboards.Button(c.T("recruit.button.renew", map[string]any{"name": name}), AddrSpecialist, no, SpecialistRenew)
				row = append(row, b)
			} else if l.Underpaid || l.MarketDue > l.Salary+l.Housing {
				b, _ := keyboards.Button(c.T("recruit.button.raise", map[string]any{"name": name}), AddrSpecialist, no, SpecialistRaise)
				row = append(row, b)
			}
			b, _ := keyboards.Button(c.T("recruit.button.dismiss", map[string]any{"name": name}), AddrSpecialist, no, SpecialistDismiss)
			kb.Row(append(row, b)...)
		}
		if len(v.Lines) == 0 {
			blocks = append(blocks, c.T("recruit.staff_none", nil))
		}
		blocks = append(blocks, c.T("recruit.staff_hint", nil))
	}
	kb.Add(c.T("recruit.button.hub", nil), AddrRecruit, v.Ref.Code)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompanyManage, v.Ref.Code),
		RefreshData: keyboards.Data(AddrSpecialists, v.Ref.Code)}))
	return c.respond(paragraphs(append([]string{notice, head}, blocks...)...), kb.Build()).MarkPrivate()
}

// Recruitment refusal kinds.
const (
	RecruitRefusedNotFound  = "not_found"
	RecruitRefusedFunds     = "funds"
	RecruitRefusedCampaigns = "campaigns"
	RecruitRefusedStaff     = "staff"
	RecruitRefusedNoCities  = "no_cities"
	RecruitRefusedNoSkill   = "no_skill"
	RecruitRefusedFinished  = "finished"
	RecruitRefusedGone      = "gone"
	RecruitRefusedPosted    = "posted"
	RecruitRefusedAmount    = "amount"
	RecruitRefusedFilled    = "filled"
	RecruitRefusedNotNow    = "not_now"
)

// RecruitRefusalView is a refused recruitment command.
type RecruitRefusalView struct {
	Kind string
	Ref  CompanyRef
	// Back is where the back button leads.
	Back []string
	// Need and Have for money; Max for a bound.
	Need, Have int64
	Max        int
	NameSeed   int
}

// RecruitRefusal renders a refused recruitment command.
func RecruitRefusal(c Context, v RecruitRefusalView) *presenter.Response {
	return c.withView(renderRecruitRefusal(c, v), ScreenRecruitRefusal, v)
}

func renderRecruitRefusal(c Context, v RecruitRefusalView) *presenter.Response {
	text := c.T("recruit.refused."+v.Kind, map[string]any{"name": v.Ref.Name, "need": FormatMoney(c, v.Need),
		"money": FormatMoney(c, v.Have), "max": FormatNumber(c, int64(v.Max)), "person": c.SpecialistName(v.NameSeed)})
	back := v.Back
	if len(back) == 0 {
		back = []string{AddrCompanyMine}
		if v.Ref.Code != "" {
			back = []string{AddrRecruit, v.Ref.Code}
		}
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(back...)}))
	return c.respond(text, kb.Build()).MarkPrivate()
}

// Recruitment notice kinds.
const (
	RecruitNoticeApplied   = "applied"
	RecruitNoticeHired     = "hired"
	RecruitNoticeEnded     = "ended"
	RecruitNoticeFilled    = "filled"
	RecruitNoticeCompleted = "completed"
	RecruitNoticeLeft      = "left"
	RecruitNoticeUnpaid    = "unpaid"
)

// RecruitNoticeView is a private notice to a company's owner about a
// campaign or a specialist.
type RecruitNoticeView struct {
	Kind       string
	Company    CompanyRef
	CampaignNo int64
	// Count is how many candidates applied.
	Count    int
	NameSeed int
	Skill    string
	Level    int
	// Reason is why a specialist left (recruit.leave.<reason>).
	Reason string
	// Amount is the equity paid at a completed contract.
	Amount int64
}

// RecruitNotice renders a recruitment notice.
func RecruitNotice(c Context, v RecruitNoticeView) *presenter.Response {
	return c.withView(renderRecruitNotice(c, v), ScreenRecruitNotice, v)
}

func renderRecruitNotice(c Context, v RecruitNoticeView) *presenter.Response {
	args := map[string]any{"company": v.Company.Name, "no": FormatNumber(c, v.CampaignNo),
		"count": FormatNumber(c, int64(v.Count)), "name": c.SpecialistName(v.NameSeed),
		"what": c.skillLevel(v.Skill, v.Level), "amount": FormatMoney(c, v.Amount)}
	if v.Reason != "" {
		args["reason"] = c.T("recruit.leave."+v.Reason, nil)
	}
	kb := keyboards.New()
	no := strconv.FormatInt(v.CampaignNo, 10)
	switch v.Kind {
	case RecruitNoticeApplied, RecruitNoticeEnded, RecruitNoticeFilled:
		kb.Add(c.T("recruit.button.campaign_open", nil), AddrRecruitCamp, no)
	default:
		kb.Add(c.T("recruit.button.staff", nil), AddrSpecialists, v.Company.Code)
	}
	text := c.T("recruit.notice."+v.Kind, args)
	if v.Kind == RecruitNoticeCompleted && v.Amount > 0 {
		text = body(text, c.T("recruit.notice.equity", args))
	}
	return c.respond(text, kb.Build()).MarkPrivate()
}

// RecruitAdAnnouncement is the public job advertisement in a city's groups:
// who is hiring and what skill — no amount.
func RecruitAdAnnouncement(c Context, ref CompanyRef, skill string, level int, cityCode, city string) string {
	return c.T("recruit.announce", map[string]any{"company": ref.Name, "type": c.CompanyTypeName(ref.Type),
		"what": c.skillLevel(skill, level), "city": c.CityName(cityCode, city)})
}
