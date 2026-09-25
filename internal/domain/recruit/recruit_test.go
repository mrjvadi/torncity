package recruit

import (
	"testing"
	"time"
)

func TestCapacityScalesWithPopulationEducationAndLevel(t *testing.T) {
	if got := Capacity(50_000, 400, 10_000, 1_500); got != 30 {
		t.Fatalf("Capacity = %d, want 30 (200 engineers, 15%% at the level)", got)
	}
	if got := Capacity(50_000, 400, 5_000, 1_500); got != 15 {
		t.Fatalf("Capacity at half the education = %d, want 15", got)
	}
	if got := Capacity(0, 400, 10_000, 1_500); got != 0 {
		t.Fatalf("Capacity with nobody = %d, want 0", got)
	}
}

func TestRefillCountsWholeTicksOnTheGameClock(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	r := Regen{Capacity: 20, PerGameDayBPS: 1_000, Scale: 60} // 2 a game day; a game day is 24 real minutes
	if p := r.Refill(Pool{}, now); p.Available != 20 || !p.RefilledAt.Equal(now) {
		t.Fatalf("a pool never seen = %+v, want it full", p)
	}
	p := Pool{Available: 5, RefilledAt: now}
	if got := r.Refill(p, now.Add(11*time.Minute)); got.Available != 5 {
		t.Fatalf("before a tick = %d, want 5", got.Available)
	}
	got := r.Refill(p, now.Add(25*time.Minute))
	if got.Available != 7 || !got.RefilledAt.Equal(now.Add(24*time.Minute)) {
		t.Fatalf("after two ticks = %+v, want 7 with the leftover minute kept", got)
	}
	if got := r.Refill(p, now.Add(1000*time.Hour)); got.Available != 20 {
		t.Fatalf("long after = %d, want the capacity", got.Available)
	}
	if got := (Regen{Capacity: 0, PerGameDayBPS: 1000, Scale: 60}).Refill(p, now); got.Available != 0 {
		t.Fatalf("no capacity = %d, want 0", got.Available)
	}
}

func TestScarcityRaisesExpectationsAsAPoolEmpties(t *testing.T) {
	for _, c := range []struct{ avail, capacity, want int64 }{
		{10, 10, 10_000}, {5, 10, 12_500}, {0, 10, 15_000}, {0, 0, 15_000},
	} {
		if got := ScarcityBPS(5_000, c.avail, c.capacity); got != c.want {
			t.Errorf("ScarcityBPS(%d/%d) = %d, want %d", c.avail, c.capacity, got, c.want)
		}
	}
}

func TestExpectedFollowsLevelCostOfLivingAndScarcity(t *testing.T) {
	w := Wage{Base: 400, PerLevel: 200}
	if got := Expected(w, 3, 10_000, 10_000); got != 800 {
		t.Fatalf("Expected level 3 = %d, want 800", got)
	}
	if got := Expected(w, 3, 15_000, 12_000); got != 1_440 {
		t.Fatalf("Expected in a dear city from a thin pool = %d, want 1440", got)
	}
}

var plain = Preference{SalaryBPS: 10_000, HousingBPS: 10_000, SigningBPS: 10_000, EquityBPS: 10_000, MoveBPS: 10_000}

func TestValueSpreadsOneOffPartsOverTheTerm(t *testing.T) {
	o := Offer{Salary: 1000, Housing: 200, Signing: 1000, Relocation: 300, Term: 10, Equity: 500}
	// 1000 + 200 + 1000/10 + 500/10 − (500−300)/10 = 1330.
	if got := Value(o, plain, 500); got != 1_330 {
		t.Fatalf("Value = %d, want 1330", got)
	}
	secure := plain
	secure.TermPerPeriodBPS, secure.TermCapBPS = 100, 500
	if got := Value(o, secure, 500); got != 1_330+66 {
		t.Fatalf("Value to one who wants security = %d, want %d", got, 1_330+66)
	}
	if got := Value(Offer{Salary: 1, Term: 1}, plain, 10_000); got != 0 {
		t.Fatalf("an offer that leaves the move unpaid = %d, want 0", got)
	}
}

