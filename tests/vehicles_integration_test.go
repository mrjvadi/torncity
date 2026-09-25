//go:build integration

// Integration test of vehicles (docs/adr/0024-property-and-politics.md): a
// player buys a car from Ostmarch's car dealer; the car option to Fenwick
// Span then costs its fuel, not the hire's fare; the journey burns the fuel
// out of the economy once for a double press, wears the car by one journey
// and names it.
package tests

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func TestOwnCarPaysFuelNotFare(t *testing.T) {
	w := newFWorld(t, "ostmarch")
	ctx := testCtx(t)
	car, ok := w.snap.ItemDef("car")
	if !ok || car.Vehicle == nil {
		t.Skip("the active content has no car; run `admin content load`")
	}
	keepShelves(t, w.pool, w.city.ID)
	driver := w.resident(0)
	t.Cleanup(func() {
		if _, err := w.pool.Raw().Exec(testCtx(t), `DELETE FROM travels WHERE player_id = $1::uuid`, driver.ID); err != nil {
			t.Errorf("cleanup travels: %v", err)
		}
	})
	grant(t, w.pool, application.AccountPlayerCash, driver.ID, 80_000)
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'business_district', place_since = now() WHERE id = $1::uuid`,
		driver.ID); err != nil {
		t.Fatal(err)
	}
	policy := postgres.NewPolicyReader(w.pool, w.now)
	travel := handlers.NewTravelHandler(w.uow, transportIDs{t}, nil, w.cities, snapshotNetwork{w.snap}, policy, 60, 25,
		time.Hour, w.now).WithPlaces(w.registry)

	// Without a car, the car option is a hire at its fare.
	hire := modeFare(t, travel, driver, "fenwick_span", "car")

	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, policy, gametime.Scale(gameScale),
		&crimeDice{}, time.Hour, w.now)
	w.do("buy a car", func() (*presenter.Response, error) {
		return shops.Buy(ctx, w.meta(driver, "shop.buy"), handlers.ShopRequest{Shop: "car_dealer", Item: "car", Qty: "1",
			Method: "cash", Nonce: strings.ReplaceAll(newUUID(t), "-", "")[:12]})
	})
	var pieceID string
	var uses int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text, uses_left FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'car'`,
		driver.ID).Scan(&pieceID, &uses); err != nil {
		t.Fatalf("no car bought: %v", err)
	}
	fuel := modeFare(t, travel, driver, "fenwick_span", "car")
	var distance int
	for _, o := range w.snap.TransportOptions("ostmarch", "fenwick_span") {
		if o.Mode.Code == "car" {
			distance = o.DistanceKM
		}
	}
	if want := int64(distance) * car.Vehicle.FuelPerDistance; fuel != want || fuel >= hire {
		t.Fatalf("the car option costs %d with a car (want its fuel %d) and %d without", fuel, want, hire)
	}
	// A car leaves from the streets of the centre.
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_centre', place_since = now() WHERE id = $1::uuid`,
		driver.ID); err != nil {
		t.Fatal(err)
	}
	cash := cashBalance(t, w.pool, application.AccountPlayerCash, driver.ID)
	start := travelMeta(driver, "req-drive")
	req := handlers.StartTravelRequest{City: "fenwick_span", Mode: "car", Max: fmt.Sprint(fuel), Method: "cash"}
	for range 2 {
		w.do("drive", func() (*presenter.Response, error) { return travel.Start(ctx, start, req) })
	}
	if got := cash - cashBalance(t, w.pool, application.AccountPlayerCash, driver.ID); got != fuel {
		t.Fatalf("the journey cost %d, want the fuel %d once", got, fuel)
	}
	if n := w.count(`SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
	   WHERE a.owner_id = $1::uuid AND e.reason = 'fuel'`, driver.ID); n != 1 {
		t.Fatalf("%d fuel payments, want 1", n)
	}
	if n := w.count(`SELECT count(*) FROM travels WHERE player_id = $1::uuid AND vehicle_id = $2::uuid`, driver.ID, pieceID); n != 1 {
		t.Fatalf("%d journeys name the car, want 1", n)
	}
	var left int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT uses_left FROM item_pieces WHERE id = $1::uuid`, pieceID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != uses-1 {
		t.Fatalf("the car has %d journeys left, want %d", left, uses-1)
	}
	if f := w.verify(); f.FuelLedger == 0 {
		t.Fatalf("the invariants saw no fuel: %+v", f)
	}
}

// modeFare reads one mode's fare off the options screen's departure button.
func modeFare(t *testing.T, travel *handlers.TravelHandler, p *application.Player, to, mode string) int64 {
	t.Helper()
	resp, err := travel.Options(testCtx(t), travelMeta(p, "req-options-"+randomToken(t, 6)), handlers.TravelOptionsRequest{City: to})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	prefix := "travel:start:" + to + ":" + mode + ":"
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, prefix) {
				var fare int64
				fmt.Sscanf(strings.TrimPrefix(b.CallbackData, prefix), "%d", &fare) //nolint:errcheck // a miss is caught below
				return fare
			}
		}
	}
	t.Fatalf("no %s to %s among the options: %q", mode, to, resp.Text)
	return 0
}
