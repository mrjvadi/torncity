package settlement

import "testing"

func TestGenerateName_Deterministic(t *testing.T) {
	w := testWorld(t)
	a := GenerateName(w, 5, 1)
	b := GenerateName(w, 5, 1)
	if a != b {
		t.Fatalf("GenerateName is not deterministic: %+v vs %+v", a, b)
	}
	if a.Latin == "" || a.Persian == "" {
		t.Fatalf("GenerateName returned an empty name: %+v", a)
	}
}

func TestGenerateName_DiffersByCell(t *testing.T) {
	w := testWorld(t)
	seen := map[string]bool{}
	dupes := 0
	for cell := int32(0); cell < 200; cell++ {
		n := GenerateName(w, cell, 1)
		if seen[n.Latin] {
			dupes++
		}
		seen[n.Latin] = true
	}
	// Some collisions across 200 cells drawing from a modest syllable set
	// are expected; a name is not required to be globally unique. This only
	// guards against the generator degenerating to one constant name.
	if len(seen) < 20 {
		t.Fatalf("only %d distinct names across 200 cells (%d duplicates), generator looks degenerate", len(seen), dupes)
	}
}

func TestNearbyFeature_Deterministic(t *testing.T) {
	w := testWorld(t)
	for cell := int32(0); cell < 50; cell++ {
		a := NearbyFeature(w, cell)
		b := NearbyFeature(w, cell)
		if a != b {
			t.Fatalf("NearbyFeature(%d) is not deterministic: %+v vs %+v", cell, a, b)
		}
	}
}
