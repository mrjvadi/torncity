package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Candidate statuses, as the candidate lines carry them.
const (
	CandidatePending   = "pending"
	CandidateHired     = "hired"
	CandidateRejected  = "rejected"
	CandidateExpired   = "expired"
	CandidateWithdrawn = "withdrawn"
)

// RecruitOffer is a campaign's package.
type RecruitOffer struct {
	Salary, Housing, Signing, Relocation int64
	Term                                 int
	Shares                               int64
}

// RecruitCandidateLine is one candidate of a campaign.
type RecruitCandidateLine struct {
	No       int64
	NameSeed int
	Skill    string
	Level    int
	Home     Named
	// Abroad is a candidate from another country.
	Abroad bool
	// Expected is what they expect per period; Cost what hiring them costs
	// now (signing bonus and move).
	Expected, Cost int64
	Status         string
	ExpiresAt      time.Time
}

// RecruitCampaignView is a campaign as its company sees it.
type RecruitCampaignView struct {
	Ref  CompanyRef
	Line RecruitCampaignLine
	// ChecksLeft are the checks still to run.
	ChecksLeft int
	Offer      RecruitOffer
	Cities     []Named
	AdFee      int64
	Auto       bool
	Candidates []RecruitCandidateLine
	// Available is the company's free money.
	Available     int64
	ConfirmCancel bool
	// Notice is a notice kind (recruit.campaign_notice.<kind>) and its
	// arguments' subject, "" for none.
	Notice     string
	NoticeSeed int
}

func (c Context) offerLines(o RecruitOffer) string {
	lines := []string{c.T("recruit.offer.salary", map[string]any{"amount": FormatMoney(c, o.Salary)})}
	if o.Housing > 0 {
		lines = append(lines, c.T("recruit.offer.housing", map[string]any{"amount": FormatMoney(c, o.Housing)}))
	}
	if o.Signing > 0 {
		lines = append(lines, c.T("recruit.offer.signing", map[string]any{"amount": FormatMoney(c, o.Signing)}))
	}
	if o.Relocation > 0 {
		lines = append(lines, c.T("recruit.offer.relocation", map[string]any{"amount": FormatMoney(c, o.Relocation)}))
	}
	lines = append(lines, c.T("recruit.offer.term", map[string]any{"count": FormatNumber(c, int64(o.Term))}))
	if o.Shares > 0 {
		lines = append(lines, c.T("recruit.offer.shares", map[string]any{"count": FormatNumber(c, o.Shares)}))
	}
	return body(lines...)
}

func (c Context) candidateLine(l RecruitCandidateLine) string {
	home := c.CityName(l.Home.Code, l.Home.Name)
	if l.Abroad {
		home = c.T("recruit.city_abroad", map[string]any{"city": home})
	}
	return c.T("recruit.candidate."+l.Status, map[string]any{"name": c.SpecialistName(l.NameSeed),
		"what": c.skillLevel(l.Skill, l.Level), "city": home, "expected": FormatMoney(c, l.Expected),
		"time": FormatClock(c, l.ExpiresAt)})
}

// RecruitCampaign renders a campaign: where it runs, its offer and its
// candidates, each pending one with a button to hire and one to turn down.
func RecruitCampaign(c Context, v RecruitCampaignView) *presenter.Response {
	return c.withView(renderRecruitCampaign(c, v), ScreenRecruitCampaign, v)
}

func renderRecruitCampaign(c Context, v RecruitCampaignView) *presenter.Response {
	no := strconv.FormatInt(v.Line.No, 10)
	notice := ""
	if v.Notice != "" {
		notice = c.T("recruit.campaign_notice."+v.Notice, map[string]any{"name": c.SpecialistName(v.NoticeSeed)})
	}
	args := map[string]any{"no": FormatNumber(c, v.Line.No), "what": c.skillLevel(v.Line.Skill, v.Line.Level),
		"time": FormatClock(c, v.Line.NextAt), "checks": FormatNumber(c, int64(v.ChecksLeft)),
		"hired": FormatNumber(c, int64(v.Line.Hired)), "positions": FormatNumber(c, int64(v.Line.Positions))}
	names := make([]string, 0, len(v.Cities))
	for _, ci := range v.Cities {
		names = append(names, c.CityName(ci.Code, ci.Name))
	}
	args["cities"] = c.list(names)
	head := body(c.T("recruit.campaign_title", args), c.T("recruit.campaign_status."+v.Line.Status, args),
		c.T("recruit.campaign_cities", args), c.T("recruit.campaign_hired", args))
	if v.Auto {
		head = body(head, c.T("recruit.draft.auto_on", nil))
	}
	kb := keyboards.New()
	var lines []string
	for _, l := range v.Candidates {
		lines = append(lines, c.candidateLine(l))
		if l.Status != CandidatePending || v.ConfirmCancel {
			continue
		}
		cand := strconv.FormatInt(l.No, 10)
		hire, _ := keyboards.Button(c.T("recruit.button.hire", map[string]any{"name": c.SpecialistName(l.NameSeed),
			"amount": FormatMoney(c, l.Cost)}), AddrRecruitDecide, cand, RecruitHire)
		reject, _ := keyboards.Button(c.T("recruit.button.reject", nil), AddrRecruitDecide, cand, RecruitReject)
		kb.Row(hire, reject)
	}
	candidates := c.T("recruit.candidates_none."+v.Line.Status, nil)
	if len(lines) > 0 {
		candidates = body(append([]string{c.T("recruit.candidates_title", nil)}, lines...)...)
	}
	extra := ""
	switch {
	case v.ConfirmCancel:
		extra = c.T("recruit.cancel_confirm", nil)
		kb.Add(c.T("recruit.button.cancel_confirm", nil), AddrRecruitCancel, no, RecruitConfirm)
	case v.Line.Status == CampaignRunning:
		kb.Add(c.T("recruit.button.cancel", nil), AddrRecruitCancel, no)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrRecruit, v.Ref.Code),
		RefreshData: keyboards.Data(AddrRecruitCamp, no)}))
	money := c.T("recruit.campaign_money", map[string]any{"money": FormatMoney(c, v.Available)})
	return c.respond(paragraphs(notice, head, c.offerLines(v.Offer), candidates, money, extra), kb.Build()).MarkPrivate()
}
