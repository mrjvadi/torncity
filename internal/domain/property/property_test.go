package property

import "testing"

func TestCityPriceRisesWithDemandUpToItsCap(t *testing.T) {
	studio := Type{Code: "studio", Kind: KindApartment, Size: 35, Quality: 2, BasePrice: 60000, Upkeep: 60, Home: true}
	d := Demand{StepBPS: 500, MaxBPS: 12000}
	if p := CityPrice(studio, 10000, 0, d); p != 60000 {
		t.Fatalf("the first unit costs %d, want 60000", p)
	}
	if p := CityPrice(studio, 10000, 2, d); p != 66000 {
		t.Fatalf("the third unit costs %d, want 66000", p)
	}
	if p := CityPrice(studio, 10000, 50, d); p != 72000 {
		t.Fatalf("a scarce unit costs %d, want the cap 72000", p)
	}
	if p := CityPrice(studio, 15000, 0, d); p != 90000 {
		t.Fatalf("a dear city asks %d, want 90000", p)
	}
}

func TestChargePaysTaxFirstAndCarriesTheRest(t *testing.T) {
	p := Charge(Owed{Upkeep: 100, Tax: 50, TaxDebt: 20}, 120)
	if p.TaxPaid != 70 || p.UpkeepPaid != 50 || p.TaxDebt != 0 || p.UpkeepDebt != 50 {
		t.Fatalf("Charge = %+v", p)
	}
	if !p.InDebt() {
		t.Fatal("a short payment left no debt")
	}
	full := Charge(Owed{Upkeep: 100, Tax: 50}, 1000)
	if full.InDebt() || full.TaxPaid != 50 || full.UpkeepPaid != 100 {
		t.Fatalf("a full payment = %+v", full)
	}
	none := Charge(Owed{Upkeep: 100, Tax: 50}, -5)
	if none.TaxPaid != 0 || none.UpkeepPaid != 0 || none.TaxDebt != 50 || none.UpkeepDebt != 100 {
		t.Fatalf("nothing to pay with = %+v", none)
	}
}

func TestRentIsAllOrNothing(t *testing.T) {
	if Rent(500, 499) != 0 || Rent(500, 500) != 500 || Rent(0, 100) != 0 {
		t.Fatal("rent is not all or nothing")
	}
	if Evicted(1, 2) || !Evicted(2, 2) || Foreclosed(2, 3) || !Foreclosed(3, 3) {
		t.Fatal("the limits are wrong")
	}
}

func TestTaxAndValidate(t *testing.T) {
	if Tax(100000, 20) != 200 || Tax(0, 20) != 0 || Tax(100, -1) != 0 {
		t.Fatal("tax is wrong")
	}
	bad := []Type{
		{Code: "x", Kind: "castle", Size: 1, Quality: 1, BasePrice: 1},
		{Code: "x", Kind: KindLand, Size: 1, Quality: 6, BasePrice: 1},
		{Code: "x", Kind: KindShop, Size: 1, Quality: 1, BasePrice: 1, RestEnergy: 5},
	}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Errorf("%+v validated", b)
		}
	}
	if (Demand{StepBPS: 300, MaxBPS: 9000}).Validate() == nil {
		t.Error("a demand cap below the whole validated")
	}
}
