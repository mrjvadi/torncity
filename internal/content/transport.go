package content

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
)

// This file holds transport content (configs/content/transport.yml): the
// facilities a city may have, the modes of transport, and which modes serve
// which route. The RULE that turns a mode and a distance into a fare and a
// duration lives in internal/domain/travel; this file only parses, validates
// and hands over the numbers.

// DemandDef is how recent departures move a mode's fare. See travel.Demand.
type DemandDef struct {
	// Window is how far back departures are counted, in real time: "30m".
	Window string `yaml:"window"`
	// FreeDepartures is how many departures inside the window leave the
	// fare untouched.
	FreeDepartures int `yaml:"free_departures"`
	// StepBPS is what each further departure adds to the multiplier.
	StepBPS int `yaml:"step_bps"`
	// MaxBPS caps the multiplier; 10000 means demand never moves the fare.
	MaxBPS int `yaml:"max_bps"`
}

// TransportModeDef is one entry of transport.yml's transport_modes.
type TransportModeDef struct {
	// Code is the stable identifier. It is stored on every journey and
	// travels in a button's address, so it is short, lower case, and never
	// changed after it ships.
	Code string `yaml:"code"`
	// Name is the authored display name, the fallback for a language with
	// no translation under transport_mode.<code>.
	Name string `yaml:"name"`
	// Public marks public transport: its fare is scaled by the origin city's
	// transit fare policy and paid into that city's treasury.
	Public bool `yaml:"public"`
	// Requires lists the facilities both ends of a route must have for the
	// mode to serve it (a flight needs an airport at each end).
	Requires []string `yaml:"requires"`
	// Speed is distance units per GAME hour.
	Speed int `yaml:"speed"`
	// Boarding is the fixed game time before moving: "20m".
	Boarding string `yaml:"boarding"`
	// BaseFare and FarePerDistance are minor currency units.
	BaseFare        int64 `yaml:"base_fare"`
	FarePerDistance int64 `yaml:"fare_per_distance"`
	// EnergyCost is the energy a departure costs.
	EnergyCost int       `yaml:"energy_cost"`
	Demand     DemandDef `yaml:"demand"`
	// Payment optionally narrows the methods its fare may be paid by
	// (payments.yml, service fare). Omitted means whatever fares accept.
	Payment []string `yaml:"payment"`
}

// Mode converts the definition to the domain value, or explains which field
// cannot be read.
func (m TransportModeDef) Mode() (travel.Mode, error) {
	boarding, err := transportDuration(m.Boarding, true)
	if err != nil {
		return travel.Mode{}, fmt.Errorf("boarding %w", err)
	}
	window, err := transportDuration(m.Demand.Window, false)
	if err != nil {
		return travel.Mode{}, fmt.Errorf("demand.window %w", err)
	}
	return travel.Mode{
		Code:       m.Code,
		Public:     m.Public,
		KMPerHour:  m.Speed,
		Boarding:   boarding,
		BaseFare:   m.BaseFare,
		FarePerKM:  m.FarePerDistance,
		EnergyCost: m.EnergyCost,
		Demand: travel.Demand{
			Window:         window,
			FreeDepartures: m.Demand.FreeDepartures,
			StepBPS:        m.Demand.StepBPS,
			MaxBPS:         m.Demand.MaxBPS,
		},
	}, nil
}

// transportDuration reads a whole-second Go duration. zeroOK says whether 0s
// is allowed.
func transportDuration(raw string, zeroOK bool) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("is required")
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 20m or 1h", raw)
	}
	if d < 0 || (!zeroOK && d == 0) {
		return 0, fmt.Errorf("%q must be positive", raw)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("%q is not a whole number of seconds", raw)
	}
	return d, nil
}

// FormatTransportDuration renders seconds as a file would state them, for a
// pack read back out of the database.
func FormatTransportDuration(seconds int64) string {
	return (time.Duration(seconds) * time.Second).String()
}

// MaxTransportCodeLength bounds a mode or facility code. A mode code rides in
// a button's callback address next to a city code and a price, and Telegram
// allows the whole address 64 bytes.
const MaxTransportCodeLength = 12

var transportCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Transport validation failures.
var (
	// ErrInvalidTransportCode means a facility or mode code that is empty,
	// too long, or not lower-case letters, digits and underscores.
	ErrInvalidTransportCode = errors.New("content: transport code must be 1-12 lower-case letters, digits or underscores, starting with a letter")
	// ErrDuplicateTransportCode means a facility or mode declared twice.
	ErrDuplicateTransportCode = errors.New("content: duplicate transport code")
	// ErrUnknownFacility means a mode or a city names a facility that
	// transport.yml does not declare.
	ErrUnknownFacility = errors.New("content: unknown facility")
	// ErrInvalidTransportMode means a mode's numbers are unusable.
	ErrInvalidTransportMode = errors.New("content: invalid transport mode")
	// ErrUnknownTransportMode means a route names a mode nobody declares.
	ErrUnknownTransportMode = errors.New("content: route names an unknown transport mode")
	// ErrModeNotServable means a route declares a mode whose required
	// facilities one of its ends lacks: a flight between two cities with no
	// airport.
	ErrModeNotServable = errors.New("content: route declares a mode an endpoint cannot serve")
	// ErrEmptyRouteModes means a route wrote `modes: []`: a road nobody may
	// use. Omit the key to let the modes be derived instead.
	ErrEmptyRouteModes = errors.New("content: route declares an empty mode list")
)

// validateTransport checks facilities, modes, the facilities each city
// lists, and the modes each route declares.
func (p *Pack) validateTransport(problems *[]error) {
	add := func(err error) { *problems = append(*problems, err) }

	facilities := map[string]struct{}{}
	for i, f := range p.Facilities {
		where := fmt.Sprintf("facilities[%d]", i)
		if !validTransportCode(f) {
			add(fmt.Errorf("%w: %s %q", ErrInvalidTransportCode, where, f))
			continue
		}
		if _, dup := facilities[f]; dup {
			add(fmt.Errorf("%w: facility %q (%s)", ErrDuplicateTransportCode, f, where))
			continue
		}
		facilities[f] = struct{}{}
	}

	modes := map[string]TransportModeDef{}
	for i, m := range p.TransportModes {
		where := fmt.Sprintf("transport_modes[%d]", i)
		if !validTransportCode(m.Code) {
			add(fmt.Errorf("%w: %s %q", ErrInvalidTransportCode, where, m.Code))
			continue
		}
		if _, dup := modes[m.Code]; dup {
			add(fmt.Errorf("%w: mode %q (%s)", ErrDuplicateTransportCode, m.Code, where))
			continue
		}
		modes[m.Code] = m
		if m.Name == "" {
			add(fmt.Errorf("%w: %s %q has no name", ErrInvalidTransportMode, where, m.Code))
		}
		seen := map[string]struct{}{}
		for _, f := range m.Requires {
			if _, ok := facilities[f]; !ok {
				add(fmt.Errorf("%w: %s %q requires %q, which transport.yml does not declare",
					ErrUnknownFacility, where, m.Code, f))
			}
			if _, dup := seen[f]; dup {
				add(fmt.Errorf("%w: %s %q requires %q twice", ErrDuplicateTransportCode, where, m.Code, f))
			}
			seen[f] = struct{}{}
		}
		mode, err := m.Mode()
		if err != nil {
			add(fmt.Errorf("%w: %s %q: %w", ErrInvalidTransportMode, where, m.Code, err))
			continue
		}
		if err := mode.Validate(); err != nil {
			add(fmt.Errorf("%w: %s: %w", ErrInvalidTransportMode, where, err))
		}
	}

	cityFacilities := map[string]map[string]struct{}{}
	for i, c := range p.Cities {
		where := fmt.Sprintf("cities[%d]", i)
		have := map[string]struct{}{}
		for _, f := range c.Facilities {
			if _, ok := facilities[f]; !ok {
				add(fmt.Errorf("%w: %s %q has facility %q, which transport.yml does not declare",
					ErrUnknownFacility, where, c.Code, f))
			}
			if _, dup := have[f]; dup {
				add(fmt.Errorf("%w: %s %q lists facility %q twice", ErrDuplicateTransportCode, where, c.Code, f))
			}
			have[f] = struct{}{}
		}
		cityFacilities[c.Code] = have
	}

	for i, r := range p.Routes {
		if r.Modes == nil {
			continue
		}
		where := fmt.Sprintf("routes[%d]", i)
		if len(r.Modes) == 0 {
			add(fmt.Errorf("%w: %s (%q -> %q)", ErrEmptyRouteModes, where, r.From, r.To))
			continue
		}
		seen := map[string]struct{}{}
		for _, code := range r.Modes {
			m, ok := modes[code]
			if !ok {
				add(fmt.Errorf("%w: %s (%q -> %q) names %q", ErrUnknownTransportMode, where, r.From, r.To, code))
				continue
			}
			if _, dup := seen[code]; dup {
				add(fmt.Errorf("%w: %s (%q -> %q) names %q twice", ErrDuplicateTransportCode, where, r.From, r.To, code))
			}
			seen[code] = struct{}{}
			for _, end := range [2]string{r.From, r.To} {
				if missing := missingFacility(m, cityFacilities[end]); missing != "" {
					add(fmt.Errorf("%w: %s declares %q, which needs %q, and %q has none",
						ErrModeNotServable, where, code, missing, end))
				}
			}
		}
	}
}

