package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
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

// teacherSkillBPS is the v1 fixed stand-in for "the leader's own skill",
// documented above.
const teacherSkillBPS = 6_000

// EnsureTeaching schedules a settlement's first literacy tick if none is
// pending yet. Idempotent: called again on a settlement that already has
// one scheduled, it does nothing. Called once, from
// SettlementsHandler.Found, in the same transaction as founding itself.
func (h *VillageHandler) EnsureTeaching(ctx context.Context, tx application.Tx, settlementID string, now time.Time) error {
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
	finish := now.Add(h.scale.RealWait(h.teachPeriod))
	actionID, err := h.schedule(ctx, tx, application.SettlementTeachActionType, "settlement", settlementID, settlementID, now, finish)
	if err != nil {
		return err
	}
	return tx.SettlementKnowledge().AdvanceLiteracy(ctx, settlementID, shareBPS, actionID, now)
}

// Taught handles settlement.taught from the SCHEDULER: one literacy
// diffusion step, then its own successor tick.
func (h *VillageHandler) Taught(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
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
		next := settlementknowledge.AdvanceLiteracy(int64(shareBPS), h.teachRateBPS, teacherSkillBPS, capacityBPS)

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
		if next == int64(shareBPS) {
			return nil // no visible change (no school yet); nothing worth announcing
		}
		return appendVillageEvent(ctx, tx, meta, "literacy_advanced", in.SettlementID, map[string]any{
			"settlement_id": in.SettlementID, "literacy_share_bps": next,
		})
	})
}
