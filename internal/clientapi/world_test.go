package clientapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// A small planet from the shipped world content, generated once for every
// test of this package.
var (
	planetOnce sync.Once
	planet     *worldgen.World
	planetErr  error
)

func testPlanet(t *testing.T) *worldgen.World {
	t.Helper()
	planetOnce.Do(func() {
		pack, err := content.LoadWorldGen("../../configs/content")
		if err != nil {
			planetErr = err
			return
		}
		c, err := pack.ToContent()
		if err != nil {
			planetErr = err
			return
		}
		params := worldgen.DefaultParams()
		params.CellCount = 6000
		planet, planetErr = worldgen.Generate(20280928, params, c)
	})
	if planetErr != nil {
		t.Fatalf("building the test planet: %v", planetErr)
	}
	return planet
}

type fakeWorldSource struct {
	mu    sync.Mutex
	row   application.World
	w     *worldgen.World
	err   error
	calls int
}

func (f *fakeWorldSource) Active(context.Context) (application.World, *worldgen.World, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.row, f.w, f.err
}

func testWorldRow() application.World {
	return application.World{ID: "0a0a0a0a-0000-4000-8000-000000000001", Seed: 20280928, GeneratorVersion: worldgen.GeneratorVersion,
		ParamsHash: "abc123", Active: true, CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
}

func newWorldService(t *testing.T, entries int) (*WorldService, *fakeWorldSource) {
	t.Helper()
	src := &fakeWorldSource{row: testWorldRow(), w: testPlanet(t)}
	return &WorldService{Source: src, CacheEntries: entries, RecheckEvery: time.Minute}, src
}

func TestParseChunkAddr(t *testing.T) {
	good := []struct {
		f, l, x, y string
		want       worldgen.ChunkAddr
	}{
		{"0", "0", "0", "0", worldgen.ChunkAddr{}},
		{"5", "10", "1023", "7", worldgen.ChunkAddr{Face: 5, LOD: 10, X: 1023, Y: 7}},
	}
	for _, g := range good {
		got, err := ParseChunkAddr(g.f, g.l, g.x, g.y)
		if err != nil || got != g.want {
			t.Errorf("ParseChunkAddr(%q,%q,%q,%q) = %+v, %v", g.f, g.l, g.x, g.y, got, err)
		}
	}
	for _, bad := range [][4]string{
		{"6", "0", "0", "0"}, {"-1", "0", "0", "0"}, {"0", "200", "0", "0"}, {"0", "0", "1e3", "0"}, {"0", "0", "", "0"},
		{"0", "0", "007", "0"}, {"0", "0", "99999999999", "0"}, {"a", "0", "0", "0"}, {"0", "0", "0", "+1"},
	} {
		if _, err := ParseChunkAddr(bad[0], bad[1], bad[2], bad[3]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestWorldInfoDescribesTheGeometry(t *testing.T) {
	svc, _ := newWorldService(t, 8)
	info, err := svc.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := testPlanet(t).Params
	if info.ID != testWorldRow().ID || info.Seed != "20280928" || info.GeneratorVersion != worldgen.GeneratorVersion {
		t.Errorf("identity: %+v", info)
	}
	if info.Chunk.Faces != 6 || info.Chunk.TileEdge != p.ChunkTileEdge || info.Chunk.MaxLOD != int(p.ChunkBaseLOD) || info.Chunk.MinLOD != 0 {
		t.Errorf("chunk geometry: %+v", info.Chunk)
	}
	if info.TileM < 250 || info.TileM > 350 || info.LotM*float64(info.LotsPerTile) != info.TileM {
		t.Errorf("tile %v m, lot %v m x %d", info.TileM, info.LotM, info.LotsPerTile)
	}
	if len(info.Biomes) == 0 || info.Biomes[0].Index != 0 || info.Biomes[0].Code == "" {
		t.Errorf("biomes: %+v", info.Biomes)
	}
	if info.ChunkPath != ChunkPath || info.ChunkCodecVersion != worldgen.ChunkCodecVersion {
		t.Errorf("path/codec: %+v", info)
	}
}

func TestNoWorldIsReported(t *testing.T) {
	svc := &WorldService{Source: &fakeWorldSource{err: application.ErrNoActiveWorld}, CacheEntries: 4, RecheckEvery: time.Minute}
	if _, err := svc.Info(context.Background()); err != ErrNoWorld {
		t.Fatalf("Info: %v", err)
	}
	if _, err := svc.Address(context.Background(), worldgen.ChunkAddr{}); err != ErrNoWorld {
		t.Fatalf("Address: %v", err)
	}
}

// The blob is the codec's, decodes to the chunk that was asked for, and the
// same address always gives the same bytes and tag.
func TestChunkIsTheEncodedChunk(t *testing.T) {
	svc, _ := newWorldService(t, 8)
	ctx := context.Background()
	addr := worldgen.ChunkAddr{Face: 2, LOD: 10, X: 300, Y: 411}
	a, err := svc.Address(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.Chunk(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := worldgen.DecodeChunk(c.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Addr != addr || dec.Seed != 20280928 || dec.TileEdge != testPlanet(t).Params.ChunkTileEdge {
		t.Errorf("decoded %+v", dec.Addr)
	}
	want, _ := testPlanet(t).GenerateChunk(addr)
	if !bytes.Equal(c.Raw, worldgen.EncodeChunk(want)) {
		t.Error("blob differs from EncodeChunk(GenerateChunk)")
	}
	if len(c.Raw) != worldgen.ChunkHeaderBytes+len(dec.Tiles)*worldgen.ChunkTileBytes+2+depositBytes(dec) {
		t.Errorf("blob is %d bytes, layout says otherwise", len(c.Raw))
	}
	zr, err := gzip.NewReader(bytes.NewReader(c.Gzip))
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := io.ReadAll(zr); !bytes.Equal(raw, c.Raw) {
		t.Error("gzip does not round-trip")
	}
	if len(c.Gzip) >= len(c.Raw) {
		t.Logf("gzip %d >= raw %d", len(c.Gzip), len(c.Raw))
	}
	if c.ETag != a.ETag || a.ETag != ChunkETag(testWorldRow(), addr) {
		t.Errorf("etag %q %q", c.ETag, a.ETag)
	}
}

func depositBytes(c *worldgen.Chunk) int {
	n := 0
	for _, d := range c.Deposits {
		n += 1 + len(d.DepositID) + 1 + len(d.ResourceCode) + 2
	}
	return n
}

func TestChunkAddressesAreValidated(t *testing.T) {
	svc, _ := newWorldService(t, 8)
	base := testPlanet(t).Params.ChunkBaseLOD
	for _, bad := range []worldgen.ChunkAddr{
		{Face: 6}, {Face: 0, LOD: base + 1}, {Face: 0, LOD: 0, X: 1}, {Face: 0, LOD: 3, X: 8}, {Face: 0, LOD: 3, Y: 8}, {Face: 0, LOD: 3, X: -1},
	} {
		if _, err := svc.Address(context.Background(), bad); err != ErrBadChunk {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	for _, ok := range []worldgen.ChunkAddr{{}, {Face: 5, LOD: 3, X: 7, Y: 7}, {Face: 1, LOD: base, X: (1 << base) - 1, Y: 0}} {
		if _, err := svc.Address(context.Background(), ok); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
}

// The cache is bounded, and concurrent requests for one chunk generate it
// once. Run under -race this also shows chunk generation is safe to share.
func TestChunkCacheIsBoundedAndShared(t *testing.T) {
	svc, _ := newWorldService(t, 3)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, err := svc.Address(ctx, worldgen.ChunkAddr{Face: 0, LOD: 4, X: int32(i % 5), Y: 1})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := svc.Chunk(ctx, a); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if n := svc.CachedChunks(); n != 3 {
		t.Errorf("cache holds %d chunks, limit is 3", n)
	}
}

func TestWorldRowIsNotReadOnEveryRequest(t *testing.T) {
	svc, src := newWorldService(t, 3)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if _, err := svc.Info(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if src.calls != 1 {
		t.Fatalf("%d reads of the registry", src.calls)
	}
	now = now.Add(2 * time.Minute)
	_, _ = svc.Info(context.Background())
	if src.calls != 2 {
		t.Fatalf("%d reads after the recheck interval", src.calls)
	}
}

func TestAcceptsGzipAndEtagMatches(t *testing.T) {
	for h, want := range map[string]bool{"": false, "gzip": true, "br, gzip;q=0.8": true, "gzip;q=0": false, "identity": false, "GZIP": true} {
		if acceptsGzip(h) != want {
			t.Errorf("acceptsGzip(%q) != %v", h, want)
		}
	}
	if !etagMatches(`"a", "b"`, `"b"`) || !etagMatches(`W/"a"`, `"a"`) || !etagMatches(`*`, `"z"`) || etagMatches(`"a"`, `"b"`) {
		t.Error("etagMatches")
	}
}

// --- over HTTP ---------------------------------------------------------------

func newWorldAPI(t *testing.T, svc *WorldService) *apiFixture {
	t.Helper()
	return newAPIFixtureWith(t, func(c *ServerConfig) { c.WorldSvc = svc })
}

func signedIn(t *testing.T, f *apiFixture) string {
	t.Helper()
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	status, session := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	if status != http.StatusOK {
		t.Fatalf("link: %d %v", status, session)
	}
	return session["access_token"].(string)
}

func (f *apiFixture) get(t *testing.T, token, path string, header map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", f.srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	// The default transport would add and undo gzip on its own.
	tr := &http.Transport{DisableCompression: true}
	res, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestChunkEndpoint(t *testing.T) {
	svc, _ := newWorldService(t, 16)
	f := newWorldAPI(t, svc)
	token := signedIn(t, f)
	path := "/api/v1/world/chunks/1/10/200/300"

	if res := f.get(t, "", path, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", res.StatusCode)
	}

	res := f.get(t, token, path, nil)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != chunkMediaType || res.Header.Get("Cache-Control") != chunkCacheControl {
		t.Fatalf("plain: %d %v", res.StatusCode, res.Header)
	}
	etag := res.Header.Get("ETag")
	if etag == "" || etag[0] != '"' {
		t.Fatalf("etag %q", etag)
	}
	if _, err := worldgen.DecodeChunk(raw); err != nil {
		t.Fatalf("body is not a chunk: %v", err)
	}

	// Conditional request: no body, still cacheable.
	res = f.get(t, token, path, map[string]string{"If-None-Match": etag})
	res.Body.Close()
	if res.StatusCode != http.StatusNotModified || res.Header.Get("Cache-Control") != chunkCacheControl {
		t.Fatalf("conditional: %d %v", res.StatusCode, res.Header)
	}

	// Compressed: a different representation, a different tag.
	res = f.get(t, token, path, map[string]string{"Accept-Encoding": "gzip"})
	zbody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("ETag") == etag || res.Header.Get("Vary") == "" {
		t.Fatalf("gzip: %v", res.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(zbody))
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := io.ReadAll(zr); !bytes.Equal(again, raw) {
		t.Error("gzip body differs from the plain one")
	}

	for _, bad := range []string{"/api/v1/world/chunks/6/0/0/0", "/api/v1/world/chunks/0/11/0/0", "/api/v1/world/chunks/0/2/4/0", "/api/v1/world/chunks/x/1/0/0"} {
		res := f.get(t, token, bad, nil)
		var body struct {
			Error APIError `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || body.Error.Code != "bad_chunk" {
			t.Errorf("%s: %d %+v", bad, res.StatusCode, body)
		}
	}
	if svc.CachedChunks() != 1 {
		t.Errorf("%d chunks cached", svc.CachedChunks())
	}
}

func TestWorldEndpointAndNoWorld(t *testing.T) {
	svc, _ := newWorldService(t, 16)
	f := newWorldAPI(t, svc)
	token := signedIn(t, f)
	res := f.get(t, token, "/api/v1/world", nil)
	var info WorldInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil || res.StatusCode != 200 || info.ID != testWorldRow().ID {
		t.Fatalf("world: %d %v %+v", res.StatusCode, err, info)
	}
	res.Body.Close()
	res = f.get(t, token, "/api/v1/world", map[string]string{"If-None-Match": res.Header.Get("ETag")})
	res.Body.Close()
	if res.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional world: %d", res.StatusCode)
	}

	f = newWorldAPI(t, &WorldService{Source: &fakeWorldSource{err: application.ErrNoActiveWorld}, CacheEntries: 1, RecheckEvery: time.Minute})
	token = signedIn(t, f)
	for _, path := range []string{"/api/v1/world", "/api/v1/world/chunks/0/0/0/0"} {
		res := f.get(t, token, path, nil)
		var body struct {
			Error APIError `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound || body.Error.Code != "world_not_created" {
			t.Errorf("%s: %d %+v", path, res.StatusCode, body)
		}
	}
}

var _ = httptest.NewRecorder