func validTransportCode(code string) bool {
	return len(code) <= MaxTransportCodeLength && transportCodePattern.MatchString(code)
}

// missingFacility names the first facility the mode requires that have
// lacks, or "".
func missingFacility(m TransportModeDef, have map[string]struct{}) string {
	for _, f := range m.Requires {
		if _, ok := have[f]; !ok {
			return f
		}
	}
	return ""
}

// ModeServesRoute reports whether a mode serves one route: the modes the
// route declares, or — when it declares none — every mode whose required
// facilities both ends have.
func (p *Pack) ModeServesRoute(m TransportModeDef, r RouteDef) bool {
	if r.Modes != nil {
		for _, code := range r.Modes {
			if code == m.Code {
				return true
			}
		}
		return false
	}
	return missingFacility(m, p.facilitiesOf(r.From)) == "" &&
		missingFacility(m, p.facilitiesOf(r.To)) == ""
}

func (p *Pack) facilitiesOf(code string) map[string]struct{} {
	for _, c := range p.Cities {
		if c.Code == code {
			out := make(map[string]struct{}, len(c.Facilities))
			for _, f := range c.Facilities {
				out[f] = struct{}{}
			}
			return out
		}
	}
	return nil
}

// ModeEdges is the part of the route network one mode serves.
func (p *Pack) ModeEdges(m TransportModeDef) []world.Edge {
	var edges []world.Edge
	for _, r := range p.Routes {
		if p.ModeServesRoute(m, r) {
			edges = append(edges, r.Edge())
		}
	}
	return edges
}

// transportWarnings reports what is loadable but leaves players stuck: roads
// no declared mode serves. A pack that declares no modes at all is not
// warned about here: it is a pack assembled without transport (a test's, or
// a partial one), and the shipped content test requires modes separately.
func (p *Pack) transportWarnings() []string {
	if len(p.Routes) == 0 || len(p.TransportModes) == 0 {
		return nil
	}
	var out []string
	for _, r := range p.Routes {
		served := false
		for _, m := range p.TransportModes {
			if p.ModeServesRoute(m, r) {
				served = true
				break
			}
		}
		if !served {
			out = append(out, fmt.Sprintf("route %q -> %q is served by no transport mode: nobody can travel it", r.From, r.To))
		}
	}
	sort.Strings(out)
	return out
}

// TransportOption is one way to make one journey: a mode, and the distance
// by that mode's own network.
type TransportOption struct {
	Mode travel.Mode
	// Name is the authored display name, the fallback for an untranslated
	// mode.
	Name       string
	DistanceKM int
}

// transportNetwork is a mode with the part of the network it serves, built
// once per snapshot.
type transportNetwork struct {
	def    TransportModeDef
	mode   travel.Mode
	routes world.Routes
}

// buildTransport converts every mode and builds its network. The pack is
// already valid, so an error here means this file disagrees with the domain.
func buildTransport(p *Pack) ([]transportNetwork, error) {
	out := make([]transportNetwork, 0, len(p.TransportModes))
	for _, def := range p.TransportModes {
		mode, err := def.Mode()
		if err != nil {
			return nil, fmt.Errorf("mode %q: %w", def.Code, err)
		}
		routes, err := world.NewRoutes(p.ModeEdges(def))
		if err != nil {
			return nil, fmt.Errorf("mode %q: %w", def.Code, err)
		}
		out = append(out, transportNetwork{def: def, mode: mode, routes: routes})
	}
	return out, nil
}

// TransportOptions lists every mode that can carry a player from one city to
// another, in the order transport.yml declares them, each with the distance
// by its own network. An empty list means no mode connects the two.
func (s *Snapshot) TransportOptions(from, to string) []TransportOption {
	var out []TransportOption
	for _, n := range s.transport {
		d, err := n.routes.DistanceBetween(from, to)
		if err != nil {
			continue
		}
		out = append(out, TransportOption{Mode: n.mode, Name: n.def.Name, DistanceKM: d})
	}
	return out
}

// TransportModes returns the mode definitions of this version, in file order.
// The slice is a copy.
func (s *Snapshot) TransportModes() []TransportModeDef {
	out := make([]TransportModeDef, 0, len(s.transport))
	for _, n := range s.transport {
		out = append(out, n.def)
	}
	return out
}

// NearestByAnyMode is the shortest distance from one city to another by any
// single mode, or false when no mode connects them. It is what the map
// lists: a destination is one some mode actually reaches.
func (s *Snapshot) NearestByAnyMode(from, to string) (int, bool) {
	best, found := 0, false
	for _, n := range s.transport {
		d, err := n.routes.DistanceBetween(from, to)
		if err != nil {
			continue
		}
		if !found || d < best {
			best, found = d, true
		}
	}
	return best, found
}
