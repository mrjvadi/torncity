package main

import "testing"

func flatTiles(size int) []cityTile {
	tiles := make([]cityTile, size*size)
	return tiles
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
}

func TestLayoutCity_NoOverlapsAndInBounds(t *testing.T) {
	tiles := flatTiles(15)
	layout := layoutCity(15, tiles)

	occupied := make(map[[2]int]string)
	for _, r := range layout.Roads {
		if r[0] < 0 || r[0] >= 15 || r[1] < 0 || r[1] >= 15 {
			t.Fatalf("road tile out of bounds: %v", r)
		}
		occupied[r] = "road"
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
	tiles := flatTiles(15)
	layout := layoutCity(15, tiles)

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
// bridgeability, but a road does NOT detour around a plain (non-Impassable)
// water tile — it crosses it in a straight line and that crossing comes
// back as a bridge (see TestLayoutCity_BridgesThinStream below for the
// dedicated bridge assertion). A road still detours around Steep ground,
// which stays a hard "unbuildable and impassable" flag independent of
// water at all.
func TestLayoutCity_AvoidsWaterAndSteep(t *testing.T) {
	size := 15
	tiles := flatTiles(size)
	// A thin, bridgeable stream segment through the middle of column 7 —
	// Water=true, Impassable=false, exactly what a lot-scale stream crossing
	// looks like (fine.go's StreamKindStream) — and a steep patch in a
	// corner, which stays impassable to roads.
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

	bridgeSet := make(map[[2]int]bool, len(layout.Bridges))
	for _, b := range layout.Bridges {
		bridgeSet[b] = true
	}
	for _, r := range layout.Roads {
		tile := tiles[r[1]*size+r[0]]
		if tile.Steep {
			t.Fatalf("road tile (%d,%d) sits on steep ground", r[0], r[1])
		}
		// A road tile is only ever allowed to sit on water when that
		// crossing is also reported back as a bridge.
		if tile.Water && !bridgeSet[[2]int{r[0], r[1]}] {
			t.Fatalf("road tile (%d,%d) sits on water but is not listed in Bridges", r[0], r[1])
		}
	}
}

func TestLayoutCity_PortOnlyNearCoast(t *testing.T) {
	size := 15
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

	tiles2 := flatTiles(size)
	tiles2[0*size+0].Coast = true
	tiles2[14*size+14].Deposit = true
	tiles2[14*size+14].ResourceCode = "iron"
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

// TestLayoutCity_BridgesThinStream is Part 2's own dedicated bridge
// assertion: a thin (1-lot-wide), non-Impassable stream crossing the
// nominal road line must be BRIDGED — the traced road stays straight and
// the crossing tiles come back in cityLayout.Bridges — not detoured around,
// which was the old (tile-scale-correct, lot-scale-wrong) behaviour.
func TestLayoutCity_BridgesThinStream(t *testing.T) {
	size := 15
	tiles := flatTiles(size)
	// roadLineIndices(15) always includes mid=7 as the nearest line to
	// size/2, so the traced road's nominal row AND column both run through
	// x=7/y=7 — see traceRoads' own doc comment. A thin vertical strip
	// straight down column 7 is therefore guaranteed to sit on the road's
	// own path.
	for y := 5; y <= 9; y++ {
		tiles[y*size+7].Water = true // bridgeable: Impassable left false
	}

	layout := layoutCity(size, tiles)

	bridgeSet := make(map[[2]int]bool, len(layout.Bridges))
	for _, b := range layout.Bridges {
		bridgeSet[b] = true
	}
	roadSet := make(map[[2]int]bool, len(layout.Roads))
	for _, r := range layout.Roads {
		roadSet[r] = true
	}

	sawBridge := false
	for y := 5; y <= 9; y++ {
		key := [2]int{7, y}
		if !roadSet[key] {
			continue // this particular row's road happened to run elsewhere; fine, another row must still cross
		}
		if !bridgeSet[key] {
			t.Fatalf("road tile %v crosses a thin stream but was not marked as a bridge", key)
		}
		sawBridge = true
	}
	if !sawBridge {
		t.Fatalf("expected the road to run straight through the thin stream at column 7 and pick up at least one bridge; got roads=%v bridges=%v", layout.Roads, layout.Bridges)
	}
	if len(layout.Bridges) == 0 {
		t.Fatalf("expected at least one bridge in the layout")
	}
}

// TestLayoutCity_DetoursImpassableWater is Part 2's counterpart to the
// bridge test above: something explicitly marked Impassable (a lake, ocean
// or a river too wide to bridge) must still force the road to jog around
// it exactly like the pre-bridging behaviour did for ALL water.
func TestLayoutCity_DetoursImpassableWater(t *testing.T) {
	size := 15
	tiles := flatTiles(size)
	// A 3-lot-wide impassable band (columns 6..8) straight across rows
	// 5..9, squarely on the road's nominal column (7) — too wide to be a
	// bridgeable crossing, so every affected row's road must jog to a
	// column outside 6..8.
	for y := 5; y <= 9; y++ {
		for x := 6; x <= 8; x++ {
			tiles[y*size+x].Water = true
			tiles[y*size+x].Impassable = true
		}
	}

	layout := layoutCity(size, tiles)

	roadAtRow := make(map[int]int) // y -> x, for rows that have a vertical-road tile
	for _, r := range layout.Roads {
		if r[1] >= 5 && r[1] <= 9 {
			roadAtRow[r[1]] = r[0]
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
		if tiles[b[1]*size+b[0]].Impassable {
			t.Fatalf("bridge %v sits on an Impassable tile", b)
		}
	}
}
