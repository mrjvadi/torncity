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
	// Reserved is right-of-way (docs/adr/0043): only a road may be placed on it.
	Reserved bool
	// Obstructed is true while trees or rocks stand on the lot: it has to be cleared first (docs/adr/0065).
	Obstructed bool
	// TerrainTags are this lot's own terrain flags (coastal_lot, river_lot,
	// sloped_lot, a biome code, or a synthetic tag such as ore_deposit —
	// the identical open vocabulary settlementknowledge.Tech.TerrainTags
	// uses) for a building whose TerrainMode is TerrainRequired.
	TerrainTags []string
}

// Placed is a standing (complete) building on the land, for the proximity rules.
type Placed struct {
	Code       string
	X, Y, W, H int
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
	// KnowledgeCapabilities is every capability tag the settlement's own
	// held knowledge Provides (computed by the caller, which alone knows
	// settlementknowledge's Provides shape — see the package doc's
	// boundary). RequiresKnowledgeCapability is checked against this set.
	KnowledgeCapabilities item.Set
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
	// LiteracyShareBPS is the settlement's own current literacy_share (ADR
	// 0031 section 4.4), 0-10000.
	LiteracyShareBPS int
	// Placed lists the complete buildings, for a building that must stand near another (docs/adr/0067).
	Placed []Placed
	// SettlementTier is the settlement's own tier ("village", "town",
	// "city"); empty skips the tier rule (a caller that has none).
	SettlementTier string
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
		if lot.Reserved && def.Code != "road" {
			return ErrReservedLot
		}
		if lot.Obstructed {
			return ErrObstructed
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
	if def.Near != nil && !nearHolds(def, grid, x, y, s) {
		return ErrNeedsNear
	}
	for _, code := range def.RequiresKnowledge {
		if !s.Knowledge.Has(code) {
			return ErrKnowledgeMissing
		}
	}
	for _, capability := range def.RequiresKnowledgeCapability {
		if !s.KnowledgeCapabilities.Has(capability) {
			return ErrKnowledgeMissing
		}
	}
	if def.RequiresBuildingRole != nil && s.Built[*def.RequiresBuildingRole] < 1 {
		return ErrRoleMissing
	}
	if def.MinLiteracyShareBPS > 0 && s.LiteracyShareBPS < def.MinLiteracyShareBPS {
		return ErrLiteracyTooLow
	}
	if !def.CapExempt && s.RunningBuilds >= s.ConcurrentCap {
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

// nearHolds reports whether a lot with one of the near tags lies within the radius of the footprint (on the grid given), or
// a standing building with one of the near codes does.
func nearHolds(def Def, grid Grid, x, y int, s Standing) bool {
	n := def.Near
	if n == nil {
		return true
	}
	for j := y - n.Radius; j < y+def.FootprintH+n.Radius; j++ {
		for i := x - n.Radius; i < x+def.FootprintW+n.Radius; i++ {
			if j < 0 || i < 0 || j >= grid.Height() || i >= grid.Width() {
				continue
			}
			for _, tag := range n.Tags {
				if hasTag(grid[j][i].TerrainTags, tag) {
					return true
				}
			}
		}
	}
	for _, p := range s.Placed {
		for _, code := range n.Codes {
			if p.Code != code {
				continue
			}
			dx := max(0, x-(p.X+p.W-1), p.X-(x+def.FootprintW-1))
			dy := max(0, y-(p.Y+p.H-1), p.Y-(y+def.FootprintH-1))
			if max(dx, dy) <= n.Radius {
				return true
			}
		}
	}
	return false
}
