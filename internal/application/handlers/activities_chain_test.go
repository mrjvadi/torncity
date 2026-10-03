package handlers

import (
	"testing"
)

// TestProgrammingNeedsTheWholeChain: a settlement with a school and a large workshop but no computing research is
// offered neither the programming course, the technology career nor the programming skill; with the chain
// researched it is, by the tier answer and by the capabilities (owner 2026-10-03).
func TestProgrammingNeedsTheWholeChain(t *testing.T) {
	snap := shippedSnapshot(t)
	build := append(append([]string{}, foundingKit...), "teaching_circle", "school", "carpentry_workshop", "manufactory")
	base := append(append([]string{}, foundingGrants...), "basic_literacy", "record_keeping", "state_school", "temple_school", "smithing", "smithing_ii", "carpentry", "masonry")
	chain := append(append([]string{}, base...), "basic_mathematics", "applied_mechanics", "electricity", "computing", "computing_ii")
	without := SweepSettlement{ID: "s-a", Code: "a", Tier: "city", Standing: seededStanding(build, base)}
	with := SweepSettlement{ID: "s-b", Code: "b", Tier: "city", Standing: seededStanding(build, chain)}
	for _, e := range [][2]string{
		{"course", "programming_fundamentals"}, {"course", "software_engineering"}, {"career", "technology"}, {"skill", "programming"},
		{"course", "automotive_repair"},
	} {
		r := rowOf(t, Sweep(snap, without, 10000), e[0], e[1])
		if r.TierAnswer || r.CapabilityAnswer {
			t.Errorf("%s/%s offered without the research: tier=%v capabilities=%v (missing %s)", e[0], e[1], r.TierAnswer, r.CapabilityAnswer, r.Missing)
		}
	}
	for _, e := range [][2]string{{"course", "programming_fundamentals"}, {"career", "technology"}, {"skill", "programming"}} {
		r := rowOf(t, Sweep(snap, with, 10000), e[0], e[1])
		if !r.CapabilityAnswer {
			t.Errorf("%s/%s not offered with the chain researched (missing %s)", e[0], e[1], r.Missing)
		}
	}
}

// TestLooseBuildingsNeedTheirTradition: a bank, a barracks, a port and an airport are no longer open to a fresh city.
func TestLooseBuildingsNeedTheirTradition(t *testing.T) {
	snap := shippedSnapshot(t)
	fresh := SweepSettlement{ID: "s-f", Code: "f", Tier: "village", Standing: seededStanding(foundingKit, foundingGrants)}
	rows := Sweep(snap, fresh, 10000)
	for _, code := range []string{"bank", "barracks", "port", "airport", "school"} {
		r := rowOf(t, rows, "building", code)
		if r.CapabilityAnswer {
			t.Errorf("building/%s is offered to a new city by the capabilities", code)
		}
	}
}
