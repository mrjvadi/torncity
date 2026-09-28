package worldgen

import "sort"

// EXACT (with one documented quantization step). Every number this file
// combines — plate base height, boundary uplift/subsidence, fractal noise —
// is produced with only +, -, *, /, sqrt and floor (see noise.go, plates.go):
// operations IEEE-754 requires to be exact, so identical on any conformant
// platform. The running sum is kept as a float64 for that reason. It is
// quantized to the fixed-point Elevation type EXACTLY ONCE, at the end of
// this file's elevation() function; every later stage in the pipeline
// (sea level comparison, biome lookup, resource eligibility) reads only that
// already-quantized int32, never the float again. That is the "quantise at
// the decision point" rule the package doc describes: the messy floating
// intermediate never itself becomes a gameplay decision.

// Where continents are is decided by a CONTINUOUS field, never by which
// plate a cell sits on. An earlier version used plate type as a hard
// +-4100-unit step (a full ocean/continent base height) added to a much
// smaller noise texture; because the step dwarfed the noise, the resulting
// coastline traced the plate boundary's own graph-Voronoi shape almost
// everywhere — long straight edges and polygon facets, however organic the
// noise on top of it looked. Real continents are not "this plate is land,
// that one is sea": a plate's crust type is a real influence (oceanic crust
// is denser and does sit lower on average) but where the coast actually
// falls is dominated by large-scale, effectively random variation
// (erosion, sediment, sea-level history over geological time) that has no
// relationship to today's plate boundaries at all.
//
// So plateBiasAmplitude — the plate-type signal — and continentBlobAmplitude
// — a low-frequency, heavily domain-warped noise field, "a few large lobes
// per hemisphere" rather than continent-scale detail — are combined with
// the blob amplitude more than twice the plate bias, making the noise field
// the dominant shape and the plate a nudge on top of it, not the other way
// around. Plate boundaries (boundaryEffect, below) then only ADD relief
// (a mountain belt, a trench, a rift, an island arc) on top of whatever
// this field already decided about land and sea; they never independently
// decide it.
const (
	plateBiasAmplitude     = 900.0
	continentBlobAmplitude = 2400.0
)

// blendedPlateBias returns a cell's plate-type signal: +1 for continental
// crust, -1 for oceanic, blended toward the plate on the OTHER side of a
// nearby boundary so this signal itself has no hard step at a plate edge
// either — it fades in over the same maxSteps the boundary's own relief
// effect decays over, halfway blended (not fully swapped) right at the
// boundary itself.
func blendedPlateBias(ownType, otherType PlateType, b boundaryInfo, maxSteps int) float64 {
	own := -1.0
	if ownType == PlateContinental {
		own = 1.0
	}
	if b.StepsAway < 0 || b.StepsAway >= maxSteps {
		return own
	}
	other := -1.0
	if otherType == PlateContinental {
		other = 1.0
	}
	t := 1.0 - float64(b.StepsAway)/float64(maxSteps)
	return own + (other-own)*t*0.5
}

