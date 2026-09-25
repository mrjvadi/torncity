package content

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/property"
)

// This file holds property (configs/content/property.yml;
// docs/adr/0024-property-and-politics.md): the kinds of property a city may
// sell — a home, a flat, shop premises, a plot — what each costs, what it
// costs to keep and whether a player can live in it; how many of each each
// city sells and at what price level; and how the price rises as a city
// sells. The rules are internal/domain/property; the tax is policy
// (city.property_tax).

// ErrInvalidPropertyContent means property.yml is unusable.
var ErrInvalidPropertyContent = errors.New("content: invalid property content")

// PropertyTypeDef is one kind of property.
type PropertyTypeDef struct {
	Code      string `yaml:"code" json:"code"`
	Name      string `yaml:"name" json:"name"`
	Kind      string `yaml:"kind" json:"kind"`
	Size      int    `yaml:"size" json:"size"`
	Quality   int    `yaml:"quality" json:"quality"`
	BasePrice int64  `yaml:"base_price" json:"base_price"`
	Upkeep    int64  `yaml:"upkeep" json:"upkeep"`
	// Home says a player can live in it: owning or renting it makes them a
	// resident of its city, and they rest there.
	Home       bool `yaml:"home,omitempty" json:"home,omitempty"`
	RestEnergy int  `yaml:"rest_energy,omitempty" json:"rest_energy,omitempty"`
	// Place is where in its city it stands (places.yml): where its owner
	// rests.
	Place string `yaml:"place" json:"place"`
}

// Type is the domain's value.
func (d PropertyTypeDef) Type() property.Type {
	return property.Type{Code: d.Code, Kind: d.Kind, Size: d.Size, Quality: d.Quality, BasePrice: d.BasePrice,
		Upkeep: d.Upkeep, Home: d.Home, RestEnergy: d.RestEnergy}
}

// PropertyMarketDef is what one city's government sells.
type PropertyMarketDef struct {
	City string `yaml:"city" json:"city"`
	// PriceBPS is the city's price level, 10000 = as authored.
	PriceBPS int64 `yaml:"price_bps" json:"price_bps"`
	// Stock is how many units of each type the city sells in all.
	Stock map[string]int `yaml:"stock" json:"stock"`
}

// PropertyDef is property.yml's property section.
type PropertyDef struct {
	// DemandStepBPS and DemandMaxBPS are how the price rises with each unit
	// sold (property.Demand).
	DemandStepBPS int64 `yaml:"demand_step_bps" json:"demand_step_bps"`
	DemandMaxBPS  int64 `yaml:"demand_max_bps" json:"demand_max_bps"`
}

// Demand is the domain's value.
func (d PropertyDef) Demand() property.Demand {
	return property.Demand{StepBPS: d.DemandStepBPS, MaxBPS: d.DemandMaxBPS}
}

// propertyContent is the snapshot's property.
type propertyContent struct {
	def     *PropertyDef
	types   []PropertyTypeDef
	markets map[string]PropertyMarketDef
}

func (s *Snapshot) buildProperty(p *Pack) {
	s.property.types = append([]PropertyTypeDef(nil), p.PropertyTypes...)
	s.property.markets = map[string]PropertyMarketDef{}
	for _, m := range p.PropertyMarkets {
		s.property.markets[m.City] = m
	}
	if len(p.Property) > 0 {
		d := p.Property[0]
		s.property.def = &d
	}
}

// Property returns the property section, and whether the content has one.
func (s *Snapshot) Property() (PropertyDef, bool) {
	if s.property.def == nil {
		return PropertyDef{}, false
	}
	return *s.property.def, true
}

// PropertyTypes lists the types, in file order.
func (s *Snapshot) PropertyTypes() []PropertyTypeDef {
	return append([]PropertyTypeDef(nil), s.property.types...)
}

// PropertyType finds a type by code.
func (s *Snapshot) PropertyType(code string) (PropertyTypeDef, bool) {
	for _, t := range s.property.types {
		if t.Code == code {
			return t, true
		}
	}
	return PropertyTypeDef{}, false
}

// PropertyMarket is what one city sells; ok is false for a city that sells
// none.
func (s *Snapshot) PropertyMarket(city string) (PropertyMarketDef, bool) {
	m, ok := s.property.markets[city]
	return m, ok
}

// validateProperty checks property.yml against the cities and the places.
func (p *Pack) validateProperty(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidPropertyContent, fmt.Sprintf(format, args...)))
	}
	if len(p.Property) > 1 {
		bad("property is declared %d times", len(p.Property))
	}
	if len(p.Property) == 1 {
		if err := p.Property[0].Demand().Validate(); err != nil {
			bad("%v", err)
		}
	} else if len(p.PropertyTypes) > 0 || len(p.PropertyMarkets) > 0 {
		bad("property types or markets without a property section")
	}
	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	types := map[string]bool{}
	for i, t := range p.PropertyTypes {
		where := fmt.Sprintf("property_types[%d] %q", i, t.Code)
		if !transportCodePattern.MatchString(t.Code) || types[t.Code] {
			bad("%s: code is not a code or repeated", where)
		}
		types[t.Code] = true
		if t.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrMissingDisplayName, where))
		}
		if err := t.Type().Validate(); err != nil {
			bad("%s: %v", where, err)
		}
		if t.Place == "" || !places[t.Place] {
			bad("%s: place %q is not a place of places.yml", where, t.Place)
		}
	}
	cities := map[string]bool{}
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	seen := map[string]bool{}
	for i, m := range p.PropertyMarkets {
		where := fmt.Sprintf("property_markets[%d] %q", i, m.City)
		if !cities[m.City] || seen[m.City] {
			bad("%s: city is not a city of cities.yml, or repeated", where)
		}
		seen[m.City] = true
		if m.PriceBPS < 1000 || m.PriceBPS > 50000 {
			bad("%s: price_bps %d is outside 1000..50000", where, m.PriceBPS)
		}
		codes := make([]string, 0, len(m.Stock))
		for code := range m.Stock {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		for _, code := range codes {
			if !types[code] {
				bad("%s: stock names unknown type %q", where, code)
			}
			if n := m.Stock[code]; n < 0 || n > 100000 {
				bad("%s: stock of %q is %d", where, code, n)
			}
		}
	}
}
