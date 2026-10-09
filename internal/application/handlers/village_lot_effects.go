package handlers

import (
	"context"
	stderrors "errors"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/lotbuild"
)

// What the modules of a lot DO (docs/adr/0045 B1, rule 1c): every effect below is read by exactly one rule.
//
//	bedroom           housing_capacity  -> the households a settlement houses (the labour pool, the population cap)
//	storeroom, cellar personal_storage  -> room in the owner's home store (room.go homeCapacity)
//	shelves           personal_storage  -> the same; in a stall also stall_slots -> counters of the owner's own on the
//	                  village book (market.go: no listing fee, no common stall used)
//	hearth            warmth_shelter    -> a warm rest at home (HomeRest), burning a unit of wood from the home store
//	workbench         workbench         -> personal crafting, ADR 0045 phase B2: not read yet, so it cannot be added
//
// The old catalogue effects of a building already count the modules its level includes; only the modules beyond
// them add to those targets (lotbuild.Provided with total=false).

// kitCache keeps the resolved kit of the snapshot in use: a reload makes a new snapshot, hence a new kit.
var kitCache struct {
	mu   sync.Mutex
	snap *content.Snapshot
	kit  lotKit
}

// kitOf is the lot kit of a snapshot.
func kitOf(snap *content.Snapshot) lotKit {
	kitCache.mu.Lock()
	defer kitCache.mu.Unlock()
	if kitCache.snap != snap {
		kitCache.snap, kitCache.kit = snap, newLotKit(snap)
	}
	return kitCache.kit
}

// lotExtras is the sum of a catalogue target the modules beyond the levels add, over the settlement's finished
// buildings with a function.
func lotExtras(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string, buildings []application.SettlementBuildingInstance,
	target string,
) (int64, error) {
	fns, err := tx.SettlementBuildings().FunctionsOfSettlement(ctx, settlementID)
	if err != nil || len(fns) == 0 {
		return 0, err
	}
	done := map[string]bool{}
	for _, b := range buildings {
		if b.Status == "complete" {
			done[b.ID] = true
		}
	}
	kit := kitOf(snap)
	var n int64
	for i := range fns {
		if !done[fns[i].RefID] {
			continue
		}
		if spec, ok := kit.specs[fns[i].Function]; ok {
			n += lotbuild.Provided(spec, kit.mods, compositionOf(&fns[i]), target, false)
		}
	}
	return n, nil
}

// housingNow is the households the settlement's finished buildings house: the old catalogue effects plus the
// bedrooms the owners built beyond what their levels include.
func (h *VillageHandler) housingNow(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string,
	buildings []application.SettlementBuildingInstance,
) (int64, error) {
	base := housingOf(snap, buildings)
	if !h.lot.Enabled() {
		return base, nil
	}
	extra, err := lotExtras(ctx, tx, snap, settlementID, buildings, "housing_capacity")
	return base + extra, err
}

// burnHearth burns a unit of wood from the owner's home store in the hearth of his house, when the house has one and
// the store has the wood; it reports whether the rest is warm. Item reason used (an end), referenced to the building.
func (h *VillageHandler) burnHearth(ctx context.Context, tx application.Tx, playerID, buildingID string, now time.Time) (bool, error) {
	if !h.lot.Enabled() {
		return false, nil
	}
	f, err := tx.SettlementBuildings().FunctionOf(ctx, application.BuildingRefSettlement, buildingID)
	if err != nil || f == nil || f.Modules["hearth"] < 1 {
		return false, err
	}
	fuel := h.lot.HearthFuel
	if fuel == "" {
		return false, nil
	}
	if err := tx.Items().LockOwner(ctx, playerID); err != nil {
		return false, err
	}
	err = tx.Items().Move(ctx, application.ItemMove{Item: fuel, Qty: 1, From: playerID, FromHolding: application.HoldHome,
		Reason: application.ItemUsed, ReferenceType: "settlement_building", ReferenceID: buildingID, At: now})
	if err != nil {
		if stderrors.Is(err, application.ErrNotEnoughItems) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
