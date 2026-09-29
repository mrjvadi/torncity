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
// plate a cell sits on, and never by a plate BOUNDARY either. An earlier
// version used plate type as a hard +-4100-unit step (a full ocean/
// continent base height); coastlines traced the plate boundary's own
// graph-Voronoi shape almost everywhere. Warping the noise on top of that
// step (a version after that one) fixed most of it, but boundary relief
// itself was still a second, independent way to create or erase a
// coastline: a long, roughly-straight boundary's mountain-building or
// trench-digging bump, decaying by integer graph-hop count, uplifted or
// subsided an entire PARALLEL STRIP of cells by the same amount at the same
// hop distance — visible as long straight coasts running exactly along
// plate boundaries in renders, and as banding/terracing near them (every
// cell at one hop count getting identical relief).
//
// Two changes fix this, both in computeElevation below:
//
//  1. The distance a boundary's relief decays over is no longer the integer
//     hop count (StepsAway) but a WARPED REAL distance (boundaryInfo.
//     RealDistance, a summed chord length, perturbed by its own noise
//     field before being turned into a falloff with smoothstep). A hop
//     count is the same for every cell along a straight boundary; a warped
//     real distance is not — it wanders, so the influence zone's edge
//     wanders with it instead of tracing a parallel line.
//  2. Boundary relief's magnitude is additionally scaled by how far the
//     PLATE/CONTINENT-BLOB BASE FIELD (before any boundary effect) already
//     sits from sea level (coastGuardScale). Near that base field's own
//     coastline, where a modest push would flip a cell from land to sea or
//     back, boundary relief is damped down; far from it — deep in a
//     continent's interior or deep ocean, where the category is already
//     decided — it applies at full strength. The result is what boundaries
//     are supposed to do: build real mountains and dig real trenches,
//     without being a second, competing decision about where the coast is.
const (
	plateBiasAmplitude     = 900.0
	continentBlobAmplitude = 2400.0

	// coastGuardWidth is the distance (in the same units as the base field,
	// roughly +-3300 end to end) within which boundary relief is damped
	// down because a push of that size could plausibly flip a cell's
	// land/sea category. coastGuardMinScale is the floor that damping
	// never goes below — real coastal mountains and fjords exist, so
	// boundary relief is reduced near the coast, never zeroed.
	coastGuardWidth    = 900.0
	coastGuardMinScale = 0.20

)

// boundaryPeakMagnitude returns the elevation contribution a boundary of the
// given kind and cell arrangement adds AT ITS STRONGEST (StepsAway==0,
// coastGuardScale==1) — the caller (computeElevation) scales it down by
// distance and by proximity to the coast. Each case models one real plate
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
func peakMagnitude(kind BoundaryKind, ownType, otherType PlateType) float64 {
	b := struct{ OwnType, OtherType PlateType }{ownType, otherType}
	switch kind {
	case BoundaryConvergent:
		switch {
		case b.OwnType == PlateContinental && b.OtherType == PlateContinental:
			return 3200
		case b.OwnType == PlateContinental && b.OtherType == PlateOceanic:
			return 2700
		case b.OwnType == PlateOceanic && b.OtherType == PlateContinental:
			return -1800
		default: // oceanic-oceanic
			return 1300
		}
	case BoundaryDivergent:
		if b.OwnType == PlateContinental {
			return -600
		}
		return 650
	case BoundaryTransform:
		if b.OwnType == PlateContinental {
			return 220
		}
		return 0
	default:
		return 0
	}
}

// coastGuardScale damps boundary relief near the base field's OWN sea
// level, where a modest push could flip a cell's land/sea category, and
// lets it through at full strength far from it. distFromSeaLevel may be
// positive (base land) or negative (base ocean); only its magnitude
// matters.
func coastGuardScale(distFromSeaLevel float64) float64 {
	ad := distFromSeaLevel
	if ad < 0 {
		ad = -ad
	}
	t := smoothstep(ad / coastGuardWidth)
	return coastGuardMinScale + (1-coastGuardMinScale)*t
}

// computeElevation returns the raw (pre-sea-level) elevation for every cell
// and, separately, SeaLevel: the raw elevation that splits cells into
// Params.LandFraction land and the rest ocean.
func computeElevation(mesh *Mesh, plates []Plate, plateOf []int16, boundaries []boundaryInfo, params Params, seed uint64) ([]float64, float64) {
	n := mesh.Len()

	// The dominant, continent-shaping field: a few large, heavily
	// domain-warped lobes per hemisphere (low frequency, few octaves —
	// this is explicitly NOT meant to add fine detail, see
	// continentBlobAmplitude's doc above). This is what decides where the
	// coast falls; plate type only nudges it, and boundaries (below) only
	// add relief on top of whatever it already decided.
	continentField := newNoiseField(seed, "continent_blobs",
		3, 0.6, 550, 0.55, 0.3)

	// PASS 1: the base field alone (plate bias, at its un-blended strength,
	// plus the continent blobs), with no boundary relief and no fine
	// texture. Its own percentile sea level is what coastGuardScale
	// measures distance from — a preliminary threshold, not the real one
	// (computed after pass 2, on the full field including relief and
	// texture), but exactly what "is this cell already deep in decided
	// land/ocean, or near the edge" needs.
	baseOnly := make([]float64, n)
	for c := 0; c < n; c++ {
		pType := plates[plateOf[c]].Type
		own := -1.0
		if pType == PlateContinental {
			own = 1.0
		}
		p := mesh.Points[c]
		blob := continentField.Sample3(p.X, p.Y, p.Z)
		baseOnly[c] = plateBiasAmplitude*own + continentBlobAmplitude*blob
	}
	baseSeaLevel := percentile(baseOnly, params.LandFraction)

	// The finer-detail fields from here down add TEXTURE and RELIEF on top
	// of the shape pass 1 already decided — local relief, not coastline
	// shape — so their amplitudes are deliberately smaller than
	// continentBlobAmplitude, unlike an earlier version where a field much
	// like elevationField here was the primary shape-former.
	elevationField := newNoiseField(seed, "elevation",
		params.NoiseOctaves, params.NoiseBaseFrequency, params.NoisePersistence,
		params.WarpAmplitude, params.WarpFrequency)
	coastlineField := newNoiseField(seed, "coastline_detail",
		4, params.NoiseBaseFrequency*5, 550,
		params.WarpAmplitude*0.4, params.WarpFrequency*2)
	raw := make([]float64, n)
	for c := 0; c < n; c++ {
		b := boundaries[c]
		p := mesh.Points[c]

		guard := coastGuardScale(baseOnly[c] - baseSeaLevel)
		be := b.Relief * guard
		bias := b.Bias

		blob := continentField.Sample3(p.X, p.Y, p.Z)
		n1 := elevationField.Sample3(p.X, p.Y, p.Z)
		n2 := coastlineField.Sample3(p.X, p.Y, p.Z)

		raw[c] = plateBiasAmplitude*bias + continentBlobAmplitude*blob + be + 700*n1 + 400*n2
	}

	seaLevel := percentile(raw, params.LandFraction)
	return raw, seaLevel
}

// percentile returns the value that splits values into landFraction/1000
// above it and the rest at or below it.
func percentile(values []float64, landFraction Permille) float64 {
	n := len(values)
	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)

	oceanCells := n - (n*int(landFraction))/1000
	if oceanCells < 0 {
		oceanCells = 0
	}
	if oceanCells >= n {
		oceanCells = n - 1
	}
	return sorted[oceanCells]
}
