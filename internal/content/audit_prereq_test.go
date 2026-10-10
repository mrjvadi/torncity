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
