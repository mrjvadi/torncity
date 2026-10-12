package project

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

func stage() Stage { return Stage{Materials: map[string]int64{"stone": 10, "timber": 4}, LabourHours: 8, MinHours: 6} }

func TestAStageWaitsForMaterialsThenCrewThenTime(t *testing.T) {
	s := stage()
	if got := Check(s, map[string]int64{"stone": 10}, 8, t0, time.Time{}, t0.Add(10*time.Hour)); got != WaitMaterials {
		t.Fatalf("no timber: %q", got)
	}
	st := map[string]int64{"stone": 10, "timber": 4}
	if got := Check(s, st, 7, t0, time.Time{}, t0.Add(10*time.Hour)); got != WaitCrew {
		t.Fatalf("7 of 8 hours: %q", got)
	}
	if got := Check(s, st, 8, t0, time.Time{}, t0.Add(5*time.Hour)); got != WaitTime {
		t.Fatalf("too soon: %q", got)
	}
	if got := Check(s, st, 8, t0, t0.Add(20*time.Hour), t0.Add(10*time.Hour)); got != WaitDelay {
		t.Fatalf("delayed: %q", got)
	}
	if got := Check(s, st, 8, t0, time.Time{}, t0.Add(10*time.Hour)); got != Ready {
		t.Fatalf("all met: %q", got)
	}
}

func TestMissingNamesOnlyWhatIsLacking(t *testing.T) {
	m := Missing(stage(), map[string]int64{"stone": 6, "timber": 4})
	if len(m) != 1 || m["stone"] != 4 {
		t.Fatalf("missing = %v", m)
	}
}

func TestTakeUsesTheMaterialsUpAndKeepsTheSurplus(t *testing.T) {
	left := Take(stage(), map[string]int64{"stone": 13, "timber": 4, "clay": 2})
	if left["stone"] != 3 || left["timber"] != 0 || left["clay"] != 2 {
		t.Fatalf("left = %v", left)
	}
}

func TestRefundsAddUpToTheFundExactly(t *testing.T) {
	ps := []Pledge{{"a", 1000}, {"b", 333}, {"c", 333}}
	for _, fund := range []int64{1666, 900, 1, 100000} {
		r := Refunds(fund, ps)
		var sum int64
		for _, v := range r {
			sum += v
		}
		if sum != fund {
			t.Fatalf("fund %d: pieces add to %d (%v)", fund, sum, r)
		}
	}
	if r := Refunds(0, ps); len(r) != 0 {
		t.Fatalf("an empty fund refunds %v", r)
	}
}

func TestRefundsAreTheSameWhateverTheOrder(t *testing.T) {
	a := Refunds(1001, []Pledge{{"x", 500}, {"y", 500}, {"z", 500}})
	b := Refunds(1001, []Pledge{{"z", 500}, {"y", 500}, {"x", 500}})
	for k, v := range a {
		if b[k] != v {
			t.Fatalf("order changed the split: %v vs %v", a, b)
		}
	}
}

func TestAPledgeIsRefundableOnlyBeforeTheFirstStageStarts(t *testing.T) {
	if !Refundable(1, false) || Refundable(1, true) || Refundable(2, false) {
		t.Fatal("refundable only in stage 1 before it has started")
	}
}

func TestACancelIsOpenOnlyBeforeTheLimitStage(t *testing.T) {
	if !CancelOpen(1, 2) || CancelOpen(2, 2) || CancelOpen(3, 2) {
		t.Fatal("cancel before stage 2 only")
	}
}

func TestNPCHoursAreCappedByTheDayAndByWhatTheStageStillAsks(t *testing.T) {
	if NPCHours(8, 3, 100) != 5 || NPCHours(8, 8, 100) != 0 || NPCHours(8, 0, 2) != 2 || NPCHours(8, 0, 0) != 0 {
		t.Fatal("npc hours")
	}
}

func TestValidateRefusesAnEmptyKindAndAStageThatAsksNothing(t *testing.T) {
	if Validate(nil) == nil || Validate([]Stage{{}}) == nil || Validate([]Stage{stage()}) != nil {
		t.Fatal("validate")
	}
}
