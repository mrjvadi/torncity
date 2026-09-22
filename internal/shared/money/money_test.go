package money

import (
	"errors"
	"math"
	"testing"
)

func TestAddSub(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
		want int64
		op   string
	}{
		{"add positive", 100, 250, 350, "add"},
		{"add negative", 100, -250, -150, "add"},
		{"add zero", 0, 0, 0, "add"},
		{"sub basic", 500, 200, 300, "sub"},
		{"sub into negative", 100, 400, -300, "sub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := FromMinor(tt.a), FromMinor(tt.b)
			var got Amount
			var err error
			if tt.op == "add" {
				got, err = a.Add(b)
			} else {
				got, err = a.Sub(b)
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Minor() != tt.want {
				t.Errorf("got %d, want %d", got.Minor(), tt.want)
			}
		})
	}
}

func TestOverflow(t *testing.T) {
	if _, err := FromMinor(math.MaxInt64).Add(FromMinor(1)); !errors.Is(err, ErrOverflow) {
		t.Errorf("expected overflow on MaxInt64+1, got %v", err)
	}
	if _, err := FromMinor(math.MinInt64).Sub(FromMinor(1)); !errors.Is(err, ErrOverflow) {
		t.Errorf("expected overflow on MinInt64-1, got %v", err)
	}
	if _, err := FromMinor(0).Sub(FromMinor(math.MinInt64)); !errors.Is(err, ErrOverflow) {
		t.Errorf("expected overflow negating MinInt64, got %v", err)
	}
}

// TestSumZero is the shape of the ledger invariant: the entries of one
// transaction must sum to exactly zero.
func TestSumZero(t *testing.T) {
	entries := []Amount{FromMinor(-1000), FromMinor(900), FromMinor(100)}
	total, err := Sum(entries...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !total.IsZero() {
		t.Errorf("balanced transaction summed to %d, want 0", total.Minor())
	}
}

func TestSumDetectsImbalance(t *testing.T) {
	entries := []Amount{FromMinor(-1000), FromMinor(900)}
	total, _ := Sum(entries...)
	if total.IsZero() {
		t.Error("unbalanced transaction reported as balanced")
	}
}

func TestNegativeIsRepresentable(t *testing.T) {
	if !FromMinor(-1).IsNegative() {
		t.Error("expected -1 to report as negative")
	}
}
