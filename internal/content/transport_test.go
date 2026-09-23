package content

import (
	"errors"
	"strings"
	"testing"
)

func testMode(code string, requires ...string) TransportModeDef {
	return TransportModeDef{
		Code: code, Name: strings.ToUpper(code), Requires: requires,
		Speed: 60, Boarding: "10m", BaseFare: 10, FarePerDistance: 1, EnergyCost: 5,
		Demand: DemandDef{Window: "30m", FreeDepartures: 1, StepBPS: 500, MaxBPS: 20000},
	}
}

// transportPack is validPack plus a third city and transport: alpha and
// charlie have airports, bravo does not. Roads: alpha-bravo 100, bravo-charlie
// 100, alpha-charlie 300.
func transportPack() *Pack {
	p := validPack()
	p.Cities = append(p.Cities, city("charlie"))
	p.Cities[0].Facilities = []string{"airport"}
	p.Cities[2].Facilities = []string{"airport"}
	p.Routes = []RouteDef{
		{From: "alpha", To: "bravo", Distance: 100},
		{From: "bravo", To: "charlie", Distance: 100},
		{From: "alpha", To: "charlie", Distance: 300},
	}
	p.Facilities = []string{"airport", "rail_station"}
	p.TransportModes = []TransportModeDef{testMode("car"), testMode("flight", "airport")}
	return p
}

func TestTransportValidates(t *testing.T) {
	if err := transportPack().Validate(); err != nil {
		t.Fatalf("a valid transport pack was rejected: %v", err)
	}
}

func TestTransportRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Pack)
		want   error
	}{
		{"bad facility code", func(p *Pack) { p.Facilities[0] = "Air Port" }, ErrInvalidTransportCode},
		{"duplicate facility", func(p *Pack) { p.Facilities[1] = "airport" }, ErrDuplicateTransportCode},
		{"bad mode code", func(p *Pack) { p.TransportModes[0].Code = "car-hire" }, ErrInvalidTransportCode},
		{"mode code too long", func(p *Pack) { p.TransportModes[0].Code = "a_very_long_mode" }, ErrInvalidTransportCode},
		{"duplicate mode", func(p *Pack) { p.TransportModes[1].Code = "car" }, ErrDuplicateTransportCode},
		{"mode without name", func(p *Pack) { p.TransportModes[0].Name = "" }, ErrInvalidTransportMode},
		{"unknown required facility", func(p *Pack) { p.TransportModes[1].Requires = []string{"spaceport"} }, ErrUnknownFacility},
		{"city with unknown facility", func(p *Pack) { p.Cities[1].Facilities = []string{"harbour"} }, ErrUnknownFacility},
		{"city lists a facility twice", func(p *Pack) { p.Cities[0].Facilities = []string{"airport", "airport"} }, ErrDuplicateTransportCode},
		{"no speed", func(p *Pack) { p.TransportModes[0].Speed = 0 }, ErrInvalidTransportMode},
		{"unreadable boarding", func(p *Pack) { p.TransportModes[0].Boarding = "soon" }, ErrInvalidTransportMode},
		{"fractional boarding", func(p *Pack) { p.TransportModes[0].Boarding = "1.5s" }, ErrInvalidTransportMode},
		{"no demand window", func(p *Pack) { p.TransportModes[0].Demand.Window = "0s" }, ErrInvalidTransportMode},
		{"demand cap below the fare", func(p *Pack) { p.TransportModes[0].Demand.MaxBPS = 9000 }, ErrInvalidTransportMode},
		{"negative fare", func(p *Pack) { p.TransportModes[0].BaseFare = -1 }, ErrInvalidTransportMode},
		{"route names an unknown mode", func(p *Pack) { p.Routes[0].Modes = []string{"hovercraft"} }, ErrUnknownTransportMode},
		{"route names a mode twice", func(p *Pack) { p.Routes[0].Modes = []string{"car", "car"} }, ErrDuplicateTransportCode},
		{"route declares no mode", func(p *Pack) { p.Routes[0].Modes = []string{} }, ErrEmptyRouteModes},
		{"flight to a city with no airport", func(p *Pack) { p.Routes[0].Modes = []string{"flight"} }, ErrModeNotServable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := transportPack()
			tc.mutate(p)
			if err := p.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestModeNetworksAreDerivedOrDeclared(t *testing.T) {
	p := transportPack()
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}

	// Alpha to charlie: the car takes the 200 road via bravo; the flight
	// only has the direct 300 route, because bravo has no airport.
	opts := snap.TransportOptions("alpha", "charlie")
	if len(opts) != 2 || opts[0].Mode.Code != "car" || opts[0].DistanceKM != 200 ||
		opts[1].Mode.Code != "flight" || opts[1].DistanceKM != 300 {
		t.Errorf("alpha -> charlie options = %+v", opts)
	}
	// Alpha to bravo: no flight.
	if opts := snap.TransportOptions("alpha", "bravo"); len(opts) != 1 || opts[0].Mode.Code != "car" {
		t.Errorf("alpha -> bravo options = %+v, want the car only", opts)
	}
	if d, ok := snap.NearestByAnyMode("alpha", "charlie"); !ok || d != 200 {
		t.Errorf("NearestByAnyMode = %d, %v; want 200", d, ok)
	}

	// Declaring the road car-free takes the car off it: now the car must
	// go the long way round, the flight is unchanged.
	p = transportPack()
	p.Routes[0].Modes = []string{"flight"}
	p.Cities[1].Facilities = []string{"airport"}
	snap, err = BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	opts = snap.TransportOptions("alpha", "bravo")
	if len(opts) != 2 || opts[0].Mode.Code != "car" || opts[0].DistanceKM != 400 || opts[1].DistanceKM != 100 {
		t.Errorf("with a declared route, alpha -> bravo options = %+v", opts)
	}
}

func TestRoadNoModeServesIsAWarning(t *testing.T) {
	p := transportPack()
	p.TransportModes = []TransportModeDef{testMode("flight", "airport")}
	if err := p.Validate(); err != nil {
		t.Fatalf("a road nobody can use was refused rather than warned about: %v", err)
	}
	w := strings.Join(p.Warnings(), "\n")
	if !strings.Contains(w, `"alpha" -> "bravo"`) || !strings.Contains(w, `"bravo" -> "charlie"`) {
		t.Errorf("warnings do not name the unserved roads:\n%s", w)
	}
}

func TestShippedTransport(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.TransportModes) == 0 {
		t.Fatal("the shipped content declares no transport mode: nobody could travel")
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	// Every pair of cities the road network connects is reachable by at
	// least one mode.
	for _, from := range pack.Cities {
		for _, to := range pack.Cities {
			if from.Code == to.Code {
				continue
			}
			if _, err := snap.Routes().DistanceBetween(from.Code, to.Code); err != nil {
				continue
			}
			if len(snap.TransportOptions(from.Code, to.Code)) == 0 {
				t.Errorf("%s -> %s is on the map but no mode travels it", from.Code, to.Code)
			}
		}
	}
	for _, w := range pack.Warnings() {
		t.Errorf("shipped transport warning: %s", w)
	}
}
