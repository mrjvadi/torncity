package settlementbuilding

import "github.com/mrjvadi/torncity/internal/domain/item"

// This file is ADR 0028 section 6's placement legality, phase W5: in
// bounds, terrain not water or steep, no overlap, knowledge and role/tier
// prerequisites met, and the settlement-tier concurrent-build cap (section
// 6.3) — everything CanPlace checks before a build may be queued. Cost is
// checked and paid by the caller at the same moment (ADR 0028 section 6.3:
// "all or nothing, exactly like a production order's PlanOrder"), because
// this package has no notion of a settlement's treasury or stock.

// Lot is one lot's own state on a settlement's grid, as the caller (which
// owns the real terrain and occupancy data, ADR 0028 sections 2/6.1) reports
// it.
type Lot struct {
	// Buildable is false for open water, a lake, a river/stream channel or
	// a slope past the grid's own unbuildable band (ADR 0028 section 6.1).
	Buildable bool
	// Occupied is true once any building (queued, building or complete)
	// claims this lot.
	Occupied bool
	// TerrainTags are this lot's own terrain flags (coastal_lot, river_lot,
	// sloped_lot, a biome code, or a synthetic tag such as ore_deposit —
	// the identical open vocabulary settlementknowledge.Tech.TerrainTags
	// uses) for a building whose TerrainMode is TerrainRequired.
	TerrainTags []string
}

// Grid is a settlement's own local lot grid, addressed [y][x], 0-based,
// origin at the grid's own corner (ADR 0028 section 6.1).
type Grid [][]Lot

// Width and Height report the grid's own dimensions; 0,0 for an empty grid.
func (g Grid) Height() int { return len(g) }
func (g Grid) Width() int {
	if len(g) == 0 {
		return 0
	}
	return len(g[0])
}

// Standing is what a settlement brings to a placement decision.
type Standing struct {
	// Knowledge is the settlement's own held knowledge codes (opaque
	// strings; see the package doc).
	Knowledge item.Set
	// Built counts how many COMPLETE buildings the settlement has at each
	// role/tier — the OR-mechanism RequiresBuildingRole checks against
	// (ADR 0031 section 3.2: "any tier-1 building of that role already
	// standing, not one specific code").
	Built map[RoleTier]int
	// RunningBuilds is how many builds are currently queued or under
	// construction right now.
	RunningBuilds int
	// ConcurrentCap is the settlement's own tier's concurrent-build cap
	// (ADR 0028 section 4: 1 village / 2 town / 4 city).
	ConcurrentCap int
}

// footprintLots yields every (lx, ly) grid coordinate def's footprint would
// occupy anchored at (x, y).
func footprintLots(def Def, x, y int) [][2]int {
	out := make([][2]int, 0, def.FootprintW*def.FootprintH)
	for j := y; j < y+def.FootprintH; j++ {
		for i := x; i < x+def.FootprintW; i++ {
			out = append(out, [2]int{i, j})
		}
	}
	return out
}

// CanPlace reports whether def may be queued at (x, y) on grid now, or the
// first reason it may not: out of bounds, an unbuildable or occupied lot in
// the footprint, terrain the footprint does not satisfy, a missing
// knowledge or role/tier prerequisite, or the concurrent-build cap. Cost
// (money and materials) is the caller's own separate all-or-nothing check
// (ADR 0028 section 6.3), not this function's concern.
func CanPlace(def Def, grid Grid, x, y int, s Standing) error {
	if x < 0 || y < 0 || x+def.FootprintW > grid.Width() || y+def.FootprintH > grid.Height() {
		return ErrOutOfBounds
	}
	terrainOK := def.TerrainMode != TerrainRequired
	for _, p := range footprintLots(def, x, y) {
		lot := grid[p[1]][p[0]]
		if !lot.Buildable {
			return ErrUnbuildableLot
		}
		if lot.Occupied {
			return ErrLotOccupied
		}
		if !terrainOK {
			for _, tag := range def.TerrainTags {
				if hasTag(lot.TerrainTags, tag) {
					terrainOK = true
					break
				}
			}
		}
	}
	if !terrainOK {
		return ErrTerrainRequired
	}
	for _, code := range def.RequiresKnowledge {
		if !s.Knowledge.Has(code) {
			return ErrKnowledgeMissing
		}
	}
	if def.RequiresBuildingRole != nil && s.Built[*def.RequiresBuildingRole] < 1 {
		return ErrRoleMissing
	}
	if s.RunningBuilds >= s.ConcurrentCap {
		return ErrConcurrentBuildCap
	}
	return nil
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// FirstFreeLot is a deterministic row-major scan for the first (x, y) def
// legally fits at, ignoring knowledge/role/cap (a "where COULD this go"
// query for a screen's placement picker, not the authority CanPlace is).
// ok is false if nothing on the grid fits.
func FirstFreeLot(def Def, grid Grid) (x, y int, ok bool) {
	for gy := 0; gy <= grid.Height()-def.FootprintH; gy++ {
		for gx := 0; gx <= grid.Width()-def.FootprintW; gx++ {
			fits := true
			for _, p := range footprintLots(def, gx, gy) {
				lot := grid[p[1]][p[0]]
				if !lot.Buildable || lot.Occupied {
					fits = false
					break
				}
			}
			if fits {
				return gx, gy, true
			}
		}
	}
	return 0, 0, false
}
