package content

import (
	"os"
	"path/filepath"
	"testing"
)

// TestShippedWorldGenContent is the same kind of guard
// TestShippedContentLoadsAndValidates is for the rest of configs/content:
// it stops somebody breaking the real configs/content/world.yml, not just a
// synthetic fixture. Every other worldgen content test builds its own
// in-memory pack.
func TestShippedWorldGenContent(t *testing.T) {
	pack, err := LoadWorldGen(shippedContentDir(t))
	if err != nil {
		t.Fatalf("LoadWorldGen: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	c, err := pack.ToContent()
	if err != nil {
		t.Fatalf("ToContent: %v", err)
	}

	if len(c.Biomes) < 8 {
		t.Errorf("expected at least 8 biomes, got %d", len(c.Biomes))
	}
	wantResources := []string{
		"crude_oil", "natural_gas", "coal", "iron", "copper", "gold", "uranium",
		"bauxite", "lithium", "fertile_soil", "fresh_water", "timber", "fish",
	}
	have := map[string]bool{}
	for _, r := range c.Resources {
		have[r.Code] = true
		if r.ColorHex == "" {
			t.Errorf("resource %q has no color_hex", r.Code)
		}
	}
	for _, code := range wantResources {
		if !have[code] {
			t.Errorf("missing resource %q from the shipped content", code)
		}
	}
	for _, b := range c.Biomes {
		if !b.IsWater && b.ColorHex == "" {
			t.Errorf("land biome %q has no color_hex", b.Code)
		}
	}
}

// TestWorldGenContentRejectsUnknownField is ADR 0004 rule 1 (validate before
// write, and an unknown key is a hard error) for this content type.
func TestWorldGenContentRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	contents := `
version: 1
biomes:
  - code: ocean
    is_water: true
    water_kind: ocean
    not_a_real_field: 1
`
	if err := os.WriteFile(filepath.Join(dir, "world.yml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorldGen(dir); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}
