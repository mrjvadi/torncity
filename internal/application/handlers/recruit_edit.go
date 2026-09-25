package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// invalidRecruit is a malformed recruitment request: a forged or stale
// press, never a player's mistake.
func invalidRecruit(msg string) error { return errors.InvalidInput(msg) }

// Set handles company.rset: one press of the campaign builder.
func (h *RecruitHandler) Set(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	return h.edit(ctx, meta, req, func(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
		camp *application.RecruitCampaign,
	) (string, error) {
		return h.apply(ctx, tx, m, c, camp, req)
	})
}

// Amount handles company.ramount: a typed amount for a field of the pay.
func (h *RecruitHandler) Amount(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	return h.edit(ctx, meta, req, func(_ context.Context, _ application.Tx, m jobMarket, c application.Company,
		camp *application.RecruitCampaign,
	) (string, error) {
		amount, ok := quantityArg(req.Amount)
		if strings.TrimSpace(req.Amount) == "0" {
			amount, ok = 0, true
		}
		if !ok || amount > h.rules.Limits.Max.Minor() || amount > recruit.MaxWage {
			r := refuseRecruit(screens.RecruitRefusedAmount, &c, m.snap)
			r.view.Back = []string{screens.AddrRecruitDraft, itoa64(camp.No), screens.RecruitSectionPay}
			return "", r
		}
		switch strings.TrimSpace(req.Field) {
		case screens.RecruitFieldSalary:
			if amount < 1 {
				r := refuseRecruit(screens.RecruitRefusedAmount, &c, m.snap)
				r.view.Back = []string{screens.AddrRecruitDraft, itoa64(camp.No), screens.RecruitSectionPay}
				return "", r
			}
			camp.Salary = amount
		case screens.RecruitFieldHousing:
			camp.Housing = amount
		case screens.RecruitFieldSigning:
			camp.Signing = amount
		case screens.RecruitFieldRelocation:
			camp.Relocation = amount
		default:
			return "", invalidRecruit("no such field of the pay")
		}
		return screens.RecruitSectionPay, nil
	})
}

