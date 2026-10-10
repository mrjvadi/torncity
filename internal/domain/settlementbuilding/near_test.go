package settlementbuilding

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

func nearGrid(w, h int) Grid {
	g := make(Grid, h)
	for y := range g {
		g[y] = make([]Lot, w)
		for x := range g[y] {
			g[y][x] = Lot{Buildable: true}
		}
	}
	return g
}

// A building that must stand near a terrain tag or a standing building of a code (docs/adr/0067): the tag or the building
// within the radius of the footprint satisfies it; either one.
func TestNearTagOrBuildingSatisfiesTheRule(t *testing.T) {
	g := nearGrid(12, 12)
	g[1][8].TerrainTags = []string{"river_lot"}
	canal := Def{Code: "canal_channel", FootprintW: 1, FootprintH: 1, Near: &Near{Radius: 3, Tags: []string{"river_lot"}}}
	if err := CanPlace(canal, g, 6, 1, Standing{ConcurrentCap: 9}); err != nil {
		t.Errorf("a river two lots away is near enough: %v", err)
	}
	if err := CanPlace(canal, g, 2, 1, Standing{ConcurrentCap: 9}); !errors.Is(err, ErrNeedsNear) {
		t.Errorf("a river six lots away is not: %v", err)
	}
	farm := Def{Code: "farm_canal", FootprintW: 3, FootprintH: 3, Near: &Near{Radius: 5, Codes: []string{"canal_channel"}}}
	st := Standing{ConcurrentCap: 9, Knowledge: item.Set{}}
	if err := CanPlace(farm, g, 0, 6, st); !errors.Is(err, ErrNeedsNear) {
		t.Errorf("no canal stands: %v", err)
	}
	st.Placed = []Placed{{Code: "canal_channel", X: 7, Y: 6, W: 1, H: 1}}
	if err := CanPlace(farm, g, 0, 6, st); err != nil {
		// the farm ends at x 2, the canal at x 7: five lots on, in reach
		t.Errorf("a canal five lots on from the farm serves it: %v", err)
	}
	st.Placed = []Placed{{Code: "canal_channel", X: 8, Y: 6, W: 1, H: 1}}
	if err := CanPlace(farm, g, 0, 6, st); !errors.Is(err, ErrNeedsNear) {
		t.Errorf("a canal six lots on is out of reach: %v", err)
	}
	st.Placed = []Placed{{Code: "shaft_well", X: 3, Y: 6, W: 1, H: 1}}
	if err := CanPlace(farm, g, 0, 6, st); !errors.Is(err, ErrNeedsNear) {
		t.Errorf("a work of another branch does not serve: %v", err)
	}
}
