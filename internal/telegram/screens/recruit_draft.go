package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// RecruitCityChoice is one city the campaign builder offers.
type RecruitCityChoice struct {
	Code, Name string
	On         bool
	// Abroad is a city of another country.
	Abroad bool
}

// RecruitPresets are the builder's one-press choices, worked out: amounts
// of money, contract lengths, share counts.
type RecruitPresets struct {
	Salary, Housing, Signing, Relocation []int64
	Terms                                []int
	Shares                               []int64
}

// RecruitDraftView is a campaign being built.
type RecruitDraftView struct {
	Ref     CompanyRef
	No      int64
	Section string
	// What the campaign seeks.
	Skill           string
	Level, MaxLevel int
	// Skills are those cities have specialists of.
	Skills []string
	Cities []RecruitCityChoice
	// CityCode and City are the company's city, where the market is quoted.
	CityCode, City          string
	Positions, MaxPositions int
	// The package.
	Salary, Housing, Signing, Relocation int64
	Term                                 int
	Shares                               int64
	// ShareValue is what the phantom shares are worth today.
	ShareValue int64
	Auto       bool
	// Market is what a specialist of the level expects in the company's
	// city; Reach how many of the skill at or above the level are free in
	// the chosen cities; ChanceBPS a candidate of the company's city's
	// chance of applying, before preferences.
	Market    int64
	Reach     int64
	ChanceBPS int
	// AdFee is one city's fee; Available the company's free money.
	AdFee, Available int64
	Presets          RecruitPresets
	// Checks and Every are how the campaign runs once posted: its checks
	// and the wall-clock time between two.
	Checks int
	Every  time.Duration
	// Confirm asks before posting.
	Confirm bool
	// Notice is a notice kind (recruit.draft_notice.<kind>), "" for none.
	Notice string
}

// chosen counts the cities on.
func (v RecruitDraftView) chosen() (n int, names []Named) {
	for _, ci := range v.Cities {
		if ci.On {
			n++
			names = append(names, Named{Code: ci.Code, Name: ci.Name})
		}
	}
	return n, names
}

// draftSummary is the campaign as it stands.
func (c Context) draftSummary(v RecruitDraftView) string {
	n, names := v.chosen()
	cities := c.T("recruit.draft.no_cities", nil)
	if n > 0 {
		list := make([]string, 0, len(names))
		for _, ci := range names {
			list = append(list, c.CityName(ci.Code, ci.Name))
		}
		cities = c.list(list)
	}
	money := func(v int64) string { return FormatMoney(c, v) }
	lines := []string{
		c.T("recruit.draft.skill", map[string]any{"what": c.skillLevel(v.Skill, v.Level)}),
		c.T("recruit.draft.cities", map[string]any{"cities": cities}),
		c.T("recruit.draft.positions", map[string]any{"count": FormatNumber(c, int64(v.Positions))}),
		c.T("recruit.draft.salary", map[string]any{"amount": money(v.Salary), "market": money(v.Market),
			"city": c.CityName(v.CityCode, v.City)}),
	}
	optional := func(key string, amount int64) {
		if amount > 0 {
			lines = append(lines, c.T("recruit.draft."+key, map[string]any{"amount": money(amount)}))
		} else {
			lines = append(lines, c.T("recruit.draft."+key+"_none", nil))
		}
	}
	optional("housing", v.Housing)
	optional("signing", v.Signing)
	optional("relocation", v.Relocation)
	lines = append(lines, c.T("recruit.draft.term", map[string]any{"count": FormatNumber(c, int64(v.Term))}))
	if v.Shares > 0 {
		lines = append(lines, c.T("recruit.draft.shares", map[string]any{"count": FormatNumber(c, v.Shares),
			"amount": money(v.ShareValue)}))
	} else {
		lines = append(lines, c.T("recruit.draft.shares_none", nil))
	}
	auto := "recruit.draft.auto_off"
	if v.Auto {
		auto = "recruit.draft.auto_on"
	}
	lines = append(lines, c.T(auto, nil))
	outlook := []string{c.T("recruit.draft.reach", map[string]any{"count": FormatNumber(c, v.Reach),
		"what": c.skillLevel(v.Skill, v.Level)})}
	if n > 0 {
		outlook = append(outlook, c.T("recruit.draft.chance", map[string]any{"percent": PercentFromBPS(c, v.ChanceBPS)}))
	}
	outlook = append(outlook, c.T("recruit.draft.fee", map[string]any{"amount": money(v.AdFee * int64(n)),
		"cities": FormatNumber(c, int64(n)), "fee": money(v.AdFee), "money": money(v.Available)}))
	return paragraphs(body(lines...), body(outlook...))
}

