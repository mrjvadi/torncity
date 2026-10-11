package landuse

import "testing"

func TestConnectedAndTouches(t *testing.T) {
	l := []Cell{{0, 0}, {1, 0}, {1, 1}}
	if !Connected(l) {
		t.Error("an L is one piece")
	}
	if Connected([]Cell{{0, 0}, {2, 0}}) {
		t.Error("two cells with a gap are two pieces")
	}
	if Connected(nil) {
		t.Error("nothing is not a piece")
	}
	if !Touches([]Cell{{0, 0}}, []Cell{{1, 0}}) || Touches([]Cell{{0, 0}}, []Cell{{1, 1}}) {
		t.Error("only a shared side touches, not a corner")
	}
	if !Connected([]Cell{{-1, -1}, {-1, 0}}) {
		t.Error("negative coordinates connect too")
	}
}

func TestBandsAndFoundationSteps(t *testing.T) {
	b := Bands{Flat: 8, Gentle: 20, Unusable: 30}
	for slope, want := range map[float64]int{0: BandFlat, 7.9: BandFlat, 8: BandGentle, 19: BandGentle, 20: BandSteep, 29.9: BandSteep, 30: BandUnusable, 99: BandUnusable} {
		if got := b.Of(slope); got != want {
			t.Errorf("slope %v: band %d, want %d", slope, got, want)
		}
	}
	gentle, _ := BandOf("gentle")
	flat, _ := BandOf("flat")
	if Steps(gentle, BandFlat) != 0 || Steps(gentle, BandGentle) != 0 {
		t.Error("a building stands unaided up to its own band")
	}
	if Steps(gentle, BandSteep) != 1 {
		t.Error("one band over takes a foundation")
	}
	if Steps(flat, BandSteep) != -1 {
		t.Error("two bands over cannot be helped")
	}
	if Steps(gentle, BandUnusable) != -1 {
		t.Error("the unusable band takes no building, foundation or not")
	}
	if _, ok := BandOf("sheer"); ok {
		t.Error("a word that names no band")
	}
	if (Bands{Flat: 20, Gentle: 8, Unusable: 30}).Valid() {
		t.Error("bands must rise")
	}
}

func TestZoneAllows(t *testing.T) {
	if !ZoneAllows("", []string{"farm"}) || !ZoneAllows("open", []string{"farm"}) {
		t.Error("no zone, or an open one, takes anything")
	}
	if !ZoneAllows("industry", []string{"core", "industry"}) || ZoneAllows("residential", []string{"industry"}) {
		t.Error("a zone takes the functions that list its kind")
	}
}
