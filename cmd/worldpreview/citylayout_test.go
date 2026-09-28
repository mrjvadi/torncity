package main

import "testing"

func flatTiles(size int) []cityTile {
	tiles := make([]cityTile, size*size)
	return tiles
}

// roadKeys flattens a []roadTile into a coordinate-keyed set, for tests
// that only care whether a particular (x,y) has a road, not its class.
func roadKeys(roads []roadTile) map[[2]int]roadTile {
	m := make(map[[2]int]roadTile, len(roads))
	for _, r := range roads {
		m[[2]int{r.X, r.Y}] = r
	}
	return m
}

func TestLayoutCity_Deterministic(t *testing.T) {
	tiles := flatTiles(15)
	a := layoutCity(15, tiles)
	b := layoutCity(15, tiles)
	if len(a.Lots) != len(b.Lots) || len(a.Roads) != len(b.Roads) {
		t.Fatalf("layoutCity is not deterministic: got %d/%d lots then %d/%d",
			len(a.Lots), len(a.Roads), len(b.Lots), len(b.Roads))
	}
	for i := range a.Lots {
		if a.Lots[i] != b.Lots[i] {
			t.Fatalf("lot %d differs between two runs: %+v vs %+v", i, a.Lots[i], b.Lots[i])
		}
	}
	for i := range a.Roads {
		if a.Roads[i] != b.Roads[i] {
			t.Fatalf("road %d differs between two runs: %+v vs %+v", i, a.Roads[i], b.Roads[i])
		}
	}
}

func TestLayoutCity_NoOverlapsAndInBounds(t *testing.T) {
	tiles := flatTiles(15)
	layout := layoutCity(15, tiles)

	occupied := make(map[[2]int]string)
	for _, r := range layout.Roads {
		if r.X < 0 || r.X >= 15 || r.Y < 0 || r.Y >= 15 {
			t.Fatalf("road tile out of bounds: %+v", r)
		}
		occupied[[2]int{r.X, r.Y}] = "road"
	}
	for _, lot := range layout.Lots {
		if lot.X < 0 || lot.Y < 0 || lot.X+lot.W > 15 || lot.Y+lot.H > 15 {
			t.Fatalf("lot out of bounds: %+v", lot)
		}
		for dy := 0; dy < lot.H; dy++ {
			for dx := 0; dx < lot.W; dx++ {
				key := [2]int{lot.X + dx, lot.Y + dy}
				if prev, taken := occupied[key]; taken {
					t.Fatalf("tile %v occupied by both %s and lot %s", key, prev, lot.Type)
				}
				occupied[key] = lot.Type
			}
		}
	}
}

func TestLayoutCity_HasCivicHallAndVariety(t *testing.T) {
	// demoCitySize, not a small literal size: the real road grid
	// (traceLineSet) traces a genuine multi-line Manhattan grid now, dense
	// enough on a 15-lot canvas alone to leave too little contiguous space
	// for 6 separate 3x3-ish civic buildings — this test wants "enough
	// room to place everything", which the real export size guarantees.
	size := demoCitySize
	tiles := flatTiles(size)
	layout := layoutCity(size, tiles)

	counts := map[string]int{}
	for _, lot := range layout.Lots {
		counts[lot.Type]++
	}
	if counts["civic_hall"] != 1 {
		t.Fatalf("expected exactly one civic_hall on flat land, got %d", counts["civic_hall"])
	}
	for _, want := range []string{"market", "bank", "school", "clinic", "police"} {
		if counts[want] == 0 {
			t.Fatalf("expected at least one %s on flat land, got 0", want)
		}
	}
	if counts["housing"] == 0 {
		t.Fatalf("expected housing to fill remaining space, got 0")
	}
}

