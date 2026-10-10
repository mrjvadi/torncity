package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	village "github.com/mrjvadi/torncity/internal/presentation/village"
)

// Condition, wear and repair of a production workplace (roadmap 2.2 phase 5, ADR 0041 6.10; owner
// decision 2 of 2026-10-01: the money upkeep is replaced by decay and repair in kind).
//
//   - A workplace's condition is 10000 less its damage_bps. It loses decay_bps_per_day for
//     every local day of the settlement that has passed since damage_at (read lazily, written
//     when a shift starts or a repair ends, so no tick writes a row per building).
//   - From worn_bps down it works at worn_output_bps of its output; under closed_bps it is
//     closed: nothing starts there (needs_repair) until it is repaired.
//   - Repair is a job of kind repair on the hiring board: labourers (players or NPCs) work
//     shifts, each restoring 10000/repair_shifts_full of the condition; the materials are the
//     building's cost_materials x the missing share x repair_material_share_bps, taken from the
//     stock when the job is posted. The wage is the treasury's, as for any labour.
//
// Only workplaces that produce something wear: the rest of the village is unchanged.

// localDayIndex counts the settlement's local days.
func localDayIndex(t time.Time, zone time.Duration) int64 {
	return t.Add(zone).Unix() / 86400
}

// decayOf is the daily wear of a building type: the function's own maintenance decay, else
// the configured default; none for a building that produces nothing.
func (h *VillageHandler) decayOf(snap *content.Snapshot, d content.SettlementBuildingDef) int64 {
	if code, ok := snap.FunctionReplacing(d.Code); ok {
		if f, ok := snap.BuildingFunction(code); ok && f.Maintenance != nil && f.Maintenance.DecayBPSPerDay > 0 {
			// a building that makes nothing wears only when its function row says it decays: a water work silts up and
			// slumps (docs/adr/0067)
			return int64(f.Maintenance.DecayBPSPerDay)
		}
	}
	if len(d.Produces) == 0 {
		return 0
	}
	return h.labor.DecayBPSPerDay
}

// repairWage is what a repair shift of the building pays: its own wage, else the base hourly wage for the length of a
// repair shift (a water work declares no wage of its own, it makes nothing).
func (h *VillageHandler) repairWage(d content.SettlementBuildingDef) int64 {
	if d.Wage > 0 {
		return d.Wage
	}
	hours := h.shiftWait().Hours()
	return max(int64(float64(content.WorkplaceBaseHourlyWage)*hours+0.5), 1)
}

// damageNow is the building's damage today: what is stored plus the wear of the local days
// since it was last true. A building that never wore counts from its completion.
func (h *VillageHandler) damageNow(b application.SettlementBuildingInstance, decay int64, now time.Time, zone time.Duration) int64 {
	if decay <= 0 || b.Status != "complete" {
		return int64(b.DamageBPS)
	}
	since := b.QueuedAt
	if b.CompletedAt != nil {
		since = *b.CompletedAt
	}
	if b.DamageAt != nil {
		since = *b.DamageAt
	}
	days := localDayIndex(now, zone) - localDayIndex(since, zone)
	if days <= 0 {
		return int64(b.DamageBPS)
	}
	return min(int64(b.DamageBPS)+days*decay, labor.BPS)
}

// conditionFactor is the output share a condition allows (bps) and whether it is closed.
func (h *VillageHandler) conditionFactor(condition int64) (bps int64, closed bool) {
	switch {
	case condition >= h.labor.WornBPS:
		return labor.BPS, false
	case condition >= h.labor.ClosedBPS:
		return h.labor.WornOutputBPS, false
	}
	return 0, true
}

// localDayStart is defined with the crew code; wearAt is the instant a persisted wear is
// stamped with: the start of today's local day, so the next day's wear counts from there.
func wearAt(now time.Time, zone time.Duration) time.Time { return localDayStart(now, zone) }

// repairGain is the condition one repair shift restores (bps).
func (h *VillageHandler) repairGain() int64 {
	return labor.BPS / max(h.labor.RepairShiftsFull, 1)
}

// repairNeed is the shifts a damage needs and the materials it takes (the building's cost
// share by the missing part, rounded up).
func (h *VillageHandler) repairNeed(d content.SettlementBuildingDef, damage int64) (shifts int, materials map[string]int64) {
	shifts = int((damage*h.labor.RepairShiftsFull + labor.BPS - 1) / labor.BPS)
	materials = map[string]int64{}
	for item, qty := range d.CostMaterials {
		if n := (qty*damage*h.labor.RepairMaterialShareBPS + labor.BPS*labor.BPS - 1) / (labor.BPS * labor.BPS); n > 0 {
			materials[item] = n
		}
	}
	return shifts, materials
}

