package moneyvalue

import (
	"errors"
	"testing"
)

// The worked example of ADR 0046 7.4: village TAL, r0 = 10, x_ref = 0.80,
// nil_unit_sup = 100. One TAL is 0.08 SUP and 0.0008 Nil; 1000 TAL is 0.8 Nil.
func TestTheWorkedExampleOfTheADR(t *testing.T) {
	xref, r0 := int64(800_000), int64(10*Micro)
	sup, err := SupPerUnit(xref, r0)
	if err != nil || sup != 80_000 {
		t.Fatalf("sup per TAL = %d (%v), want 0.08", sup, err)
	}
	q, err := NilPerUnit(xref, r0, 100)
	if err != nil || q != 800 { // 0.0008 Nil = 800 micro-Nil
		t.Fatalf("nil per TAL = %d (%v), want 800 micro-Nil", q, err)
	}
	if got, err := NilOf(1000, q); err != nil || got != 800_000 { // 0.8 Nil
		t.Fatalf("1000 TAL = %d micro-Nil (%v), want 800000", got, err)
	}
	// A Nil-priced thing of 3 Nil, in the village's money: 3 / 0.0008 = 3750 TAL.
	if got, err := UnitsOfNil(3*Micro, q); err != nil || got != 3750 {
		t.Fatalf("3 Nil = %d TAL (%v), want 3750", got, err)
	}
}

func TestTheNeutralCurrencyIsQuotedByTheConstant(t *testing.T) {
	q, err := NeutralNilPerUnit(100)
	if err != nil || q != 10_000 { // 1 SUP = 0.01 Nil
		t.Fatalf("1 SUP = %d micro-Nil (%v), want 10000", q, err)
	}
	if got, _ := NilOf(100, q); got != Micro {
		t.Fatalf("100 SUP = %d micro-Nil, want exactly 1 Nil", got)
	}
}

func TestNothingIsQuotedWithoutARate(t *testing.T) {
	for name, f := range map[string]func() error{
		"no market rate":   func() error { _, err := NilPerUnit(0, Micro, 100); return err },
		"no charter rate":  func() error { _, err := NilPerUnit(Micro, 0, 100); return err },
		"no Nil constant":  func() error { _, err := NilPerUnit(Micro, Micro, 0); return err },
		"no Nil to divide": func() error { _, err := UnitsOfNil(Micro, 0); return err },
	} {
		if err := f(); !errors.Is(err, ErrNoRate) {
			t.Errorf("%s: %v, want ErrNoRate", name, err)
		}
	}
	if _, err := NilOf(1<<62, 1<<40); !errors.Is(err, ErrOverflow) {
		t.Errorf("an amount past 64 bits: %v", err)
	}
}

func TestAPriceInNilIsNeverRoundedInTheBuyersFavour(t *testing.T) {
	// 1 micro-Nil more than a whole number of units costs one unit more.
	q := int64(800)
	if got, _ := UnitsOfNil(801, q); got != 2 {
		t.Errorf("801 micro-Nil at 800 per unit = %d units, want 2", got)
	}
	if got, _ := UnitsOfNil(800, q); got != 1 {
		t.Errorf("800 micro-Nil = %d units, want 1", got)
	}
	if got, _ := UnitsOfNil(0, q); got != 0 {
		t.Errorf("0 Nil = %d units", got)
	}
}

func TestTheBasketReadsThePricesAgainstTheReference(t *testing.T) {
	lines := []BasketLine{
		{WeightMilli: 1400, Ref: 35, Price: 38}, // rice
		{WeightMilli: 560, Ref: 45, Price: 49},  // tea
		{WeightMilli: 700, Ref: 40, Price: 0},   // cloth: not on the shelf today
	}
	r := ReadBasket(lines)
	wantRef := int64(1400*35 + 560*45)
	wantCost := int64(1400*38 + 560*49)
	if r.RefCost != wantRef || r.Cost != wantCost {
		t.Fatalf("costs %d/%d, want %d/%d", r.RefCost, r.Cost, wantRef, wantCost)
	}
	if r.IndexBPS != wantCost*BPS/wantRef || r.IndexBPS <= BPS {
		t.Errorf("index %d: prices over the reference must read over 10000", r.IndexBPS)
	}
	// The cloth is 28000 of 28000+74200... the cover is the shelf's share at reference prices.
	total := wantRef + 700*40
	if r.CoverBPS != wantRef*BPS/total {
		t.Errorf("cover %d, want %d", r.CoverBPS, wantRef*BPS/total)
	}
	if r := ReadBasket(nil); r.IndexBPS != 0 || r.CoverBPS != 0 {
		t.Errorf("an empty basket reads %+v", r)
	}
	if r := ReadBasket([]BasketLine{{WeightMilli: 100, Ref: 10}}); r.IndexBPS != 0 || r.CoverBPS != 0 {
		t.Errorf("a basket with nothing on the shelf has no index: %+v", r)
	}
}

func TestPurchasingPowerAndTheGap(t *testing.T) {
	// A basket that costs 100 in SUP and 1000 in a currency with charter rate 10:
	// the fair rate is 100 x 10 / 1000 = 1.0; a market at 0.8 is 20 % under it.
	ppp, err := PPPRate(100, 10*Micro, 1000)
	if err != nil || ppp != Micro {
		t.Fatalf("ppp = %s (%v), want 1.000000", String(ppp), err)
	}
	gap, err := GapBPS(800_000, ppp)
	if err != nil || gap != -2000 {
		t.Fatalf("gap = %d (%v), want -2000", gap, err)
	}
	if _, err := PPPRate(0, Micro, 5); !errors.Is(err, ErrNoRate) {
		t.Error("a basket with no cost gave a rate")
	}
	if _, err := GapBPS(0, Micro); !errors.Is(err, ErrNoRate) {
		t.Error("no market rate gave a gap")
	}
}
