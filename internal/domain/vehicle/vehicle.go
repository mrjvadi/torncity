// Package vehicle holds the rules of a player's own vehicle: a car or a
// motorbike is a piece of goods (a unique item, bought from a dealer or
// built by a company) that lets its owner make the journeys of its transport
// mode without paying the mode's fare. They pay for its fuel instead, by the
// distance, and every journey wears it: a piece is made with a number of
// journeys in it, and one with none left no longer drives — the owner hires
// or rides again, until it is repaired.
//
// Which goods are vehicles, of which mode, and what they burn is CONTENT
// (items.yml vehicle:); the package reads no clock and no file.
package vehicle

import (
	"errors"
	"fmt"
)

// ErrInvalid means a vehicle the rules cannot use.
var ErrInvalid = errors.New("vehicle: invalid")

// Vehicle is a kind of vehicle.
type Vehicle struct {
	// Mode is the transport mode it drives.
	Mode string
	// FuelPerDistance is what it burns per distance unit, minor units.
	FuelPerDistance int64
	// Durability is the journeys a new one holds.
	Durability int
	// RepairPerJourney is what putting one journey of wear back costs.
	RepairPerJourney int64
}

// Validate checks a kind of vehicle.
func (v Vehicle) Validate() error {
	if v.Mode == "" || v.FuelPerDistance < 0 || v.FuelPerDistance > 10_000 || v.Durability < 1 ||
		v.RepairPerJourney < 0 || v.RepairPerJourney > 1_000_000 {
		return fmt.Errorf("%w: %+v", ErrInvalid, v)
	}
	return nil
}

// Fuel is what a journey of a distance burns.
func (v Vehicle) Fuel(distance int) int64 { return int64(max(distance, 0)) * max(v.FuelPerDistance, 0) }

// Drives reports whether a piece with uses journeys left can make one more.
func Drives(uses int) bool { return uses > 0 }

// ConditionBPS is a piece's condition: journeys left of a new one's, bps.
func (v Vehicle) ConditionBPS(uses int) int64 {
	if v.Durability <= 0 {
		return 0
	}
	return int64(min(max(uses, 0), v.Durability)) * 10000 / int64(v.Durability)
}

// Repair is what restoring a piece with uses left to new costs.
func (v Vehicle) Repair(uses int) int64 {
	return int64(max(v.Durability-max(uses, 0), 0)) * v.RepairPerJourney
}
