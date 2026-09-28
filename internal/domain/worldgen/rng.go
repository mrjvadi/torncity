package worldgen

// This file is the ONLY source of randomness the generator uses. That is a
// deliberate constraint, not a style preference.
//
// # Why not math/rand
//
// math/rand's global functions draw from a single shared source: two calls
// from two different subsystems race against each other for "who draws
// first", so the same seed can produce a different world depending on
// goroutine scheduling or on the order two independent features happen to be
// coded in. math/rand/v2's algorithms are a better fit but are still a Go
// standard library implementation with no promise of matching a
// reimplementation in another language bit for bit — and the owner has asked
// for a design where a TypeScript client could one day reproduce the same
// classification from the same seed. That rules out depending on any
// language's black-box PRNG, including Go's.
//
// So the generator carries its own PRNG: SplitMix64, a full-period 64-bit
// generator with a public, one-line specification (a single 64-bit state,
// advanced by a fixed additive constant, then run through a fixed bit-mixing
// finalizer). It is the algorithm behind Java's SplittableRandom and is
// usually used to SEED a fancier generator; here it is used directly as the
// stream, because its statistical quality is more than sufficient for
// terrain placement and its simplicity is exactly what "a TypeScript port
// must match this" needs: nine lines of unsigned 64-bit arithmetic, no
// library, no platform-dependent rounding.
//
// # Independent streams instead of one shared generator
//
// A single Rand shared across plates, elevation, rivers and resources would
// make every one of those subsystems sensitive to the others: reordering two
// unrelated loops, or adding one extra random draw to name generation, would
// silently shift every draw that comes after it and change the whole planet.
// Sub derives an independent stream for a named purpose by hashing the seed
// together with a stream tag through SplitMix64's own finalizer. Two streams
// derived this way are independent generators with unrelated state; drawing
// from one never perturbs the other, and adding a new stream never disturbs
// the ones that already exist.
//
// # What must never happen
//
// Iterating a Go map and feeding the iteration order into a Rand draw (or
// into anything hashed into the world's fingerprint) is a determinism bug:
// Go deliberately randomises map iteration order per process. Every ordered
// operation in this package walks a slice indexed by a stable cell ID, never
// a map.
type Rand struct {
	state uint64
}

// goldenGamma is the odd 64-bit constant SplitMix64 advances its state by. It
// is fixed by the algorithm's specification (derived from the golden ratio),
// not a tunable.
const goldenGamma = 0x9E3779B97F4A7C15

// NewRand builds a stream seeded directly from a 64-bit value.
func NewRand(seed uint64) *Rand {
	return &Rand{state: seed}
}

// Sub derives an independent, named sub-stream from r.
//
// The same (r, tag) pair always derives the same child stream, and different
// tags derive streams with no relationship to each other or to r's own
// subsequent draws — drawing from the child never advances r, and later
// draws from r never affect a child already derived. This is what lets every
// subsystem own a private, order-independent stream: "plates", "rivers",
// "resource:crude_oil" and "name:continent:3" are all reproducible from the
// world seed alone, regardless of what order the generator's Go code happens
// to call them in.
func (r *Rand) Sub(tag string) *Rand {
	h := fnv1a64(tag)
	// Mix the tag hash with the parent's CURRENT state (not just the root
	// seed) via one SplitMix64 step, so a tag collision would need both an
	// identical tag and an identical parent state to collide — and mix again
	// so the child's own first draw does not directly expose the parent
	// state it was derived from.
	mixed := splitmix64Step(r.state ^ h)
	return &Rand{state: splitmix64Step(mixed ^ goldenGamma)}
}

// Uint64 returns the next value in the stream. This is the ONLY primitive;
// every other method is built from it, so there is exactly one place that
// needs to match a reimplementation.
func (r *Rand) Uint64() uint64 {
	r.state += goldenGamma
	return splitmix64Finalize(r.state)
}

// splitmix64Step advances a state word by one SplitMix64 increment and
// returns the finalized output, without needing a *Rand. Used only to derive
// sub-stream seeds.
func splitmix64Step(state uint64) uint64 {
	return splitmix64Finalize(state + goldenGamma)
}

