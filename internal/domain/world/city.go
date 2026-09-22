// Package world holds the rules of the game's geography: what a city is, what
// it takes in tax, and how far apart two cities are.
//
// RULES LIVE HERE, CONTENT DOES NOT. Which cities exist, what they are called,
// what they charge and which routes connect them is content: it is authored
// outside the code, loaded at runtime and changed without a deployment. This
// package never declares a city and never declares a route. It declares the
// City value a loader fills in, and the Routes value a loader builds from an
// edge list, and it holds the arithmetic that turns those into tax amounts and
// distances. There is deliberately no built-in city list and no default route
// table to fall back on: a default would be a second source of truth that
// silently disagrees with the real one the first time somebody edits it.
//
// THE ROUTE MODEL. docs/database.md gives a city a code, a name, a tax rate, a
// cost of living and a population, and nothing spatial — there are no
// coordinates to measure. Distance is therefore declared rather than measured:
// the caller supplies direct routes between city codes and the distance
// between any two cities is the shortest path through them. See routes.go.
//
// Nothing here performs I/O. Parsing a content file, validating it against a
// schema and reloading it while the process runs is infrastructure's job; this
// package receives values that are already parsed and hands back an error if
// they do not make sense as geography.
package world

import "errors"

// Sentinel errors for invalid city content. They exist because this data comes
// from a file a human edits, so "reject it and say why" has to be an ordinary
// outcome rather than a panic.
var (
	// ErrMissingCityCode means a city arrived without its stable code, which
	// is the only thing routes and stored rows can refer to it by.
	ErrMissingCityCode = errors.New("world: city code is required")

	// ErrInvalidTaxRate means a tax rate fell outside 0..10000 basis points.
	// Above 10000 a city would take more than the whole amount, and below
	// zero it would pay the player to be taxed.
	ErrInvalidTaxRate = errors.New("world: tax rate must be between 0 and 10000 basis points")

	// ErrInvalidCostOfLiving means a negative cost of living.
	ErrInvalidCostOfLiving = errors.New("world: cost of living cannot be negative")

	// ErrInvalidPopulation means a negative population.
	ErrInvalidPopulation = errors.New("world: population cannot be negative")
)

// BasisPointsScale is the denominator of a basis-point rate: 10000 bps is
// 100%. Tax rates are stored in basis points precisely so that the rate itself
// is an exact integer, with no decimal to round on the way in.
const BasisPointsScale = 10000

// City mirrors a row of the cities table in docs/database.md.
//
// ID is the opaque storage identifier (a uuid in Postgres) and is kept as a
// string because the domain never parses, generates or compares the inside of
// one; it only carries it. Code is the stable human-authored key that content
// files and route tables refer to a city by.
//
// treasury_account_id is deliberately absent. It is a foreign key into the
// ledger, which is a storage concern, and no rule in this package needs it.
type City struct {
	ID           string
	Code         string
	Name         string
	TaxRateBPS   int
	CostOfLiving int64
	Population   int
}

// Validate reports whether a loaded city is usable as content.
//
// Name is allowed to be empty: it is display text, a missing one is ugly
// rather than dangerous, and refusing to load a whole world because one city
// lost its label would be the wrong trade at 2am.
func (c City) Validate() error {
	if c.Code == "" {
		return ErrMissingCityCode
	}
	if c.TaxRateBPS < 0 || c.TaxRateBPS > BasisPointsScale {
		return ErrInvalidTaxRate
	}
	if c.CostOfLiving < 0 {
		return ErrInvalidCostOfLiving
	}
	if c.Population < 0 {
		return ErrInvalidPopulation
	}
	return nil
}

// TaxOn returns the tax this city takes on amount, in the same minor currency
// units amount is expressed in.
//
// Integer arithmetic only. There is not a float anywhere in this calculation,
// because a tax is a ledger entry and a ledger built on floats accumulates
// error that shows up later as a transaction whose entries do not sum to zero.
//
// ROUNDING: the result is truncated toward zero, never rounded up or to
// nearest. Two consequences are intended. The tax on a positive amount is
// rounded DOWN, so an ambiguous fraction of a unit is always left with the
// payer rather than invented for the treasury — the house never gains a unit
// it cannot justify. And the tax can never exceed the amount, because
// truncation cannot push the result past the exact value.
//
// A rate outside 0..10000 bps is clamped to the nearest end of that range.
// Such a rate is corrupt content, and the safe reading of corrupt content in
// the middle of a transaction is the boundary; Validate is where that data is
// supposed to be caught and rejected, at load time, with a message.
func (c City) TaxOn(amount int64) int64 {
	bps := c.TaxRateBPS
	if bps <= 0 {
		return 0
	}
	if bps > BasisPointsScale {
		bps = BasisPointsScale
	}

	// Split into whole scale units and a remainder before multiplying. The
	// direct amount*bps would overflow int64 for large balances; this form
	// only overflows once the amount itself is within a factor of the rate of
	// the type's limit, which no balance in this game can reach.
	//
	// Go truncates integer division toward zero and gives the remainder the
	// sign of the dividend, so both terms carry the sign of amount and the sum
	// is exactly the truncation of amount*bps/10000.
	q := amount / BasisPointsScale
	r := amount % BasisPointsScale
	return q*int64(bps) + (r*int64(bps))/BasisPointsScale
}