// TestLayoutCity_AvoidsWaterAndSteep covers both halves of the lot-scale
// water rule: buildings never go on water or steep ground regardless of
// bridgeability, but a road does NOT detour around a plain (non-Impassable,
// non-WideRiver) water tile — it crosses it in a straight line and that
// crossing comes back as a bridge (see TestLayoutCity_BridgesThinStream
// below for the dedicated bridge assertion). A road still detours around
// Steep ground, which stays a hard "unbuildable and impassable" flag
// independent of water at all.
func TestLayoutCity_AvoidsWaterAndSteep(t *testing.T) {
	size := 15
	tiles := flatTiles(size)
	// A thin, bridgeable stream segment through the middle of column 7 —
	// Water=true, Impassable=false, WideRiver=false, exactly what a
	// lot-scale stream crossing looks like (fine.go's StreamKindStream) —
	// and a steep patch in a corner, which stays impassable to roads.
	for y := 5; y <= 9; y++ {
		tiles[y*size+7].Water = true
	}
	for y := 12; y < size; y++ {
		for x := 12; x < size; x++ {
			tiles[y*size+x].Steep = true
		}
	}
	layout := layoutCity(size, tiles)

	for _, lot := range layout.Lots {
		for dy := 0; dy < lot.H; dy++ {
			for dx := 0; dx < lot.W; dx++ {
				x, y := lot.X+dx, lot.Y+dy
				tile := tiles[y*size+x]
				if tile.Water {
					t.Fatalf("lot %s placed on a water tile (%d,%d)", lot.Type, x, y)
				}
				if tile.Steep {
					t.Fatalf("lot %s placed on a steep tile (%d,%d)", lot.Type, x, y)
				}
			}
		}
	}

	bridgeKeys := roadKeys(layout.Bridges)
	for _, r := range layout.Roads {
		tile := tiles[r.Y*size+r.X]
		// The elevated highway is documented to ignore terrain entirely
		// (elevatedHighwayPath) — it may legitimately sit on steep ground
		// or water; only local/arterial roads are expected to jog around
		// Steep.
		if tile.Steep && r.Class != roadClassHighway {
			t.Fatalf("road tile %+v sits on steep ground", r)
		}
		// A road tile is only ever allowed to sit on water when that
		// crossing is also reported back as a bridge.
		if tile.Water {
			if _, ok := bridgeKeys[[2]int{r.X, r.Y}]; !ok {
				t.Fatalf("road tile (%d,%d) sits on water but is not listed in Bridges", r.X, r.Y)
			}
		}
	}
}

func TestLayoutCity_PortOnlyNearCoast(t *testing.T) {
	// demoCitySize, same reasoning as TestLayoutCity_HasCivicHallAndVariety:
	// the real road grid needs real room to leave a coast/deposit-adjacent
	// pocket free for a 2x2 building.
	size := demoCitySize
	tiles := flatTiles(size)
	layout := layoutCity(size, tiles)
	for _, lot := range layout.Lots {
		if lot.Type == "port" {
			t.Fatalf("port placed with no coast tile anywhere in the grid")
		}
		if lot.Type == "mine" {
			t.Fatalf("mine placed with no deposit tile anywhere in the grid")
		}
	}

	// (2,2) and (41,41) are deliberately OFF every local (multiples of
	// roadSpacing=4) and arterial (lineIndices(size,arterialSpacing)) grid
	// line, so the coast/deposit flag sits in a genuinely buildable pocket
	// rather than getting paved over by the road grid itself.
	tiles2 := flatTiles(size)
	tiles2[2*size+2].Coast = true
	tiles2[41*size+41].Deposit = true
	tiles2[41*size+41].ResourceCode = "iron"
	layout2 := layoutCity(size, tiles2)
	sawPort, sawMine := false, false
	for _, lot := range layout2.Lots {
		if lot.Type == "port" {
			sawPort = true
		}
		if lot.Type == "mine" {
			sawMine = true
		}
	}
	if !sawPort {
		t.Fatalf("expected a port near the coast tile")
	}
	if !sawMine {
		t.Fatalf("expected a mine near the deposit tile")
	}
}

