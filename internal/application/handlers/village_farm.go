package handlers

import (
	"context"
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

// The farm cycle (docs/adr/0067, ADR 0041 7.6, 8.17). A farm is sown, grows, ripens and is harvested; the harvest is the
// crop's base x the soil x the water x the tending.
//
//   - WHO WORKS THERE. Farmers, four at a time: players or NPCs of the pool, in shifts of the farm. The work comes in peaks:
//     sowing, harvest; tending in between.
//   - WHAT IT CONSUMES. The seed, wheat, at the first sowing shift (from the settlement's stock, or from the owner's home
//     store on a private farm); meals at the sowing and tending shifts; a tool's wear at every shift. Harvest workers eat from
//     the crop.
//   - WHAT IT PROVIDES. At each harvest shift its share of the yield, into the settlement's stock (or the owner's home store).
//     A store with no room refuses the harvest shift and the crop waits in the field, spoiling past its window.
//   - HOW WORKERS WORK. The stage decides the shift: sow (the first of them takes the seed), tend (while the crop grows), harvest
//     (once it is ripe). A shift that has no work to do (nothing sown; every tending shift done) is refused.
//   - LINKS. Water work -> farm -> wheat -> granary -> mill -> bakery; the water factor comes from the work of the farm's branch
//     within reach whose water master was on duty today and whose condition is at least half.
//   - NOTHING TICKS. The stage is read from the crop's counters and the clock (domain/farm); an N-replica reader sees the same.
//
// A farm that stood before the rule date keeps its flat shift (the content's produces) until the grace is over, so no live
// settlement breaks overnight.

// WithFarm switches the farm cycle on (docs/adr/0067).
func (h *VillageHandler) WithFarm(r application.FarmRules) *VillageHandler {
	h.farmRules = r
	return h
}

// farmKit is a farm building's content: the farming block, the pure config and the branch it draws from.
type farmKit struct {
	def    content.FarmingDef
	cfg    farm.Config
	branch content.FarmingBranchDef
	base   string
}

// farmKitOf finds the farming content of a building code (the citizen twin counts as its public farm).
func farmKitOf(snap *content.Snapshot, typeCode string) (farmKit, bool) {
	fd, ok := snap.Farming()
	if !ok {
		return farmKit{}, false
	}
	base := strings.TrimSuffix(typeCode, content.PrivateSuffix)
	br, ok := fd.BranchOf(base)
	if !ok {
		return farmKit{}, false
	}
	return farmKit{def: fd, cfg: fd.Cycle(), branch: br, base: base}, true
}

// crop is the item the farms grow.
const farmCrop = "wheat"

// sitesOf are the farms of the branch and the water works of it on the land.
func (h *VillageHandler) sitesOf(snap *content.Snapshot, kit farmKit, buildings []application.SettlementBuildingInstance) (works, farms []farm.Site) {
	for _, x := range buildings {
		d, ok := snap.SettlementBuildingDef(x.TypeCode)
		if !ok {
			continue
		}
		def := d.Def()
		if x.Rotated {
			def = def.Rotate()
		}
		site := farm.Site{ID: x.ID, X: x.LotX, Y: x.LotY, W: def.FootprintW, H: def.FootprintH}
		switch {
		case kit.branch.Work != "" && x.TypeCode == kit.branch.Work && x.Status == "complete":
			works = append(works, site)
		case strings.TrimSuffix(x.TypeCode, content.PrivateSuffix) == kit.base && x.Holds():
			farms = append(farms, site)
		}
	}
	return works, farms
}

// waterState is the water a farm draws from now.
type waterState struct {
	factor int64
	line   *village.FarmWater
}

// workOpenToday reports whether the water master of a work is on duty today (the day's service judgement is made now if
// nobody has made it).
func (h *VillageHandler) workOpenToday(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, workID string,
) (bool, error) {
	day, err := h.SettleServiceDay(ctx, tx, snap, s, buildings)
	if err != nil || day == nil {
		return false, err
	}
	return day.HeldPost(workID), nil
}

// waterOf is the water factor of the farm b now: the work of its branch within reach that serves it, its master on duty
// today and its condition. A rain-fed farm needs none.
func (h *VillageHandler) waterOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	kit farmKit, b application.SettlementBuildingInstance, buildings []application.SettlementBuildingInstance, now time.Time,
) (waterState, error) {
	if kit.branch.Rainfed {
		return waterState{factor: kit.cfg.WaterServedBPS}, nil
	}
	works, farms := h.sitesOf(snap, kit, buildings)
	serving := farm.Serving(works, farms, kit.cfg.Reach, kit.cfg.Serves)
	line := &village.FarmWater{}
	workID := serving[b.ID]
	if workID == "" {
		line.Reason = "no_work"
		for _, w := range works {
			for _, f := range farms {
				if f.ID == b.ID && farm.Gap(w, f) <= kit.cfg.Reach {
					line.Reason = "not_served"
				}
			}
		}
		return waterState{factor: kit.cfg.WaterUnservedBPS, line: line}, nil
	}
	var work application.SettlementBuildingInstance
	for _, x := range buildings {
		if x.ID == workID {
			work = x
		}
	}
	wd, _ := snap.SettlementBuildingDef(work.TypeCode)
	nm := named(wd.Code, wd.Name)
	line.Work, line.Served = &nm, true
	line.ConditionBPS = labor.BPS - h.damageNow(work, h.decayOf(snap, wd), now, s.Zone())
	open, err := h.workOpenToday(ctx, tx, snap, s, buildings, workID)
	if err != nil {
		return waterState{}, err
	}
	line.Open = open
	switch {
	case !open:
		line.Reason = "no_master"
	case line.ConditionBPS < kit.cfg.MinConditionBPS:
		line.Reason = "worn"
	}
	if line.Reason != "" {
		return waterState{factor: kit.cfg.WaterUnservedBPS, line: line}, nil
	}
	return waterState{factor: kit.cfg.WaterServedBPS, line: line}, nil
}

