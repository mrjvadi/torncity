package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
)

func TestOnlyThePostsThatWereOpenMakeGarrisonSeats(t *testing.T) {
	h := &VillageHandler{raid: application.RaidRules{GarrisonServices: []string{"local_security", "garrison"}}}
	day := &application.ServiceDay{Posts: []application.ServicePost{
		{BuildingID: "a", Service: "local_security", Held: true, Staff: 2},
		{BuildingID: "b", Service: "garrison", Held: true, Staff: 4},
		{BuildingID: "c", Service: "garrison", Held: false, Idle: application.ServiceIdleNoWage},
		{BuildingID: "d", Service: "lodging_and_tea", Held: true, Staff: 2},
		{BuildingID: "e", Service: "claim_and_sight", Held: true, Staff: 3},
	}}
	if got := h.garrisonSeats(day); got != 6 {
		t.Fatalf("seats = %d, want the watch's 2 and the militia's 4 only", got)
	}
	if got := h.garrisonSeats(nil); got != 0 {
		t.Fatalf("a day nobody judged has no seats: %d", got)
	}
}

func TestARaidSeedIsStableAndTheOutcomeReadsBack(t *testing.T) {
	if raidSeedOf("a") != raidSeedOf("a") || raidSeedOf("a") == raidSeedOf("b") {
		t.Fatal("the seed of a raid is its id's, and differs between raids")
	}
	if raidSeedOf("x") < 0 {
		t.Fatal("a seed is not negative")
	}
	o := decodeOutcome([]byte(`{"raiders":4,"transport":"cart","taken":true}`))
	if o.Raiders != 4 || o.Transport != "cart" || !o.Taken {
		t.Fatalf("%+v", o)
	}
	if decodeOutcome(nil).Raiders != 0 || decodeOutcome([]byte("nope")).Taken {
		t.Fatal("garbage reads as empty")
	}
}
