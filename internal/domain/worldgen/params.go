// Package worldgen generates a whole planet — plates, elevation, climate,
// biomes, rivers and natural-resource deposits — from a seed and a small set
// of parameters, and nothing else. It performs no I/O: everything it needs
// (which biomes exist, which resources exist and where geology favours them)
// is handed in already parsed and validated as Content, the same split
// docs/adr/0004-content-system.md draws for the rest of the game. Generating
// the same seed and the same Content twice always produces an identical
// World; see doc_test.go for the test that guards that promise.
//
// # Seed-first
//
// A World is never itself "the truth" that gets stored. The truth is the
// triple (Seed, GeneratorVersion, Params/Content) — a few hundred bytes. Any
// process, at any time, can hand that triple back to Generate and get the
// exact same planet back. What DOES get stored durably is only what the seed
// cannot reproduce because it changed after generation: how much of a
// deposit has been mined out, who claimed it, what a player built on a cell.
// That mirrors how Minecraft stores a world seed plus only the chunks a
// player actually modified, and it is why this package fixes
// GeneratorVersion (world.go): once a version has shipped, changing this
// package's algorithm would silently regenerate a different planet under an
// already-settled seed, which is the one thing that must never happen to a
// live world. A change to the algorithm bumps the version instead, and old
// worlds keep loading under the version they were made with.
//
// # Exact vs visual
//
// Every exported decision that a player's outcome depends on — which biome a
// cell is, whether a resource deposit exists there and how large and pure it
// is, where a river's course runs — is computed from this package's own
// integer PRNG (rng.go) and from integer or fixed-point arithmetic, so that a
// from-scratch reimplementation of this algorithm in another language,
// following this package's doc comments, reproduces the identical result for
// the identical seed. Anything computed instead from floating-point
// trigonometry (the exact 3D position of a cell on the sphere, an
// equirectangular pixel projection) is marked VISUAL ONLY in its doc comment:
// it can shift by a fraction of a bit between math libraries, and nothing
// that a player can act on is allowed to depend on it. See geometry.go for
// the one place that boundary is drawn.
package worldgen

// Fixed-point value types. Every one of these is a plain integer so that a
// gameplay decision made by comparing two of them is an exact, portable
// comparison — never a floating-point one that could round differently
// between the number that was stored and the number a re-derivation
// produces.
type (
	// Elevation is height above/below sea level in abstract units, roughly
	// -10000 (deep ocean trench) .. 10000 (highest peaks). Zero is sea level
	// by definition — SeaLevelElevation, not a fixed constant, is chosen per
	// world so that the configured land fraction comes out right; see
	// elevation.go.
	Elevation int32

	// Temp is temperature in centi-degrees Celsius (2500 = 25.00C).
	Temp int32

	// Precip is annual precipitation in millimetres/year, 0..~6000.
	Precip int32

	// Permille is a fraction expressed as parts-per-thousand, 0..1000. Used
	// for grade/purity, humidity and any other "percentage-like" value that
	// must stay an exact integer rather than a float that a resource sale
	// price could compound rounding error on.
	Permille int32
)

