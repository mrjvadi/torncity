package worldgen

// Biome classification: a Whittaker-style temperature/precipitation lookup
// against the authored BiomeRules, plus the two special cases (ocean, lake)
// that come from hydrology rather than climate.
//
// EXACT: distances compared below are between already-quantized Temp/Precip
// int32 values.

// biomeBox is one land BiomeRule's temperature/precipitation range, indexed
// back to its slot in content.Biomes.
type biomeBox struct {
	idx        int
	minT, maxT Temp
	minP, maxP Precip
}

// biomeBoxes splits content.Biomes into the land boxes classifyLandBiome
// scores against, plus the two special water biome indices (ocean, lake) —
// shared by classifyBiomes (the coarse mesh, every cell) and chunk.go (one
// tile at a time, from interpolated climate) so both read the exact same
// authored rule set and there is only one place this split is computed.
func biomeBoxes(content Content) (boxes []biomeBox, oceanIdx, lakeIdx int) {
	oceanIdx, lakeIdx = -1, -1
	for i, b := range content.Biomes {
		if b.IsWater {
			switch b.WaterKind {
			case "ocean":
				oceanIdx = i
			case "lake":
				lakeIdx = i
			}
			continue
		}
		boxes = append(boxes, biomeBox{i, b.MinTemp, b.MaxTemp, b.MinPrecip, b.MaxPrecip})
	}
	return boxes, oceanIdx, lakeIdx
}

// classifyLandBiome returns the land BiomeRule (index into content.Biomes,
// via boxes) whose temperature/precipitation box is closest to (t,p): zero
// distance if both fall inside the rule's range, growing the further
// outside it falls otherwise. Comparing distance rather than requiring an
// exact box match (and taking the first rule that matches) means two
// things: overlapping rules resolve to whichever is the better fit rather
// than to whichever an author happened to list first, and a content pack
// that leaves a gap between ranges (say, nothing authored for a narrow
// cold-and-wet strip) still classifies every point instead of leaving it
// unset — it gets the nearest neighbouring biome, which is the same thing
// a human cartographer would do by hand.
//
// Used both per-CELL (classifyBiomes, the coarse mesh) and per-TILE
// (chunk.go, from climate interpolated across nearby coarse cells) — the
// same exact function either way, so a base-LOD chunk's biome border is a
// genuine refinement of the coarse classification, not a different rule.
func classifyLandBiome(t Temp, p Precip, boxes []biomeBox) uint8 {
	best := -1
	var bestDist int64 = -1
	for _, bx := range boxes {
		dt := int64(0)
		if t < bx.minT {
			dt = int64(bx.minT - t)
		} else if t > bx.maxT {
			dt = int64(t - bx.maxT)
		}
		dp := int64(0)
		if p < bx.minP {
			dp = int64(bx.minP - p)
		} else if p > bx.maxP {
			dp = int64(p - bx.maxP)
		}
		dist := dt*dt + dp*dp
		if best == -1 || dist < bestDist {
			best = bx.idx
			bestDist = dist
		}
	}
	return uint8(best)
}

// classifyBiomes assigns every cell a biome, returning an index into
// content.Biomes for each cell. See classifyLandBiome for the land rule;
// ocean and lake are assigned directly from hydrology rather than climate.
func classifyBiomes(elevation []int32, temp []Temp, precip []Precip, hy Hydrology, content Content) []uint8 {
	n := len(elevation)
	out := make([]uint8, n)

	boxes, oceanIdx, lakeIdx := biomeBoxes(content)

	for c := 0; c < n; c++ {
		if elevation[c] <= 0 {
			out[c] = uint8(oceanIdx)
			continue
		}
		if hy.IsLake[c] {
			out[c] = uint8(lakeIdx)
			continue
		}
		out[c] = classifyLandBiome(temp[c], precip[c], boxes)
	}

	return out
}
