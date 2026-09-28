package worldgen

import (
	"math"
	"sync"
)

// This file adds a FINE, lot-resolution sampling method on top of the same
// coarse-mesh machinery chunk.go's GenerateChunk already uses
// (coarseCandidates, sampleCoarseAmong, the base-LOD detail/stream noise
// fields built once in Generate) — but callable at an EXACT lat/lon point
// instead of snapped to a chunk-grid tile centre, and at a much finer
// wavelength (a settlement LOT, roughly a tenth of a base tile) than a
// chunk's own ~305m TILE.
//
// WHY THIS EXISTS. cmd/worldpreview's --export-city demo used to lay a
// settlement's building lots directly on base terrain tiles, which made
// every building ~305m (Params.TileMeters) wide and a 15x15 city ~4.5km
// across. Nothing about what a base-LOD TILE means changes here —
// GenerateChunk, chunk_golden_test.go and every existing Params field are
// untouched; this is strictly an ADDITIVE sampling layer read on top of an
// already-generated World, for the demo export to place lots at a realistic
// scale. See docs/adr/0028-world-and-settlements.md's lot-count table and
// the project report for the full story.
//
// PURE AND ADDITIVE. This file adds no field to World and changes no
// existing method: the lazily-built fine detail noise field lives in a
// package-level cache keyed by seed (fineNoiseCache below), not on World
// itself, specifically so Generate's eager construction, World's exported
// shape and every existing golden/fingerprint test stay byte-for-byte
// unchanged. SampleFineLatLon is a pure function of (w.Seed, w.Params,
// w.Content, latDeg, lonDeg) — see fine_test.go's determinism test.

// StreamKind classifies a fine sample's water channel, narrower than
// ChunkTile's single IsStream() bit: a settlement lot needs to know not
// just "is there flowing water here" but "is this a footbridge-width brook
// or a river too wide to bridge" — see cmd/worldpreview/citylayout.go's
// bridge-vs-detour logic, which is the reason this distinction exists.
type StreamKind uint8

const (
	StreamKindNone StreamKind = iota
	StreamKindStream
	StreamKindRiver
)

// FineSample is one exact lat/lon point's terrain, at LOT resolution rather
// than a base chunk's ~305m TILE resolution.
type FineSample struct {
	// ElevationM is the coarse-interpolated elevation (sampleCoarseAmong,
	// same numeric units Cell.Elevation/ChunkTile.Elevation already use —
	// see Elevation's own doc in params.go for why that unit is loosely
	// metre-scaled, not literally metres) plus a small lot-scale detail
	// noise term (fineDetailNoiseFor below). Left as an unclamped float64,
	// unlike ChunkTile.Elevation's int16: a fine sample is never stored or
	// moved over the wire at chunk.go's own grid scale, so there is no
	// reason to narrow or quantize it early.
	ElevationM float64
	Biome      uint8
	IsOcean    bool
	IsLake     bool
	StreamKind StreamKind
}

// fineDetailFrequency/fineDetailAmplitudeM tune fineDetailNoiseFor's noise
// field, the lot-scale surface texture layered on top of the
// coarse-interpolated elevation SampleFineLatLon reads — the same role
// ChunkDetailAmplitude plays at tile scale (chunk.go), one further octave
// down. Same derivation as ChunkDetailFrequency's own doc comment
// (params.go): a point on the unit sphere sees a real wavelength of
// PlanetRadiusKm/Frequency kilometres, so this frequency is picked against
// DefaultParams' own PlanetRadiusKm (6371) for a wavelength of roughly
// 150m — a handful of the ~30m lots Part 2's lotMeters works in, clearly
// finer than ChunkDetailFrequency's own ~3km chunk-scale hills but still
// several lots wide itself, so it reads as gentle surface texture rather
// than a different value on every neighbouring lot. Fixed constants, not
// exposed through Params, same reasoning climateJitterFrequency's own doc
// comment (chunk.go) gives: this noise has no gameplay weight of its own,
// only a demo-visual one.
const (
	fineDetailFrequency  = 42000.0
	fineDetailAmplitudeM = 3.0
)

