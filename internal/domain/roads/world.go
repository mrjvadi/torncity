package roads

import "github.com/mrjvadi/torncity/internal/domain/worldgen"

// WorldTerrain adapts a generated world to the planner's Terrain: elevation
// and water from the tile, the clearing multiplier from the tile's biome
// (ADR 0042 6.2: grassland 10000, desert 12000, tundra 14000, temperate
// forest 16000, rainforest 22000, wetland 25000, high mountain 18000; all
// content, roads.yml `terrain_bps`).
type WorldTerrain struct {
	W   *worldgen.World
	Src worldgen.TileSource
	// TerrainBPS maps a biome code to its multiplier; a biome not listed
	// costs DefaultBPS (10000 when zero).
	TerrainBPS map[string]int
	DefaultBPS int
	// HighMountainM and HighMountainBPS: ground above this elevation costs
	// the multiplier (2,500 m and 18000).
	HighMountainM   int
	HighMountainBPS int
	// Blocked closes a tile (held land of another settlement, a zone's lots).
	Blocked func(worldgen.TileID) bool
}

// Cell implements Terrain.
func (t WorldTerrain) Cell(id worldgen.TileID) (Cell, error) {
	ct, err := t.Src.TileAt(id)
	if err != nil {
		return Cell{}, err
	}
	water, err := t.W.TileWaterKind(t.Src, id)
	if err != nil {
		return Cell{}, err
	}
	bps := t.DefaultBPS
	if bps <= 0 {
		bps = 10000
	}
	if int(ct.Biome) < len(t.W.Content.Biomes) {
		if v, ok := t.TerrainBPS[t.W.Content.Biomes[ct.Biome].Code]; ok {
			bps = v
		}
	}
	if t.HighMountainM > 0 && int(ct.Elevation) > t.HighMountainM && t.HighMountainBPS > bps {
		bps = t.HighMountainBPS
	}
	return Cell{
		ElevationM: int(ct.Elevation),
		TerrainBPS: bps,
		Water:      water,
		Blocked:    t.Blocked != nil && t.Blocked(id),
	}, nil
}

// Neighbours8 implements Terrain.
func (t WorldTerrain) Neighbours8(id worldgen.TileID) [8]worldgen.TileID {
	return t.W.Neighbours8(id)
}

// StepM implements Terrain.
func (t WorldTerrain) StepM(k int) int {
	if k%2 == 1 {
		return t.W.TileDiagonalM()
	}
	return t.W.TileLengthM()
}

// Grid implements Terrain.
func (t WorldTerrain) Grid() int32 { return t.W.TileGrid() }
