package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds K2's two player-triggered acquisitions (ADR 0031 sections
// 4.1, 4.2): starting research and buying from Support at the scarcity
// price. Both are gated by the settlement's own top office
// (authorizeVillage); buying moves money through the ledger under
// ReasonSupplierPurchase, the identical shape a company buying from an NPC
// supplier already uses (ports_ledger.go's own doc comment on that reason).

// VillageKnowledgeRequest names one settlement_knowledge code, and for research the slot to run it in ("" lets the
// settlement take the one that finishes soonest).
type VillageKnowledgeRequest struct {
	Code string `json:"code"`
	Slot string `json:"slot,omitempty"`
}

// knowledgeRefusal maps a settlementknowledge.CanAcquire failure to a
// screen.
func knowledgeRefusal(err error) *villageRefusal {
	switch {
	case stderrors.Is(err, settlementknowledge.ErrAlreadyOwned):
		return refuseVillage(village.VillageAlreadyOwned)
	case stderrors.Is(err, settlementknowledge.ErrNotModeEligible):
		return refuseVillage(village.VillageNotAvailable)
	case stderrors.Is(err, settlementknowledge.ErrTerrainRequired):
		return refuseVillage(village.VillageTerrain)
	case stderrors.Is(err, settlementknowledge.ErrLiteracyTooLow):
		return refuseVillage(village.VillageLiteracy)
	case stderrors.Is(err, settlementknowledge.ErrSkillTooLow):
		return refuseVillage(village.VillagePrerequisite)
	case stderrors.Is(err, settlementknowledge.ErrBusy):
		return refuseVillage(village.VillageBusy)
	case stderrors.Is(err, settlementknowledge.ErrPrerequisiteMissing):
		return refuseVillage(village.VillagePrerequisite)
	default:
		return refuseVillage(village.VillageNotAvailable)
	}
}

