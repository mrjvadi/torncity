//go:build integration

package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// The car asks the driving licence its availability row names (ADR 0059): during the grace it is offered with a notice, once
// the grace is over a rider without the certificate is not offered it, and a rider with the certificate always is.
func TestTheCarAsksTheDrivingLicence(t *testing.T) {
	w := newFWorld(t, "ostmarch")
	ctx := testCtx(t)
	if _, ok := w.snap.ItemDef("car"); !ok {
		t.Skip("the active content has no car; run `admin content load`")
	}
	keepShelves(t, w.pool, w.city.ID)
	driver := w.resident(0)
	t.Cleanup(func() {
		if _, err := w.pool.Raw().Exec(testCtx(t), `DELETE FROM travels WHERE player_id = $1::uuid`, driver.ID); err != nil {
			t.Errorf("cleanup travels: %v", err)
		}
	})
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'business_district', place_since = now() WHERE id = $1::uuid`, driver.ID); err != nil {
		t.Fatal(err)
	}
	policy := postgres.NewPolicyReader(w.pool, w.now)
	rule := time.Now().UTC().Add(-24 * time.Hour)
	options := func(rules handlers.PersonalRules) (offered bool, text string) {
		travel := handlers.NewTravelHandler(w.uow, transportIDs{t}, nil, w.cities, snapshotNetwork{w.snap}, policy, 60, 25,
			time.Hour, w.now).WithPlaces(w.registry).WithPersonal(rules)
		resp, err := rr(travel.Options(ctx, travelMeta(driver, "req-licence-"+randomToken(t, 6)), handlers.TravelOptionsRequest{City: "fenwick_span"}))
		if err != nil {
			t.Fatalf("options: %v", err)
		}
		for _, row := range resp.Keyboard.Rows {
			for _, b := range row {
				if strings.HasPrefix(b.CallbackData, "travel:start:fenwick_span:car:") {
					offered = true
				}
			}
		}
		return offered, resp.Text
	}

	if offered, _ := options(handlers.PersonalRules{}); !offered {
		t.Fatal("with no rule the car is offered as before")
	}
	if offered, text := options(handlers.PersonalRules{From: rule, GraceDays: 7}); !offered || !strings.Contains(text, "🪪") {
		t.Errorf("during the grace the car is offered with the licence notice: offered %v, text %q", offered, text)
	}
	if offered, text := options(handlers.PersonalRules{From: rule, GraceDays: 0}); offered || !strings.Contains(text, "🪪") {
		t.Errorf("after the grace a rider without the licence is not offered the car and is told why: offered %v, text %q", offered, text)
	}
	certifyIn(t, w.pool, driver.ID, "driving_licence")
	if offered, text := options(handlers.PersonalRules{From: rule, GraceDays: 0}); !offered || strings.Contains(text, "🪪") {
		t.Errorf("with the licence the car is offered and nothing is said: offered %v, text %q", offered, text)
	}
}
