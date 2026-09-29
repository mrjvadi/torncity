package clientapi

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// The generated planet as a client reads it (docs/adr/0028 sections 2, 6.4
// and 9.4): the world's own numbers so a client can work out chunk addresses,
// and the chunks themselves, encoded by worldgen.EncodeChunk. A chunk is a
// pure function of (world seed, generator version, address), so a blob is
// immutable and cacheable forever; nothing is ever stored, every replica
// generates what it is asked for and keeps a bounded number of them.

// Refusals of the world endpoints.
var (
	// ErrNoWorld means `admin world create` has not run yet.
	ErrNoWorld = errors.New("clientapi: the world has not been created yet")
	// ErrBadChunk means the address names no chunk of this world.
	ErrBadChunk = errors.New("clientapi: no such chunk")
)

// WorldSource is the active world: its registry row and the generated
// planet (application.WorldCache).
type WorldSource interface {
	Active(ctx context.Context) (application.World, *worldgen.World, error)
}

// WorldInfo is GET /api/v1/world.
type WorldInfo struct {
	ID string `json:"id"`
	// Seed is a decimal string: it is a 64-bit number, more than a script
	// number holds exactly.
	Seed             string `json:"seed"`
	GeneratorVersion int    `json:"generator_version"`
	ParamsHash       string `json:"params_hash"`
	// ChunkCodecVersion is the version byte of the chunk blob format.
	ChunkCodecVersion int           `json:"chunk_codec_version"`
	Chunk             ChunkGeometry `json:"chunk"`
	// TileM is a tile's side in metres at the finest LOD (each coarser LOD
	// doubles it); LotM is a settlement lot's side, LotsPerTile lots to a
	// tile's edge.
	TileM          float64 `json:"tile_m"`
	LotM           float64 `json:"lot_m"`
	LotsPerTile    int     `json:"lots_per_tile"`
	PlanetRadiusKm float64 `json:"planet_radius_km"`
	// Biomes are the biome table a tile's biome index points into.
	Biomes []WorldBiome `json:"biomes"`
	// ChunkPath is the URL template of a chunk, relative to the API root.
	ChunkPath string `json:"chunk_path"`
	CreatedAt string `json:"created_at"`
}

// ChunkGeometry is how the cube-sphere is cut into chunks.
type ChunkGeometry struct {
	// Faces is the number of cube faces (6). Face f, LOD l has 2^l chunks
	// along each edge, x and y in [0, 2^l).
	Faces      int `json:"faces"`
	TileEdge   int `json:"tile_edge"`
	MinLOD     int `json:"min_lod"`
	MaxLOD     int `json:"max_lod"`
	HeaderSize int `json:"header_bytes"`
	TileSize   int `json:"tile_bytes"`
}

// WorldBiome is one row of the biome table.
type WorldBiome struct {
	Index int    `json:"index"`
	Code  string `json:"code"`
	Water bool   `json:"water,omitempty"`
	Color string `json:"color,omitempty"`
}

// ChunkPath is the route of a chunk.
const ChunkPath = "/api/v1/world/chunks/{face}/{lod}/{x}/{y}"

// WorldService answers the world's metadata and its chunks.
type WorldService struct {
	Source WorldSource
	// CacheEntries bounds the encoded chunks kept in memory.
	CacheEntries int
	// RecheckEvery is how long the registry row is trusted before the
	// database is asked which world is active again.
	RecheckEvery time.Duration
	Now          func() time.Time

	mu       sync.Mutex
	checked  time.Time
	row      application.World
	planet   *worldgen.World
	rowErr   error
	items    map[chunkKey]*list.Element
	lru      *list.List
	inflight map[chunkKey]*chunkCall
}

type chunkKey struct {
	world string
	addr  worldgen.ChunkAddr
}

type chunkEntry struct {
	key   chunkKey
	chunk EncodedChunk
}

type chunkCall struct {
	done  chan struct{}
	chunk EncodedChunk
	err   error
}

// EncodedChunk is one chunk blob, raw and gzipped, with its entity tag.
type EncodedChunk struct {
	ETag string
	Raw  []byte
	Gzip []byte
}

// active is the active world, its row read at most every RecheckEvery.
func (s *WorldService) active(ctx context.Context) (application.World, *worldgen.World, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	s.mu.Lock()
	if !s.checked.IsZero() && now.Sub(s.checked) < s.RecheckEvery {
		row, w, err := s.row, s.planet, s.rowErr
		s.mu.Unlock()
		return row, w, err
	}
	s.mu.Unlock()

	// The database and the generator are asked outside the lock: a slow
	// first generation must not stall requests that could be answered.
	row, w, err := s.Source.Active(ctx)
	if errors.Is(err, application.ErrNoActiveWorld) {
		err = ErrNoWorld
	}
	if err != nil && !errors.Is(err, ErrNoWorld) {
		return application.World{}, nil, err
	}
	s.mu.Lock()
	if s.row.ID != row.ID {
		// A different world is active now: nothing cached belongs to it.
		s.items, s.lru = nil, nil
	}
	s.checked, s.row, s.planet, s.rowErr = now, row, w, err
	s.mu.Unlock()
	return row, w, err
}

