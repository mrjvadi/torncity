package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/intel"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Intelligence between settlements (docs/adr/0077 5 and 6). A model of an institution, not a manual: roles, chances, costs and consequences.
//
//   - WHO WORKS THERE. The bureau's analyst and handler (the service day's staff, paid by the treasury) and the agents: players who live and
//     work in the target settlement, so their cover is a real residence, and who do an operation a day for the bureau's fee and their wage.
//   - WHAT IT CONSUMES. The bureau's paper (the service day), the operation's fee and the agent's wage from the sponsor's treasury, a day's
//     limit of operations, and for sabotage the cost of the act.
//   - WHAT IT PROVIDES. A banded report (never a figure, never a person) with a confidence and an age, or a small, capped act of sabotage.
//   - WHAT BREAKS. A bureau closed for the day (no staff, no wage, no paper) sends nobody out; a blown operation tests the target's watch and may
//     put the agent out of the settlement; the sponsor is named only if the trace succeeds.

// VillageIntelRequest is the payload of settlement.intel.
type VillageIntelRequest struct {
	// Action is "" (the screen) or one of village.IntelAction*.
	Action string `json:"action,omitempty"`
	// ID names an agent, an offer or a report; Code an operation or a settlement (engage, share); Player a public code of a player.
	ID     string `json:"id,omitempty"`
	Code   string `json:"code,omitempty"`
	Player string `json:"player,omitempty"`
	// Ref is what an act of sabotage is about: a project, an item or a building of the target. N is the probation days of a vetting policy.
	Ref string `json:"ref,omitempty"`
	N   string `json:"n,omitempty"`
}

func (h *VillageHandler) intelRules() intel.Rules {
	t := h.special.Intel
	return intel.Rules{BaseChanceBPS: t.BaseChanceBPS, SkillBPSPerLevel: t.SkillBPSPerLevel, ChanceFloorBPS: t.ChanceFloorBPS, ChanceCapBPS: t.ChanceCapBPS,
		CoverCapDays: t.CoverCapDays, CoverBPSPerDay: t.CoverBPSPerDay, CounterPostBPS: t.CounterPostBPS, CounterVetBPS: t.CounterVetBPS, CounterSmallBPS: t.CounterSmallBPS,
		SmallResidents: t.SmallResidents, DetectBPS: t.DetectBPS, AttributionBPS: t.AttributionBPS, ExpelDays: t.ExpelDays, ReportTTL: t.ReportTTL}
}

func (h *VillageHandler) sabotageRules() intel.SabotageRules {
	t := h.special.Intel
	return intel.SabotageRules{DelayHours: t.SabotageDelayHours, SpoilBPS: t.SabotageSpoilBPS, BlindBPS: t.SabotageBlindBPS, WeeklyLimit: t.SabotageWeeklyLimit,
		PerTargetDays: t.SabotagePerTargetDays, SkipDays: t.SabotageSkipDays, Cost: t.SabotageCost}
}

// Intel handles settlement.intel.
func (h *VillageHandler) Intel(ctx context.Context, meta envelope.Metadata, req VillageIntelRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.IntelView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, has, err := h.settlementOpt(ctx, tx, meta)
		if err != nil {
			return err
		}
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" && act != village.IntelActionOpen {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.intelAct(ctx, tx, meta, snap, s, has, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.intelView(ctx, tx, snap, s, has, p)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Intel(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) intelAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool,
	p *application.Player, act string, req VillageIntelRequest,
) (string, error) {
	if !h.intelOpen() {
		return "", refuseVillage(village.IntelNotYet, village.AddrIntel)
	}
	now := h.now()
	switch act {
	case village.IntelActionEngage:
		return village.IntelNoticeEngaged, h.intelEngage(ctx, tx, snap, s, has, p, req, now)
	case village.IntelActionAccept:
		return village.IntelNoticeAccepted, h.intelAnswer(ctx, tx, p, req, true, now)
	case village.IntelActionRefuse:
		return village.IntelNoticeRefused, h.intelAnswer(ctx, tx, p, req, false, now)
	case village.IntelActionRecall:
		return village.IntelNoticeRecalled, h.intelRecall(ctx, tx, s, has, p, req, now)
	case village.IntelActionOp:
		return h.intelOperate(ctx, tx, meta, snap, p, req, false, now)
	case village.IntelActionSab:
		return h.intelOperate(ctx, tx, meta, snap, p, req, true, now)
	case village.IntelActionShare:
		return village.IntelNoticeShared, h.intelShare(ctx, tx, s, has, p, req, now)
	case village.IntelActionVet:
		return village.IntelNoticeVetting, h.intelVet(ctx, tx, s, has, p, req, now)
	}
	return "", refuseVillage(village.IntelBadArgs, village.AddrIntel)
}

