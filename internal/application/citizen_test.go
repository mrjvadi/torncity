package application

import (
	"strings"
	"testing"
)

func TestPropertyTaxDue(t *testing.T) {
	for _, c := range []struct {
		assessed int64
		bps      int
		want     int64
	}{
		{1245, 200, 24},
		{400, 200, 8},
		{10, 200, 1}, // worth something, taxed at least one unit
		{0, 200, 0},
		{1245, 0, 0},
	} {
		if got := PropertyTaxDue(c.assessed, c.bps); got != c.want {
			t.Errorf("PropertyTaxDue(%d, %d) = %d, want %d", c.assessed, c.bps, got, c.want)
		}
	}
}

func TestAssessOwnersCountsLotsAndStandingBuildings(t *testing.T) {
	lots := []SettlementLot{
		{OwnerID: "a", Price: 400}, {OwnerID: "a", Price: 400}, {OwnerID: "b", Price: 250},
	}
	buildings := []PrivateBuilding{
		{BuildingID: "h1", OwnerID: "a", AssessedValue: 845},
		{BuildingID: "h2", OwnerID: "b", AssessedValue: 500}, // demolished
	}
	got := AssessOwners(lots, buildings, func(id string) bool { return id != "h2" })
	want := map[string]int64{"a": 400 + 400 + 845, "b": 250}
	if len(got) != 2 {
		t.Fatalf("assessments: %+v", got)
	}
	for _, a := range got {
		if a.Value != want[a.PlayerID] {
			t.Errorf("%s assessed %d, want %d", a.PlayerID, a.Value, want[a.PlayerID])
		}
	}
}

func TestCitizenBoundsClampTheHeadsLevers(t *testing.T) {
	b := CitizenBounds{LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000, TaxBPS: 200, TaxBPSMax: 500}
	if p, f, x := b.Effective(LotTerms{}); p != 400 || f != 100 || x != 200 {
		t.Errorf("defaults: %d %d %d", p, f, x)
	}
	if p, f, x := b.Effective(LotTerms{LotPrice: 9999, PermitFee: 5000, HasPermit: true, TaxBPS: 900, HasTax: true}); p != 5000 || f != 1000 || x != 500 {
		t.Errorf("above the bounds: %d %d %d", p, f, x)
	}
	// A head may waive the permit and the tax entirely.
	if p, f, x := b.Effective(LotTerms{PermitFee: 0, HasPermit: true, TaxBPS: 0, HasTax: true}); p != 400 || f != 0 || x != 0 {
		t.Errorf("waived: %d %d %d", p, f, x)
	}
}

func TestTenureMarkMovesWithOwnership(t *testing.T) {
	if TenureMark(nil, nil) != "" {
		t.Error("a village without private property must keep the version it always had")
	}
	a := TenureMark([]SettlementLot{{X: 1, Y: 2, Tenure: TenureFreehold, OwnerID: "p1"}}, nil)
	b := TenureMark([]SettlementLot{{X: 1, Y: 2, Tenure: TenureFreehold, OwnerID: "p2"}}, nil)
	c := TenureMark([]SettlementLot{{X: 1, Y: 2, Tenure: TenureFreehold, OwnerID: "p1"}}, []PrivateBuilding{{BuildingID: "h", OwnerID: "p1"}})
	if a == "" || a == b || a == c {
		t.Errorf("the mark does not follow who owns what: %q %q %q", a, b, c)
	}
	// Order does not matter.
	l1, l2 := SettlementLot{X: 1, Tenure: TenureFreehold, OwnerID: "p1"}, SettlementLot{X: 2, Tenure: TenureFreehold, OwnerID: "p2"}
	if TenureMark([]SettlementLot{l1, l2}, nil) != TenureMark([]SettlementLot{l2, l1}, nil) {
		t.Error("the mark depends on row order")
	}
}

func TestLayoutVersionsWithTenureLeaveThePublicOneAlone(t *testing.T) {
	rows := []SettlementBuildingInstance{{ID: "b1", TypeCode: "road", Status: "complete"}}
	fp := func(string, bool) (int, int) { return 1, 1 }
	plain := LayoutVersionsOf("s", "village", "N", 5, rows, fp)
	owned := LayoutVersionsWithTenure("s", "village", "N", 5, rows, fp, TenureMark([]SettlementLot{{OwnerID: "p"}}, nil))
	if plain.Public != owned.Public {
		t.Error("ownership is not shown to strangers: the public version must not move")
	}
	if plain.Member == owned.Member || plain.Head == owned.Head {
		t.Error("a lot changing hands must move the member and head versions")
	}
	none := LayoutVersionsWithTenure("s", "village", "N", 5, rows, fp, "")
	if none != plain {
		t.Error("an empty mark must keep the old versions")
	}
}

// Residency alone pays nothing (docs/adr/0033 section 4.4): no ledger reason
// of the citizen loop credits a player, and none is named like a stipend.
func TestNoResidencyFaucetInTheReasonTable(t *testing.T) {
	for _, r := range Reasons() {
		name := string(r)
		for _, bad := range []string{"residen", "stipend", "citizen_grant", "citizen_income", "homestead"} {
			if strings.Contains(name, bad) {
				t.Errorf("reason %q looks like a residency faucet", name)
			}
		}
	}
	for _, r := range []Reason{ReasonSettlementLotSale, ReasonSettlementPermitFee, ReasonCitizenConstruction, ReasonCitizenMaterials, ReasonSettlementPropertyTax} {
		if !r.Known() {
			t.Errorf("%s is missing from the closed set", r)
		}
	}
}
