package content

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
)

// The reach the shipped config gives the modes that serve world-derived
// journeys (configs/config.yml travel.world_reach).
var shippedReach = map[string]int{"walk": 60, "cart": 500, "car": 21000}

func derivedCodes(s *Snapshot, km int) []string {
	var out []string
	for _, o := range s.DerivedTransport(km, shippedReach) {
		out = append(out, o.Mode.Code)
	}
	return out
}

// The modes a journey is offered by depend on its distance alone, in the
// order transport.yml declares them.
func TestDerivedTransportOffersModesByReach(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		km   int
		want string
	}{
		{1, "walk cart car"},
		{60, "walk cart car"},
		{61, "cart car"},
		{500, "cart car"},
		{501, "car"},
		{21000, "car"},
		{21001, ""},
	} {
		got := ""
		for i, c := range derivedCodes(snap, tc.km) {
			if i > 0 {
				got += " "
			}
			got += c
		}
		if got != tc.want {
			t.Errorf("%d km: modes %q, want %q", tc.km, got, tc.want)
		}
	}
	// A mode the config does not list never serves a world journey, and the
	// walk and the cart never serve a content route.
	if got := snap.DerivedTransport(100, map[string]int{}); len(got) != 0 {
		t.Errorf("no reach configured, yet %d modes offered", len(got))
	}
	for _, o := range snap.TransportOptions("support", "support") {
		if o.Mode.Code == "walk" || o.Mode.Code == "cart" {
			t.Errorf("%s serves a content route", o.Mode.Code)
		}
	}
}

// Time and fare grow with the distance by transport.yml's own numbers, at the
// game clock's 60x: the same distance always gives the same quote.
func TestDerivedQuotesFollowTheDistance(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	from := world.City{ID: "a", Code: "a", Name: "A"}
	to := world.City{ID: "b", Code: "b", Name: "B"}
	quote := func(mode string, km int) travel.Quote {
		t.Helper()
		for _, o := range snap.DerivedTransport(km, shippedReach) {
			if o.Mode.Code == mode {
				q, err := travel.QuoteJourney(from, to, o.Mode, o.DistanceKM,
					travel.Pricing{PolicyBPS: travel.BasisPoints}, 60)
				if err != nil {
					t.Fatal(err)
				}
				return q
			}
		}
		t.Fatalf("%s does not serve %d km", mode, km)
		return travel.Quote{}
	}
	for _, tc := range []struct {
		mode   string
		km     int
		fare   int64
		wait   time.Duration
		energy int
	}{
		// walk: 5 km/h, free, no boarding; cart: 20 km/h, 10m boarding,
		// 20 + 2/km; car: 90 km/h, 5m boarding, 5/km.
		{"walk", 42, 0, 8*time.Minute + 24*time.Second, 12},
		{"cart", 42, 104, 2*time.Minute + 16*time.Second, 5},
		{"car", 42, 210, 33 * time.Second, 14},
		{"cart", 380, 780, 19*time.Minute + 10*time.Second, 5},
		{"car", 1280, 6400, 14*time.Minute + 19*time.Second, 14},
	} {
		q := quote(tc.mode, tc.km)
		if q.Fare.Minor() != tc.fare || q.Wait != tc.wait || q.Energy != tc.energy {
			t.Errorf("%s %d km: fare %d wait %s energy %d; want %d, %s, %d",
				tc.mode, tc.km, q.Fare.Minor(), q.Wait, q.Energy, tc.fare, tc.wait, tc.energy)
		}
	}
	// Farther costs more and takes longer, in every mode.
	for _, mode := range []string{"cart", "car"} {
		near, far := quote(mode, 50), quote(mode, 400)
		if far.Fare.Minor() <= near.Fare.Minor() || far.Wait <= near.Wait {
			t.Errorf("%s: 400 km (%d, %s) is not dearer and longer than 50 km (%d, %s)",
				mode, far.Fare.Minor(), far.Wait, near.Fare.Minor(), near.Wait)
		}
	}
}
