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

func TestLayoutCity_AvoidsWaterAndSteep(t *testing.T) {
	size := 15
	tiles := flatTiles(size)
	// A stream segment through the middle of column 7 (not spanning the
	// whole grid, so a road can still dodge around either end of it — the
	// same way a real road bridges or detours a stream rather than having
	// nowhere left to go), and a steep patch in a corner.
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
	for _, r := range layout.Roads {
		tile := tiles[r[1]*size+r[0]]
		if tile.Water {
			t.Fatalf("road tile (%d,%d) sits on water", r[0], r[1])
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
