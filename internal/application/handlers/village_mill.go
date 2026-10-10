package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/farm"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The mill and the miller's toll (docs/adr/0067, ADR 0041 8.15).
//
//   - WHO WORKS THERE. The miller and, at a water mill, a mill hand; or a citizen grinding his own grain himself.
//   - WHAT IT CONSUMES. Wheat, a batch a shift (8 at the hand mill, 24 at the water mill): the settlement's wheat in the shifts
//     of its crews, the citizen's own from his home store when he grinds for himself.
//   - WHAT IT PROVIDES. Flour into the stock the wheat came from. The citizen who grinds his own grain pays the toll of the
//     settlement's statute in kind out of the flour: into the settlement's stock at a treasury mill, to the owner's home store
//     at a citizen's mill (his income). The statute lies between a thirtieth and a tenth of the grain, 1/20 by default.
//   - THE STATUTE. A settlement's charter lever (mill.toll), for every mill of it; the fraction a shift does not make whole is
//     carried by the mill, so many shifts add up exactly.
//
// A mill's own crew grinds the owner's grain (the treasury's wheat, the owner's wheat): no toll is asked of oneself.

// tollOf is the settlement's toll in basis points: its statute, else the content's default.
func (h *VillageHandler) tollOf(ctx context.Context, tx application.Tx, fd content.FarmingDef, settlementID string) (int64, error) {
	bps, set, err := tx.Farm().MillToll(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	if !set {
		return int64(fd.Toll.DefaultBPS), nil
	}
	return bps, nil
}

// millLine is what a mill adds to its panel: the statute and what the viewer may grind.
func (h *VillageHandler) millLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	d content.SettlementBuildingDef, viewer string,
) (*village.MillLine, error) {
	fd, ok := snap.Farming()
	if !ok || !d.Grinds {
		return nil, nil
	}
	bps, err := h.tollOf(ctx, tx, fd, s.CityID)
	if err != nil {
		return nil, err
	}
	batch := d.Consumes[farmCrop]
	flour := d.Produces[firstKey(d.Produces)]
	line := &village.MillLine{TollBPS: bps, MinBPS: int64(fd.Toll.MinBPS), MaxBPS: int64(fd.Toll.MaxBPS), Batch: batch}
	line.TollUnits = flour * bps / farm.BPS
	if viewer != "" {
		line.CanSet = hasPermission(ctx, tx, s, viewer, charter.MillToll)
		if line.Have, err = h.farmWheat(ctx, tx, snap, s, viewer); err != nil {
			return nil, err
		}
	}
	return line, nil
}

func firstKey(m map[string]int64) string {
	keys := materialCodes(m)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// VillageMillRequest names the mill to grind at, or the toll to set.
type VillageMillRequest struct {
	ID  string `json:"id,omitempty"`
	BPS string `json:"bps,omitempty"`
}

// MillToll handles settlement.mill.toll: the holder of mill.toll sets the statute of the miller's toll, inside the content's
// range.
func (h *VillageHandler) MillToll(ctx context.Context, meta envelope.Metadata, req VillageMillRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.MillTollView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		fd, ok := snap.Farming()
		if !ok {
			return refuseVillage(village.VillageNotAvailable, village.AddrWork)
		}
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.MillToll); err != nil {
			return err
		}
		bps, perr := strconv.ParseInt(strings.TrimSpace(req.BPS), 10, 64)
		if perr != nil || bps < int64(fd.Toll.MinBPS) || bps > int64(fd.Toll.MaxBPS) {
			return refuseVillage(village.MillTollRange, village.AddrWork)
		}
		if err := tx.Farm().SetMillToll(ctx, s.CityID, bps, p.ID, h.now()); err != nil {
			return err
		}
		view = village.MillTollView{Village: s.Name, TollBPS: bps, MinBPS: int64(fd.Toll.MinBPS), MaxBPS: int64(fd.Toll.MaxBPS)}
		return nil
	})
	if resp, ferr := h.villageFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return village.MillToll(h.screen(meta, lang), view), nil
}