// bureauOf is the standing bureau of a settlement, nil for none.
func bureauOf(buildings []application.SettlementBuildingInstance) *application.SettlementBuildingInstance {
	for i := range buildings {
		if buildings[i].TypeCode == "intelligence_office" && buildings[i].Status == "complete" {
			return &buildings[i]
		}
	}
	return nil
}

func (h *VillageHandler) intelEngage(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageIntelRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.IntelRun); err != nil {
		return err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return err
	}
	if bureauOf(buildings) == nil {
		return refuseVillage(village.IntelNoBureau, village.AddrIntel)
	}
	target, err := h.settlementByCode(ctx, tx, strings.TrimSpace(req.Code))
	if err != nil {
		return err
	}
	if target == nil {
		return refuseVillage(village.IntelNoTarget, village.AddrIntel)
	}
	if target.CityID == s.CityID {
		return refuseVillage(village.IntelSelf, village.AddrIntel)
	}
	pid, ok, err := tx.Unions().PlayerByCode(ctx, strings.TrimSpace(req.Player))
	if err != nil {
		return err
	}
	if !ok {
		return refuseVillage(village.IntelNoPlayer, village.AddrIntel)
	}
	if open, err := tx.Intel().OpenAgent(ctx, pid, target.CityID); err != nil {
		return err
	} else if open != nil {
		return refuseVillage(village.IntelOpenAgent, village.AddrIntel)
	}
	if fresh, err := tx.Intel().InsertAgent(ctx, application.IntelAgent{ID: h.ids.NewID(), PlayerID: pid, SponsorID: s.CityID, TargetID: target.CityID, OfferedBy: p.ID, CreatedAt: now}); err != nil {
		return err
	} else if !fresh {
		return refuseVillage(village.IntelOpenAgent, village.AddrIntel)
	}
	return nil
}

// intelAnswer is the player's answer to an offer. The cover begins with the acceptance.
func (h *VillageHandler) intelAnswer(ctx context.Context, tx application.Tx, p *application.Player, req VillageIntelRequest, accept bool, now time.Time) error {
	a, err := tx.Intel().AgentByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if a == nil || a.PlayerID != p.ID || a.Status != "offered" {
		return refuseVillage(village.IntelNoOffer, village.AddrIntel)
	}
	if accept {
		_, err = tx.Intel().Activate(ctx, a.ID, now)
		return err
	}
	_, err = tx.Intel().Close(ctx, a.ID, "declined", now)
	return err
}

func (h *VillageHandler) intelRecall(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, req VillageIntelRequest, now time.Time) error {
	a, err := tx.Intel().AgentByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if a == nil || (a.Status != "active" && a.Status != "offered") {
		return refuseVillage(village.IntelNoAgent, village.AddrIntel)
	}
	if a.PlayerID != p.ID {
		if !has || a.SponsorID != s.CityID {
			return refuseVillage(village.IntelNoAgent, village.AddrIntel)
		}
		if _, err := requirePermission(ctx, tx, s, p.ID, charter.IntelRun); err != nil {
			return err
		}
	}
	_, err = tx.Intel().Close(ctx, a.ID, "recalled", now)
	return err
}

// detectorServices are the services whose posts, held, notice a stranger: the watch, the lookouts and the beacons.
var detectorServices = map[string]bool{"local_security": true, "claim_and_sight": true, "relay": true, "garrison": true}

// watchPosts are the target's posts that were open on its last judged day and watch for strangers.
func (h *VillageHandler) watchPosts(ctx context.Context, tx application.Tx, snap *content.Snapshot, target application.FoundedSettlement) ([]application.ServicePost, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, target.CityID)
	if err != nil {
		return nil, err
	}
	day, err := h.SettleServiceDay(ctx, tx, snap, target, buildings)
	if err != nil || day == nil {
		return nil, err
	}
	var out []application.ServicePost
	for _, p := range day.Posts {
		if p.Held && detectorServices[p.Service] {
			out = append(out, p)
		}
	}
	return out, nil
}

