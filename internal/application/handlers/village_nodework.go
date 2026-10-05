package handlers

import (
	"context"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	village "github.com/mrjvadi/torncity/internal/presentation/village"
)

// nodeWork is the work block of a standing building (roadmap 2.2 phase 1, rule 1c): what
// it does, who works there, what a shift uses and gives, and why it is idle. It only
// READS: the shifts running there, the stock and the treasury. The posts come from the
// function schema (building_functions.yml, through `replaces`) with the building row's
// own workers as the fallback; a building with neither is said to have no work yet.
func (h *VillageHandler) nodeWork(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, buildings []application.SettlementBuildingInstance,
) (*village.WorkNode, error) {
	w := &village.WorkNode{Kind: village.NodeKindNone, Status: village.NodeIdle}
	var fn content.BuildingFunctionDef
	var hasFn bool
	if code, ok := snap.FunctionReplacing(b.TypeCode); ok {
		fn, hasFn = snap.BuildingFunction(code)
	}
	if hasFn {
		w.Kind = fn.Kind
		w.IfUnstaffed = fn.IfUnstaffed
	}

	// the shifts running at this building, by who holds them
	shifts, err := tx.SettlementTreasury().WorkingShifts(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	var here []application.SettlementShift
	for _, sh := range shifts {
		if sh.BuildingID == b.ID {
			here = append(here, sh)
		}
	}

	// posts: the function's staff, else the building's own worker count
	type post struct {
		role  string
		slots int
	}
	var posts []post
	if hasFn {
		for _, st := range fn.Staff {
			posts = append(posts, post{st.Role, st.Slots})
		}
	}
	if len(posts) == 0 && d.Workers > 0 {
		posts = append(posts, post{"worker", d.Workers})
	}
	produces := len(d.Produces) > 0
	if !hasFn && !produces && len(posts) == 0 {
		w.Reasons = append(w.Reasons, village.WorkReason{Code: village.NodeReasonNoFunction})
		return w, nil
	}
	if w.Kind == village.NodeKindNone {
		switch {
		case produces:
			w.Kind = village.NodeKindProduction
		case len(posts) > 0:
			w.Kind = village.NodeKindService
		}
	}
	// the running shifts fill the first slots; a store's keeper is counted from its day
	// (below), not from a shift
	i := 0
	for _, p := range posts {
		for n := 0; n < p.slots; n++ {
			slot := village.WorkSlot{Role: p.role, Worker: "empty"}
			if i < len(here) {
				if here[i].WorkerKind == application.LaborWorkerNPC {
					slot.Worker = "npc"
				} else {
					slot.Worker = "player"
					slot.Name = h.playerName(ctx, tx, here[i].PlayerID)
				}
				i++
				w.Filled++
			}
			w.Slots = append(w.Slots, slot)
			w.Max++
		}
	}

	// what one shift uses and gives
	if produces || len(d.Consumes) > 0 {
		w.ShiftSeconds = int64(h.scale.RealWait(d.Def().Work.Shift).Seconds())
		w.Wage = d.Wage
		w.Inputs = h.nodeLines(snap, d.Consumes)
		w.Outputs = h.nodeLines(snap, d.Produces)
	}

	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return nil, err
	}
	if produces {
		for _, c := range materialCodes(d.Consumes) {
			if have := stock.Units[c]; have < d.Consumes[c] {
				item := materialLineOf(snap, c, d.Consumes[c]).Component
				w.Reasons = append(w.Reasons, village.WorkReason{Code: village.NodeReasonNoInput, Item: &item, Have: have, Need: d.Consumes[c]})
			}
		}
		if class, missing := stock.shortfall(d.Produces, d.Consumes); missing > 0 {
			w.StorageClass = class
			w.StorageFree = stock.freeSpace(class)
			w.Reasons = append(w.Reasons, village.WorkReason{Code: village.NodeReasonStorageFull, Class: class,
				Have: stock.freeSpace(class), Need: stock.freeSpace(class) + missing})
		} else if outs := stock.spaces(d.Produces); len(outs) > 0 {
			classes := make([]string, 0, len(outs))
			for c := range outs {
				classes = append(classes, c)
			}
			sort.Strings(classes)
			w.StorageClass, w.StorageFree = classes[0], stock.freeSpace(classes[0])
		}
		if treasury, err := treasuryBalance(ctx, tx, s.CityID); err != nil {
			return nil, err
		} else if d.Wage > 0 && treasury < d.Wage {
			w.Reasons = append(w.Reasons, village.WorkReason{Code: village.NodeReasonEmployerBroke, Have: treasury, Need: d.Wage})
		}
	}
	if w.Kind == village.NodeKindStorage {
		for _, st := range stock.Stores {
			if st.ID == b.ID && !st.Kept {
				w.Reasons = append(w.Reasons, village.WorkReason{Code: village.NodeReasonNoKeeper})
			} else if st.ID == b.ID {
				w.Filled = max(w.Filled, 1)
			}
		}
	}
	if w.Max > 0 && w.Filled == 0 && w.Kind != village.NodeKindStorage {
		w.Reasons = append([]village.WorkReason{{Code: village.NodeReasonNoStaff}}, w.Reasons...)
	}
	if w.Filled > 0 {
		w.Status = village.NodeWorking // running, possibly with something held back
	}
	return w, nil
}

// nodeLines turns a basket into named lines in a fixed order.
func (h *VillageHandler) nodeLines(snap *content.Snapshot, m map[string]int64) []village.WorkItemLine {
	var out []village.WorkItemLine
	for _, c := range materialCodes(m) {
		out = append(out, village.WorkItemLine{Item: materialLineOf(snap, c, m[c]).Component, Qty: m[c]})
	}
	return out
}