// workCondition is the work block's condition part.
func (h *VillageHandler) workCondition(snap *content.Snapshot, d content.SettlementBuildingDef, b application.SettlementBuildingInstance,
	now time.Time, zone time.Duration,
) *village.WorkCondition {
	decay := h.decayOf(snap, d)
	if decay <= 0 {
		return nil
	}
	damage := h.damageNow(b, decay, now, zone)
	cond := labor.BPS - damage
	factor, closed := h.conditionFactor(cond)
	shifts, mats := h.repairNeed(d, damage)
	c := &village.WorkCondition{BPS: cond, DecayBPSPerDay: decay, OutputBPS: factor, Closed: closed,
		CanRepair: cond < h.labor.RepairBelowBPS, RepairShifts: shifts}
	for item, qty := range mats {
		c.RepairMaterials = append(c.RepairMaterials, village.WorkItemLine{Item: materialLineOf(snap, item, qty).Component, Qty: qty})
	}
	return c
}

// persistWear writes the building's wear up to today when it has changed.
func (h *VillageHandler) persistWear(ctx context.Context, tx application.Tx, b application.SettlementBuildingInstance, damage int64,
	now time.Time, zone time.Duration,
) error {
	if damage == int64(b.DamageBPS) {
		return nil
	}
	return tx.SettlementBuildings().SetDamage(ctx, b.ID, int(damage), wearAt(now, zone))
}

// postRepair posts the repair job of a worn workplace: the shifts its damage needs, the
// materials taken from the stock now (item reason repair_materials). Idempotent: a second post
// while the job is open changes nothing.
func (h *VillageHandler) postRepair(ctx context.Context, tx application.Tx, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, by string,
) error {
	snap := h.content.Current()
	d, ok := snap.SettlementBuildingDef(b.TypeCode)
	back := village.AddrLaborSite + ":" + b.ID
	if !ok || h.decayOf(snap, d) <= 0 {
		return refuseVillage(village.LaborNoSite, back)
	}
	repo := tx.SettlementTreasury()
	if open, err := repo.JobOfBuildingKind(ctx, b.ID, application.LaborKindRepair); err != nil || open != nil {
		return err
	}
	now := h.now()
	zone := s.Zone()
	damage := h.damageNow(b, h.decayOf(snap, d), now, zone)
	if labor.BPS-damage >= h.labor.RepairBelowBPS {
		return refuseVillage(village.VillageNotAvailable, back)
	}
	shifts, mats := h.repairNeed(d, damage)
	if owner, oerr := h.privateOwnerOf(ctx, tx, b.ID); oerr != nil {
		return oerr
	} else if owner != "" {
		return h.postPrivateRepair(ctx, tx, s, b, d, owner, by, damage, shifts, mats, now, zone)
	}
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return err
	}
	pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
	if err != nil {
		return err
	}
	if needs := pc.materialNeeds(mats); len(needs) > 0 {
		return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(d.Code, d.Name), needs, back)
	}
	jobID := h.ids.NewID()
	for _, item := range materialCodes(mats) {
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: item, Qty: mats[item], FromOrg: application.SettlementOrg(s.CityID), FromHolding: application.HoldWarehouse,
			Reason: application.ItemRepairMaterials, ReferenceType: application.RepairReference, ReferenceID: jobID, At: now,
		}); err != nil {
			return err
		}
	}
	if err := h.persistWear(ctx, tx, b, damage, now, zone); err != nil {
		return err
	}
	return repo.PostJob(ctx, application.LaborJob{
		ID: jobID, SettlementID: s.CityID, BuildingID: b.ID, Kind: application.LaborKindRepair,
		EmployerKind: application.LaborEmployerSettlement, EmployerID: s.CityID, Wage: h.repairWage(d),
		ShiftsTotal: shifts, CreatedBy: by, CreatedAt: now,
	})
}

// repairDone ends a finished repair shift: the condition rises by the shift's gain; when the
// workplace is whole the job closes, else the crew goes on.
func (h *VillageHandler) repairDone(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	s application.FoundedSettlement, sh *application.SettlementShift, pay int64, now time.Time,
) error {
	b, err := tx.SettlementBuildings().Get(ctx, sh.BuildingID)
	if err != nil {
		return err
	}
	d, _ := snap.SettlementBuildingDef(b.TypeCode)
	zone := s.Zone()
	damage := max(h.damageNow(*b, h.decayOf(snap, d), now, zone)-sh.ConditionGain, 0)
	if err := tx.SettlementBuildings().SetDamage(ctx, b.ID, int(damage), wearAt(now, zone)); err != nil {
		return err
	}
	if damage == 0 && sh.JobID != "" {
		if err := tx.SettlementTreasury().CloseJob(ctx, sh.JobID, now); err != nil {
			return err
		}
	}
	if err := appendVillageEvent(ctx, tx, meta, "shift_done", s.CityID, map[string]any{
		"settlement_id": s.CityID, "shift_id": sh.ID, "building_id": sh.BuildingID, "player_id": sh.PlayerID, "worker": sh.WorkerKind,
		"kind": sh.Kind, "wage": pay, "condition_gain": sh.ConditionGain, "damage": damage,
	}); err != nil {
		return err
	}
	if damage > 0 && sh.JobID != "" {
		if _, err := h.fillCrew(ctx, tx, meta, snap, s, sh.JobID); err != nil {
			var r *villageRefusal
			if !stderrors.As(err, &r) {
				return err
			}
		}
	}
	return nil
}
