package economy

import "github.com/mrjvadi/torncity/internal/presentation"

// NeedBuilding is a building a settlement must have standing for a service to
// run: one building by code, or a role at a tier or better.
type NeedBuilding struct {
	Code string
	Role string
	Tier int
}

// Unavailable is the data of a "not available here" state (CLAUDE.md section
// 2, availability.yml): the service is not offered in the settlement the player
// stands in. It says which service, the smallest stage of settlement that has
// it, the stage the player is in, what such a settlement must have standing,
// and the nearest place the player can have it now. A screen that carries it
// has no other facts.
type Unavailable struct {
	// Service is the service's code: loans, savings, insurance, stocks, gold,
	// credit, bank, or a place service such as auction_house.
	Service string
	// Stage is the smallest settlement stage that has it: village, town, city,
	// country or support (the neutral city only).
	Stage string
	// Here is the stage of the settlement the player stands in.
	Here string
	// Requires are the buildings the service needs, a hint for a village head.
	Requires []NeedBuilding
	// Nearest is where the player can have it: the neutral city. Nil when
	// nowhere is known.
	Nearest *presentation.Named
}

// actions is the answer of a screen whose service is not offered here: the
// journey to the nearest place that has it, and the way back.
func (u *Unavailable) actions(backAddr string) []presentation.Action {
	var a []presentation.Action
	if u.Nearest != nil && u.Nearest.Code != "" {
		a = append(a, act(AddrTravelOptions, u.Nearest.Code).Named("support.travel").About(u.Nearest.Code))
	}
	return append(a, back(backAddr))
}
