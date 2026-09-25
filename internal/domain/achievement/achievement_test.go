package achievement

import "testing"

func TestAchievement(t *testing.T) {
	a := Achievement{Code: "ten_crimes", Event: CrimeSucceeded, Count: 10, Reward: 500}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if a.Reached(9) || !a.Reached(10) {
		t.Fatal("Reached is wrong")
	}
	if p, w := CapCash(500, 300, 1000); p != 300 || w != 200 {
		t.Fatalf("CapCash = %d, %d", p, w)
	}
	if p, w := CapCash(500, 1000, -5); p != 0 || w != 500 {
		t.Fatalf("CapCash with nothing left = %d, %d", p, w)
	}
	for _, bad := range []Achievement{{Code: "x", Event: "fishing", Count: 1}, {Code: "x", Event: JourneyMade},
		{Event: JourneyMade, Count: 1}, {Code: "x", Event: JourneyMade, Count: 1, Reward: -1}} {
		if bad.Validate() == nil {
			t.Errorf("%+v validated", bad)
		}
	}
}
