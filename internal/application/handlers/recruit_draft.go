package handlers

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The recruitment hub and the campaign builder: a draft is a
// recruit_campaigns row in 'draft', one per company, built by presses and
// typed amounts and posted once.

// market is the job market of one command.
func (h *RecruitHandler) market(snap *content.Snapshot, def content.RecruitmentDef) jobMarket {
	return jobMarket{def: def, snap: snap, scale: h.scale, now: h.now()}
}

// Hub handles company.recruit: a company's recruitment.
func (h *RecruitHandler) Hub(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.RecruitHubView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, "", req.code())
		if err != nil {
			return err
		}
		view, err = h.hubView(ctx, tx, snap, *c)
		return err
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	return screens.RecruitHub(h.screen(meta, lang), view), nil
}

func (h *RecruitHandler) hubView(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company,
) (screens.RecruitHubView, error) {
	v := screens.RecruitHubView{Ref: companyRef(snap, c), MaxStaff: h.rules.MaxStaff, MaxCampaign: h.rules.MaxCampaigns}
	staff, err := tx.Recruitment().Staff(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Staff = len(staff)
	camps, err := tx.Recruitment().Campaigns(ctx, c.ID, 8)
	if err != nil {
		return v, err
	}
	for _, camp := range camps {
		line, err := h.campaignLine(ctx, tx, camp)
		if err != nil {
			return v, err
		}
		if camp.Open() {
			v.Running++
		}
		v.Campaigns = append(v.Campaigns, line)
	}
	return v, nil
}

// campaignLine is a campaign for the hub.
func (h *RecruitHandler) campaignLine(ctx context.Context, tx application.Tx, camp application.RecruitCampaign,
) (screens.RecruitCampaignLine, error) {
	l := screens.RecruitCampaignLine{No: camp.No, Status: camp.Status, Skill: camp.Skill, Level: camp.MinLevel,
		Cities: len(camp.Cities), Positions: camp.Positions, Hired: camp.Hired}
	if camp.NextCheckAt != nil {
		l.NextAt = *camp.NextCheckAt
	}
	if camp.Status == application.CampaignDraft {
		return l, nil
	}
	cands, err := tx.Recruitment().Candidates(ctx, camp.ID)
	if err != nil {
		return l, err
	}
	now := h.now()
	for _, cd := range cands {
		if cd.Status == application.CandidatePending && now.Before(cd.ExpiresAt) {
			l.Pending++
		}
	}
	return l, nil
}

// New handles company.rnew: the company's draft, created when it has none,
// set to the skill and level the request names (from the research lab's
// «recruit» button, for one).
func (h *RecruitHandler) New(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.RecruitDraftView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		def, err := recruitment(snap)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, "", req.code())
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		draft, err := tx.Recruitment().Draft(ctx, c.ID)
		if err != nil {
			return err
		}
		notice := ""
		if fresh {
			m := h.market(snap, def)
			if draft, err = h.startDraft(ctx, tx, snap, m, *c, draft, p.ID, req); err != nil {
				return err
			}
			notice = "created"
		}
		if draft == nil {
			return refuseRecruit(screens.RecruitRefusedNotFound, c, snap)
		}
		view, err = h.draftView(ctx, tx, snap, def, *c, *draft, "")
		view.Notice = notice
		return err
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	return screens.RecruitDraft(h.screen(meta, lang), view), nil
}

// startDraft creates the company's draft, or re-aims the one it has, at the
// skill and level the request names.
func (h *RecruitHandler) startDraft(ctx context.Context, tx application.Tx, snap *content.Snapshot, m jobMarket,
	c application.Company, draft *application.RecruitCampaign, by string, req RecruitRequest,
) (*application.RecruitCampaign, error) {
	now := m.now
	skill := strings.TrimSpace(req.Skill)
	if _, ok := m.def.Skill(skill); !ok {
		skill = ""
	}
	level, _ := strconv.Atoi(strings.TrimSpace(req.Level))
	if draft != nil {
		if skill != "" {
			draft.Skill = skill
		}
		if level > 0 {
			draft.MinLevel = min(level, max(m.def.MaxLevel(), 1))
		}
		if skill != "" || level > 0 {
			if err := h.reprice(ctx, tx, m, c, draft); err != nil {
				return nil, err
			}
			draft.UpdatedAt = now
			if err := tx.Recruitment().SaveCampaign(ctx, *draft); err != nil {
				return nil, err
			}
		}
		return draft, nil
	}
	if skill == "" {
		skill = m.def.Skills[0].Skill
	}
	own, _ := cityOfCompany(snap, c)
	pr := m.def.Presets
	camp := application.RecruitCampaign{ID: h.ids.NewID(), CompanyID: c.ID, Status: application.CampaignDraft,
		Skill: skill, MinLevel: min(max(level, 1), max(m.def.MaxLevel(), 1)), Cities: []string{own.Code},
		Positions: 1, Relocation: pr.Relocation[0], TermPeriods: pr.Terms[len(pr.Terms)/2], ChecksTotal: h.rules.Checks,
		CreatedBy: by, CreatedAt: now, UpdatedAt: now}
	if err := h.reprice(ctx, tx, m, c, &camp); err != nil {
		return nil, err
	}
	created, err := tx.Recruitment().CreateCampaign(ctx, camp)
	if isSentinel(err, application.ErrCampaignDraftExists) {
		return tx.Recruitment().Draft(ctx, c.ID)
	}
	return &created, err
}

