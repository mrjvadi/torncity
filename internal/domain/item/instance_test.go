package item

import (
	"errors"
	"testing"
)

// TestNewInstanceProvenance is the phone scenario's invariant: an instance
// exists only as the output of a production order or as a recorded reward.
func TestNewInstanceProvenance(t *testing.T) {
	phone := phoneArchetype()
	tests := []struct {
		name    string
		a       Archetype
		serial  string
		design  string
		quality int
		prov    Provenance
		want    error
	}{
		{"produced", phone, "SN-1", "nil-mobile-x1", 70, Provenance{ProductionOrderID: "po-1"}, nil},
		{"rewarded", phone, "SN-2", "", 70, Provenance{RewardGrantID: "rg-1"}, nil},
		{"rewarded with a design", phone, "SN-3", "nil-mobile-x1", 70, Provenance{RewardGrantID: "rg-1"}, nil},
		{"sold by the NPC economy", phone, "SN-10", "", 60, Provenance{SupplyID: "sale-1"}, nil},
		{"a crime's loot", phone, "SN-11", "", 40, Provenance{LootID: "crime-1"}, nil},
		{"supply and loot", phone, "SN-12", "", 40, Provenance{SupplyID: "sale-1", LootID: "crime-1"}, ErrAmbiguousProvenance},
		{"from nowhere", phone, "SN-4", "nil-mobile-x1", 70, Provenance{}, ErrNoProvenance},
		{"from both", phone, "SN-5", "nil-mobile-x1", 70, Provenance{ProductionOrderID: "po-1", RewardGrantID: "rg-1"}, ErrAmbiguousProvenance},
		{"produced without a design", phone, "SN-6", "", 70, Provenance{ProductionOrderID: "po-1"}, ErrMissingDesign},
		{"a service", Archetype{Code: "surgery", Method: MethodServe}, "SN-7", "", 70, Provenance{RewardGrantID: "rg-1"}, ErrNotAGood},
		{"no serial", phone, "", "nil-mobile-x1", 70, Provenance{ProductionOrderID: "po-1"}, ErrEmptyCode},
		{"quality below zero", phone, "SN-8", "nil-mobile-x1", -1, Provenance{ProductionOrderID: "po-1"}, ErrInvalidQuality},
		{"quality above max", phone, "SN-9", "nil-mobile-x1", MaxQuality + 1, Provenance{ProductionOrderID: "po-1"}, ErrInvalidQuality},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst, err := NewInstance(tc.a, tc.serial, tc.design, tc.quality, tc.prov)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if err == nil && (inst.Serial != tc.serial || inst.Archetype != tc.a.Code || inst.Provenance != tc.prov) {
				t.Errorf("instance %+v does not match its inputs", inst)
			}
		})
	}
}
