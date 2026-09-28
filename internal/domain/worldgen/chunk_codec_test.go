package worldgen

import (
	"reflect"
	"testing"
)

func TestChunkCodec_RoundTrip(t *testing.T) {
	w := testWorldForChunks(t)
	addrs := []ChunkAddr{
		{FacePZ, w.Params.ChunkBaseLOD, 5, 5},
		{FaceNY, w.Params.ChunkBaseLOD - 2, 1, 1}, // coarse LOD, no deposits
	}
	for _, addr := range addrs {
		c, err := w.GenerateChunk(addr)
		if err != nil {
			t.Fatalf("GenerateChunk(%+v): %v", addr, err)
		}
		blob := EncodeChunk(c)
		got, err := DecodeChunk(blob)
		if err != nil {
			t.Fatalf("DecodeChunk(%+v): %v", addr, err)
		}
		if got.Addr != c.Addr {
			t.Errorf("Addr: got %+v, want %+v", got.Addr, c.Addr)
		}
		if got.Seed != c.Seed || got.GeneratorVersion != c.GeneratorVersion || got.TileEdge != c.TileEdge {
			t.Errorf("header mismatch: got seed=%d genver=%d edge=%d, want seed=%d genver=%d edge=%d",
				got.Seed, got.GeneratorVersion, got.TileEdge, c.Seed, c.GeneratorVersion, c.TileEdge)
		}
		if !reflect.DeepEqual(got.Tiles, c.Tiles) {
			t.Errorf("tiles differ after round trip")
		}
		if !reflect.DeepEqual(got.Deposits, c.Deposits) {
			t.Errorf("deposits differ after round trip: got %+v, want %+v", got.Deposits, c.Deposits)
		}
		if got.Fingerprint() != c.Fingerprint() {
			t.Errorf("fingerprint differs after round trip: got %#x, want %#x", got.Fingerprint(), c.Fingerprint())
		}
	}
}

func TestChunkCodec_RejectsGarbage(t *testing.T) {
	if _, err := DecodeChunk([]byte("not a chunk blob at all")); err == nil {
		t.Error("expected an error decoding garbage bytes")
	}
	if _, err := DecodeChunk(nil); err == nil {
		t.Error("expected an error decoding an empty blob")
	}
}

func TestChunkCodec_RejectsTruncated(t *testing.T) {
	w := testWorldForChunks(t)
	c, err := w.GenerateChunk(ChunkAddr{FacePZ, w.Params.ChunkBaseLOD, 5, 5})
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	blob := EncodeChunk(c)
	if _, err := DecodeChunk(blob[:len(blob)-10]); err == nil {
		t.Error("expected an error decoding a truncated blob")
	}
}

// TestChunkCodec_BytesPerChunk reports the encoded size of a typical base
// chunk — the number the project report's "bytes per chunk" figure comes
// from — so a change to the tile/deposit format shows up here rather than
// only being discovered from a production payload-size regression.
func TestChunkCodec_BytesPerChunk(t *testing.T) {
	w, err := Generate(7, DefaultParams(), sampleContent())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// A handful of chunks, since a chunk with more deposits encodes larger.
	var total int
	const n = 8
	for i := 0; i < n; i++ {
		addr := ChunkAddr{int8(i % numFaces), w.Params.ChunkBaseLOD, int32(i * 97), int32(i * 53)}
		c, err := w.GenerateChunk(addr)
		if err != nil {
			t.Fatalf("GenerateChunk: %v", err)
		}
		total += len(EncodeChunk(c))
	}
	t.Logf("average base chunk blob size: %d bytes (%d tiles/chunk)", total/n, w.Params.ChunkTileEdge*w.Params.ChunkTileEdge)
}
