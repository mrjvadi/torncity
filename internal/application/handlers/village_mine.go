package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/mining"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Mining (docs/adr/0075, plan B11). A pit and a minehead dig the deposit of the world under the settlement's cell; the deposit is a
// quantity (reserve divided by config) with a grade and depth bands, and what the crews take comes off it.
//
//   - WHO WORKS THERE. The pit: labourers, as before. The minehead: miners at the face, haulers with the hand cart, a timberman who
//     sets props, a banksman at the shaft top, a pumper. A shift fills the post the mine most needs (or the one a player asks for),
//     and a player must meet the personal list of the post. A shift's post is stored on it (staff_role).
//   - WHAT IT CONSUMES. Pine splints for light below the surface, timber props for a timberman's shift, wheat for the team of a gin
//     hoist and of a pump; food and tools as every shift. The modules and the adit are paid in materials and builders' wages.
//   - WHAT IT PROVIDES. Ore of the deposit's grade band into the settlement's stock; the ore bin adds room to the yard for ore.
//     What a miner's shift delivers is the least of what the face breaks, what the hoist lifts and what the haulers carry, times the
//     grade band and the thin of the face (the shift records what limited it).
//   - WHAT BREAKS. A level whose props or water are unsafe is closed (never destroyed); the deposit runs out by depth band; no
//     hauler carries nothing; below the surface nothing is lifted without a banksman.

// WithMining gives the village handler the rules of mining, carriage and rail.
func (h *VillageHandler) WithMining(r application.MiningRules) *VillageHandler {
	h.mining = r
	return h
}

// The posts of a minehead.
const (
	RoleMiner     = "miner"
	RoleHauler    = "mine_hauler"
	RoleTimberman = "timberman"
	RoleBanksman  = "banksman"
	RolePumper    = "pumper"
)

var mineRoles = []string{RoleMiner, RoleHauler, RoleTimberman, RoleBanksman, RolePumper}

// mineJob is what a shift at a pit or a minehead is: the post, what limited it, the deposit it digs and how much of the base output it
// delivers.
type mineJob struct {
	role, limit, depositID, resource string
	depth                            int
	reserve                          int64
	// factorBPS scales the shift's output (the least of face, hoist and haul, the thin of the face and the grade band).
	factorBPS int64
}

func mineDay(now time.Time, zone time.Duration) int64 { return now.Add(zone).Unix() / 86400 }

// shiftMinutes is how long a shift lasts in game minutes (a settlement's game clock is the real clock).
func shiftMinutes(d content.SettlementBuildingDef) int64 {
	if dur, err := time.ParseDuration(d.Shift); err == nil && dur > 0 {
		return int64(dur / time.Minute)
	}
	return 30
}

// mineFall is the metres between the settlement's tile and the lowest water within reach below it (0: none lower). The adit drains by
// the fall.
func (h *VillageHandler) mineFall(ctx context.Context, s application.FoundedSettlement) (int, error) {
	w, err := h.world(ctx)
	if err != nil {
		return 0, err
	}
	if s.WorldCellID < 0 || int(s.WorldCellID) >= len(w.Cells) {
		return 0, nil
	}
	pt := w.Cells[s.WorldCellID].Point
	tile := w.TileOfLatLon(pt.LatDeg, pt.LonDeg)
	src := worldgen.NewChunkTileSource(w, 64)
	here, err := src.TileAt(tile)
	if err != nil {
		return 0, err
	}
	low := int(here.Elevation)
	for _, kind := range []worldgen.WaterKind{worldgen.WaterStream, worldgen.WaterRiver, worldgen.WaterGreatRiver, worldgen.WaterLake, worldgen.WaterOcean} {
		hit, ok, err := w.NearestWater(src, tile, 12, kind)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		ct, err := src.TileAt(hit.Tile)
		if err != nil {
			return 0, err
		}
		low = min(low, int(ct.Elevation))
	}
	return max(int(here.Elevation)-low, 0), nil
}

