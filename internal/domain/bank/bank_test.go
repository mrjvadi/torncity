package bank

import (
	"errors"
	"math"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func amt(n int64) money.Amount { return money.FromMinor(n) }

func TestFeeRoundsUp(t *testing.T) {
	cases := []struct {
		amount, bps, want int64
	}{
		{0, 100, 0},
		{1, 1, 1},         // any non-zero fee is at least one unit
		{10000, 100, 100}, // exact
		{10001, 100, 101}, // rounded up
		{999, 0, 0},       // no rate, no fee
		{12345, 10000, 12345},
		{math.MaxInt64, 10000, math.MaxInt64},
	}
	for _, c := range cases {
		got, err := Fee(amt(c.amount), c.bps)
		if err != nil {
			t.Fatalf("Fee(%d, %d): %v", c.amount, c.bps, err)
		}
		if got.Minor() != c.want {
			t.Errorf("Fee(%d, %d) = %d, want %d", c.amount, c.bps, got.Minor(), c.want)
		}
	}
	for _, bad := range []int64{-1, MaxFeeBps + 1} {
		if _, err := Fee(amt(100), bad); !errors.Is(err, ErrInvalidFeeRate) {
			t.Errorf("Fee at %d bps: %v, want ErrInvalidFeeRate", bad, err)
		}
	}
}

func TestQuoteChargesOnTop(t *testing.T) {
	q, err := QuoteFor(amt(1000), 250)
	if err != nil {
		t.Fatal(err)
	}
	if q.Amount.Minor() != 1000 || q.Fee.Minor() != 25 || q.Total.Minor() != 1025 {
		t.Errorf("quote = %+v", q)
	}
	if _, err := QuoteFor(amt(0), 0); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("zero amount: %v", err)
	}
	if _, err := QuoteFor(amt(math.MaxInt64), 1); !errors.Is(err, ErrOverflow) {
		t.Errorf("overflow: %v", err)
	}
}

func TestMaxAffordable(t *testing.T) {
	for _, bps := range []int64{0, 1, 99, 250, 5000, 10000} {
		for _, balance := range []int64{0, 1, 2, 7, 100, 1025, 99999, 123456789} {
			got, err := MaxAffordable(amt(balance), bps)
			if err != nil {
				t.Fatal(err)
			}
			if got.IsZero() {
				if balance > 0 {
					// Not even one unit fits: one unit plus its fee exceeds it.
					q, _ := QuoteFor(amt(1), bps)
					if q.Total.Minor() <= balance {
						t.Errorf("balance %d at %d bps: 0, but 1 fits", balance, bps)
					}
				}
				continue
			}
			q, _ := QuoteFor(got, bps)
			if q.Total.Minor() > balance {
				t.Errorf("balance %d at %d bps: %d costs %d", balance, bps, got.Minor(), q.Total.Minor())
			}
			next, _ := QuoteFor(amt(got.Minor()+1), bps)
			if next.Total.Minor() <= balance {
				t.Errorf("balance %d at %d bps: %d is not the maximum", balance, bps, got.Minor())
			}
		}
	}
}

func TestShareRoundsDown(t *testing.T) {
	if got := Share(amt(1001), 2500); got.Minor() != 250 {
		t.Errorf("25%% of 1001 = %d", got.Minor())
	}
	if got := Share(amt(1001), 10000); got.Minor() != 1001 {
		t.Errorf("100%% of 1001 = %d", got.Minor())
	}
	if got := Share(amt(math.MaxInt64), 5000); got.Minor() != math.MaxInt64/2 {
		t.Errorf("50%% of max = %d", got.Minor())
	}
	if got := Share(amt(0), 5000); !got.IsZero() {
		t.Errorf("share of nothing = %d", got.Minor())
	}
}

func TestLimits(t *testing.T) {
	if _, err := NewLimits(10, 5); !errors.Is(err, ErrInvalidLimits) {
		t.Errorf("min above max: %v", err)
	}
	if _, err := NewLimits(0, 5); !errors.Is(err, ErrInvalidLimits) {
		t.Errorf("zero min: %v", err)
	}
	l, err := NewLimits(10, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		n    int64
		want error
	}{
		{0, ErrInvalidAmount}, {-5, ErrInvalidAmount}, {9, ErrBelowMinimum},
		{10, nil}, {100, nil}, {101, ErrAboveMaximum},
	} {
		if err := l.Check(amt(c.n)); !errors.Is(err, c.want) && !(err == nil && c.want == nil) {
			t.Errorf("Check(%d) = %v, want %v", c.n, err, c.want)
		}
	}
	if got := l.Clamp(amt(500)); got.Minor() != 100 {
		t.Errorf("Clamp = %d", got.Minor())
	}
}

func TestParseAmount(t *testing.T) {
	good := map[string]int64{
		"5000":      5000,
		" 12,500 ":  12500,
		"۱۲۰۰":      1200,
		"٣٤":        34,
		"1_000_000": 1000000,
	}
	for in, want := range good {
		got, err := ParseAmount(in)
		if err != nil || got.Minor() != want {
			t.Errorf("ParseAmount(%q) = %d, %v; want %d", in, got.Minor(), err, want)
		}
	}
	for _, in := range []string{"", "0", "-5", "1.5", "abc", ",", "10k"} {
		if _, err := ParseAmount(in); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("ParseAmount(%q) = %v, want ErrInvalidAmount", in, err)
		}
	}
	if _, err := ParseAmount("99999999999999999999"); !errors.Is(err, ErrOverflow) {
		t.Errorf("huge amount: %v", err)
	}
}

func TestPayments(t *testing.T) {
	here := Presence{CityID: "c1"}
	there := Presence{CityID: "c2"}
	onTheRoad := Presence{CityID: "c1", Travelling: true}

	if err := CheckPayment("a", "a", MethodCard, here, here); !errors.Is(err, ErrSelfPayment) {
		t.Errorf("self payment: %v", err)
	}
	if err := CheckPayment("a", "b", MethodCash, here, here); err != nil {
		t.Errorf("cash face to face: %v", err)
	}
	for _, other := range []Presence{there, onTheRoad, {}} {
		if err := CheckPayment("a", "b", MethodCash, here, other); !errors.Is(err, ErrNotTogether) {
			t.Errorf("cash to %+v: %v", other, err)
		}
		if err := CheckPayment("a", "b", MethodCard, onTheRoad, other); err != nil {
			t.Errorf("card to %+v: %v", other, err)
		}
	}
	if err := CheckPayment("a", "b", Method("gold"), here, here); !errors.Is(err, ErrUnknownMethod) {
		t.Errorf("unknown method: %v", err)
	}
	if err := CheckAtBank(onTheRoad); !errors.Is(err, ErrNotInCity) {
		t.Errorf("bank on the road: %v", err)
	}
	if err := CheckAtBank(Presence{}); !errors.Is(err, ErrNotInCity) {
		t.Errorf("bank nowhere: %v", err)
	}
	if err := CheckAtBank(here); err != nil {
		t.Errorf("bank in a city: %v", err)
	}
}
