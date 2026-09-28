package worldgen

import "math"

// EXACT. This file's noise is trilinear VALUE noise on an integer lattice,
// deliberately chosen over the more common gradient (Perlin) noise: gradient
// noise needs a pseudo-random unit VECTOR per lattice corner, built from a
// dot product with sin/cos or a normalization sqrt of two random components,
// which reintroduces exactly the cross-platform rounding risk rng.go and
// geometry.go's package comment describe. Value noise instead hashes each
// lattice corner straight to a scalar and blends corners with polynomial
// (smoothstep) weights — only +, -, *, / and floor, every one of them an
// IEEE-754 operation required to be exact and therefore identical on any
// conformant platform. The trade-off is a slightly less isotropic look than
// gradient noise at a single octave; summed across NoiseOctaves (fractal
// Brownian motion) the difference is not visible on a rendered planet.

// latticeValue returns a deterministic value in [0,1) for one integer lattice
// point, seeded so that two different seeds (or two different noise
// "channels" mixed into the same seed by the caller) never correlate.
func latticeValue(seed uint64, ix, iy, iz int64) float64 {
	h := uint64(ix)*0x9E3779B97F4A7C15 ^
		uint64(iy)*0xC2B2AE3D27D4EB4F ^
		uint64(iz)*0x165667B19E3779F9 ^
		seed
	h = splitmix64Finalize(h)
	return float64(h>>11) / (1 << 53)
}

// smoothstep is the standard 3t^2-2t^3 ease curve used to blend between
// lattice corners so the noise field has a continuous derivative (no visible
// grid lines).
func smoothstep(t float64) float64 {
	return t * t * (3 - 2*t)
}

// valueNoise3 samples one octave of 3D value noise at (x,y,z), trilinearly
// interpolating the 8 surrounding lattice corners.
func valueNoise3(seed uint64, x, y, z float64) float64 {
	x0 := math.Floor(x)
	y0 := math.Floor(y)
	z0 := math.Floor(z)
	tx := smoothstep(x - x0)
	ty := smoothstep(y - y0)
	tz := smoothstep(z - z0)

	ix0, iy0, iz0 := int64(x0), int64(y0), int64(z0)

	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }

	c000 := latticeValue(seed, ix0, iy0, iz0)
	c100 := latticeValue(seed, ix0+1, iy0, iz0)
	c010 := latticeValue(seed, ix0, iy0+1, iz0)
	c110 := latticeValue(seed, ix0+1, iy0+1, iz0)
	c001 := latticeValue(seed, ix0, iy0, iz0+1)
	c101 := latticeValue(seed, ix0+1, iy0, iz0+1)
	c011 := latticeValue(seed, ix0, iy0+1, iz0+1)
	c111 := latticeValue(seed, ix0+1, iy0+1, iz0+1)

	x00 := lerp(c000, c100, tx)
	x10 := lerp(c010, c110, tx)
	x01 := lerp(c001, c101, tx)
	x11 := lerp(c011, c111, tx)

	y0v := lerp(x00, x10, ty)
	y1v := lerp(x01, x11, ty)

	return lerp(y0v, y1v, tz)
}

// fbm sums NoiseOctaves of value noise at doubling frequency and decaying
// amplitude (fractal Brownian motion) and returns a value in [-1,1].
// seedTag lets a caller draw an independent noise FIELD (elevation vs. a
// second texture-only detail layer, say) from the same world seed, the same
// way Rand.Sub separates independent random streams.
func fbm(seed uint64, tag string, p Point, octaves int, baseFrequency float64, persistence Permille) float64 {
	fieldSeed := splitmix64Finalize(seed ^ fnv1a64(tag))

	amplitude := 1.0
	frequency := baseFrequency
	total := 0.0
	maxAmp := 0.0
	persist := float64(persistence) / 1000.0

	for o := 0; o < octaves; o++ {
		n := valueNoise3(fieldSeed, p.X*frequency, p.Y*frequency, p.Z*frequency)*2 - 1
		total += n * amplitude
		maxAmp += amplitude
		amplitude *= persist
		frequency *= 2
		// Perturb the per-octave seed so octaves don't just look like the
		// same texture at different scales stacked on itself.
		fieldSeed = splitmix64Finalize(fieldSeed + uint64(o) + goldenGamma)
	}
	if maxAmp == 0 {
		return 0
	}
	return total / maxAmp
}
