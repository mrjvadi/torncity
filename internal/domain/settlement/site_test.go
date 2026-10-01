package settlement

import (
	"testing"
	"time"
)

func TestShiftOrderIsBoundedAndDeterministic(t *testing.T) {
	got := shiftOrder(3)
	if len(got) != 49 {
		t.Fatalf("shiftOrder(3) has %d slides, want 7x7 = 49", len(got))
	}
	if got[0] != [2]int{0, 0} {
		t.Errorf("the first slide is %v, want the untouched grid", got[0])
	}
	seen := map[[2]int]bool{}
	lastRing := 0
	for _, s := range got {
		if seen[s] {
			t.Errorf("slide %v listed twice", s)
		}
		seen[s] = true
		ring := abs(s[0])
		if abs(s[1]) > ring {
			ring = abs(s[1])
		}
		if ring < lastRing {
			t.Errorf("slide %v comes after a farther ring", s)
		}
		lastRing = ring
	}
	again := shiftOrder(3)
	for i := range got {
		if got[i] != again[i] {
			t.Fatalf("order differs between calls at %d", i)
		}
	}
	if n := len(shiftOrder(0)); n != 1 {
		t.Errorf("shiftOrder(0) has %d slides, want 1", n)
	}
}

func TestSiteReportMeets(t *testing.T) {
	full := []BuildingPlacement{{TypeCode: "civic_hall"}, {TypeCode: "road"}, {TypeCode: "barter_post"}, {TypeCode: "granary"}}
	rules := SiteRules{GridLots: 5, MinBuildableShareBps: 7000, MaxShiftLots: 3}
	cases := []struct {
		name string
		rep  SiteReport
		want bool
	}{
		{"all land", SiteReport{BuildableLots: 25, TotalLots: 25, CentreBuildable: true, Kit: full}, true},
		{"just over the share", SiteReport{BuildableLots: 18, TotalLots: 25, CentreBuildable: true, Kit: full}, true},
		{"share met", SiteReport{BuildableLots: 20, TotalLots: 25, CentreBuildable: true, Kit: full}, true},
		{"wet centre", SiteReport{BuildableLots: 25, TotalLots: 25, CentreBuildable: false, Kit: full}, false},
		{"kit not placed", SiteReport{BuildableLots: 25, TotalLots: 25, CentreBuildable: true, Kit: full[:1]}, false},
	}
	// 70% of 25 lots is 17.5: 18 lots meet it, 17 do not (below).
	for _, c := range cases {
		if got := c.rep.Meets(rules); got != c.want {
			t.Errorf("%s: Meets = %v, want %v", c.name, got, c.want)
		}
	}
	if got := (SiteReport{BuildableLots: 17, TotalLots: 25, CentreBuildable: true, Kit: full}).Meets(rules); got {
		t.Error("17 of 25 lots (68%) met a 70% rule")
	}
	if (SiteRules{}).Enabled() {
		t.Error("zero SiteRules is enabled")
	}
}

// The founding hall goes to the buildable footprint nearest the centre, and
// never onto a wet lot.
func TestPlaceKitPrefersCentreAndDryLots(t *testing.T) {
	all := func(x, y int) bool { return true }
	kit := placeKit(5, all)
	if len(kit) != len(FoundingKitBuildings) {
		t.Fatalf("kit = %v", kit)
	}
	if kit[0].TypeCode != "civic_hall" || kit[0].LotX < 1 || kit[0].LotX > 2 || kit[0].LotY < 1 || kit[0].LotY > 2 {
		t.Errorf("civic hall at (%d,%d), want a 2x2 footprint around the centre", kit[0].LotX, kit[0].LotY)
	}
	// The whole west half is water: nothing may be placed there.
	dryEast := func(x, y int) bool { return x >= 3 }
	kit = placeKit(5, dryEast)
	if len(kit) == 0 {
		t.Fatalf("kit on a half-dry grid = %v", kit)
	}
	for _, b := range kit {
		if b.LotX < 3 {
			t.Errorf("%s placed on a wet lot (%d,%d)", b.TypeCode, b.LotX, b.LotY)
		}
	}
	// No dry 2x2 anywhere: the hall is omitted rather than forced.
	if kit := placeKit(5, func(x, y int) bool { return (x+y)%2 == 0 }); len(kit) == len(FoundingKitBuildings) {
		t.Errorf("a checkerboard of land took a full kit: %v", kit)
	}
}