// boundaryEffect returns the elevation contribution (before noise) a
// boundary of the given kind and cell arrangement adds, decaying linearly to
// zero over Params.BoundaryInfluenceSteps. Each case models one real plate
// interaction:
//
//   - convergent, continental-continental: a collision belt (Himalayas).
//   - convergent, continental-oceanic (own continental): a volcanic
//     mountain range on the overriding continental plate (Andes).
//   - convergent, continental-oceanic (own oceanic): the subducting plate's
//     trench (Mariana-like), driven downward.
//   - convergent, oceanic-oceanic: an island arc, uplifted on both sides.
//   - divergent, continental-continental: a rift valley, lower than the
//     surrounding craton (East African Rift).
//   - divergent, oceanic-oceanic: a mid-ocean ridge, higher than the
//     surrounding ocean floor but still submerged.
//   - transform: minor uplift on the continental side only, nothing on
//     oceanic crust (a simplification of ranges like California's Coast
//     Ranges), otherwise left to noise.
func boundaryEffect(b boundaryInfo, maxSteps int) float64 {
	if !b.hasInfluence(maxSteps) {
		return 0
	}
	decay := 1.0 - float64(b.StepsAway)/float64(maxSteps)

	switch b.Kind {
	case BoundaryConvergent:
		switch {
		case b.OwnType == PlateContinental && b.OtherType == PlateContinental:
			return 3200 * decay
		case b.OwnType == PlateContinental && b.OtherType == PlateOceanic:
			return 2700 * decay
		case b.OwnType == PlateOceanic && b.OtherType == PlateContinental:
			return -1800 * decay
		default: // oceanic-oceanic
			return 1300 * decay
		}
	case BoundaryDivergent:
		if b.OwnType == PlateContinental {
			return -600 * decay
		}
		return 650 * decay
	case BoundaryTransform:
		if b.OwnType == PlateContinental {
			return 220 * decay
		}
		return 0
	default:
		return 0
	}
}

func (b boundaryInfo) hasInfluence(maxSteps int) bool {
	return b.IsBoundary || (b.StepsAway >= 0 && b.StepsAway < maxSteps)
}

// computeElevation returns the raw (pre-sea-level) elevation for every cell
// and, separately, SeaLevel: the raw elevation that splits cells into
// Params.LandFraction land and the rest ocean.
func computeElevation(mesh *Mesh, plates []Plate, plateOf []int16, boundaries []boundaryInfo, params Params, seed uint64) ([]float64, float64) {
	n := mesh.Len()
	raw := make([]float64, n)

	// The dominant, continent-shaping field: a few large, heavily
	// domain-warped lobes per hemisphere (low frequency, few octaves —
	// this is explicitly NOT meant to add fine detail, see
	// continentBlobAmplitude's doc above). This is what decides where the
	// coast falls; plate type only nudges it.
	continentField := newNoiseField(seed, "continent_blobs",
		3, 0.6, 550, 0.55, 0.3)

	// The finer-detail fields from here down add TEXTURE on top of the
	// shape continentField already decided — local relief, not coastline
	// shape — so their amplitudes are deliberately smaller than
	// continentBlobAmplitude now, unlike an earlier version where a field
	// much like elevationField here was the primary shape-former.
	elevationField := newNoiseField(seed, "elevation",
		params.NoiseOctaves, params.NoiseBaseFrequency, params.NoisePersistence,
		params.WarpAmplitude, params.WarpFrequency)
	coastlineField := newNoiseField(seed, "coastline_detail",
		4, params.NoiseBaseFrequency*5, 550,
		params.WarpAmplitude*0.4, params.WarpFrequency*2)

	for c := 0; c < n; c++ {
		pid := plateOf[c]
		pType := plates[pid].Type
		b := boundaries[c]

		otherType := pType
		if b.StepsAway >= 0 {
			otherType = b.OtherType
		}
		bias := blendedPlateBias(pType, otherType, b, params.BoundaryInfluenceSteps)

		be := boundaryEffect(b, params.BoundaryInfluenceSteps)
		p := mesh.Points[c]
		blob := continentField.Sample3(p.X, p.Y, p.Z)
		n1 := elevationField.Sample3(p.X, p.Y, p.Z)
		n2 := coastlineField.Sample3(p.X, p.Y, p.Z)

		raw[c] = plateBiasAmplitude*bias + continentBlobAmplitude*blob + be + 700*n1 + 400*n2
	}

	sorted := make([]float64, n)
	copy(sorted, raw)
	sort.Float64s(sorted)

	oceanCells := n - (n*int(params.LandFraction))/1000
	if oceanCells < 0 {
		oceanCells = 0
	}
	if oceanCells >= n {
		oceanCells = n - 1
	}
	seaLevel := sorted[oceanCells]

	return raw, seaLevel
}
