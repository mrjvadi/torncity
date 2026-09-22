package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// MapCity is one row of the world map.
//
// TaxPercent arrives already rendered, by PercentFromBPS, so the screen never
// does arithmetic on a rate a player compares two cities by.
type MapCity struct {
	Code         string
	Name         string
	TaxPercent   string
	CostOfLiving int64

	// Current marks the city the player is standing in.
	Current bool
	// Reachable says whether a route exists from where the player is. An
	// unreachable city is still listed: a player is entitled to see that a
	// place exists and that they cannot get to it from here, which is a fact
	// about the world rather than an error.
	Reachable  bool
	DistanceKM int
}

// MapView is the paginated list of cities.
type MapView struct {
	Cities []MapCity
	Page   int
	Pages  int
	// Origin is the resolved name of the player's city, empty when they are
	// nowhere yet.
	Origin string
	// Travelling suppresses the departure buttons: a player already on the
	// road cannot start a second journey, and offering the button anyway
	// would be an invitation to a refusal.
	Travelling bool
}

// Map renders the world map.
func Map(c Context, v MapView) *presenter.Response {
	origin := v.Origin
	if origin == "" {
		origin = c.T("profile.city_unknown", nil)
	}

	lines := make([]string, 0, len(v.Cities)+3)
	lines = append(lines, c.T("map.title", nil))
	lines = append(lines, c.T("map.origin", map[string]any{"city": origin}))
	lines = append(lines, "")

	if len(v.Cities) == 0 {
		lines = append(lines, c.T("map.empty", nil))
	}

	kb := keyboards.New()
	for _, city := range v.Cities {
		lines = append(lines, cityLine(c, city))
		if city.Current || !city.Reachable || v.Travelling {
			continue
		}
		// The city CODE is the address: it is authored content, it is stable,
		// and it is short enough to leave room inside the 64-byte budget. The
		// core looks it up again and re-checks the route, the energy and
		// whether this player is already travelling, so a hand-written
		// address buys nothing.
		kb.Add(c.T("map.depart", map[string]any{"city": city.Name}), AddrTravelStart, city.Code)
	}

	if v.Pages > 1 {
		lines = append(lines, "")
		lines = append(lines, c.T("page.indicator", map[string]any{
			"page":  pageOrOne(v.Page),
			"pages": v.Pages,
		}))
	}

	kb.Nav(c.nav(keyboards.Nav{
		Prefix:  AddrMap,
		Page:    v.Page,
		HasPrev: pageOrOne(v.Page) > 1,
		HasNext: pageOrOne(v.Page) < v.Pages,
	}))

	return c.respond(body(lines...), kb.Build())
}

// cityLine renders one city, choosing between the three shapes a city can
// have on this screen: where you are, somewhere you can go, somewhere you
// cannot reach from here.
func cityLine(c Context, city MapCity) string {
	args := map[string]any{
		"city":     city.Name,
		"tax":      city.TaxPercent,
		"cost":     city.CostOfLiving,
		"distance": city.DistanceKM,
	}
	switch {
	case city.Current:
		return c.T("map.city.current", args)
	case city.Reachable:
		return c.T("map.city.reachable", args)
	default:
		return c.T("map.city.unreachable", args)
	}
}

// pageOrOne keeps a page number readable: a list always has a first page,
// whatever a caller passed.
func pageOrOne(page int) int {
	if page < 1 {
		return 1
	}
	return page
}
