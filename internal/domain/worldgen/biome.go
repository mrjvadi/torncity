package worldgen

// Biome classification: a Whittaker-style temperature/precipitation lookup
// against the authored BiomeRules, plus the two special cases (ocean, lake)
// that come from hydrology rather than climate.
//
// EXACT: distances compared below are between already-quantized Temp/Precip
// int32 values.

// classifyBiomes assigns every cell a biome, returning an index into
// content.Biomes for each cell.
//
// A land cell is assigned the rule with the SMALLEST "distance outside its
// box" — zero if the cell's temperature and precipitation both fall inside
// the rule's range, and growing the further outside it falls otherwise.
// Comparing distance rather than requiring an exact box match (and taking
// the first rule that matches) means two things: overlapping rules resolve
// to whichever is the better fit rather than to whichever an author happened
// to list first, and a content pack that leaves a gap between ranges (say,
// nothing authored for a narrow cold-and-wet strip) still classifies every
// cell instead of leaving it unset — it gets the nearest neighbouring biome,
// which is the same thing a human cartographer would do by hand.
func classifyBiomes(elevation []int32, temp []Temp, precip []Precip, hy Hydrology, content Content) []uint8 {
	n := len(elevation)
	out := make([]uint8, n)

	oceanIdx, lakeIdx := -1, -1
	type box struct {
		idx        int
		minT, maxT Temp
		minP, maxP Precip
	}
	var boxes []box
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
		boxes = append(boxes, box{i, b.MinTemp, b.MaxTemp, b.MinPrecip, b.MaxPrecip})
	}

	for c := 0; c < n; c++ {
		if elevation[c] <= 0 {
			out[c] = uint8(oceanIdx)
			continue
		}
		if hy.IsLake[c] {
			out[c] = uint8(lakeIdx)
			continue
		}

		t, p := temp[c], precip[c]
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
		out[c] = uint8(best)
	}

	return out
}
