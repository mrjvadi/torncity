package settlement

import "testing"

func TestPlaceFoundingKit_Deterministic(t *testing.T) {
	w := testWorld(t)
	p := testParams()

	c, err := FindSpawn(w, nil, 1, p)
	if err != nil {
		t.Fatalf("FindSpawn: %v", err)
	}

	a := PlaceFoundingKit(w, c.LatDeg, c.LonDeg, 5)
	b := PlaceFoundingKit(w, c.LatDeg, c.LonDeg, 5)
	if len(a) != len(b) {
		t.Fatalf("PlaceFoundingKit is not deterministic: %+v vs %+v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("PlaceFoundingKit is not deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestPlaceFoundingKit_HasRoadAndCivicHall(t *testing.T) {
	w := testWorld(t)
	p := testParams()

	var got []string
	// A handful of spawn points, since not every one leaves both buildings
	// placeable on a 5x5 grid (a village hugging a coastline can run out of
	// dry lots); this checks the common case reliably instead of asserting
	// it for one possibly-awkward site.
	var placedBoth int
	for n := int64(1); n <= 15; n++ {
		c, err := FindSpawn(w, nil, n, p)
		if err != nil {
			t.Fatalf("FindSpawn(%d): %v", n, err)
		}
		kit := PlaceFoundingKit(w, c.LatDeg, c.LonDeg, 5)
		got = nil
		for _, b := range kit {
			got = append(got, b.TypeCode)
		}
		if len(kit) == len(FoundingKitBuildings) {
			placedBoth++
		}
	}
	if placedBoth == 0 {
		t.Fatalf("no founding kit placed every building across 15 spawn sites; last result: %v", got)
	}
}

func TestPlaceFoundingKit_NoOverlap(t *testing.T) {
	w := testWorld(t)
	p := testParams()

	for n := int64(1); n <= 15; n++ {
		c, err := FindSpawn(w, nil, n, p)
		if err != nil {
			t.Fatalf("FindSpawn(%d): %v", n, err)
		}
		kit := PlaceFoundingKit(w, c.LatDeg, c.LonDeg, 5)
		occupied := map[[2]int]string{}
		footprints := map[string][2]int{"road": {1, 1}, "civic_hall": {2, 2}}
		for _, b := range kit {
			fw, fh := footprints[b.TypeCode][0], footprints[b.TypeCode][1]
			for y := b.LotY; y < b.LotY+fh; y++ {
				for x := b.LotX; x < b.LotX+fw; x++ {
					if x < 0 || y < 0 || x >= 5 || y >= 5 {
						t.Fatalf("spawn %d: %s lot (%d,%d) is outside the 5x5 grid", n, b.TypeCode, x, y)
					}
					key := [2]int{x, y}
					if owner, taken := occupied[key]; taken {
						t.Fatalf("spawn %d: lot (%d,%d) is claimed by both %s and %s", n, x, y, owner, b.TypeCode)
					}
					occupied[key] = b.TypeCode
				}
			}
		}
	}
}