// mineState reads a minehead's modules (made on first sight, locked), measures the fall once and brings its levels up to today.
func (h *VillageHandler) mineState(ctx context.Context, tx application.Tx, s application.FoundedSettlement, b application.SettlementBuildingInstance,
	now time.Time,
) (application.MineState, []application.MineLevel, error) {
	repo := tx.Mines()
	ms, err := repo.EnsureState(ctx, b.ID, s.CityID, now)
	if err != nil {
		return ms, nil, err
	}
	if ms.FallM < 0 {
		fall, err := h.mineFall(ctx, s)
		if err != nil {
			return ms, nil, err
		}
		ms.FallM, ms.UpdatedAt = fall, now
		if err := repo.SetState(ctx, ms); err != nil {
			return ms, nil, err
		}
	}
	levels, err := repo.Levels(ctx, b.ID)
	if err != nil {
		return ms, nil, err
	}
	byDepth := map[int]application.MineLevel{}
	for _, l := range levels {
		byDepth[l.Depth] = l
	}
	for d := 1; d <= ms.Shafts; d++ {
		if _, ok := byDepth[d]; !ok {
			byDepth[d] = application.MineLevel{Depth: d, ConditionBPS: mining.BPS}
		}
	}
	today := mineDay(now, s.Zone())
	if ms.DayDone == 0 {
		// the first sight of the mine: its days count from now, not from before it stood
		if ok, err := repo.AdvanceDay(ctx, b.ID, 0, today); err != nil || !ok {
			return ms, nil, err
		}
		ms.DayDone = today
	} else if today > ms.DayDone {
		days := min(today-ms.DayDone, 30)
		if ok, err := repo.AdvanceDay(ctx, b.ID, ms.DayDone, today); err != nil || !ok {
			return ms, nil, err
		}
		pumped := false
		if ms.Pump >= 0 {
			n, err := repo.ShiftsSince(ctx, b.ID, RolePumper, now.AddDate(0, 0, -int(days)))
			if err != nil {
				return ms, nil, err
			}
			pumped = n > 0
		}
		for d := 1; d <= ms.Shafts; d++ {
			l := byDepth[d]
			ml := mining.Level{ConditionBPS: l.ConditionBPS, WaterBPS: l.WaterBPS}
			var out int64
			if h.aditDrains(ms, d) {
				out += h.mining.AditOutflowBPS
			}
			if pumped {
				out += h.mining.PumpGinOutflowBPS
			}
			for i := int64(0); i < days; i++ {
				ml = mining.Day(ml, h.mining.WearPerDayBPS, h.mining.InflowPerDayBPS, out)
			}
			byDepth[d] = application.MineLevel{Depth: d, ConditionBPS: ml.ConditionBPS, WaterBPS: ml.WaterBPS}
			if err := repo.PutLevel(ctx, b.ID, byDepth[d]); err != nil {
				return ms, nil, err
			}
		}
		ms.DayDone = today
	}
	out := make([]application.MineLevel, 0, len(byDepth))
	for d := 1; d <= ms.Shafts; d++ {
		out = append(out, byDepth[d])
	}
	return ms, out, nil
}

// aditNeedM is the length of adit that drains a level by gravity, false when no adit can (no water lower than the floor).
func (h *VillageHandler) aditNeedM(ms application.MineState, depth int) (int64, bool) {
	if ms.FallM <= 0 || depth < 1 || depth >= len(h.mining.Rules.DepthM) {
		return 0, false
	}
	return h.mining.Rules.AditLengthM(float64(ms.FallM), float64(h.mining.Rules.DepthM[depth]))
}

// aditDrains reports whether the adit built so far drains a level.
func (h *VillageHandler) aditDrains(ms application.MineState, depth int) bool {
	need, ok := h.aditNeedM(ms, depth)
	return ok && int64(ms.AditM) >= need
}

func (h *VillageHandler) hoistDepth(depth int) int64 {
	if depth < 0 || depth >= len(h.mining.Rules.DepthM) {
		return 0
	}
	return h.mining.Rules.DepthM[depth]
}

