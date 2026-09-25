package item

import (
	"reflect"
	"testing"
)

func gapsCatalog() Components {
	return Components{
		"cpu_a7":     {Code: "cpu_a7", Category: "processor", RequiresTechnology: []string{"microchips"}},
		"cpu_basic":  {Code: "cpu_basic", Category: "processor", RequiresTechnology: []string{"microchips", "lithography"}},
		"cell":       {Code: "cell", Category: "battery", RequiresTechnology: []string{"batteries"}},
		"lcd":        {Code: "lcd", Category: "display"},
		"shell":      {Code: "shell", Category: "casing"},
		"speaker":    {Code: "speaker", Category: "speaker"},
		"barrel":     {Code: "barrel", Category: "barrel"},
		"stock":      {Code: "stock", Category: "stock"},
		"scope":      {Code: "scope", Category: "optic", RequiresTechnology: []string{"optics"}},
		"cartridge":  {Code: "cartridge", Category: "cartridge"},
		"ammo_smart": {Code: "ammo_smart", Category: "cartridge", RequiresTechnology: []string{"guidance"}},
	}
}

func TestDesignGaps(t *testing.T) {
	comps := gapsCatalog()
	none := TechAccess{}

	// A phone: the cheapest processor needs one technology, the battery
	// another.
	missing, possible := DesignGaps(phoneArchetype(), nil, comps, none)
	if !possible || !reflect.DeepEqual(missing, []string{"batteries", "microchips"}) {
		t.Fatalf("a phone with nothing: %v %v", missing, possible)
	}
	// Licensed microchips and published batteries: nothing missing.
	open := TechAccess{Unlocked: NewSet("batteries"), Licensed: NewSet("microchips")}
	if missing, possible := DesignGaps(phoneArchetype(), nil, comps, open); !possible || len(missing) != 0 {
		t.Fatalf("a phone with the technologies: %v %v", missing, possible)
	}
	// A good's own technology counts, whatever its parts.
	if missing, _ := DesignGaps(phoneArchetype(), []string{"design_school", "batteries"}, comps, open); !reflect.DeepEqual(missing, []string{"design_school"}) {
		t.Fatalf("a good that needs its own technology: %v", missing)
	}
	// An optional slot never gates: a rifle is open without optics, and a
	// slot with a free component needs nothing.
	if missing, possible := DesignGaps(rifleArchetype(), nil, comps, none); !possible || len(missing) != 0 {
		t.Fatalf("a rifle: %v %v", missing, possible)
	}
	// A slot nothing fills: no technology opens it.
	bare := Components{"lcd": comps["lcd"]}
	if _, possible := DesignGaps(phoneArchetype(), nil, bare, open); possible {
		t.Fatal("a phone with no processor in the catalogue is possible")
	}
}
