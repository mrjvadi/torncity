// Package place holds the rules of where a player is inside their city.
//
// A city is not one room. It has places — the city centre, the bazaar, the
// business district with its bank, the university, the police station, the
// train station — and the player stands at exactly one of them. What they can
// do depends on where they stand: a service is found at its place, and a
// crime is committed where the thief is, against whoever is there.
//
// Which places exist, which cities have them, how long it takes to walk to
// each and what each holds are CONTENT (configs/content/places.yml). This
// package holds the rules: which places a city has, what a move costs and
// how long it takes on the one game clock, and where an arrival or a shift
// puts a player. It reads no clock and no file.
package place

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

// Failures.
var (
	// ErrInvalidPlaces means a place list is unusable.
	ErrInvalidPlaces = errors.New("place: invalid places")
	// ErrUnknownPlace means a move to a place the city does not have.
	ErrUnknownPlace = errors.New("place: the city has no such place")
	// ErrAlreadyThere means a move to where the player already stands.
	ErrAlreadyThere = errors.New("place: already there")
)

// Service is something found at a place. The set is closed: code asks
// "where is the bank?", and a service invented in content would be one no
// code offers.
type Service string

const (
	ServiceBank           Service = "bank"
	ServicePolice         Service = "police"
	ServiceCityHall       Service = "city_hall"
	ServiceMarket         Service = "market"
	ServiceAuctionHouse   Service = "auction_house"
	ServiceUniversity     Service = "university"
	ServiceTrainingCenter Service = "training_center"
)

var services = []Service{
	ServiceBank, ServicePolice, ServiceCityHall, ServiceMarket, ServiceAuctionHouse,
	ServiceUniversity, ServiceTrainingCenter,
}

// Services returns every service. The slice is a copy.
func Services() []Service { return append([]Service(nil), services...) }

// Valid reports whether s is a known service.
func (s Service) Valid() bool {
	for _, k := range services {
		if k == s {
			return true
		}
	}
	return false
}

// MaxMoveTime caps how far across a city a place may be, in game time.
const MaxMoveTime = 24 * time.Hour

// MaxMoveEnergy caps what a move may cost.
const MaxMoveEnergy = 100

// Place is one place a city may have.
type Place struct {
	Code string
	// Default is where a player with no other place stands: the centre.
	Default bool
	// Cities limits the place to these city codes; empty means every city
	// that has the facilities below.
	Cities []string
	// Requires lists the facilities a city needs to have the place: a
	// train station only where there is a railway.
	Requires []string
	// MoveTime is how long, in GAME time, walking there takes from anywhere
	// else in the city.
	MoveTime time.Duration
	// Energy is what the walk costs.
	Energy int
	// Services are what is found here.
	Services []Service
	// Arrivals are the transport modes that land and depart here.
	Arrivals []string
	// WorkCategories are the career categories whose workers work here.
	WorkCategories []string
}

// Has reports whether the place offers a service.
func (p Place) Has(s Service) bool {
	for _, x := range p.Services {
		if x == s {
			return true
		}
	}
	return false
}

// InCity reports whether a city with this code and these facilities has the
// place.
func (p Place) InCity(cityCode string, facilities []string) bool {
	if len(p.Cities) > 0 && !contains(p.Cities, cityCode) {
		return false
	}
	for _, need := range p.Requires {
		if !contains(facilities, need) {
			return false
		}
	}
	return true
}

