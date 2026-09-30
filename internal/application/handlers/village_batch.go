package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds settlement.build.place_many: the head lays several
// one-lot buildings (in practice a run of roads) with ONE command.
//
// The rule is Place's own, lot by lot — CanPlace against the grid, the
// knowledge/role gates, the money and the materials — and the whole batch is
// all-or-nothing in ONE transaction: every lot is judged first (against a
// grid that already counts the batch's earlier lots), a refusal names every
// lot that stopped it, and only a clean batch is paid (the TOTAL cost, one
// spend) and started.
//
// THE CAP. The concurrent-construction cap (village 1, town 2, city 4) exists
// to keep the head from starting many big things at once. A road is cheap and
// quick, so the content marks it cap_exempt: a road neither waits for a free
// slot nor holds one, in a batch or alone, and a batch of roads never blocks
// the house being built beside it. Any other 1x1 type is allowed in a batch
// but keeps its cost to the cap: each lot is one job, so a batch is refused
// (concurrent_cap, naming the lots past the limit) as soon as it would
// exceed the cap. Nothing is hardcoded: which types are exempt is content.

// VillageBuildManyRequest is a batch: the lots as tokens (a client sends
// {x,y,rotated} objects; the bridge spells them as tokens), or two ends of a
// line — From and To, joined horizontally first and then vertically.
type VillageBuildManyRequest struct {
	Code    string   `json:"code"`
	Lots    []string `json:"lots,omitempty"`
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	Confirm string   `json:"confirm,omitempty"`
}

func (r VillageBuildManyRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == screens.VillageBuildConfirm
}

// lots parses the request into distinct lots, in order. ok is false for a
// malformed token or an empty batch.
func (r VillageBuildManyRequest) lots() (out [][2]int, ok bool) {
	seen := map[[2]int]bool{}
	add := func(x, y int) {
		p := [2]int{x, y}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, tok := range r.Lots {
		x, y, _, tokOK := screens.ParseLotToken(strings.TrimSpace(tok))
		if !tokOK {
			return nil, false
		}
		add(x, y)
	}
	if r.From != "" || r.To != "" {
		fx, fy, _, okFrom := screens.ParseLotToken(strings.TrimSpace(r.From))
		tx, ty, _, okTo := screens.ParseLotToken(strings.TrimSpace(r.To))
		if !okFrom || !okTo {
			return nil, false
		}
		for _, p := range lineLots(fx, fy, tx, ty) {
			add(p[0], p[1])
		}
	}
	return out, len(out) > 0
}

// lineLots is the run of lots from (fx,fy) to (tx,ty): along the row first,
// then down the column, both ends included.
func lineLots(fx, fy, tx, ty int) [][2]int {
	var out [][2]int
	step := func(a, b int) int {
		if b >= a {
			return 1
		}
		return -1
	}
	for x := fx; ; x += step(fx, tx) {
		out = append(out, [2]int{x, fy})
		if x == tx {
			break
		}
	}
	for y := fy + step(fy, ty); fy != ty; y += step(fy, ty) {
		out = append(out, [2]int{tx, y})
		if y == ty {
			break
		}
	}
	return out
}

// PlaceMany handles settlement.build.place_many.
func (h *VillageHandler) PlaceMany(ctx context.Context, meta envelope.Metadata, req VillageBuildManyRequest) (*presenter.Response, error) {
	lang := meta.Language
	var confirmView *screens.LotBatchConfirmView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		lots, ok := req.lots()
		if !ok {
			return refuseVillage(screens.VillageNotFound)
		}
		code := strings.TrimSpace(req.Code)
		s, d, def, grid, standing, err := h.buildPlacementContext(ctx, tx, meta, code, false)
		if err != nil {
			return err
		}
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		if def.FootprintW != 1 || def.FootprintH != 1 || len(lots) > len(grid)*len(grid) {
			return refuseVillage(screens.VillageNotAvailable)
		}

		// Judge every lot; a placed lot occupies its cell for the next one
		// (a batch never collides with itself) and, unless the type is
		// cap_exempt, holds a slot of the cap.
		var failures []screens.BatchLotFailure
		for _, lot := range lots {
			x, y := lot[0], lot[1]
			if cerr := settlementbuilding.CanPlace(def, grid, x, y, standing); cerr != nil {
				kind := buildingRefusal(cerr).kind
				failures = append(failures, screens.BatchLotFailure{X: x, Y: y, Kind: kind})
				continue
			}
			grid[y][x].Occupied = true
			if !def.CapExempt {
				standing.RunningBuilds++
			}
		}
		if len(failures) > 0 {
			return &villageRefusal{kind: screens.VillageBatch, lots: failures}
		}

		n := int64(len(lots))
		snap := h.content.Current()
		total := def
		total.CostMoney = d.CostMoney * n
		total.CostMaterials = map[string]int64{}
		for material, qty := range def.CostMaterials {
			total.CostMaterials[material] = qty * n
		}
		if !req.confirmed() {
			view := screens.LotBatchConfirmView{
				SettlementName: s.Name, Building: named(d.Code, d.Name), Count: len(lots),
				CostMoney: total.CostMoney, BuildTime: h.scale.RealWait(def.BuildTime), Materials: materialLines(snap, total),
			}
			for _, lot := range lots {
				view.Lots = append(view.Lots, screens.LotBatchLot{X: lot[0], Y: lot[1]})
			}
			confirmView = &view
			return nil
		}

		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		finish := now.Add(h.scale.RealWait(def.BuildTime))
		ids := make([]string, len(lots))
		for i := range ids {
			ids[i] = h.ids.NewID()
		}
		for _, material := range sortedMaterialCodes(total) {
			if err := tx.Items().Move(ctx, application.ItemMove{
				Item: material, Qty: total.CostMaterials[material],
				FromOrg: application.SettlementOrg(s.CityID), FromHolding: application.HoldWarehouse,
				Reason: application.ItemSettlementConstruction, ReferenceType: "settlement_building", ReferenceID: ids[0], At: now,
			}); err != nil {
				if stderrors.Is(err, application.ErrNotEnoughItems) {
					return refuseVillage(screens.VillageMaterials)
				}
				return err
			}
		}
		if total.CostMoney > 0 {
			if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonSettlementConstruction, total.CostMoney, now); err != nil {
				return err
			}
		}
		placed := make([]map[string]any, 0, len(lots))
		for i, lot := range lots {
			if err := tx.SettlementBuildings().Place(ctx, application.SettlementBuildingInstance{
				ID: ids[i], SettlementID: s.CityID, TypeCode: code, LotX: lot[0], LotY: lot[1], Status: "building",
				QueuedAt: now, FinishAt: &finish,
			}); err != nil {
				if stderrors.Is(err, application.ErrLotOccupied) {
					return &villageRefusal{kind: screens.VillageBatch, lots: []screens.BatchLotFailure{{X: lot[0], Y: lot[1], Kind: screens.VillageOccupied}}}
				}
				return err
			}
			if _, err := h.schedule(ctx, tx, application.SettlementBuildActionType, "settlement_building", ids[i], s.CityID, now, finish); err != nil {
				return err
			}
			placed = append(placed, map[string]any{"building_id": ids[i], "lot_x": lot[0], "lot_y": lot[1]})
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "build_batch_started", map[string]any{
			"settlement_id": s.CityID, "type_code": code, "name": d.Name, "count": len(lots), "buildings": placed,
			"finish_at": finish.UTC().Format(time.RFC3339),
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirmView != nil {
		return screens.LotBatchConfirm(h.screen(meta, lang), *confirmView), nil
	}
	return h.Progress(ctx, meta)
}