// farmSoil is the soil factor of the farm at (x, y): its lots' biomes, less the slope.
func (h *VillageHandler) farmSoil(ctx context.Context, tx application.Tx, s application.FoundedSettlement, kit farmKit,
	def content.SettlementBuildingDef, x, y int,
) (int64, error) {
	pic, err := h.pictureOf(ctx, tx, s)
	if err != nil {
		return 0, err
	}
	var biomes []int64
	steep := 0
	for _, q := range footprintOf(def.Def(), x, y) {
		lot := pic.lotAt(q[0], q[1])
		bps := int64(kit.def.SoilDefaultBPS)
		for _, tag := range lot.TerrainTags {
			for _, sd := range kit.def.Soil {
				if sd.Biome == tag {
					bps = int64(sd.BPS)
				}
			}
			if tag == "sloped_lot" {
				steep++
			}
		}
		biomes = append(biomes, bps)
	}
	return farm.Soil(biomes, steep, int64(kit.def.SteepPenaltyBPS), int64(kit.def.SoilDefaultBPS)), nil
}

// farmJob is a farm shift in the making: the crop with the shift counted in, the phase and the def shaped for it.
type farmJob struct {
	cycle *farm.Cycle
	phase string
	def   content.SettlementBuildingDef
}

// farmShape decides what the shift at the farm b is, from its crop's stage: nil when the building is not a farm of the cycle
// (or still keeps its flat shift). Nothing is written; the crop with the shift counted in is saved by applyFarm once the shift
// is started. owner is the citizen who owns the farm ("" for the treasury's).
func (h *VillageHandler) farmShape(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, owner string, now time.Time,
) (*farmJob, error) {
	kit, ok := farmKitOf(snap, b.TypeCode)
	if !ok || !h.farmRules.OnCycle(b, now) {
		return nil, nil
	}
	repo := tx.Farm()
	if err := repo.Lock(ctx, b.ID); err != nil {
		return nil, err
	}
	cy, err := repo.Open(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	stage := farm.StageAt(cy, kit.cfg, now)
	if stage == farm.StageIdle || stage == farm.StageHarvested || stage == farm.StageRotted {
		return nil, refuseVillage(village.FarmIdle, village.AddrWork)
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	water, err := h.waterOf(ctx, tx, snap, s, kit, b, buildings, now)
	if err != nil {
		return nil, err
	}
	finish := now.Add(h.scale.RealWait(d.Def().Work.Shift))
	job := &farmJob{cycle: cy, def: d}
	job.def.Produces = map[string]int64{}
	job.def.Consumes = copyQty(d.Consumes)
	switch stage {
	case farm.StageSowing:
		job.phase = application.FarmPhaseSow
		if cy.SowStarted == 0 {
			seed := cy.Seed(kit.cfg)
			job.def.Consumes[farmCrop] += seed
			cy.SeedSpent = seed
		}
		cy.SowStarted++
		if cy.SowStarted >= kit.cfg.SowShifts {
			gf := finish
			cy.GrowFrom = &gf
			// the crews that wait for the crop start again when it is ripe
			if _, err := h.schedule(ctx, tx, application.FarmRipeActionType, application.FarmReference, cy.ID, s.CityID, now, cy.RipeAt(kit.cfg)); err != nil {
				return nil, err
			}
		}
		cy.WaterSum += water.factor
		cy.WaterN++
	case farm.StageGrowing:
		if cy.Tended >= kit.cfg.TendMax {
			return nil, refuseVillage(village.FarmWaiting, village.AddrWork)
		}
		job.phase = application.FarmPhaseTend
		cy.Tended++
		cy.WaterSum += water.factor
		cy.WaterN++
	default: // ripe, overripe, harvest
		job.phase = application.FarmPhaseHarvest
		if cy.HarvestStarted == 0 {
			cy.WaterSum += water.factor
			cy.WaterN++
			cy.YieldTotal = cy.Yield(kit.cfg, now)
		}
		if share := farm.ShareOfHarvest(cy.YieldTotal, cy.HarvestStarted, kit.cfg.HarvestShifts); share > 0 {
			job.def.Produces[farmCrop] = share
		}
		cy.HarvestStarted++
	}
	return job, nil
}

// applyFarm saves the crop with the shift counted in.
func (h *VillageHandler) applyFarm(ctx context.Context, tx application.Tx, job *farmJob) error {
	if job == nil {
		return nil
	}
	return tx.Farm().Save(ctx, *job.cycle)
}

// farmStock is the wheat the farm's seed would come from: the settlement's stock, or the owner's home store.
func (h *VillageHandler) farmWheat(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, owner string) (int64, error) {
	if owner != "" {
		_, stacks, _, err := homeUsed(ctx, tx, snap, owner)
		if err != nil {
			return 0, err
		}
		var n int64
		for _, st := range stacks {
			if st.Item == farmCrop {
				n += st.Qty
			}
		}
		return n, nil
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return 0, err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return 0, err
	}
	return stock.Units[farmCrop], nil
}

// farmLine is the crop of the farm b as a view shows it: nil when the building is not a farm of the cycle or the cycle is off.
func (h *VillageHandler) farmLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, buildings []application.SettlementBuildingInstance, viewer string,
) (*village.FarmLine, error) {
	kit, ok := farmKitOf(snap, b.TypeCode)
	if !ok || !h.farmRules.Enabled() {
		return nil, nil
	}
	now := h.now()
	line := &village.FarmLine{Stage: string(farm.StageIdle), Rainfed: kit.branch.Rainfed,
		SowNeed: kit.cfg.SowShifts, TendMax: kit.cfg.TendMax, HarvestNeed: kit.cfg.HarvestShifts}
	if !h.farmRules.OnCycle(b, now) {
		line.Legacy = true
		if u := h.farmRules.GraceUntil(); !u.IsZero() {
			line.LegacyUntil = &u
		}
		return line, nil
	}
	owner, err := h.privateOwnerOf(ctx, tx, b.ID)
	if err != nil {
		return nil, err
	}
	cy, err := tx.Farm().Open(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	stage := farm.StageAt(cy, kit.cfg, now)
	line.Stage = string(stage)
	water, err := h.waterOf(ctx, tx, snap, s, kit, b, buildings, now)
	if err != nil {
		return nil, err
	}
	line.Water = water.line
	seed := kit.cfg.SeedIrrigated
	if kit.branch.Rainfed {
		seed = kit.cfg.SeedRainfed
	}
	line.Seed = seed
	if line.SeedHave, err = h.farmWheat(ctx, tx, snap, s, owner); err != nil {
		return nil, err
	}
	line.Factors = village.FarmFactor{Water: water.factor, Tending: farm.BPS}
	if cy != nil {
		line.SowDone, line.Tended, line.HarvestDone = cy.SowStarted, cy.Tended, cy.HarvestStarted
		line.Seed = cy.Seed(kit.cfg)
		if t := cy.RipeAt(kit.cfg); !t.IsZero() {
			line.RipeAt = &t
			sp := cy.SpoilAt(kit.cfg)
			line.SpoilAt = &sp
		}
		line.Factors = village.FarmFactor{Soil: cy.SoilBPS, Water: cy.WaterAvg(kit.cfg), Tending: cy.TendingBPS(kit.cfg), Loss: cy.LossBPS(kit.cfg, now)}
		if cy.WaterN == 0 {
			line.Factors.Water = water.factor
		}
		switch {
		case cy.HarvestStarted > 0:
			line.Expected = cy.YieldTotal
		default:
			est := *cy
			if est.WaterN == 0 {
				est.WaterSum, est.WaterN = water.factor, 1
			}
			line.Expected = est.Yield(kit.cfg, now)
		}
	}
	if viewer != "" && farm.CanOrder(cy, kit.cfg, now) {
		if owner != "" {
			line.CanSow = owner == viewer
		} else if ok, err := h.mayVillage(ctx, tx, s, viewer, charter.FarmSow); err == nil && ok {
			line.CanSow = true
		}
	}
	return line, nil
}

// VillageFarmRequest names the farm to sow.
type VillageFarmRequest struct {
	ID string `json:"id"`
}

// FarmSow handles settlement.farm.sow: the head (the farm.sow permission) for the treasury's farm, the owner for his own,
// orders the sowing. The crop of the farm before it, if harvested or rotted, is closed. The seed is taken by the first sowing
// shift, not by the order.
func (h *VillageHandler) FarmSow(ctx context.Context, meta envelope.Metadata, req VillageFarmRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.FarmSowView
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
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.ID))
		if isSentinel(err, application.ErrBuildingNotFound) || (err == nil && b.SettlementID != s.CityID) {
			return refuseVillage(village.VillageNotFound, village.AddrWork)
		}
		if err != nil {
			return err
		}
		kit, ok := farmKitOf(snap, b.TypeCode)
		d, dok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || !dok || b.Status != "complete" {
			return refuseVillage(village.FarmNotFarm, village.AddrWork)
		}
		now := h.now()
		if !h.farmRules.OnCycle(*b, now) {
			return refuseVillage(village.FarmLegacy, village.AddrWork)
		}
		owner, err := h.privateOwnerOf(ctx, tx, b.ID)
		if err != nil {
			return err
		}
		if owner != "" {
			if owner != p.ID {
				return refuseVillage(village.LotNotYours, village.AddrWork)
			}
		} else if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.FarmSow); err != nil {
			return err
		}
		repo := tx.Farm()
		if err := repo.Lock(ctx, b.ID); err != nil {
			return err
		}
		old, err := repo.Open(ctx, b.ID)
		if err != nil {
			return err
		}
		if !farm.CanOrder(old, kit.cfg, now) {
			return refuseVillage(village.FarmBusy, village.AddrWork)
		}
		if old != nil {
			result := "harvested"
			if farm.StageAt(old, kit.cfg, now) == farm.StageRotted {
				result = "rotted"
			}
			if err := repo.Close(ctx, old.ID, now, result); err != nil {
				return err
			}
		}
		soil, err := h.farmSoil(ctx, tx, s, kit, d, b.LotX, b.LotY)
		if err != nil {
			return err
		}
		if err := repo.Insert(ctx, farm.Cycle{ID: h.ids.NewID(), SettlementID: s.CityID, BuildingID: b.ID, Rainfed: kit.branch.Rainfed,
			SoilBPS: soil, OrderedBy: p.ID, OrderedAt: now}); err != nil {
			return err
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		line, err := h.farmLine(ctx, tx, snap, s, *b, buildings, p.ID)
		if err != nil {
			return err
		}
		view = village.FarmSowView{Village: s.Name, Farm: named(d.Code, d.Name), Line: *line}
		return h.appendBuildingEvent(ctx, tx, meta, s, "land_changed", map[string]any{
			"settlement_id": s.CityID, "kind": "farm_sown", "x": b.LotX, "y": b.LotY,
		})
	})
	if resp, ferr := h.villageFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return village.FarmSow(h.screen(meta, lang), view), nil
}

// FarmRipe handles settlement.farm.ripe from the SCHEDULER: a crop has ripened, so the crews that waited for it start again.
// Starting the crews again is idempotent, a redelivery changes nothing.
func (h *VillageHandler) FarmRipe(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		s, err := tx.Settlements().ByID(ctx, in.SettlementID)
		if isSentinel(err, application.ErrCityNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return h.refillCrews(ctx, tx, meta, snap, s)
	})
}