// Validate checks a place list: codes present and distinct, exactly one
// default that every city has, times and energy in bounds, known services,
// and no mode or career category claimed by two places.
func Validate(places []Place) error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidPlaces, fmt.Sprintf(format, args...)))
	}
	codes := map[string]bool{}
	modes := map[string]string{}
	categories := map[string]string{}
	defaults := 0
	for i, p := range places {
		switch {
		case p.Code == "":
			bad("place %d has no code", i)
		case codes[p.Code]:
			bad("place %q is declared twice", p.Code)
		}
		codes[p.Code] = true
		if p.Default {
			defaults++
			if len(p.Cities) > 0 || len(p.Requires) > 0 {
				bad("the default place %q must be in every city", p.Code)
			}
		}
		if p.MoveTime <= 0 || p.MoveTime > MaxMoveTime {
			bad("place %q move time %s is outside (0, %s]", p.Code, p.MoveTime, MaxMoveTime)
		}
		if p.Energy < 0 || p.Energy > MaxMoveEnergy {
			bad("place %q energy %d is outside 0..%d", p.Code, p.Energy, MaxMoveEnergy)
		}
		for _, s := range p.Services {
			if !s.Valid() {
				bad("place %q offers unknown service %q", p.Code, s)
			}
		}
		for _, m := range p.Arrivals {
			if other, taken := modes[m]; taken {
				bad("transport mode %q stops at both %q and %q", m, other, p.Code)
			}
			modes[m] = p.Code
		}
		for _, c := range p.WorkCategories {
			if other, taken := categories[c]; taken {
				bad("career category %q works at both %q and %q", c, other, p.Code)
			}
			categories[c] = p.Code
		}
	}
	if len(places) > 0 && defaults != 1 {
		bad("exactly one place must be the default, found %d", defaults)
	}
	return errors.Join(errs...)
}

// Map is the places of one city, in content order.
type Map struct {
	City   string
	Places []Place
}

// CityMap returns the places a city has, in the order they are listed.
func CityMap(all []Place, cityCode string, facilities []string) Map {
	m := Map{City: cityCode}
	for _, p := range all {
		if p.InCity(cityCode, facilities) {
			m.Places = append(m.Places, p)
		}
	}
	return m
}

// Find returns the city's place with this code.
func (m Map) Find(code string) (Place, bool) {
	for _, p := range m.Places {
		if p.Code == code {
			return p, true
		}
	}
	return Place{}, false
}

// Default returns the city's default place. ok is false only for an empty
// map.
func (m Map) Default() (Place, bool) {
	for _, p := range m.Places {
		if p.Default {
			return p, true
		}
	}
	return Place{}, false
}

// Current resolves a stored place code: the place itself when the city has
// it, else the default. A player whose recorded place was removed from the
// content, or who never had one, stands at the default.
func (m Map) Current(code string) (Place, bool) {
	if code != "" {
		if p, ok := m.Find(code); ok {
			return p, true
		}
	}
	return m.Default()
}

// ForService returns the city's place offering a service.
func (m Map) ForService(s Service) (Place, bool) {
	for _, p := range m.Places {
		if p.Has(s) {
			return p, true
		}
	}
	return Place{}, false
}

// ForMode returns the city's place a transport mode stops at, or the
// default when no place claims it.
func (m Map) ForMode(mode string) (Place, bool) {
	for _, p := range m.Places {
		if contains(p.Arrivals, mode) {
			return p, true
		}
	}
	return m.Default()
}

// ForWork returns the city's place a career category works at, or the
// default when no place claims it.
func (m Map) ForWork(category string) (Place, bool) {
	for _, p := range m.Places {
		if contains(p.WorkCategories, category) {
			return p, true
		}
	}
	return m.Default()
}

// Move is a walk from one place to another, on the game clock.
type Move struct {
	From, To  string
	Energy    int
	StartedAt time.Time
	ArrivesAt time.Time
}

// Duration is the real wait of the move.
func (m Move) Duration() time.Duration { return m.ArrivesAt.Sub(m.StartedAt) }

// StartMove plans a walk from the player's current place to another place of
// the same city. The walk takes the destination's MoveTime in game time,
// waited in real time through the game clock, and costs its Energy.
func StartMove(m Map, from, to string, now time.Time, scale gametime.Scale) (Move, error) {
	dest, ok := m.Find(to)
	if !ok {
		return Move{}, fmt.Errorf("%w: %q in %q", ErrUnknownPlace, to, m.City)
	}
	here, _ := m.Current(from)
	if here.Code == dest.Code {
		return Move{}, ErrAlreadyThere
	}
	return Move{
		From: here.Code, To: dest.Code, Energy: dest.Energy,
		StartedAt: now, ArrivesAt: now.Add(scale.RealWait(dest.MoveTime)),
	}, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