// fineStreamBand/fineRiverBand are SampleFineLatLon's isoline half-widths
// for chunkStreamNoise (reused UNMODIFIED from world.go/chunk.go — this
// file builds no noise field of its own for streams). GenerateChunk's own
// band (+-0.06, chunk.go) is calibrated so the isoline reads as roughly a
// WHOLE base tile's worth of channel (~305m) at tile-grid sampling
// spacing; sampled at LOT resolution (roughly a tenth of that spacing,
// Part 2's lotMeters) the same noise field's isoline needs a
// proportionally narrower band to still read as a channel a few lots wide
// instead of restating a whole tile's width at finer resolution. Tuned
// empirically against chunkStreamNoise's own frequency (fine_test.go's
// TestSampleFineLatLon_StreamIsNarrow), the same way chunk.go's own +-0.06
// was picked empirically rather than derived algebraically.
//
// fineRiverFlowMultiple is how much higher a cell's coarse RiverFlow must
// be, relative to Params.RiverFlowThreshold, before a fine sample reads as
// the wider StreamKindRiver rather than StreamKindStream: an ordinary
// small stream barely clears RiverFlowThreshold at all (chunk.go's own
// eligibility check), so a multiple of that SAME threshold singles out the
// genuinely high-flow cells worth widening without needing a second
// authored parameter.
const (
	fineStreamBand        = 0.010
	fineRiverBand         = 0.04
	fineRiverFlowMultiple = 4
)

// fineNoiseCache lazily builds one fine-detail noise field per SEED (not
// per *World): the field is a pure function of (seed, "fine:detail" tag,
// the fixed frequency/octave constants above), so two different *World
// values built from the same seed correctly share one cache entry instead
// of paying the small fastnoise.State setup cost twice. Keyed by the seed
// value itself, not a *World pointer, so a World that goes out of scope is
// free to be garbage-collected — nothing here keeps it alive.
var fineNoiseCache sync.Map // uint64 (seed) -> *noiseField

// ensureFineNoise returns w's fine-detail noise field, building it on
// first use. Deliberately NOT part of Generate's eager construction (see
// this file's package doc): a caller that never exports a fine grid never
// pays this cost, and building it lazily here cannot perturb
// World.Fingerprint or Chunk.Fingerprint, both of which are computed
// entirely from fields this method never touches.
func (w *World) ensureFineNoise() *noiseField {
	if v, ok := fineNoiseCache.Load(w.Seed); ok {
		return v.(*noiseField)
	}
	nf := newNoiseField(w.Seed, "fine:detail", 2, fineDetailFrequency, 500, 0, 1)
	actual, _ := fineNoiseCache.LoadOrStore(w.Seed, nf)
	return actual.(*noiseField)
}

// SampleFineLatLon samples exactly one lat/lon point at LOT resolution: the
// same coarse-mesh interpolation GenerateChunk performs for a tile
// (coarseCandidates/sampleCoarseAmong), evaluated at the point's OWN
// lat/lon instead of a chunk-grid tile centre — which is what keeps this
// accurate right up to a chunk or margin boundary, unlike snapping the
// point to its nearest tile first — plus a lot-scale detail noise term and
// a much narrower stream/river isoline than GenerateChunk's own tile-scale
// one.
//
// PURE: a function of (w.Seed, w.Params, w.Content, latDeg, lonDeg) alone;
// the same point on the same World always returns the identical FineSample
// (fine_test.go's determinism test).
//
// VISUAL/QUERY, same caveat NearestCell/NearestCells already document
// (world.go): the lat/lon -> unit-sphere conversion goes through sin/cos.
func (w *World) SampleFineLatLon(latDeg, lonDeg float64) FineSample {
	latRad := latDeg * math.Pi / 180
	lonRad := lonDeg * math.Pi / 180
	x := math.Cos(latRad) * math.Cos(lonRad)
	y := math.Cos(latRad) * math.Sin(lonRad)
	z := math.Sin(latRad)

	candidates, _ := w.NearestCells(latDeg, lonDeg, chunkCandidateK)
	ranked := make([]candDist, len(candidates))
	cs := w.sampleCoarseAmong(candidates, ranked, x, y, z)

	elev := cs.elevation + w.ensureFineNoise().Sample3(x, y, z)*fineDetailAmplitudeM

	boxes, oceanIdx, lakeIdx := biomeBoxes(w.Content)

	isOcean := elev <= 0
	isLake := false
	var biome uint8
	switch {
	case isOcean:
		biome = uint8(oceanIdx)
	case cs.lakeWeight > 0.5:
		isLake = true
		biome = uint8(lakeIdx)
	default:
		biome = classifyLandBiome(Temp(quantize(cs.temperature)), Precip(quantize(cs.precipitation)), boxes)
	}

	kind := StreamKindNone
	if !isOcean && cs.riverFlow >= float64(w.Params.RiverFlowThreshold) {
		wiggle := w.chunkStreamNoise.Sample3(x, y, z)
		switch {
		case cs.riverFlow >= float64(w.Params.RiverFlowThreshold)*fineRiverFlowMultiple &&
			wiggle > -fineRiverBand && wiggle < fineRiverBand:
			kind = StreamKindRiver
		case wiggle > -fineStreamBand && wiggle < fineStreamBand:
			kind = StreamKindStream
		}
	}

	return FineSample{
		ElevationM: elev,
		Biome:      biome,
		IsOcean:    isOcean,
		IsLake:     isLake,
		StreamKind: kind,
	}
}
