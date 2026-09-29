package worldgen

import "testing"

// goldenFingerprints locks the exact output of GeneratorVersion 1 for a
// handful of seeds, using DefaultParams and sampleContent (this package's
// test fixture, not the production configs/content/world.yml — the point is
// to catch an accidental change to this package's ALGORITHM, not to track
// content edits).
//
// (Regenerated once with the worldgen-seams fix while GeneratorVersion 1 was still unshipped; after the first shipped world, never do this without bumping the version.)
//
// IF THIS TEST FAILS: either a real bug was introduced, or a deliberate
// change to generation was made. A deliberate change must bump
// GeneratorVersion (world.go) — an already-shipped world's seed must keep
// producing what it always produced — and ONLY THEN may this map be
// regenerated (see the seed-first design in the package doc and in the
// project report's proposed schema). Never update these values to make a
// failing test pass without bumping the version first; that is precisely
// the bug this test exists to catch.
var goldenFingerprints = map[uint64]uint64{
	1:  0x392680ee2d1e5d2c,
	2:  0xe9af4a045ac905d7,
	3:  0x1f4049fc850c6c2c,
	42: 0x55b1b5043b7ca9b9,
}

func TestGolden_Fingerprints(t *testing.T) {
	if GeneratorVersion != 1 {
		t.Skip("GeneratorVersion has moved past the version these golden values were captured for; " +
			"capture a fresh set for the new version instead of editing this skip away")
	}
	content := sampleContent()
	for seed, want := range goldenFingerprints {
		w, err := Generate(seed, DefaultParams(), content)
		if err != nil {
			t.Fatalf("seed %d: Generate: %v", seed, err)
		}
		got := w.Fingerprint()
		if got != want {
			t.Errorf("seed %d: fingerprint changed: got %016x, want %016x", seed, got, want)
		}
	}
}
