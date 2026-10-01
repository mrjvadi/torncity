package settlementbuilding

import (
	"math/rand"
	"testing"
)

var testRules = AccessRules{RoadLotCost: 10, CrossingLotCost: 70, MaxCrossing: 2}

// openGrid is a w x h grid of free, buildable lots.
func openGrid(w, h int) Grid {
	g := make(Grid, h)
	for y := range g {
		g[y] = make([]Lot, w)
		for x := range g[y] {
			g[y][x] = Lot{Buildable: true}
		}
	}
	return g
}

func mapOf(g Grid, net ...[2]int) AccessMap {
	for _, p := range net {
		g[p[1]][p[0]].Occupied = true
	}
	return AccessMap{Grid: g, Network: net, Held: map[[2]int]string{}, Reserved: map[[2]int]bool{}}
}

func TestAccessAlreadyOnTheRoad(t *testing.T) {
	m := mapOf(openGrid(5, 5), [2]int{0, 0})
	if a := m.Plan([2]int{1, 0}, "p", false, testRules); a.Kind != AccessRoad || len(a.Path) != 0 || a.Cost != 0 {
		t.Fatalf("a lot beside the road: %+v", a)
	}
}

func TestAccessNeedsARoad(t *testing.T) {
	m := mapOf(openGrid(6, 1), [2]int{0, 0})
	a := m.Plan([2]int{4, 0}, "p", false, testRules)
	if a.Kind != AccessNeedsRoad || len(a.Path) != 3 || a.Cost != 30 || a.Roads() != 3 {
		t.Fatalf("needs a road of 3 lots: %+v", a)
	}
	// from the lot outwards, ending beside the network
	if a.Path[0] != [2]int{3, 0} || a.Path[2] != [2]int{1, 0} {
		t.Errorf("path order: %v", a.Path)
	}
}

func TestAccessNeverCrossesAnotherResidentsLot(t *testing.T) {
	m := mapOf(openGrid(6, 1), [2]int{0, 0})
	m.Held[[2]int{2, 0}] = "other"
	if a := m.Plan([2]int{4, 0}, "p", true, testRules); a.Kind != AccessNone {
		t.Fatalf("another's lot in the way, even with carving: %+v", a)
	}
}

func TestAccessEnclosedLot(t *testing.T) {
	g := openGrid(5, 5)
	m := mapOf(g, [2]int{0, 0})
	for _, p := range [][2]int{{1, 2}, {3, 2}, {2, 1}, {2, 3}} {
		m.Held[p] = "other"
	}
	if a := m.Plan([2]int{2, 2}, "p", false, testRules); a.Kind != AccessNone {
		t.Fatalf("a lot ringed by other people's lots: %+v", a)
	}
}

func TestAccessCarveNeedsConsent(t *testing.T) {
	m := mapOf(openGrid(5, 1), [2]int{0, 0})
	m.Held[[2]int{2, 0}] = "me"
	m.Held[[2]int{3, 0}] = "me"
	if a := m.Plan([2]int{4, 0}, "me", false, testRules); a.Kind != AccessNone {
		t.Fatalf("own lots in the way without consent: %+v", a)
	}
	a := m.Plan([2]int{4, 0}, "me", true, testRules)
	// route: (3,0) carved, (2,0) carved, (1,0) public
	if a.Kind != AccessNeedsRoad || len(a.Carved) != 2 || len(a.Path) != 3 {
		t.Fatalf("carving own land: %+v", a)
	}
}

func TestAccessCrossesWaterWithACulvert(t *testing.T) {
	g := openGrid(6, 3)
	for y := 0; y < 3; y++ {
		g[y][3].Buildable = false // a stream across the whole village
	}
	m := mapOf(g, [2]int{0, 1})
	a := m.Plan([2]int{5, 1}, "p", false, testRules)
	if a.Kind != AccessNeedsBridge || a.Crossings != 1 {
		t.Fatalf("a stream between the lot and the road: %+v", a)
	}
	// 3 plain lots and 1 water lot
	if want := int64(3*10 + 70); a.Cost != want {
		t.Errorf("cost %d, want %d", a.Cost, want)
	}
}

func TestAccessWaterTooWide(t *testing.T) {
	g := openGrid(8, 1)
	for x := 2; x <= 5; x++ {
		g[0][x].Buildable = false
	}
	m := mapOf(g, [2]int{0, 0})
	if a := m.Plan([2]int{7, 0}, "p", false, testRules); a.Kind != AccessNone {
		t.Fatalf("4 lots of water, limit 2: %+v", a)
	}
}

func TestAccessSteepGroundIsNotCrossed(t *testing.T) {
	g := openGrid(5, 1)
	g[0][2].Buildable = false
	g[0][2].TerrainTags = []string{"sloped_lot"}
	m := mapOf(g, [2]int{0, 0})
	if a := m.Plan([2]int{4, 0}, "p", false, testRules); a.Kind != AccessNone {
		t.Fatalf("a cliff is not a bridge: %+v", a)
	}
}