// intelOperate runs one operation (a survey, or an act of sabotage) of an agent on the target. One row for each agent, target, kind and day is the
// fence; the roll is seeded by those, so a replay finds the same outcome.
func (h *VillageHandler) intelOperate(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, p *application.Player, req VillageIntelRequest, sabotage bool, now time.Time) (string, error) {
	def, ok := snap.IntelOp(strings.TrimSpace(req.Code))
	if !ok || (def.Kind == content.IntelSabotage) != sabotage {
		return "", refuseVillage(village.IntelNoOp, village.AddrIntel)
	}
	if sabotage && !h.sabotageOpen() {
		return "", refuseVillage(village.IntelSabNotYet, village.AddrIntel)
	}
	a, err := tx.Intel().AgentByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return "", err
	}
	if a == nil || a.PlayerID != p.ID || a.Status != "active" || a.CoverSince == nil {
		return "", refuseVillage(village.IntelNoAgent, village.AddrIntel)
	}
	sponsor, err := tx.Settlements().ByID(ctx, a.SponsorID)
	if err != nil {
		return "", err
	}
	target, err := tx.Settlements().ByID(ctx, a.TargetID)
	if err != nil {
		return "", err
	}
	if missing, err := knowledgeMissing(ctx, tx, sponsor.CityID, needsKnowledge(def.Requires)); err != nil {
		return "", err
	} else if len(missing) > 0 {
		return "", researchRefusal(village.IntelResearch, village.AddrIntel, missing, snap)
	}
	// the cover is real: the agent lives in the target now, and has not been put out of it
	if home, err := tx.Employment().ResidenceCityID(ctx, p.ID); err != nil {
		return "", err
	} else if home != target.CityID {
		return "", refuseVillage(village.IntelNoCover, village.AddrIntel)
	}
	if until, err := tx.Intel().ExpelledUntil(ctx, p.ID, target.CityID); err != nil {
		return "", err
	} else if until.After(now) {
		return "", refuseVillage(village.IntelExpelled, village.AddrIntel)
	}
	// the bureau stands and is open today
	buildings, err := tx.SettlementBuildings().List(ctx, sponsor.CityID)
	if err != nil {
		return "", err
	}
	bureau := bureauOf(buildings)
	if bureau == nil {
		return "", refuseVillage(village.IntelNoBureau, village.AddrIntel)
	}
	if day, err := h.SettleServiceDay(ctx, tx, snap, sponsor, buildings); err != nil {
		return "", err
	} else if day == nil || !day.HeldPost(bureau.ID) {
		return "", refuseVillage(village.IntelBureauClosed, village.AddrIntel)
	}
	tune := h.special.Intel
	dayNo := gameDayNumber(now)
	if n, err := tx.Intel().OpsToday(ctx, a.ID, dayNo); err != nil {
		return "", err
	} else if n >= tune.OpsPerDay {
		return "", refuseVillage(village.IntelDayLimit, village.AddrIntel)
	}
	fee, wage := tune.OpFee, tune.AgentWage
	if sabotage {
		fee = tune.SabotageCost
	}
	if bal, err := treasuryBalance(ctx, tx, sponsor.CityID); err != nil {
		return "", err
	} else if bal < fee+wage {
		return "", refuseVillage(village.IntelNoMoney, village.AddrIntel)
	}
	// sabotage is refused before it is tried when the rules forbid it
	var act *sabotageTry
	if sabotage {
		if act, err = h.sabotageAim(ctx, tx, snap, sponsor, target, a, def.Code, strings.TrimSpace(req.Ref), now); err != nil {
			return "", err
		}
	}
	rules := h.intelRules()
	skill := int64(0)
	if sk, err := tx.Skills().Get(ctx, p.ID, "fieldwork"); err == nil && sk != nil {
		skill = int64(sk.Level)
	}
	posts, err := h.watchPosts(ctx, tx, snap, target)
	if err != nil {
		return "", err
	}
	vet, err := tx.Intel().Vetting(ctx, target.CityID)
	if err != nil {
		return "", err
	}
	residents, err := tx.Settlements().ResidentCount(ctx, target.CityID)
	if err != nil {
		return "", err
	}
	coverDays := int64(now.Sub(*a.CoverSince) / (24 * time.Hour))
	counter := rules.Counter(int64(len(posts)), vet > 0, residents)
	chance := min(max(rules.Chance(skill, coverDays, counter)+def.ChanceBPS, rules.ChanceFloorBPS), rules.ChanceCapBPS)
	label := fmt.Sprintf("op:%s:%s:%s:%d", a.ID, target.CityID, def.Code, dayNo)
	roll := intel.Roll(label)
	outcome, conf := intel.Resolve(chance, roll)
	op := application.IntelOp{ID: h.ids.NewID(), AgentID: a.ID, SponsorID: sponsor.CityID, TargetID: target.CityID, Kind: def.Code, Day: dayNo, ChanceBPS: int(chance), Roll: int(roll),
		Outcome: string(outcome), Fee: fee, Wage: wage, At: now}
	if fresh, err := tx.Intel().InsertOp(ctx, op); err != nil {
		return "", err
	} else if !fresh {
		return "", refuseVillage(village.IntelDone, village.AddrIntel)
	}
	// the fee and the wage are paid whatever came of the day
	treasury, err := cityTreasury(ctx, tx, sponsor.CityID)
	if err != nil {
		return "", err
	}
	if fee > 0 {
		reason := application.ReasonIntelOpFee
		if sabotage {
			reason = application.ReasonSabotageCost
		}
		if _, err := move(ctx, tx, treasury.ID, application.SystemSinkAccountID, fee, reason, application.IntelReference, op.ID, "", now); err != nil {
			return "", err
		}
	}
	if wage > 0 {
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
		if err != nil {
			return "", err
		}
		if _, err := move(ctx, tx, treasury.ID, cash.ID, wage, application.ReasonIntelAgentWage, application.IntelReference, op.ID, "", now); err != nil {
			return "", err
		}
	}
	if err := h.agentTrains(ctx, tx, snap, p, now); err != nil {
		return "", err
	}
	if outcome == intel.Blown {
		exposed, err := h.exposureTest(ctx, tx, meta, snap, op, a, sponsor, target, posts, now)
		if err != nil {
			return "", err
		}
		if exposed {
			return village.IntelNoticeExposed, nil
		}
		return village.IntelNoticeBlown, nil
	}
	if sabotage {
		if err := h.sabotageDo(ctx, tx, meta, snap, sponsor, target, a, op, act, now); err != nil {
			return "", err
		}
		return village.IntelNoticeSabotage, nil
	}
	if err := h.surveyReport(ctx, tx, snap, def, op, sponsor, target, string(outcome) == string(intel.Partial), conf, now); err != nil {
		return "", err
	}
	if outcome == intel.Partial {
		return village.IntelNoticePartial, nil
	}
	return village.IntelNoticeClean, nil
}

