package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Specialist recruitment joins the snapshot harness as its own area —
// testdata/snapshots/<language>/recruit.txt (docs/adr/0027) — and its job
// advertisement joins the group lines (group.txt).
func init() { snapshotAreas["recruit"] = recruitSnapshots }

func recruitSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	n := productionNames[c.Lang]
	studio := Named{Code: "tech_studio", Name: "Technology studio"}
	maker := CompanyRef{Code: "Q7M2K9B", Name: n.maker, Type: studio}
	ostmarch := Named{Code: "ostmarch", Name: "Ostmarch"}
	fenwick := Named{Code: "fenwick_span", Name: "Fenwick Span"}
	calderis := Named{Code: "calderis", Name: "Calderis"}

	// The lab's dead end is a way out now.
	gap := &SkillGap{Company: maker.Code, Skill: "engineering", Level: 3,
		Courses: []CourseRef{{Code: "software_engineering", Name: "Software Engineering"}, {Code: "automotive_repair", Name: "Automotive Repair"}}}
	micro := Named{Code: "microchips", Name: "Microchips"}
	add("Technology · nobody skilled enough, recruit or train", Tech(c, TechView{Ref: maker, Tech: micro, State: TechAvailable,
		Cost: 60000, Time: 12 * time.Minute, Skill: "engineering", Level: 3, Best: 0, Available: 84000,
		Blocked: ProductionRefusedSkill, Gap: gap}))
	add("Refusal · a design needs a skill, recruit or train", ProductionRefusal(c, ProductionRefusalView{
		Kind: ProductionRefusedSkill, Ref: maker, Skill: "cooking", Level: 1, Have: 0,
		Gap: &SkillGap{Company: maker.Code, Skill: "cooking", Level: 1}}))

	add("Recruitment · the hub", RecruitHub(c, RecruitHubView{Ref: maker, Staff: 1, MaxStaff: 10, Running: 1, MaxCampaign: 2,
		Campaigns: []RecruitCampaignLine{
			{No: 14, Status: CampaignRunning, Skill: "engineering", Level: 3, Cities: 3, Positions: 2, Hired: 1, Pending: 2,
				NextAt: snapshotNow.Add(6 * time.Minute)},
			{No: 15, Status: CampaignDraft, Skill: "medicine", Level: 2},
			{No: 9, Status: CampaignEnded, Skill: "programming", Level: 1, Cities: 1, Positions: 1},
		}}))
	add("Recruitment · no campaign yet", RecruitHub(c, RecruitHubView{Ref: maker, MaxStaff: 10, MaxCampaign: 2}))

	draft := RecruitDraftView{Ref: maker, No: 14, Skill: "engineering", Level: 3, MaxLevel: 18,
		Skills: []string{"engineering", "programming", "medicine", "management", "finance", "mechanics"},
		Cities: []RecruitCityChoice{{Code: ostmarch.Code, Name: ostmarch.Name, On: true},
			{Code: fenwick.Code, Name: fenwick.Name, On: true}, {Code: calderis.Code, Name: calderis.Name, Abroad: true}},
		CityCode: ostmarch.Code, City: ostmarch.Name, Positions: 1, MaxPositions: 5, Salary: 330, Housing: 33,
		Signing: 330, Relocation: 1000, Term: 10, Market: 264, Reach: 41, ChanceBPS: 4200, AdFee: 1500, Available: 52000,
		Checks: 4, Every: 6 * time.Minute,
		Presets: RecruitPresets{Salary: []int64{250, 290, 343}, Housing: []int64{0, 33, 66}, Signing: []int64{0, 330, 990},
			Relocation: []int64{0, 1000, 2500}, Terms: []int{5, 10, 20}, Shares: []int64{0, 10, 25, 50}},
		Notice: "created"}
	add("Recruitment · a new draft", RecruitDraft(c, draft))
	for _, section := range []string{RecruitSectionSkill, RecruitSectionCities, RecruitSectionPay, RecruitSectionTerms} {
		v := draft
		v.Section, v.Notice = section, "set"
		if section == RecruitSectionTerms {
			v.Shares, v.ShareValue, v.Auto = 25, 4200, true
		}
		add("Recruitment · the builder, "+section, RecruitDraft(c, v))
	}
	confirm := draft
	confirm.Notice, confirm.Confirm = "", true
	add("Recruitment · post, confirm", RecruitDraft(c, confirm))
	none := draft
	none.Notice, none.Cities, none.Reach, none.ChanceBPS = "", nil, 0, 0
	add("Recruitment · a draft with no city", RecruitDraft(c, none))

	offer := RecruitOffer{Salary: 330, Housing: 33, Signing: 330, Relocation: 1000, Term: 10, Shares: 25}
	running := RecruitCampaignView{Ref: maker, Line: RecruitCampaignLine{No: 14, Status: CampaignRunning, Skill: "engineering",
		Level: 3, Cities: 2, Positions: 2, Hired: 1, NextAt: snapshotNow.Add(6 * time.Minute)}, ChecksLeft: 2, Offer: offer,
		Cities: []Named{fenwick, ostmarch}, AdFee: 3000, Available: 48000, Notice: "posted",
		Candidates: []RecruitCandidateLine{
			{No: 31, NameSeed: 7, Skill: "engineering", Level: 4, Home: fenwick, Expected: 520, Cost: 1330,
				Status: CandidatePending, ExpiresAt: snapshotNow.Add(24 * time.Minute)},
			{No: 32, NameSeed: 52, Skill: "engineering", Level: 3, Home: ostmarch, Expected: 290, Cost: 330,
				Status: CandidatePending, ExpiresAt: snapshotNow.Add(20 * time.Minute)},
			{No: 29, NameSeed: 3, Skill: "engineering", Level: 3, Home: calderis, Abroad: true, Expected: 610, Status: CandidateHired},
			{No: 27, NameSeed: 88, Skill: "engineering", Level: 5, Home: fenwick, Expected: 700, Status: CandidateExpired},
			{No: 26, NameSeed: 120, Skill: "engineering", Level: 3, Home: fenwick, Expected: 410, Status: CandidateRejected},
		}}
	add("Campaign · running, candidates waiting", RecruitCampaign(c, running))
	hired := running
	hired.Notice, hired.NoticeSeed = "hired", 7
	add("Campaign · one hired", RecruitCampaign(c, hired))
	cancel := running
	cancel.Notice, cancel.ConfirmCancel = "", true
	add("Campaign · stop, confirm", RecruitCampaign(c, cancel))
	ended := RecruitCampaignView{Ref: maker, Line: RecruitCampaignLine{No: 9, Status: CampaignEnded, Skill: "programming", Level: 1,
		Cities: 1, Positions: 1}, Offer: RecruitOffer{Salary: 250, Term: 5}, Cities: []Named{ostmarch}, AdFee: 1500,
		Available: 48000}
	add("Campaign · over, nobody answered", RecruitCampaign(c, ended))
	filled := ended
	filled.Line.Status, filled.Line.Hired, filled.Auto = CampaignFilled, 1, true
	add("Campaign · filled by auto-hire", RecruitCampaign(c, filled))

	staff := SpecialistsView{Ref: maker, Max: 10, Lines: []SpecialistLine{
		{No: 5, NameSeed: 3, Skill: "engineering", Level: 3, Home: calderis, Salary: 330, Housing: 33, Served: 4, Term: 10,
			MarketDue: 363, Shares: 25},
		{No: 6, NameSeed: 44, Skill: "medicine", Level: 2, Home: fenwick, Salary: 300, Served: 3, Term: 10, Underpaid: true,
			UnderpaidLeft: 1, MarketDue: 420},
		{No: 7, NameSeed: 9, Skill: "programming", Level: 5, Home: ostmarch, Salary: 700, Served: 5, Term: 5, Expiring: true,
			UnpaidLeft: 1, MarketDue: 700},
	}}
	add("Specialists · the list", Specialists(c, staff))
	renewed := staff
	renewed.Notice, renewed.NoticeSeed = "renewed", 9
	add("Specialists · a contract renewed", Specialists(c, renewed))
	dismiss := staff
	dismiss.Confirm, dismiss.ConfirmAct = &staff.Lines[1], SpecialistDismiss
	add("Specialists · part ways, confirm", Specialists(c, dismiss))
	add("Specialists · none yet", Specialists(c, SpecialistsView{Ref: maker, Max: 10}))

	for _, r := range []RecruitRefusalView{
		{Kind: RecruitRefusedFunds, Ref: maker, Need: 3000, Have: 1200},
		{Kind: RecruitRefusedCampaigns, Ref: maker, Max: 2},
		{Kind: RecruitRefusedStaff, Ref: maker, Max: 10},
		{Kind: RecruitRefusedNoCities, Ref: maker},
		{Kind: RecruitRefusedNoSkill, Ref: maker},
		{Kind: RecruitRefusedFinished, Ref: maker},
		{Kind: RecruitRefusedGone, Ref: maker, NameSeed: 7},
		{Kind: RecruitRefusedPosted, Ref: maker},
		{Kind: RecruitRefusedAmount, Ref: maker},
		{Kind: RecruitRefusedFilled, Ref: maker},
		{Kind: RecruitRefusedNotNow, Ref: maker},
		{Kind: RecruitRefusedNotFound},
	} {
		add("Recruitment refusal · "+r.Kind, RecruitRefusal(c, r))
	}

	notice := RecruitNoticeView{Company: maker, CampaignNo: 14, NameSeed: 7, Skill: "engineering", Level: 4}
	for _, k := range []struct {
		kind, reason string
		count        int
		amount       int64
	}{
		{RecruitNoticeApplied, "", 3, 0}, {RecruitNoticeHired, "", 0, 0}, {RecruitNoticeEnded, "", 0, 0},
		{RecruitNoticeFilled, "", 0, 0}, {RecruitNoticeUnpaid, "", 0, 0}, {RecruitNoticeCompleted, "", 0, 5250},
		{RecruitNoticeCompleted, "", 0, 0}, {RecruitNoticeLeft, "unpaid", 0, 0}, {RecruitNoticeLeft, "poached", 0, 0},
		{RecruitNoticeLeft, "contract_end", 0, 0}, {RecruitNoticeLeft, "company_closed", 0, 0},
	} {
		v := notice
		v.Kind, v.Reason, v.Count, v.Amount = k.kind, k.reason, k.count, k.amount
		add("Notice · "+k.kind+" "+k.reason, RecruitNotice(sent(c), v))
	}
}

// recruitAnnouncements is the job advertisement a target city's groups
// read, and the typed amount's question.
func recruitAnnouncements(c Context, book *screentest.Book) {
	n := productionNames[c.Lang]
	maker := CompanyRef{Code: "Q7M2K9B", Name: n.maker, Type: Named{Code: "tech_studio", Name: "Technology studio"}}
	book.AddText("announcement · a job advertisement", RecruitAdAnnouncement(c, maker, "engineering", 3, "fenwick_span", "Fenwick Span"))
	book.AddText("question · company.ramount", InputPrompt(c, "company.ramount", ""))
	book.AddText("question · company.ramount · reply box", InputPlaceholder(c, "company.ramount"))
}
