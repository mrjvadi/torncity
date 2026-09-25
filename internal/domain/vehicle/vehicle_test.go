package vehicle

import "testing"

func TestVehicle(t *testing.T) {
	car := Vehicle{Mode: "car", FuelPerDistance: 2, Durability: 100, RepairPerJourney: 30}
	if err := car.Validate(); err != nil {
		t.Fatal(err)
	}
	if car.Fuel(180) != 360 || car.Fuel(-1) != 0 {
		t.Fatal("fuel is wrong")
	}
	if !Drives(1) || Drives(0) {
		t.Fatal("Drives is wrong")
	}
	if car.ConditionBPS(25) != 2500 || car.ConditionBPS(500) != 10000 {
		t.Fatal("condition is wrong")
	}
	if car.Repair(90) != 300 || car.Repair(100) != 0 {
		t.Fatal("repair is wrong")
	}
	if (Vehicle{Mode: "car"}).Validate() == nil {
		t.Fatal("a vehicle with no journeys in it validated")
	}
}
