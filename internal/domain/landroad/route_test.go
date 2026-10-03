package landroad

import (
	"errors"
	"reflect"
	"testing"
)

// flat is a synthetic ground: everything is dry grass at sea level unless fn
// says otherwise.
type flat struct{ fn func(Lot) Info }

func (f flat) Info(l Lot) Info {
	in := Info{TerrainBPS: 10000}
	if f.fn != nil {
		in = f.fn(l)
		if in.TerrainBPS == 0 {
			in.TerrainBPS = 10000
		}
	}
	return in
}

var dirt = Class{Code: "path", MaxGradeBPS: 2500, BridgeMaxSpanM: 10, Fords: true}
var track = Class{Code: "track", MaxGradeBPS: 1200, BridgeMaxSpanM: 60}

func env(g Ground, c Class) Env {
	return Env{Ground: g, Class: c, LotM: 30.5, StreamRun: 2, MaxExpansions: 20000}
}

func TestRoute_StraightOnAPlain(t *testing.T) {
	p, err := Route(env(flat{}, dirt), Lot{0, 0}, Lot{10, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 10 || p.Steps[0].Lot != (Lot{1, 0}) || p.Steps[9].Lot != (Lot{10, 0}) {
		t.Fatalf("steps %v", p.Steps)
	}
	if p.Crossings != 0 || p.ClimbM != 0 {
		t.Fatalf("crossings %d climb %v", p.Crossings, p.ClimbM)
	}
}

func TestRoute_ReachesWestAndSouthOfTheOrigin(t *testing.T) {
	// the land has no edge: negative lots are lots like any other
	p, err := Route(env(flat{}, dirt), Lot{0, 0}, Lot{-7, -4})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 11 || p.Steps[len(p.Steps)-1].Lot != (Lot{-7, -4}) {
		t.Fatalf("steps %v", p.Steps)
	}
}

func TestRoute_IsDeterministic(t *testing.T) {
	g := flat{fn: func(l Lot) Info { return Info{ElevationM: float64((l.X*7 + l.Y*13) % 5)} }}
	a, err := Route(env(g, dirt), Lot{0, 0}, Lot{12, 9})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, err := Route(env(g, dirt), Lot{0, 0}, Lot{12, 9})
		if err != nil || !reflect.DeepEqual(a.Steps, b.Steps) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestRoute_GoesAroundWhatIsBlocked(t *testing.T) {
	e := env(flat{}, dirt)
	// a wall of owned lots across x=5 from y=-3 to y=3
	e.Blocked = func(l Lot) bool { return l.X == 5 && l.Y >= -3 && l.Y <= 3 }
	p, err := Route(e, Lot{0, 0}, Lot{10, 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Steps {
		if e.Blocked(s.Lot) {
			t.Fatalf("the road runs over a blocked lot %v", s.Lot)
		}
	}
	if len(p.Steps) <= 10 {
		t.Fatalf("a detour must be longer than the straight line: %d", len(p.Steps))
	}
}

func TestRoute_PrefersFlatGroundToAClimb(t *testing.T) {
	// a ridge across the straight line: the road skirts it through the gap
	g := flat{fn: func(l Lot) Info {
		if l.X >= 4 && l.X <= 6 && l.Y != 4 {
			return Info{ElevationM: 25}
		}
		return Info{}
	}}
	p, err := Route(env(g, track), Lot{0, 0}, Lot{10, 0})
	if err != nil {
		t.Fatal(err)
	}
	if p.ClimbM != 0 {
		t.Fatalf("climbed %v m though a flat gap exists", p.ClimbM)
	}
}

func TestRoute_OpenWaterIsRefusedAndABrookIsForded(t *testing.T) {
	lake := flat{fn: func(l Lot) Info {
		if l.X == 10 && l.Y == 0 {
			return Info{Water: WaterStill}
		}
		return Info{}
	}}
	if _, err := Route(env(lake, dirt), Lot{0, 0}, Lot{10, 0}); !errors.Is(err, ErrWater) {
		t.Fatalf("a road into a lake: %v", err)
	}
	brook := flat{fn: func(l Lot) Info {
		if l.X == 5 {
			return Info{Water: WaterStream}
		}
		return Info{}
	}}
	p, err := Route(env(brook, dirt), Lot{0, 0}, Lot{10, 0})
	if err != nil {
		t.Fatal(err)
	}
	if p.Crossings != 1 {
		t.Fatalf("crossings %d, want one ford", p.Crossings)
	}
}

func TestRoute_ARiverNeedsABridgeTheClassCarries(t *testing.T) {
	river := flat{fn: func(l Lot) Info {
		if l.X == 5 || l.X == 6 { // two lots wide: 61 m
			return Info{Water: WaterRiver}
		}
		return Info{}
	}}
	// the footpath class carries 10 m: not even one river lot
	if _, err := Route(env(river, dirt), Lot{0, 0}, Lot{10, 0}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("a path over a river: %v", err)
	}
	// the track carries 60 m: 61 m is one metre too long
	_, err := Route(env(river, track), Lot{0, 0}, Lot{10, 0})
	var be *BridgeError
	if !errors.As(err, &be) || be.SpanM != 61 || be.ClassMaxM != 60 {
		t.Fatalf("a 61 m bridge on a 60 m class: %v", err)
	}
	narrow := flat{fn: func(l Lot) Info {
		if l.X == 5 {
			return Info{Water: WaterRiver}
		}
		return Info{}
	}}
	p, err := Route(env(narrow, track), Lot{0, 0}, Lot{10, 0})
	if err != nil || p.Crossings != 1 || p.LongestSpanM != 30 {
		t.Fatalf("a 30 m bridge: %v %+v", err, p)
	}
}

func TestRoute_CorridorAndBounds(t *testing.T) {
	e := env(flat{}, dirt)
	e.Corridor = func(l Lot) bool { return l.Y == 0 }
	if p, err := Route(e, Lot{0, 0}, Lot{6, 0}); err != nil || len(p.Steps) != 6 {
		t.Fatalf("%v %v", err, p)
	}
	e.Blocked = func(l Lot) bool { return l == (Lot{3, 0}) }
	if _, err := Route(e, Lot{0, 0}, Lot{6, 0}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("a corridor one lot wide with a block in it: %v", err)
	}
	e = env(flat{}, dirt)
	e.MaxLots = 5
	if _, err := Route(e, Lot{0, 0}, Lot{6, 0}); !errors.Is(err, ErrTooLong) {
		t.Fatalf("a plan longer than the bound: %v", err)
	}
	if _, err := Route(e, Lot{0, 0}, Lot{0, 0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("the same lot: %v", err)
	}
}

func TestRoute_RunsAlongAnExistingRoadCheaply(t *testing.T) {
	e := env(flat{}, dirt)
	// an existing road along y=3 from x=0..12; the end is at (12, 0)
	e.Cheap = func(l Lot) bool { return l.Y == 3 && l.X >= 0 && l.X <= 12 }
	p, err := Route(e, Lot{0, 0}, Lot{12, 0})
	if err != nil {
		t.Fatal(err)
	}
	onRoad := 0
	for _, s := range p.Steps {
		if e.Cheap(s.Lot) {
			onRoad++
		}
	}
	if onRoad < 10 {
		t.Fatalf("the line uses the existing road for %d lots only", onRoad)
	}
}
