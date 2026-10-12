package licence

import (
	"testing"
	"time"
)

var now = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

func rules() Rules {
	return Rules{Grace: 14 * 24 * time.Hour, AuctionMin: 24 * time.Hour, ReserveMin: 100, TransferFeeBPS: 500, TransferCooldown: 7 * 24 * time.Hour,
		MaxShareBPS: 4000, ShareCapFrom: 5, Notice: 7 * 24 * time.Hour, Nonuse: 30 * 24 * time.Hour, TermMax: 365 * 24 * time.Hour, ListingFee: 50}
}

func TestTheRequirementBindsOnlyAfterTheGrace(t *testing.T) {
	r := rules()
	q := Requirement{Kind: "stall_right", EffectiveAt: r.EffectiveFrom(now)}
	if q.InForce(now) || q.InForce(now.Add(13*24*time.Hour)) {
		t.Fatal("in the grace nobody needs it")
	}
	if !q.InForce(now.Add(14 * 24 * time.Hour)) {
		t.Fatal("after the grace it binds")
	}
}

func TestTheFirstGrantIsAtTheFeeOrTheReserve(t *testing.T) {
	if (Requirement{Mode: Fixed, Fee: 300, Reserve: 900}).Price() != 300 || (Requirement{Mode: Auction, Fee: 300, Reserve: 900}).Price() != 900 {
		t.Fatal("price")
	}
}

func TestAZeroOrTinyReserveIsRefused(t *testing.T) {
	r := rules()
	if r.ReserveOK(0) || r.ReserveOK(99) || !r.ReserveOK(100) {
		t.Fatal("reserve")
	}
}

func TestTheTermIsClampedAndAWindowMustBeLongEnough(t *testing.T) {
	r := rules()
	if d, ok := r.ValidTerm(1000 * 24 * time.Hour); !ok || d != 365*24*time.Hour {
		t.Fatal("term clamp")
	}
	if _, ok := r.ValidTerm(0); ok {
		t.Fatal("a zero term")
	}
	if r.WindowOK(now, now.Add(23*time.Hour)) || !r.WindowOK(now, now.Add(24*time.Hour)) {
		t.Fatal("window")
	}
}

func TestTheHighestSealedBidAtOrAboveTheReserveWins(t *testing.T) {
	bids := []Bid{{"b1", "x", 150, now}, {"b2", "y", 400, now.Add(time.Hour)}, {"b3", "z", 399, now}}
	w, ok := Winner(100, bids)
	if !ok || w.ID != "b2" {
		t.Fatalf("winner = %+v %v", w, ok)
	}
	if _, ok := Winner(500, bids); ok {
		t.Fatal("nothing reaches the reserve")
	}
}

func TestATieGoesToTheEarliestBidThenTheLowestId(t *testing.T) {
	bids := []Bid{{"b9", "x", 300, now.Add(time.Minute)}, {"b2", "y", 300, now}, {"b1", "z", 300, now}}
	w, _ := Winner(100, bids)
	if w.ID != "b1" {
		t.Fatalf("winner = %s", w.ID)
	}
	rev := []Bid{bids[2], bids[1], bids[0]}
	if w2, _ := Winner(100, rev); w2.ID != "b1" {
		t.Fatal("order must not matter")
	}
}

func TestTheTransferFeeAndTheSellersPartAddUpToThePrice(t *testing.T) {
	r := rules()
	for _, p := range []int64{1, 19, 20, 1000, 12345} {
		f, s := r.TransferFee(p)
		if f+s != p || f != p*500/10000 {
			t.Fatalf("price %d: fee %d seller %d", p, f, s)
		}
	}
	if f, s := r.TransferFee(0); f != 0 || s != 0 {
		t.Fatal("no price")
	}
}

func TestATransferWaitsForTheCooldown(t *testing.T) {
	r := rules()
	if !r.TransferOpen(time.Time{}, now) || r.TransferOpen(now.Add(-6*24*time.Hour), now) || !r.TransferOpen(now.Add(-7*24*time.Hour), now) {
		t.Fatal("cooldown")
	}
}

func TestNoHolderMayPassTheShareCapOnceTheKindHasEnoughLicences(t *testing.T) {
	r := rules()
	// a lone shop: 1 of 1 would be 100 percent, but the cap does not apply to a kind this small
	if r.OverShare(0, 0) || r.OverShare(1, 2) {
		t.Fatal("small kinds are not capped")
	}
	// ten licences, the holder has four: a fifth would be 5 of 11 = 45 percent
	if !r.OverShare(4, 10) {
		t.Fatal("five of eleven is over forty percent")
	}
	if r.OverShare(3, 10) {
		t.Fatal("four of eleven is 36 percent")
	}
	// exactly forty: 4 of 10 licences after the grant (3 held, 9 total)
	if r.OverShare(3, 9) {
		t.Fatal("exactly forty percent is allowed")
	}
}

func TestARevocationWithoutCauseRefundsTheUnusedPartPaidInProportion(t *testing.T) {
	start := now
	end := now.Add(100 * 24 * time.Hour)
	if Refund(1000, start, end, now) != 1000 {
		t.Fatal("unused whole term")
	}
	if got := Refund(1000, start, end, now.Add(25*24*time.Hour)); got != 750 {
		t.Fatalf("three quarters left = %d", got)
	}
	if Refund(1000, start, end, end) != 0 || Refund(1000, start, end, end.Add(time.Hour)) != 0 || Refund(0, start, end, now) != 0 {
		t.Fatal("nothing left or nothing paid")
	}
	if Refund(1000, start, end, now.Add(-time.Hour)) != 1000 {
		t.Fatal("not started yet refunds all")
	}
}

func TestNonUseCountsFromTheLaterOfTheGrantAndTheLastUse(t *testing.T) {
	r := rules()
	if r.Unused(now, nil, now.Add(29*24*time.Hour)) || !r.Unused(now, nil, now.Add(30*24*time.Hour)) {
		t.Fatal("from the grant")
	}
	last := now.Add(20 * 24 * time.Hour)
	if r.Unused(now, &last, now.Add(49*24*time.Hour)) || !r.Unused(now, &last, now.Add(50*24*time.Hour)) {
		t.Fatal("from the last use")
	}
}

func TestOnlyAKnownCauseRevokesWithCause(t *testing.T) {
	if !ValidCause(CauseUnpaid) || !ValidCause(CauseNonUse) || !ValidCause(CauseBreach) || ValidCause(CauseWithdrawn) || ValidCause("whim") {
		t.Fatal("causes")
	}
	if !rules().NoticeEnds(now).Equal(now.Add(7 * 24 * time.Hour)) {
		t.Fatal("notice")
	}
}
