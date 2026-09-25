package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// A campaign's check, from the SCHEDULER (game_actions 'recruit_check'):
// the specialists the campaign reaches weigh its offer, and those who like
// it apply.
//
// Exactly once: the campaign row is locked (after its company) and must
// still be running, name this action, and expect this check; the
// candidates' key (campaign, check, seq) is the backstop, so a repeated or
// stale delivery adds nobody. The rolls are drawn from the campaign's id and
// the check's number, so a replay would roll the same.

// Check handles company.rcheck.
func (h *RecruitHandler) Check(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in RecruitCheckPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("check payload is unreadable").WithCause(err)
		}
	}
	if in.CampaignID == "" {
		in.CampaignID = req.ReferenceID
	}
	if in.CampaignID == "" || in.CheckNo < 1 {
		return nil, errors.InvalidInput("check names no campaign or no check")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		def, err := recruitment(snap)
		if err != nil {
			return err
		}
		camp, err := tx.Recruitment().CampaignByID(ctx, in.CampaignID, false)
		if isSentinel(err, application.ErrCampaignNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		current := func(camp *application.RecruitCampaign) bool {
			return camp.Open() && camp.ChecksDone+1 == in.CheckNo && (req.ActionID == "" || camp.ActionID == req.ActionID)
		}
		if !current(camp) {
			return nil
		}
		now := h.now()
		if camp.NextCheckAt != nil && now.Before(*camp.NextCheckAt) {
			// Early by a clock step: a fault the broker's backoff retries.
			return errors.Internal(stderrors.New("handlers: a campaign's check ran before its time"))
		}
		c, err := tx.Companies().Lock(ctx, camp.CompanyID)
		if err != nil {
			return err
		}
		if camp, err = tx.Recruitment().CampaignByID(ctx, in.CampaignID, true); err != nil {
			return err
		}
		if !current(camp) {
			return nil
		}
		if !c.Active() {
			if _, err := tx.Recruitment().Withdraw(ctx, camp.ID, now); err != nil {
				return err
			}
			camp.Status, camp.EndedAt, camp.ActionID, camp.NextCheckAt, camp.UpdatedAt = application.CampaignCancelled,
				&now, "", nil, now
			return tx.Recruitment().SaveCampaign(ctx, *camp)
		}
		m := jobMarket{def: def, snap: snap, scale: h.scale, now: now}
		found, err := h.evaluate(ctx, tx, m, *c, camp, in.CheckNo)
		if err != nil {
			return err
		}
		camp.ChecksDone = in.CheckNo
		waiting := 0
		for i := range found {
			if !camp.AutoAccept || !camp.Open() {
				waiting++
				continue
			}
			s, err := h.hire(ctx, tx, meta, snap, def, *c, camp, &found[i], "", now)
			var refused *recruitRefusal
			if stderrors.As(err, &refused) {
				// Not now — no money, no room: they wait for the owner.
				waiting++
				continue
			}
			if err != nil {
				return err
			}
			if err := appendCompanyEvent(ctx, tx, meta, "recruit", c.ID, recruitEvent(*c, screens.RecruitNoticeHired,
				map[string]any{"campaign_no": camp.No, "name_seed": s.NameSeed, "skill": s.Skill, "level": s.Level})); err != nil {
				return err
			}
		}
		if waiting > 0 {
			if err := appendCompanyEvent(ctx, tx, meta, "recruit", c.ID, recruitEvent(*c, screens.RecruitNoticeApplied,
				map[string]any{"campaign_no": camp.No, "count": waiting})); err != nil {
				return err
			}
		}
		if camp.Open() {
			if camp.ChecksDone >= camp.ChecksTotal {
				camp.Status, camp.EndedAt, camp.ActionID, camp.NextCheckAt = application.CampaignEnded, &now, "", nil
				if err := appendCompanyEvent(ctx, tx, meta, "recruit", c.ID, recruitEvent(*c, screens.RecruitNoticeEnded,
					map[string]any{"campaign_no": camp.No})); err != nil {
					return err
				}
			} else if err := h.scheduleCheck(ctx, tx, camp, now); err != nil {
				return err
			}
		}
		camp.UpdatedAt = now
		return tx.Recruitment().SaveCampaign(ctx, *camp)
	})
}

