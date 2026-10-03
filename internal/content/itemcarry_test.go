package content

import (
	"errors"
	"strings"
	"testing"
)

func carryProblems(d ItemDef) string {
	var problems []error
	validateItemCarry(d, "item", func(e error) { problems = append(problems, e) })
	if len(problems) == 0 {
		return ""
	}
	return errors.Join(problems...).Error()
}

func goodBag() ItemDef {
	return ItemDef{Code: "bag", Shelf: "bags.back", Form: "unique", Durability: 40,
		Bag: &BagDef{Slot: "back", Space: 12, ComfortKg: 10, HardKg: 30}}
}

func TestAGoodBagIsAccepted(t *testing.T) {
	if got := carryProblems(goodBag()); got != "" {
		t.Fatalf("a good bag was refused: %s", got)
	}
}

func TestBagsAreHeldToTheirRules(t *testing.T) {
	cases := map[string]func(*ItemDef){
		"bag slot":                func(d *ItemDef) { d.Bag.Slot = "head" },
		"bag space":               func(d *ItemDef) { d.Bag.Space = 0 },
		"bag load":                func(d *ItemDef) { d.Bag.HardKg = 5 },
		"must be a unique piece":  func(d *ItemDef) { d.Form = "stack" },
		"needs durability":        func(d *ItemDef) { d.Durability = 0 },
		"must sit on the shelf":   func(d *ItemDef) { d.Shelf = "bags.belt" },
		"no vehicle, gear":        func(d *ItemDef) { d.Parked = true },
		"is bought and sold":      func(d *ItemDef) { f := false; d.Tradeable = &f },
		"is out of range":         func(d *ItemDef) { d.Bulk = 500 },
		"is parked and so has no": func(d *ItemDef) { d.Parked = true; d.Bulk = 2; d.Bag = nil },
	}
	for want, mutate := range cases {
		d := goodBag()
		mutate(&d)
		if got := carryProblems(d); !strings.Contains(got, want) {
			t.Errorf("%q: not refused as expected: %q", want, got)
		}
	}
}

func TestBulkAndWeightDefaults(t *testing.T) {
	if (ItemDef{}).BulkUnits() != 1 || (ItemDef{}).WeightGrams() != DefaultWeightG {
		t.Fatal("an item with no figures takes 1 room and the default weight")
	}
	if (ItemDef{Parked: true}).BulkUnits() != 0 || (ItemDef{Parked: true}).WeightGrams() != 0 {
		t.Fatal("a parked vehicle takes no room and has no weight")
	}
	if (ItemDef{Bulk: 3, WeightG: 900}).BulkUnits() != 3 || (ItemDef{Bulk: 3, WeightG: 900}).WeightGrams() != 900 {
		t.Fatal("authored figures were ignored")
	}
}

func TestShippedBagsFollowTheLadderOfTheADR(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{"bag_pouch", "bag_sack", "bag_daypack", "bag_hiking", "bag_frame"}
	var prevPer float64
	for i, code := range order {
		d, ok := snap.ItemDef(code)
		if !ok || d.Bag == nil {
			t.Fatalf("%s is missing or is not a bag", code)
		}
		if _, ok := d.CarryBag(d.Durability); !ok {
			t.Fatalf("%s does not convert to a carry bag", code)
		}
		per := float64(d.BasePrice) / float64(d.Bag.Space)
		// "The more you pay, the better": from the daypack up the price per space rises with every tier.
		if i >= 3 && per <= prevPer {
			t.Errorf("%s costs %.1f per space, not more than the tier below (%.1f)", code, per, prevPer)
		}
		if i >= 2 {
			prevPer = per
		}
	}
	sack, _ := snap.ItemDef("bag_sack")
	if sack.Bag.Space != 12 {
		t.Errorf("the sack gives %d, ADR 0046 needs 12 so that 8 + 12 restores the planned 20", sack.Bag.Space)
	}
	if sh, ok := snap.ShelfOf("bag_pouch"); !ok || sh.Code != "bags.belt" {
		t.Errorf("the pouch sits on %+v", sh)
	}
}
