package company

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func city() Market {
	return Market{Population: 40000, WealthBPS: 9000, BudgetPerThousand: 400,
		Demand: map[string]int64{"groceries": 6, "meals": 5}}
}

func TestQualityAndVolume(t *testing.T) {
	ty := grocery()
	if Quality(ty, 0) != 3000 || Quality(ty, 2) != 6500 || Quality(ty, 4) != 10000 || Quality(ty, 9) != 10000 {
		t.Fatalf("quality %d %d %d", Quality(ty, 0), Quality(ty, 2), Quality(ty, 4))
	}
	if Volume(ty, 10000) != 10000 || Volume(ty, 11000) != 8600 || Volume(ty, 6000) != 15600 {
		t.Fatalf("volume %d %d %d", Volume(ty, 10000), Volume(ty, 11000), Volume(ty, 6000))
	}
	steep := ty
	steep.ElasticityBPS = 100000
	if Volume(steep, 16000) != 0 {
		t.Fatal("volume below zero is not clamped")
	}
}

func TestSettleOneCompany(t *testing.T) {
	// demand = 40000 × 6 / 1000 × 0.9 = 216 units; capacity 20 + 4 × 40 =
	// 180; sold 180 at 12 = 2160.
	s, err := Settle(city(), []Seller{{ID: "a", Type: grocery(), PriceBPS: 10000, Shifts: 4, PresenceBPS: 10000}})
	if err != nil {
		t.Fatal(err)
	}
	sale := s.Sales[0]
	if sale.Wanted != 216 || sale.Capacity != 180 || sale.Sold != 180 || sale.Revenue.Minor() != 2160 || sale.QualityBPS != 10000 {
		t.Fatalf("sale %+v", sale)
	}
	if s.Budget.Minor() != 14400 || s.Paid.Minor() != 2160 || s.Unmet["groceries"] != 36 || s.Unmet["meals"] != 180 {
		t.Fatalf("settlement %+v", s)
	}
	// A dearer price near capacity earns more: 10% above sells 185 wanted,
	// 180 served, at 13.2.
	dear, _ := Settle(city(), []Seller{{ID: "a", Type: grocery(), PriceBPS: 11000, Shifts: 4, PresenceBPS: 10000}})
	if dear.Sales[0].Revenue.Minor() <= sale.Revenue.Minor() {
		t.Fatalf("a slightly dearer full company should earn more: %+v", dear.Sales[0])
	}
	// Unstaffed, it sells only its base.
	idle, _ := Settle(city(), []Seller{{ID: "a", Type: grocery(), PriceBPS: 10000, PresenceBPS: 10000}})
	if idle.Sales[0].Sold != 20 || idle.Sales[0].QualityBPS != 3000 {
		t.Fatalf("idle %+v", idle.Sales[0])
	}
}

func TestSettleCompetitionSplitsByQuality(t *testing.T) {
	s, err := Settle(city(), []Seller{
		{ID: "good", Type: grocery(), PriceBPS: 10000, Shifts: 4, PresenceBPS: 10000},
		{ID: "poor", Type: grocery(), PriceBPS: 10000, Shifts: 0, PresenceBPS: 10000},
	})
	if err != nil {
		t.Fatal(err)
	}
	good, poor := s.Sales[0], s.Sales[1]
	if good.Wanted <= poor.Wanted || good.Wanted+poor.Wanted > 216 {
		t.Fatalf("split %+v %+v", good, poor)
	}
	// 216 × 10000 / 13000 = 166, 216 × 3000 / 13000 = 49.
	if good.Wanted != 166 || poor.Wanted != 49 || poor.Sold != 20 {
		t.Fatalf("split %+v %+v", good, poor)
	}
}

func TestSettleNeverExceedsTheBudget(t *testing.T) {
	mk := city()
	mk.BudgetPerThousand = 10 // 40000 × 10 / 1000 × 0.9 = 360
	sellers := []Seller{
		{ID: "a", Type: grocery(), PriceBPS: 10000, Shifts: 4, PresenceBPS: 10000},
		{ID: "b", Type: grocery(), PriceBPS: 12000, Shifts: 3, PresenceBPS: 5000},
	}
	s, err := Settle(mk, sellers)
	if err != nil {
		t.Fatal(err)
	}
	if s.Budget.Minor() != 360 || s.Paid.Minor() > 360 || s.Asked.Minor() <= 360 {
		t.Fatalf("budget %s asked %s paid %s", s.Budget, s.Asked, s.Paid)
	}
	mk.Cap = money.FromMinor(100)
	s, _ = Settle(mk, sellers)
	if s.Budget.Minor() != 100 || s.Paid.Minor() > 100 {
		t.Fatalf("cap %s paid %s", s.Budget, s.Paid)
	}
}

func TestSettleRefusesBadInput(t *testing.T) {
	if _, err := Settle(city(), []Seller{{ID: "a", Type: grocery(), PriceBPS: 50000, PresenceBPS: 10000}}); err == nil {
		t.Error("a price outside the range settled")
	}
	bad := city()
	bad.Population = -1
	if _, err := Settle(bad, nil); err == nil {
		t.Error("a negative population settled")
	}
	s, err := Settle(Market{}, []Seller{{ID: "a", Type: grocery(), PriceBPS: 10000, Shifts: 2, PresenceBPS: 10000}})
	if err != nil || !s.Paid.IsZero() {
		t.Errorf("a city with nobody in it paid %s (%v)", s.Paid, err)
	}
}