// Research handles settlement.knowledge.research: starting to research a
// knowledge item in a free slot (ADR 0048). Its cost leaves the settlement's
// own treasury (a drain, ReasonResearch — the identical shape a company's own
// research already uses); the item is the settlement's once its scheduled
// action runs. The price, the pace and the slot are the quote at this moment
// and are written on the project.
func (h *VillageHandler) Research(ctx context.Context, meta envelope.Metadata, req VillageKnowledgeRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.ResearchStart); err != nil {
			return err
		}
		code := strings.TrimSpace(req.Code)
		d, ok := snap.SettlementKnowledgeDef(code)
		if !ok {
			return refuseVillage(village.VillageNotFound)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		terrain := terrainTagsFor(w, s)
		// One writer of the settlement's research at a time: the capacity is checked under this lock, so two
		// replicas cannot both take the last slot.
		if err := tx.Research().LockSettlement(ctx, s.CityID); err != nil {
			return err
		}
		st, _, err := h.knowledgeStanding(ctx, tx, snap, s, terrain, false)
		if err != nil {
			return err
		}
		tree := snap.SettlementKnowledgeTree()
		t := tree[code]
		if cerr := settlementknowledge.CanAcquire(t, tree, st, true); cerr != nil {
			return h.knowledgeAttempt(snap, st, tree, t, d, cerr, village.AddrKnowledgeList)
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		rc, err := h.researchContext(ctx, tx, snap, s, buildings, int64(st.LiteracyShareBPS))
		if err != nil {
			return err
		}
		for _, r := range rc.state.Running {
			if r.Code == code {
				return refuseVillage(village.VillageBusy)
			}
		}
		partners, err := tx.Research().PartnersHolding(ctx, s.CityID, code)
		if err != nil {
			return err
		}
		var q researchQuote
		if ref := strings.TrimSpace(req.Slot); ref != "" {
			slot, ok := rc.state.slot(ref)
			if !ok || !rc.state.free(slot) {
				return refuseVillage(village.ResearchNoSlot, village.AddrKnowledgeList)
			}
			q = rc.quote(d, t, slot, partners)
		} else if q, ok = rc.best(d, t, partners); !ok {
			return refuseVillage(village.VillageBusy)
		}

		now := h.now()
		id := h.ids.NewID()
		var txID string
		if q.Cost > 0 {
			txID, err = spendVillage(ctx, tx, s.CityID, application.ReasonResearch, q.Cost, now)
			if err != nil {
				return err
			}
		}
		if q.Spent > 0 {
			if ok, err := tx.Research().SpendExperience(ctx, s.CityID, d.Field, q.Spent, now); err != nil {
				return err
			} else if !ok {
				return refuseVillage(village.VillageBusy)
			}
		}
		finish := h.researchFinish(now, q)
		actionID, err := h.schedule(ctx, tx, application.SettlementResearchActionType, "settlement_research", id, s.CityID, now, finish)
		if err != nil {
			return err
		}
		if err := tx.SettlementKnowledge().StartResearch(ctx, application.SettlementResearch{
			ID: id, SettlementID: s.CityID, Code: code, Cost: q.Cost, LedgerTransactionID: txID,
			GameActionID: actionID, StartedBy: p.ID, StartedAt: now, FinishAt: finish,
			SlotRef: q.Slot.Ref, SpeedBPS: q.SpeedBPS, AheadBPS: q.AheadBPS, DiscountBPS: q.DiscountBPS, ShareBPS: q.ShareBPS, SpentPoints: q.Spent,
		}); err != nil {
			switch {
			case stderrors.Is(err, application.ErrSettlementResearchBusy):
				return refuseVillage(village.VillageBusy)
			case stderrors.Is(err, application.ErrSettlementAlreadyResearched):
				return refuseVillage(village.VillageAlreadyOwned)
			}
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "research_started", s.CityID, map[string]any{
			"settlement_id": s.CityID, "research_id": id, "code": code, "name": d.Name, "started_by": p.ID,
			"finish_at": finish.UTC().Format(time.RFC3339), "slot": q.Slot.Ref, "speed_bps": q.SpeedBPS,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.KnowledgeList(ctx, meta)
}

// knowledgeName is an item's authored name for an event payload, empty for
// a code the active content no longer declares.
func (h *VillageHandler) knowledgeName(code string) string {
	if d, ok := h.content.Current().SettlementKnowledgeDef(code); ok {
		return d.Name
	}
	return ""
}

// Buy handles settlement.knowledge.buy: buying a knowledge item from
// Support at the scarcity price (ADR 0031 section 10 point 3), paid in the
// neutral currency through the ledger (ReasonSupplierPurchase — the
// identical shape a company buying an NPC supplier's basic input already
// uses). Support sells every non-restricted, mode-eligible item (the
// owner's own decision, section 10 point 2); a restricted item is never
// offered here.
func (h *VillageHandler) Buy(ctx context.Context, meta envelope.Metadata, req VillageKnowledgeRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.ResearchStart); err != nil {
			return err
		}
		code := strings.TrimSpace(req.Code)
		d, ok := snap.SettlementKnowledgeDef(code)
		if !ok {
			return refuseVillage(village.VillageNotFound)
		}
		if d.Restricted {
			return refuseVillage(village.VillageNotAvailable)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		terrain := terrainTagsFor(w, s)
		st, _, err := h.knowledgeStanding(ctx, tx, snap, s, terrain, false)
		if err != nil {
			return err
		}
		tree := snap.SettlementKnowledgeTree()
		t := tree[code]
		if cerr := settlementknowledge.CanAcquire(t, tree, st, false); cerr != nil {
			return h.knowledgeAttempt(snap, st, tree, t, d, cerr, village.AddrKnowledgeList)
		}
		price, err := h.scarcityPrice(ctx, tx, d.Cost, code)
		if err != nil {
			return err
		}

		now := h.now()
		if price > 0 {
			if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonSupplierPurchase, price, now); err != nil {
				return err
			}
		}
		if _, err := tx.SettlementKnowledge().Grant(ctx, application.SettlementKnowledgeOwned{
			SettlementID: s.CityID, Code: code, AcquiredVia: "bought", AcquiredAt: now,
		}); err != nil {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "knowledge_bought", s.CityID, map[string]any{
			"settlement_id": s.CityID, "code": code, "name": d.Name, "price": price, "bought_by": p.ID,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.KnowledgeList(ctx, meta)
}

// Researched handles settlement.researched from the SCHEDULER: a research
// finishing. Exactly once: the research row, locked by id, must still be
// running under the action that finishes it.
func (h *VillageHandler) Researched(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		rs, err := tx.SettlementKnowledge().Research(ctx, in.ID)
		if isSentinel(err, application.ErrSettlementResearchNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if rs.Status != "running" || (req.ActionID != "" && rs.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(rs.FinishAt) {
			return errors.Internal(stderrors.New("handlers: a settlement research finished before its time"))
		}
		if err := tx.SettlementKnowledge().FinishResearch(ctx, rs.ID, now); err != nil {
			return err
		}
		if _, err := tx.SettlementKnowledge().Grant(ctx, application.SettlementKnowledgeOwned{
			SettlementID: rs.SettlementID, Code: rs.Code, AcquiredVia: "researched", AcquiredAt: now,
		}); err != nil {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "knowledge_researched", rs.SettlementID, map[string]any{
			"settlement_id": rs.SettlementID, "code": rs.Code, "name": h.knowledgeName(rs.Code),
		})
	})
}

// knowledgeAttempt is the refusal of a research or purchase: when the reason is
// a prerequisite it names exactly the knowledge that is missing (one hop) and
// where it comes from, otherwise the plain refusal.
func (h *VillageHandler) knowledgeAttempt(snap *content.Snapshot, st settlementknowledge.Standing, tree settlementknowledge.Tree,
	t settlementknowledge.Tech, d content.SettlementKnowledgeDef, cerr error, back string,
) *villageRefusal {
	if !stderrors.Is(cerr, settlementknowledge.ErrPrerequisiteMissing) {
		return knowledgeRefusal(cerr)
	}
	missing := st.Missing(t, tree)
	if len(missing) == 0 {
		return knowledgeRefusal(cerr)
	}
	pc := pathContext{snap: snap}
	return needsRefusal(village.VillagePrerequisite, village.NeedsForResearch, named(d.Code, d.Name), pc.knowledgeNeeds(missing), back)
}
