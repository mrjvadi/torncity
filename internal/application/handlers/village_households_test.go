package handlers

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

func hh(id, ref string, sat int64, arrived int) application.Household {
	return application.Household{ID: id, HomeRef: ref, State: application.HouseholdLiving, SatisfactionBPS: sat,
		ArrivedAt: time.Date(2026, 10, 1+arrived, 0, 0, 0, 0, time.UTC)}
}

// The households over the homes are the least satisfied first, the newest on a tie; they get notice and keep their home until it runs
// out, and the others stay where they are.
func TestPlaceHouseholdsGivesNoticeToTheLeastSatisfied(t *testing.T) {
	refs := []string{"base:0", "base:1", "base:2"}
	hs := []application.Household{hh("a", "base:0", 9000, 0), hh("b", "base:1", 4000, 1), hh("c", "base:2", 4000, 2), hh("d", "base:3", 8000, 3)}
	changed, free := placeHouseholds(refs, hs, 3)
	noticed := map[string]bool{}
	for _, c := range changed {
		if c.State == application.HouseholdLeaving {
			noticed[c.ID] = true
			if c.LowDays != 3 {
				t.Errorf("%s has %d days of notice", c.ID, c.LowDays)
			}
		}
	}
	if len(noticed) != 1 || !noticed["c"] {
		t.Errorf("the newest of the two least satisfied should have notice, got %v", noticed)
	}
	if len(free) != 0 {
		t.Errorf("no home is free: %v", free)
	}
}

// A household whose home went (a demolished building) moves to a free home; one with no free home to move to waits for the day it can.
func TestPlaceHouseholdsMovesTheHomeless(t *testing.T) {
	refs := []string{"base:0", "base:1", "b:x:0"}
	hs := []application.Household{hh("a", "base:0", 7000, 0), hh("b", "gone:1", 7000, 1)}
	changed, free := placeHouseholds(refs, hs, 1)
	if len(changed) != 1 || changed[0].ID != "b" || changed[0].HomeRef != "base:1" {
		t.Errorf("b should move to the first free home: %+v", changed)
	}
	if len(free) != 1 || free[0] != "b:x:0" {
		t.Errorf("free homes %v", free)
	}
}

func TestBuildingOfRef(t *testing.T) {
	if buildingOfRef("b:abc:1") != "abc" || buildingOfRef("base:3") != "" || buildingOfRef("lot:0") != "" || buildingOfRef("") != "" {
		t.Error("the building of a home reference")
	}
}
