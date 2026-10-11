package handlers

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/landuse"
)

func TestRectCells(t *testing.T) {
	cells, ok := rectCells("3-1", "4-2", 10)
	if !ok || len(cells) != 4 {
		t.Fatalf("a 2x2 rectangle: %v %v", cells, ok)
	}
	// the corners may be given in either order, and negative lots are written with m
	back, ok := rectCells("4-2", "3-1", 10)
	if !ok || len(back) != 4 || back[0] != cells[0] {
		t.Fatalf("the corners in the other order: %v %v", back, ok)
	}
	neg, ok := rectCells("m1-0", "0-0", 10)
	if !ok || len(neg) != 2 || neg[0] != (landuse.Cell{X: -1, Y: 0}) {
		t.Fatalf("a negative corner: %v %v", neg, ok)
	}
	if _, ok := rectCells("0-0", "9-9", 50); ok {
		t.Error("a rectangle larger than the cap")
	}
	if _, ok := rectCells("x", "0-0", 5); ok {
		t.Error("a bad token")
	}
}

func TestRegistryRulesDates(t *testing.T) {
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	r := application.RegistryRules{ZoningRuleAt: at, ZoningGraceDays: 14, SlopeRuleAt: at, SlopeGraceDays: 7, Slope: landuse.Bands{Flat: 8, Gentle: 20, Unusable: 30}}
	if r.ZoningOn(at.AddDate(0, 0, 13)) || !r.ZoningOn(at.AddDate(0, 0, 14)) {
		t.Error("zoning counts after the rule date plus the grace")
	}
	if r.SlopeOn(at.AddDate(0, 0, 6)) || !r.SlopeOn(at.AddDate(0, 0, 7)) {
		t.Error("the slope rule counts after its grace")
	}
	if !r.SlopeStartsAt().Equal(at.AddDate(0, 0, 7)) || !r.ZoningStartsAt().Equal(at.AddDate(0, 0, 14)) {
		t.Error("the start days")
	}
	var off application.RegistryRules
	if off.ZoningOn(at) || off.SlopeOn(at) {
		t.Error("with no rule date the rules are off")
	}
}
