package technology

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

var vocab = Vocabulary{
	CompanyTypes: item.NewSet("factory", "mine"),
	Skills:       item.NewSet("engineering", "medicine"),
}

func tree() []Tech {
	return []Tech{
		{Code: "semiconductors", Cost: 100, Time: time.Hour, CompanyTypes: []string{"factory"}, Skill: "engineering", Level: 1},
		{Code: "microchips", Requires: []string{"semiconductors"}, Cost: 200, Time: 2 * time.Hour,
			CompanyTypes: []string{"factory"}, Skill: "engineering", Level: 3},
		{Code: "batteries", Cost: 50, Time: time.Hour, CompanyTypes: []string{"factory"}},
	}
}

func TestValidateTree(t *testing.T) {
	if err := ValidateTree(tree(), vocab); err != nil {
		t.Fatalf("the sample tree: %v", err)
	}
	bad := []struct {
		name  string
		techs []Tech
		want  error
	}{
		{"unknown prerequisite", []Tech{{Code: "a", Requires: []string{"b"}, Time: time.Hour, CompanyTypes: []string{"factory"}}}, ErrUnknownPrerequisite},
		{"self", []Tech{{Code: "a", Requires: []string{"a"}, Time: time.Hour, CompanyTypes: []string{"factory"}}}, ErrCycle},
		{"cycle", []Tech{
			{Code: "a", Requires: []string{"b"}, Time: time.Hour, CompanyTypes: []string{"factory"}},
			{Code: "b", Requires: []string{"c"}, Time: time.Hour, CompanyTypes: []string{"factory"}},
			{Code: "c", Requires: []string{"a"}, Time: time.Hour, CompanyTypes: []string{"factory"}},
		}, ErrCycle},
		{"no time", []Tech{{Code: "a", CompanyTypes: []string{"factory"}}}, ErrInvalidTech},
		{"no company type", []Tech{{Code: "a", Time: time.Hour}}, ErrInvalidTech},
		{"unknown company type", []Tech{{Code: "a", Time: time.Hour, CompanyTypes: []string{"bakery"}}}, ErrInvalidTech},
		{"unknown skill", []Tech{{Code: "a", Time: time.Hour, CompanyTypes: []string{"factory"}, Skill: "alchemy"}}, ErrInvalidTech},
		{"level without skill", []Tech{{Code: "a", Time: time.Hour, CompanyTypes: []string{"factory"}, Level: 3}}, ErrInvalidTech},
		{"twice", []Tech{{Code: "a", Time: time.Hour, CompanyTypes: []string{"factory"}}, {Code: "a", Time: time.Hour, CompanyTypes: []string{"factory"}}}, ErrInvalidTech},
		{"negative cost", []Tech{{Code: "a", Cost: -1, Time: time.Hour, CompanyTypes: []string{"factory"}}}, ErrInvalidTech},
	}
	for _, tt := range bad {
		if err := ValidateTree(tt.techs, vocab); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestCanResearch(t *testing.T) {
	techs := tree()
	level := func(n int) func(string) int { return func(string) int { return n } }
	base := Standing{CompanyType: "factory", Owned: item.NewSet(), Published: item.NewSet(), SkillLevel: level(5)}

	if err := CanResearch(techs[0], base); err != nil {
		t.Fatalf("a root technology: %v", err)
	}
	if err := CanResearch(techs[1], base); !errors.Is(err, ErrPrerequisiteMissing) {
		t.Errorf("without its prerequisite: %v", err)
	}
	owned := base
	owned.Owned = item.NewSet("semiconductors")
	if err := CanResearch(techs[1], owned); err != nil {
		t.Errorf("with its prerequisite owned: %v", err)
	}
	published := base
	published.Published = item.NewSet("semiconductors")
	if err := CanResearch(techs[1], published); err != nil {
		t.Errorf("with its prerequisite published by someone else: %v", err)
	}
	if err := CanResearch(techs[0], owned); !errors.Is(err, ErrAlreadyOwned) {
		t.Errorf("owned already: %v", err)
	}
	mine := base
	mine.CompanyType = "mine"
	if err := CanResearch(techs[0], mine); !errors.Is(err, ErrWrongCompanyType) {
		t.Errorf("wrong kind of company: %v", err)
	}
	novice := owned
	novice.SkillLevel = level(2)
	if err := CanResearch(techs[1], novice); !errors.Is(err, ErrSkillTooLow) {
		t.Errorf("skill too low: %v", err)
	}
	nobody := base
	nobody.SkillLevel = nil
	if err := CanResearch(techs[2], nobody); err != nil {
		t.Errorf("a technology that needs no skill: %v", err)
	}
	busy := base
	busy.Researching = true
	if err := CanResearch(techs[0], busy); !errors.Is(err, ErrBusy) {
		t.Errorf("another research running: %v", err)
	}
}

func TestChangeMode(t *testing.T) {
	cases := []struct {
		from, to Mode
		price    int64
		want     error
	}{
		{Private, Licensed, 500, nil},
		{Licensed, Private, 0, nil},
		{Private, Published, 0, nil},
		{Licensed, Published, 0, nil},
		{Published, Private, 0, ErrPublishedForever},
		{Published, Licensed, 500, ErrPublishedForever},
		{Private, Licensed, 0, ErrInvalidPrice},
		{Private, Licensed, 10_001, ErrInvalidPrice},
		{Private, "shared", 0, ErrUnknownMode},
	}
	for _, c := range cases {
		if err := ChangeMode(c.from, c.to, c.price, 10_000); !errors.Is(err, c.want) {
			t.Errorf("%s → %s at %d: %v, want %v", c.from, c.to, c.price, err, c.want)
		}
	}
}

func TestCheckLicense(t *testing.T) {
	none := Access(nil, nil, nil)
	offer := Offer{OwnerID: "A", Mode: Licensed, Price: 900}
	if err := CheckLicense("B", offer, none, "microchips"); err != nil {
		t.Fatalf("a licensed technology: %v", err)
	}
	if err := CheckLicense("A", offer, none, "microchips"); !errors.Is(err, ErrOwnLicense) {
		t.Errorf("from itself: %v", err)
	}
	if err := CheckLicense("B", Offer{OwnerID: "A", Mode: Private}, none, "microchips"); !errors.Is(err, ErrNotForSale) {
		t.Errorf("private: %v", err)
	}
	held := Access(nil, nil, []string{"microchips"})
	if err := CheckLicense("B", offer, held, "microchips"); !errors.Is(err, ErrNoNeed) {
		t.Errorf("licensed already: %v", err)
	}
	public := Access(nil, []string{"microchips"}, nil)
	if err := CheckLicense("B", offer, public, "microchips"); !errors.Is(err, ErrNoNeed) {
		t.Errorf("published: %v", err)
	}
}

// Reverse engineering never adds to what a company may build on: the copy
// it yields has the original's bill of materials, and the access built from
// ownership, publication and licenses answers CanManufacture exactly as
// before the copy.
func TestReverseEngineeringGrantsNoTechnology(t *testing.T) {
	chip := item.Component{Code: "chipset", Category: "circuit", RequiresTechnology: []string{"microchips"}}
	a := item.Archetype{Code: "device", Method: item.MethodAssemble,
		Slots:      []item.Slot{{Name: "board", Accepts: "circuit", Quantity: item.Fixed(1)}},
		Attributes: []item.Attribute{{Name: "quality", Aggregate: item.AggregateWeighted, FromAll: true}},
		Value:      item.ValueDerived, Cost: item.CostItemized, Durability: item.DurabilityDurable,
		ReverseDifficulty: 10, ReverseSkill: "engineering"}
	original := item.Design{Archetype: "device", Origin: item.OriginAuthored,
		Fills: map[string]item.Fill{"board": {Component: "chipset", Quantity: 1}}}
	access := Access([]string{"batteries"}, nil, nil)
	before := item.CanManufacture(chip, access)
	out, err := item.ReverseEngineer(a, original, item.Engineer{Skill: "engineering", Level: 90}, 0)
	if err != nil || !out.Succeeded {
		t.Fatalf("reverse engineering: %+v, %v", out, err)
	}
	if err := item.ValidateStructure(a, out.Design, item.Components{"chipset": chip}); err != nil {
		t.Fatalf("the copy is not a producible design: %v", err)
	}
	after := item.CanManufacture(chip, access)
	if !errors.Is(before, item.ErrTechnologyLocked) || !errors.Is(after, item.ErrTechnologyLocked) {
		t.Fatalf("access before %v, after %v; the copy must not unlock the chipset", before, after)
	}
	if err := item.ValidateDesign(a, out.Design, item.Components{"chipset": chip}, access); !errors.Is(err, item.ErrTechnologyLocked) {
		t.Errorf("authoring the copy's parts without the technology: %v", err)
	}
}

func TestCleared(t *testing.T) {
	player := Buyer{Kind: "player"}
	civilian := Buyer{Kind: "company", Sector: "civilian"}
	defence := Buyer{Kind: "company", Sector: "defence"}
	if err := Cleared(Control{}, player); err != nil {
		t.Errorf("an unrestricted good to a player: %v", err)
	}
	if err := Cleared(Control{Restricted: true}, defence); !errors.Is(err, ErrNotCleared) {
		t.Errorf("restricted to nobody yet: %v", err)
	}
	restricted := Control{Restricted: true, BuyerClasses: []string{"company:defence", "state"}}
	if err := Cleared(restricted, defence); err != nil {
		t.Errorf("a defence company: %v", err)
	}
	for _, b := range []Buyer{player, civilian, {Kind: "company"}} {
		if err := Cleared(restricted, b); !errors.Is(err, ErrNotCleared) {
			t.Errorf("%+v: %v, want ErrNotCleared", b, err)
		}
	}
	if err := Cleared(restricted, Buyer{Kind: "state"}); err != nil {
		t.Errorf("a state, a kind that does not exist yet, as data: %v", err)
	}
}
