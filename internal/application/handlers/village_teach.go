package handlers

import (
	"context"
	"github.com/mrjvadi/torncity/internal/presentation"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// This file holds ADR 0031 section 4.4's literacy diffusion: a settlement's
// school teaching its own population over time, run as its own recurring,
// self-rescheduling game_action — settlement_teach — rather than depending
// on a broader settlement_period system (ADR 0028 section 8.5's own
// exactly-once shape) that does not exist in code yet. Each firing applies
// one diffusion step and schedules its own successor in the same
// transaction, fenced by settlement_literacy.pending_action_id so a
// redelivered completion cannot double-apply or double-schedule (see
// migration 0046's own comment on why the fence lives there).
//
// V1 SIMPLIFICATIONS, documented here rather than silently assumed:
//   - teacher_skill_bps (ADR 0031 section 4.4's own formula term, "the
//     best-skilled resident's relevant skill") is a fixed constant, not a
//     live lookup of the settlement's leading office holder's skill —
//     wiring a real skill read is a small, separate follow-up once a
//     settlement-scale "who teaches" concept exists.
//   - school_capacity_factor is binary: baseSchoolCapacityBPS while ANY
//     complete education-role building stands (teaching_circle or school),
//     zero otherwise — "the school's real job before city tier" (ADR 0031
//     section 2 point 2) taken literally: no school standing, no diffusion,
//     exactly right for a two-building village with nobody teaching yet.
//   - RefreshHolderCounts is piggybacked onto this tick rather than run by
//     its own singleton scheduler primitive: every settlement's own teach
//     tick refreshes the SAME shared, idempotent aggregate
//     (settlement_knowledge_holder_counts), which costs a little redundant
//     work across many settlements but needs no leader-election machinery
//     this codebase does not otherwise have for a bare periodic job.

// TeacherRules are the teacher's skill in the literacy tick (settlement.teacher_*, docs/adr/0057): the literacy a class moves
// follows its teacher. Zero rules keep the old fixed 6000.
type TeacherRules struct {
	BaseBPS, PerLevelBPS, XPPerClass int64
}

// fixedTeacherSkillBPS is what a settlement with no teacher rules teaches at.
const fixedTeacherSkillBPS = 6_000

// WithTeacherRules gives the village handler the teacher's skill rules.
func (h *VillageHandler) WithTeacherRules(r TeacherRules) *VillageHandler {
	h.teacherRules = r
	return h
}

// teacherSkillOf is the skill the settlement's literacy classes teach at now: the best of the active teachers of the
// literacy class (a player's teaching level lifts it, an NPC teaches at the base), the base when nobody teaches.
func (h *VillageHandler) teacherSkillOf(ctx context.Context, tx application.Tx, settlementID string) (int64, error) {
	r := h.teacherRules
	if r.BaseBPS <= 0 {
		return fixedTeacherSkillBPS, nil
	}
	teachers, err := tx.Education().SettlementTeachers(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	best := r.BaseBPS
	for _, t := range teachers {
		if t.CourseCode != literacyCourse || t.Kind == application.TeacherNPC || t.PlayerID == "" {
			continue
		}
		sk, err := tx.Skills().Get(ctx, t.PlayerID, "teaching")
		if err != nil {
			if apperrors.CodeOf(err) == apperrors.CodeNotFound {
				continue
			}
			return 0, err
		}
		best = max(best, min(10_000, r.BaseBPS+r.PerLevelBPS*int64(sk.Level)))
	}
	return best, nil
}

// EnsureTeaching schedules a settlement's first literacy tick if none is
// pending yet. Idempotent: called again on a settlement that already has
// one scheduled, it does nothing.
func (h *VillageHandler) EnsureTeaching(ctx context.Context, tx application.Tx, settlementID string, now time.Time) error {
	return ensureSettlementTeaching(ctx, tx, h.ids, h.scale, h.teachPeriod, settlementID, now)
}

// ensureSettlementTeaching is EnsureTeaching's own body, standalone so
// SettlementsHandler.Found can start a freshly founded settlement's first
// literacy tick in the same transaction as founding itself (ADR 0031
// section 4.4), without needing a VillageHandler instance.
func ensureSettlementTeaching(ctx context.Context, tx application.Tx, ids IDGenerator, scale gametimeScale, teachPeriod time.Duration,
	settlementID string, now time.Time,
) error {
	if err := tx.SettlementKnowledge().EnsureLiteracy(ctx, settlementID, now); err != nil {
		return err
	}
	shareBPS, pending, err := tx.SettlementKnowledge().Literacy(ctx, settlementID)
	if err != nil {
		return err
	}
	if pending != "" {
		return nil
	}
	finish := now.Add(scale.RealWait(teachPeriod))
	actionID, err := scheduleVillageAction(ctx, tx, ids, application.SettlementTeachActionType, "settlement", settlementID, settlementID, now, finish)
	if err != nil {
		return err
	}
	return tx.SettlementKnowledge().AdvanceLiteracy(ctx, settlementID, shareBPS, actionID, now)
}

// Taught handles settlement.taught from the SCHEDULER: one literacy
// diffusion step, then its own successor tick.
func (h *VillageHandler) Taught(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		shareBPS, pending, err := tx.SettlementKnowledge().Literacy(ctx, in.SettlementID)
		if err != nil {
			return err
		}
		if req.ActionID != "" && pending != req.ActionID {
			// A later tick already superseded this one; a stale
			// redelivery of an old completion is a no-op.
			return nil
		}

		buildings, err := tx.SettlementBuildings().List(ctx, in.SettlementID)
		if err != nil {
			return err
		}
		capacityBPS := int64(0)
		for _, b := range buildings {
			if !b.Complete() || b.Status == "demolished" {
				continue
			}
			if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok && d.Role == "education" {
				capacityBPS = h.baseSchoolCapacityBPS
				break
			}
		}

		now := h.now()
		teacherSkill, err := h.teacherSkillOf(ctx, tx, in.SettlementID)
		if err != nil {
			return err
		}
		next := settlementknowledge.AdvanceLiteracy(int64(shareBPS), h.teachRateBPS, teacherSkill, capacityBPS)

		finish := now.Add(h.scale.RealWait(h.teachPeriod))
		nextActionID, err := h.schedule(ctx, tx, application.SettlementTeachActionType, "settlement", in.SettlementID, in.SettlementID, now, finish)
		if err != nil {
			return err
		}
		if err := tx.SettlementKnowledge().AdvanceLiteracy(ctx, in.SettlementID, int(next), nextActionID, now); err != nil {
			return err
		}
		if err := tx.SettlementKnowledge().RefreshHolderCounts(ctx, now); err != nil {
			return err
		}
		// The property tax rides the village's own tick (village_citizen.go):
		// one row per owner and period, so a redelivery charges nothing twice.
		if err := h.SettleTax(ctx, tx, in.SettlementID, now); err != nil {
			return err
		}
		// The village shop's morning delivery rides the same tick: one row per
		// settlement and game day (village_shop_days), so a redelivery or a player's
		// first look of the day delivers nothing twice.
		if h.shop.enabled() {
			s, err := tx.Settlements().ByID(ctx, in.SettlementID)
			if err != nil {
				return err
			}
			if _, err := h.SettleShopDay(ctx, tx, s, now); err != nil {
				return err
			}
		}
		// The market day rides the tick too (trade_days: one row per settlement and local day).
		if h.trade.enabled() {
			s, err := tx.Settlements().ByID(ctx, in.SettlementID)
			if err != nil {
				return err
			}
			if _, err := h.SettleTradeDay(ctx, tx, snap, s, buildings); err != nil {
				return err
			}
		}
		// a day the class taught someone to read is practice in education
		if next > int64(shareBPS) && capacityBPS > 0 {
			if s, err := tx.Settlements().ByID(ctx, in.SettlementID); err == nil && h.service.enabled() {
				if err := h.accrueDaily(ctx, tx, in.SettlementID, "education", "teaching", h.service.Clock.DayAtIn(now, s.Zone()), 1, now); err != nil {
					return err
				}
			}
		}
		// and so do the daily services (service_days)
		if h.service.enabled() {
			s, err := tx.Settlements().ByID(ctx, in.SettlementID)
			if err != nil {
				return err
			}
			if _, err := h.SettleServiceDay(ctx, tx, snap, s, buildings); err != nil {
				return err
			}
			// and the day wage of the stall keepers hired by the day (stall_keeper_wages)
			if err := h.SettleKeeperWages(ctx, tx, s, now, meta); err != nil {
				return err
			}
		}
		if next == int64(shareBPS) {
			return nil // no visible change (no school yet); nothing worth announcing
		}
		return appendVillageEvent(ctx, tx, meta, "literacy_advanced", in.SettlementID, map[string]any{
			"settlement_id": in.SettlementID, "literacy_share_bps": next, "previous_bps": shareBPS,
		})
	})
}
