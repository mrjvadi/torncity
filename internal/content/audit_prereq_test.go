package content

import "testing"

// The prerequisite audit (docs/adr/0061) finds a staff role nobody fills and a placeable building that only adds a
// percentage, and stops finding them when the caller names the code that does.
func TestPrerequisiteAuditFindsWhatNobodyFills(t *testing.T) {
	p := shippedPack(t)
	find := func(o AuditOptions, kind, code string) bool {
		for _, f := range p.Audit(o) {
			if f.Check == AuditPrereq && f.Kind == kind && f.Code == code {
				return true
			}
		}
		return false
	}
	bare := AuditOptions{FoundingGrant: 10_000, EarnPerDay: 840, HorizonDays: 30, WalkKm: 40, CartKm: 150}
	if !find(bare, "staff_role", "stall/stall_keeper") {
		t.Error("the stall's keeper has no filler: the audit must say so")
	}
	if !find(bare, "settlement_building", "park") {
		t.Error("the park only adds a percentage: the audit must say so")
	}
	named := bare
	named.Fillers = map[string]string{"stall_keeper": "an owner-hired keeper"}
	named.Runtimes = map[string]string{"park": "a place to rest"}
	if find(named, "staff_role", "stall/stall_keeper") || find(named, "settlement_building", "park") {
		t.Error("a role or a building whose filler or runtime is named is no finding")
	}
}

// A building that waits for its mechanic is gated out of the menus (ADR 0063): the snapshot says so, the reachability
// lint does not call it a dead end, and the audit lists it with what it waits for instead of calling it a stand-in.
func TestAGatedBuildingIsOutOfTheMenusAndNamed(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := snap.SettlementBuildingDef("bank")
	if !ok || !d.Gated() {
		t.Fatalf("the bank waits for the licence: %+v", d)
	}
	o := AuditOptions{FoundingGrant: 10_000, EarnPerDay: 840, HorizonDays: 30, WalkKm: 40, CartKm: 150}
	var named bool
	for _, f := range p.Audit(o) {
		if f.Check == AuditPrereq && f.Kind == "settlement_building" && f.Code == "bank" {
			named = true
		}
		if f.Check == AuditPrereq && f.Code == "bank/teller" {
			t.Error("a gated building's staff are not a finding: nobody can place it")
		}
	}
	if !named {
		t.Error("the audit lists the gated building with what it waits for")
	}
}
