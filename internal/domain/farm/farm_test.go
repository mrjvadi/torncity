package farm

import (
	"testing"
	"time"
)

func cfg() Config {
	return Config{SowShifts: 8, TendMax: 6, HarvestShifts: 12, TendBPS: 200,
		Grow: 6 * time.Hour, Window: 12 * time.Hour, RotStep: 3 * time.Hour, RotStepBPS: 1000,
		SeedIrrigated: 50, SeedRainfed: 25, BaseIrrigated: 400, BaseRainfed: 200,
		WaterServedBPS: 10000, WaterUnservedBPS: 6000, Reach: 5, Serves: 2, MinConditionBPS: 5000}
}

var t0 = time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)

func TestStagesFollowTheClockAndTheWork(t *testing.T) {
	c := cfg()
	if got := StageAt(nil, c, t0); got != StageIdle {
		t.Fatalf("no cycle: %s", got)
	}
	cy := &Cycle{SoilBPS: 10000}
	if got := StageAt(cy, c, t0); got != StageSowing {
		t.Fatalf("a new cycle is being sown: %s", got)
	}
	cy.SowStarted = 8
	grow := t0.Add(time.Hour)
	cy.GrowFrom = &grow
	for _, tc := range []struct {
		at   time.Duration
		want Stage
	}{
		{2 * time.Hour, StageGrowing},
		{6*time.Hour + time.Hour, StageRipe},
		{6*time.Hour + time.Hour + 12*time.Hour, StageRipe},
		{6*time.Hour + time.Hour + 12*time.Hour + time.Minute, StageOverripe},
		{7*time.Hour + 12*time.Hour + 30*time.Hour, StageRotted},
	} {
		if got := StageAt(cy, c, t0.Add(tc.at)); got != tc.want {
			t.Errorf("at +%s: %s, want %s", tc.at, got, tc.want)
		}
	}
	cy.HarvestStarted = 3
	if got := StageAt(cy, c, t0.Add(100*time.Hour)); got != StageHarvest {
		t.Errorf("a harvest begun keeps its yield and goes on: %s", got)
	}
	cy.HarvestStarted = 12
	if got := StageAt(cy, c, t0.Add(100*time.Hour)); got != StageHarvested {
		t.Errorf("every harvest shift started: %s", got)
	}
	if !CanOrder(cy, c, t0.Add(100*time.Hour)) || CanOrder(&Cycle{}, c, t0) {
		t.Error("a new crop may be ordered after a harvest, never over a crop that is being sown")
	}
}

func TestYieldIsTheProductOfSoilWaterAndTending(t *testing.T) {
	c := cfg()
	ripe := t0
	cy := Cycle{SoilBPS: 10000, WaterSum: 10000 * 3, WaterN: 3, GrowFrom: &ripe, SowStarted: 8}
	if got := cy.Yield(c, ripe); got != 400 {
		t.Errorf("a good field, a served farm, no tending: %d, want 400", got)
	}
	cy.Tended = 6
	if got := cy.Yield(c, ripe); got != 448 {
		t.Errorf("six tending shifts add twelve percent: %d, want 448", got)
	}
	cy.Tended = 20
	if got := cy.Yield(c, ripe); got != 448 {
		t.Errorf("tending is capped at the maximum: %d", got)
	}
	cy.Tended = 0
	cy.WaterSum, cy.WaterN = 6000*2, 2
	if got := cy.Yield(c, ripe); got != 240 {
		t.Errorf("an unserved farm yields 60 percent: %d, want 240", got)
	}
	cy.WaterSum, cy.WaterN = 0, 0
	cy.SoilBPS = 8000
	if got := cy.Yield(c, ripe); got != 192 {
		t.Errorf("nothing sampled counts as unserved, on poorer soil: %d, want 192", got)
	}
	cy.Rainfed, cy.SoilBPS, cy.WaterSum, cy.WaterN = true, 10000, 10000, 1
	if got := cy.Yield(c, ripe); got != 200 {
		t.Errorf("a rain-fed field has the lower base: %d, want 200", got)
	}
}

func TestSpoilingTakesATenthEveryStep(t *testing.T) {
	c := cfg()
	grown := t0
	cy := Cycle{SoilBPS: 10000, WaterSum: 10000, WaterN: 1, GrowFrom: &grown, SowStarted: 8}
	spoil := cy.SpoilAt(c)
	if got := cy.LossBPS(c, spoil); got != 0 {
		t.Errorf("at the end of the window nothing is lost: %d", got)
	}
	if got := cy.LossBPS(c, spoil.Add(time.Minute)); got != 1000 {
		t.Errorf("one step past: %d", got)
	}
	if got := cy.Yield(c, spoil.Add(4*time.Hour)); got != 320 {
		t.Errorf("two steps past the window: %d, want 320", got)
	}
	if got := cy.LossBPS(c, spoil.Add(100*time.Hour)); got != BPS {
		t.Errorf("never more than all: %d", got)
	}
}

func TestHarvestSharesAddUpToTheYield(t *testing.T) {
	for _, y := range []int64{0, 1, 11, 400, 448, 997} {
		var sum int64
		for k := 0; k < 12; k++ {
			sum += ShareOfHarvest(y, k, 12)
		}
		if sum != y {
			t.Errorf("yield %d: the 12 shares add up to %d", y, sum)
		}
	}
	if ShareOfHarvest(400, 12, 12) != 0 || ShareOfHarvest(400, -1, 12) != 0 {
		t.Error("a shift outside the harvest brings nothing")
	}
}

func TestSoilIsTheMeanOfTheLotsLessTheSlope(t *testing.T) {
	if got := Soil([]int64{12000, 10000, 8000}, 0, 1500, 9000); got != 10000 {
		t.Errorf("mean: %d", got)
	}
	if got := Soil([]int64{10000, 10000}, 1, 1500, 9000); got != 9250 {
		t.Errorf("one steep lot of two: %d, want 9250", got)
	}
	if got := Soil(nil, 0, 1500, 9000); got != 9000 {
		t.Errorf("no lots: the default, %d", got)
	}
	if got := Soil([]int64{1000}, 5, 5000, 9000); got != 1000 {
		t.Errorf("never below a tenth: %d", got)
	}
}

func TestAWaterWorkServesTheNearestTwoFarms(t *testing.T) {
	works := []Site{{ID: "w1", X: 0, Y: 0, W: 1, H: 1}}
	farms := []Site{
		{ID: "a", X: 2, Y: 0, W: 3, H: 3}, {ID: "b", X: 0, Y: 3, W: 3, H: 3},
		{ID: "c", X: 4, Y: 4, W: 3, H: 3}, {ID: "far", X: 20, Y: 20, W: 3, H: 3},
	}
	got := Serving(works, farms, 5, 2)
	if len(got) != 2 || got["a"] != "w1" || got["b"] != "w1" {
		t.Errorf("the two nearest are served: %v", got)
	}
	if _, ok := got["far"]; ok {
		t.Error("a farm out of reach is not served")
	}
	works = append(works, Site{ID: "w2", X: 6, Y: 6, W: 1, H: 1})
	got = Serving(works, farms, 5, 2)
	if got["c"] != "w2" {
		t.Errorf("a second work takes the farm the first could not: %v", got)
	}
}

func TestTheTollIsCarriedExactly(t *testing.T) {
	var carry, total int64
	for i := 0; i < 20; i++ {
		var toll int64
		toll, carry = Toll(8, 500, carry)
		total += toll
	}
	if total != 8 || carry != 0 {
		t.Errorf("20 batches of 8 at 5 percent give 8 in all: %d, carry %d", total, carry)
	}
}