// The live bug: seed 42, the shipped config, N=1.
func TestFirstVillageOfSeed42IsNotOnALake(t *testing.T) {
	if testing.Short() {
		t.Skip("generates the full-size planet")
	}
	w := realWorld(t)
	p := realConfigParams(t)
	g := p.Site.GridLots

	old := p
	old.Site = SiteRules{}
	before, err := FindSpawn(w, nil, 1, old)
	if err != nil {
		t.Fatal(err)
	}
	b := EvaluateSite(w, before.CellID, 0, 0, g)
	t.Logf("BEFORE (cell-only rule): cell %d, %d/%d lots buildable = %d bps", before.CellID, b.BuildableLots, b.TotalLots, b.ShareBps())
	if b.Meets(p.Site) {
		t.Fatalf("the old rule's spot (cell %d, %d bps) already meets the new one: the regression is not reproduced", before.CellID, b.ShareBps())
	}

	start := time.Now()
	after, err := FindSpawn(w, nil, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	a := EvaluateSite(w, after.CellID, after.ShiftX, after.ShiftY, g)
	t.Logf("AFTER: cell %d shift (%d,%d), %d/%d lots buildable = %d bps, FindSpawn %s", after.CellID, after.ShiftX, after.ShiftY,
		a.BuildableLots, a.TotalLots, a.ShareBps(), took)
	if !a.Meets(p.Site) {
		t.Errorf("the chosen site does not meet the rules: %+v", a)
	}
	if after.BuildableLots != a.BuildableLots || after.TotalLots != a.TotalLots {
		t.Errorf("the candidate reports %d/%d lots, the exact sampler %d/%d", after.BuildableLots, after.TotalLots, a.BuildableLots, a.TotalLots)
	}
}

// Property: every one of the first 100 spawns of the real world has a grid
// that meets the rules, a buildable centre lot and kit lots on dry ground,
// judged by the very sampler the placement rules use; and the series is
// reproducible.
func TestRealWorldSpawnsHaveBuildableGrids(t *testing.T) {
	if testing.Short() {
		t.Skip("generates the full-size planet")
	}
	w := realWorld(t)
	p := realConfigParams(t)
	g := p.Site.GridLots
	start := time.Now()
	spawns := spawnSeries(t, w, p, 100)
	t.Logf("100 spawns took %s (%s each)", time.Since(start), time.Since(start)/100)

	shifted := 0
	worst := 10000
	for i, c := range spawns {
		rep := EvaluateSite(w, c.CellID, c.ShiftX, c.ShiftY, g)
		if !rep.Meets(p.Site) {
			t.Errorf("spawn %d (cell %d): %d/%d lots buildable, centre %v, kit %v", i+1, c.CellID, rep.BuildableLots, rep.TotalLots, rep.CentreBuildable, rep.Kit)
		}
		if abs(c.ShiftX) > p.Site.MaxShiftLots || abs(c.ShiftY) > p.Site.MaxShiftLots {
			t.Errorf("spawn %d slides (%d,%d), beyond the %d-lot bound", i+1, c.ShiftX, c.ShiftY, p.Site.MaxShiftLots)
		}
		if c.ShiftX != 0 || c.ShiftY != 0 {
			shifted++
		}
		if s := rep.ShareBps(); s < worst {
			worst = s
		}

		lat, lon := GridCentre(w, c.LatDeg, c.LonDeg, c.ShiftX, c.ShiftY)
		detail := SampleGridDetail(w, lat, lon, g, c.CellID)
		mid := g / 2
		if !detail.Lots[mid][mid].Buildable {
			t.Errorf("spawn %d: the centre lot is not buildable", i+1)
		}
		for _, b := range PlaceFoundingKit(w, lat, lon, g) {
			fw, fh := 1, 1
			for _, k := range FoundingKitBuildings {
				if k.TypeCode == b.TypeCode {
					fw, fh = k.W, k.H
				}
			}
			for y := b.LotY; y < b.LotY+fh; y++ {
				for x := b.LotX; x < b.LotX+fw; x++ {
					if !detail.Lots[y][x].Buildable {
						t.Errorf("spawn %d: %s stands on the unbuildable lot (%d,%d)", i+1, b.TypeCode, x, y)
					}
				}
			}
		}
	}
	t.Logf("%d of 100 spawns slid their grid; the worst grid is %d bps buildable", shifted, worst)

	again := spawnSeries(t, w, p, 20)
	for i := range again {
		if spawns[i] != again[i] {
			t.Fatalf("spawn %d differs between runs: %+v vs %+v", i+1, spawns[i], again[i])
		}
	}
}

// A village founded under the old rule (cell 6781, grid unshifted, 6 of 25
// lots dry) is moved to a valid site, deterministically, and not into
// another settlement's spacing.
func TestFindRelocationMovesAWateryVillage(t *testing.T) {
	if testing.Short() {
		t.Skip("generates the full-size planet")
	}
	w := realWorld(t)
	p := realConfigParams(t)
	old := p
	old.Site = SiteRules{}
	before, err := FindSpawn(w, nil, 1, old)
	if err != nil {
		t.Fatal(err)
	}
	c, err := FindRelocation(w, before.CellID, nil, p)
	if err != nil {
		t.Fatal(err)
	}
	if c.CellID != before.CellID {
		t.Errorf("a slide was enough, yet the village moved from cell %d to %d", before.CellID, c.CellID)
	}
	if c.ShiftX == 0 && c.ShiftY == 0 {
		t.Errorf("the relocation kept the watery grid")
	}
	rep := EvaluateSite(w, c.CellID, c.ShiftX, c.ShiftY, p.Site.GridLots)
	if !rep.Meets(p.Site) {
		t.Errorf("relocated site does not meet the rules: %+v", rep)
	}
	again, _ := FindRelocation(w, before.CellID, nil, p)
	if again != c {
		t.Errorf("relocation is not deterministic: %+v vs %+v", c, again)
	}

	// With no slide allowed and a neighbour right next door, it must move to
	// a different cell that keeps the spacing.
	strict := p
	strict.Site.MaxShiftLots = 0
	strictC, err := FindRelocation(w, before.CellID, nil, strict)
	if err == nil {
		if r := EvaluateSite(w, strictC.CellID, 0, 0, p.Site.GridLots); !r.Meets(p.Site) {
			t.Errorf("no-slide relocation to cell %d does not meet the rules: %+v", strictC.CellID, r)
		}
		nb := []ExistingSettlement{{CellID: strictC.CellID, TierWeight: 1}}
		moved, err := FindRelocation(w, before.CellID, nb, strict)
		if err != nil {
			t.Fatal(err)
		}
		if moved.CellID == strictC.CellID {
			t.Errorf("relocated onto another settlement's cell %d", moved.CellID)
		}
		if d := greatCircleKm(w, moved.CellID, strictC.CellID); d < p.MinSpawnDistanceKm {
			t.Errorf("relocated %.1f km from another settlement, want >= %.1f", d, p.MinSpawnDistanceKm)
		}
	}
}
