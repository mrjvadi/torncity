package screens

import (
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Specialist recruitment (docs/adr/0027-specialist-recruitment.md): a
// company's recruitment hub, the campaign builder, a campaign with its
// candidates, the company's specialists, the notices of a campaign and of a
// specialist, and the public job advertisement.
//
// Running recruitment is the owner's or the manager's business, in their
// private chat. A specialist is named from the language's name lists
// (recruit.names.first / recruit.names.last) by the seed they were drawn
// with, so the same person has the same name on every screen.

// Callback addresses of the recruitment screens.
const (
	AddrRecruit       = "company:recruit"
	AddrRecruitNew    = "company:rnew"
	AddrRecruitDraft  = "company:rdraft"
	AddrRecruitSet    = "company:rset"
	AddrRecruitPost   = "company:rpost"
	AddrRecruitCamp   = "company:rcamp"
	AddrRecruitDecide = "company:rdecide"
	AddrRecruitCancel = "company:rcancel"
	AddrSpecialists   = "company:npcs"
	AddrSpecialist    = "company:npc"
	// AddrCourse opens a course of the education screens.
	AddrCourse = "education:view"
)

// commandRecruitAmount asks a typed amount for a field of a draft.
const commandRecruitAmount = "company.ramount"

// RecruitConfirm is the argument that confirms a posting, a cancellation
// and a dismissal.
const RecruitConfirm = "yes"

// The campaign builder's sections, and the fields a press sets.
const (
	RecruitSectionSkill  = "skill"
	RecruitSectionCities = "cities"
	RecruitSectionPay    = "pay"
	RecruitSectionTerms  = "terms"

	RecruitFieldSkill      = "skill"
	RecruitFieldLevel      = "level"
	RecruitFieldCity       = "city"
	RecruitFieldScope      = "scope"
	RecruitFieldSalary     = "salary"
	RecruitFieldHousing    = "housing"
	RecruitFieldSigning    = "signing"
	RecruitFieldRelocation = "relocation"
	RecruitFieldTerm       = "term"
	RecruitFieldShares     = "shares"
	RecruitFieldPositions  = "positions"
	RecruitFieldAuto       = "auto"

	// Scopes of a press on the cities: the company's own city, every city
	// of its country, every city.
	RecruitScopeOwn    = "own"
	RecruitScopeNation = "nation"
	RecruitScopeAll    = "all"

	// Verdicts on a candidate, and what an owner does to a specialist.
	RecruitHire       = "yes"
	RecruitReject     = "no"
	SpecialistRenew   = "renew"
	SpecialistRaise   = "raise"
	SpecialistDismiss = "dismiss"
)

// Campaign statuses, as the campaign lines carry them.
const (
	CampaignDraft     = "draft"
	CampaignRunning   = "running"
	CampaignFilled    = "filled"
	CampaignEnded     = "ended"
	CampaignCancelled = "cancelled"
)

// SpecialistName is a specialist's name in c's language, from their seed.
func (c Context) SpecialistName(seed int) string {
	first := strings.Split(c.T("recruit.names.first", nil), "|")
	last := strings.Split(c.T("recruit.names.last", nil), "|")
	seed = max(seed, 0)
	return strings.TrimSpace(first[seed%len(first)]) + " " + strings.TrimSpace(last[(seed/len(first))%len(last)])
}

// skillLevel is «engineering level 3».
func (c Context) skillLevel(skill string, level int) string {
	return c.T("recruit.skill_level", map[string]any{"skill": c.SkillName(skill), "level": FormatNumber(c, int64(level))})
}

// SkillGap is a skill a company lacks for something it wants to do: the way
// to hire it, and the courses that train it.
type SkillGap struct {
	// Company is the company's code.
	Company string
	Skill   string
	Level   int
	// Courses train the skill, the most first.
	Courses []CourseRef
}

// skillGap renders the two ways to close a gap, and adds their buttons.
func (c Context) skillGap(kb *keyboards.Builder, g *SkillGap) string {
	if g == nil || g.Skill == "" {
		return ""
	}
	args := map[string]any{"skill": c.SkillName(g.Skill), "level": FormatNumber(c, int64(g.Level))}
	lines := []string{c.T("recruit.gap.hire", args)}
	kb.Add(c.T("recruit.button.gap", args), AddrRecruitNew, g.Company, g.Skill, strconv.Itoa(g.Level))
	if len(g.Courses) > 0 {
		names := make([]string, 0, len(g.Courses))
		var row []presenter.Button
		for _, co := range g.Courses {
			names = append(names, c.T("recruit.gap.course", map[string]any{"course": c.course(co)}))
			if btn, ok := keyboards.Button(c.T("recruit.button.course", map[string]any{"course": c.course(co)}),
				AddrCourse, co.Code); ok {
				row = append(row, btn)
			}
		}
		args["courses"] = c.list(names)
		lines = append(lines, c.T("recruit.gap.train", args))
		kb.Grid(2, row...)
	} else {
		lines = append(lines, c.T("recruit.gap.no_course", args))
	}
	return body(lines...)
}

// RecruitCampaignLine is one campaign of a company's hub.
type RecruitCampaignLine struct {
	No     int64
	Status string
	Skill  string
	Level  int
	// Cities is how many cities it advertises in.
	Cities           int
	Positions, Hired int
	// Pending are the candidates waiting for an answer.
	Pending int
	NextAt  time.Time
}

// RecruitHubView is a company's recruitment: its specialists, its
// campaigns, and the way to start one.
type RecruitHubView struct {
	Ref                  CompanyRef
	Staff, MaxStaff      int
	Running, MaxCampaign int
	Campaigns            []RecruitCampaignLine
}

// campaignLine renders one campaign.
func (c Context) campaignLine(l RecruitCampaignLine) string {
	args := map[string]any{"no": FormatNumber(c, l.No), "what": c.skillLevel(l.Skill, l.Level),
		"cities": FormatNumber(c, int64(l.Cities)), "hired": FormatNumber(c, int64(l.Hired)),
		"positions": FormatNumber(c, int64(l.Positions)), "pending": FormatNumber(c, int64(l.Pending)),
		"time": FormatClock(c, l.NextAt)}
	line := c.T("recruit.campaign_line."+l.Status, args)
	if l.Pending > 0 {
		line += c.T("recruit.campaign_pending", args)
	}
	return line
}

// RecruitHub renders a company's recruitment hub.
func RecruitHub(c Context, v RecruitHubView) *presenter.Response {
	head := body(c.T("recruit.hub_title", map[string]any{"name": v.Ref.Name}),
		c.T("recruit.hub_staff", map[string]any{"staff": FormatNumber(c, int64(v.Staff)), "max": FormatNumber(c, int64(v.MaxStaff))}))
	kb := keyboards.New()
	var lines []string
	for _, l := range v.Campaigns {
		lines = append(lines, c.campaignLine(l))
		addr := AddrRecruitCamp
		if l.Status == CampaignDraft {
			addr = AddrRecruitDraft
		}
		kb.Add(c.T("recruit.button.campaign", map[string]any{"no": FormatNumber(c, l.No),
			"what": c.skillLevel(l.Skill, l.Level)}), addr, strconv.FormatInt(l.No, 10))
	}
	campaigns := c.T("recruit.hub_none", nil)
	if len(lines) > 0 {
		campaigns = body(append([]string{c.T("recruit.hub_campaigns", nil)}, lines...)...)
	}
	newBtn, _ := keyboards.Button(c.T("recruit.button.new", nil), AddrRecruitNew, v.Ref.Code)
	staffBtn, _ := keyboards.Button(c.T("recruit.button.staff", nil), AddrSpecialists, v.Ref.Code)
	kb.Row(newBtn, staffBtn)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompanyManage, v.Ref.Code),
		RefreshData: keyboards.Data(AddrRecruit, v.Ref.Code)}))
	return c.respond(paragraphs(head, campaigns, c.T("recruit.hub_hint", map[string]any{
		"max": FormatNumber(c, int64(v.MaxCampaign)), "running": FormatNumber(c, int64(v.Running))})), kb.Build()).MarkPrivate()
}