// RecruitDraft renders the campaign builder at one of its sections.
func RecruitDraft(c Context, v RecruitDraftView) *presenter.Response {
	return c.withView(renderRecruitDraft(c, v), ScreenRecruitDraft, v)
}

func renderRecruitDraft(c Context, v RecruitDraftView) *presenter.Response {
	no := strconv.FormatInt(v.No, 10)
	head := body(c.T("recruit.draft_title", map[string]any{"no": FormatNumber(c, v.No), "name": v.Ref.Name}),
		c.T("recruit.draft_section."+sectionKey(v.Section), nil))
	notice := ""
	if v.Notice != "" {
		notice = c.T("recruit.draft_notice."+v.Notice, nil)
	}
	kb := keyboards.New()
	back := []string{AddrRecruitDraft, no}
	var extra string
	switch {
	case v.Confirm:
		n, _ := v.chosen()
		extra = c.T("recruit.draft.confirm", map[string]any{"cities": FormatNumber(c, int64(n)),
			"amount": FormatMoney(c, v.AdFee*int64(n)), "checks": FormatNumber(c, int64(v.Checks)),
			"every": FormatDuration(c, v.Every)})
		kb.Add(c.T("recruit.button.post_confirm", map[string]any{"amount": FormatMoney(c, v.AdFee*int64(n))}),
			AddrRecruitPost, no, RecruitConfirm)
	case v.Section == RecruitSectionSkill:
		c.draftSkillButtons(kb, v, no)
	case v.Section == RecruitSectionCities:
		c.draftCityButtons(kb, v, no)
	case v.Section == RecruitSectionPay:
		c.draftPayButtons(kb, v, no)
	case v.Section == RecruitSectionTerms:
		c.draftTermButtons(kb, v, no)
	default:
		back = []string{AddrRecruit, v.Ref.Code}
		sk, _ := keyboards.Button(c.T("recruit.button.section_skill", nil), AddrRecruitDraft, no, RecruitSectionSkill)
		ci, _ := keyboards.Button(c.T("recruit.button.section_cities", nil), AddrRecruitDraft, no, RecruitSectionCities)
		kb.Row(sk, ci)
		pay, _ := keyboards.Button(c.T("recruit.button.section_pay", nil), AddrRecruitDraft, no, RecruitSectionPay)
		te, _ := keyboards.Button(c.T("recruit.button.section_terms", nil), AddrRecruitDraft, no, RecruitSectionTerms)
		kb.Row(pay, te)
		if n, _ := v.chosen(); n > 0 {
			kb.Add(c.T("recruit.button.post", map[string]any{"amount": FormatMoney(c, v.AdFee*int64(n))}), AddrRecruitPost, no)
		}
	}
	refresh := []string{AddrRecruitDraft, no}
	if v.Section != "" {
		refresh = append(refresh, v.Section)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(back...), RefreshData: keyboards.Data(refresh...)}))
	return c.respond(paragraphs(notice, head, c.draftSummary(v), extra), kb.Build()).MarkPrivate()
}

// sectionKey names a section in the catalogue.
func sectionKey(s string) string {
	if s == "" {
		return "main"
	}
	return s
}

// mark prefixes a chosen button.
func (c Context) mark(on bool, label string) string {
	if on {
		return c.T("recruit.chosen", map[string]any{"label": label})
	}
	return label
}

func (c Context) draftSkillButtons(kb *keyboards.Builder, v RecruitDraftView, no string) {
	var row []presenter.Button
	for _, s := range v.Skills {
		if btn, ok := keyboards.Button(c.mark(s == v.Skill, c.SkillName(s)), AddrRecruitSet, no, RecruitFieldSkill, s); ok {
			row = append(row, btn)
		}
	}
	kb.Grid(3, row...)
	var lv []presenter.Button
	if v.Level > 1 {
		b, _ := keyboards.Button(c.T("recruit.button.level_down", nil), AddrRecruitSet, no, RecruitFieldLevel, strconv.Itoa(v.Level-1))
		lv = append(lv, b)
	}
	if v.Level < v.MaxLevel {
		b, _ := keyboards.Button(c.T("recruit.button.level_up", nil), AddrRecruitSet, no, RecruitFieldLevel, strconv.Itoa(v.Level+1))
		lv = append(lv, b)
	}
	kb.Row(lv...)
}

