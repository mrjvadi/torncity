package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// MapCity is one destination on the map: a city a route reaches from where
// the player stands.
//
// Cities with no route from here are not destinations and are not shown. A
// list of places the player cannot go is noise on a screen whose whole job is
// "where can I go"; they appear as soon as the player stands somewhere that
// connects to them.
type MapCity struct {
	Code       string
	Name       string
	DistanceKM int
}

// MapView is one page of destinations.
type MapView struct {
	// Destinations are the reachable cities on this page, never including
	// the one the player is in.
	Destinations []MapCity
	Page         int
	Pages        int
	// Origin is the resolved name of the player's city, empty when they are
	// nowhere yet.
	Origin string
	// Travelling says a journey is in progress, and TravellingTo names its
	// destination. A traveller is shown the journey, not a departures board
	// full of buttons that would all be refused.
	Travelling   bool
	TravellingTo string
}

// Map renders the destinations reachable from the player's city.
func Map(c Context, v MapView) *presenter.Response {
	kb := keyboards.New()

	var content string
	switch {
	case v.Travelling:
		if v.TravellingTo != "" {
			content = c.T("map.travelling", map[string]any{"city": v.TravellingTo})
		}
		journey, _ := keyboards.Button(c.T("button.journey", nil), AddrTravelStatus)
		kb.Row(journey)

	case v.Origin == "":
		content = c.T("map.no_city", nil)

	case len(v.Destinations) == 0:
		content = paragraphs(
			c.T("map.origin", map[string]any{"city": v.Origin}),
			c.T("map.no_routes", nil),
		)

	default:
		lines := make([]string, 0, len(v.Destinations)+1)
		lines = append(lines, c.T("map.destinations", nil))
		for _, city := range v.Destinations {
			lines = append(lines, c.T("map.destination", map[string]any{
				"city":     city.Name,
				"distance": FormatNumber(int64(city.DistanceKM)),
			}))
			// The city CODE is the address: it is authored content, it is
			// stable, and it is short enough to leave room inside the 64-byte
			// budget. The core looks it up again and re-checks the route, the
			// energy and whether this player is already travelling, so a
			// hand-written address buys nothing.
			kb.Add(c.T("button.travel_to", map[string]any{"city": city.Name}), AddrTravelStart, city.Code)
		}
		content = paragraphs(
			c.T("map.origin", map[string]any{"city": v.Origin}),
			body(lines...),
			pageIndicator(c, v.Page, v.Pages),
		)
	}

	nav := keyboards.Nav{RefreshData: AddrMap}
	if !v.Travelling && v.Origin != "" {
		nav = keyboards.Nav{
			Prefix:  AddrMap,
			Page:    v.Page,
			HasPrev: pageOrOne(v.Page) > 1,
			HasNext: pageOrOne(v.Page) < v.Pages,
		}
	}
	kb.Nav(c.nav(nav))

	return c.respond(paragraphs(c.T("map.title", nil), content), kb.Build())
}

// pageIndicator says where in a list the player is, and says nothing at all
// when the list fits on one page.
func pageIndicator(c Context, page, pages int) string {
	if pages <= 1 {
		return ""
	}
	return c.T("page.indicator", map[string]any{
		"page":  pageOrOne(page),
		"pages": pages,
	})
}

// pageOrOne keeps a page number readable: a list always has a first page,
// whatever a caller passed.
func pageOrOne(page int) int {
	if page < 1 {
		return 1
	}
	return page
}
