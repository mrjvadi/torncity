package settlementbuilding

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// A 3x3 grid: fully buildable except (2,2), which is water; (0,0)-(0,1) is
// coastal; the rest carries no terrain tag.
func sampleGrid() Grid {
	g := make(Grid, 3)
	for y := range g {
		g[y] = make([]Lot, 3)
		for x := range g[y] {
			g[y][x] = Lot{Buildable: true}
		}
	}
	g[2][2].Buildable = false
	g[0][0].TerrainTags = []string{"coastal_lot"}
	g[1][0].TerrainTags = []string{"coastal_lot"}
	return g
}

func openStanding() Standing {
	return Standing{Knowledge: item.Set{}, Built: map[RoleTier]int{}, RunningBuilds: 0, ConcurrentCap: 1}
}

func TestCanPlaceOutOfBounds(t *testing.T) {
	def := Def{Code: "road", FootprintW: 1, FootprintH: 1}
	grid := sampleGrid()
	if err := CanPlace(def, grid, 3, 0, openStanding()); !errors.Is(err, ErrOutOfBounds) {
		t.Errorf("err = %v, want ErrOutOfBounds", err)
	}
	if err := CanPlace(def, grid, -1, 0, openStanding()); !errors.Is(err, ErrOutOfBounds) {
		t.Errorf("err = %v, want ErrOutOfBounds", err)
	}
}

func TestCanPlaceUnbuildableLot(t *testing.T) {
	def := Def{Code: "road", FootprintW: 1, FootprintH: 1}
	if err := CanPlace(def, sampleGrid(), 2, 2, openStanding()); !errors.Is(err, ErrUnbuildableLot) {
		t.Errorf("err = %v, want ErrUnbuildableLot", err)
	}
}

func TestCanPlaceOccupiedLot(t *testing.T) {
	grid := sampleGrid()
	grid[1][1].Occupied = true
	def := Def{Code: "road", FootprintW: 1, FootprintH: 1}
	if err := CanPlace(def, grid, 1, 1, openStanding()); !errors.Is(err, ErrLotOccupied) {
		t.Errorf("err = %v, want ErrLotOccupied", err)
	}
}

func TestCanPlaceTerrainRequired(t *testing.T) {
	def := Def{Code: "port", FootprintW: 1, FootprintH: 1, TerrainTags: []string{"coastal_lot"}, TerrainMode: TerrainRequired}
	grid := sampleGrid()
	if err := CanPlace(def, grid, 0, 0, openStanding()); err != nil {
		t.Errorf("a coastal lot should satisfy a port: %v", err)
	}
	if err := CanPlace(def, grid, 1, 1, openStanding()); !errors.Is(err, ErrTerrainRequired) {
		t.Errorf("an inland lot: err = %v, want ErrTerrainRequired", err)
	}
}

func TestCanPlaceKnowledgeMissing(t *testing.T) {
	def := Def{Code: "watch_hut", FootprintW: 1, FootprintH: 1, RequiresKnowledge: []string{"communal_watch"}}
	grid := sampleGrid()
	s := openStanding()
	if err := CanPlace(def, grid, 0, 1, s); !errors.Is(err, ErrKnowledgeMissing) {
		t.Errorf("err = %v, want ErrKnowledgeMissing", err)
	}
	s.Knowledge = item.NewSet("communal_watch")
	if err := CanPlace(def, grid, 1, 1, s); err != nil {
		t.Errorf("holding the knowledge should allow placement: %v", err)
	}
}

func TestCanPlaceRoleMissing(t *testing.T) {
	def := Def{Code: "police_post", FootprintW: 1, FootprintH: 1, RequiresBuildingRole: &RoleTier{Role: "security", Tier: 1}}
	grid := sampleGrid()
	s := openStanding()
	if err := CanPlace(def, grid, 1, 1, s); !errors.Is(err, ErrRoleMissing) {
		t.Errorf("err = %v, want ErrRoleMissing", err)
	}
	// Any tier-1 security building satisfies it, not one specific code
	// (ADR 0031 section 3.2's OR-mechanism).
	s.Built[RoleTier{Role: "security", Tier: 1}] = 1
	if err := CanPlace(def, grid, 1, 1, s); err != nil {
		t.Errorf("any standing tier-1 security building should satisfy the promotion: %v", err)
	}
}

