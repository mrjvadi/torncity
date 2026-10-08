package settlement

import (
	"errors"
	"math"
	"testing"
)

func testCircleParams() CircleParams {
	return CircleParams{RadiusKm: 700, Capacity: 5, FillBandKm: 150, MaxAdvance: 60}
}

func TestHexOffsetWalksEveryRingOnce(t *testing.T) {
	seen := map[[2]int]bool{{0, 0}: true}
	prevQ, prevR := 0, 0
	for k := 1; k <= 3*4*5; k++ { // rings 1..4
		q, r := HexOffset(k)
		if seen[[2]int{q, r}] {
			t.Fatalf("circle %d repeats %d,%d", k, q, r)
		}
		seen[[2]int{q, r}] = true
		ring := (abs(q) + abs(r) + abs(q+r)) / 2
		wantRing := 1
		for 3*wantRing*(wantRing+1) < k {
			wantRing++
		}
		if ring != wantRing {
			t.Fatalf("circle %d is on ring %d, want %d", k, ring, wantRing)
		}
		// the spiral is connected: each circle touches the one before it, or the first of a ring touches the last of
		// the ring before it's neighbourhood (distance at most 2 in hex steps)
		d := (abs(q-prevQ) + abs(r-prevR) + abs((q-prevQ)+(r-prevR))) / 2
		if d > 2 {
			t.Fatalf("circle %d is %d hex steps from circle %d", k, d, k-1)
		}
		prevQ, prevR = q, r
	}
}

func TestCircleCentresAreOneSpacingApart(t *testing.T) {
	w := testWorld(t)
	R := w.Params.PlanetRadiusKm
	for k := 1; k <= 6; k++ {
		lat, lon := CircleCentre(10, 20, k, 300, R)
		x1, y1, z1 := unitOf(10, 20)
		x2, y2, z2 := unitOf(lat, lon)
		d := math.Acos(clamp(x1*x2+y1*y2+z1*z2, -1, 1)) * R
		if math.Abs(d-CircleSpacingKm(300)) > 5 {
			t.Errorf("circle %d is %.1f km from the first, want %.1f", k, d, CircleSpacingKm(300))
		}
	}
	if lat, lon := CircleCentre(10, 20, 0, 300, R); lat != 10 || lon != 20 {
		t.Error("circle 0 is the first centre")
	}
}

// fill founds n settlements one after another the way the application does, carrying the circle state.
func fill(t *testing.T, n int, existing []ExistingSettlement, cur, first *Circle, cp CircleParams) ([]ExistingSettlement, []SpawnPlan, *Circle, *Circle) {
	t.Helper()
	w := testWorld(t)
	var plans []SpawnPlan
	circles := map[int]*Circle{}
	if cur != nil {
		circles[cur.Index] = cur
	}
	for i := 0; i < n; i++ {
		plan, err := PlanSpawn(w, existing, cur, first, cp, testParams())
		if err != nil {
			t.Fatalf("founding %d: %v", i, err)
		}
		for _, o := range plan.Opened {
			o := o
			circles[o.Index] = &o
			if o.Index == 0 {
				first = &o
			}
		}
		cur = circles[plan.Circle]
		cur.Count++
		existing = append(existing, ExistingSettlement{CellID: plan.Cand.CellID, TierWeight: 1})
		plans = append(plans, plan)
	}
	return existing, plans, cur, first
}

func TestFoundingsFillACircleThenAdvance(t *testing.T) {
	w := testWorld(t)
	cp := testCircleParams()
	// a settlement already stands: the first circle is centred on it and counts it
	land := int32(-1)
	for id := range w.Cells {
		if !w.Cells[id].IsOcean && !w.Cells[id].IsLake && math.Abs(w.Cells[id].Point.LatDeg) < 40 && w.Cells[id].Point.LonDeg > 0 {
			land = int32(id)
			break
		}
	}
	if land < 0 {
		t.Skip("no land in the test world")
	}
	existing := []ExistingSettlement{{CellID: land, TierWeight: 1}}
	all, plans, _, first := fill(t, 14, existing, nil, nil, cp)
	if first == nil || w.NearestCell(first.LatDeg, first.LonDeg) != land {
		t.Fatalf("the first circle is not centred on the existing settlement: %+v", first)
	}
	// spacing and habitability hold for every founding
	for i, a := range all {
		for _, b := range all[i+1:] {
			if d := greatCircleKm(w, a.CellID, b.CellID); d < testParams().MinSpawnDistanceKm {
				t.Fatalf("two settlements %.1f km apart", d)
			}
		}
		if i > 0 && (w.Cells[a.CellID].IsOcean || w.Cells[a.CellID].IsLake) {
			t.Fatalf("a settlement stands on water")
		}
	}
	// no circle took more than its capacity, circles close in order and the later foundings moved on
	taken := map[int]int{}
	closed := map[int]string{}
	for _, p := range plans {
		taken[p.Circle]++
		for _, c := range p.Closed {
			closed[c.Index] = c.Reason
		}
	}
	for idx, n := range taken {
		if n > cp.Capacity {
			t.Errorf("circle %d took %d foundings, capacity %d", idx, n, cp.Capacity)
		}
	}
	if len(taken) < 2 {
		t.Fatalf("14 foundings stayed in %d circle(s): %v", len(taken), taken)
	}
	if closed[0] == "" {
		t.Errorf("the first circle never closed: %v", closed)
	}
	// every founding sits inside its own circle
	circles := map[int]Circle{}
	for _, p := range plans {
		for _, o := range p.Opened {
			circles[o.Index] = o
		}
		c := circles[p.Circle]
		if d := cellToPointKm(w, p.Cand.CellID, c.LatDeg, c.LonDeg); d > c.RadiusKm {
			t.Errorf("a founding stands %.0f km from the centre of a %.0f km circle", d, c.RadiusKm)
		}
	}
	// the settled region is connected: every settlement is within two circle radii of another one
	for _, a := range all {
		near := false
		for _, b := range all {
			if a.CellID != b.CellID && greatCircleKm(w, a.CellID, b.CellID) <= 3*cp.RadiusKm {
				near = true
			}
		}
		if !near {
			t.Errorf("a settlement stands alone")
		}
	}
}

