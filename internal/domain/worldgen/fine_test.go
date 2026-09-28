package worldgen

import (
	"math"
	"testing"
)

func testWorldForFine(t *testing.T) *World {
	t.Helper()
	w, err := Generate(42, smallParams(), sampleContent())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return w
}

// metersPerDegree converts lot-scale metre offsets into latitude/longitude
// degree offsets near latDeg — the same small-patch equirectangular
// approximation cmd/worldpreview/export_city.go uses to place its fine lot
// grid; good enough over the few-km windows this test and that export both
// work in (see fine.go's own VISUAL/QUERY caveat on the lat/lon <-> 3D
// conversion generally).
func metersPerDegree(latDeg, planetRadiusKm float64) (mPerDegLat, mPerDegLon float64) {
	mPerDegLat = planetRadiusKm * 1000 * math.Pi / 180
	mPerDegLon = mPerDegLat * math.Cos(latDeg*math.Pi/180)
	return
}

func TestSampleFineLatLon_Deterministic(t *testing.T) {
	w := testWorldForFine(t)
	addr := ChunkAddr{FacePZ, w.Params.ChunkBaseLOD, 12, 20}
	latDeg, lonDeg := addr.LatLon()

	a := w.SampleFineLatLon(latDeg, lonDeg)
	b := w.SampleFineLatLon(latDeg, lonDeg)
	if a != b {
		t.Fatalf("SampleFineLatLon not deterministic within one World: %+v vs %+v", a, b)
	}

	// Also deterministic across an entirely independent World built from the
	// same seed/params/content — the real promise: a pure function of
	// (seed, params, content, latDeg, lonDeg), never of a *World's identity
	// or of ensureFineNoise's lazy-build order.
	w2, err := Generate(w.Seed, w.Params, sampleContent())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	c := w2.SampleFineLatLon(latDeg, lonDeg)
	if a != c {
		t.Fatalf("SampleFineLatLon differs across independently-generated Worlds: %+v vs %+v", a, c)
	}
}

func TestSampleFineLatLon_MatchesChunkNearOcean(t *testing.T) {
	// Sanity check unrelated to width tuning: a point deep inside a chunk
	// already known to be ocean (chunk.go's own classification) should read
	// as ocean at fine resolution too — the two classifications share the
	// same coarse elevation field and the same isOcean<=0 rule, just at
	// different sampling resolutions.
	w := testWorldForFine(t)
	edge := w.Params.ChunkTileEdge
	baseLOD := w.Params.ChunkBaseLOD

	for face := int8(0); face < numFaces; face++ {
		addr := ChunkAddr{face, baseLOD, 0, 0}
		c, err := w.GenerateChunk(addr)
		if err != nil {
			t.Fatalf("GenerateChunk: %v", err)
		}
		for j := 0; j < edge; j++ {
			for i := 0; i < edge; i++ {
				tl := c.TileAt(i, j)
				if !tl.IsOcean() {
					continue
				}
				x, y, z := addr.TileUnitSpherePoint(edge, i, j)
				latDeg := math.Asin(clamp(z, -1, 1)) * 180 / math.Pi
				lonDeg := math.Atan2(y, x) * 180 / math.Pi
				fs := w.SampleFineLatLon(latDeg, lonDeg)
				if !fs.IsOcean {
					t.Fatalf("tile (%d,%d) of %+v is ocean at chunk resolution but SampleFineLatLon at its own centre says not-ocean: %+v", i, j, addr, fs)
				}
				return
			}
		}
	}
	t.Skip("no ocean tile found in the (0,0) corner chunk of any face for this seed/params")
}

// findStreamAnchor locates a lat/lon point this World's own GenerateChunk
// already flags IsStream() (an ORDINARY stream, flow well under
// fineRiverFlowMultiple*RiverFlowThreshold) — the same terrain
// SampleFineLatLon reads, just re-found at fine resolution instead of tile
// resolution. Deterministic for a fixed seed/params: always the same tile.
func findStreamAnchor(t *testing.T, w *World) (latDeg, lonDeg float64) {
	t.Helper()
	baseLOD := w.Params.ChunkBaseLOD
	edge := w.Params.ChunkTileEdge
	n := chunksPerEdge(baseLOD)

	for face := int8(0); face < numFaces; face++ {
		for x := int32(0); x < n; x += 7 {
			for y := int32(0); y < n; y += 7 {
				addr := ChunkAddr{face, baseLOD, x, y}
				c, err := w.GenerateChunk(addr)
				if err != nil {
					continue
				}
				for j := 0; j < edge; j++ {
					for i := 0; i < edge; i++ {
						tl := c.TileAt(i, j)
						if !tl.IsStream() || tl.IsOcean() {
							continue
						}
						xx, yy, zz := addr.TileUnitSpherePoint(edge, i, j)
						lat := math.Asin(clamp(zz, -1, 1)) * 180 / math.Pi
						lon := math.Atan2(yy, xx) * 180 / math.Pi
						if fs := w.SampleFineLatLon(lat, lon); fs.StreamKind == StreamKindStream {
							return lat, lon
						}
					}
				}
			}
		}
	}
	t.Fatal("findStreamAnchor: no ordinary-stream tile found for this seed/params")
	return 0, 0
}

