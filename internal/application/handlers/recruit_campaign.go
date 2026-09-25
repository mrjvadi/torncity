package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// A posted campaign: its candidates, hiring one (the signing bonus and the
// move paid once, the specialist taken out of their city's pool), turning
// one down, and stopping the campaign.

// campaignView builds a campaign's screen.
func (h *RecruitHandler) campaignView(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	c application.Company, camp application.RecruitCampaign,
) (screens.RecruitCampaignView, error) {
	line, err := h.campaignLine(ctx, tx, camp)
	if err != nil {
		return screens.RecruitCampaignView{}, err
	}
	v := screens.RecruitCampaignView{Ref: companyRef(snap, c), Line: line, ChecksLeft: camp.ChecksTotal - camp.ChecksDone,
		Offer: screens.RecruitOffer{Salary: camp.Salary, Housing: camp.Housing, Signing: camp.Signing,
			Relocation: camp.Relocation, Term: camp.TermPeriods, Shares: camp.Shares}, AdFee: camp.AdFee,
		Auto: camp.AutoAccept}
	for _, ci := range sortedCities(snap, camp.Cities) {
		v.Cities = append(v.Cities, named(ci.Code, ci.Name))
	}
	cands, err := tx.Recruitment().Candidates(ctx, camp.ID)
	if err != nil {
		return v, err
	}
	own, _ := cityOfCompany(snap, c)
	country, err := tx.Diplomacy().CountryOfCity(ctx, own.ID)
	if err != nil {
		return v, err
	}
	now := h.now()
	for _, cd := range cands {
		home, _ := snap.CityByID(cd.HomeCityID)
		l := screens.RecruitCandidateLine{No: cd.No, NameSeed: cd.NameSeed, Skill: cd.Skill, Level: cd.Level,
			Home: named(home.Code, home.Name), Expected: cd.Expected, Status: cd.Status, ExpiresAt: cd.ExpiresAt,
			Cost: camp.Signing + min(camp.Relocation, cd.MoveCost)}
		if cd.Status == application.CandidatePending && !now.Before(cd.ExpiresAt) {
			// They waited as long as they would: gone, whether or not the
			// row has been told.
			l.Status = application.CandidateExpired
		}
		if home.ID != own.ID && country != "" {
			hc, err := tx.Diplomacy().CountryOfCity(ctx, home.ID)
			if err != nil {
				return v, err
			}
			l.Abroad = hc != country
		}
		v.Candidates = append(v.Candidates, l)
	}
	b, _, err := companyBooks(ctx, tx, c)
	if err != nil {
		return v, err
	}
	v.Available = b.Available().Minor()
	return v, nil
}

// Campaign handles company.rcamp: a campaign and its candidates; a draft
// opens in the builder.
func (h *RecruitHandler) Campaign(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	return h.draftWith(ctx, meta, req, false)
}

