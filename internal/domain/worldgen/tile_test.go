package worldgen

import (
	"hash/fnv"
	"testing"
)

// gridSource is a synthetic elevation field over tile coordinates.
type gridSource struct {
	elev func(gx, gy int32) int16
	flag func(gx, gy int32) uint8
}

func (g gridSource) TileAt(t TileID) (ChunkTile, error) {
	_, x, y := t.Parts()
	ct := ChunkTile{Elevation: g.elev(x, y)}
	if g.flag != nil {
		ct.Flags = g.flag(x, y)
	}
	return ct, nil
}

func TestTileID_RoundTrip(t *testing.T) {
	for _, c := range []struct {
		f    int8
		x, y int32
	}{{0, 0, 0}, {5, 32767, 32767}, {3, 1, 30000}, {2, 12345, 6789}} {
		f, x, y := MakeTileID(c.f, c.x, c.y).Parts()
		if f != c.f || x != c.x || y != c.y {
			t.Fatalf("round trip %v -> %d %d %d", c, f, x, y)
		}
	}
}

func TestTile_LatLonRoundTripAndSize(t *testing.T) {
	w := testWorldForChunks(t)
	if got := w.TileGrid(); got != (int32(1)<<uint(w.Params.ChunkBaseLOD))*int32(w.Params.ChunkTileEdge) {
		t.Fatalf("grid %d", got)
	}
	// 6371 km radius, ChunkBaseLOD 10 and 32 tiles: about 305 m (ADR 0042 6.2).
	if w.Params.ChunkBaseLOD == 10 && w.Params.ChunkTileEdge == 32 && w.Params.PlanetRadiusKm == 6371 {
		if l, d := w.TileLengthM(), w.TileDiagonalM(); l != 305 || d != 431 {
			t.Fatalf("tile length %d diagonal %d, want 305 and 431", l, d)
		}
	}
	for _, p := range [][2]float64{{0, 0}, {35.7, 51.4}, {-60, 120}, {89, 10}, {-45, -170}} {
		id := w.TileOfLatLon(p[0], p[1])
		la, lo := w.TileCentre(id)
		if back := w.TileOfLatLon(la, lo); back != id {
			t.Fatalf("%v: centre of %v maps to %v", p, id, back)
		}
		addr, i, j := w.TileChunk(id)
		if want := ChunkOfLatLon(w.Params.ChunkBaseLOD, p[0], p[1]); want != addr {
			t.Fatalf("%v: chunk %v, ChunkOfLatLon says %v", p, addr, want)
		}
		if i < 0 || i >= w.Params.ChunkTileEdge || j < 0 || j >= w.Params.ChunkTileEdge {
			t.Fatalf("local index %d %d", i, j)
		}
	}
}

func TestTile_Neighbours8Symmetric(t *testing.T) {
	w := testWorldForChunks(t)
	n := w.TileGrid()
	starts := []TileID{
		MakeTileID(FacePZ, 100, 100), MakeTileID(FacePZ, 0, 50), MakeTileID(FacePX, n-1, n/2),
		MakeTileID(FaceNY, n/2, 0), MakeTileID(FacePY, 7, n-1),
	}
	opp := [8]int{4, 5, 6, 7, 0, 1, 2, 3}
	for _, s := range starts {
		for k, nb := range w.Neighbours8(s) {
			if nb == s {
				t.Fatalf("%v is its own neighbour %d", s, k)
			}
			// only straight steps are guaranteed reversible across a face edge
			if k%2 == 0 && w.Neighbours8(nb)[opp[k]] != s {
				t.Fatalf("%v step %d to %v does not come back (got %v)", s, k, nb, w.Neighbours8(nb)[opp[k]])
			}
		}
	}
}

func TestTile_SlopeFlatAndRamp(t *testing.T) {
	w := testWorldForChunks(t)
	flat := gridSource{elev: func(x, y int32) int16 { return 100 }}
	mid := MakeTileID(FacePZ, 500, 500)
	if s, _ := w.TileSlopeBPS(flat, mid); s != 0 {
		t.Fatalf("flat slope %d", s)
	}
	ramp := gridSource{elev: func(x, y int32) int16 { return int16(x) * 10 }}
	s, _ := w.TileSlopeBPS(ramp, mid)
	if want := 10 * 10000 / w.TileLengthM(); s != want {
		t.Fatalf("ramp slope %d, want %d", s, want)
	}
}

