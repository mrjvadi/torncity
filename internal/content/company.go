package content

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// This file holds player companies (configs/content/companies.yml): the kinds
// of business a player may found, the NPC market of each city they sell into,
// what the population buys per category, and the words no company name may
// contain. The rules — who may do what, where money may go, how a city's
// population divides its spending — are internal/domain/company.

// ErrInvalidCompanyContent means the company content is unusable.
var ErrInvalidCompanyContent = errors.New("content: invalid company content")

// CompanyTypeDef is one kind of business.
type CompanyTypeDef struct {
	Code string `yaml:"code" json:"code"`
	// Name is the fallback display name; company_type.<code> in the
	// locales is what a player reads.
	Name string `yaml:"name" json:"name"`
	// Category is the NPC demand category it sells into (company_demand).
	Category string `yaml:"category" json:"category"`
	// Place is where in the city it operates and its staff work.
	Place string `yaml:"place" json:"place"`
	// Careers are the careers (jobs.yml) it hires into.
	Careers []string `yaml:"careers" json:"careers"`
	// FoundingFee is the registration fee before the city's multiplier
	// (city.company_registration); Upkeep what a full period costs; both
	// minor units.
	FoundingFee int64 `yaml:"founding_fee" json:"founding_fee"`
	Upkeep      int64 `yaml:"upkeep" json:"upkeep"`
	// MaxStaff bounds employees plus open positions.
	MaxStaff int `yaml:"max_staff" json:"max_staff"`
	// UnitPrice, BaseUnits, UnitsPerShift, StaffTarget, MinQualityBPS,
	// PriceMinBPS, PriceMaxBPS and ElasticityBPS are the market figures of
	// internal/domain/company.Type.
	UnitPrice     int64 `yaml:"unit_price" json:"unit_price"`
	BaseUnits     int64 `yaml:"base_units" json:"base_units"`
	UnitsPerShift int64 `yaml:"units_per_shift" json:"units_per_shift"`
	StaffTarget   int   `yaml:"staff_target" json:"staff_target"`
	MinQualityBPS int   `yaml:"min_quality_bps" json:"min_quality_bps"`
	PriceMinBPS   int   `yaml:"price_min_bps" json:"price_min_bps"`
	PriceMaxBPS   int   `yaml:"price_max_bps" json:"price_max_bps"`
	ElasticityBPS int   `yaml:"elasticity_bps" json:"elasticity_bps"`
	// Produces lists the archetypes (items.yml) its engineers may design
	// and its floor may make: a factory makes devices, a restaurant food.
	// None means it designs and makes no goods of its own.
	Produces []string `yaml:"produces,omitempty" json:"produces,omitempty"`
	// Stocked lists the item or component categories (items.yml) whose
	// goods from its warehouse it sells to the city's population: its NPC
	// sales are then bounded by that stock, and the units sold leave its
	// warehouse. None means it sells a service, in abstract units.
	Stocked []string `yaml:"stocked,omitempty" json:"stocked,omitempty"`
	// Sector is the part of the economy it belongs to (civilian when
	// omitted; defence…): a buyer class of export control, so a restricted
	// technology or good can go to some sectors only.
	Sector string `yaml:"sector,omitempty" json:"sector,omitempty"`
}

// DefaultSector is the sector of a kind of business that names none.
const DefaultSector = "civilian"

// SectorCode is the kind of business's sector.
func (d CompanyTypeDef) SectorCode() string {
	if d.Sector == "" {
		return DefaultSector
	}
	return d.Sector
}

// Makes reports whether the kind of business designs and makes archetype.
func (d CompanyTypeDef) Makes(archetype string) bool {
	for _, a := range d.Produces {
		if a == archetype {
			return true
		}
	}
	return false
}

// Type converts the definition to the domain value.
func (d CompanyTypeDef) Type() company.Type {
	return company.Type{
		Code: d.Code, Category: d.Category, Careers: append([]string(nil), d.Careers...), Place: d.Place,
		FoundingFee: money.FromMinor(d.FoundingFee), Upkeep: money.FromMinor(d.Upkeep), MaxStaff: d.MaxStaff,
		UnitPrice: money.FromMinor(d.UnitPrice), BaseUnits: d.BaseUnits, UnitsPerShift: d.UnitsPerShift,
		StaffTarget: d.StaffTarget, MinQualityBPS: d.MinQualityBPS,
		PriceMinBPS: d.PriceMinBPS, PriceMaxBPS: d.PriceMaxBPS, ElasticityBPS: d.ElasticityBPS,
	}
}

// CompanyMarketDef is one city's NPC market.
type CompanyMarketDef struct {
	City string `yaml:"city" json:"city"`
	// Population is the NPC population that buys from the city's
	// companies. It is content until the population simulation of ADR 0008
	// moves cities.population; nothing reads that column for this.
	Population int64 `yaml:"population" json:"population"`
	// WealthBPS scales spending: 10000 is an average city.
	WealthBPS int `yaml:"wealth_bps" json:"wealth_bps"`
	// BudgetPerThousand is the most the population spends per period, per
	// thousand residents, minor units.
	BudgetPerThousand int64 `yaml:"budget_per_thousand" json:"budget_per_thousand"`
}

// CompanyDemandDef is what the population buys of one category, per period.
type CompanyDemandDef struct {
	Category string `yaml:"category" json:"category"`
	// PerThousand is units per thousand residents per period.
	PerThousand int64 `yaml:"per_thousand" json:"per_thousand"`
}

