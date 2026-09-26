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
//
// Code is both the address of its travel button and the key its display name
// is looked up by; Name is the authored name, shown only when the catalogue
// has no translation for Code.
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
	// OriginCode and Origin are the player's city: its content code and its
	// authored name, the fallback for an untranslated code. Both empty when
	// they are nowhere yet.
	OriginCode string
	Origin     string
	// Travelling says a journey is in progress, and TravellingToCode and
	// TravellingTo name its destination. A traveller is shown the journey,
	// not a departures board full of buttons that would all be refused.
	Travelling       bool
	TravellingToCode string
	TravellingTo     string
}

// Map renders the destinations reachable from the player's city.
func Map(c Context, v MapView) *presenter.Response {
	return c.withView(renderMap(c, v), ScreenMap, v)
}

func renderMap(c Context, v MapView) *presenter.Response {
	kb := keyboards.New()
	origin := c.CityName(v.OriginCode, v.Origin)

	var content string
	switch {
	case v.Travelling:
		if to := c.CityName(v.TravellingToCode, v.TravellingTo); to != "" {
			content = c.T("map.travelling", map[string]any{"city": to})
		}
		journey, _ := keyboards.Button(c.T("button.journey", nil), AddrTravelStatus)
		kb.Row(journey)

	case origin == "":
		content = c.T("map.no_city", nil)

	case len(v.Destinations) == 0:
		content = paragraphs(
			c.T("map.origin", map[string]any{"city": origin}),
			c.T("map.no_routes", nil),
		)

	default:
		lines := make([]string, 0, len(v.Destinations)+1)
		lines = append(lines, c.T("map.destinations", nil))
		for _, city := range v.Destinations {
			name := c.CityName(city.Code, city.Name)
			lines = append(lines, c.T("map.destination", map[string]any{
				"city":     name,
				"distance": FormatNumber(c, int64(city.DistanceKM)),
			}))
			// The city CODE is the address: it is authored content, it is
			// stable, and it is short enough to leave room inside the 64-byte
			// budget. The core looks it up again and re-checks the route, the
			// energy and whether this player is already travelling, so a
			// hand-written address buys nothing.
			// Pressing a destination opens the choice of transport; nothing
			// departs until a mode is chosen there.
			kb.Add(c.T("button.travel_to", map[string]any{"city": name}), AddrTravelOptions, city.Code)
		}
		content = paragraphs(
			c.T("map.origin", map[string]any{"city": origin}),
			body(lines...),
			pageIndicator(c, v.Page, v.Pages),
		)
	}

	// Back is the map of the player's own city, where this list is opened
	// from.
	nav := keyboards.Nav{RefreshData: AddrCities, BackData: AddrMap}
	if !v.Travelling && origin != "" {
		nav = keyboards.Nav{
			Prefix:   AddrCities,
			Page:     v.Page,
			HasPrev:  pageOrOne(v.Page) > 1,
			HasNext:  pageOrOne(v.Page) < v.Pages,
			BackData: AddrMap,
		}
	}
	kb.Nav(c.nav(nav))

	title := htmlBold(htmlEscape(c.T("map.cities_title", nil)))
	return c.respond(paragraphs(title, htmlEscape(content)), kb.Build()).AsHTML()
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
