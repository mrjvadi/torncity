package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/place"
)

// This file holds the content side of city places (configs/content/places.yml):
// which places a city has, how long it takes to walk to each, and what each
// holds. The rules — which city has which place, what a move costs and
// takes, where an arrival or a shift puts a player — are
// internal/domain/place; the crime engine reads the same entries as venues.

// Validation failures for places.
var (
	// ErrInvalidPlaces means the place list is unusable.
	ErrInvalidPlaces = errors.New("content: invalid places")
	// ErrUnknownPlaceCity means a place names a city nobody declares.
	ErrUnknownPlaceCity = errors.New("content: place names an unknown city")
	// ErrUnknownPlaceFacility means a place requires a facility
	// transport.yml does not declare.
	ErrUnknownPlaceFacility = errors.New("content: place requires an unknown facility")
)

// Place converts the definition to the domain value. The pack has been
// validated, so an unreadable move time reads as zero.
func (v VenueDef) Place() place.Place {
	move, _ := optionalDuration(v.MoveTime)
	out := place.Place{
		Code:           v.Code,
		Default:        v.Default,
		Cities:         append([]string(nil), v.Cities...),
		Requires:       append([]string(nil), v.Requires...),
		MoveTime:       move,
		Energy:         v.Energy,
		Arrivals:       append([]string(nil), v.Arrivals...),
		WorkCategories: append([]string(nil), v.WorkCategories...),
	}
	for _, s := range v.Services {
		out.Services = append(out.Services, place.Service(s))
	}
	return out
}

// validatePlaces checks the place list against the domain rules and against
// the cities and facilities it names.
func (p *Pack) validatePlaces(problems *[]error) {
	if len(p.Venues) == 0 {
		// Places are optional as a whole: a pack without any (a test
		// fixture) has a city without places, where nothing is located.
		return
	}
	add := func(err error) { *problems = append(*problems, err) }
	cities := map[string]bool{}
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	facilities := map[string]bool{}
	for _, f := range p.Facilities {
		facilities[f] = true
	}
	places := make([]place.Place, 0, len(p.Venues))
	for i, v := range p.Venues {
		if v.Name == "" {
			add(fmt.Errorf("%w: places[%d] %q", ErrMissingDisplayName, i, v.Code))
		}
		if v.MoveTime == "" {
			add(fmt.Errorf("%w: place %q has no move_time", ErrInvalidPlaces, v.Code))
		} else if d, err := optionalDuration(v.MoveTime); err != nil {
			add(fmt.Errorf("%w: place %q move_time %q: %v", ErrInvalidDuration, v.Code, v.MoveTime, err))
		} else if d%time.Second != 0 {
			add(fmt.Errorf("%w: place %q move_time %q is not whole seconds", ErrInvalidPlaces, v.Code, v.MoveTime))
		}
		for _, c := range v.Cities {
			if !cities[c] {
				add(fmt.Errorf("%w: place %q names %q", ErrUnknownPlaceCity, v.Code, c))
			}
		}
		for _, f := range v.Requires {
			if !facilities[f] {
				add(fmt.Errorf("%w: place %q requires %q", ErrUnknownPlaceFacility, v.Code, f))
			}
		}
		places = append(places, v.Place())
	}
	if err := place.Validate(places); err != nil {
		add(fmt.Errorf("%w: %w", ErrInvalidPlaces, err))
	}
}

// buildPlaces indexes the places. The pack has been validated.
func (s *Snapshot) buildPlaces(p *Pack) {
	s.places = make([]place.Place, 0, len(p.Venues))
	for _, v := range p.Venues {
		s.places = append(s.places, v.Place())
	}
}

// CityMap returns the places of the city with this code, in content order.
func (s *Snapshot) CityMap(cityCode string) place.Map {
	return place.CityMap(s.places, cityCode, s.crime.facilities[cityCode])
}

// PlaceDef returns the authored definition of a place.
func (s *Snapshot) PlaceDef(code string) (PlaceDef, bool) {
	for _, v := range s.crime.venueDefs {
		if v.Code == code {
			return v, true
		}
	}
	return PlaceDef{}, false
}
