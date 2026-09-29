package worldgen

import "testing"

// goldenChunkFingerprints locks the exact chunk output of GeneratorVersion 1
// for a couple of seeds and a handful of chunks — one same-face interior
// chunk, one right on a cube edge, and one at a coarse (non-base) LOD —
// using smallParams()+sampleContent() (this package's fast test fixture,
// not production content — see golden_test.go's identical rationale for
// the coarse World fingerprint).
//
// (Regenerated once with the worldgen-seams fix while GeneratorVersion 1 was still unshipped; after the first shipped world, never do this without bumping the version.)
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
		{FacePZ, 10, 5, 5}:      0xd3ab45fef8e64bb5,
		{FacePX, 10, 1023, 512}: 0xe4b8506fcb20201b,
		{FaceNZ, 8, 3, 3}:       0x4c2cc574a72d72f4,
	},
	42: {
		{FacePZ, 10, 5, 5}:      0x3dad11a64d4916aa,
		{FacePX, 10, 1023, 512}: 0xc1579e5ccc1df735,
		{FaceNZ, 8, 3, 3}:       0x80c1e529dad8f505,
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