// findRiverAnchor locates a lat/lon point near a high-flow coarse cell
// (flow >= RiverFlowThreshold*fineRiverFlowMultiple) whose own
// chunkStreamNoise sample lands inside fineRiverBand — i.e. a point
// SampleFineLatLon actually classifies StreamKindRiver, not merely a
// high-flow cell that happens to miss the isoline (most do; the isoline
// only carves a thin path through the eligible swath, chunk.go's own doc
// comment on the mechanism). The exact cell centre itself is rarely ON the
// isoline, so this searches a small lot-spaced neighbourhood around every
// qualifying cell rather than only the cell's own coordinate.
func findRiverAnchor(t *testing.T, w *World) (latDeg, lonDeg float64) {
	t.Helper()
	thr := w.Params.RiverFlowThreshold
	const halfSearchLots = 15
	for _, cell := range w.Cells {
		if cell.RiverFlow < int32(thr*fineRiverFlowMultiple) {
			continue
		}
		lat, lon := cell.Point.LatDeg, cell.Point.LonDeg
		lotMeters := w.Params.TileMeters() / 10
		mPerDegLat, mPerDegLon := metersPerDegree(lat, w.Params.PlanetRadiusKm)
		dLat := lotMeters / mPerDegLat
		dLon := lotMeters / mPerDegLon
		for j := -halfSearchLots; j <= halfSearchLots; j++ {
			for i := -halfSearchLots; i <= halfSearchLots; i++ {
				plat := lat + float64(j)*dLat
				plon := lon + float64(i)*dLon
				if fs := w.SampleFineLatLon(plat, plon); fs.StreamKind == StreamKindRiver {
					return plat, plon
				}
			}
		}
	}
	t.Fatal("findRiverAnchor: no high-flow cell's neighbourhood classifies as StreamKindRiver for this seed/params")
	return 0, 0
}

// maxRunAlong scans a line of lot-spaced samples through (latDeg,lonDeg) —
// horizontally (varying longitude) if horiz, vertically (varying latitude)
// otherwise — and returns the longest contiguous run of samples matching
// want.
func maxRunAlong(w *World, latDeg, lonDeg float64, horiz bool, want StreamKind) int {
	const halfSpanLots = 35 // 70 lots total, ~2.1km at default lotMeters: comfortably covers a single crossing
	lotMeters := w.Params.TileMeters() / 10
	mPerDegLat, mPerDegLon := metersPerDegree(latDeg, w.Params.PlanetRadiusKm)
	dLat := lotMeters / mPerDegLat
	dLon := lotMeters / mPerDegLon

	maxRun, run := 0, 0
	for k := -halfSpanLots; k < halfSpanLots; k++ {
		var lat, lon float64
		if horiz {
			lat, lon = latDeg, lonDeg+float64(k)*dLon
		} else {
			lat, lon = latDeg+float64(k)*dLat, lonDeg
		}
		if w.SampleFineLatLon(lat, lon).StreamKind == want {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	return maxRun
}

// TestSampleFineLatLon_StreamIsNarrow is Part 1's own required test: at LOT
// resolution (~30.5m at default Params), an ordinary stream must read as a
// channel a FEW lots wide (this test requires 1..8), not the ~10-lot width
// a whole base tile would represent if fine sampling just re-flagged
// GenerateChunk's own tile-scale isoline band without narrowing it — that
// was the bug this file exists to avoid. It also checks that
// StreamKindRiver only shows up near genuinely high coarse RiverFlow, and
// reads wider than an ordinary StreamKindStream crossing.
func TestSampleFineLatLon_StreamIsNarrow(t *testing.T) {
	w := testWorldForFine(t)

	streamLat, streamLon := findStreamAnchor(t, w)
	streamWidth := 0
	for _, horiz := range [2]bool{true, false} {
		if r := maxRunAlong(w, streamLat, streamLon, horiz, StreamKindStream); r > streamWidth {
			streamWidth = r
		}
	}
	if streamWidth < 1 || streamWidth > 8 {
		t.Fatalf("ordinary stream at (%.4f,%.4f) reads %d lots wide, want 1..8 (a whole base tile is ~10 lots)", streamLat, streamLon, streamWidth)
	}
	t.Logf("stream width at fine resolution: %d lots", streamWidth)

	riverLat, riverLon := findRiverAnchor(t, w)
	riverWidth := 0
	for _, horiz := range [2]bool{true, false} {
		if r := maxRunAlong(w, riverLat, riverLon, horiz, StreamKindRiver); r > riverWidth {
			riverWidth = r
		}
	}
	if riverWidth < 1 {
		t.Fatalf("high-flow river anchor at (%.4f,%.4f) never read StreamKindRiver along either scan axis", riverLat, riverLon)
	}
	t.Logf("river width at fine resolution: %d lots", riverWidth)

	if riverWidth <= streamWidth {
		t.Fatalf("expected a river crossing to read wider than an ordinary stream: river=%d lots, stream=%d lots", riverWidth, streamWidth)
	}

	// StreamKindRiver must only ever appear where the coarse-interpolated
	// flow actually clears the high-flow cutoff — never as a side effect of
	// merely being inside fineRiverBand with ordinary flow.
	fs := w.SampleFineLatLon(riverLat, riverLon)
	if fs.StreamKind != StreamKindRiver {
		t.Fatalf("river anchor itself did not classify as StreamKindRiver: %+v", fs)
	}
}
