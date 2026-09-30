package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds W5's placement, demolition and construction completion
// (docs/adr/0028-world-and-settlements.md section 6). The LEADER chooses
// every building's own lot (and, where the footprint allows it, its
// rotation) — Lots renders the grid to choose from, Place takes the exact
// coordinates and validates them with settlementbuilding.CanPlace, the
// same authority the grid screen itself queried to grey out what would not
// fit. Materials are drawn from the settlement's own public stock
// (org_stacks, OrgSettlement); demolition credits back
// settlement.demolition_salvage_bps of a building's own money cost.

// VillageBuildRequest names one settlement_building code, and — once a lot
// is chosen — that lot.
type VillageBuildRequest struct {
	Code string `json:"code"`
	// Lot is "{x}-{y}" or "{x}-{y}-r" (rotated), the token
	// screens.LotToken/ParseLotToken both read and write, so a screen and
	// a handler can never spell a coordinate two different ways.
	Lot     string `json:"lot,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

func (r VillageBuildRequest) code() string { return strings.TrimSpace(r.Code) }

func (r VillageBuildRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == screens.VillageBuildConfirm
}

// lot parses r.Lot, or reports ok=false for a missing or malformed one — a
// forged or stale button, refused the same way any other malformed
// callback argument is, never trusted as a coordinate on its own.
func (r VillageBuildRequest) lot() (x, y int, rotated, ok bool) {
	return screens.ParseLotToken(r.Lot)
}

// VillageLotsRequest names the building code the grid is being shown for,
// and whether to preview it rotated.
type VillageLotsRequest struct {
	Code   string `json:"code"`
	Rotate string `json:"rotate,omitempty"`
}

func (r VillageLotsRequest) code() string  { return strings.TrimSpace(r.Code) }
func (r VillageLotsRequest) rotated() bool { return strings.TrimSpace(r.Rotate) == "1" }

// VillageBuildingRequest names one placed building's own id (demolish).
type VillageBuildingRequest struct {
	ID string `json:"id"`
}

// buildingRefusal maps a settlementbuilding.CanPlace failure to a screen.
func buildingRefusal(err error) *villageRefusal {
	switch {
	case stderrors.Is(err, settlementbuilding.ErrOutOfBounds):
		return refuseVillage(screens.VillageOutOfBounds)
	case stderrors.Is(err, settlementbuilding.ErrUnbuildableLot):
		return refuseVillage(screens.VillageUnbuildable)
	case stderrors.Is(err, settlementbuilding.ErrLotOccupied):
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

// buildPlacementContext resolves everything a placement decision needs for
// one building code: the settlement, its content definition, its domain
// definition (rotated if asked), the settlement's own occupancy+terrain
// grid, and its standing. Shared between Lots (the picker) and Place (the
// authority), so the two can never quietly disagree about what fits where.
func (h *VillageHandler) buildPlacementContext(ctx context.Context, tx application.Tx, meta envelope.Metadata, code string, rotate bool,
) (s application.FoundedSettlement, d content.SettlementBuildingDef, def settlementbuilding.Def, grid settlementbuilding.Grid,
	standing settlementbuilding.Standing, err error,
) {
	snap := h.content.Current()
	s, err = h.settlementOf(ctx, tx, meta)
	if err != nil {
		return
	}
	var ok bool
	d, ok = snap.SettlementBuildingDef(code)
	if !ok {
		err = refuseVillage(screens.VillageNotFound)
		return
	}
	def = d.Def()
	if rotate && def.CanRotate() {
		def = def.Rotate()
	}
	w, werr := h.world(ctx)
	if werr != nil {
		err = werr
		return
	}
	grid, _, err = h.grid(ctx, tx, w, s)
	if err != nil {
		return
	}
	st, capabilities, kerr := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if kerr != nil {
		err = kerr
		return
	}
	built, berr := builtRoleCounts(ctx, tx, snap, s.CityID)
	if berr != nil {
		err = berr
		return
	}
	running, rerr := tx.SettlementBuildings().RunningCount(ctx, s.CityID)
	if rerr != nil {
		err = rerr
		return
	}
	standing = settlementbuilding.Standing{
		Knowledge: st.Owned, KnowledgeCapabilities: capabilities, Built: built,
		RunningBuilds: running, ConcurrentCap: h.concurrentBuildCap[s.Tier], LiteracyShareBPS: st.LiteracyShareBPS,
	}
	return
}

// lotState reports one lot's own visible state for the grid screen,
// independent of which building (if any) is being placed.
func lotState(lot settlementbuilding.Lot, occupiedRoad bool) string {
	switch {
	case !lot.Buildable:
		return screens.LotWater
	case lot.Occupied && occupiedRoad:
		return screens.LotRoad
	case lot.Occupied:
		return screens.LotOccupied
	case hasAny(lot.TerrainTags, []string{"sloped_lot"}):
		return screens.LotSteep
	default:
		return screens.LotFree
	}
}

// materialLines is a building's own CostMaterials, named and ordered, for
// the confirm screen.
func materialLines(snap *content.Snapshot, def settlementbuilding.Def) []screens.MaterialLine {
	if len(def.CostMaterials) == 0 {
		return nil
	}
	codes := make([]string, 0, len(def.CostMaterials))
	for code := range def.CostMaterials {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	out := make([]screens.MaterialLine, 0, len(codes))
	for _, code := range codes {
		cd, _ := snap.ComponentDef(code)
		out = append(out, screens.MaterialLine{Component: named(cd.Code, cd.Name), Quantity: def.CostMaterials[code]})
	}
	return out
}

// Lots handles settlement.build.lots: the grid the leader chooses a
// building's own lot from (ADR 0028 section 6 — the leader places a
// building on the settlement's own placement grid; nothing here guesses a
// lot for them).
func (h *VillageHandler) Lots(ctx context.Context, meta envelope.Metadata, req VillageLotsRequest) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.LotGridView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		code := req.code()
		rotated := req.rotated()
		s, d, def, grid, standing, err := h.buildPlacementContext(ctx, tx, meta, code, rotated)
		if err != nil {
			return err
		}
		view = screens.LotGridView{
			SettlementName: s.Name, Building: named(d.Code, d.Name),
			CanRotate: d.Def().CanRotate(), Rotated: rotated && d.Def().CanRotate(),
			GridLots: grid.Height(),
		}
		for y := 0; y < grid.Height(); y++ {
			row := make([]screens.LotCell, 0, grid.Width())
			for x := 0; x < grid.Width(); x++ {
				lot := grid[y][x]
				road := lot.Occupied && isRoadLot(ctx, tx, s.CityID, x, y)
				cell := screens.LotCell{X: x, Y: y, State: lotState(lot, road)}
				cell.Fits = settlementbuilding.CanPlace(def, grid, x, y, standing) == nil
				row = append(row, cell)
			}
			view.Rows = append(view.Rows, row)
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.LotGrid(h.screen(meta, lang), view), nil
}

// isRoadLot reports whether the standing building at (x,y) is a road, for
// the grid's own distinct 🛣 marker. A small, direct lookup rather than
// plumbing type codes through the whole grid-building path, since only the
// display needs it.
func isRoadLot(ctx context.Context, tx application.Tx, settlementID string, x, y int) bool {
	buildings, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return false
	}
	for _, b := range buildings {
		if b.LotX == x && b.LotY == y && b.Holds() {
			return b.TypeCode == "road"
		}
	}
	return false
}

// Place handles settlement.build.place: the leader's own chosen lot (ADR
// 0028 section 6.3). The first press (unconfirmed) shows the cost and time
// and changes nothing; the second (confirmed) pays and queues it — cost is
// paid, materials drawn from the settlement's own public stock, and
// construction starts in the same command (see
// application.SettlementBuildingInstance.Status's own doc).
func (h *VillageHandler) Place(ctx context.Context, meta envelope.Metadata, req VillageBuildRequest) (*presenter.Response, error) {
	lang := meta.Language
	var confirmView *screens.LotConfirmView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		code := req.code()
		x, y, rotated, ok := req.lot()
		if !ok {
			return refuseVillage(screens.VillageNotFound)
		}

		s, d, def, grid, standing, err := h.buildPlacementContext(ctx, tx, meta, code, rotated)
		if err != nil {
			return err
		}
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		if cerr := settlementbuilding.CanPlace(def, grid, x, y, standing); cerr != nil {
			return buildingRefusal(cerr)
		}

		if !req.confirmed() {
			confirmView = &screens.LotConfirmView{
				SettlementName: s.Name, Building: named(d.Code, d.Name), X: x, Y: y, Rotated: rotated,
				CostMoney: d.CostMoney, BuildTime: h.scale.RealWait(def.BuildTime),
				Materials: materialLines(h.content.Current(), def),
			}
			return nil
		}

		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}

		now := h.now()
		id := h.ids.NewID()
		for _, material := range sortedMaterialCodes(def) {
			if err := tx.Items().Move(ctx, application.ItemMove{
				Item: material, Qty: def.CostMaterials[material],
				FromOrg: application.SettlementOrg(s.CityID), FromHolding: application.HoldWarehouse,
				Reason: application.ItemSettlementConstruction, ReferenceType: "settlement_building", ReferenceID: id, At: now,
			}); err != nil {
				if stderrors.Is(err, application.ErrNotEnoughItems) {
					return refuseVillage(screens.VillageMaterials)
				}
				return err
			}
		}
		if d.CostMoney > 0 {
			if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonSettlementConstruction, d.CostMoney, now); err != nil {
				return err
			}
		}
		finish := now.Add(h.scale.RealWait(def.BuildTime))
		if err := tx.SettlementBuildings().Place(ctx, application.SettlementBuildingInstance{
			ID: id, SettlementID: s.CityID, TypeCode: code, LotX: x, LotY: y, Status: "building", QueuedAt: now,
			Rotated: rotated && d.Def().CanRotate(), FinishAt: &finish,
		}); err != nil {
			if stderrors.Is(err, application.ErrLotOccupied) {
				return refuseVillage(screens.VillageOccupied)
			}
			return err
		}
		if _, err := h.schedule(ctx, tx, application.SettlementBuildActionType, "settlement_building", id, s.CityID, now, finish); err != nil {
			return err
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "build_started", map[string]any{
			"settlement_id": s.CityID, "building_id": id, "type_code": code, "name": d.Name, "lot_x": x, "lot_y": y,
			"rotated": rotated && d.Def().CanRotate(), "finish_at": finish.UTC().Format(time.RFC3339),
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirmView != nil {
		return screens.LotConfirm(h.screen(meta, lang), *confirmView), nil
	}
	return h.Progress(ctx, meta)
}

func sortedMaterialCodes(def settlementbuilding.Def) []string {
	codes := make([]string, 0, len(def.CostMaterials))
	for code := range def.CostMaterials {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// Demolish handles settlement.build.demolish: a complete building, once
// demolished, never comes back (ADR 0028 section 6.2) — the lot is simply
// free again for something else. Its own money cost's
// demolition_salvage_bps share is credited back to the treasury.
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
		if err := h.creditSalvage(ctx, tx, s.CityID, b.TypeCode, now); err != nil {
			return err
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "building_demolished", map[string]any{
			"settlement_id": s.CityID, "building_id": b.ID, "type_code": b.TypeCode, "name": h.buildingName(b.TypeCode),
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.BuildMenu(ctx, meta)
}

// Cancel handles settlement.build.cancel: the leader calls off a building
// that is still going up. Its spend is forfeited (ADR 0028 section 6.2: once
// building has started, cancelling forfeits the spend and only frees the
// lot); the scheduled completion finds the row no longer "building" and does
// nothing. A finished building is demolished, not cancelled.
func (h *VillageHandler) Cancel(ctx context.Context, meta envelope.Metadata, req VillageBuildingRequest) (*presenter.Response, error) {
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
		if err := tx.SettlementBuildings().Cancel(ctx, b.ID, h.now()); err != nil {
			if stderrors.Is(err, application.ErrBuildingNotCancellable) {
				return refuseVillage(screens.VillageNotCancellable)
			}
			return err
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "build_cancelled", map[string]any{
			"settlement_id": s.CityID, "building_id": b.ID, "type_code": b.TypeCode,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.Progress(ctx, meta)
}

// creditSalvage pays demolition_salvage_bps of a demolished building's own
// money cost into the settlement's treasury, from system_source: scrap
// value recovered (ReasonSettlementSalvage), not a refund of money that
// never left (ADR 0028 section 6.2).
func (h *VillageHandler) creditSalvage(ctx context.Context, tx application.Tx, settlementID, typeCode string, at time.Time) error {
	if h.demolitionSalvageBPS <= 0 {
		return nil
	}
	d, ok := h.content.Current().SettlementBuildingDef(typeCode)
	if !ok || d.CostMoney <= 0 {
		return nil
	}
	salvage := d.CostMoney * h.demolitionSalvageBPS / 10_000
	if salvage <= 0 {
		return nil
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, settlementID)
	if err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: application.ReasonSettlementSalvage, CreatedAt: at,
		Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-salvage)},
			{AccountID: treasury.ID, Amount: money.FromMinor(salvage)},
		},
	})
	return err
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
		s, err := tx.Settlements().ByID(ctx, b.SettlementID)
		if err != nil {
			return err
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "built", map[string]any{
			"settlement_id": b.SettlementID, "building_id": b.ID, "type_code": b.TypeCode, "name": h.buildingName(b.TypeCode),
		})
	})
}

// buildingName is a building type's authored name for an event payload,
// empty for a code the active content no longer declares.
func (h *VillageHandler) buildingName(code string) string {
	if d, ok := h.content.Current().SettlementBuildingDef(code); ok {
		return d.Name
	}
	return ""
}

// appendBuildingEvent writes an event that changes the picture of the
// settlement (a building placed, finished, cancelled or pulled down). It
// carries the version the layout will have once this transaction commits, for
// each kind of viewer (application.LayoutVersions), so a client holding a
// layout knows from the event alone whether it is already current or must
// fetch it again. The buildings are read in the same transaction, after the
// change, so the version is exactly the one GET /settlements/{id}/layout will
// report.
func (h *VillageHandler) appendBuildingEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	s application.FoundedSettlement, name string, payload map[string]any,
) error {
	rows, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return err
	}
	snap := h.content.Current()
	footprint := func(code string, rotated bool) (int, int) {
		if d, ok := snap.SettlementBuildingDef(code); ok {
			def := d.Def()
			if rotated {
				def = def.Rotate()
			}
			return def.FootprintW, def.FootprintH
		}
		return 1, 1
	}
	payload["layout_version"] = application.LayoutVersionsOf(s.CityID, s.Tier, s.Name,
		wsettle.GridLotsForTier(s.Tier, h.villageGridLots), rows, footprint)
	return appendVillageEvent(ctx, tx, meta, name, s.CityID, payload)
}