// reprice sets a draft's salary to the middle preset of the market for its
// skill and level in the company's city: the salary follows the market when
// what the campaign seeks changes.
func (h *RecruitHandler) reprice(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
	camp *application.RecruitCampaign,
) error {
	own, ok := cityOfCompany(m.snap, c)
	if !ok {
		return nil
	}
	market, err := m.expectedFrom(ctx, tx, camp.Skill, camp.MinLevel, own, own)
	if err != nil {
		return err
	}
	pr := m.def.Presets.SalaryBPS
	camp.Salary = max(recruit.Scale(market, pr[len(pr)/2]), 1)
	return nil
}

// Draft handles company.rdraft: the campaign builder at a section; a
// campaign already posted opens as a campaign.
func (h *RecruitHandler) Draft(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	return h.draftWith(ctx, meta, req, false)
}

func (h *RecruitHandler) draftWith(ctx context.Context, meta envelope.Metadata, req RecruitRequest, confirm bool,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := req.number()
	if !ok {
		return nil, errNoNumber
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view   screens.RecruitDraftView
		posted *screens.RecruitCampaignView
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		def, err := recruitment(snap)
		if err != nil {
			return err
		}
		camp, c, err := h.campaignOf(ctx, tx, snap, p, no, false)
		if err != nil {
			return err
		}
		if camp.Status != application.CampaignDraft {
			v, err := h.campaignView(ctx, tx, snap, *c, *camp)
			posted = &v
			return err
		}
		view, err = h.draftView(ctx, tx, snap, def, *c, *camp, req.Section)
		view.Confirm = confirm
		return err
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	if posted != nil {
		return screens.RecruitCampaign(h.screen(meta, lang), *posted), nil
	}
	return screens.RecruitDraft(h.screen(meta, lang), view), nil
}

// campaignOf reads a campaign by number and its company, which the player
// must recruit for (locked); lock also locks the campaign.
func (h *RecruitHandler) campaignOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	no int64, lock bool,
) (*application.RecruitCampaign, *application.Company, error) {
	camp, err := tx.Recruitment().Campaign(ctx, no, false)
	if isSentinel(err, application.ErrCampaignNotFound) {
		return nil, nil, refuseRecruit(screens.RecruitRefusedNotFound, nil, snap)
	}
	if err != nil {
		return nil, nil, err
	}
	c, err := h.managed(ctx, tx, snap, p, camp.CompanyID, "")
	if err != nil {
		return nil, nil, err
	}
	if lock {
		if camp, err = tx.Recruitment().Campaign(ctx, no, true); err != nil {
			return nil, nil, err
		}
	}
	return camp, c, nil
}

