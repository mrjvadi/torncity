package settlement

import (
	"math"
	"testing"
)

func TestGrowthLotsAndPrice(t *testing.T) {
	if got := GrowthLots(5); got != 11 {
		t.Errorf("GrowthLots(5) = %d, want 11 (a column of 6 and a row of 6 sharing a corner)", got)
	}
	// 11 lots x 50 = 550 for the first step; each earlier step adds 5%.
	if got := GrowthPrice(5, 0, 50, 500); got != 550 {
		t.Errorf("first step = %d, want 550", got)
	}
	if got := GrowthPrice(6, 1, 50, 500); got != 13*50*10500/10000 {
		t.Errorf("second step = %d, want %d", got, 13*50*10500/10000)
	}
	if GrowthPrice(6, 1, 50, 500) <= GrowthPrice(6, 0, 50, 500) {
		t.Error("an expansion must cost more once earlier ones were bought")
	}
}

// A grown grid must keep every existing lot on exactly the ground it stood
// on: lot (x,y) of the grown grid is the very same terrain as lot (x,y) of
// the original, for every original lot, after any number of steps.
func TestGrownGridKeepsExistingLots(t *testing.T) {
	w := testWorld(t)
	p := testParams()
	c, err := FindSpawn(w, nil, 1, p)
	if err != nil {
		t.Fatalf("FindSpawn: %v", err)
	}
	cell := w.Cells[c.CellID].Point
	baseLat, baseLon := GridCentreGrown(w, cell.LatDeg, cell.LonDeg, 1, -1, 0)
	base := SampleGridDetail(w, baseLat, baseLon, 5, c.CellID)
	for growth := 1; growth <= 4; growth++ {
		lat, lon := GridCentreGrown(w, cell.LatDeg, cell.LonDeg, 1, -1, growth)
		grown := SampleGridDetail(w, lat, lon, 5+growth, c.CellID)
		if len(grown.Lots) != 5+growth {
			t.Fatalf("growth %d: %d rows, want %d", growth, len(grown.Lots), 5+growth)
		}
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				a, b := base.Lots[y][x], grown.Lots[y][x]
				if a.Buildable != b.Buildable || a.Biome != b.Biome || math.Abs(a.ElevationM-b.ElevationM) > 1e-3 {
					t.Errorf("growth %d: lot (%d,%d) is not the ground it was: %+v vs %+v", growth, x, y, a, b)
				}
			}
		}
		if d := grown.OriginLat - base.OriginLat; math.Abs(d) > 1e-7 { // ~1 cm
			t.Errorf("growth %d: lot (0,0) moved by %g degrees of latitude", growth, d)
		}
		if d := grown.OriginLon - base.OriginLon; math.Abs(d) > 1e-7 {
			t.Errorf("growth %d: lot (0,0) moved by %g degrees of longitude", growth, d)
		}
	}
}

func TestGridCentreGrownWithNoGrowthIsGridCentre(t *testing.T) {
	w := testWorld(t)
	pt := w.Cells[0].Point
	la, lo := GridCentre(w, pt.LatDeg, pt.LonDeg, 2, -3)
	lb, lob := GridCentreGrown(w, pt.LatDeg, pt.LonDeg, 2, -3, 0)
	if la != lb || lo != lob {
		t.Errorf("GridCentreGrown with no growth = (%v,%v), GridCentre = (%v,%v)", lb, lob, la, lo)
	}
}