// evaluate is one check of a campaign. Every pool of its cities at its
// levels is locked and refilled (in city, then level, order: the one order
// every check and hire takes them in); of each, the specialists not already
// waiting on an answer — up to the content's reach — weigh the offer by
// their preference and apply with the chance the rules give. Of those who
// apply, a rolled order picks the operator's candidates per check, so no
// city or level crowds out the rest.
func (h *RecruitHandler) evaluate(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
	camp *application.RecruitCampaign, checkNo int,
) ([]application.RecruitCandidate, error) {
	own, ok := cityOfCompany(m.snap, c)
	if !ok {
		return nil, nil
	}
	country, err := tx.Diplomacy().CountryOfCity(ctx, own.ID)
	if err != nil {
		return nil, err
	}
	appeal, err := m.appeal(ctx, tx, c, own, country)
	if err != nil {
		return nil, err
	}
	equity, err := shareValue(ctx, tx, c, camp.Shares)
	if err != nil {
		return nil, err
	}
	offer, curve, weights := offerOf(*camp, equity), m.def.Acceptance.Curve(), m.weights()
	patience := m.now.Add(h.scale.RealWait(h.rules.Patience))
	type applicant struct {
		cand application.RecruitCandidate
		rank int64
	}
	var applied []applicant
	for ci, home := range sortedCities(m.snap, camp.Cities) {
		o, err := m.originOf(ctx, tx, home, own, country)
		if err != nil {
			return nil, err
		}
		if o.blocked {
			continue
		}
		move := m.moveCost(o)
		for level := camp.MinLevel; level <= m.def.MaxLevel(); level++ {
			if m.capacity(home, camp.Skill, level) == 0 {
				continue
			}
			pool, capacity, err := m.pool(ctx, tx, home, camp.Skill, level, true)
			if err != nil {
				return nil, err
			}
			pending, err := tx.Recruitment().Pending(ctx, home.ID, camp.Skill, level, m.now)
			if err != nil {
				return nil, err
			}
			expected := m.expected(camp.Skill, level, own, pool.Available, capacity)
			reach := min(pool.Available-pending, int64(m.def.Reach))
			for i := int64(0); i < reach; i++ {
				at := []int64{int64(checkNo), int64(ci), int64(level), i}
				pref := m.def.Preferences[recruit.Pick(weights, recruit.Roll(camp.ID, append(at, 1)...))]
				value := recruit.Value(offer, pref.Rule(), move)
				chance := min(recruit.Scale(curve.Chance(value, expected), m.placeBPS(o), appeal), recruit.BPS)
				if recruit.Roll(camp.ID, append(at, 2)...) >= chance {
					continue
				}
				applied = append(applied, applicant{rank: recruit.Roll(camp.ID, append(at, 4)...),
					cand: application.RecruitCandidate{CampaignID: camp.ID, CompanyID: c.ID, HomeCityID: home.ID,
						Skill: camp.Skill, Level: level, Preference: pref.Code,
						NameSeed: int(recruit.Roll(camp.ID, append(at, 3)...)), Expected: expected, MoveCost: move,
						Value: value, ChanceBPS: int(chance), CheckNo: checkNo, AppliedAt: m.now, ExpiresAt: patience}})
			}
		}
	}
	sort.SliceStable(applied, func(i, j int) bool { return applied[i].rank < applied[j].rank })
	var out []application.RecruitCandidate
	for i := 0; i < len(applied) && i < h.rules.MaxCandidates; i++ {
		cand := applied[i].cand
		cand.ID, cand.Seq = h.ids.NewID(), i+1
		added, ok, err := tx.Recruitment().AddCandidate(ctx, cand)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, added)
		}
	}
	return out, nil
}

// recruitmentOf is the content's recruitment for a caller that may run
// without it (the settlement): false when there is none.
func recruitmentOf(snap *content.Snapshot) (content.RecruitmentDef, bool) { return snap.Recruitment() }
