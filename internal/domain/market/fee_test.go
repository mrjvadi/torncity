package market

import (
	"errors"
	"math"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func tradeOf(notional int64) Trade {
	return Trade{Quantity: 1, UnitPrice: money.FromMinor(notional), Notional: money.FromMinor(notional)}
}

func TestFee(t *testing.T) {
	tests := []struct {
		name     string
		notional int64
		bps      int64
		want     int64
	}{
		{"zero rate is free", 1_000_000, 0, 0},
		{"zero notional is free", 0, 250, 0},
		{"exact: 1% of 10000", 10_000, 100, 100},
		{"one minor unit at one bp rounds up to 1", 1, 1, 1},
		{"just under a whole unit rounds up", 9_999, 1, 1},
		{"exactly one unit", 10_000, 1, 1},
		{"one past a whole unit rounds up", 10_001, 1, 2},
		{"2.5% of 99 = 2.475 rounds up", 99, 250, 3},
		{"33.33% of 3 = 0.9999 rounds up", 3, 3333, 1},
		{"full rate takes the whole notional", 12_345, MaxFeeBps, 12_345},
		{"full rate at the int64 ceiling", math.MaxInt64, MaxFeeBps, math.MaxInt64},
		{"near-full rate at the int64 ceiling", math.MaxInt64, MaxFeeBps - 1, 9_222_449_699_651_090_330},
		{"one bp at the int64 ceiling", math.MaxInt64, 1, 922_337_203_685_478},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Fee(tradeOf(tt.notional), tt.bps)
			if err != nil {
				t.Fatalf("Fee: %v", err)
			}
			if got.Minor() != tt.want {
				t.Errorf("Fee(%d, %d bps) = %d, want %d", tt.notional, tt.bps, got.Minor(), tt.want)
			}
		})
	}
}

func TestFeeRejectsRates(t *testing.T) {
	for _, bps := range []int64{-1, MaxFeeBps + 1, math.MaxInt64} {
		if _, err := Fee(tradeOf(100), bps); !errors.Is(err, ErrInvalidFeeRate) {
			t.Errorf("Fee at %d bps = %v, want ErrInvalidFeeRate", bps, err)
		}
		if _, err := Settle(tradeOf(100), bps); !errors.Is(err, ErrInvalidFeeRate) {
			t.Errorf("Settle at %d bps = %v, want ErrInvalidFeeRate", bps, err)
		}
	}
}

// TestFeeAgainstExactCeiling compares the split arithmetic with the textbook
// ceil(n × bps / 10000) wherever the textbook form does not overflow, and
// checks the fee never exceeds the notional.
func TestFeeAgainstExactCeiling(t *testing.T) {
	notionals := []int64{0, 1, 2, 3, 7, 99, 101, 9_999, 10_000, 10_001, 123_457, 1 << 40}
	for _, n := range notionals {
		for bps := int64(0); bps <= MaxFeeBps; bps += 37 {
			got, err := Fee(tradeOf(n), bps)
			if err != nil {
				t.Fatal(err)
			}
			want := (n*bps + MaxFeeBps - 1) / MaxFeeBps
			if got.Minor() != want || got.Minor() > n {
				t.Fatalf("Fee(%d, %d) = %d, want %d (<= notional)", n, bps, got.Minor(), want)
			}
		}
	}
}

func TestSettleConservesMoney(t *testing.T) {
	tests := []struct {
		notional, bps int64
		seller, fee   int64
	}{
		{10_000, 100, 9_900, 100},
		{99, 250, 96, 3},
		{1, 1, 0, 1},
		{0, 500, 0, 0},
		{math.MaxInt64, MaxFeeBps, 0, math.MaxInt64},
		{math.MaxInt64, 0, math.MaxInt64, 0},
	}
	for _, tt := range tests {
		s, err := Settle(tradeOf(tt.notional), tt.bps)
		if err != nil {
			t.Fatalf("Settle(%d, %d): %v", tt.notional, tt.bps, err)
		}
		if s.BuyerPays.Minor() != tt.notional || s.SellerReceives.Minor() != tt.seller || s.Fee.Minor() != tt.fee {
			t.Errorf("Settle(%d, %d) = %+v, want pays %d, receives %d, fee %d",
				tt.notional, tt.bps, s, tt.notional, tt.seller, tt.fee)
		}
	}
}

func TestSettlementCheck(t *testing.T) {
	bad := Settlement{BuyerPays: money.FromMinor(100), SellerReceives: money.FromMinor(90), Fee: money.FromMinor(5)}
	if err := bad.check(); !errors.Is(err, ErrInvariant) {
		t.Errorf("unbalanced settlement check = %v, want ErrInvariant", err)
	}
	neg := Settlement{BuyerPays: money.FromMinor(100), SellerReceives: money.FromMinor(110), Fee: money.FromMinor(-10)}
	if err := neg.check(); !errors.Is(err, ErrInvariant) {
		t.Errorf("negative-fee settlement check = %v, want ErrInvariant", err)
	}
}
