package settlement

import "testing"

func TestSampleGridDeterministicAndShaped(t *testing.T) {
	w := testWorld(t)
	p := testParams()
	c, err := FindSpawn(w, nil, 1, p)
	if err != nil {
		t.Fatalf("FindSpawn: %v", err)
	}

	a := SampleGrid(w, c.LatDeg, c.LonDeg, 5, c.CellID)
	b := SampleGrid(w, c.LatDeg, c.LonDeg, 5, c.CellID)

	if len(a) != 5 {
		t.Fatalf("grid has %d rows, want 5", len(a))
	}
	for y, row := range a {
		if len(row) != 5 {
			t.Fatalf("row %d has %d lots, want 5", y, len(row))
		}
		for x, lot := range row {
			if lot.Buildable != b[y][x].Buildable {
				t.Errorf("lot (%d,%d) buildable is not deterministic", x, y)
			}
			if len(lot.Tags) != len(b[y][x].Tags) {
				t.Errorf("lot (%d,%d) tags are not deterministic: %v vs %v", x, y, lot.Tags, b[y][x].Tags)
			}
		}
	}

	// The centre lot, at least, should be buildable (FindSpawn already
	// refused an ocean/lake cell), and every lot should carry a biome tag.
	center := a[2][2]
	if !center.Buildable {
		t.Error("the centre lot of a spawned settlement should be buildable")
	}
	if len(center.Tags) == 0 {
		t.Error("every lot should carry at least a biome tag")
	}
}

func TestSampleGridSmallestGrid(t *testing.T) {
	w := testWorld(t)
	p := testParams()
	c, err := FindSpawn(w, nil, 1, p)
	if err != nil {
		t.Fatalf("FindSpawn: %v", err)
	}
	g := SampleGrid(w, c.LatDeg, c.LonDeg, 1, c.CellID)
	if len(g) != 1 || len(g[0]) != 1 {
		t.Fatalf("a gridLots=1 grid should be 1x1, got %dx%d", len(g), len(g[0]))
	}
}