// Params are the tunable NUMBERS behind generation: how many cells, how many
// plates, what fraction of the world is land. They are TUNING
// (docs/adr/0004-content-system.md's sense: a coefficient, not a rule) and
// belong in configs/config.yml, not in code and not in configs/content: they
// change what the generator asks for, not what it is capable of describing.
type Params struct {
	// CellCount is the number of mesh cells covering the sphere. 20_000-60_000
	// is the target range in the project brief; DefaultParams uses 40_000.
	CellCount int

	// NeighborK is how many nearest neighbours each cell connects to (see
	// geometry.go). 6 approximates the hexagonal adjacency a true spherical
	// Voronoi diagram would give.
	NeighborK int

	// PlateCount is how many tectonic plates the mesh is partitioned into.
	PlateCount int

	// OceanicPlateFraction is how many of PlateCount are oceanic (permille of
	// PlateCount), the rest continental. Earth is roughly balanced toward
	// ocean floor by AREA but plates themselves are a mix; this governs the
	// mix of plate TYPES, area balance instead comes from LandFraction below.
	OceanicPlateFraction Permille

	// LandFraction is the target share of cells that end up above sea level,
	// in permille. DefaultParams uses 450 (45.0%) rather than Earth's own
	// ~29%: the owner's world exists for Telegram groups to settle on, so
	// more land means more room for groups, at the cost of the planet
	// looking somewhat less "wet" than Earth's own photographs. It is a
	// tuning knob, not a rule — nothing downstream assumes a particular
	// value.
	LandFraction Permille

	// NoiseOctaves is how many octaves of fractal noise are summed into the
	// elevation field. More octaves add finer detail at higher generation
	// cost.
	NoiseOctaves int

	// NoiseBaseFrequency is the spatial frequency of the coarsest noise
	// octave, in cycles across the sphere's diameter. Larger values shrink
	// the size of continents relative to the whole planet.
	NoiseBaseFrequency float64

	// NoisePersistence is the amplitude multiplier applied each time the
	// frequency doubles for the next octave (permille; 500 = each octave
	// contributes half the amplitude of the last).
	NoisePersistence Permille

	// BoundaryInfluenceSteps is how many graph-steps a plate boundary's
	// effect (mountain building, rifting) reaches before decaying to zero.
	BoundaryInfluenceSteps int

	// MoistureBands is how many latitude bands the wind/moisture simulation
	// divides the sphere into (climate.go). More bands is a finer-grained
	// approximation of the Hadley/Ferrel/polar circulation cells at the cost
	// of generation time.
	MoistureBands int

	// RiverFlowThreshold is the minimum flow accumulation (a cell count) a
	// cell needs before it is classified as carrying a river rather than
	// just hillside runoff.
	RiverFlowThreshold int

	// LakeMinDepth is the minimum fill depth (Elevation units) a filled
	// depression needs before it is rendered as a lake rather than treated as
	// a rounding artifact of the depression-filling step.
	LakeMinDepth Elevation
}

// DefaultParams returns the parameters used when nothing more specific is
// given — the CLI preview's default and the values documented in the
// project's report.
func DefaultParams() Params {
	return Params{
		CellCount:              40_000,
		NeighborK:              6,
		PlateCount:             16,
		OceanicPlateFraction:   550,
		LandFraction:           450,
		NoiseOctaves:           6,
		NoiseBaseFrequency:     2.0,
		NoisePersistence:       520,
		BoundaryInfluenceSteps: 9,
		MoistureBands:          90,
		RiverFlowThreshold:     12,
		LakeMinDepth:           40,
	}
}

// Validate reports whether Params can be generated from at all: the
// generator's own defensive check against a corrupt or hand-edited config,
// distinct from Content.Validate which checks the authored rules.
func (p Params) Validate() error {
	switch {
	case p.CellCount < 100 || p.CellCount > 500_000:
		return errInvalidParam("cell_count", "must be between 100 and 500000")
	case p.NeighborK < 3 || p.NeighborK > 12:
		return errInvalidParam("neighbor_k", "must be between 3 and 12")
	case p.PlateCount < 2 || p.PlateCount > 200:
		return errInvalidParam("plate_count", "must be between 2 and 200")
	case p.LandFraction <= 0 || p.LandFraction >= 1000:
		return errInvalidParam("land_fraction", "must be between 0 and 1000 permille, exclusive")
	case p.NoiseOctaves < 1 || p.NoiseOctaves > 12:
		return errInvalidParam("noise_octaves", "must be between 1 and 12")
	case p.MoistureBands < 6 || p.MoistureBands > 720:
		return errInvalidParam("moisture_bands", "must be between 6 and 720")
	case p.RiverFlowThreshold < 1:
		return errInvalidParam("river_flow_threshold", "must be at least 1")
	}
	return nil
}
