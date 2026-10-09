package trade

import "testing"

func rules() Rules { return Rules{PriceBPS: 9000, CapBase: 400, CapPerResident: 40} }

func TestUnitPriceIsBelowTheReferenceAndNeverFree(t *testing.T) {
	r := rules()
	for _, c := range []struct{ ref, want int64 }{{15, 13}, {3, 2}, {1, 1}, {0, 0}, {40, 36}} {
		if got := UnitPrice(c.ref, r); got != c.want {
			t.Errorf("UnitPrice(%d) = %d, want %d", c.ref, got, c.want)
		}
	}
	// a trader never pays more than the reference price, whatever the rules say
	if got := UnitPrice(10, Rules{PriceBPS: BPS, CapBase: 1}); got != 10 {
		t.Errorf("at 100 percent the trader pays the reference price: %d", got)
	}
}

func TestPlanSellsOnlyTheSurplusOfOrderedGoods(t *testing.T) {
	stock := map[string]int64{"timber": 39, "stone": 25, "wheat": 66, "wool": 25}
	ref := map[string]int64{"timber": 15, "stone": 6, "wheat": 3, "wool": 12}
	orders := []Order{{"timber", 30, true}, {"wheat", 0, false}, {"wool", 25, true}, {"gold", 0, true}}
	p := Make(stock, orders, ref, 2, rules())
	if len(p.Lines) != 1 || p.Lines[0].Item != "timber" || p.Lines[0].Qty != 9 || p.Lines[0].Unit != 13 || p.Gross != 117 {
		t.Errorf("the plan: %+v", p)
	}
	if p.Capped {
		t.Error("nothing hit the cap")
	}
}

func TestPlanStopsAtWhatOneVisitCarries(t *testing.T) {
	stock := map[string]int64{"timber": 1000, "stone": 1000}
	ref := map[string]int64{"timber": 15, "stone": 6}
	orders := []Order{{"stone", 0, true}, {"timber", 0, true}}
	p := Make(stock, orders, ref, 10, rules()) // cap 400 + 10*40 = 800
	if p.Cap != 800 || p.Gross > 800 || !p.Capped {
		t.Fatalf("the cap: %+v", p)
	}
	// items are served in code order: stone first (unit 5), timber gets the rest
	if p.Lines[0].Item != "stone" || p.Lines[0].Qty != 160 {
		t.Errorf("stone first: %+v", p.Lines)
	}
	if again := Make(stock, orders, ref, 10, rules()); again.Gross != p.Gross {
		t.Error("planning is not deterministic")
	}
}

func TestNoOrdersNoSale(t *testing.T) {
	if p := Make(map[string]int64{"timber": 50}, nil, map[string]int64{"timber": 15}, 5, rules()); p.Gross != 0 || len(p.Lines) != 0 {
		t.Errorf("a visit with no orders sold %+v", p)
	}
	if (Rules{}).Enabled() || !rules().Enabled() {
		t.Error("Enabled")
	}
}
