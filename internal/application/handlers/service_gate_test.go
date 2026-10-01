package handlers

import (
	"context"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
)

type gateCities map[string]application.City

func (g gateCities) List(context.Context) ([]application.City, error) { return nil, nil }

func (g gateCities) ByID(_ context.Context, id string) (*application.City, error) {
	for _, c := range g {
		if c.ID == id {
			c := c
			return &c, nil
		}
	}
	return nil, application.ErrCityNotFound
}

func (g gateCities) ByCode(_ context.Context, code string) (*application.City, error) {
	c, ok := g[code]
	if !ok {
		return nil, application.ErrCityNotFound
	}
	return &c, nil
}

// TestServiceGate: a village or a town does not offer what the tags start at
// the city stage, and says where to go; the neutral city, and a grown city,
// offer it; a service nothing tags is offered everywhere.
func TestServiceGate(t *testing.T) {
	snap := shippedSnapshot(t)
	cities := gateCities{
		"support":  {ID: "c1", Code: "support", Name: "Central", Tier: ""},
		"hamlet":   {ID: "c2", Code: "hamlet", Name: "Hamlet", Tier: "village"},
		"township": {ID: "c3", Code: "township", Name: "Township", Tier: "town"},
		"metro":    {ID: "c4", Code: "metro", Name: "Metro", Tier: "city"},
	}
	gate := NewServiceGate(cities, "support")
	ctx := context.Background()

	un, err := gate.Check(ctx, snap, "c2", "gold")
	if err != nil || un == nil {
		t.Fatalf("a village must not offer gold: %v %v", un, err)
	}
	if un.Stage != "city" || un.Here != "village" || un.Nearest == nil || un.Nearest.Code != "support" {
		t.Errorf("the state must name the stage, where the player is and where to go: %+v", un)
	}
	if len(un.Requires) == 0 || un.Requires[0].Code != "bank" {
		t.Errorf("the state must say what the settlement needs: %+v", un.Requires)
	}
	if un, _ := gate.Check(ctx, snap, "c3", "stocks"); un == nil || un.Here != "town" {
		t.Errorf("a town must not offer the exchange: %+v", un)
	}
	for _, id := range []string{"c1", "c4"} {
		if un, err := gate.Check(ctx, snap, id, "loans"); err != nil || un != nil {
			t.Errorf("city %s must offer loans: %+v %v", id, un, err)
		}
	}
	if un, _ := gate.Check(ctx, snap, "c2", "auction_house"); un == nil {
		t.Error("a village must not offer the auction house")
	}
	if un, _ := gate.Check(ctx, snap, "c2", "a_service_nothing_tags"); un != nil {
		t.Errorf("an untagged service is offered everywhere: %+v", un)
	}
	if un, _ := gate.Check(ctx, snap, "", "gold"); un != nil {
		t.Errorf("a player in no city is not refused: %+v", un)
	}
	var none *ServiceGate
	if un, err := none.Check(ctx, snap, "c2", "gold"); un != nil || err != nil {
		t.Errorf("no gate offers everything: %+v %v", un, err)
	}
}
