package economy

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Addresses of commands other areas own, which the economy screens point to.
const (
	// AddrHome is the player's profile.
	AddrHome = "player:profile.get"
	// AddrMap is the city's places.
	AddrMap = "map:list"
	// AddrInventory is the bag; AddrItem one piece in it.
	AddrInventory = "inventory:show"
	AddrItem      = "inventory:item"
	// AddrCompanyGoods is the goods the player's companies make.
	AddrCompanyGoods = "company:goods"
	// AddrGovCity is a city's government; AddrGovLever one of its levers.
	AddrGovCity  = "gov:city"
	AddrGovLever = "gov:lever"
	// AddrBills is the legislature's list of bills.
	AddrBills = "law:list"
	// AddrSearch finds a player.
	AddrSearch = "social:search"
	// AddrTravelOptions is the choice of transport to one city.
	AddrTravelOptions = "travel:options"
	// AddrPlaceGo walks to a place of the city, then runs a command.
	AddrPlaceGo = "place:go"
)

// PledgeArg is the argument a pledge travels as: a property's number, a
// company's code, "0" for none.
func PledgeArg(p *PledgeLine) string {
	if p == nil {
		return "0"
	}
	if p.Code != "" {
		return p.Code
	}
	return strconv.FormatInt(p.No, 10)
}

// walk is the action that walks to the place of a way and then runs a
// command there.
func walk(w *Way, then string, args ...string) (presentation.Action, bool) {
	if w == nil || w.Place.Code == "" {
		return presentation.Action{}, false
	}
	a := presentation.Do("place.go", append([]string{w.Place.Code, then}, args...)...)
	return a.Named("place.walk").About(w.Place.Code), true
}
