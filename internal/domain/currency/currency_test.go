package currency

import "testing"

func TestRateConvertsBothWaysAndSaysHowItRounds(t *testing.T) {
	r := Rate{R0: 10, XRefPPM: PPM} // 10 units per SUP at charter
	if got := r.ToLocalFloor(8); got != 80 {
		t.Errorf("8 SUP = 80 units, got %d", got)
	}
	// a weaker unit (x_ref 0.80): 12.5 units per SUP
	w := Rate{R0: 10, XRefPPM: 800_000}
	if got := w.ToLocalFloor(3); got != 37 {
		t.Errorf("3 SUP at 12.5 units is 37.5: floor 37, got %d", got)
	}
	if got := w.ToLocalCeil(3); got != 38 {
		t.Errorf("a payer owes 38, got %d", got)
	}
	if got := w.ToLocalNearest(3); got != 38 {
		t.Errorf("a screen shows 38 (the half goes up), got %d", got)
	}
	if got := w.ToLocalNearest(1); got != 13 || w.ToLocalFloor(1) != 12 || w.ToLocalCeil(1) != 13 {
		t.Errorf("1 SUP = 12.5: floor 12, ceil 13, nearest 13")
	}
	if got := r.ToSUPFloor(95); got != 9 {
		t.Errorf("95 units are 9.5 SUP, floor 9, got %d", got)
	}
	if (Rate{}).ToLocalFloor(5) != 0 || (Rate{R0: 1}).Valid() {
		t.Error("an invalid rate converts nothing")
	}
	// a huge amount does not overflow the intermediate
	if got := (Rate{R0: 100, XRefPPM: PPM}).ToLocalFloor(1 << 50); got != 100*(1<<50) {
		t.Errorf("big conversion %d", got)
	}
}

func TestMintPricesADepositAtTheReferenceRate(t *testing.T) {
	// the ADR's worked example: r0 = 10, mint fee 0.5 percent, x_ref 1.00 -> 995 units for 100 SUP
	m, err := Mint(100, Rate{R0: 10, XRefPPM: PPM}, 50)
	if err != nil || m.Units != 995 || m.BasisSUP != 99 || m.FeeSUP != 1 {
		t.Errorf("100 SUP at r0 10: %+v %v", m, err)
	}
	// a weaker unit (x_ref 0.80) gives more units: 100 x 12.5 x 0.995 = 1243.75 -> 1243
	m, _ = Mint(100, Rate{R0: 10, XRefPPM: 800_000}, 50)
	if m.Units != 1243 {
		t.Errorf("weaker unit: %d", m.Units)
	}
	// Marco Polo's deposit
	m, _ = Mint(2602, Rate{R0: 10, XRefPPM: PPM}, 50)
	if m.Units != 25_890 && m.Units != 25_889 {
		t.Errorf("2602 SUP: %d", m.Units)
	}
	if _, err := Mint(10, Rate{}, 50); err == nil {
		t.Error("an invalid rate cannot mint")
	}
}

func TestPlanAutoNeverStripsTheTreasury(t *testing.T) {
	terms := Terms{Fee: 1000, MinDeposit: 5000, ShareBPS: 5000, Floor: 500}
	// founding with the grant of 10000: both paid in full
	if p := PlanAuto(10_000, terms, true); !p.OK || p.Fee != 1000 || p.Deposit != 5000 {
		t.Errorf("founding pays in full: %+v", p)
	}
	// Marco Polo: 6205 on the treasury, an existing city: fee 1000, deposit (6205-1000)/2 = 2602
	if p := PlanAuto(6205, terms, false); !p.OK || p.Fee != 1000 || p.Deposit != 2602 {
		t.Errorf("Marco Polo: %+v", p)
	}
	// a founding grant that does not cover both falls back to the share rule
	if p := PlanAuto(5500, terms, true); !p.OK || p.Deposit != 2250 {
		t.Errorf("a short founding grant: %+v", p)
	}
	// a rich existing treasury still deposits only the minimum
	if p := PlanAuto(100_000, terms, false); !p.OK || p.Deposit != 5000 {
		t.Errorf("rich: %+v", p)
	}
	// under the floor: skipped, the head keeps the offer
	if p := PlanAuto(1900, terms, false); p.OK {
		t.Errorf("1900 leaves a deposit of 450, under the floor of 500: %+v", p)
	}
	if p := PlanAuto(900, terms, false); p.OK {
		t.Errorf("a treasury below the fee: %+v", p)
	}
}

