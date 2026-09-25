package content

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/vehicle"
)

// This file holds vehicles (items.yml vehicle:;
// docs/adr/0024-property-and-politics.md): a good that is a car or a
// motorbike, the transport mode its owner drives without the mode's fare,
// what it burns per distance unit, and what a journey of wear costs to
// repair. Its durability is the journeys a new one holds. The rules are
// internal/domain/vehicle.

// ErrInvalidVehicleContent means a vehicle good is unusable.
var ErrInvalidVehicleContent = errors.New("content: invalid vehicle")

// VehicleDef is a good's vehicle block.
type VehicleDef struct {
	Mode             string `yaml:"mode" json:"mode"`
	FuelPerDistance  int64  `yaml:"fuel_per_distance" json:"fuel_per_distance"`
	RepairPerJourney int64  `yaml:"repair_per_journey" json:"repair_per_journey"`
}

// VehicleOf is the vehicle a good is, and whether it is one.
func (d ItemDef) VehicleOf() (vehicle.Vehicle, bool) {
	if d.Vehicle == nil {
		return vehicle.Vehicle{}, false
	}
	return vehicle.Vehicle{Mode: d.Vehicle.Mode, FuelPerDistance: d.Vehicle.FuelPerDistance,
		RepairPerJourney: d.Vehicle.RepairPerJourney, Durability: d.Durability}, true
}

// validateVehicles checks every vehicle good: a unique good that wears,
// driving a private transport mode.
func (p *Pack) validateVehicles(problems *[]error) {
	modes := map[string]TransportModeDef{}
	for _, m := range p.TransportModes {
		modes[m.Code] = m
	}
	for i, it := range p.Items {
		v, ok := it.VehicleOf()
		if !ok {
			continue
		}
		where := fmt.Sprintf("items[%d] %q vehicle", i, it.Code)
		bad := func(format string, args ...any) {
			*problems = append(*problems, fmt.Errorf("%w: %s: %s", ErrInvalidVehicleContent, where, fmt.Sprintf(format, args...)))
		}
		if it.Form != "unique" {
			bad("a vehicle is a unique piece, not %q", it.Form)
		}
		if err := v.Validate(); err != nil {
			bad("%v", err)
		}
		m, known := modes[v.Mode]
		switch {
		case !known:
			bad("mode %q is not a transport mode", v.Mode)
		case m.Public:
			bad("mode %q is public transport, which nobody drives", v.Mode)
		}
	}
}

// Vehicles lists the goods that are vehicles of a mode.
func (s *Snapshot) Vehicles(mode string) []ItemDef {
	var out []ItemDef
	for _, it := range s.Items() {
		if v, ok := it.VehicleOf(); ok && v.Mode == mode {
			out = append(out, it)
		}
	}
	return out
}