func TestCurveRisesFromFloorToFull(t *testing.T) {
	c := Curve{FloorBPS: 8_000, FullBPS: 12_000, MaxChanceBPS: 8_000}
	for _, x := range []struct{ value, want int64 }{{700, 0}, {800, 0}, {1_000, 4_000}, {1_200, 8_000}, {5_000, 8_000}} {
		if got := c.Chance(x.value, 1_000); got != x.want {
			t.Errorf("Chance(%d/1000) = %d, want %d", x.value, got, x.want)
		}
	}
	if (Curve{FloorBPS: 5, FullBPS: 5, MaxChanceBPS: 1}).Validate() == nil {
		t.Fatal("a flat curve validated")
	}
}

func TestMoveAndReputation(t *testing.T) {
	m := Move{Base: 500, PerKM: 2, Abroad: 1_000}
	if m.Cost(true, true, 300) != 0 || m.Cost(false, true, 300) != 1_100 || m.Cost(false, false, 300) != 2_100 {
		t.Fatal("move costs are wrong")
	}
	if got := Reputation(8_000, 400, 3, 100, 1_000, 25); got != 10_200 {
		t.Fatalf("Reputation = %d, want 10200", got)
	}
}

func TestPickAndRollAreDeterministic(t *testing.T) {
	w := []int64{4_000, 0, 6_000}
	if Pick(w, 0) != 0 || Pick(w, 3_999) != 0 || Pick(w, 4_000) != 2 || Pick(w, 9_999) != 2 {
		t.Fatal("Pick does not follow the weights")
	}
	if Roll("seed", 1, 2) != Roll("seed", 1, 2) || Roll("seed", 1, 2) == Roll("seed", 2, 1) {
		t.Fatal("Roll is not a function of its seed and position")
	}
}

func TestSettleUnpaidUnderpaidAndTerm(t *testing.T) {
	r := StaffRules{UnderpaidBPS: 9_000, UnderpaidPeriods: 2, UnpaidPeriods: 2}
	s := Standing{Due: 1_000, AcceptedBPS: 10_000, Term: 3}
	out := Settle(s, r, true, 1_000)
	if !out.Pay || out.Leave != "" || out.Underpaid || out.Next.Served != 1 {
		t.Fatalf("a paid, fairly paid period = %+v", out)
	}
	out = Settle(out.Next, r, false, 1_000)
	if out.Pay || out.Next.UnpaidRun != 1 || out.Leave != "" {
		t.Fatalf("one unpaid period = %+v", out)
	}
	if out = Settle(out.Next, r, false, 1_000); out.Leave != LeaveUnpaid {
		t.Fatalf("two unpaid periods = %+v, want them gone", out)
	}
	// The market rises a third: underpaid twice, poached.
	out = Settle(s, r, true, 1_334)
	if !out.Underpaid || out.Leave != "" {
		t.Fatalf("underpaid once = %+v", out)
	}
	if out = Settle(out.Next, r, true, 1_334); out.Leave != LeavePoached {
		t.Fatalf("underpaid twice = %+v, want poached", out)
	}
	// Completed at the third period, gone at the fourth unless renewed.
	s.Served = 2
	out = Settle(s, r, true, 1_000)
	if !out.Completed || !out.Next.Expiring || out.Leave != "" {
		t.Fatalf("the last period of the contract = %+v", out)
	}
	if left := Settle(out.Next, r, true, 1_000); left.Leave != LeaveContractEnd {
		t.Fatalf("unrenewed = %+v, want contract_end", left)
	}
	renewed := Renew(out.Next, 5, 1_200)
	if renewed.Expiring || renewed.Served != 0 || renewed.Term != 5 || renewed.Due != 1_200 {
		t.Fatalf("Renew = %+v, want a fresh term at the market", renewed)
	}
	if kept := Renew(out.Next, 5, 800); kept.Due != 1_000 {
		t.Fatalf("Renew in a falling market = %d, want the pay kept", kept.Due)
	}
}
