package handlers

import (
	"context"
	stderrors "errors"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds W5's placement, demolition and construction completion
// (docs/adr/0028-world-and-settlements.md section 6). Materials
// (CostMaterials) are not yet drawn from the settlement's own public stock
// (org_stacks) — only the money leg is charged here; org_kind gained
// 'settlement' in migration 0045 for exactly this, and wiring the goods
// leg is a documented follow-up, not silently skipped. Demolition salvage
// (ADR 0028 section 6.2's demolition_salvage_bps) is the same kind of
// documented v1 gap: a lot is freed, nothing is credited back yet.

// VillageBuildRequest names one settlement_building code to place.
type VillageBuildRequest struct {
	Code string `json:"code"`
}

// VillageBuildingRequest names one placed building's own id (demolish).
type VillageBuildingRequest struct {
	ID string `json:"id"`
}

// buildingRefusal maps a settlementbuilding.CanPlace failure to a screen.
func buildingRefusal(err error) *villageRefusal {
	switch {
	case stderrors.Is(err, settlementbuilding.ErrOutOfBounds):
		return refuseVillage(screens.VillageOutOfBounds)
	case stderrors.Is(err, settlementbuilding.ErrUnbuildableLot), stderrors.Is(err, settlementbuilding.ErrLotOccupied):
		return refuseVillage(screens.VillageOccupied)
	case stderrors.Is(err, settlementbuilding.ErrTerrainRequired):
		return refuseVillage(screens.VillageTerrain)
	case stderrors.Is(err, settlementbuilding.ErrKnowledgeMissing), stderrors.Is(err, settlementbuilding.ErrRoleMissing):
		return refuseVillage(screens.VillagePrerequisite)
	case stderrors.Is(err, settlementbuilding.ErrLiteracyTooLow):
		return refuseVillage(screens.VillageLiteracy)
	case stderrors.Is(err, settlementbuilding.ErrConcurrentBuildCap):
		return refuseVillage(screens.VillageConcurrentCap)
	default:
		return refuseVillage(screens.VillageNotAvailable)
	}
}

// Place handles settlement.build.place: queuing a building. ADR 0028
// section 6.3's own "chosen lot" is, in this v1, the first lot CanPlace
// itself would accept (settlementbuilding.FirstFreeLot) — a deterministic,
// legality-respecting pick rather than a free-form grid-tap UI, exactly the
// founding kit's own PlaceFoundingKit already does for its two starting
// buildings. Cost is paid and construction starts in the same command (see
// application.SettlementBuildingInstance.Status's own doc).
func (h *VillageHandler) Place(ctx context.Context, meta envelope.Metadata, req VillageBuildRequest) (*presenter.Response, error) {
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
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		code := strings.TrimSpace(req.Code)
		d, ok := snap.SettlementBuildingDef(code)
		if !ok {
			return refuseVillage(screens.VillageNotFound)
		}
		def := d.Def()

		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		grid, _, err := h.grid(ctx, tx, w, s)
		if err != nil {
			return err
		}
		st, capabilities, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
		if err != nil {
			return err
		}
		built, err := builtRoleCounts(ctx, tx, snap, s.CityID)
		if err != nil {
			return err
		}
		running, err := tx.SettlementBuildings().RunningCount(ctx, s.CityID)
		if err != nil {
			return err
		}
		standing := settlementbuilding.Standing{
			Knowledge: st.Owned, KnowledgeCapabilities: capabilities, Built: built,
			RunningBuilds: running, ConcurrentCap: h.concurrentBuildCap[s.Tier], LiteracyShareBPS: st.LiteracyShareBPS,
		}

		x, y, ok := settlementbuilding.FirstFreeLot(def, grid)
		if !ok {
			return refuseVillage(screens.VillageOccupied)
		}
		if cerr := settlementbuilding.CanPlace(def, grid, x, y, standing); cerr != nil {
			return buildingRefusal(cerr)
		}

		now := h.now()
		if def.CostMoney > 0 {
			if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonSettlementConstruction, def.CostMoney, now); err != nil {
				return err
			}
		}
		id := h.ids.NewID()
		if err := tx.SettlementBuildings().Place(ctx, application.SettlementBuildingInstance{
			ID: id, SettlementID: s.CityID, TypeCode: code, LotX: x, LotY: y, Status: "building", QueuedAt: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrLotOccupied) {
				return refuseVillage(screens.VillageOccupied)
			}
			return err
		}
		finish := now.Add(h.scale.RealWait(def.BuildTime))
		if _, err := h.schedule(ctx, tx, application.SettlementBuildActionType, "settlement_building", id, s.CityID, now, finish); err != nil {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "build_started", s.CityID, map[string]any{
			"settlement_id": s.CityID, "building_id": id, "type_code": code, "lot_x": x, "lot_y": y,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.Progress(ctx, meta)
}

// Demolish handles settlement.build.demolish: a complete building, once
// demolished, never comes back (ADR 0028 section 6.2) — the lot is simply
// free again for something else.
func (h *VillageHandler) Demolish(ctx context.Context, meta envelope.Metadata, req VillageBuildingRequest) (*presenter.Response, error) {
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
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.ID))
		if isSentinel(err, application.ErrBuildingNotFound) {
			return refuseVillage(screens.VillageNotFound)
		}
		if err != nil {
			return err
		}
		if b.SettlementID != s.CityID {
			return refuseVillage(screens.VillageNotFound)
		}
		now := h.now()
		if err := tx.SettlementBuildings().Demolish(ctx, b.ID, now); err != nil {
			if stderrors.Is(err, application.ErrBuildingNotDemolishable) {
				return refuseVillage(screens.VillageNotDemolishable)
			}
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "building_demolished", s.CityID, map[string]any{
			"settlement_id": s.CityID, "building_id": b.ID, "type_code": b.TypeCode,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.BuildMenu(ctx, meta)
}

// Built handles settlement.built from the SCHEDULER: a building finishing
// construction. Idempotent: Complete only ever transitions "building" ->
// "complete", so a redelivered completion is a no-op the second time.
func (h *VillageHandler) Built(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		b, err := tx.SettlementBuildings().Get(ctx, in.ID)
		if isSentinel(err, application.ErrBuildingNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if b.Status != "building" {
			return nil
		}
		now := h.now()
		if err := tx.SettlementBuildings().Complete(ctx, b.ID, now); err != nil {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "built", b.SettlementID, map[string]any{
			"settlement_id": b.SettlementID, "building_id": b.ID, "type_code": b.TypeCode,
		})
	})
}
