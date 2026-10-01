package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
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
	// village.LotToken/ParseLotToken both read and write, so a screen and
	// a handler can never spell a coordinate two different ways.
	Lot     string `json:"lot,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

func (r VillageBuildRequest) code() string { return strings.TrimSpace(r.Code) }

func (r VillageBuildRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == village.VillageBuildConfirm
}

// lot parses r.Lot, or reports ok=false for a missing or malformed one — a
// forged or stale button, refused the same way any other malformed
// callback argument is, never trusted as a coordinate on its own.
func (r VillageBuildRequest) lot() (x, y int, rotated, ok bool) {
	return village.ParseLotToken(r.Lot)
}

// VillageLotsRequest names the building code the grid is being shown for,
// and whether to preview it rotated.
type VillageLotsRequest struct {
	Code   string `json:"code"`
	Rotate string `json:"rotate,omitempty"`
	// From is the line-picking step of a run of one-lot buildings: "line"
	// (choose the first lot) or the first lot's token (choose the last).
	From string `json:"from,omitempty"`
	// Win is the north-west lot of the window a Telegram keyboard shows over a
	// grid wider than a row of buttons (a lot token).
	Win string `json:"win,omitempty"`
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
		return refuseVillage(village.VillageOutOfBounds)
	case stderrors.Is(err, settlementbuilding.ErrUnbuildableLot):
		return refuseVillage(village.VillageUnbuildable)
	case stderrors.Is(err, settlementbuilding.ErrLotOccupied):
		return refuseVillage(village.VillageOccupied)
	case stderrors.Is(err, settlementbuilding.ErrTerrainRequired):
		return refuseVillage(village.VillageTerrain)
	case stderrors.Is(err, settlementbuilding.ErrKnowledgeMissing), stderrors.Is(err, settlementbuilding.ErrRoleMissing):
		return refuseVillage(village.VillagePrerequisite)
	case stderrors.Is(err, settlementbuilding.ErrLiteracyTooLow):
		return refuseVillage(village.VillageLiteracy)
	case stderrors.Is(err, settlementbuilding.ErrConcurrentBuildCap):
		return refuseVillage(village.VillageConcurrentCap)
	default:
		return refuseVillage(village.VillageNotAvailable)
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
		err = refuseVillage(village.VillageNotFound)
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
	running, rerr := h.runningJobs(ctx, tx, snap, s.CityID)
	if rerr != nil {
		err = rerr
		return
	}
	standing = settlementbuilding.Standing{
		Knowledge: st.Owned, KnowledgeCapabilities: capabilities, Built: built,
		RunningBuilds: running, ConcurrentCap: h.concurrentBuildCap[s.Tier], LiteracyShareBPS: st.LiteracyShareBPS,
		SettlementTier: s.Tier,
	}
	return
}

// lotState reports one lot's own visible state for the grid screen,
// independent of which building (if any) is being placed.
func lotState(lot settlementbuilding.Lot, occupiedRoad bool) string {
	switch {
	case !lot.Buildable:
		return village.LotWater
	case lot.Occupied && occupiedRoad:
		return village.LotRoad
	case lot.Occupied:
		return village.LotOccupied
	case hasAny(lot.TerrainTags, []string{"sloped_lot"}):
		return village.LotSteep
	default:
		return village.LotFree
	}
}

// materialLines is a building's own CostMaterials, named and ordered, for
// the confirm screen.
func materialLines(snap *content.Snapshot, def settlementbuilding.Def) []village.MaterialLine {
	if len(def.CostMaterials) == 0 {
		return nil
	}
	codes := make([]string, 0, len(def.CostMaterials))
	for code := range def.CostMaterials {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	out := make([]village.MaterialLine, 0, len(codes))
	for _, code := range codes {
		cd, _ := snap.ComponentDef(code)
		out = append(out, village.MaterialLine{Component: named(cd.Code, cd.Name), Quantity: def.CostMaterials[code]})
	}
	return out
}

// Lots handles settlement.build.lots: the grid the leader chooses a
// building's own lot from (ADR 0028 section 6 — the leader places a
// building on the settlement's own placement grid; nothing here guesses a
// lot for them).
func (h *VillageHandler) Lots(ctx context.Context, meta envelope.Metadata, req VillageLotsRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.LotGridView
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
		if !def.ListedAt(s.Tier) {
			return refuseVillage(village.VillageNotAvailable)
		}
		// The prerequisites first: a building the village cannot start yet shows
		// what is missing and where it comes from, not a grid to choose a lot on.
		if rf, err := h.placementRefusal(ctx, tx, h.content.Current(), s, d, def); err != nil {
			return err
		} else if rf != nil {
			return rf
		}
		view = village.LotGridView{
			SettlementName: s.Name, Building: named(d.Code, d.Name),
			CanRotate: d.Def().CanRotate(), Rotated: rotated && d.Def().CanRotate(),
			GridLots: grid.Height(),
			Multi:    d.Footprint == [2]int{1, 1} && d.CapExempt,
		}
		if wx, wy, _, ok := village.ParseLotToken(strings.TrimSpace(req.Win)); ok {
			view.WinX, view.WinY = wx, wy
		}
		if view.Multi {
			switch from := strings.TrimSpace(req.From); {
			case from == "" || from == "-":
			case from == village.LineStart:
				view.Line = village.LineStart
			case from != "":
				if fx, fy, _, ok := village.ParseLotToken(from); ok {
					view.Line, view.From = village.LineEnd, village.LotBatchLot{X: fx, Y: fy}
				}
			}
		}
		owned, oerr := privateLotSet(ctx, tx, s.CityID)
		if oerr != nil {
			return oerr
		}
		for y := 0; y < grid.Height(); y++ {
			row := make([]village.LotCell, 0, grid.Width())
			for x := 0; x < grid.Width(); x++ {
				lot := grid[y][x]
				road := lot.Occupied && isRoadLot(ctx, tx, s.CityID, x, y)
				cell := village.LotCell{X: x, Y: y, State: lotState(lot, road)}
				cell.Fits = !d.Private() && settlementbuilding.CanPlace(def, grid, x, y, standing) == nil &&
					!footprintTouches(owned, def, x, y)
				row = append(row, cell)
			}
			view.Rows = append(view.Rows, row)
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.LotGrid(h.screen(meta, lang), view), nil
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
func (h *VillageHandler) Place(ctx context.Context, meta envelope.Metadata, req VillageBuildRequest) (*presentation.Response, error) {
	lang := meta.Language
	var confirmView *village.LotConfirmView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		code := req.code()
		x, y, rotated, ok := req.lot()
		if !ok {
			return refuseVillage(village.VillageNotFound)
		}

		s, d, def, grid, standing, err := h.buildPlacementContext(ctx, tx, meta, code, rotated)
		if err != nil {
			return err
		}
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		if d.Private() {
			return refuseVillage(village.CitizenPrivateOnly)
		}
		if !def.ListedAt(s.Tier) {
			return refuseVillage(village.VillageNotAvailable)
		}
		if rf, err := h.placementRefusal(ctx, tx, h.content.Current(), s, d, def); err != nil {
			return err
		} else if rf != nil {
			return rf
		}
		if cerr := settlementbuilding.CanPlace(def, grid, x, y, standing); cerr != nil {
			return buildingRefusal(cerr)
		}
		owned, oerr := privateLotSet(ctx, tx, s.CityID)
		if oerr != nil {
			return oerr
		}
		if footprintTouches(owned, def, x, y) {
			return refuseVillage(village.CitizenLotPrivate)
		}
		// The game lays the road that connects the building (roadplan.go);
		// a building no road could ever reach is refused before anything
		// is paid.
		autoRoads, perr := h.planAutoRoads(ctx, tx, s, code, def, grid, x, y)
		if perr != nil {
			return perr
		}
		roadFee := int64(len(autoRoads)) * h.autoRoadCost

		if !req.confirmed() {
			confirmView = &village.LotConfirmView{
				SettlementName: s.Name, Building: named(d.Code, d.Name), X: x, Y: y, Rotated: rotated,
				CostMoney: d.CostMoney + roadFee, BuildTime: h.scale.RealWait(def.BuildTime),
				Materials: materialLines(h.content.Current(), def), AutoRoads: len(autoRoads),
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
					// A racing build took the stock: name what is missing now.
					if rf, rerr := h.placementRefusal(ctx, tx, h.content.Current(), s, d, def); rerr == nil && rf != nil {
						return rf
					}
					return refuseVillage(village.VillageMaterials)
				}
				return err
			}
		}
		if d.CostMoney+roadFee > 0 {
			if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonSettlementConstruction, d.CostMoney+roadFee, now); err != nil {
				return err
			}
		}
		inst := application.SettlementBuildingInstance{
			ID: id, SettlementID: s.CityID, TypeCode: code, LotX: x, LotY: y, Status: "building", QueuedAt: now,
			Rotated: rotated && d.Def().CanRotate(),
		}
		// With the labour rules on, construction is raised by the work of shifts
		// (ADR 0037): the build time becomes the work required and nothing is
		// scheduled to finish it. Without them, the older timer.
		byWork := h.labor.Enabled()
		var finish time.Time
		if byWork {
			inst.WorkRequired = h.labor.WorkRequired(int64(def.BuildTime / time.Minute))
		} else {
			finish = now.Add(h.scale.RealWait(def.BuildTime))
			inst.FinishAt = &finish
		}
		if err := tx.SettlementBuildings().Place(ctx, inst); err != nil {
			if stderrors.Is(err, application.ErrLotOccupied) {
				return refuseVillage(village.VillageOccupied)
			}
			return err
		}
		payload := map[string]any{
			"settlement_id": s.CityID, "building_id": id, "type_code": code, "name": d.Name, "lot_x": x, "lot_y": y,
			"rotated": rotated && d.Def().CanRotate(),
		}
		if byWork {
			payload["work_required"] = inst.WorkRequired
			if err := h.openSiteJob(ctx, tx, h.content.Current(), s, inst, application.LaborEmployerSettlement, s.CityID, p.ID, now); err != nil {
				return err
			}
		} else {
			if _, err := h.schedule(ctx, tx, application.SettlementBuildActionType, "settlement_building", id, s.CityID, now, finish); err != nil {
				return err
			}
			payload["finish_at"] = finish.UTC().Format(time.RFC3339)
		}
		laid, err := h.layAutoRoads(ctx, tx, s.CityID, autoRoads, now)
		if err != nil {
			return err
		}
		if len(laid) > 0 {
			payload["auto_roads"] = laid
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "build_started", payload)
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirmView != nil {
		return village.LotConfirm(h.screen(meta, lang), *confirmView), nil
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
func (h *VillageHandler) Demolish(ctx context.Context, meta envelope.Metadata, req VillageBuildingRequest) (*presentation.Response, error) {
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
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.ID))
		if isSentinel(err, application.ErrBuildingNotFound) {
			return refuseVillage(village.VillageNotFound)
		}
		if err != nil {
			return err
		}
		if b.SettlementID != s.CityID {
			return refuseVillage(village.VillageNotFound)
		}
		// The head changes the village's own buildings; a resident's is theirs.
		if err := h.mayChangeBuilding(ctx, tx, s, p.ID, b.ID); err != nil {
			return err
		}
		now := h.now()
		if err := tx.SettlementBuildings().Demolish(ctx, b.ID, now); err != nil {
			if stderrors.Is(err, application.ErrBuildingNotDemolishable) {
				return refuseVillage(village.VillageNotDemolishable)
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
func (h *VillageHandler) Cancel(ctx context.Context, meta envelope.Metadata, req VillageBuildingRequest) (*presentation.Response, error) {
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
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.ID))
		if isSentinel(err, application.ErrBuildingNotFound) {
			return refuseVillage(village.VillageNotFound)
		}
		if err != nil {
			return err
		}
		if b.SettlementID != s.CityID {
			return refuseVillage(village.VillageNotFound)
		}
		// The head changes the village's own buildings; a resident's is theirs.
		if err := h.mayChangeBuilding(ctx, tx, s, p.ID, b.ID); err != nil {
			return err
		}
		if err := tx.SettlementBuildings().Cancel(ctx, b.ID, h.now()); err != nil {
			if stderrors.Is(err, application.ErrBuildingNotCancellable) {
				return refuseVillage(village.VillageNotCancellable)
			}
			return err
		}
		if b.ByWork() {
			if err := tx.SettlementTreasury().CloseJobOfBuilding(ctx, b.ID, h.now()); err != nil {
				return err
			}
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
	if ok && d.Private() {
		return nil // a resident's building: what it cost was theirs, not the treasury's
	}
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
func (h *VillageHandler) Built(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
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
		if b.Status != "building" || b.ByWork() {
			// A building raised by work is finished by the last shift, never by a timer.
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
	// Who owns which lot is part of what a member's layout shows (the
	// citizen loop): the version an event announces must include it.
	lots, err := tx.Citizens().Lots(ctx, s.CityID)
	if err != nil {
		return err
	}
	priv, err := tx.Citizens().PrivateBuildings(ctx, s.CityID)
	if err != nil {
		return err
	}
	payload["layout_version"] = application.LayoutVersionsWithTenure(s.CityID, s.Tier, s.Name,
		wsettle.GridLotsGrown(s.Tier, h.villageGridLots, s.GridGrowth), rows, footprint, application.TenureMark(lots, priv))
	return appendVillageEvent(ctx, tx, meta, name, s.CityID, payload)
}

// runningJobs is how many of the settlement's buildings under construction
// hold a slot of the concurrent-construction cap: every one but a type the
// content marks cap_exempt (a road).
func (h *VillageHandler) runningJobs(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string) (int, error) {
	rows, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range rows {
		if b.Status != "building" {
			continue
		}
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok && d.CapExempt {
			continue
		}
		n++
	}
	return n, nil
}
