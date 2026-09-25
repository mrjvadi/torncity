package technology

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

func stagingTree() Tree {
	out := Tree{}
	for _, t := range tree() {
		out[t.Code] = t
	}
	out["radar"] = Tech{Code: "radar", Cost: 10, Time: time.Hour, CompanyTypes: []string{"factory"},
		Control: Control{Restricted: true, BuyerClasses: []string{"company:defence"}}}
	out["aesa"] = Tech{Code: "aesa", Requires: []string{"radar"}, Cost: 10, Time: time.Hour, CompanyTypes: []string{"factory"},
		Control: Control{Restricted: true, BuyerClasses: []string{"company:defence"}}}
	return out
}

func TestAssessSortsWhatIsMissing(t *testing.T) {
	tr := stagingTree()
	fresh := Standing{CompanyType: "factory", Owned: item.NewSet(), Published: item.NewSet(),
		Buyer: Buyer{Kind: "company", Sector: "civilian"}}

	if r, steps := Assess(nil, tr, fresh, nil); r != Ready || steps != nil {
		t.Fatalf("nothing missing: %v %v", r, steps)
	}
	// A root technology the company may research: one step.
	r, steps := Assess([]string{"batteries"}, tr, fresh, nil)
	if r != Next || len(steps) != 1 || !steps[0].Research {
		t.Fatalf("batteries for a new factory: %v %v", r, steps)
	}
	// Microchips need semiconductors first: further away, unless a license
	// is on offer.
	if r, _ := Assess([]string{"microchips"}, tr, fresh, nil); r != Far {
		t.Fatalf("microchips for a new factory: %v, want far", r)
	}
	offered := func(code string) bool { return code == "microchips" }
	if r, steps := Assess([]string{"microchips"}, tr, fresh, offered); r != Next || steps[0].Research {
		t.Fatalf("microchips on offer: %v %v, want one step by license", r, steps)
	}
	// Every missing technology must be one step away.
	if r, _ := Assess([]string{"batteries", "microchips"}, tr, fresh, nil); r != Far {
		t.Fatalf("batteries and microchips: %v, want far", r)
	}
	withSemis := fresh
	withSemis.Owned = item.NewSet("semiconductors")
	if r, steps := Assess([]string{"microchips", "batteries"}, tr, withSemis, nil); r != Next || len(steps) != 2 {
		t.Fatalf("after semiconductors: %v %v", r, steps)
	}
	// A kind that may not research it, and an unknown code, are far.
	mine := fresh
	mine.CompanyType = "mine"
	if r, _ := Assess([]string{"batteries"}, tr, mine, nil); r != Far {
		t.Fatalf("a mine and batteries: %v", r)
	}
	if r, _ := Assess([]string{"alchemy"}, tr, fresh, nil); r != Far {
		t.Fatalf("an unknown technology: %v", r)
	}
	// A restricted technology is a step only for a cleared company.
	if r, _ := Assess([]string{"radar"}, tr, fresh, nil); r != Far {
		t.Fatalf("radar for a civilian company: %v", r)
	}
	cleared := fresh
	cleared.Buyer.Sector = "defence"
	if r, _ := Assess([]string{"radar"}, tr, cleared, nil); r != Next {
		t.Fatalf("radar for a cleared company: %v", r)
	}
}

func TestCanResearchRefusesAnUnclearedCompany(t *testing.T) {
	tr := stagingTree()
	s := Standing{CompanyType: "factory", Owned: item.NewSet(), Published: item.NewSet(),
		Buyer: Buyer{Kind: "company", Sector: "civilian"}}
	if err := CanResearch(tr["radar"], s); !errors.Is(err, ErrNotCleared) {
		t.Fatalf("a civilian company researching radar: %v", err)
	}
	s.Buyer.Sector = "defence"
	if err := CanResearch(tr["radar"], s); err != nil {
		t.Fatalf("a defence company researching radar: %v", err)
	}
	// An unrestricted technology needs no clearance at all.
	if err := CanResearch(tr["batteries"], Standing{CompanyType: "factory"}); err != nil {
		t.Fatalf("batteries with no buyer: %v", err)
	}
}

func TestTiers(t *testing.T) {
	tiers := Tiers(stagingTree())
	want := map[string]int{"semiconductors": 1, "microchips": 2, "batteries": 1, "radar": 1, "aesa": 2}
	for code, tier := range want {
		if tiers[code] != tier {
			t.Errorf("%s: tier %d, want %d", code, tiers[code], tier)
		}
	}
	if got := TopTier(tiers, []string{"batteries", "microchips"}); got != 2 {
		t.Errorf("top tier %d, want 2", got)
	}
	if got := TopTier(tiers, nil); got != 0 {
		t.Errorf("top tier of nothing %d", got)
	}
	got := ByTier(tiers, []string{"microchips", "batteries", "aesa", "semiconductors"})
	if got[0] != "batteries" || got[1] != "semiconductors" || got[2] != "microchips" || got[3] != "aesa" {
		t.Errorf("by tier: %v", got)
	}
}
