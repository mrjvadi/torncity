package landroad

import (
	"github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// WorldGround reads lots from a generated world through a settlement's
// LotFrame. It caches what it has sampled; it is for one request, not shared
// between goroutines.
type WorldGround struct {
	W *worldgen.World
	F settlement.LotFrame
	// TerrainBPS maps a biome code to its clearing multiplier (roads.yml
	// planner.terrain_bps); a biome not listed costs DefaultBPS (10000).
	TerrainBPS map[string]int
	DefaultBPS int

	samples map[Lot]worldgen.FineSample
	tiles   map[Lot]worldgen.TileID
}

// NewWorldGround builds the reader.
func NewWorldGround(w *worldgen.World, f settlement.LotFrame, terrainBPS map[string]int) *WorldGround {
	return &WorldGround{W: w, F: f, TerrainBPS: terrainBPS, DefaultBPS: 10000,
		samples: map[Lot]worldgen.FineSample{}, tiles: map[Lot]worldgen.TileID{}}
}

func (g *WorldGround) sample(l Lot) worldgen.FineSample {
	if s, ok := g.samples[l]; ok {
		return s
	}
	lat, lon := g.F.LatLon(l.X, l.Y)
	s := g.W.SampleFineLatLon(lat, lon)
	g.samples[l] = s
	return s
}

// Tile is the base tile a lot lies in.
func (g *WorldGround) Tile(l Lot) worldgen.TileID {
	if t, ok := g.tiles[l]; ok {
		return t
	}
	lat, lon := g.F.LatLon(l.X, l.Y)
	t := g.W.TileOfLatLon(lat, lon)
	g.tiles[l] = t
	return t
}

// TileCentre is the lot nearest the centre of a tile.
func (g *WorldGround) TileCentre(t worldgen.TileID) Lot {
	lat, lon := g.W.TileCentre(t)
	x, y := g.F.LotOf(lat, lon)
	return Lot{x, y}
}

// Info implements Ground.
func (g *WorldGround) Info(l Lot) Info {
	s := g.sample(l)
	in := Info{ElevationM: s.ElevationM, TerrainBPS: g.DefaultBPS}
	switch {
	case s.IsOcean || s.IsLake:
		in.Water = WaterStill
	case s.StreamKind == worldgen.StreamKindRiver:
		in.Water = WaterRiver
	case s.StreamKind == worldgen.StreamKindStream:
		in.Water = WaterStream
	}
	if int(s.Biome) < len(g.W.Content.Biomes) {
		if v, ok := g.TerrainBPS[g.W.Content.Biomes[s.Biome].Code]; ok {
			in.TerrainBPS = v
		}
	}
	for _, d := range dirs {
		n := g.sample(l.Add(d.X, d.Y))
		diff := s.ElevationM - n.ElevationM
		if diff < 0 {
			diff = -diff
		}
		if diff > in.SlopeM {
			in.SlopeM = diff
		}
	}
	return in
}
