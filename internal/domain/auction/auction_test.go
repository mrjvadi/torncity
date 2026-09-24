package auction

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func terms() Terms {
	return Terms{Reserve: money.FromMinor(1000), StepBPS: 500, MinStep: money.FromMinor(10),
		OpensAt: t0, EndsAt: t0.Add(10 * time.Minute)}
}

func TestBidding(t *testing.T) {
	tr := terms()
	high := Bid{}
	if _, err := PlaceBid(tr, "seller", high, "a", money.FromMinor(999), t0); !errors.Is(err, ErrBidTooLow) {
		t.Fatalf("under the reserve = %v", err)
	}
	high, err := PlaceBid(tr, "seller", high, "a", money.FromMinor(1000), t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.MinNext(high).Minor(); got != 1050 {
		t.Fatalf("next = %d, want 1050", got)
	}
	if _, err := PlaceBid(tr, "seller", high, "a", money.FromMinor(2000), t0); !errors.Is(err, ErrAlreadyWinning) {
		t.Fatalf("raising oneself = %v", err)
	}
	if _, err := PlaceBid(tr, "seller", high, "seller", money.FromMinor(2000), t0); !errors.Is(err, ErrOwnAuction) {
		t.Fatalf("the seller bidding = %v", err)
	}
	high, err = PlaceBid(tr, "seller", high, "b", money.FromMinor(1050), t0.Add(time.Minute))
	if err != nil || high.Bidder != "b" {
		t.Fatalf("outbid = %+v, %v", high, err)
	}
	if _, err := PlaceBid(tr, "seller", high, "a", money.FromMinor(5000), tr.EndsAt); !errors.Is(err, ErrClosed) {
		t.Fatalf("a bid at the close = %v", err)
	}
	if r, err := Close(tr, high, tr.EndsAt); err != nil || r != Sold {
		t.Fatalf("Close = %s, %v", r, err)
	}
	if r, _ := Close(tr, Bid{}, tr.EndsAt); r != Unsold {
		t.Fatalf("no bids closes %s", r)
	}
	if _, err := Close(tr, high, t0); err == nil {
		t.Fatal("an early close is accepted")
	}
}

func TestFee(t *testing.T) {
	if got := Fee(money.FromMinor(1001), 500).Minor(); got != 51 {
		t.Errorf("fee = %d, want 51 (rounded up)", got)
	}
	if got := Fee(money.FromMinor(3), 10000).Minor(); got != 3 {
		t.Errorf("a full fee = %d", got)
	}
}

func TestTermsValidate(t *testing.T) {
	l := Limits{MinDuration: time.Minute, MaxDuration: time.Hour, MaxReserve: money.FromMinor(1_000_000)}
	if err := terms().Validate(l); err != nil {
		t.Fatal(err)
	}
	bad := terms()
	bad.EndsAt = bad.OpensAt.Add(time.Second)
	if err := bad.Validate(l); !errors.Is(err, ErrInvalidAuction) {
		t.Fatalf("a too-short auction = %v", err)
	}
}