func (c Context) draftCityButtons(kb *keyboards.Builder, v RecruitDraftView, no string) {
	var row []presenter.Button
	for _, ci := range v.Cities {
		label := c.CityName(ci.Code, ci.Name)
		if ci.Abroad {
			label = c.T("recruit.city_abroad", map[string]any{"city": label})
		}
		on := "1"
		if ci.On {
			on = "0"
		}
		if btn, ok := keyboards.Button(c.mark(ci.On, label), AddrRecruitSet, no, RecruitFieldCity, ci.Code, on); ok {
			row = append(row, btn)
		}
	}
	kb.Grid(2, row...)
	var scopes []presenter.Button
	for _, s := range []string{RecruitScopeOwn, RecruitScopeNation, RecruitScopeAll} {
		b, _ := keyboards.Button(c.T("recruit.button.scope_"+s, nil), AddrRecruitSet, no, RecruitFieldScope, s)
		scopes = append(scopes, b)
	}
	kb.Grid(3, scopes...)
}

// presetRow adds one row of money presets for a field, then its «✏️».
func (c Context) presetRow(kb *keyboards.Builder, no, field string, amounts []int64, current int64) {
	var row []presenter.Button
	for i, a := range amounts {
		label := c.T("recruit.preset."+field, map[string]any{"amount": FormatMoney(c, a)})
		if a == 0 {
			label = c.T("recruit.preset."+field+"_none", nil)
		}
		if btn, ok := keyboards.Button(c.mark(a == current, label), AddrRecruitSet, no, field, strconv.Itoa(i)); ok {
			row = append(row, btn)
		}
	}
	if btn, ok := askButton(c.T("recruit.button.type_"+field, nil), commandRecruitAmount, no, field); ok {
		row = append(row, btn)
	}
	kb.Grid(4, row...)
}

func (c Context) draftPayButtons(kb *keyboards.Builder, v RecruitDraftView, no string) {
	c.presetRow(kb, no, RecruitFieldSalary, v.Presets.Salary, v.Salary)
	c.presetRow(kb, no, RecruitFieldHousing, v.Presets.Housing, v.Housing)
	c.presetRow(kb, no, RecruitFieldSigning, v.Presets.Signing, v.Signing)
	c.presetRow(kb, no, RecruitFieldRelocation, v.Presets.Relocation, v.Relocation)
}

func (c Context) draftTermButtons(kb *keyboards.Builder, v RecruitDraftView, no string) {
	var terms []presenter.Button
	for i, t := range v.Presets.Terms {
		label := c.T("recruit.preset.term", map[string]any{"count": FormatNumber(c, int64(t))})
		if b, ok := keyboards.Button(c.mark(t == v.Term, label), AddrRecruitSet, no, RecruitFieldTerm, strconv.Itoa(i)); ok {
			terms = append(terms, b)
		}
	}
	kb.Grid(4, terms...)
	var shares []presenter.Button
	for i, s := range v.Presets.Shares {
		label := c.T("recruit.preset.shares", map[string]any{"count": FormatNumber(c, s)})
		if b, ok := keyboards.Button(c.mark(s == v.Shares, label), AddrRecruitSet, no, RecruitFieldShares, strconv.Itoa(i)); ok {
			shares = append(shares, b)
		}
	}
	kb.Grid(4, shares...)
	var pos []presenter.Button
	if v.Positions > 1 {
		b, _ := keyboards.Button(c.T("recruit.button.fewer", nil), AddrRecruitSet, no, RecruitFieldPositions, strconv.Itoa(v.Positions-1))
		pos = append(pos, b)
	}
	if v.Positions < v.MaxPositions {
		b, _ := keyboards.Button(c.T("recruit.button.more", nil), AddrRecruitSet, no, RecruitFieldPositions, strconv.Itoa(v.Positions+1))
		pos = append(pos, b)
	}
	kb.Row(pos...)
	auto, label := "1", "recruit.button.auto_on"
	if v.Auto {
		auto, label = "0", "recruit.button.auto_off"
	}
	kb.Add(c.T(label, nil), AddrRecruitSet, no, RecruitFieldAuto, auto)
}
