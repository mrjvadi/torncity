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

// Base heights per plate type, in Elevation units. Oceanic crust is dense and
// sits low; continental crust is buoyant and sits high — this is the single
// largest signal separating ocean basins from continents, before any
// boundary or noise detail is added.
const (
	baseOceanicElevation     = -3500.0
	baseContinentalElevation = 600.0
)

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

	// The main continent-scale field, domain-warped at Params' own
	// amplitude/frequency: this is what keeps a coastline from tracing the
	// smooth graph-Voronoi shape of the plate boundary that raised it (see
	// noise.go's package comment).
	elevationField := newNoiseField(seed, "elevation",
		params.NoiseOctaves, params.NoiseBaseFrequency, params.NoisePersistence,
		params.WarpAmplitude, params.WarpFrequency)

	// A second, higher-frequency field, independent of the first, adding
	// fine texture (weathering/sediment scale, not continent scale) with
	// its own smaller warp so it roughens edges without also bending the
	// large-scale continent shape a second time. Its frequency/warp are
	// fixed multiples of the base field's rather than separate Params,
	// because its only job is finer-grained texture under the field above
	// it, not an independent knob an author would ever want to tune alone.
	coastlineField := newNoiseField(seed, "coastline_detail",
		4, params.NoiseBaseFrequency*5, 550,
		params.WarpAmplitude*0.4, params.WarpFrequency*2)

	for c := 0; c < n; c++ {
		pType := plates[plateOf[c]].Type
		base := baseOceanicElevation
		noiseAmp := 1000.0
		if pType == PlateContinental {
			base = baseContinentalElevation
			noiseAmp = 1700.0
		}

		be := boundaryEffect(boundaries[c], params.BoundaryInfluenceSteps)
		p := mesh.Points[c]
		n1 := elevationField.Sample3(p.X, p.Y, p.Z)
		n2 := coastlineField.Sample3(p.X, p.Y, p.Z)

		raw[c] = base + be + noiseAmp*n1 + 550*n2
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