func TestPlacementIsDeterministic(t *testing.T) {
	w := testWorld(t)
	cp := testCircleParams()
	var land int32
	for id := range w.Cells {
		if !w.Cells[id].IsOcean && !w.Cells[id].IsLake && math.Abs(w.Cells[id].Point.LatDeg) < 40 {
			land = int32(id)
			break
		}
	}
	existing := []ExistingSettlement{{CellID: land, TierWeight: 1}}
	a, _, _, _ := fill(t, 8, existing, nil, nil, cp)
	b, _, _, _ := fill(t, 8, existing, nil, nil, cp)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("run %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// A circle centred on open ocean has nothing eligible: it is closed as having no room and the founding lands in a
// later circle, in the same call.
func TestAnOceanCircleAdvances(t *testing.T) {
	w := testWorld(t)
	cp := CircleParams{RadiusKm: 400, Capacity: 5, FillBandKm: 100, MaxAdvance: 200}
	// find an ocean cell with no land within the radius
	ocean := int32(-1)
	for id := range w.Cells {
		if !w.Cells[id].IsOcean {
			continue
		}
		clear := true
		for _, nb := range w.Neighbors(int32(id)) {
			if !w.Cells[nb].IsOcean {
				clear = false
			}
		}
		if !clear {
			continue
		}
		// no land within the radius (checked on the cells of the sphere)
		for j := range w.Cells {
			if !w.Cells[j].IsOcean && cellToPointKm(w, int32(j), w.Cells[id].Point.LatDeg, w.Cells[id].Point.LonDeg) <= cp.RadiusKm {
				clear = false
				break
			}
		}
		if clear {
			ocean = int32(id)
			break
		}
	}
	if ocean < 0 {
		t.Skip("no open-ocean spot with that radius in the test world")
	}
	c := w.Cells[ocean].Point
	cur := &Circle{Index: 0, LatDeg: c.LatDeg, LonDeg: c.LonDeg, RadiusKm: cp.RadiusKm, Capacity: cp.Capacity}
	plan, err := PlanSpawn(w, nil, cur, cur, cp, testParams())
	if err != nil {
		t.Fatalf("an ocean circle should advance, got %v", err)
	}
	if len(plan.Closed) == 0 || plan.Closed[0].Index != 0 || plan.Closed[0].Reason != CircleNoRoom {
		t.Fatalf("circle 0 was not closed for lack of room: %+v", plan.Closed)
	}
	if plan.Circle == 0 || len(plan.Opened) == 0 {
		t.Fatalf("the founding stayed in the ocean circle: %+v", plan)
	}
	if w.Cells[plan.Cand.CellID].IsOcean {
		t.Error("placed in the ocean")
	}
}

func TestPlanRefusesUnusableParameters(t *testing.T) {
	w := testWorld(t)
	if _, err := PlanSpawn(w, nil, nil, nil, CircleParams{}, testParams()); !errors.Is(err, ErrCircleParams) {
		t.Errorf("got %v", err)
	}
}

// With no settlement standing yet the first circle opens at the first lattice point.
func TestTheFirstCircleOfAnEmptyWorldOpensAtTheLattice(t *testing.T) {
	w := testWorld(t)
	plan, err := PlanSpawn(w, nil, nil, nil, testCircleParams(), testParams())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Opened) == 0 || plan.Opened[0].Index != 0 {
		t.Fatalf("no first circle: %+v", plan)
	}
}
