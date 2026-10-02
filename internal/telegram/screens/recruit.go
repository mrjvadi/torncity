package screens

import (
	"strconv"
	"strings"

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

// commandRecruitAmount asks a typed amount for a field of a draft.
const commandRecruitAmount = "company.ramount"

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
	return c.withView(renderRecruitHub(c, v), ScreenRecruitHub, v)
}

func renderRecruitHub(c Context, v RecruitHubView) *presenter.Response {
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