// TestLayoutCity_BridgesThinStream is the dedicated local-road bridge
// assertion: a thin (1-lot-wide), non-Impassable, non-WideRiver stream
// crossing the nominal LOCAL road line must be BRIDGED — the traced road
// stays straight and the crossing tiles come back in cityLayout.Bridges —
// not detoured around, which was the old (tile-scale-correct,
// lot-scale-wrong) behaviour.
func TestLayoutCity_BridgesThinStream(t *testing.T) {
	size := 15
	tiles := flatTiles(size)
	// lineIndices(15, roadSpacing) always includes mid=7 as the nearest
	// line to size/2, so the local road's nominal row AND column both run
	// through x=7/y=7 — see traceRoadClass's own doc comment. A thin
	// vertical strip straight down column 7 is therefore guaranteed to sit
	// on the local road's own path.
	for y := 5; y <= 9; y++ {
		tiles[y*size+7].Water = true // bridgeable: Impassable and WideRiver both left false
	}

	layout := layoutCity(size, tiles)

	bridgeKeys := roadKeys(layout.Bridges)
	roadSet := roadKeys(layout.Roads)

	sawBridge := false
	for y := 5; y <= 9; y++ {
		key := [2]int{7, y}
		if _, ok := roadSet[key]; !ok {
			continue // this particular row's road happened to run elsewhere; fine, another row must still cross
		}
		if _, ok := bridgeKeys[key]; !ok {
			t.Fatalf("road tile %v crosses a thin stream but was not marked as a bridge", key)
		}
		sawBridge = true
	}
	if !sawBridge {
		t.Fatalf("expected the road to run straight through the thin stream at column 7 and pick up at least one bridge; got roads=%+v bridges=%+v", layout.Roads, layout.Bridges)
	}
	if len(layout.Bridges) == 0 {
		t.Fatalf("expected at least one bridge in the layout")
	}
}

// TestLayoutCity_DetoursImpassableWater is the bridge test's counterpart:
// something explicitly marked Impassable (a lake or ocean) must still force
// EVERY road class to jog around it exactly like the pre-bridging behaviour
// did for all water.
func TestLayoutCity_DetoursImpassableWater(t *testing.T) {
	size := 15
	tiles := flatTiles(size)
	// A 3-lot-wide impassable band (columns 6..8) straight across rows
	// 5..9, squarely on the local road's nominal column (7) — too wide to
	// be a bridgeable crossing, so every affected row's local road must jog
	// to a column outside 6..8.
	for y := 5; y <= 9; y++ {
		for x := 6; x <= 8; x++ {
			tiles[y*size+x].Water = true
			tiles[y*size+x].Impassable = true
		}
	}

	layout := layoutCity(size, tiles)

	roadAtRow := make(map[int]int) // y -> x, for rows that have a road tile
	for _, r := range layout.Roads {
		if r.Y >= 5 && r.Y <= 9 {
			roadAtRow[r.Y] = r.X
		}
	}
	for y := 5; y <= 9; y++ {
		x, ok := roadAtRow[y]
		if !ok {
			t.Fatalf("expected some road tile in row %d", y)
		}
		if x >= 6 && x <= 8 {
			t.Fatalf("road tile (%d,%d) sits on an Impassable tile; expected it to jog outside columns 6..8", x, y)
		}
	}
	// Nothing impassable should ever be reported as a bridge.
	for _, b := range layout.Bridges {
		if tiles[b.Y*size+b.X].Impassable {
			t.Fatalf("bridge %+v sits on an Impassable tile", b)
		}
	}
}

// TestLayoutCity_ArterialBridgesRiverButLocalDetours covers the new
// per-class water rule directly: a WideRiver (too wide for a LOCAL road,
// still bridgeable by an ARTERIAL or the elevated highway) must never carry
// a roadClassLocal tile, but should still end up crossed (and bridged) by
// SOME higher-class road, since arterial/highway lines don't jog around it.
func TestLayoutCity_ArterialBridgesRiverButLocalDetours(t *testing.T) {
	size := 30 // wide enough for the arterial grid (spacing 7) to differ meaningfully from the local grid (spacing 4)
	tiles := flatTiles(size)
	mid := size / 2
	for x := 0; x < size; x++ {
		tiles[mid*size+x].Water = true
		tiles[mid*size+x].WideRiver = true
	}

	layout := layoutCity(size, tiles)

	sawNonLocalBridgeOnRiver := false
	for _, r := range layout.Roads {
		onRiver := tiles[r.Y*size+r.X].WideRiver
		if !onRiver {
			continue
		}
		if r.Class == roadClassLocal {
			t.Fatalf("local road tile %+v sits on a WideRiver lot; local roads must jog around a WideRiver", r)
		}
		sawNonLocalBridgeOnRiver = true
	}
	if !sawNonLocalBridgeOnRiver {
		t.Fatalf("expected at least one arterial/highway road tile crossing the WideRiver at row %d; got roads=%+v", mid, layout.Roads)
	}

	// Every river-crossing road tile found above must also be reported as
	// a bridge.
	bridgeKeys := roadKeys(layout.Bridges)
	for _, r := range layout.Roads {
		if tiles[r.Y*size+r.X].WideRiver {
			if _, ok := bridgeKeys[[2]int{r.X, r.Y}]; !ok {
				t.Fatalf("road tile %+v crosses the WideRiver but is not listed in Bridges", r)
			}
		}
	}
}

