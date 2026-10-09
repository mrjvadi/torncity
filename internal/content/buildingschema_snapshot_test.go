package content

import "testing"

func TestBuildingSchemaSnapshotLookups(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := snap.BuildingFunction("woodcutter_yard")
	if !ok || f.Produces == nil || f.Produces.Outputs["timber"] != 6 || f.Produces.Outputs["firewood"] != 3 {
		t.Fatalf("woodcutter_yard: %+v", f)
	}
	if got, ok := snap.FunctionReplacing("woodcutter_camp"); !ok || got != "woodcutter_yard" {
		t.Fatalf("woodcutter_camp is replaced by %q", got)
	}
	if got, ok := snap.FunctionReplacing("private_house"); !ok || got != "dwelling" {
		t.Fatalf("private_house is replaced by %q", got)
	}
	found := false
	for _, r := range snap.RecipesAt("smithy") {
		found = found || r == "bar_iron"
	}
	if !found {
		t.Fatal("bar_iron is made at the smithy")
	}
	if c, ok := snap.StorageClass("bulk"); !ok || c.BaseRoom != 60 {
		t.Fatalf("bulk class %+v", c)
	}
	if i, ok := snap.ItemStorage("bread"); !ok || i.FoodPoints != 4 || i.Class != "food" {
		t.Fatalf("bread %+v", i)
	}
	if cl, ok := snap.Climate(); !ok || cl.GameYearDays != 28 {
		t.Fatalf("climate %+v", cl)
	}
	if r, ok := snap.SettlementRaid(); !ok || r.MaxLeadHours != 48 || r.GraceDays != 7 {
		t.Fatalf("raid %+v", r)
	}
	if len(snap.SettlementRaidDetectors()) != 6 {
		t.Fatal("six detectors")
	}
	if c, ok := snap.RoadClass("track"); !ok || c.MaxGradeBPS != 1200 {
		t.Fatalf("track %+v", c)
	}
	if p, ok := snap.RoadPlanner(); !ok || p.BridgeCostRatio != 25 {
		t.Fatalf("planner %+v", p)
	}
	// every legacy building a function takes over is a real building, and the first-stage
	// research rows are in the lookups
	for _, code := range []string{"teahouse_inn", "land_registry", "exchange_house", "bank"} {
		if _, ok := snap.BuildingFunction(code); !ok {
			t.Errorf("%s is missing", code)
		}
	}
}