func TestTile_WaterKinds(t *testing.T) {
	w := testWorldForChunks(t)
	src := gridSource{elev: func(x, y int32) int16 { return 0 }, flag: func(x, y int32) uint8 {
		switch {
		case x == 505 && y == 500:
			return tileFlagOcean
		case x == 500 && y == 503:
			return tileFlagLake
		case x == 500 && y == 495:
			return tileFlagStream
		}
		return 0
	}}
	from := MakeTileID(FacePZ, 500, 500)
	for _, c := range []struct {
		want WaterKind
		ring int
		ok   bool
	}{{WaterOcean, 5, true}, {WaterLake, 3, true}, {WaterStream, 5, true}, {WaterRiver, 0, false}} {
		hit, ok, err := w.NearestWater(src, from, 8, c.want)
		if err != nil || ok != c.ok || (ok && hit.Distance != c.ring) {
			t.Fatalf("want %v: %+v %v %v", c.want, hit, ok, err)
		}
	}
	if _, ok, _ := w.NearestWater(src, from, 2, WaterLake); ok {
		t.Fatal("the lake is outside 2 rings")
	}
	if WaterRiver.WaterWidthM() != 40 || WaterGreatRiver.WaterWidthM() != 200 || WaterStream.WaterWidthM() != 10 {
		t.Fatal("span widths changed from ADR 0042 6.3")
	}
}

func TestTile_PeaksAndProminence(t *testing.T) {
	w := testWorldForChunks(t)
	// Two hills on a plain at 0: a tall one (x 500, 300 m) and a short one
	// (x 505, 120 m). Along y=500 the profile is max(300-60|x-500|,
	// 120-20|x-505|): 300, 240, 180, 120, 100, 120 ... so the col between
	// them is 100 at x=504.
	prof := func(x, y int32) int16 {
		abs := func(a int) int {
			if a < 0 {
				return -a
			}
			return a
		}
		dy := abs(int(y) - 500)
		d1 := abs(int(x) - 500)
		if dy > d1 {
			d1 = dy
		}
		d2 := abs(int(x) - 505)
		if dy > d2 {
			d2 = dy
		}
		h := 300 - 60*d1
		if h2 := 120 - 20*d2; h2 > h {
			h = h2
		}
		if h < 0 {
			h = 0
		}
		return int16(h)
	}
	src := gridSource{elev: prof}
	peaks, err := w.PeaksNear(src, MakeTileID(FacePZ, 502, 500), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(peaks) != 2 {
		t.Fatalf("peaks %+v", peaks)
	}
	tall, short := peaks[0], peaks[1]
	if tall.Tile != MakeTileID(FacePZ, 500, 500) || tall.Elevation != 300 || tall.Prominence != 300 {
		t.Fatalf("tall peak %+v (window floor is 0, so prominence is 300)", tall)
	}
	if short.Tile != MakeTileID(FacePZ, 505, 500) || short.Elevation != 120 || short.Prominence != 20 {
		t.Fatalf("short peak %+v, want prominence 20 above the 100 m col", short)
	}
}

// TestTile_GoldenSeed42 pins the tile primitives on a real generated world:
// the same seed must always give the same tiles, slopes and water.
func TestTile_GoldenSeed42(t *testing.T) {
	if GeneratorVersion != 1 {
		t.Skip("capture fresh golden values for the new GeneratorVersion")
	}
	w := testWorldForChunks(t)
	src := NewChunkTileSource(w, 16)
	h := fnv.New64a()
	start := w.TileOfLatLon(35.7, 51.4)
	nb := w.Neighbours8(start)
	ids := append([]TileID{start}, nb[:]...)
	for _, id := range ids {
		s, err := w.TileSlopeBPS(src, id)
		if err != nil {
			t.Fatal(err)
		}
		k, _ := w.TileWaterKind(src, id)
		ct, _ := src.TileAt(id)
		h.Write([]byte{byte(s), byte(s >> 8), byte(k), byte(ct.Elevation), byte(ct.Elevation >> 8), ct.Biome})
	}
	const want = uint64(0x2acc2df0b441de71)
	if got := h.Sum64(); got != want {
		t.Fatalf("golden 0x%x", got)
	}
}
