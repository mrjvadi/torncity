package landroad

import (
	"reflect"
	"testing"
)

func line(n int) []Lot {
	var r []Lot
	for x := 0; x < n; x++ {
		r = append(r, Lot{x, 0})
	}
	return r
}

func TestFrontage_DepthOneIsTheLotsThatTouchTheRoad(t *testing.T) {
	open := Frontage(line(4), 1, nil)
	// north and south of each cell, plus the two ends
	if len(open) != 4+4+2 {
		t.Fatalf("%d lots opened: %v", len(open), open)
	}
	for _, o := range open {
		if o.Dist != 1 {
			t.Fatalf("%v is %d lots from the road", o.Lot, o.Dist)
		}
	}
}

func TestFrontage_BandDepthAndServingCell(t *testing.T) {
	open := Frontage(line(3), 3, nil)
	byLot := map[Lot]Open{}
	for _, o := range open {
		byLot[o.Lot] = o
	}
	if o := byLot[Lot{1, 3}]; o.Dist != 3 || o.Serves != (Lot{1, 0}) {
		t.Fatalf("(1,3) = %+v", o)
	}
	if o := byLot[Lot{1, -2}]; o.Dist != 2 || o.Serves != (Lot{1, 0}) {
		t.Fatalf("(1,-2) = %+v", o)
	}
	if _, ok := byLot[Lot{1, 4}]; ok {
		t.Fatal("a lot four away opened on a band of three")
	}
	if _, ok := byLot[Lot{1, 0}]; ok {
		t.Fatal("a road cell opened as a lot")
	}
}

func TestFrontage_SkipClosesLotsAndWhatLiesBehindThem(t *testing.T) {
	// a closed row of lots at y=1 (land of another settlement): nothing behind it opens from the north side
	open := Frontage(line(5), 3, func(l Lot) bool { return l.Y == 1 })
	for _, o := range open {
		if o.Lot.Y >= 1 {
			t.Fatalf("%v opened behind a closed row", o.Lot)
		}
	}
	if len(open) == 0 {
		t.Fatal("the south side should still open")
	}
}

func TestFrontage_IsDeterministicAndUnique(t *testing.T) {
	roads := []Lot{{0, 0}, {1, 0}, {2, 0}, {2, 1}, {2, 2}, {3, 2}}
	a := Frontage(roads, 2, nil)
	rev := make([]Lot, len(roads))
	for i, l := range roads {
		rev[len(roads)-1-i] = l
	}
	b := Frontage(rev, 2, nil)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the order the road cells come in changes what opens")
	}
	seen := map[Lot]bool{}
	for _, o := range a {
		if seen[o.Lot] {
			t.Fatalf("%v opened twice", o.Lot)
		}
		seen[o.Lot] = true
	}
	if Frontage(nil, 3, nil) != nil || Frontage(roads, 0, nil) != nil {
		t.Fatal("no road or no depth opens nothing")
	}
}

func TestToBuild_ChargesTheUnbuiltStretchOnce(t *testing.T) {
	cells := map[Lot]Cell{
		{1, 0}: {Lot: Lot{1, 0}},
		{2, 0}: {Lot: Lot{2, 0}, Parent: Lot{1, 0}, HasParent: true},
		{3, 0}: {Lot: Lot{3, 0}, Parent: Lot{2, 0}, HasParent: true},
		{4, 0}: {Lot: Lot{4, 0}, Parent: Lot{3, 0}, HasParent: true},
	}
	todo, ok := ToBuild(cells, Lot{3, 0})
	if !ok || !reflect.DeepEqual(todo, []Lot{{1, 0}, {2, 0}, {3, 0}}) {
		t.Fatalf("first buyer: %v %v", todo, ok)
	}
	// the first buyer's road is laid: the next buyer one cell further pays one cell
	for _, l := range todo {
		c := cells[l]
		c.Built = true
		cells[l] = c
	}
	todo, ok = ToBuild(cells, Lot{4, 0})
	if !ok || !reflect.DeepEqual(todo, []Lot{{4, 0}}) {
		t.Fatalf("second buyer: %v %v", todo, ok)
	}
	// a lot beside an already built cell costs no road at all (and asking twice is the same)
	for i := 0; i < 2; i++ {
		todo, ok = ToBuild(cells, Lot{2, 0})
		if !ok || len(todo) != 0 {
			t.Fatalf("a built cell: %v %v", todo, ok)
		}
	}
	if _, ok := ToBuild(cells, Lot{9, 9}); ok {
		t.Fatal("an unknown cell")
	}
	loop := map[Lot]Cell{
		{0, 0}: {Lot: Lot{0, 0}, Parent: Lot{1, 0}, HasParent: true},
		{1, 0}: {Lot: Lot{1, 0}, Parent: Lot{0, 0}, HasParent: true},
	}
	if _, ok := ToBuild(loop, Lot{0, 0}); ok {
		t.Fatal("a plan that loops must not be trusted")
	}
}

func TestPrice(t *testing.T) {
	if got := Price(10, 2, 10, 60, 10000); got != 220 {
		t.Fatalf("%d", got)
	}
	if got := Price(10, 2, 10, 60, 15000); got != 330 {
		t.Fatalf("%d", got)
	}
	if got := Price(10, 0, 10, 60, 0); got != 100 {
		t.Fatalf("a missing surface multiplier reads as the base price: %d", got)
	}
}
