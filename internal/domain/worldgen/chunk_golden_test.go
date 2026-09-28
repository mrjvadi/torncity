package worldgen

import "testing"

// goldenChunkFingerprints locks the exact chunk output of GeneratorVersion 1
// for a couple of seeds and a handful of chunks — one same-face interior
// chunk, one right on a cube edge, and one at a coarse (non-base) LOD —
// using smallParams()+sampleContent() (this package's fast test fixture,
// not production content — see golden_test.go's identical rationale for
// the coarse World fingerprint).
//
// IF THIS TEST FAILS: either a real bug was introduced into chunk
// generation, or a deliberate change was made to it. A deliberate change
// bumps GeneratorVersion (world.go) exactly as golden_test.go's own
// doc already explains for the coarse World — chunk generation reads the
// same seed-first contract, so the same rule applies to it. Never edit
// these values to make a failing test pass without bumping the version
// first.
var goldenChunkFingerprints = map[uint64]map[ChunkAddr]uint64{
	1: {
		{FacePZ, 10, 5, 5}:      0xf2dab93119d3f0ec,
		{FacePX, 10, 1023, 512}: 0xfdc75562e56854d0,
		{FaceNZ, 8, 3, 3}:       0xa79a0c110f8a2035,
	},
	42: {
		{FacePZ, 10, 5, 5}:      0x1bf246f86c74c2a0,
		{FacePX, 10, 1023, 512}: 0x95507e4dd5dbe4cb,
		{FaceNZ, 8, 3, 3}:       0xa69fc3c9db835f85,
	},
}

func TestGolden_ChunkFingerprints(t *testing.T) {
	if GeneratorVersion != 1 {
		t.Skip("GeneratorVersion has moved past the version these golden values were captured for; " +
			"capture a fresh set for the new version instead of editing this skip away")
	}
	content := sampleContent()
	for seed, addrs := range goldenChunkFingerprints {
		w, err := Generate(seed, smallParams(), content)
		if err != nil {
			t.Fatalf("seed %d: Generate: %v", seed, err)
		}
		for addr, want := range addrs {
			c, err := w.GenerateChunk(addr)
			if err != nil {
				t.Fatalf("seed %d addr %+v: GenerateChunk: %v", seed, addr, err)
			}
			got := c.Fingerprint()
			if want == 0 {
				t.Logf("seed %d addr %+v: fingerprint = %#016x (paste this into goldenChunkFingerprints)", seed, addr, got)
				continue
			}
			if got != want {
				t.Errorf("seed %d addr %+v: fingerprint changed: got %#016x, want %#016x", seed, addr, got, want)
			}
		}
	}
}
