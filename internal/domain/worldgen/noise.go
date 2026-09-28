package worldgen

import "github.com/mrjvadi/torncity/internal/domain/worldgen/fastnoise"

// EXACT. Noise is FastNoiseLite's OpenSimplex2 (fractal FBm) with an
// OpenSimplex2 progressive domain warp applied to the sampling coordinates
// first — see internal/domain/worldgen/fastnoise's package doc for why that
// library and why it still counts as exact: every function this file calls
// uses only +, -, *, /, floor and sqrt, the operations IEEE-754 requires to
// be identical on any conformant platform. This is also the same noise
// algorithm the web client and the Godot client (which ships FastNoiseLite
// as a built-in class) can use, so a rendered preview and an eventual
// client-side render of the same seed are the same algorithm end to end —
// not merely both "some Perlin-ish noise".
//
// Everything is sampled directly on the unit sphere (x, y, z), never
// (latitude, longitude): a 2D noise texture addressed by lat/lon has a seam
// at +-180 degrees and pinches every octave's texture into a point at each
// pole, both of which would show up as a visible artifact on a rendered
// planet. 3D noise evaluated at points that already lie on a sphere has
// neither problem — it is simply a smooth field over the whole surface.
//
// DOMAIN WARPING, AND WHY. Elevation without it (an early version of this
// package) added noise on top of a plate-tectonics base signal, but a
// coastline is decided by comparing the SUM against sea level — and where
// the boundary-driven part of that sum dominates, the resulting coastline
// still traces the smooth geometric shape of the plate boundary that
// produced it (a graph-Voronoi partition), which reads as an artificial
// straight edge or polygon facet rather than a weathered coastline. Domain
// warping distorts the COORDINATES a noise field is sampled at, before
// sampling, using a second independent noise field, so the same underlying
// signal comes out bent and stretched rather than read off along a
// straight line. Applied to elevation, this is what breaks a coastline (or
// a mountain range, or — see climate.go — a climate band) away from the
// geometric shape of whatever produced it.

// noiseField is one independently-seeded noise generator: a domain-warp
// pass feeding a fractal base noise. Two fields built from different tags
// never correlate, the same way Rand.Sub keeps its RNG streams independent
// — renaming or retuning one noise field never perturbs another one's
// output for the same world seed.
//
// Built ONCE per call to Generate (never per cell) and reused across every
// sample: constructing a fastnoise.State does a small amount of setup
// (picking the function pointers FractalType/NoiseType select), cheap once
// but wasteful tens of thousands of times over.
type noiseField struct {
	warp *fastnoise.State[float64]
	base *fastnoise.State[float64]
}

// newNoiseField builds a named noise field. octaves/frequency/gain
// configure the base fractal FBm; warpAmp/warpFrequency configure the
// domain warp applied before sampling it. warpAmp is in the same units as
// the sphere coordinates (roughly -1..1 per axis), so even a modest value
// (a few tenths) meaningfully bends the field at this scale.
func newNoiseField(seed uint64, tag string, octaves int, frequency float64, gain Permille, warpAmp, warpFrequency float64) *noiseField {
	// The tag is hashed into the seed exactly like Rand.Sub does, so a
	// noise field and an RNG stream sharing a tag are still unrelated —
	// nothing here promises or relies on them agreeing.
	fieldSeed := int32(splitmix64Finalize(seed ^ fnv1a64(tag)))

	base := fastnoise.New[float64]()
	base.Seed = int(fieldSeed)
	base.Frequency = frequency
	base.Octaves = octaves
	base.Gain = float64(gain) / 1000
	base.NoiseType(fastnoise.OpenSimplex2)
	base.FractalType(fastnoise.FractalFBm)

	warp := fastnoise.New[float64]()
	// +1 only to give the warp pass a distinct seed from the base field it
	// feeds; both are still deterministic functions of the same tag.
	warp.Seed = int(fieldSeed + 1)
	warp.Frequency = warpFrequency
	warp.DomainWarpAmp = warpAmp
	// 2 octaves, not 3: a domain warp's job is to bend the base field's
	// coordinates, not to add its own fine detail, and this is evaluated
	// on every sample the base field takes — its cost is directly the
	// generator's per-cell noise budget.
	warp.Octaves = 2
	warp.DomainWarpType = fastnoise.DomainWarpOpenSimplex2
	warp.FractalType(fastnoise.FractalDomainWarpProgressive)

	return &noiseField{warp: warp, base: base}
}

// Sample3 returns a value in [-1,1] at a point on the unit sphere, after
// warping the sampling coordinates through this field's own warp pass.
func (f *noiseField) Sample3(x, y, z float64) float64 {
	wx, wy, wz := f.warp.DomainWarp3D(x, y, z)
	return f.base.GetNoise3D(wx, wy, wz)
}
