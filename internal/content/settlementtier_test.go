package content

import (
	"errors"
	"testing"
)

func TestShippedSettlementTiers(t *testing.T) {
	pack := shippedPack(t)
	if len(pack.SettlementTiers) != 2 {
		t.Fatalf("%d tier steps shipped, want village->town and town->city", len(pack.SettlementTiers))
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if d, ok := snap.SettlementTierStep("village"); !ok || d.Code != "town" || d.Residents < 1 {
		t.Fatalf("village step = %+v, %v", d, ok)
	}
	if _, ok := snap.SettlementTierStep("city"); ok {
		t.Fatal("the top of the ladder has a step")
	}
}

func TestSettlementTierValidation(t *testing.T) {
	base := shippedPack(t)
	with := func(steps ...SettlementTierDef) error {
		p := *base
		p.SettlementTiers = steps
		return p.Validate()
	}
	good := SettlementTierDef{Code: "town", From: "village", Residents: 5}
	if err := with(good); err != nil {
		t.Fatalf("a good step is refused: %v", err)
	}
	for name, step := range map[string]SettlementTierDef{
		"asks for nothing":   {Code: "town", From: "village"},
		"skips a tier":       {Code: "city", From: "village", Residents: 5},
		"literacy too high":  {Code: "town", From: "village", LiteracyBPS: 10001},
		"unreachable role":   {Code: "town", From: "village", Roles: []SettlementTierRoleDef{{Role: "food", Tier: 9}}},
		"unknown role":       {Code: "town", From: "village", Roles: []SettlementTierRoleDef{{Role: "nope", Tier: 1}}},
		"negative residents": {Code: "town", From: "village", Residents: -1, Buildings: 1},
	} {
		if err := with(step); !errors.Is(err, ErrInvalidSettlementTierContent) {
			t.Errorf("%s: err = %v, want ErrInvalidSettlementTierContent", name, err)
		}
	}
	if err := with(SettlementTierDef{Code: "city", From: "town", Residents: 5}); !errors.Is(err, ErrInvalidSettlementTierContent) {
		t.Errorf("town->city without village->town: err = %v", err)
	}
}