// draftView builds the campaign builder.
func (h *RecruitHandler) draftView(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	def content.RecruitmentDef, c application.Company, camp application.RecruitCampaign, section string,
) (screens.RecruitDraftView, error) {
	m := h.market(snap, def)
	own, _ := cityOfCompany(snap, c)
	v := screens.RecruitDraftView{Ref: companyRef(snap, c), No: camp.No, Section: section, Skill: camp.Skill,
		Level: camp.MinLevel, MaxLevel: def.MaxLevel(), CityCode: own.Code, City: own.Name, Positions: camp.Positions,
		MaxPositions: h.rules.MaxPositions, Salary: camp.Salary, Housing: camp.Housing, Signing: camp.Signing,
		Relocation: camp.Relocation, Term: camp.TermPeriods, Shares: camp.Shares, Auto: camp.AutoAccept,
		AdFee: def.AdFee, Checks: h.rules.Checks, Every: h.scale.RealWait(h.rules.CheckEvery)}
	switch section {
	case screens.RecruitSectionSkill, screens.RecruitSectionCities, screens.RecruitSectionPay, screens.RecruitSectionTerms:
	default:
		v.Section = ""
	}
	for _, s := range def.Skills {
		v.Skills = append(v.Skills, s.Skill)
	}
	country, err := tx.Diplomacy().CountryOfCity(ctx, own.ID)
	if err != nil {
		return v, err
	}
	domestic := map[string]bool{}
	if country != "" {
		cs, err := tx.Diplomacy().CitiesOf(ctx, country)
		if err != nil {
			return v, err
		}
		for _, ci := range cs {
			domestic[ci.ID] = true
		}
	}
	chosen := map[string]bool{}
	for _, code := range camp.Cities {
		chosen[code] = true
	}
	for _, ci := range snap.Cities() {
		v.Cities = append(v.Cities, screens.RecruitCityChoice{Code: ci.Code, Name: ci.Name, On: chosen[ci.Code],
			Abroad: country != "" && !domestic[ci.ID] && ci.ID != own.ID})
	}
	if v.Market, err = m.expectedFrom(ctx, tx, camp.Skill, camp.MinLevel, own, own); err != nil {
		return v, err
	}
	pr := def.Presets
	for _, bps := range pr.SalaryBPS {
		v.Presets.Salary = append(v.Presets.Salary, max(recruit.Scale(v.Market, bps), 1))
	}
	for _, bps := range pr.HousingBPS {
		v.Presets.Housing = append(v.Presets.Housing, recruit.Scale(camp.Salary, bps))
	}
	for _, bps := range pr.SigningBPS {
		v.Presets.Signing = append(v.Presets.Signing, recruit.Scale(camp.Salary, bps))
	}
	v.Presets.Relocation, v.Presets.Terms, v.Presets.Shares = pr.Relocation, pr.Terms, pr.Shares
	if v.ShareValue, err = shareValue(ctx, tx, c, camp.Shares); err != nil {
		return v, err
	}
	b, _, err := companyBooks(ctx, tx, c)
	if err != nil {
		return v, err
	}
	v.Available = b.Available().Minor()
	v.Reach, v.ChanceBPS, err = h.outlook(ctx, tx, m, c, camp, own, country, v.ShareValue)
	return v, err
}

// outlook is how many specialists a campaign could reach, and the rough
// chance one of the first of its cities answers, before preferences.
func (h *RecruitHandler) outlook(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
	camp application.RecruitCampaign, own world.City, country string, equity int64,
) (int64, int, error) {
	var (
		reach  int64
		chance int64
		first  = true
	)
	appeal, err := m.appeal(ctx, tx, c, own, country)
	if err != nil {
		return 0, 0, err
	}
	neutral := recruit.Preference{SalaryBPS: recruit.BPS, HousingBPS: recruit.BPS, SigningBPS: recruit.BPS,
		EquityBPS: recruit.BPS, MoveBPS: recruit.BPS}
	for _, home := range sortedCities(m.snap, camp.Cities) {
		o, err := m.originOf(ctx, tx, home, own, country)
		if err != nil {
			return 0, 0, err
		}
		if o.blocked {
			continue
		}
		for level := camp.MinLevel; level <= m.def.MaxLevel(); level++ {
			p, capacity, err := m.pool(ctx, tx, home, camp.Skill, level, false)
			if err != nil {
				return 0, 0, err
			}
			pending, err := tx.Recruitment().Pending(ctx, home.ID, camp.Skill, level, m.now)
			if err != nil {
				return 0, 0, err
			}
			reach += max(p.Available-pending, 0)
			if first && capacity > 0 {
				first = false
				expected := m.expected(camp.Skill, level, own, p.Available, capacity)
				value := recruit.Value(offerOf(camp, equity), neutral, m.moveCost(o))
				chance = recruit.Scale(m.def.Acceptance.Curve().Chance(value, expected), m.placeBPS(o), appeal)
			}
		}
	}
	return reach, int(min(chance, recruit.BPS)), nil
}

// sortedCities are the content's cities of codes, in code order: the one
// order every check locks pools in.
func sortedCities(snap *content.Snapshot, codes []string) []world.City {
	sorted := append([]string(nil), codes...)
	sort.Strings(sorted)
	var out []world.City
	for _, code := range sorted {
		if ci, ok := snap.City(code); ok {
			out = append(out, ci)
		}
	}
	return out
}

// errNoNumber refuses a request without a public number.
var errNoNumber = invalidRecruit("the request names no number")

// timeOrZero is a pointer's time, zero for nil.
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
