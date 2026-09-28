package worldgen

import (
	"testing"
	"time"
)

func testWorldForChunks(t *testing.T) *World {
	t.Helper()
	w, err := Generate(42, smallParams(), sampleContent())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return w
}

func TestGenerateChunk_Basic(t *testing.T) {
	w := testWorldForChunks(t)
	addr := ChunkAddr{FacePZ, w.Params.ChunkBaseLOD, 3, 5}
	c, err := w.GenerateChunk(addr)
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	edge := w.Params.ChunkTileEdge
	if len(c.Tiles) != edge*edge {
		t.Fatalf("got %d tiles, want %d", len(c.Tiles), edge*edge)
	}
	if c.Addr != addr {
		t.Errorf("Addr = %+v, want %+v", c.Addr, addr)
	}
}

func TestGenerateChunk_Deterministic(t *testing.T) {
	w := testWorldForChunks(t)
	addr := ChunkAddr{FaceNY, w.Params.ChunkBaseLOD, 10, 4}
	a, err := w.GenerateChunk(addr)
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	b, err := w.GenerateChunk(addr)
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	if len(a.Tiles) != len(b.Tiles) {
		t.Fatalf("tile count differs: %d vs %d", len(a.Tiles), len(b.Tiles))
	}
	for i := range a.Tiles {
		if a.Tiles[i] != b.Tiles[i] {
			t.Fatalf("tile %d differs: %+v vs %+v", i, a.Tiles[i], b.Tiles[i])
		}
	}

	// Also determinstic across an entirely independent World built from
	// the same seed/params/content — the real promise (chunk.go's doc):
	// depends only on (seed, version, params/content, addr).
	w2, err := Generate(w.Seed, w.Params, sampleContent())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	c2, err := w2.GenerateChunk(addr)
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	for i := range a.Tiles {
		if a.Tiles[i] != c2.Tiles[i] {
			t.Fatalf("tile %d differs across independently-generated Worlds: %+v vs %+v", i, a.Tiles[i], c2.Tiles[i])
		}
	}
}

func TestGenerateChunk_InvalidAddr(t *testing.T) {
	w := testWorldForChunks(t)
	bad := ChunkAddr{FacePX, w.Params.ChunkBaseLOD + 1, 0, 0}
	if _, err := w.GenerateChunk(bad); err == nil {
		t.Fatal("expected an error for an LOD beyond ChunkBaseLOD")
	}
}

func TestGenerateChunk_ElevationInInt16Range(t *testing.T) {
	w := testWorldForChunks(t)
	n := chunksPerEdge(w.Params.ChunkBaseLOD)
	for _, xy := range [][2]int32{{0, 0}, {n / 2, n / 2}, {n - 1, n - 1}} {
		addr := ChunkAddr{FacePY, w.Params.ChunkBaseLOD, xy[0], xy[1]}
		c, err := w.GenerateChunk(addr)
		if err != nil {
			t.Fatalf("GenerateChunk: %v", err)
		}
		for _, tile := range c.Tiles {
			if tile.Elevation <= -32000 || tile.Elevation >= 32000 {
				t.Errorf("tile elevation %d suspiciously close to int16 clamp range", tile.Elevation)
			}
		}
	}
}

func TestGenerateChunk_CoarserLODIsCheaperAndHasNoDeposits(t *testing.T) {
	w := testWorldForChunks(t)
	coarse := ChunkAddr{FacePX, w.Params.ChunkBaseLOD - 2, 0, 0}
	c, err := w.GenerateChunk(coarse)
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	if len(c.Deposits) != 0 {
		t.Errorf("coarse-LOD chunk has %d deposits, want 0 (deposits are base-LOD only)", len(c.Deposits))
	}
}

func TestGenerateChunk_Timing(t *testing.T) {
	if testing.Short() {
		t.Skip("timing benchmark, skipped in -short")
	}
	w, err := Generate(7, DefaultParams(), sampleContent())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	n := chunksPerEdge(w.Params.ChunkBaseLOD)
	const trials = 20
	start := time.Now()
	for i := 0; i < trials; i++ {
		addr := ChunkAddr{int8(i % numFaces), w.Params.ChunkBaseLOD, int32(i*37) % n, int32(i*91) % n}
		if _, err := w.GenerateChunk(addr); err != nil {
			t.Fatalf("GenerateChunk: %v", err)
		}
	}
	elapsed := time.Since(start)
	perChunk := elapsed / trials
	t.Logf("base chunk generation: %s average over %d chunks (%d tiles each)", perChunk, trials, w.Params.ChunkTileEdge*w.Params.ChunkTileEdge)
	if perChunk > 20*time.Millisecond {
		t.Errorf("base chunk generation averaged %s, want well under 20ms", perChunk)
	}
}