// TestLayoutCity_ZonedSkylineAtDemoScale exercises the zoned layout at the
// real --export-city window size (demoCitySize), on flat, unobstructed
// terrain so every zone/pass has room to place something: a downtown
// tower, a mid-rise ring, low-rise housing, parks and farms should all
// appear, every lot's Floors should fall in its type's own documented
// range (floorsFor), and all three road classes (local, arterial, elevated
// highway) should be present.
//
// Only "at least one downtown tower of SOME kind" is asserted, not both
// tower_office AND tower_residential specifically: fillZone places the
// larger footprint first across the WHOLE zone (see its own doc comment
// for why), so on perfectly flat, unobstructed terrain the downtown zone
// can legitimately tile into exact 3x3 (tower_office) blocks with no
// smaller leftover fragment at all — real generated terrain almost always
// breaks that up (water, a steep patch, the coarse grid's own irregular
// buildable window), but this synthetic flat grid does not need to.
func TestLayoutCity_ZonedSkylineAtDemoScale(t *testing.T) {
	tiles := flatTiles(demoCitySize)
	layout := layoutCity(demoCitySize, tiles)

	counts := map[string]int{}
	for _, lot := range layout.Lots {
		counts[lot.Type]++
		var lo, hi int
		switch lot.Type {
		case "tower_office", "tower_residential":
			lo, hi = 15, 40
		case "midrise":
			lo, hi = 5, 12
		case "housing":
			lo, hi = 2, 4
		case "park", "farm":
			lo, hi = 0, 0
		default: // civic_hall, market, bank, school, clinic, police, port, mine
			lo, hi = 2, 6
		}
		if lot.Floors < lo || lot.Floors > hi {
			t.Errorf("lot %s at (%d,%d) has Floors=%d, want %d..%d", lot.Type, lot.X, lot.Y, lot.Floors, lo, hi)
		}
	}

	if counts["tower_office"]+counts["tower_residential"] == 0 {
		t.Errorf("expected at least one downtown tower (office or residential) in a %dx%d flat city, got 0 (counts=%v)", demoCitySize, demoCitySize, counts)
	}
	for _, want := range []string{"midrise", "housing", "park", "farm", "civic_hall"} {
		if counts[want] == 0 {
			t.Errorf("expected at least one %s in a %dx%d flat city, got 0 (counts=%v)", want, demoCitySize, demoCitySize, counts)
		}
	}

	classSeen := map[int]bool{}
	for _, r := range layout.Roads {
		classSeen[r.Class] = true
	}
	for _, want := range []int{roadClassLocal, roadClassArterial, roadClassHighway} {
		if !classSeen[want] {
			t.Errorf("expected at least one road tile of class %d, saw classes=%v", want, classSeen)
		}
	}

	// Deterministic re-run: same terrain, same Floors/zoning every time.
	layout2 := layoutCity(demoCitySize, tiles)
	if len(layout2.Lots) != len(layout.Lots) {
		t.Fatalf("re-running layoutCity on identical terrain changed lot count: %d vs %d", len(layout.Lots), len(layout2.Lots))
	}
	for i := range layout.Lots {
		if layout.Lots[i] != layout2.Lots[i] {
			t.Fatalf("lot %d not deterministic: %+v vs %+v", i, layout.Lots[i], layout2.Lots[i])
		}
	}
}
