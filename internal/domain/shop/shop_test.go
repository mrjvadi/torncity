package shop

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func TestPriceMovesWithDemandAndIsBounded(t *testing.T) {
	d := Demand{Window: time.Hour, FreeSales: 2, StepBPS: 500, MaxBPS: 15000}
	base := money.FromMinor(100)
	for recent, want := range map[int]int64{0: 100, 2: 100, 3: 105, 12: 150, 50: 150} {
		got, err := Price(base, d, recent)
		if err != nil || got.Minor() != want {
			t.Errorf("Price after %d sales = %s, %v; want %d", recent, got, err, want)
		}
	}
	if got, _ := Price(base, Demand{}, 99); got.Minor() != 100 {
		t.Errorf("no demand model moved the price to %s", got)
	}
	if err := (Demand{Window: time.Hour, MaxBPS: 9000}).Validate(); err == nil {
		t.Error("a ceiling under 100% is accepted")
	}
}

func TestTaxAndBuyback(t *testing.T) {
	tax, _ := SalesTax(money.FromMinor(999), 250)
	if tax.Minor() != 24 {
		t.Errorf("tax = %s, want 24 (rounded down)", tax)
	}
	back, _ := Buyback(money.FromMinor(333), 4000)
	if back.Minor() != 133 {
		t.Errorf("buyback = %s, want 133", back)
	}
	if _, err := Total(money.FromMinor(1<<62), 4); !errors.Is(err, ErrOverflow) {
		t.Errorf("an overflowing total = %v", err)
	}
}

func TestRestock(t *testing.T) {
	r := Restock{Every: time.Hour, Amount: 5, Max: 20}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	s := r.Refill(Shelf{}, now, 60)
	if s.Stock != 20 {
		t.Fatalf("a new shelf = %+v, want full", s)
	}
	s, err := s.Take(18)
	if err != nil || s.Stock != 2 {
		t.Fatalf("Take = %+v, %v", s, err)
	}
	// A game hour at scale 60 is a real minute: 150 seconds is two ticks.
	s = r.Refill(s, now.Add(150*time.Second), 60)
	if s.Stock != 12 || !s.RestockedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("refilled = %+v", s)
	}
	if _, err := s.Take(13); !errors.Is(err, ErrOutOfStock) {
		t.Fatalf("over-take = %v", err)
	}
	if next := r.NextRestock(s, 60); !next.Equal(now.Add(3 * time.Minute)) {
		t.Errorf("next restock %s", next)
	}
}