// Cancel handles company.rcancel: without confirmation, the question; with
// it, the campaign stopped — its advertising fee is not refunded, its
// waiting candidates step aside, and its scheduled check finds it stopped.
func (h *RecruitHandler) Cancel(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
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
		camp, c, err := h.campaignOf(ctx, tx, snap, p, no, req.confirmed())
		if err != nil {
			return err
		}
		if !req.confirmed() {
			view, err = h.campaignView(ctx, tx, snap, *c, *camp)
			view.ConfirmCancel = camp.Open()
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if fresh && camp.Open() {
			now := h.now()
			if _, err := tx.Recruitment().Withdraw(ctx, camp.ID, now); err != nil {
				return err
			}
			camp.Status, camp.EndedAt, camp.ActionID, camp.NextCheckAt, camp.UpdatedAt = application.CampaignCancelled,
				&now, "", nil, now
			if err := tx.Recruitment().SaveCampaign(ctx, *camp); err != nil {
				return err
			}
			view, err = h.campaignView(ctx, tx, snap, *c, *camp)
			view.Notice = "cancelled"
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

// Decide handles company.rdecide: hire a candidate, or turn one down.
func (h *RecruitHandler) Decide(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := req.number()
	if !ok {
		return nil, errNoNumber
	}
	verdict := strings.TrimSpace(req.Verdict)
	if verdict != screens.RecruitHire && verdict != screens.RecruitReject {
		return nil, invalidRecruit("a verdict is yes or no")
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
		cand, err := tx.Recruitment().Candidate(ctx, no, false)
		if isSentinel(err, application.ErrCandidateNotFound) {
			return refuseRecruit(screens.RecruitRefusedNotFound, nil, snap)
		}
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, cand.CompanyID, "")
		if err != nil {
			return err
		}
		camp, err := tx.Recruitment().CampaignByID(ctx, cand.CampaignID, true)
		if err != nil {
			return err
		}
		if cand, err = tx.Recruitment().Candidate(ctx, no, true); err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		notice := ""
		if fresh {
			now := h.now()
			if verdict == screens.RecruitReject {
				if ok, err := tx.Recruitment().Decide(ctx, cand.ID, application.CandidateRejected, p.ID, now); err != nil {
					return err
				} else if ok {
					notice = "rejected"
				}
			} else {
				if _, err := h.hire(ctx, tx, meta, snap, def, *c, camp, cand, p.ID, now); err != nil {
					return err
				}
				notice = "hired"
			}
		}
		view, err = h.campaignView(ctx, tx, snap, *c, *camp)
		view.Notice, view.NoticeSeed = notice, cand.NameSeed
		return err
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	return screens.RecruitCampaign(h.screen(meta, lang), view), nil
}

// hire makes a pending candidate a specialist of the company: under the
// company's and the campaign's locks, it takes them out of their city's
// pool, pays their signing bonus and their move from the company's free
// money, and fills a position of the campaign — which ends it once every
// position is filled. A candidate no longer there (answered, waited too
// long, or taken by another company) is refused.
func (h *RecruitHandler) hire(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	def content.RecruitmentDef, c application.Company, camp *application.RecruitCampaign, cand *application.RecruitCandidate,
	by string, now time.Time,
) (application.NPCStaff, error) {
	back := []string{screens.AddrRecruitCamp, itoa64(camp.No)}
	gone := func() error {
		r := refuseRecruit(screens.RecruitRefusedGone, &c, snap)
		r.view.Back, r.view.NameSeed = back, cand.NameSeed
		return r
	}
	if cand.Status != application.CandidatePending || !now.Before(cand.ExpiresAt) {
		return application.NPCStaff{}, gone()
	}
	if camp.Hired >= camp.Positions {
		r := refuseRecruit(screens.RecruitRefusedFilled, &c, snap)
		r.view.Back = back
		return application.NPCStaff{}, r
	}
	staff, err := tx.Recruitment().Staff(ctx, c.ID)
	if err != nil {
		return application.NPCStaff{}, err
	}
	if len(staff) >= h.rules.MaxStaff {
		r := refuseRecruit(screens.RecruitRefusedStaff, &c, snap)
		r.view.Max, r.view.Back = h.rules.MaxStaff, back
		return application.NPCStaff{}, r
	}
	home, ok := snap.CityByID(cand.HomeCityID)
	if !ok {
		return application.NPCStaff{}, gone()
	}
	m := jobMarket{def: def, snap: snap, scale: h.scale, now: now}
	pool, _, err := m.pool(ctx, tx, home, cand.Skill, cand.Level, true)
	if err != nil {
		return application.NPCStaff{}, err
	}
	if pool.Available < 1 {
		return application.NPCStaff{}, gone()
	}
	signing, relocation := camp.Signing, min(camp.Relocation, cand.MoveCost)
	b, _, err := companyBooks(ctx, tx, c)
	if err != nil {
		return application.NPCStaff{}, err
	}
	if b.Available().Minor() < signing+relocation {
		r := refuseRecruit(screens.RecruitRefusedFunds, &c, snap)
		r.view.Need, r.view.Have, r.view.Back = signing+relocation, b.Available().Minor(), back
		return application.NPCStaff{}, r
	}
	id := h.ids.NewID()
	for _, pay := range []struct {
		reason application.Reason
		amount int64
	}{{application.ReasonSpecialistSigning, signing}, {application.ReasonSpecialistRelocation, relocation}} {
		if _, err := spendFree(ctx, tx, c, snap, pay.reason, application.NPCStaffReference, id,
			application.SystemSinkAccountID, pay.amount, now); err != nil {
			return application.NPCStaff{}, err
		}
	}
	pool.Available--
	if err := tx.Recruitment().SavePool(ctx, pool); err != nil {
		return application.NPCStaff{}, err
	}
	if ok, err := tx.Recruitment().Decide(ctx, cand.ID, application.CandidateHired, by, now); err != nil {
		return application.NPCStaff{}, err
	} else if !ok {
		return application.NPCStaff{}, gone()
	}
	cand.Status = application.CandidateHired
	s, err := tx.Recruitment().Hire(ctx, application.NPCStaff{ID: id, CompanyID: c.ID, CandidateID: cand.ID,
		CampaignID: camp.ID, HomeCityID: cand.HomeCityID, Skill: cand.Skill, Level: cand.Level,
		Preference: cand.Preference, NameSeed: cand.NameSeed, Salary: camp.Salary, Housing: camp.Housing,
		AcceptedBPS: recruit.RatioBPS(camp.Salary+camp.Housing, cand.Expected), TermPeriods: camp.TermPeriods,
		Shares: camp.Shares, SigningPaid: signing, RelocationPaid: relocation, HiredAt: now})
	if err != nil {
		return s, err
	}
	camp.Hired++
	if camp.Hired >= camp.Positions {
		if _, err := tx.Recruitment().Withdraw(ctx, camp.ID, now); err != nil {
			return s, err
		}
		camp.Status, camp.EndedAt, camp.ActionID, camp.NextCheckAt = application.CampaignFilled, &now, "", nil
		if err := appendCompanyEvent(ctx, tx, meta, "recruit", c.ID, recruitEvent(c, screens.RecruitNoticeFilled,
			map[string]any{"campaign_no": camp.No})); err != nil {
			return s, err
		}
	}
	camp.UpdatedAt = now
	return s, tx.Recruitment().SaveCampaign(ctx, *camp)
}

// recruitEvent is the payload of a recruitment notice to a company's owner.
func recruitEvent(c application.Company, kind string, more map[string]any) map[string]any {
	out := map[string]any{"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode,
		"owner_id": c.OwnerID, "city_id": c.CityID, "kind": kind}
	for k, v := range more {
		out[k] = v
	}
	return out
}