// MillGrind handles settlement.mill.grind: a resident grinds a batch of his own wheat at a mill, himself, for the toll. The
// shift takes the wheat from his home store, holds a slot of the mill, and gives the flour (less the toll) back to his store.
func (h *VillageHandler) MillGrind(ctx context.Context, meta envelope.Metadata, req VillageMillRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.WorkView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if on, err := h.travelling(ctx, tx, p.ID); err != nil {
			return err
		} else if on {
			return application.ErrAlreadyTravelling
		}
		if ok, err := h.resident(ctx, tx, p.ID, s.CityID); err != nil {
			return err
		} else if !ok {
			return refuseVillage(village.VillageNotResident, village.AddrWork)
		}
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.ID))
		if isSentinel(err, application.ErrBuildingNotFound) || (err == nil && b.SettlementID != s.CityID) {
			return refuseVillage(village.VillageNotFound, village.AddrWork)
		}
		if err != nil {
			return err
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || !d.Grinds || b.Status != "complete" || d.Consumes[farmCrop] < 1 || len(d.Produces) == 0 {
			return refuseVillage(village.MillNotMill, village.AddrWork)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		if mine, err := tx.SettlementTreasury().PlayerShift(ctx, p.ID); err != nil {
			return err
		} else if mine != nil {
			return refuseVillage(village.VillageAlreadyWorking, village.AddrWork)
		}
		if err := h.startGrind(ctx, tx, meta, snap, s, *b, d, p); err != nil {
			return err
		}
		view, err = h.workView(ctx, tx, p, s)
		view.Started = true
		return err
	})
	if resp, ferr := h.villageFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return village.VillageWork(h.screen(meta, lang), view), nil
}

// startGrind starts the shift of a citizen grinding his own batch. Every refusal precedes the first write.
func (h *VillageHandler) startGrind(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement, b application.SettlementBuildingInstance, d content.SettlementBuildingDef, p *application.Player,
) error {
	back := village.AddrWork
	st, err := h.loadHomeStock(ctx, tx, snap, s, p.ID)
	if err != nil {
		return err
	}
	if have := st.units[farmCrop]; have < d.Consumes[farmCrop] {
		return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(d.Code, d.Name),
			[]village.VillageNeed{{Kind: village.NeedMaterial, Item: componentNamed(snap, farmCrop), Have: have, Need: d.Consumes[farmCrop]}}, back)
	}
	if grow := netGrowth(snap, d.Produces, d.Consumes); grow > st.free() {
		r := refuseVillage(village.VillageStorageFull, back)
		r.missing = grow - st.free()
		return r
	}
	now := h.now()
	zone := s.Zone()
	damage := h.damageNow(b, h.decayOf(snap, d), now, zone)
	condFactor, closed := h.conditionFactor(labor.BPS - damage)
	if closed {
		return refuseVillage(village.LaborNeedsRepair, back)
	}
	rung := h.labor.NPCProductivityBPS
	rules := len(h.labor.Levels) > 0 && h.labor.HungryOutputBPS > 0
	if !rules {
		rung = labor.BPS
	} else {
		w, err := tx.SettlementTreasury().Worker(ctx, p.ID)
		if err != nil {
			return err
		}
		rung = h.labor.LevelOf(w.Shifts).ProductivityBPS
	}
	outputBPS := rung
	if rules {
		outputBPS = outputBPS * condFactor / labor.BPS
	}
	if d.OutputTarget != "" {
		bonus, err := h.knowledgeOutputBPS(ctx, tx, snap, s.CityID, d.OutputTarget)
		if err != nil {
			return err
		}
		outputBPS = outputBPS * (labor.BPS + bonus) / labor.BPS
	}
	shiftID := h.ids.NewID()
	if err := tx.Items().Move(ctx, application.ItemMove{
		Item: farmCrop, Qty: d.Consumes[farmCrop], From: p.ID, FromHolding: application.HoldHome,
		Reason: application.ItemProductionInput, ReferenceType: application.SettlementShiftItemReference, ReferenceID: shiftID, At: now,
	}); err != nil {
		if stderrors.Is(err, application.ErrNotEnoughItems) {
			return refuseVillage(village.MillNoGrain, back)
		}
		return err
	}
	finish := now.Add(h.scale.RealWait(d.Def().Work.Shift))
	actionID, err := h.schedule(ctx, tx, application.SettlementWorkActionType, application.SettlementShiftItemReference, shiftID, s.CityID, now, finish)
	if err != nil {
		return err
	}
	sh := application.SettlementShift{
		Fed: true, OutputBPS: outputBPS, ID: shiftID, SettlementID: s.CityID, BuildingID: b.ID, Wage: 0, WorkerKind: application.LaborWorkerPlayer,
		PlayerID: p.ID, Produced: copyQty(d.Produces), Consumed: copyQty(map[string]int64{farmCrop: d.Consumes[farmCrop]}),
		FarmPhase: application.FarmPhaseGrind, CustomFor: p.ID, GameActionID: actionID, StartedAt: now, FinishAt: finish,
	}
	if err := tx.SettlementTreasury().StartShift(ctx, sh, d.Workers); err != nil {
		switch {
		case stderrors.Is(err, application.ErrWorkplaceFull):
			return refuseVillage(village.VillageWorkplaceFull, back)
		case stderrors.Is(err, application.ErrAlreadyWorking):
			return refuseVillage(village.VillageAlreadyWorking, back)
		}
		return err
	}
	if err := h.persistWear(ctx, tx, b, damage, now, zone); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "shift_started", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": shiftID, "building_id": b.ID, "type_code": b.TypeCode, "player_id": p.ID,
		"fed": true, "output_bps": outputBPS, "grind": true, "worker": sh.WorkerKind, "finish_at": finish.UTC().Format(time.RFC3339),
	})
}

