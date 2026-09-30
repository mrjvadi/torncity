package clientapi

import (
	"context"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
)

// The version a village event carries (application.LayoutVersionsOf, written
// by the handler in the transaction that changed the buildings) is exactly the
// version GET /settlements/{id}/layout reports afterwards, for each kind of
// viewer. This is what lets a client compare an event with the layout it
// holds instead of counting anything.
func TestEventLayoutVersionsAreTheLayoutsVersions(t *testing.T) {
	f := newVillageFixture(t)
	ctx := context.Background()
	snap := f.svc.Content.Current()
	footprint := func(code string, rotated bool) (int, int) {
		if d, ok := snap.SettlementBuildingDef(code); ok {
			def := d.Def()
			if rotated {
				def = def.Rotate()
			}
			return def.FootprintW, def.FootprintH
		}
		return 1, 1
	}
	check := func(stage string) application.LayoutVersions {
		t.Helper()
		lv := application.LayoutVersionsOf(villageID, "village", "Amol", f.svc.gridLots("village"), f.buildings.rows, footprint)
		for who, want := range map[string]string{headID: lv.Head, residentID: lv.Member, strangerID: lv.Public} {
			l, err := f.svc.Layout(ctx, who, villageID)
			if err != nil {
				t.Fatal(err)
			}
			if l.Version != want {
				t.Errorf("%s: viewer %s has layout version %s, the event would say %s", stage, who[:4], l.Version, want)
			}
		}
		return lv
	}

	before := check("as it stands")
	f.buildings.rows[2].Status = "complete" // the camp finishes
	after := check("after a finish")
	if before.Head == after.Head || before.Member == after.Member || before.Public == after.Public {
		t.Errorf("a finished building must move every kind of viewer's version: %+v -> %+v", before, after)
	}
	if after.Head == after.Member {
		t.Error("the head's picture (who may place) must differ from another member's")
	}

	// A cancelled building is nothing any picture shows.
	f.buildings.rows = append(f.buildings.rows, application.SettlementBuildingInstance{ID: "b-new", SettlementID: villageID,
		TypeCode: "watch_hut", LotX: 2, LotY: 4, Status: "building"})
	placed := check("after a placement")
	f.buildings.rows[len(f.buildings.rows)-1].Status = "cancelled"
	again := check("after the cancel")
	if again != after || placed == after {
		t.Errorf("cancelling must bring the versions back to before the placement: %+v %+v %+v", after, placed, again)
	}
}
