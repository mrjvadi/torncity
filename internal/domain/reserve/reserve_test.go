package reserve

import "testing"

func TestExcessAndCoverage(t *testing.T) {
	if Excess(5000, 4975) != 25 || Excess(100, 400) != 0 {
		t.Error("excess is the pot above the basis, never negative")
	}
	// supply 49750 units at x 1.00 and r0 10 is worth 4975 SUP; a pot of 5000 covers it at 100.5 %
	if got, ok := Coverage(5000, 49_750, 1_000_000, 10); !ok || got != 10_050 {
		t.Errorf("coverage %d %v", got, ok)
	}
	// the units lost value (x 0.5): the same pot covers twice as much
	if got, _ := Coverage(5000, 49_750, 500_000, 10); got != 20_104 {
		t.Errorf("coverage %d", got)
	}
	if _, ok := Coverage(5000, 0, 1_000_000, 10); ok {
		t.Error("no supply, no coverage")
	}
}

func TestTheInterventionStaysInsideItsLimits(t *testing.T) {
	// a pot of 10000 against a basis of 4975: 8 % a period is 800; the floor 30 % of the basis is 1492
	if got := BuyBudget(10_000, 4_975, 0, 800, 3000); got != 800 {
		t.Errorf("the period cap: %d", got)
	}
	// 500 already used: 8 % of the 10000 the pot began the period with is 800, so 300 are left
	if got := BuyBudget(9_500, 4_975, 500, 800, 3000); got != 300 {
		t.Errorf("the cap counts what the period used: %d", got)
	}
	// a pot near the floor can spend only what is above it
	if got := BuyBudget(1_600, 4_975, 0, 800, 3000); got != 108 {
		t.Errorf("the pot may not fall below the floor: %d", got)
	}
	if got := BuyBudget(1_400, 4_975, 0, 800, 3000); got != 0 {
		t.Errorf("under the floor nothing is spent: %d", got)
	}
	if got := SellBudget(10_000, 0, 800); got != 800 {
		t.Errorf("a sale offers its share of the units: %d", got)
	}
}

func TestClaimsAreEqualShares(t *testing.T) {
	// 3 holders of 100, 200 and 700 units share a pot of 1000; each claim is taken from what is left
	pot, claimable := int64(1000), int64(1000)
	var got []int64
	for _, u := range []int64{700, 100, 200} {
		s := ClaimShare(pot, claimable, u)
		got = append(got, s)
		pot -= s
		claimable -= u
	}
	if got[0] != 700 || got[1] != 100 || got[2] != 200 || pot != 0 {
		t.Errorf("shares %v, left %d", got, pot)
	}
	// with a pot smaller than the units: each unit is worth a fraction, rounded down; the dust stays
	pot, claimable = 100, 333
	var sum int64
	for _, u := range []int64{111, 111, 111} {
		s := ClaimShare(pot, claimable, u)
		sum += s
		pot -= s
		claimable -= u
	}
	if sum > 100 || sum < 98 {
		t.Errorf("the shares add up to the pot less a little dust: %d", sum)
	}
	if ClaimShare(100, 50, 60) != 0 {
		t.Error("nobody claims more than is claimable")
	}
}

func TestTheMacroTick(t *testing.T) {
	r := MacroRules{MNormBPS: 2000, KappaBPS: 200, PiMaxBPS: 300, WTradableBPS: 6000}
	base := Macro{SupplyUnits: 50_000, M: 5000, Y: 10_000, XRefPPM: 1_000_000, PrevXRefPPM: 1_000_000,
		Tradable: PPM, NonTradable: PPM, Price: PPM, PrevSupply: 50_000}
	// a steady rate and money at the norm of output: nothing moves
	// gap = 5000 x 1.0 / (0.2 x 10000) - 1 = 1.5 -> kappa 2 % x 150 % = 3 %: capped at +3 % (300 bps)
	got := Tick(base, r)
	if got.Tradable != PPM || got.PiLocalBPS != 300 || got.NonTradable != 1_030_000 {
		t.Errorf("a steady rate: %+v", got)
	}
	// the money lost a fifth of its value (x 0.8): tradable goods cost a quarter more in it
	weak := base
	weak.XRefPPM = 800_000
	weak.Y = 0 // no measured output: no local inflation
	got = Tick(weak, r)
	if got.Tradable != 1_250_000 || got.PiLocalBPS != 0 || got.NonTradable != PPM {
		t.Errorf("a weaker money: %+v", got)
	}
	// the basket: 60 % tradable (x1.25) and 40 % non-tradable (x1): 1.15
	if got.Price != 1_150_000 {
		t.Errorf("the basket moved to %d, want 1150000", got.Price)
	}
	// supply growth
	grown := base
	grown.SupplyUnits = 51_000
	if got := Tick(grown, r); got.SupplyGrowthBPS != 200 {
		t.Errorf("supply growth %d", got.SupplyGrowthBPS)
	}
}
