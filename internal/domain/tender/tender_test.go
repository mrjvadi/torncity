package tender

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func TestLowestPriceWinsAndATieGoesToTheEarlierBidThenTheSmallerId(t *testing.T) {
	bids := []Bid{
		{Company: "c3", Price: 900, Days: 5, At: t0.Add(time.Hour)},
		{Company: "c2", Price: 800, Days: 9, At: t0.Add(2 * time.Hour)},
		{Company: "c1", Price: 800, Days: 9, At: t0.Add(2 * time.Hour)},
	}
	w, ok := Winner(LowestPrice, bids)
	if !ok || w.Company != "c1" {
		t.Fatalf("the lowest price, then the smaller id: %+v", w)
	}
	// the same bids in any order give the same winner: no replica can disagree
	rev := []Bid{bids[2], bids[1], bids[0]}
	if w2, _ := Winner(LowestPrice, rev); w2.Company != w.Company {
		t.Fatalf("order of bids changed the winner: %s vs %s", w2.Company, w.Company)
	}
	if _, ok := Winner(LowestPrice, nil); ok {
		t.Fatal("no bids, no winner")
	}
}

func TestScoredWeighsTheTimeAndTheRecord(t *testing.T) {
	bids := []Bid{
		{Company: "cheap-slow", Price: 1000, Days: 30, Record: 0, At: t0},
		{Company: "dear-fast", Price: 1100, Days: 5, Record: 20, At: t0},
	}
	if w, _ := Winner(Scored, bids); w.Company != "dear-fast" {
		t.Fatalf("a quick bid with a record beats a cheap slow one: %s", w.Company)
	}
	if w, _ := Winner(LowestPrice, bids); w.Company != "cheap-slow" {
		t.Fatalf("lowest price ignores both: %s", w.Company)
	}
}

func TestABidNeedsItsBondAndStaysUnderTheCap(t *testing.T) {
	if BondFor(1001, 1000) != 101 {
		t.Fatalf("the bond rounds up: %d", BondFor(1001, 1000))
	}
	good := Bid{Price: 1000, Days: 3, Bond: 100}
	if err := Check(good, 2000, 1000); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]Bid{"small bond": {Price: 1000, Days: 3, Bond: 99}, "no days": {Price: 1000, Bond: 100}, "over cap": {Price: 3000, Days: 3, Bond: 300}} {
		if err := Check(b, 2000, 1000); !errors.Is(err, ErrBid) {
			t.Errorf("%s must be refused: %v", name, err)
		}
	}
}

func TestConflictOfInterestRefusesTheOfficialsWhoOwnManageOrHoldAStake(t *testing.T) {
	c := Company{Owner: "owner", Managers: []string{"mgr"}, Stakes: map[string]int64{"holder": 50}}
	if !Conflicts([]string{"x", "owner"}, c, 1) || !Conflicts([]string{"mgr"}, c, 1) || !Conflicts([]string{"holder"}, c, 1) {
		t.Fatal("owner, manager and any stake must conflict at a threshold of one basis point")
	}
	if Conflicts([]string{"holder"}, c, 100) {
		t.Fatal("a stake under the threshold is allowed when the threshold is higher")
	}
	if Conflicts([]string{"stranger", ""}, c, 1) {
		t.Fatal("an official with no tie is free to award")
	}
}

func TestStagesPayInThirdsWithTheRetentionHeld(t *testing.T) {
	var paid, held int64
	for k := 1; k <= Stages; k++ {
		p, h := StagePay(1000, k, 500)
		paid, held = paid+p, held+h
	}
	if paid+held != 1000 {
		t.Fatalf("the stages must add up to the price: %d + %d", paid, held)
	}
	if held != 51 { // 333*5% and 334*5% each round up to 17
		t.Fatalf("retention rounds up per stage: %d", held)
	}
	if p, h := StagePay(1000, 4, 500); p != 0 || h != 0 {
		t.Fatal("there is no fourth stage")
	}
}

func TestStagesAreReachedByTheWorkDone(t *testing.T) {
	cases := map[int64]int{0: 0, 32: 0, 33: 0, 34: 1, 66: 1, 67: 2, 99: 2, 100: 3}
	for done, want := range cases {
		if got := StagesDone(done, 100); got != want {
			t.Errorf("done %d: stages %d, want %d", done, got, want)
		}
	}
}

func TestDeadlinesGraceAndDebarment(t *testing.T) {
	if Overdue(t0, 72*time.Hour, t0.Add(71*time.Hour)) || !Overdue(t0, 72*time.Hour, t0.Add(72*time.Hour)) {
		t.Fatal("overdue only after the grace")
	}
	if !Debarred(t0.Add(time.Hour), t0) || Debarred(t0, t0) || Debarred(time.Time{}, t0) {
		t.Fatal("debarment runs until its end")
	}
	if !RetentionReleaseAt(t0, 7).Equal(t0.AddDate(0, 0, 7)) {
		t.Fatal("the retention is held the defects period")
	}
}

func TestAWorkOfAnyStageCountPaysTheWholePriceAndHoldsTheRetention(t *testing.T) {
	for _, n := range []int{1, 3, 4, 5, 9} {
		for _, price := range []int64{1000, 1001, 999999} {
			var paid, held int64
			for k := 1; k <= n; k++ {
				p, h := StagePayN(price, k, n, 500)
				paid += p
				held += h
			}
			if paid+held != price {
				t.Fatalf("n=%d price=%d: paid %d + held %d != price", n, price, paid, held)
			}
		}
	}
	if StageShareN(1000, 0, 4) != 0 || StageShareN(1000, 5, 4) != 0 || StageShareN(0, 1, 4) != 0 || StageShareN(1000, 1, 0) != 0 {
		t.Fatal("a stage outside the work is worth nothing")
	}
	if StageShareN(900, 2, 3) != StageShare(900, 2) {
		t.Fatal("with three stages it is the ordinary tender")
	}
}