// edit runs one change to a draft and shows the builder where it was made.
func (h *RecruitHandler) edit(ctx context.Context, meta envelope.Metadata, req RecruitRequest,
	change func(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
		camp *application.RecruitCampaign) (string, error),
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
		camp, c, err := h.campaignOf(ctx, tx, snap, p, no, true)
		if err != nil {
			return err
		}
		if camp.Status != application.CampaignDraft {
			r := refuseRecruit(screens.RecruitRefusedPosted, c, snap)
			r.view.Back = []string{screens.AddrRecruitCamp, itoa64(camp.No)}
			return r
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		m := h.market(snap, def)
		section := req.Section
		notice := ""
		if fresh {
			before := *camp
			if section, err = change(ctx, tx, m, *c, camp); err != nil {
				return err
			}
			if camp.Skill != before.Skill || camp.MinLevel != before.MinLevel {
				if err := h.reprice(ctx, tx, m, *c, camp); err != nil {
					return err
				}
			}
			camp.UpdatedAt = m.now
			if err := tx.Recruitment().SaveCampaign(ctx, *camp); err != nil {
				return err
			}
			notice = "set"
		}
		view, err = h.draftView(ctx, tx, snap, def, *c, *camp, section)
		view.Notice = notice
		return err
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	return screens.RecruitDraft(h.screen(meta, lang), view), nil
}

// apply sets one field of a draft from a press; it returns the builder's
// section the press belongs to.
func (h *RecruitHandler) apply(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
	camp *application.RecruitCampaign, req RecruitRequest,
) (string, error) {
	value := strings.TrimSpace(req.Value)
	index := func(n int) (int, error) {
		i, err := strconv.Atoi(value)
		if err != nil || i < 0 || i >= n {
			return 0, invalidRecruit("no such preset")
		}
		return i, nil
	}
	pr := m.def.Presets
	switch strings.TrimSpace(req.Field) {
	case screens.RecruitFieldSkill:
		if _, ok := m.def.Skill(value); !ok {
			return "", invalidRecruit("no specialists of that skill")
		}
		camp.Skill = value
		return screens.RecruitSectionSkill, nil
	case screens.RecruitFieldLevel:
		l, err := strconv.Atoi(value)
		if err != nil || l < 1 || l > m.def.MaxLevel() {
			return "", invalidRecruit("no such level")
		}
		camp.MinLevel = l
		return screens.RecruitSectionSkill, nil
	case screens.RecruitFieldCity:
		if _, ok := m.snap.City(value); !ok {
			return "", invalidRecruit("no such city")
		}
		camp.Cities = toggleCity(camp.Cities, value, strings.TrimSpace(req.Extra) == "1")
		return screens.RecruitSectionCities, nil
	case screens.RecruitFieldScope:
		return screens.RecruitSectionCities, h.scope(ctx, tx, m, c, camp, value)
	case screens.RecruitFieldSalary:
		i, err := index(len(pr.SalaryBPS))
		if err != nil {
			return "", err
		}
		own, _ := cityOfCompany(m.snap, c)
		market, err := m.expectedFrom(ctx, tx, camp.Skill, camp.MinLevel, own, own)
		if err != nil {
			return "", err
		}
		camp.Salary = max(recruit.Scale(market, pr.SalaryBPS[i]), 1)
		return screens.RecruitSectionPay, nil
	case screens.RecruitFieldHousing:
		i, err := index(len(pr.HousingBPS))
		camp.Housing = recruit.Scale(camp.Salary, pr.HousingBPS[min(i, len(pr.HousingBPS)-1)])
		return screens.RecruitSectionPay, err
	case screens.RecruitFieldSigning:
		i, err := index(len(pr.SigningBPS))
		camp.Signing = recruit.Scale(camp.Salary, pr.SigningBPS[min(i, len(pr.SigningBPS)-1)])
		return screens.RecruitSectionPay, err
	case screens.RecruitFieldRelocation:
		i, err := index(len(pr.Relocation))
		camp.Relocation = pr.Relocation[min(i, len(pr.Relocation)-1)]
		return screens.RecruitSectionPay, err
	case screens.RecruitFieldTerm:
		i, err := index(len(pr.Terms))
		camp.TermPeriods = pr.Terms[min(i, len(pr.Terms)-1)]
		return screens.RecruitSectionTerms, err
	case screens.RecruitFieldShares:
		i, err := index(len(pr.Shares))
		camp.Shares = pr.Shares[min(i, len(pr.Shares)-1)]
		return screens.RecruitSectionTerms, err
	case screens.RecruitFieldPositions:
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > h.rules.MaxPositions {
			return "", invalidRecruit("no such number of positions")
		}
		camp.Positions = n
		return screens.RecruitSectionTerms, nil
	case screens.RecruitFieldAuto:
		camp.AutoAccept = value == "1"
		return screens.RecruitSectionTerms, nil
	}
	return "", invalidRecruit("no such field")
}

// toggleCity puts code in or out of a campaign's cities.
func toggleCity(cities []string, code string, on bool) []string {
	out := make([]string, 0, len(cities)+1)
	for _, c := range cities {
		if c != code {
			out = append(out, c)
		}
	}
	if on {
		out = append(out, code)
	}
	return out
}

// scope sets a campaign's cities to the company's own, its country's, or
// every city.
func (h *RecruitHandler) scope(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
	camp *application.RecruitCampaign, scope string,
) error {
	own, ok := cityOfCompany(m.snap, c)
	if !ok {
		return invalidRecruit("the company's city is not in the content")
	}
	switch scope {
	case screens.RecruitScopeOwn:
		camp.Cities = []string{own.Code}
	case screens.RecruitScopeNation:
		camp.Cities = []string{own.Code}
		country, err := tx.Diplomacy().CountryOfCity(ctx, own.ID)
		if err != nil || country == "" {
			return err
		}
		cities, err := tx.Diplomacy().CitiesOf(ctx, country)
		if err != nil {
			return err
		}
		camp.Cities = camp.Cities[:0]
		for _, ci := range cities {
			if _, ok := m.snap.City(ci.Code); ok {
				camp.Cities = append(camp.Cities, ci.Code)
			}
		}
	case screens.RecruitScopeAll:
		camp.Cities = nil
		for _, ci := range m.snap.Cities() {
			camp.Cities = append(camp.Cities, ci.Code)
		}
	default:
		return invalidRecruit("no such scope")
	}
	return nil
}

// Post handles company.rpost: without confirmation, what posting costs;
// with it, the advertising fee paid to each city's treasury and the
// campaign's first check scheduled.
func (h *RecruitHandler) Post(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	if !req.confirmed() {
		return h.draftWith(ctx, meta, req, true)
	}
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := req.number()
	if !ok {
		return nil, errNoNumber
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.RecruitCampaignView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		def, err := recruitment(snap)
		if err != nil {
			return err
		}
		camp, c, err := h.campaignOf(ctx, tx, snap, p, no, true)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if fresh && camp.Status == application.CampaignDraft {
			if err := h.post(ctx, tx, meta, snap, def, *c, camp); err != nil {
				return err
			}
			view, err = h.campaignView(ctx, tx, snap, *c, *camp)
			view.Notice = "posted"
			return err
		}
		view, err = h.campaignView(ctx, tx, snap, *c, *camp)
		return err
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	return screens.RecruitCampaign(h.screen(meta, lang), view), nil
}

// post pays a draft's advertising and sets it running.
func (h *RecruitHandler) post(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	def content.RecruitmentDef, c application.Company, camp *application.RecruitCampaign,
) error {
	cities := sortedCities(snap, camp.Cities)
	back := []string{screens.AddrRecruitDraft, itoa64(camp.No)}
	if len(cities) == 0 {
		r := refuseRecruit(screens.RecruitRefusedNoCities, &c, snap)
		r.view.Back = append(back, screens.RecruitSectionCities)
		return r
	}
	running, err := tx.Recruitment().Running(ctx, c.ID)
	if err != nil {
		return err
	}
	if running >= h.rules.MaxCampaigns {
		r := refuseRecruit(screens.RecruitRefusedCampaigns, &c, snap)
		r.view.Max, r.view.Back = h.rules.MaxCampaigns, back
		return r
	}
	m := h.market(snap, def)
	var anybody int64
	for _, ci := range cities {
		for level := camp.MinLevel; level <= def.MaxLevel(); level++ {
			anybody += m.capacity(ci, camp.Skill, level)
		}
	}
	if anybody == 0 {
		r := refuseRecruit(screens.RecruitRefusedNoSkill, &c, snap)
		r.view.Back = append(back, screens.RecruitSectionSkill)
		return r
	}
	now := m.now
	total := def.AdFee * int64(len(cities))
	b, acct, err := companyBooks(ctx, tx, c)
	if err != nil {
		return err
	}
	if b.Available().Minor() < total {
		r := refuseRecruit(screens.RecruitRefusedFunds, &c, snap)
		r.view.Need, r.view.Have, r.view.Back = total, b.Available().Minor(), back
		return r
	}
	for _, ci := range cities {
		fee := application.RecruitAdFee{CampaignID: camp.ID, CityID: ci.ID, Amount: def.AdFee, PaidAt: now}
		if def.AdFee > 0 {
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, ci.ID)
			if err != nil {
				return err
			}
			if fee.LedgerTransactionID, err = postRef(ctx, tx.Ledger(), application.ReasonRecruitmentAd,
				application.RecruitCampaignReference, camp.ID, acct.ID, treasury.ID, moneyOf(def.AdFee), now); err != nil {
				return err
			}
		}
		if err := tx.Recruitment().RecordAdFee(ctx, fee); err != nil {
			return err
		}
		if def.Announce {
			if err := appendCompanyEvent(ctx, tx, meta, "recruit_ad", c.ID, map[string]any{
				"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode, "city_id": ci.ID,
				"skill": camp.Skill, "level": camp.MinLevel,
			}); err != nil {
				return err
			}
		}
	}
	camp.Status, camp.PostedAt, camp.AdFee, camp.ChecksDone, camp.ChecksTotal = application.CampaignRunning, &now, total, 0, h.rules.Checks
	camp.Cities = codesOf(cities)
	if err := h.scheduleCheck(ctx, tx, camp, now); err != nil {
		return err
	}
	camp.UpdatedAt = now
	return tx.Recruitment().SaveCampaign(ctx, *camp)
}

// codesOf are cities' codes.
func codesOf(cities []world.City) []string {
	out := make([]string, 0, len(cities))
	for _, c := range cities {
		out = append(out, c.Code)
	}
	return out
}

// RecruitCheckPayload is the jsonb a campaign's check carries.
type RecruitCheckPayload struct {
	CampaignID string `json:"campaign_id"`
	CheckNo    int    `json:"check_no"`
}

// scheduleCheck puts a running campaign's next check on the game clock.
func (h *RecruitHandler) scheduleCheck(ctx context.Context, tx application.Tx, camp *application.RecruitCampaign, now time.Time) error {
	at := now.Add(h.scale.RealWait(h.rules.CheckEvery))
	payload, err := jsonOf(RecruitCheckPayload{CampaignID: camp.ID, CheckNo: camp.ChecksDone + 1})
	if err != nil {
		return err
	}
	id := h.ids.NewID()
	if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: id, ActionType: application.RecruitCheckActionType,
		ActorType: "system", ReferenceType: application.RecruitCampaignReference, ReferenceID: camp.ID, Payload: payload,
		StartedAt: now, FinishAt: at}); err != nil {
		return err
	}
	camp.ActionID, camp.NextCheckAt = id, &at
	return nil
}