func TestRateOptions(t *testing.T) {
	if !ValidR0(10) || ValidR0(7) {
		t.Error("the options are 1, 10 and 100")
	}
}

func TestDeskQuotesKeepTheFeeAndRoundAgainstTheTaker(t *testing.T) {
	r := Rate{R0: 10, XRefPPM: PPM}
	// 100 SUP at 30 bps: fee ceil(0.3) = 1, 99 SUP x 10 = 990 units
	if u, f := DeskBuy(100, r, 30); u != 990 || f != 1 {
		t.Errorf("buy: %d units, fee %d", u, f)
	}
	// 1000 units at 30 bps: fee ceil(3) = 3 units, 997 units = 99.7 SUP, floor 99
	if s, f := DeskSell(1000, r, 30); s != 99 || f != 3 {
		t.Errorf("sell: %d SUP, fee %d units", s, f)
	}
	// buying and selling the same amount back never makes money
	units, _ := DeskBuy(1000, r, 30)
	back, _ := DeskSell(units, r, 30)
	if back >= 1000 {
		t.Errorf("a round trip must lose the fee: %d", back)
	}
	if u, _ := DeskBuy(0, r, 30); u != 0 {
		t.Error("nothing bought for nothing")
	}
	if !ValidDeskFee(30) || ValidDeskFee(9) || ValidDeskFee(301) {
		t.Error("the fee is 10 to 300 bps")
	}
}

// DeskBuyCost is the least SUP that buys at least the units asked for: one less would not.
func TestDeskBuyCostIsTheLeastPriceThatBuysTheUnits(t *testing.T) {
	for _, r := range []Rate{{R0: 10, XRefPPM: PPM}, {R0: 10, XRefPPM: 800_000}, {R0: 7, XRefPPM: 1_300_000}, {R0: 1, XRefPPM: PPM}} {
		for _, fee := range []int64{10, 30, 100, 300} {
			for _, units := range []int64{1, 2, 9, 10, 99, 1_000, 2_030, 123_457} {
				cost := DeskBuyCost(units, r, fee)
				if cost <= 0 {
					t.Fatalf("no price for %d units at %+v fee %d", units, r, fee)
				}
				if got, _ := DeskBuy(cost, r, fee); got < units {
					t.Errorf("%d SUP buys %d units, fewer than %d (rate %+v fee %d)", cost, got, units, r, fee)
				}
				if got, _ := DeskBuy(cost-1, r, fee); got >= units && cost > 1 {
					t.Errorf("%d SUP already buys %d units: %d is not the least price for %d (rate %+v fee %d)", cost-1, got, cost, units, r, fee)
				}
			}
		}
	}
	if DeskBuyCost(0, Rate{R0: 10, XRefPPM: PPM}, 30) != 0 || DeskBuyCost(5, Rate{}, 30) != 0 {
		t.Error("nothing is bought for no units or at no rate")
	}
}

// A burn takes its share of the basis, rounded down; burning everything takes all of it.
func TestBurnBasisIsTheBurntShare(t *testing.T) {
	if got := BurnBasis(1_000, 10_000, 500); got != 50 {
		t.Errorf("5%% of the supply is 5%% of the basis: %d", got)
	}
	if got := BurnBasis(999, 10_000, 1); got != 0 {
		t.Errorf("a share under one rounds down: %d", got)
	}
	if got := BurnBasis(1_000, 100, 100); got != 1_000 {
		t.Errorf("burning the whole supply takes the whole basis: %d", got)
	}
	if BurnBasis(0, 100, 10) != 0 || BurnBasis(100, 0, 10) != 0 || BurnBasis(100, 100, 0) != 0 {
		t.Error("nothing to take")
	}
}