// The owner's village: a 10x10 grid, the road at (5,3), his three empty lots
// at (8,0), (8,1), (8,2) with a pond and paddies between.
func TestAccessOwnersVillage(t *testing.T) {
	g := openGrid(10, 10)
	for y := 0; y <= 2; y++ {
		for x := 5; x <= 7; x++ {
			g[y][x].Buildable = false
		}
	}
	for y := 3; y <= 5; y++ {
		for x := 6; x <= 9; x++ {
			g[y][x].Buildable = false
		}
	}
	for y := 0; y <= 2; y++ {
		g[y][9].Buildable = false
	}
	m := mapOf(g, [2]int{5, 3})
	for _, p := range [][2]int{{8, 0}, {8, 1}, {8, 2}} {
		m.Held[p] = "owner"
	}
	// a broad band of water: more than two lots on every route
	for _, p := range [][2]int{{8, 0}, {8, 1}, {8, 2}} {
		if a := m.Plan(p, "owner", false, testRules); a.Kind != AccessNone {
			t.Fatalf("lot %v is landlocked: %+v", p, a)
		}
		if a := m.Plan(p, "owner", true, testRules); a.Kind != AccessNone {
			t.Fatalf("lot %v: carving own land does not cross the water: %+v", p, a)
		}
	}
	// a wider crossing limit connects them, and the cost says what it is
	wide := testRules
	wide.MaxCrossing = 4
	if a := m.Plan([2]int{8, 2}, "owner", false, wide); a.Kind != AccessNeedsBridge || a.Crossings == 0 {
		t.Fatalf("with a longer causeway: %+v", a)
	}
}

func TestAccessReservedLotsAreTheCheapestRoad(t *testing.T) {
	m := mapOf(openGrid(4, 3), [2]int{0, 0})
	m.Reserved[[2]int{1, 1}] = true
	m.Reserved[[2]int{2, 1}] = true
	a := m.Plan([2]int{3, 1}, "p", false, testRules)
	if a.Kind != AccessNeedsRoad || a.Path[0] != [2]int{2, 1} {
		t.Fatalf("the route follows the reserved street: %+v", a)
	}
}

// No sale may landlock a reserved corridor: sell lots one after another, each
// with its corridor laid as road at the sale, and every lot ever sold keeps
// its road however the rest of the village is sold.
func TestSalesNeverLandlockACorridor(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g := openGrid(9, 9)
		for i := 0; i < 12; i++ {
			g[rng.Intn(9)][rng.Intn(9)].Buildable = false
		}
		start := [2]int{rng.Intn(9), rng.Intn(9)}
		g[start[1]][start[0]] = Lot{Buildable: true}
		m := mapOf(g, start)
		var sold [][2]int
		for i := 0; i < 200; i++ {
			p := [2]int{rng.Intn(9), rng.Intn(9)}
			if _, held := m.Held[p]; held || m.Reserved[p] || !g[p[1]][p[0]].Buildable || g[p[1]][p[0]].Occupied {
				continue
			}
			a := m.Plan(p, "p", false, testRules)
			if !a.Feasible() {
				continue
			}
			// the sale: the lot is held, its corridor becomes road and reserved
			m.Held[p] = "p"
			sold = append(sold, p)
			for _, c := range a.Path {
				m.Reserved[c] = true
				g[c[1]][c[0]].Occupied = true
				m.Network = append(m.Network, c)
			}
		}
		for _, p := range sold {
			if a := m.Plan(p, "p", false, testRules); a.Kind != AccessRoad {
				t.Fatalf("seed %d: lot %v sold with access lost it: %+v", seed, p, a)
			}
		}
	}
}

func TestAccessIsDeterministic(t *testing.T) {
	g := openGrid(8, 8)
	m := mapOf(g, [2]int{0, 0})
	a := m.Plan([2]int{7, 7}, "p", false, testRules)
	for i := 0; i < 5; i++ {
		b := m.Plan([2]int{7, 7}, "p", false, testRules)
		if len(a.Path) != len(b.Path) {
			t.Fatal("different length")
		}
		for j := range a.Path {
			if a.Path[j] != b.Path[j] {
				t.Fatalf("path differs at %d: %v vs %v", j, a.Path, b.Path)
			}
		}
	}
}

func TestNearbyOffersServedLotsFirst(t *testing.T) {
	g := openGrid(6, 6)
	for y := 0; y < 6; y++ {
		g[y][3].Buildable = false
	}
	m := mapOf(g, [2]int{0, 0})
	ok := func(x, y int) bool { return g[y][x].Buildable && !g[y][x].Occupied }
	near := m.Nearby([2]int{5, 5}, "p", AccessRules{RoadLotCost: 10, CrossingLotCost: 70, MaxCrossing: 0}, ok, 3)
	// the stream cannot be crossed (limit 0): only the west bank is served
	for _, n := range near {
		if n.X >= 3 {
			t.Fatalf("lot %v is across the water", n)
		}
	}
	if len(near) == 0 || near[0].Access.Kind != AccessRoad {
		t.Fatalf("lots already on the road come first: %+v", near)
	}
}

func TestPlanStreets(t *testing.T) {
	g := openGrid(10, 10)
	m := mapOf(g, [2]int{0, 0}, [2]int{1, 0})
	a := PlanStreets(g, m.Network, nil, 4)
	b := PlanStreets(g, m.Network, nil, 4)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("street plan %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("not deterministic")
		}
	}
	// every street lot joins the network through street lots
	reach := map[[2]int]bool{}
	for _, p := range m.Network {
		reach[p] = true
	}
	changed := true
	for changed {
		changed = false
		for _, p := range a {
			if reach[p] {
				continue
			}
			for _, d := range roadDirs {
				if reach[[2]int{p[0] + d[0], p[1] + d[1]}] {
					reach[p] = true
					changed = true
					break
				}
			}
		}
	}
	for _, p := range a {
		if !reach[p] {
			t.Fatalf("street lot %v is cut off", p)
		}
	}
	if got := PlanStreets(g, m.Network, nil, 0); got != nil {
		t.Errorf("pitch 0 plans nothing, got %v", got)
	}
	// a held lot is never platted over
	held := map[[2]int]string{{4, 0}: "x"}
	for _, p := range PlanStreets(g, m.Network, held, 4) {
		if p == [2]int{4, 0} {
			t.Fatal("platted over a held lot")
		}
	}
}
