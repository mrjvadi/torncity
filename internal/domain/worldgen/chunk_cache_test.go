package worldgen

import "testing"

func TestChunkCache_HitsAndEviction(t *testing.T) {
	w := testWorldForChunks(t)
	lod := w.Params.ChunkBaseLOD
	cache := NewChunkCache(w, 2)

	a := ChunkAddr{FacePZ, lod, 0, 0}
	b := ChunkAddr{FacePZ, lod, 1, 0}
	cAddr := ChunkAddr{FacePZ, lod, 2, 0}

	if _, err := cache.Get(a); err != nil {
		t.Fatalf("Get(a): %v", err)
	}
	if _, err := cache.Get(b); err != nil {
		t.Fatalf("Get(b): %v", err)
	}
	if hits, misses := cache.Stats(); hits != 0 || misses != 2 {
		t.Fatalf("after 2 fresh gets: hits=%d misses=%d, want 0,2", hits, misses)
	}
	if _, err := cache.Get(a); err != nil {
		t.Fatalf("Get(a) again: %v", err)
	}
	if hits, _ := cache.Stats(); hits != 1 {
		t.Fatalf("expected a cache hit for a re-fetched chunk, got hits=%d", hits)
	}
	if cache.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", cache.Len())
	}

	// a is now most-recently-used (re-fetched above), b is least-recently-used.
	// Adding a third chunk with limit=2 must evict b, not a.
	if _, err := cache.Get(cAddr); err != nil {
		t.Fatalf("Get(c): %v", err)
	}
	if cache.Len() != 2 {
		t.Fatalf("Len() after eviction = %d, want 2", cache.Len())
	}
	if _, ok := cache.entries[b]; ok {
		t.Error("expected b to have been evicted (least recently used), but it is still cached")
	}
	if _, ok := cache.entries[a]; !ok {
		t.Error("expected a to still be cached (most recently used), but it was evicted")
	}
}

func TestChunkCache_ReturnsSameData(t *testing.T) {
	w := testWorldForChunks(t)
	addr := ChunkAddr{FaceNX, w.Params.ChunkBaseLOD, 4, 4}
	cache := NewChunkCache(w, 8)

	a, err := cache.Get(addr)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	direct, err := w.GenerateChunk(addr)
	if err != nil {
		t.Fatalf("GenerateChunk: %v", err)
	}
	if a.Fingerprint() != direct.Fingerprint() {
		t.Errorf("cached chunk fingerprint %#x != freshly generated %#x", a.Fingerprint(), direct.Fingerprint())
	}
}

func TestChunkCache_Prefetch(t *testing.T) {
	w := testWorldForChunks(t)
	addr := ChunkAddr{FacePY, w.Params.ChunkBaseLOD, 8, 8}
	cache := NewChunkCache(w, 16)

	ring := addr.Neighbors8()
	if err := cache.Prefetch(ring[:]); err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	for _, n := range ring {
		if _, ok := cache.entries[n]; !ok {
			t.Errorf("neighbor %+v not in cache after Prefetch", n)
		}
	}
	if hits, misses := cache.Stats(); misses != uint64(len(ring)) || hits != 0 {
		t.Errorf("after prefetch: hits=%d misses=%d, want 0,%d", hits, misses, len(ring))
	}
	// Fetching an already-prefetched chunk should now be a hit.
	if _, err := cache.Get(ring[0]); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if hits, _ := cache.Stats(); hits != 1 {
		t.Errorf("hits=%d, want 1", hits)
	}
}
