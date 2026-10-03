package content

import (
	"errors"
	"strings"
	"testing"
)

func shelfTestPack() *Pack {
	return &Pack{
		ItemCategories: []ItemCategoryDef{
			{Code: "food", Children: []ItemShelfDef{{Code: "grain"}, {Code: "cooked"}}},
			{Code: "tools", Children: []ItemShelfDef{{Code: "hand"}, {Code: "locks", Restricted: true}}},
			{Code: "military"},
			{Code: "other"},
		},
		Components: []ComponentDef{{Code: "flour", Shelf: "food.grain"}},
		Items: []ItemDef{
			{Code: "bread", Shelf: "food.cooked"},
			{Code: "jet", Shelf: "military"},
		},
	}
}

func shelfProblems(p *Pack) string {
	var problems []error
	p.validateItemShelves(&problems)
	return errors.Join(problems...).Error()
}

func TestItemShelvesAcceptAWellFormedTree(t *testing.T) {
	var problems []error
	shelfTestPack().validateItemShelves(&problems)
	if len(problems) != 0 {
		t.Fatalf("a good tree was refused: %v", problems)
	}
}

func TestItemShelvesRefuseAGoodWithoutAShelf(t *testing.T) {
	p := shelfTestPack()
	p.Items = append(p.Items, ItemDef{Code: "rope"})
	if got := shelfProblems(p); !strings.Contains(got, `item "rope" has no shelf`) {
		t.Fatalf("a good without a shelf was not named: %s", got)
	}
}

func TestItemShelvesRefuseAnUnknownShelfAndTheFallback(t *testing.T) {
	p := shelfTestPack()
	p.Items = append(p.Items, ItemDef{Code: "rope", Shelf: "food.nope"}, ItemDef{Code: "junk", Shelf: "other"})
	p.Components = append(p.Components, ComponentDef{Code: "dust", Shelf: "tools"})
	got := shelfProblems(p)
	for _, want := range []string{`"food.nope"`, `fallback shelf "other"`, `component "dust" names shelf "tools"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %s", want, got)
		}
	}
}

func TestItemShelvesKeepRestrictedLeavesForBlackMarketGoods(t *testing.T) {
	p := shelfTestPack()
	p.Items = append(p.Items, ItemDef{Code: "pick", Shelf: "tools.locks"})
	if got := shelfProblems(p); !strings.Contains(got, "restricted shelf") {
		t.Fatalf("a plain good on a restricted shelf was not refused: %s", got)
	}
	p.Items[len(p.Items)-1].Tags = []string{"crime_tool"}
	var problems []error
	p.validateItemShelves(&problems)
	if len(problems) != 0 {
		t.Fatalf("a crime tool on the locks shelf was refused: %v", problems)
	}
}

func TestItemShelvesNeedTheFallbackGroup(t *testing.T) {
	p := shelfTestPack()
	p.ItemCategories = p.ItemCategories[:3]
	if got := shelfProblems(p); !strings.Contains(got, "fallback category") {
		t.Fatalf("a tree without other was accepted: %s", got)
	}
}

func TestShippedItemsAllSitOnAShelf(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.ItemCategories) == 0 {
		t.Fatal("the shipped content has no item_categories.yml")
	}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range snap.Items() {
		sh, ok := snap.ShelfOf(it.Code)
		if !ok {
			t.Errorf("item %q has no resolvable shelf (%q)", it.Code, it.Shelf)
			continue
		}
		if sh.Label == "" || sh.GroupLabel == "" {
			t.Errorf("item %q: shelf %q has no label key", it.Code, sh.Code)
		}
	}
	if sh, _ := snap.ShelfOf("bread"); sh.Code != "food.cooked" || sh.Label != "item_shelf.food.cooked" || sh.GroupLabel != "item_shelf_group.food" {
		t.Errorf("bread shelf = %+v", sh)
	}
	if sh, ok := snap.ItemShelf("military"); !ok || sh.Label != "item_shelf_group.military" {
		t.Errorf("military leaf = %+v ok=%v", sh, ok)
	}
}