func TestCanPlaceKnowledgeCapabilityMissing(t *testing.T) {
	def := Def{Code: "market", FootprintW: 1, FootprintH: 1, RequiresKnowledgeCapability: []string{"market_access"}}
	grid := sampleGrid()
	s := openStanding()
	if err := CanPlace(def, grid, 1, 1, s); !errors.Is(err, ErrKnowledgeMissing) {
		t.Errorf("err = %v, want ErrKnowledgeMissing", err)
	}
	s.KnowledgeCapabilities = item.NewSet("market_access")
	if err := CanPlace(def, grid, 1, 1, s); err != nil {
		t.Errorf("holding a capability provider should allow placement: %v", err)
	}
}

func TestCanPlaceLiteracyTooLow(t *testing.T) {
	def := Def{Code: "school", FootprintW: 1, FootprintH: 1, MinLiteracyShareBPS: 5000}
	grid := sampleGrid()
	s := openStanding()
	s.LiteracyShareBPS = 2000
	if err := CanPlace(def, grid, 1, 1, s); !errors.Is(err, ErrLiteracyTooLow) {
		t.Errorf("err = %v, want ErrLiteracyTooLow", err)
	}
	s.LiteracyShareBPS = 5000
	if err := CanPlace(def, grid, 1, 1, s); err != nil {
		t.Errorf("a met literacy threshold should pass: %v", err)
	}
}

func TestCanPlaceConcurrentBuildCap(t *testing.T) {
	def := Def{Code: "road", FootprintW: 1, FootprintH: 1}
	grid := sampleGrid()
	s := openStanding()
	s.ConcurrentCap = 1
	s.RunningBuilds = 1
	if err := CanPlace(def, grid, 1, 1, s); !errors.Is(err, ErrConcurrentBuildCap) {
		t.Errorf("err = %v, want ErrConcurrentBuildCap", err)
	}
	s.RunningBuilds = 0
	if err := CanPlace(def, grid, 1, 1, s); err != nil {
		t.Errorf("under the cap should be allowed: %v", err)
	}
}

func TestCanPlaceOverlappingFootprintChecksEveryLot(t *testing.T) {
	def := Def{Code: "civic_hall", FootprintW: 2, FootprintH: 2}
	grid := sampleGrid()
	// Anchored at (1,1) the footprint covers (1,1),(2,1),(1,2),(2,2); (2,2)
	// is water.
	if err := CanPlace(def, grid, 1, 1, openStanding()); !errors.Is(err, ErrUnbuildableLot) {
		t.Errorf("err = %v, want ErrUnbuildableLot (the footprint's far corner is water)", err)
	}
}

func TestFirstFreeLot(t *testing.T) {
	grid := sampleGrid()
	def := Def{Code: "road", FootprintW: 1, FootprintH: 1}
	x, y, ok := FirstFreeLot(def, grid)
	if !ok || x != 0 || y != 0 {
		t.Fatalf("got (%d,%d,%v), want the first free lot to be (0,0)", x, y, ok)
	}
	grid[0][0].Occupied = true
	x, y, ok = FirstFreeLot(def, grid)
	if !ok || x != 1 || y != 0 {
		t.Fatalf("got (%d,%d,%v), want (1,0) once (0,0) is occupied", x, y, ok)
	}
}

func TestFirstFreeLotNothingFits(t *testing.T) {
	grid := make(Grid, 1)
	grid[0] = []Lot{{Buildable: false}}
	def := Def{Code: "road", FootprintW: 1, FootprintH: 1}
	if _, _, ok := FirstFreeLot(def, grid); ok {
		t.Error("nothing should fit on an all-unbuildable grid")
	}
}
