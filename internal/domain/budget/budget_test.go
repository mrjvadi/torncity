package budget

import "testing"

func rules() Rules {
	return Rules{SpendShareBPS: 2000, Lines: []Line{
		{Code: "police", Effect: EffectInvestigation, FullAt: 10000, MaxBPS: 2000},
		{Code: "transit", Effect: EffectTransitFare, FullAt: 5000, MaxBPS: 4000},
		{Code: "defence", Effect: EffectDefenceFund},
	}}
}

func TestSpendDividesTheBudgetByTheAllocation(t *testing.T) {
	r := rules()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	p := Spend(r, 100000, map[string]int64{"police": 5000, "transit": 2500, "defence": 1000})
	if p.Spendable != 20000 {
		t.Fatalf("spendable %d, want a fifth of the treasury", p.Spendable)
	}
	if p.Spent != 10000+5000+2000 || p.Defence != 2000 {
		t.Fatalf("spent %d (defence %d)", p.Spent, p.Defence)
	}
	if p.Lines[0].EffectBPS != 2000 || p.Lines[1].EffectBPS != 4000 || p.Lines[2].EffectBPS != 0 {
		t.Fatalf("effects %+v", p.Lines)
	}
	half := Spend(r, 25000, map[string]int64{"police": 10000})
	if half.Spent != 5000 || half.Lines[0].EffectBPS != 1000 {
		t.Fatalf("a part of full_at buys a part of the effect: %+v", half)
	}
	if none := Spend(r, 100000, nil); none.Spent != 0 {
		t.Fatalf("nothing allocated spent %d", none.Spent)
	}
	if empty := Spend(r, -5, map[string]int64{"police": 10000}); empty.Spent != 0 || empty.Treasury != 0 {
		t.Fatalf("an empty treasury spent %d", empty.Spent)
	}
}

func TestLowerAndRaise(t *testing.T) {
	if Lower(1000, 2500) != 750 || Lower(1000, 20000) != 0 || Lower(1000, -1) != 1000 {
		t.Fatal("Lower is wrong")
	}
	if Raise(1000, 1500) != 1150 || Raise(1000, -5) != 1000 {
		t.Fatal("Raise is wrong")
	}
}

func TestValidateRefuses(t *testing.T) {
	for _, r := range []Rules{
		{SpendShareBPS: 0, Lines: rules().Lines},
		{SpendShareBPS: 1000},
		{SpendShareBPS: 1000, Lines: []Line{{Code: "x", Effect: "fireworks", FullAt: 1, MaxBPS: 1}}},
		{SpendShareBPS: 1000, Lines: []Line{{Code: "d", Effect: EffectDefenceFund, FullAt: 1, MaxBPS: 1}}},
		{SpendShareBPS: 1000, Lines: []Line{{Code: "p", Effect: EffectInvestigation}}},
	} {
		if r.Validate() == nil {
			t.Errorf("%+v validated", r)
		}
	}
}
