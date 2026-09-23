package market

import (
	"errors"
	"math"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func TestReserve(t *testing.T) {
	tests := []struct {
		name  string
		order Order
		money int64
		qty   int64
	}{
		{"limit buy reserves quantity × limit", ord("b", Buy, 10, 105, "bob", 0), 1_050, 0},
		{"partly filled buy reserves the remainder", filled(ord("b", Buy, 10, 105, "bob", 0), 4), 630, 0},
		{"market buy reserves at its bound", mkt(ord("b", Buy, 3, 1_000, "bob", 0)), 3_000, 0},
		{"filled buy reserves nothing", filled(ord("b", Buy, 10, 105, "bob", 0), 10), 0, 0},
		{"sell reserves units, no money", ord("s", Sell, 10, 105, "alice", 0), 0, 10},
		{"partly filled sell reserves the remainder", filled(ord("s", Sell, 10, 105, "alice", 0), 7), 0, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Reserve(tt.order)
			if err != nil {
				t.Fatalf("Reserve: %v", err)
			}
			if r.Money.Minor() != tt.money || r.Quantity != tt.qty {
				t.Errorf("Reserve = %+v, want money %d qty %d", r, tt.money, tt.qty)
			}
		})
	}

	if _, err := Reserve(ord("b", Buy, math.MaxInt64, 2, "bob", 0)); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("Reserve past int64 = %v, want overflow", err)
	}
}

// TestReleaseOnFill checks the escrow identity on the sweep from TestMatch:
// before = after + consumed + released for each side of each trade.
func TestReleaseOnFill(t *testing.T) {
	b := book(nil, []Order{
		ord("s1", Sell, 2, 100, "alice", 0),
		ord("s2", Sell, 3, 101, "carol", 0),
		ord("s3", Sell, 5, 103, "dave", 0),
	})
	buy := ord("b1", Buy, 8, 103, "bob", 1)
	res, err := Match(b, buy, at(2))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}

	start, _ := Reserve(buy)
	var consumed, released int64
	for _, tr := range res.Trades {
		fe, err := ReleaseOnFill(buy, tr)
		if err != nil {
			t.Fatalf("ReleaseOnFill: %v", err)
		}
		consumed += fe.Consumed.Money.Minor()
		released += fe.Released.Money.Minor()
	}
	end, _ := Reserve(res.Incoming)
	// 2×100 + 3×101 + 3×103 = 812 paid; improvement 2×3 + 3×2 + 0 = 12.
	if consumed != 812 || released != 12 || end.Money.Minor() != 0 {
		t.Errorf("consumed %d released %d left %d, want 812, 12, 0", consumed, released, end.Money.Minor())
	}
	if start.Money.Minor() != consumed+released+end.Money.Minor() {
		t.Errorf("escrow not conserved: %d != %d + %d + %d", start.Money.Minor(), consumed, released, end.Money.Minor())
	}

	sellEscrow, err := ReleaseOnFill(b.Asks[0], res.Trades[0])
	if err != nil {
		t.Fatalf("ReleaseOnFill(sell): %v", err)
	}
	if sellEscrow.Consumed.Quantity != 2 || sellEscrow.Released != (Reservation{}) {
		t.Errorf("sell escrow = %+v, want 2 units consumed, nothing released", sellEscrow)
	}

	if _, err := ReleaseOnFill(b.Asks[1], res.Trades[0]); !errors.Is(err, ErrNotParty) {
		t.Errorf("ReleaseOnFill for a bystander = %v, want ErrNotParty", err)
	}
	if _, err := ReleaseOnFill(ord("b1", Sell, 8, 103, "bob", 1), res.Trades[0]); !errors.Is(err, ErrNotParty) {
		t.Errorf("ReleaseOnFill with the wrong side = %v, want ErrNotParty", err)
	}
	worse := res.Trades[0]
	worse.UnitPrice = money.FromMinor(200)
	if _, err := ReleaseOnFill(buy, worse); !errors.Is(err, ErrInvariant) {
		t.Errorf("ReleaseOnFill above the bound = %v, want ErrInvariant", err)
	}
}
