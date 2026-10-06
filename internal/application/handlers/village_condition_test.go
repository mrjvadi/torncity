package handlers

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/labor"
)

func conditionHandler() *VillageHandler {
	h := &VillageHandler{}
	h.labor = labor.Default()
	return h
}

func TestWearCountsLocalDays(t *testing.T) {
	h := conditionHandler()
	done := time.Date(2026, 10, 1, 23, 0, 0, 0, time.UTC)
	b := application.SettlementBuildingInstance{Status: "complete", CompletedAt: &done}
	// two hours later it is already the next UTC day: one day of wear in UTC...
	later := done.Add(2 * time.Hour)
	if got := h.damageNow(b, 50, later, 0); got != 50 {
		t.Errorf("one UTC day boundary: damage %d, want 50", got)
	}
	// ...but in a zone three hours behind it is still the same local day: no wear
	if got := h.damageNow(b, 50, later, -3*time.Hour); got != 0 {
		t.Errorf("the same local day: damage %d, want 0", got)
	}
	// a stored damage counts from its own instant, and it never passes a ruin
	at := done.Add(24 * time.Hour)
	b2 := application.SettlementBuildingInstance{Status: "complete", CompletedAt: &done, DamageBPS: 9990, DamageAt: &at}
	if got := h.damageNow(b2, 50, at.Add(72*time.Hour), 0); got != labor.BPS {
		t.Errorf("a ruin is 10000 at most: %d", got)
	}
	// a building that does not wear
	if got := h.damageNow(b, 0, later.Add(1000*time.Hour), 0); got != 0 {
		t.Errorf("no decay: %d", got)
	}
}

func TestConditionFactorBands(t *testing.T) {
	h := conditionHandler()
	cases := []struct {
		cond   int64
		bps    int64
		closed bool
	}{{10000, 10000, false}, {5000, 10000, false}, {4999, 7500, false}, {2500, 7500, false}, {2499, 0, true}, {0, 0, true}}
	for _, c := range cases {
		if bps, closed := h.conditionFactor(c.cond); bps != c.bps || closed != c.closed {
			t.Errorf("condition %d: bps %d closed %v, want %d %v", c.cond, bps, closed, c.bps, c.closed)
		}
	}
}
