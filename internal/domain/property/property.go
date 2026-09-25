// Package property holds the rules of real estate: what a city asks for a
// home, a flat, shop premises or a plot of land; what an owner owes every
// city period in upkeep and tax, and what happens when they cannot pay; what
// a tenant owes in rent, and when they are evicted.
//
// What kinds of property exist, what each costs and how many a city sells
// are CONTENT (configs/content/property.yml); the tax rate is a city's
// POLICY (city.property_tax, decided by its council). The package reads no
// clock and no file.
//
// # Never negative
//
// A charge takes only what the payer holds. Tax is paid first (it is the
// city's), then upkeep; what could not be paid is carried as the property's
// debt, and a property that ends foreclosure_periods city periods in a row
// in debt goes back to the city. Rent is all or nothing: a period's rent is
// paid in full or not at all, and a tenant who owes eviction_periods periods
// in a row is evicted.
package property

import (
	"errors"
	"fmt"
)

// Kinds of property: a closed set, because code reads each.
const (
	KindHouse     = "house"
	KindApartment = "apartment"
	KindShop      = "shop"
	KindLand      = "land"
)

// Kinds lists every kind.
var Kinds = []string{KindHouse, KindApartment, KindShop, KindLand}

// BasisPoints is the whole.
const BasisPoints = 10000

// ErrInvalid means property content the rules cannot use.
var ErrInvalid = errors.New("property: invalid")

// Type is one kind of property a city may sell.
type Type struct {
	Code    string
	Kind    string
	Size    int
	Quality int
	// BasePrice is what the city asks before its price level and demand.
	BasePrice int64
	// Upkeep is what it costs its owner each city period.
	Upkeep int64
	// Home says a player can live in it: it makes them a resident of its
	// city, and they rest there.
	Home bool
	// RestEnergy is the energy a rest at this home gives back.
	RestEnergy int
}

// Validate checks a type.
func (t Type) Validate() error {
	known := false
	for _, k := range Kinds {
		known = known || k == t.Kind
	}
	switch {
	case t.Code == "":
		return fmt.Errorf("%w: a type with no code", ErrInvalid)
	case !known:
		return fmt.Errorf("%w: %q has unknown kind %q", ErrInvalid, t.Code, t.Kind)
	case t.Size < 1 || t.Size > 100000:
		return fmt.Errorf("%w: %q size %d", ErrInvalid, t.Code, t.Size)
	case t.Quality < 1 || t.Quality > 5:
		return fmt.Errorf("%w: %q quality %d is outside 1..5", ErrInvalid, t.Code, t.Quality)
	case t.BasePrice < 1 || t.BasePrice > 1_000_000_000:
		return fmt.Errorf("%w: %q base price %d", ErrInvalid, t.Code, t.BasePrice)
	case t.Upkeep < 0 || t.Upkeep > t.BasePrice:
		return fmt.Errorf("%w: %q upkeep %d", ErrInvalid, t.Code, t.Upkeep)
	case t.RestEnergy < 0 || t.RestEnergy > 1000 || (!t.Home && t.RestEnergy > 0):
		return fmt.Errorf("%w: %q rest energy %d (only a home gives any)", ErrInvalid, t.Code, t.RestEnergy)
	}
	return nil
}

// Demand is how a city's price rises as it sells: each unit of a type sold
// and still owned adds StepBPS to the price, up to MaxBPS of it.
type Demand struct {
	StepBPS int64
	MaxBPS  int64
}

// Validate checks the demand rule.
func (d Demand) Validate() error {
	if d.StepBPS < 0 || d.StepBPS > BasisPoints || d.MaxBPS < BasisPoints || d.MaxBPS > 5*BasisPoints {
		return fmt.Errorf("%w: demand step %d bps, max %d bps", ErrInvalid, d.StepBPS, d.MaxBPS)
	}
	return nil
}

// CityPrice is what a city asks for one unit of a type: the base price at
// the city's price level (priceBPS, 10000 = as authored), raised by demand
// for every unit of it already sold.
func CityPrice(t Type, priceBPS int64, sold int, d Demand) int64 {
	level := t.BasePrice * max(priceBPS, 0) / BasisPoints
	mult := min(BasisPoints+int64(max(sold, 0))*d.StepBPS, max(d.MaxBPS, BasisPoints))
	return max(level*mult/BasisPoints, 1)
}

// Tax is a period's tax on a property of a value at a rate.
func Tax(value, rateBPS int64) int64 {
	if value <= 0 || rateBPS <= 0 {
		return 0
	}
	return value * rateBPS / BasisPoints
}

// Owed is what an owner owes for one period, before paying: this period's
// upkeep and tax and the debt carried in.
type Owed struct {
	Upkeep, Tax         int64
	UpkeepDebt, TaxDebt int64
}

// Paid is what a period's charge took and left.
type Paid struct {
	TaxPaid, UpkeepPaid int64
	TaxDebt, UpkeepDebt int64
}

// InDebt reports whether anything was left owing.
func (p Paid) InDebt() bool { return p.TaxDebt > 0 || p.UpkeepDebt > 0 }

// Charge takes what is owed from what the owner holds: tax first, debt
// before this period's, then upkeep likewise. It never takes more than
// available.
func Charge(o Owed, available int64) Paid {
	left := max(available, 0)
	taxDue := max(o.TaxDebt, 0) + max(o.Tax, 0)
	upDue := max(o.UpkeepDebt, 0) + max(o.Upkeep, 0)
	var p Paid
	p.TaxPaid = min(taxDue, left)
	left -= p.TaxPaid
	p.UpkeepPaid = min(upDue, left)
	p.TaxDebt, p.UpkeepDebt = taxDue-p.TaxPaid, upDue-p.UpkeepPaid
	return p
}

// Foreclosed reports whether a property whose owner has now ended periods
// periods in a row in debt goes back to the city.
func Foreclosed(periods, limit int) bool { return limit > 0 && periods >= limit }

// Rent is what a period's rent takes from a tenant holding available: all
// of it, or nothing.
func Rent(rent, available int64) int64 {
	if rent > 0 && available >= rent {
		return rent
	}
	return 0
}

// Evicted reports whether a tenant owing arrears periods in a row is
// evicted.
func Evicted(arrears, limit int) bool { return limit > 0 && arrears >= limit }
