package fx

import "testing"

func TestRefPriceAndBand(t *testing.T) {
	// 10 units per SUP at charter, x = 1.00: a unit is worth 0.1 SUP = 100000 micro-SUP
	if got := RefPrice(1_000_000, 10); got != 100_000 {
		t.Errorf("ref price %d, want 100000", got)
	}
	// a weaker unit (x 0.80) is worth 0.08 SUP
	if got := RefPrice(800_000, 10); got != 80_000 {
		t.Errorf("ref price %d, want 80000", got)
	}
	if XFromPrice(80_000, 10) != 800_000 {
		t.Error("x from price")
	}
	lo, hi := Band(100_000, 2000)
	if lo != 80_000 || hi != 120_000 {
		t.Errorf("a 20%% band around 100000: %d..%d", lo, hi)
	}
	if !InBand(120_000, 100_000, 2000) || InBand(120_001, 100_000, 2000) || InBand(79_999, 100_000, 2000) {
		t.Error("the band is closed at both ends and no wider")
	}
	if lo, _ := Band(1, 5000); lo != 1 {
		t.Errorf("the band never reaches zero: %d", lo)
	}
}

// Whatever the fills, a buy order never pays more than its escrow and the pieces add up to the whole.
func TestFillsAddUpAndStayInsideTheEscrow(t *testing.T) {
	const price, fee = 83_333, 30
	qtys := []int64{7, 1, 1, 13, 250, 3, 999, 1, 40}
	var total int64
	for _, q := range qtys {
		total += q
	}
	escrow := BuyEscrow(total, price, fee)
	var cum, sup, supFee, sold int64
	for _, q := range qtys {
		f, ok := FillOf(q, price, cum, fee, sold, 40)
		if !ok {
			t.Fatal("fill")
		}
		cum += f.Notional
		sup += f.SUP
		supFee += f.SUPFee
		sold += q
		if sup+supFee > escrow {
			t.Fatalf("paid %d of an escrow of %d", sup+supFee, escrow)
		}
	}
	if sup != SUPOf(cum) || sup != cum/PriceScale {
		t.Errorf("the pieces (%d) are the floor of the whole (%d)", sup, cum/PriceScale)
	}
	if supFee != FeeOf(sup, fee) {
		t.Errorf("the fee pieces (%d) are the fee of the whole (%d)", supFee, FeeOf(sup, fee))
	}
	var unitsFee int64
	var before int64
	for _, q := range qtys {
		f, _ := FillOf(q, price, 0, fee, before, 40)
		unitsFee += f.UnitsFee
		before += q
	}
	if unitsFee != FeeOf(total, 40) {
		t.Errorf("the units fee pieces (%d) are the fee of the whole (%d)", unitsFee, FeeOf(total, 40))
	}
}

func TestNextRefWaitsForTheTradesAndMovesGradually(t *testing.T) {
	// fewer fills than the minimum: where it is
	if got, moved := NextRef(1_000_000, []int64{800_000}, 1_000_000, 7, 8, 7); moved || got != 1_000_000 {
		t.Errorf("under the minimum the reference stays: %d %v", got, moved)
	}
	// 8 fills in one period at 0.80 against a window of 7: a seventh of the gap
	got, moved := NextRef(1_000_000, []int64{800_000}, 1_000_000, 8, 8, 7)
	if !moved || got != 971_429 {
		t.Errorf("one period of trading moves a seventh of the gap: %d %v", got, moved)
	}
	// seven periods at 0.80: all the way
	seven := []int64{800_000, 800_000, 800_000, 800_000, 800_000, 800_000, 800_000}
	if got, _ := NextRef(971_429, seven, 1_000_000, 50, 8, 7); got != 800_000 {
		t.Errorf("a full window at 0.80 is 0.80: %d", got)
	}
	// nothing moves for a value that is not positive
	if got, moved := NextRef(1_000_000, []int64{0}, 1_000_000, 50, 8, 7); moved || got != 1_000_000 {
		t.Error("a zero reading is ignored")
	}
}

func TestVWAPPrice(t *testing.T) {
	if VWAPPrice(0, 0) != 0 {
		t.Error("no volume, no price")
	}
	// 10 units at 100000 and 30 at 80000
	if got := VWAPPrice(40, 10*100_000+30*80_000); got != 85_000 {
		t.Errorf("vwap %d, want 85000", got)
	}
}