// roadBPS is the pace of the haulers: whole beside a road of the settlement, the unmade way's otherwise.
func (h *VillageHandler) mineRoadBPS(buildings []application.SettlementBuildingInstance, b application.SettlementBuildingInstance) int64 {
	for _, o := range buildings {
		if o.TypeCode != "road" || o.Status != "complete" {
			continue
		}
		if abs(o.LotX-b.LotX) <= 1 && abs(o.LotY-b.LotY) <= 1 {
			return mining.BPS
		}
	}
	return h.mining.Rules.UnmadeWayBPS
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// mineShape turns the standing definition of a pit or a minehead into the definition of THIS shift: the item of the deposit's band,
// the quantity the post delivers, what the post burns. It refuses a shift that would deliver nothing, with the reason. The hint is
// the post a player asked for. d.Mining nil, or mining off: the definition as it is.
func (h *VillageHandler) mineShape(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, p *application.Player, hint string,
) (content.SettlementBuildingDef, *mineJob, error) {
	m := d.Mining
	now := h.now()
	if m == nil || !h.mining.Enabled() {
		return d, nil, nil
	}
	if !m.Deep && !h.mining.On(now) {
		return d, nil, nil // the pits dig as they always did until the grace after the rule date is over
	}
	if m.Deep && now.Before(h.mining.RuleAt) {
		return d, nil, refuseVillage(village.MineNotYet, village.AddrMining)
	}
	w, err := h.world(ctx)
	if err != nil {
		return d, nil, err
	}
	dep, ok := wsettle.DepositOf(w, s.WorldCellID, m.Resource)
	if !ok {
		return d, nil, refuseVillage(village.MineNoDeposit, village.AddrMining)
	}
	rules := h.mining.Rules
	band := rules.BandOf(dep.GradePermille)
	item := m.ItemOf(string(band))
	medium := m.ItemOf(string(mining.BandMedium))
	base := d.Produces[medium]
	taken, err := tx.Mines().Deposit(ctx, dep.ID)
	if err != nil {
		return d, nil, err
	}
	job := &mineJob{depositID: dep.ID, resource: m.Resource, reserve: dep.Reserve}
	out := d
	out.Produces = map[string]int64{}
	out.Consumes = copyQty(d.Consumes)
	minutes := shiftMinutes(d)

	if !m.Deep {
		// a pit works the surface
		left := rules.Remaining(dep.Reserve, taken.Taken, mining.DepthSurface)
		if left <= 0 {
			return d, nil, refuseVillage(village.MineNoOre, village.AddrWork)
		}
		thin := rules.Thin(left, rules.Reachable(dep.Reserve, mining.DepthSurface))
		out.Produces[item] = base
		job.depth, job.factorBPS = mining.DepthSurface, thin*rules.BandOutBPS(band)/mining.BPS
		return out, job, nil
	}

	ms, levels, err := h.mineState(ctx, tx, s, b, now)
	if err != nil {
		return d, nil, err
	}
	working, err := tx.Mines().WorkingByRole(ctx, b.ID)
	if err != nil {
		return d, nil, err
	}
	fn, _ := snap.FunctionReplacing(b.TypeCode)
	def, _ := snap.BuildingFunction(fn)
	slots := map[string]int{}
	for _, st := range def.Staff {
		slots[st.Role] += st.Slots
	}
	byDepth := map[int]application.MineLevel{}
	for _, l := range levels {
		byDepth[l.Depth] = l
	}
	// the deepest level that may be worked, and what it leaves
	depth, anyClosed := -1, false
	for dp := ms.Shafts; dp >= 0; dp-- {
		if dp >= 1 {
			l := byDepth[dp]
			if rules.Check(mining.Level{ConditionBPS: l.ConditionBPS, WaterBPS: l.WaterBPS}) != mining.StatusOK {
				anyClosed = true
				continue
			}
		}
		if rules.Remaining(dep.Reserve, taken.Taken, dp) > 0 {
			depth = dp
			break
		}
	}
	role := hint
	if role == "" {
		role = h.pickMineRole(ms, byDepth, working, slots, depth, now)
	}
	if !containsString(mineRoles, role) {
		return d, nil, refuseVillage(village.MineBadRole, village.AddrWork)
	}
	if working[role] >= slots[role] {
		return d, nil, refuseVillage(village.MineRoleFull, village.AddrWork)
	}
	if p != nil && h.personal.enabled() && !h.personal.InGrace(now) {
		if rd, ok := snap.StaffRole(role); ok && len(rd.Personal) > 0 {
			st, err := h.standingOf(ctx, tx, p.ID)
			if err != nil {
				return d, nil, err
			}
			if miss := st.missing(snap, rd.Personal); len(miss) > 0 {
				return d, nil, personalRefusal(miss, village.AddrWork)
			}
		}
	}
	job.role = role
	underground := ms.Shafts >= 1
	delete(out.Consumes, "splint")
	switch role {
	case RoleMiner:
		if depth < 0 {
			if anyClosed {
				return d, nil, refuseVillage(village.MineClosed, village.AddrMining)
			}
			return d, nil, refuseVillage(village.MineNoOre, village.AddrMining)
		}
		minersNow := int64(working[RoleMiner] + 1)
		faceKg := base * h.mining.OreUnitG / 1000
		hoistKg := int64(1) << 40 // the surface needs no hoist
		if depth >= 1 {
			if ms.Hoist < 0 {
				return d, nil, refuseVillage(village.MineNoHoist, village.AddrMining)
			}
			hoistKg = rules.HoistKg(ms.Hoist, h.hoistDepth(depth), working[RoleBanksman] > 0, minutes) / minersNow
			out.Consumes["splint"] = d.Consumes["splint"]
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return d, nil, err
		}
		haulKg := rules.HaulKg(int64(working[RoleHauler]), h.mineRoadBPS(buildings, b), minutes) / minersNow
		kg, limit := mining.Throughput(faceKg, hoistKg, haulKg)
		if kg <= 0 {
			r := refuseVillage(village.MineLimited, village.AddrMining)
			r.subject = named(string(limit), string(limit))
			return d, nil, r
		}
		left := rules.Remaining(dep.Reserve, taken.Taken, depth)
		thin := rules.Thin(left, rules.Reachable(dep.Reserve, depth))
		out.Produces[item] = base
		job.depth, job.limit = depth, string(limit)
		f := kg * mining.BPS / max(faceKg, 1)
		job.factorBPS = min(f, mining.BPS) * thin / mining.BPS * rules.BandOutBPS(band) / mining.BPS
	case RoleHauler:
		if underground {
			out.Consumes["splint"] = d.Consumes["splint"]
		}
	case RoleBanksman:
		if ms.Shafts < 1 || ms.Hoist < 0 {
			return d, nil, refuseVillage(village.MineNothingToDo, village.AddrMining)
		}
		if ms.Hoist == application.MineGin {
			out.Consumes["wheat"] += h.mining.TeamFeedWheat
		}
	case RoleTimberman:
		worn := false
		for _, l := range levels {
			worn = worn || l.ConditionBPS <= mining.BPS-h.mining.SetPropsGainBPS/2
		}
		if !worn {
			return d, nil, refuseVillage(village.MineNothingToDo, village.AddrMining)
		}
		out.Consumes["splint"] = d.Consumes["splint"]
		out.Consumes["timber_prop"] += h.mining.SetPropsItems
	case RolePumper:
		wet := false
		for _, l := range levels {
			wet = wet || l.WaterBPS > 0
		}
		if ms.Pump < 0 || !wet {
			return d, nil, refuseVillage(village.MineNothingToDo, village.AddrMining)
		}
		out.Consumes["wheat"] += h.mining.TeamFeedWheat
	}
	return out, job, nil
}

// pickMineRole is the post a mine needs most: a hauler first (nothing is carried without one), a banksman when the workings are
// below the surface, a timberman when props are worn, a pumper when a level holds water, then the miners, then any free post.
func (h *VillageHandler) pickMineRole(ms application.MineState, levels map[int]application.MineLevel, working, slots map[string]int, depth int, now time.Time) string {
	free := func(role string) bool { return working[role] < slots[role] }
	worn, wet := false, false
	for _, l := range levels {
		worn = worn || l.ConditionBPS <= mining.BPS-h.mining.SetPropsGainBPS/2
		wet = wet || l.WaterBPS > 0
	}
	switch {
	case working[RoleHauler] == 0 && free(RoleHauler):
		return RoleHauler
	case depth >= 1 && ms.Hoist >= 0 && working[RoleBanksman] == 0 && free(RoleBanksman):
		return RoleBanksman
	case worn && ms.Shafts >= 1 && working[RoleTimberman] == 0 && free(RoleTimberman):
		return RoleTimberman
	case wet && ms.Pump >= 0 && working[RolePumper] == 0 && free(RolePumper):
		return RolePumper
	case free(RoleMiner):
		return RoleMiner
	}
	for _, role := range []string{RoleHauler, RoleBanksman, RoleTimberman, RolePumper} {
		if free(role) {
			return role
		}
	}
	return RoleMiner
}

// mineFinish is what a finished mining shift does to the deposit and the mine, before the shift is closed: the goods it delivers are
// trimmed to what the deposit still holds within its depth (the shift is paid on what it delivered), and the extraction is what to
// write once the shift is fresh. A shift that is not a mining shift returns nil.
func (h *VillageHandler) mineTrim(ctx context.Context, tx application.Tx, sh *application.SettlementShift, made map[string]int64, resource string, now time.Time,
) (*application.OreExtraction, error) {
	if sh.DepositID == "" || len(made) == 0 {
		return nil, nil
	}
	items := materialCodes(made)
	dep, err := tx.Mines().LockDeposit(ctx, sh.DepositID, resource, sh.DepositReserve, now)
	if err != nil {
		return nil, err
	}
	var e *application.OreExtraction
	for _, it := range items {
		grant := h.mining.Rules.Grant(made[it], sh.DepositReserve, dep.Taken, sh.DepositDepth)
		if grant < made[it] {
			if grant <= 0 {
				delete(made, it)
			} else {
				made[it] = grant
			}
		}
		if grant > 0 {
			e = &application.OreExtraction{ShiftID: sh.ID, SettlementID: sh.SettlementID, BuildingID: sh.BuildingID, DepositID: sh.DepositID, Item: it, Qty: grant, Depth: sh.DepositDepth, At: now}
			dep.Taken += grant // the next item of the same shift sees it (one item in practice)
		}
	}
	return e, nil
}

// mineAfter writes what a fresh finished shift did: the ore off the deposit, the wear of the level it worked, the props a timberman
// set, and the hour's statistics of the building.
func (h *VillageHandler) mineAfter(ctx context.Context, tx application.Tx, sh *application.SettlementShift, e *application.OreExtraction, made map[string]int64, now time.Time) error {
	repo := tx.Mines()
	if e != nil {
		fresh, err := repo.RecordExtraction(ctx, *e)
		if err != nil {
			return err
		}
		if fresh {
			if err := repo.Take(ctx, e.DepositID, e.Qty, now); err != nil {
				return err
			}
		}
	}
	switch sh.StaffRole {
	case RoleMiner:
		if sh.DepositDepth >= 1 {
			return h.bumpLevel(ctx, tx, sh.BuildingID, sh.DepositDepth, -h.mining.WearPerShiftBPS, 0, now)
		}
	case RoleTimberman:
		levels, err := repo.Levels(ctx, sh.BuildingID)
		if err != nil {
			return err
		}
		if len(levels) == 0 {
			return nil
		}
		worst := levels[0]
		for _, l := range levels {
			if l.ConditionBPS < worst.ConditionBPS {
				worst = l
			}
		}
		return h.bumpLevel(ctx, tx, sh.BuildingID, worst.Depth, h.mining.SetPropsGainBPS, 0, now)
	}
	return nil
}

// bumpLevel changes a level's condition (and water) by a delta, within its bounds.
func (h *VillageHandler) bumpLevel(ctx context.Context, tx application.Tx, buildingID string, depth int, dCondition, dWater int64, now time.Time) error {
	levels, err := tx.Mines().Levels(ctx, buildingID)
	if err != nil {
		return err
	}
	l := application.MineLevel{Depth: depth, ConditionBPS: mining.BPS}
	for _, x := range levels {
		if x.Depth == depth {
			l = x
		}
	}
	l.ConditionBPS = min(max(l.ConditionBPS+dCondition, 0), mining.BPS)
	l.WaterBPS = min(max(l.WaterBPS+dWater, 0), mining.BPS)
	return tx.Mines().PutLevel(ctx, buildingID, l)
}

// recordHour adds a finished shift to the hour's statistics of its building (the ratio view reads them).
func (h *VillageHandler) recordHour(ctx context.Context, tx application.Tx, sh *application.SettlementShift, made map[string]int64, now time.Time) error {
	if !h.haulOpen() {
		return nil
	}
	return tx.Mines().AddStats(ctx, application.NodeStat{
		BuildingID: sh.BuildingID, SettlementID: sh.SettlementID, Bucket: now.UTC().Truncate(time.Hour),
		Produced: made, Consumed: sh.Consumed, Shifts: 1,
	})
}

// mineBinRoom is the spaces of yard the ore bins of a settlement's mineheads add.
func (h *VillageHandler) mineBinRoom(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string) int64 {
	repo := tx.Mines()
	if repo == nil || !h.haulOpen() {
		return 0
	}
	mk, ok := snap.ModuleKind("ore_bin")
	if !ok {
		return 0
	}
	states, err := repo.Mines(ctx, settlementID)
	if err != nil {
		return 0
	}
	var n int64
	for _, ms := range states {
		if ms.OreBin {
			n += int64(mk.Provides["bulk"])
		}
	}
	return n
}