// validateCompanies checks the company content against the careers, the
// places and the cities it names.
func (p *Pack) validateCompanies(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidCompanyContent, fmt.Sprintf(format, args...)))
	}
	categories := map[string]bool{}
	for _, d := range p.CompanyDemand {
		switch {
		case !transportCodePattern.MatchString(d.Category):
			bad("demand category %q is not a code", d.Category)
		case categories[d.Category]:
			bad("demand category %q twice", d.Category)
		case d.PerThousand < 0 || d.PerThousand > company.MaxUnits:
			bad("demand %q per_thousand %d", d.Category, d.PerThousand)
		}
		categories[d.Category] = true
	}
	careers := map[string]bool{}
	for _, c := range p.Careers {
		careers[c.Code] = true
	}
	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	seen := map[string]bool{}
	for i, d := range p.CompanyTypes {
		if !transportCodePattern.MatchString(d.Code) || len(d.Code) > 24 {
			bad("company_types[%d] code %q must be a short lower-case code", i, d.Code)
		}
		if seen[d.Code] {
			bad("company type %q twice", d.Code)
		}
		seen[d.Code] = true
		if d.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: company_types[%d] %q", ErrMissingDisplayName, i, d.Code))
		}
		if err := d.Type().Validate(); err != nil {
			bad("%v", err)
		}
		if !categories[d.Category] {
			bad("company type %q sells into %q, which company_demand does not declare", d.Code, d.Category)
		}
		for _, c := range d.Careers {
			if !careers[c] {
				bad("company type %q hires into unknown career %q", d.Code, c)
			}
		}
		if len(p.Venues) > 0 && !places[d.Place] {
			bad("company type %q operates at unknown place %q", d.Code, d.Place)
		}
	}
	cities := map[string]bool{}
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	markets := map[string]bool{}
	for _, m := range p.CompanyMarkets {
		switch {
		case !cities[m.City]:
			bad("company market names unknown city %q", m.City)
		case markets[m.City]:
			bad("company market for %q twice", m.City)
		}
		markets[m.City] = true
		if err := (company.Market{Population: m.Population, WealthBPS: m.WealthBPS,
			BudgetPerThousand: m.BudgetPerThousand}).Validate(); err != nil {
			bad("market %q: %v", m.City, err)
		}
	}
	words := map[string]bool{}
	for i, w := range p.CompanyReservedNames {
		k := company.NameKey(w)
		switch {
		case k == "":
			bad("company_reserved_names[%d] is empty", i)
		case words[k]:
			bad("company_reserved_names[%d] %q twice", i, w)
		}
		words[k] = true
	}
}

// companyContent is the company content of a snapshot.
type companyContent struct {
	types    []CompanyTypeDef
	byCode   map[string]CompanyTypeDef
	markets  map[string]CompanyMarketDef
	demand   map[string]int64
	reserved []string
}

// buildCompanies indexes the company content. The pack has been validated.
func (s *Snapshot) buildCompanies(p *Pack) {
	c := companyContent{
		types:    append([]CompanyTypeDef(nil), p.CompanyTypes...),
		byCode:   make(map[string]CompanyTypeDef, len(p.CompanyTypes)),
		markets:  make(map[string]CompanyMarketDef, len(p.CompanyMarkets)),
		demand:   make(map[string]int64, len(p.CompanyDemand)),
		reserved: append([]string(nil), p.CompanyReservedNames...),
	}
	for _, t := range p.CompanyTypes {
		c.byCode[t.Code] = t
	}
	for _, m := range p.CompanyMarkets {
		c.markets[m.City] = m
	}
	for _, d := range p.CompanyDemand {
		c.demand[d.Category] = d.PerThousand
	}
	s.companies = c
}

// CompanyTypes lists the kinds of business, in file order.
func (s *Snapshot) CompanyTypes() []CompanyTypeDef {
	return append([]CompanyTypeDef(nil), s.companies.types...)
}

// CompanyType returns one kind of business, as authored and as the domain
// reads it.
func (s *Snapshot) CompanyType(code string) (CompanyTypeDef, company.Type, bool) {
	d, ok := s.companies.byCode[code]
	if !ok {
		return CompanyTypeDef{}, company.Type{}, false
	}
	return d, d.Type(), true
}

// CompanyMarket returns a city's NPC market, without the operator's cap
// (config), and whether the city has one. A city without one has no NPC
// customers: its companies sell nothing to the population.
func (s *Snapshot) CompanyMarket(cityCode string) (CompanyMarketDef, company.Market, bool) {
	d, ok := s.companies.markets[cityCode]
	if !ok {
		return CompanyMarketDef{}, company.Market{}, false
	}
	demand := make(map[string]int64, len(s.companies.demand))
	for k, v := range s.companies.demand {
		demand[k] = v
	}
	return d, company.Market{Population: d.Population, WealthBPS: d.WealthBPS,
		BudgetPerThousand: d.BudgetPerThousand, Demand: demand}, true
}

// CompanyDemandCategories lists the demand categories, sorted.
func (s *Snapshot) CompanyDemandCategories() []string {
	out := make([]string, 0, len(s.companies.demand))
	for k := range s.companies.demand {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CompanyReservedNames lists the words no company name may contain.
func (s *Snapshot) CompanyReservedNames() []string {
	return append([]string(nil), s.companies.reserved...)
}
