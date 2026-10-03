package content

import (
	"strings"
	"testing"
)

func TestBuildCategoriesRefuseABuildingInAnUnknownGroup(t *testing.T) {
	p := &Pack{
		BuildCategories: []BuildCategoryDef{{Code: "shops", Roles: []string{"market"}}, {Code: "other"}},
		SettlementBuildings: []SettlementBuildingDef{
			{Code: "stall", Role: "market"},
			{Code: "odd", Role: "market", BuildCategory: "nope"},
		},
	}
	var problems []error
	p.validateBuildCategories(&problems)
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), `build_category "nope"`) {
		t.Fatalf("problems = %v", problems)
	}
}

func TestBuildCategoriesRefuseARoleInTwoGroupsAndAMissingFallback(t *testing.T) {
	p := &Pack{BuildCategories: []BuildCategoryDef{{Code: "a", Roles: []string{"x"}}, {Code: "b", Roles: []string{"x"}}}}
	var problems []error
	p.validateBuildCategories(&problems)
	got := ""
	for _, e := range problems {
		got += e.Error()
	}
	for _, want := range []string{`role "x" is in both`, "fallback group"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestShippedBuildingsAllHaveABuildCategory(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"civic_hall": "public", "barter_post": "shops", "road": "construction", "cottage": "housing", "watch_hut": "security"}
	for code, cat := range want {
		d, ok := snap.SettlementBuildingDef(code)
		if !ok {
			continue
		}
		if got := snap.BuildCategoryOf(d); got != cat {
			t.Errorf("%s: category %q, want %q", code, got, cat)
		}
	}
	for _, d := range snap.SettlementBuildingDefs() {
		if d.Role != "" && snap.BuildCategoryOf(d) == BuildCategoryOther {
			t.Errorf("building %s (role %s) falls into the fallback group: map its role", d.Code, d.Role)
		}
	}
}
