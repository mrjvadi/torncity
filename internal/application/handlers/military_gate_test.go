package handlers

import (
	"context"
	"testing"

	mview "github.com/mrjvadi/torncity/internal/presentation/military"
)

// TestServiceGateOfCountryActions: the army, war, procurement and the defence
// licences are country-level. A village or a town is told they are not offered
// there, with the stage they start at, from the tags of availability.yml; a
// city has the national level around it, so its player is answered by the
// command itself (a country, or a refusal).
func TestServiceGateOfCountryActions(t *testing.T) {
	snap := shippedSnapshot(t)
	cities := gateCities{
		"support":  {ID: "c1", Code: "support", Name: "Central", Tier: ""},
		"hamlet":   {ID: "c2", Code: "hamlet", Name: "Hamlet", Tier: "village"},
		"township": {ID: "c3", Code: "township", Name: "Township", Tier: "town"},
		"metro":    {ID: "c4", Code: "metro", Name: "Metro", Tier: "city"},
	}
	gate := NewServiceGate(cities, "support")
	ctx := context.Background()

	for _, tag := range []struct{ kind, code, service string }{
		{mview.TagKindAction, mview.TagWar, mview.ServiceWar},
		{mview.TagKindAction, mview.TagProcure, mview.ServiceProcurement},
		{mview.TagKindAction, mview.TagLicence, mview.ServiceDefenceLicence},
		{mview.TagKindOffice, mview.TagDefenceHead, mview.ServiceArmedForces},
	} {
		for _, id := range []string{"c2", "c3"} {
			un, err := gate.CheckTag(ctx, snap, id, tag.kind, tag.code, tag.service)
			if err != nil || un == nil {
				t.Fatalf("%s must not be offered in settlement %s: %+v %v", tag.service, id, un, err)
			}
			if un.Service != tag.service || un.Stage != "country" {
				t.Errorf("%s: the answer must name the service and the stage it starts at: %+v", tag.service, un)
			}
		}
		for _, id := range []string{"c1", "c4"} {
			if un, err := gate.CheckTag(ctx, snap, id, tag.kind, tag.code, tag.service); err != nil || un != nil {
				t.Errorf("%s must reach a city (%s): %+v %v", tag.service, id, un, err)
			}
		}
	}
	if un, _ := gate.CheckTag(ctx, snap, "c2", mview.TagKindAction, "country.nothing", "x"); un != nil {
		t.Errorf("an untagged action is not refused: %+v", un)
	}
	if un, _ := gate.CheckTag(ctx, snap, "", mview.TagKindAction, mview.TagWar, mview.ServiceWar); un != nil {
		t.Errorf("a player in no city is not refused: %+v", un)
	}
	var none *ServiceGate
	if un, err := none.CheckTag(ctx, snap, "c2", mview.TagKindAction, mview.TagWar, mview.ServiceWar); un != nil || err != nil {
		t.Errorf("no gate offers everything: %+v %v", un, err)
	}
}