// workedGrind ends a citizen's grinding shift, exactly once: the flour made (the shift's productivity applied, the fraction
// carried by the mill) goes to his home store less the toll of the statute, which goes to the settlement's stock at a treasury
// mill and to the owner's home store at a citizen's mill (as far as their stores have room; what does not fit stays his).
func (h *VillageHandler) workedGrind(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	sh *application.SettlementShift, now time.Time,
) error {
	s, err := tx.Settlements().ByID(ctx, sh.SettlementID)
	if err != nil {
		return err
	}
	fd, _ := snap.Farming()
	owner, err := h.privateOwnerOf(ctx, tx, sh.BuildingID)
	if err != nil {
		return err
	}
	carry, err := tx.SettlementTreasury().Carry(ctx, sh.BuildingID)
	if err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
		return err
	}
	if carry == nil {
		carry = map[string]int64{}
	}
	bps := sh.OutputBPS
	if bps <= 0 {
		bps = labor.BPS
	}
	flour := firstKey(sh.Produced)
	x := sh.Produced[flour]*bps + carry[flour]
	made, rest := x/labor.BPS, x%labor.BPS
	carry[flour] = rest
	tollBPS, err := h.tollOf(ctx, tx, fd, sh.SettlementID)
	if err != nil {
		return err
	}
	toll, trest := farm.Toll(made, tollBPS, carry["toll"])
	toll = min(toll, made)
	carry["toll"] = trest
	if err := tx.SettlementTreasury().SetCarry(ctx, sh.BuildingID, carry); err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
		return err
	}
	// where the toll goes, as far as it fits
	bulk := max(snap.BulkOf(flour), 1)
	toStock, toOwner := int64(0), int64(0)
	if toll > 0 {
		if owner != "" && owner != sh.CustomFor {
			ost, err := h.loadHomeStock(ctx, tx, snap, s, owner)
			if err != nil {
				return err
			}
			toOwner = min(toll, max(ost.free(), 0)/bulk)
		} else if owner == "" {
			org := application.SettlementOrg(sh.SettlementID)
			if err := tx.Items().LockOrg(ctx, org); err != nil {
				return err
			}
			buildings, err := tx.SettlementBuildings().List(ctx, sh.SettlementID)
			if err != nil {
				return err
			}
			stock, err := h.stockOf(ctx, tx, snap, sh.SettlementID, buildings)
			if err != nil {
				return err
			}
			toStock = min(toll, stock.freeSpace(stock.ClassOf(flour))/bulk)
		}
	}
	net := made - toOwner - toStock
	fresh, err := tx.SettlementTreasury().FinishPrivateShift(ctx, sh.ID, map[string]int64{flour: made}, 0, 0, "", now)
	if err != nil || !fresh {
		return err
	}
	move := func(qty int64, m application.ItemMove) error {
		if qty <= 0 {
			return nil
		}
		m.Item, m.Qty, m.Reason, m.ReferenceType, m.ReferenceID, m.At = flour, qty, application.ItemProduced, application.SettlementShiftItemReference, sh.ID, now
		return tx.Items().Move(ctx, m)
	}
	if err := move(net, application.ItemMove{To: sh.CustomFor, ToHolding: application.HoldHome}); err != nil {
		return err
	}
	if err := move(toOwner, application.ItemMove{To: owner, ToHolding: application.HoldHome}); err != nil {
		return err
	}
	if err := move(toStock, application.ItemMove{ToOrg: application.SettlementOrg(sh.SettlementID), ToHolding: application.HoldWarehouse}); err != nil {
		return err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, sh.SettlementID)
	if err != nil {
		return err
	}
	if err := h.trainOnShift(ctx, tx, snap, buildings, sh, now); err != nil {
		return err
	}
	if err := h.accrueExperience(ctx, tx, snap, buildings, sh, now); err != nil {
		return err
	}
	if err := appendVillageEvent(ctx, tx, meta, "shift_done", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": sh.ID, "building_id": sh.BuildingID, "player_id": sh.PlayerID, "worker": sh.WorkerKind,
		"produced": map[string]int64{flour: made}, "toll": toOwner + toStock, "wage": 0, "grind": true,
	}); err != nil {
		return err
	}
	return h.refillCrews(ctx, tx, meta, snap, s)
}
