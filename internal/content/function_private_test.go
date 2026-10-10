package content

import "testing"

// A workplace row that opts in gets its citizen twin (docs/adr/0066): a building the lot owner places and employs workers in,
// and the function row a lot can choose, with the same staff, inputs and outputs and a yard of personal storage.
func TestAWorkplaceHasItsCitizenTwin(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	twins := 0
	for _, f := range p.BuildingFunctions {
		if f.Workplace == nil || !f.Workplace.Private {
			continue
		}
		twins++
		pub := f.Workplace.buildingCode(f.Code)
		b, ok := snap.SettlementBuildingDef(pub + PrivateSuffix)
		if !ok || !b.Private() || b.PermitClass != f.Permit || len(b.Produces) == 0 {
			t.Errorf("%s: the citizen twin is wrong: %+v", pub, b)
			continue
		}
		pubDef, _ := snap.SettlementBuildingDef(pub)
		if b.Wage != pubDef.Wage || b.Shift != pubDef.Shift || b.Workers != pubDef.Workers || b.Upkeep != 0 {
			t.Errorf("%s: the twin must work like the public one (wage, shift, hands) and cost no upkeep", pub)
		}
		var yard int64
		for _, e := range b.Effects {
			if e.Target == "personal_storage" {
				yard += e.Value
			}
		}
		if yard != 40 {
			t.Errorf("%s: the twin gives a yard of 40 spaces, not %d", pub, yard)
		}
		fn, ok := snap.BuildingFunction(f.Code + PrivateSuffix)
		if !ok || len(fn.Levels) != 1 || fn.Levels[0].Building != b.Code || len(fn.Staff) != len(f.Staff) {
			t.Errorf("%s: the twin function row is wrong: %+v", f.Code, fn)
		}
	}
	// the first wave of 11 (docs/adr/0066) and the five farms and the water mill (docs/adr/0067), the herb garden and the apothecary (docs/adr/0069)
	if twins != 19 {
		t.Errorf("%d workplaces opt in to a citizen twin, want 19", twins)
	}
}
