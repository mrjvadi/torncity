package content

import "testing"

// The farming content is coherent (docs/adr/0067): every branch farm stands near the water work of its branch within the reach
// the water factor uses, the rain-fed farm needs none, the water works are daily services with a master and decay, the mills
// grind, and the toll statute is the range the ADR names.
func TestShippedFarmingIsCoherent(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	fd, ok := snap.Farming()
	if !ok {
		t.Fatal("farming.yml is shipped")
	}
	for _, br := range fd.Branches {
		farm, ok := snap.SettlementBuildingDef(br.Farm)
		if !ok {
			t.Fatalf("no farm %s", br.Farm)
		}
		if _, ok := snap.SettlementBuildingDef(br.Farm + PrivateSuffix); !ok {
			t.Errorf("%s has no citizen twin", br.Farm)
		}
		if br.Rainfed {
			if farm.Near != nil {
				t.Errorf("%s is rain-fed and needs no water work near it", br.Farm)
			}
			continue
		}
		if farm.Near == nil || farm.Near.Radius != fd.Water.Reach || len(farm.Near.Codes) != 1 || farm.Near.Codes[0] != br.Work {
			t.Errorf("%s must stand within %d lots of its %s: %+v", br.Farm, fd.Water.Reach, br.Work, farm.Near)
		}
		work, _ := snap.SettlementBuildingDef(br.Work)
		if work.Role != "water_infra" || (work.Near == nil && work.TerrainMode != "required") {
			t.Errorf("%s is a water work that must stand where its water is: %+v", br.Work, work)
		}
		fn, ok := snap.FunctionReplacing(br.Work)
		if !ok {
			t.Fatalf("%s has no function row", br.Work)
		}
		row, _ := snap.BuildingFunction(fn)
		if row.Produces == nil || !row.Produces.Daily || row.Produces.Service != "irrigation" || len(row.Staff) != 1 || row.Staff[0].Role != "water_master" {
			t.Errorf("%s is the daily service of a water master: %+v", br.Work, row.Produces)
		}
		if row.Maintenance == nil || row.Maintenance.DecayBPSPerDay <= 0 {
			t.Errorf("%s wears and is repaired by labour", br.Work)
		}
	}
	for _, code := range []string{"mill", "water_mill"} {
		d, ok := snap.SettlementBuildingDef(code)
		if !ok || !d.Grinds || d.Consumes["wheat"] < 1 {
			t.Errorf("%s grinds wheat for the toll: %+v", code, d)
		}
	}
	hand, _ := snap.SettlementBuildingDef("mill")
	water, _ := snap.SettlementBuildingDef("water_mill")
	if water.Consumes["wheat"] != 3*hand.Consumes["wheat"] || water.Near == nil {
		t.Errorf("the water mill grinds three times the hand mill and needs its water near: %d vs %d, %+v", water.Consumes["wheat"], hand.Consumes["wheat"], water.Near)
	}
	if fd.Toll.MinBPS != 333 || fd.Toll.MaxBPS != 1000 || fd.Toll.DefaultBPS != 500 {
		t.Errorf("the toll statute is a thirtieth to a tenth, a twentieth by default: %+v", fd.Toll)
	}
	if k, ok := snap.SettlementKnowledgeDef("dryland_farming"); !ok || len(k.Provides) == 0 || k.Provides[0] != "arable_farming" {
		t.Error("the rain-fed farm's research provides arable farming, so no village is without a farm")
	}
	if pasture, ok := snap.SettlementBuildingDef("pasture_range"); !ok || !pasture.Grazes {
		t.Error("the pasture grazes open land")
	}
}
