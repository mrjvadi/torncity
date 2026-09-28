package worldgen

import "container/list"

// ChunkCache is an in-process, per-replica LRU cache of generated chunks.
//
// SAFE TO DUPLICATE ACROSS REPLICAS, ON PURPOSE. Everything a ChunkCache
// holds is immutable, seed-derived data (chunk.go's own doc: a Chunk
// depends only on (seed, GeneratorVersion, Params/Content, addr)) — never a
// player-owned or mutable value. The owner runs many replicas of every
// service (see the project report's scale-out note); a chunk cache that
// needed to be the single shared truth would be exactly the kind of hot,
// coordinated state that doesn't scale out. Instead every replica keeps its
// own independent cache, filled lazily from whichever chunks its own
// traffic actually asked for, and two replicas computing the same chunk
// twice is a correctness non-event: Get always reproduces the identical
// bytes GenerateChunk would, so a cache miss is only ever a latency cost,
// never a consistency one.
//
// NOT SAFE FOR CONCURRENT USE. Same convention as nearestIndex
// (geometry.go): a caller needing concurrent access wraps ChunkCache with
// its own lock, or keeps one ChunkCache per goroutine/worker rather than
// sharing one across an HTTP server's request handlers directly.
type ChunkCache struct {
	world   *World
	limit   int
	ll      *list.List // MRU at Front, LRU at Back
	entries map[ChunkAddr]*list.Element
	hits    uint64
	misses  uint64
}

type chunkCacheEntry struct {
	addr  ChunkAddr
	chunk *Chunk
}

// NewChunkCache builds a cache of at most limit chunks, generating from w on
// a miss.
func NewChunkCache(w *World, limit int) *ChunkCache {
	if limit < 1 {
		limit = 1
	}
	return &ChunkCache{
		world:   w,
		limit:   limit,
		ll:      list.New(),
		entries: make(map[ChunkAddr]*list.Element, limit),
	}
}

// Get returns the chunk at addr, generating and caching it on a miss, and
// marking it most-recently-used either way.
func (c *ChunkCache) Get(addr ChunkAddr) (*Chunk, error) {
	if el, ok := c.entries[addr]; ok {
		c.hits++
		c.ll.MoveToFront(el)
		return el.Value.(*chunkCacheEntry).chunk, nil
	}
	c.misses++
	chunk, err := c.world.GenerateChunk(addr)
	if err != nil {
		return nil, err
	}
	el := c.ll.PushFront(&chunkCacheEntry{addr: addr, chunk: chunk})
	c.entries[addr] = el
	if c.ll.Len() > c.limit {
		c.evictOldest()
	}
	return chunk, nil
}

// Prefetch warms the cache for addrs without returning them — used to fill
// the "neighbouring chunks ready" ring the project report's client
// streaming policy describes, server-side (e.g. before a batch chunk
// response) or by a client-facing service pre-warming its own cache ahead
// of a player's predicted movement.
func (c *ChunkCache) Prefetch(addrs []ChunkAddr) error {
	for _, a := range addrs {
		if _, err := c.Get(a); err != nil {
			return err
		}
	}
	return nil
}

func (c *ChunkCache) evictOldest() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	c.ll.Remove(el)
	delete(c.entries, el.Value.(*chunkCacheEntry).addr)
}

// Len returns how many chunks are currently cached.
func (c *ChunkCache) Len() int { return c.ll.Len() }

// Stats returns the cache's cumulative hit/miss counts, for the operator
// panel or a log line — never for a gameplay decision.
func (c *ChunkCache) Stats() (hits, misses uint64) { return c.hits, c.misses }
