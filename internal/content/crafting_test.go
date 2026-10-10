package content

import "testing"

// The recipes, the home modules and the tool ladder are coherent (docs/adr/0068): every home module stands for a workshop that
// has recipes; every recipe that is read is made somewhere a player can reach (a workshop crew, a home station or the
// workshop's own standard shift); the tool ladder is made of items that exist, tier 1 is the old `tools`.
func TestShippedCraftingIsCoherent(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	cd, ok := snap.Crafting()
	if !ok {
		t.Fatal("crafting.yml is shipped")
	}
	tiers := map[int]string{}
	for _, x := range cd.Tools {
		tiers[x.Tier] = x.Item
		if _, ok := snap.ItemStorage(x.Item); !ok {
			t.Errorf("tool tier %d: %s has no storage class", x.Tier, x.Item)
		}
	}
	if tiers[1] != "tools" || tiers[0] == "" || tiers[2] == "" {
		t.Errorf("the ladder: stone, the old tools, forged: %v", tiers)
	}
	standsFor := map[string]bool{"dwelling": true}
	for _, code := range snap.ModuleKindCodes() {
		md, _ := snap.ModuleKind(code)
		for _, st := range md.StandsFor {
			standsFor[st] = true
			if len(snap.RecipesAt(st)) == 0 {
				t.Errorf("module %s stands for %s, which has no recipe", code, st)
			}
		}
	}
	for _, md := range []string{"workbench", "forge", "loom", "kiln", "oven", "millstone"} {
		m, ok := snap.ModuleKind(md)
		if !ok || len(m.StandsFor) == 0 || m.BuildShifts < 1 || m.WaitsFor != "" {
			t.Errorf("the home module %s is built and stands for a workshop: %+v", md, m)
		}
	}
	waiting := 0
	for _, f := range snap.BuildingFunctionCodes() {
		for _, code := range snap.RecipesAt(f) {
			r, _ := snap.Recipe(code)
			if r.WaitsFor != "" {
				continue
			}
			home := standsFor[f]
			workshop := !r.HomeOnly && len(r.DefaultAt) == 0
			def := isDefault(r, f)
			if !home && !workshop && !def {
				t.Errorf("recipe %s at %s is made nowhere a player can reach", code, f)
			}
		}
	}
	for _, code := range []string{"lime", "fulled_cloth", "linen", "rope"} {
		if r, ok := snap.Recipe(code); !ok || r.WaitsFor == "" {
			t.Errorf("recipe %s waits for its source goods or its reader", code)
		} else {
			waiting++
		}
	}
	if waiting != 4 {
		t.Errorf("four recipes wait: %d", waiting)
	}
	// 14 of the ADR's 29 are read now, as three recipes of the iron tools make 16 here
	read := 0
	for _, p := range p.Recipes {
		if p.WaitsFor == "" {
			read++
		}
	}
	if read != 19 {
		t.Errorf("19 recipes are read (14 rows of the ADR, row 17 as three, and three remedies of ADR 0069): %d", read)
	}
}

func isDefault(r RecipeDef, station string) bool {
	for _, d := range r.DefaultAt {
		if d == station {
			return true
		}
	}
	return false
}
