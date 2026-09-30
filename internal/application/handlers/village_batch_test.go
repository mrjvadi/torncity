package handlers

import (
	"reflect"
	"testing"

	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func TestLineLotsGoesAlongTheRowThenDownTheColumn(t *testing.T) {
	got := lineLots(0, 0, 2, 2)
	want := [][2]int{{0, 0}, {1, 0}, {2, 0}, {2, 1}, {2, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("line (0,0)-(2,2) = %v, want %v", got, want)
	}
	if got := lineLots(3, 1, 3, 1); !reflect.DeepEqual(got, [][2]int{{3, 1}}) {
		t.Errorf("a line from a lot to itself = %v, want that one lot", got)
	}
	if got := lineLots(3, 3, 1, 3); !reflect.DeepEqual(got, [][2]int{{3, 3}, {2, 3}, {1, 3}}) {
		t.Errorf("a line running west = %v", got)
	}
	if got := lineLots(1, 3, 1, 0); !reflect.DeepEqual(got, [][2]int{{1, 3}, {1, 2}, {1, 1}, {1, 0}}) {
		t.Errorf("a line running north = %v", got)
	}
}

func TestBatchRequestLotsAreDistinctAndOrdered(t *testing.T) {
	req := VillageBuildManyRequest{Code: "road", Lots: []string{"1-1", "2-1", "1-1", "2-2-r"}}
	got, ok := req.lots()
	if !ok {
		t.Fatal("a well-formed batch was refused")
	}
	want := [][2]int{{1, 1}, {2, 1}, {2, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lots = %v, want %v (duplicates dropped, order kept, rotation ignored)", got, want)
	}
}

func TestBatchRequestFromTo(t *testing.T) {
	req := VillageBuildManyRequest{Code: "road", From: screens.LotToken(0, 0, false), To: screens.LotToken(2, 0, false)}
	got, ok := req.lots()
	if !ok || !reflect.DeepEqual(got, [][2]int{{0, 0}, {1, 0}, {2, 0}}) {
		t.Errorf("from/to = %v (ok %v)", got, ok)
	}
}

func TestBatchRequestRefusesMalformed(t *testing.T) {
	for name, req := range map[string]VillageBuildManyRequest{
		"empty":         {Code: "road"},
		"bad token":     {Code: "road", Lots: []string{"1-1", "nonsense"}},
		"half a line":   {Code: "road", From: "1-1"},
		"bad line end":  {Code: "road", From: "1-1", To: "x"},
		"negative lots": {Code: "road", Lots: []string{"-1-2"}},
	} {
		if _, ok := req.lots(); ok {
			t.Errorf("%s: a malformed batch was accepted", name)
		}
	}
}