// Info is the active world's metadata.
func (s *WorldService) Info(ctx context.Context) (WorldInfo, error) {
	row, w, err := s.active(ctx)
	if err != nil {
		return WorldInfo{}, err
	}
	p := w.Params
	info := WorldInfo{
		ID: row.ID, Seed: strconv.FormatUint(row.Seed, 10), GeneratorVersion: row.GeneratorVersion,
		ParamsHash: row.ParamsHash, ChunkCodecVersion: worldgen.ChunkCodecVersion,
		Chunk: ChunkGeometry{Faces: 6, TileEdge: p.ChunkTileEdge, MinLOD: 0, MaxLOD: int(p.ChunkBaseLOD),
			HeaderSize: worldgen.ChunkHeaderBytes, TileSize: worldgen.ChunkTileBytes},
		TileM: p.TileMeters(), LotM: settlement.LotMeters(w), LotsPerTile: settlement.LotsPerTile,
		PlanetRadiusKm: p.PlanetRadiusKm, Biomes: make([]WorldBiome, 0, len(w.Content.Biomes)),
		ChunkPath: ChunkPath, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
	for i, b := range w.Content.Biomes {
		info.Biomes = append(info.Biomes, WorldBiome{Index: i, Code: b.Code, Water: b.IsWater, Color: b.ColorHex})
	}
	return info, nil
}

// ParseChunkAddr reads a chunk address from its path segments. Anything that
// is not a plain non-negative decimal number is refused, so a segment can
// never be read two ways.
func ParseChunkAddr(face, lod, x, y string) (worldgen.ChunkAddr, error) {
	f, ok1 := smallInt(face, 5)
	l, ok2 := smallInt(lod, 127)
	cx, ok3 := smallInt(x, 1<<30)
	cy, ok4 := smallInt(y, 1<<30)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return worldgen.ChunkAddr{}, ErrBadChunk
	}
	return worldgen.ChunkAddr{Face: int8(f), LOD: int8(l), X: int32(cx), Y: int32(cy)}, nil
}

func smallInt(s string, max int) (int, bool) {
	if s == "" || len(s) > 10 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > max {
			return 0, false
		}
	}
	return n, true
}

// ChunkETag is the strong entity tag of a chunk: everything its bytes
// depend on, so it can be answered before the chunk is generated.
func ChunkETag(row application.World, a worldgen.ChunkAddr) string {
	return fmt.Sprintf(`"%s.g%d.c%d.%d.%d.%d.%d"`, row.ID, row.GeneratorVersion, worldgen.ChunkCodecVersion,
		a.Face, a.LOD, a.X, a.Y)
}

// Addressed is a chunk address checked against the active world.
type Addressed struct {
	ETag string
	key  chunkKey
	w    *worldgen.World
}

// Address checks addr against the active world and gives its entity tag.
func (s *WorldService) Address(ctx context.Context, addr worldgen.ChunkAddr) (Addressed, error) {
	row, w, err := s.active(ctx)
	if err != nil {
		return Addressed{}, err
	}
	if !addr.Valid(w.Params.ChunkBaseLOD) {
		return Addressed{}, ErrBadChunk
	}
	return Addressed{ETag: ChunkETag(row, addr), key: chunkKey{world: row.ID, addr: addr}, w: w}, nil
}

// Chunk returns the encoded chunk, from this replica's cache or generated
// once: concurrent requests for one chunk share one generation.
func (s *WorldService) Chunk(ctx context.Context, a Addressed) (EncodedChunk, error) {
	s.mu.Lock()
	if el, ok := s.items[a.key]; ok {
		s.lru.MoveToFront(el)
		c := el.Value.(*chunkEntry).chunk
		s.mu.Unlock()
		return c, nil
	}
	if call, ok := s.inflight[a.key]; ok {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.chunk, call.err
		case <-ctx.Done():
			return EncodedChunk{}, ctx.Err()
		}
	}
	call := &chunkCall{done: make(chan struct{})}
	if s.inflight == nil {
		s.inflight = map[chunkKey]*chunkCall{}
	}
	s.inflight[a.key] = call
	s.mu.Unlock()

	call.chunk, call.err = encodeChunk(a)

	s.mu.Lock()
	delete(s.inflight, a.key)
	if call.err == nil && s.active0(a.key.world) {
		if s.items == nil {
			s.items, s.lru = map[chunkKey]*list.Element{}, list.New()
		}
		s.items[a.key] = s.lru.PushFront(&chunkEntry{key: a.key, chunk: call.chunk})
		for s.lru.Len() > max(1, s.CacheEntries) {
			old := s.lru.Back()
			s.lru.Remove(old)
			delete(s.items, old.Value.(*chunkEntry).key)
		}
	}
	s.mu.Unlock()
	close(call.done)
	return call.chunk, call.err
}

// active0 reports (with the lock held) that world is still the active one.
func (s *WorldService) active0(world string) bool { return s.row.ID == world }

// CachedChunks is how many chunks this replica holds.
func (s *WorldService) CachedChunks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lru == nil {
		return 0
	}
	return s.lru.Len()
}

func encodeChunk(a Addressed) (EncodedChunk, error) {
	c, err := a.w.GenerateChunk(a.key.addr)
	if err != nil {
		return EncodedChunk{}, ErrBadChunk
	}
	raw := worldgen.EncodeChunk(c)
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return EncodedChunk{}, err
	}
	if err := zw.Close(); err != nil {
		return EncodedChunk{}, err
	}
	return EncodedChunk{ETag: a.ETag, Raw: raw, Gzip: buf.Bytes()}, nil
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		params = strings.ReplaceAll(strings.ToLower(params), " ", "")
		return params != "q=0" && params != "q=0.0" && params != "q=0.00" && params != "q=0.000"
	}
	return false
}

// etagMatches reports whether an If-None-Match header names etag (or *).
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if part == "*" || part == etag {
			return true
		}
	}
	return false
}
