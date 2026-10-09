package lookdesc

import (
	"encoding/json"
	"fmt"
	"testing"
)

func house(id string) Input {
	return Input{ID: id, Function: "dwelling", Level: 2, W: 1, D: 1, Storeys: 1, ConditionBPS: 10000, Biome: "temperate_forest",
		Modules: map[string]int{"bedroom": 2, "hearth": 1, "storeroom": 1}}
}

func TestTheSameBuildingHasTheSameLook(t *testing.T) {
	a, b := Describe(house("b1")), Describe(house("b1"))
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("the look is not deterministic:\n%s\n%s", ja, jb)
	}
	if len(ja) > 280 {
		t.Errorf("the descriptor is %d bytes, it should stay near 200: %s", len(ja), ja)
	}
	if !a.Chimney || a.Awning || a.Windows < 1 || a.Material != "timber" {
		t.Errorf("look does not follow the contents: %+v", a)
	}
}

func TestContentsShowInTheLook(t *testing.T) {
	stall := Describe(Input{ID: "s1", Function: "stall", Level: 1, W: 1, D: 1, Storeys: 1, ConditionBPS: 10000, Modules: map[string]int{"shelves": 2}})
	if !stall.Awning || stall.Chimney {
		t.Errorf("a stall has an awning and no chimney: %+v", stall)
	}
	tall := house("t1")
	tall.Storeys = 3
	if Describe(tall).Material != "stone" {
		t.Error("three storeys stand in stone")
	}
	dry := house("d1")
	dry.Biome = "desert"
	if r := Describe(dry).Roof; r != "flat" && r != "shed" {
		t.Errorf("a desert roof is %s", r)
	}
	cold := house("c1")
	cold.Biome = "tundra"
	if r := Describe(cold).Roof; r != "gable" && r != "hip" {
		t.Errorf("a tundra roof is %s", r)
	}
}

func TestNoTwoHousesAreIdentical(t *testing.T) {
	keys := map[string]int{}
	for i := 0; i < 300; i++ {
		keys[Describe(house(fmt.Sprintf("building-%d", i))).Key()]++
	}
	// well over a hundred distinct looks among 300 houses (thousands are possible)
	if len(keys) < 120 {
		t.Errorf("only %d distinct looks among 300 houses", len(keys))
	}
}

func TestANeighbourIsNeverTheSameHouse(t *testing.T) {
	first := Describe(house("n1"))
	near := map[string]bool{first.Key(): true}
	d, rerolls := Distinct(house("n1"), near, 8)
	if d.Key() == first.Key() || rerolls == 0 {
		t.Errorf("the look equals its neighbour's (rerolls %d)", rerolls)
	}
	// nothing to avoid: the first roll stands
	if _, r := Distinct(house("n1"), nil, 8); r != 0 {
		t.Errorf("rerolled with no neighbour: %d", r)
	}
}
