package rail

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func wagonway() Class {
	return Class{Code: "wagonway", MaxConsistKg: 8000, WagonKg: 2000, SpeedKmh: 5, MaxGradeBPS: 300,
		LotMaterials: map[string]int64{"sleeper": 2, "wooden_rail": 1, "stone": 1}, LotWorkHours: 2, LotMinutes: 6, BrokenBelowBPS: 3000, MaxTrains: 4}
}

func TestQuoteScalesWithTheLots(t *testing.T) {
	c := wagonway()
	q := c.QuoteOf(305)
	if q.Lots != 11 || q.Materials["sleeper"] != 22 || q.Materials["wooden_rail"] != 11 || q.WorkHours != 22 || q.Duration != 66*time.Minute {
		t.Fatalf("quote for one tile: %+v", q)
	}
	if Lots(1) != 1 || Lots(30) != 1 || Lots(31) != 2 {
		t.Fatal("a stretch is at least one lot and rounds up")
	}
}

func TestCapacityIsTheLesserOfWagonsAndTheTeam(t *testing.T) {
	c := wagonway()
	if c.CapacityKg(2) != 4000 || c.CapacityKg(9) != 8000 || c.CapacityKg(0) != 0 || c.Wagons() != 4 {
		t.Fatalf("capacity %d %d %d wagons %d", c.CapacityKg(2), c.CapacityKg(9), c.CapacityKg(0), c.Wagons())
	}
}

func TestDurationSlowsOnWornTrack(t *testing.T) {
	c := wagonway()
	good := []Segment{{ID: "a", LengthM: 5000, ConditionBPS: 9000}}
	worn := []Segment{{ID: "a", LengthM: 5000, ConditionBPS: 4000}}
	if c.Duration(good, 5000) != time.Hour || c.Duration(worn, 5000) != 2*time.Hour {
		t.Fatalf("5 km at 5 km/h: %s, worn %s", c.Duration(good, 5000), c.Duration(worn, 5000))
	}
	if id, err := Block([]Segment{{ID: "a", Built: true, ConditionBPS: 9000}, {ID: "b", Built: false, ConditionBPS: 9000}}, 3000); err == nil || id != "b" {
		t.Fatalf("an unlaid section must block the route, got %q %v", id, err)
	}
	if id, err := Block([]Segment{{ID: "a", Built: true, ConditionBPS: 2000}}, 3000); err == nil || id != "a" {
		t.Fatalf("a broken section must block the route, got %q %v", id, err)
	}
	if _, err := Block([]Segment{{ID: "a", Built: true, ConditionBPS: 9000}}, 3000); err != nil {
		t.Fatal(err)
	}
}

func TestSingleTrackHoldsOneTrainAndALoopTwo(t *testing.T) {
	tab := NewTable()
	route := []Segment{{ID: "s1"}, {ID: "s2"}, {ID: "l1", Loop: true}}
	if ok, _ := tab.Reserve("t1", route); !ok {
		t.Fatal("a free route must be reserved")
	}
	if ok, _ := tab.Reserve("t1", route); !ok {
		t.Fatal("the call is idempotent")
	}
	if ok, who := tab.Reserve("t2", []Segment{{ID: "s2"}}); ok || len(who) != 1 || who[0] != "t1" {
		t.Fatalf("a held section must refuse a second train, got %v %v", ok, who)
	}
	// a refusal takes nothing: s0 stays free
	if ok, _ := tab.Reserve("t2", []Segment{{ID: "s0"}, {ID: "s2"}}); ok || len(tab.Holders("s0")) != 0 {
		t.Fatal("a refused reservation must take no section at all")
	}
	if ok, _ := tab.Reserve("t2", []Segment{{ID: "l1", Loop: true}}); !ok {
		t.Fatal("a loop holds two trains")
	}
	if ok, _ := tab.Reserve("t3", []Segment{{ID: "l1", Loop: true}}); ok {
		t.Fatal("a loop holds no third")
	}
	tab.Release("t1")
	if ok, _ := tab.Reserve("t3", []Segment{{ID: "s2"}}); !ok {
		t.Fatal("a released section is free again")
	}
	if len(tab.Over()) != 0 {
		t.Fatalf("over-held sections: %v", tab.Over())
	}
}

// Many schedules at once never put two trains on one section.
func TestConcurrentSchedulesNeverShareASection(t *testing.T) {
	tab := NewTable()
	var mu sync.Mutex // the table is one atomic step, as the stored table's primary key is
	segs := []Segment{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	var wg sync.WaitGroup
	won := make([]bool, 40)
	for i := range won {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			// each train wants two neighbouring sections
			from := i % 3
			ok, _ := tab.Reserve(fmt.Sprintf("t%d", i), segs[from:from+2])
			won[i] = ok
		}(i)
	}
	wg.Wait()
	if len(tab.Over()) != 0 {
		t.Fatalf("over-held sections: %v", tab.Over())
	}
	for _, s := range segs {
		if len(tab.Holders(s.ID)) > 1 {
			t.Fatalf("section %s holds %v", s.ID, tab.Holders(s.ID))
		}
	}
	n := 0
	for _, w := range won {
		if w {
			n++
		}
	}
	if n == 0 || n > 2 {
		t.Fatalf("%d trains hold neighbouring pairs of 4 sections; between 1 and 2 may", n)
	}
}

func TestALegIsAppliedOnce(t *testing.T) {
	leg := int64(3)
	applied := 0
	for _, ev := range []int64{3, 3, 2, 5} { // the event, its repeat, a stale one, a premature one
		if Apply(leg, ev) {
			applied++
			leg++
		}
	}
	if applied != 1 || leg != 4 {
		t.Fatalf("applied %d times, leg %d", applied, leg)
	}
}