// agentTrains gives the agent the fieldwork experience of a day's work.
func (h *VillageHandler) agentTrains(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, now time.Time) error {
	xp := h.special.Intel.SkillXPPerOp
	if xp <= 0 {
		return nil
	}
	cur, err := tx.Skills().List(ctx, p.ID)
	if err != nil {
		return err
	}
	_, err = awardSkillXP(ctx, tx, snap, p.ID, cur, []skillAward{{Skill: player.SkillCode("fieldwork"), XP: xp}}, now)
	return err
}

// exposureTest asks each open watch post of the target whether it noticed the blown operation (one seeded roll for each op and post), and puts
// the agent out of the settlement if one did. The sponsor is named to the target only if a further roll traces the agent to it.
func (h *VillageHandler) exposureTest(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, op application.IntelOp, a *application.IntelAgent,
	sponsor, target application.FoundedSettlement, posts []application.ServicePost, now time.Time,
) (bool, error) {
	rules := h.intelRules()
	detected := false
	for _, post := range posts {
		chance := rules.DetectBPS
		if blind, err := tx.Intel().BlindBPS(ctx, post.BuildingID, now); err != nil {
			return false, err
		} else if blind > 0 {
			chance = max(chance-chance*int64(blind)/intel.BPS, 0)
		}
		roll := intel.Roll(fmt.Sprintf("expose:%s:%s", op.ID, post.BuildingID))
		hit := intel.Detected(chance, roll)
		if _, err := tx.Intel().InsertExposure(ctx, application.IntelExposure{OpID: op.ID, DetectorID: post.BuildingID, ChanceBPS: int(chance), Roll: int(roll), Detected: hit, At: now}); err != nil {
			return false, err
		}
		detected = detected || hit
	}
	if !detected {
		return false, nil
	}
	attributed := intel.Detected(rules.AttributionBPS, intel.Roll(fmt.Sprintf("trace:%s", op.ID)))
	expulsionID := h.ids.NewID()
	if _, err := tx.Intel().InsertExpulsion(ctx, application.IntelExpulsion{ID: expulsionID, OpID: op.ID, AgentID: a.ID, PlayerID: a.PlayerID, TargetID: target.CityID, SponsorID: sponsor.CityID,
		Attributed: attributed, Until: rules.Expel(now), At: now}); err != nil {
		return false, err
	}
	// an exposed agent is a charge where the target outlaws espionage and a courthouse stands (docs/adr/0078 3.11)
	if err := h.lawEspionageCase(ctx, tx, target, a.PlayerID, expulsionID, now); err != nil {
		return false, err
	}
	if _, err := tx.Intel().Close(ctx, a.ID, "expelled", now); err != nil {
		return false, err
	}
	// the residence and the post end; nothing the player owns is touched
	if h.homeCityCode != "" {
		if dest, err := h.cities.ByCode(ctx, h.homeCityCode); err == nil && dest != nil {
			if _, err := moveResidence(ctx, tx, meta, a.PlayerID, dest.ID, now, "expelled"); err != nil {
				return false, err
			}
		}
	}
	detail := map[string]any{"agent": a.ID, "attributed": attributed}
	if attributed {
		detail["sponsor"] = sponsor.CityID
	}
	return true, specialEvent(ctx, tx, meta, "intel_exposed", target.CityID, detail)
}