// splitmix64Finalize is SplitMix64's fixed bit-mixing finalizer (Murmur3-style
// avalanche, the exact constants from the reference algorithm). Every bit of
// the output depends on every bit of the input.
func splitmix64Finalize(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// fnv1a64 hashes a short ASCII tag deterministically. FNV-1a is used here
// only to turn a human-readable tag into a 64-bit seed, not as the
// generator's PRNG.
func fnv1a64(s string) uint64 {
	const offset = 14695981039346656037
	const prime = 1099511628211
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// UintN returns a uniform value in [0, n) with no modulo bias, for n > 0.
//
// PORTABILITY: this is Lemire's rejection method using only unsigned 64-bit
// multiply, shift and compare — operations that behave identically on any
// platform with 64-bit unsigned integers, including a TypeScript port using
// BigInt. A naive Uint64() % n is avoided because it is biased whenever n does
// not evenly divide 2^64, and that bias is exactly the kind of tiny,
// silent, seed-dependent skew this package exists to avoid.
func (r *Rand) UintN(n uint64) uint64 {
	if n == 0 {
		panic("worldgen: UintN(0)")
	}
	// threshold is the largest multiple of n that fits in 64 bits, expressed
	// as what remains after it: draws landing in that remainder are rejected
	// and redrawn so every accepted draw is exactly uniform.
	threshold := -n % n
	for {
		x := r.Uint64()
		hi, lo := bits64Mul(x, n)
		_ = lo
		if lo := mul64Low(x, n); lo >= threshold {
			return hi
		}
	}
}

// bits64Mul and mul64Low split a 64x64->128 bit multiply into its high and
// low 64-bit halves using only 64-bit arithmetic (no math/bits dependency),
// so the same four-multiplication decomposition can be read straight across
// into a TypeScript BigInt implementation.
func bits64Mul(x, y uint64) (hi, lo uint64) {
	const mask32 = 0xFFFFFFFF
	xLo, xHi := x&mask32, x>>32
	yLo, yHi := y&mask32, y>>32

	t := xLo * yLo
	w0 := t & mask32
	k := t >> 32

	t = xHi*yLo + k
	w1 := t & mask32
	w2 := t >> 32

	t = xLo*yHi + w1
	k = t >> 32

	hi = xHi*yHi + w2 + k
	lo = (t << 32) | w0
	return hi, lo
}

func mul64Low(x, y uint64) uint64 {
	_, lo := bits64Mul(x, y)
	return lo
}

// IntN returns a uniform int in [0, n) for n > 0. A thin, more convenient
// wrapper over UintN for the common case of indexing a slice.
func (r *Rand) IntN(n int) int {
	if n <= 0 {
		panic("worldgen: IntN(<=0)")
	}
	return int(r.UintN(uint64(n)))
}

// Bool returns a uniform coin flip.
func (r *Rand) Bool() bool {
	return r.Uint64()&1 == 1
}

// Fixed returns a uniform fixed-point value in [0, scale) as a plain integer
// numerator over that scale — e.g. Fixed(1000) is a uniform "permille" value
// usable directly as 0.0%..100.0% without ever constructing a float. Prefer
// this over Float64 for anything that feeds a gameplay decision.
func (r *Rand) Fixed(scale int64) int64 {
	if scale <= 0 {
		panic("worldgen: Fixed(<=0)")
	}
	return int64(r.UintN(uint64(scale)))
}

// Float64 returns a uniform value in [0, 1) as an IEEE-754 double.
//
// VISUAL ONLY. This exists for rendering and cosmetic jitter (nudging a
// sphere point off its lattice position, choosing a display color offset)
// where a fraction-of-a-percent difference between platforms is invisible.
// No gameplay-relevant branch (which biome, which resource, how large a
// deposit, which cell a river flows to) may depend on a value from this
// method; use Fixed or UintN instead so the decision is an exact integer
// comparison that any correct SplitMix64 implementation reproduces bit for
// bit, on any platform.
func (r *Rand) Float64() float64 {
	// 53 bits of mantissa, the same construction Go's own math/rand uses,
	// kept here so the "visual only" numbers still look like ordinary
	// uniform floats rather than being decimated to fewer bits.
	return float64(r.Uint64()>>11) / (1 << 53)
}
