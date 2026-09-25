package screens

import (
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The map of the player's own city: where they stand, every place of the
// city with the walk to it and what is found there, and the way to another
// city. A walk is a short timed move on the game clock; a service asked for
// at the wrong place answers with where it is and the walk there.
//
// Places are content keyed on a code; their names are looked up under
// venue.<code> (the crime engine calls a place a venue) and fall back to the
// authored name. Everything here is public: where a player stands is what the
// people around them can see.

// Callback addresses of the city map.
const (
	// AddrCities is the list of other cities to travel to (the old map).
	AddrCities  = "map:cities"
	AddrPlaceGo = "place:go"
)

// SpotName is a city place's display name in this context's language.
// (PlaceName names a jurisdiction; a city place is a venue in the
// catalogue.)
func (c Context) SpotName(n Named) string { return c.VenueName(n) }

// ShopName is a city shop's display name (shop_name.<code>).
func (c Context) ShopName(n Named) string { return c.named("shop_name."+n.Code, n.Name) }

// WalkView is a walk under way.
type WalkView struct {
	To        Named
	Remaining time.Duration
	ArrivesAt time.Time
}

// PlaceLine is one place on the city map.
type PlaceLine struct {
	Place Named
	// Walk is the real time the walk there takes; Energy what it costs.
	Walk   time.Duration
	Energy int
	// Services are what is found there (place.service.<code>); Departures
	// the transport modes that leave from there.
	Services   []string
	Departures []string
	// Shops are the shops found there (shops.yml place).
	Shops []Named
	// Here marks where the player stands.
	Here bool
}

// CityMapView is the map of the player's own city.
type CityMapView struct {
	CityCode, City string
	// NoCity: the player is nowhere yet.
	NoCity bool
	// Travelling: a journey between cities is under way.
	Travelling                     bool
	TravellingToCode, TravellingTo string
	// Here is where the player stands; Walking a walk under way instead.
	Here    Named
	Walking *WalkView
	// Others counts the other players standing at the same place.
	Others int
	Places []PlaceLine
}

// placeWhat is the short line of what a place holds.
func (c Context) placeWhat(l PlaceLine) string {
	var parts []string
	for _, s := range l.Services {
		parts = append(parts, c.T("place.service."+s, nil))
	}
	for _, m := range l.Departures {
		parts = append(parts, c.T("place.departures", map[string]any{"mode": c.ModeName(m, "")}))
	}
	for _, s := range l.Shops {
		parts = append(parts, c.ShopName(s))
	}
	return strings.Join(parts, c.T("place.separator", nil))
}

// CityMap renders the map of the player's own city.
func CityMap(c Context, v CityMapView) *presenter.Response {
	kb := keyboards.New()
	switch {
	case v.Travelling:
		journey, _ := keyboards.Button(c.T("button.journey", nil), AddrTravelStatus)
		kb.Row(journey)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrMap}))
		return c.respond(paragraphs(c.T("map.title", nil),
			c.T("map.travelling", map[string]any{"city": c.CityName(v.TravellingToCode, v.TravellingTo)})), kb.Build())
	case v.NoCity:
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrMap}))
		return c.respond(paragraphs(c.T("map.title", nil), c.T("map.no_city", nil)), kb.Build())
	}

	city := c.CityName(v.CityCode, v.City)
	var where string
	switch {
	case v.Walking != nil:
		where = body(
			c.T("place.walking", map[string]any{
				"place": c.SpotName(v.Walking.To), "remaining": FormatDuration(c, v.Walking.Remaining),
			}),
			clockLine(c, "place.arrives_at", v.Walking.ArrivesAt),
		)
	case v.Here.Code != "":
		where = c.T("place.here", map[string]any{"place": c.SpotName(v.Here)})
		if v.Others > 0 {
			where = body(where, c.T("place.others", map[string]any{"count": FormatNumber(c, int64(v.Others))}))
		}
	}

	var list string
	if len(v.Places) > 0 {
		lines := []string{c.T("place.list_title", nil)}
		var buttons, shops []presenter.Button
		for _, l := range v.Places {
			args := map[string]any{"place": c.SpotName(l.Place), "walk": FormatDuration(c, l.Walk)}
			key := "place.line"
			if l.Here && v.Walking == nil {
				key = "place.line_here"
			}
			line := c.T(key, args)
			if what := c.placeWhat(l); what != "" {
				line = c.T("place.line_with", map[string]any{"line": line, "what": what})
			}
			lines = append(lines, line)
			if !l.Here && v.Walking == nil {
				if btn, ok := keyboards.Button(c.T("place.button.go", map[string]any{"place": c.SpotName(l.Place)}),
					AddrPlaceGo, l.Place.Code); ok {
					buttons = append(buttons, btn)
				}
			}
			// A place with shops offers them: here, the list at once;
			// elsewhere, one press that walks there and opens it.
			if len(l.Shops) > 0 && v.Walking == nil {
				var btn presenter.Button
				var ok bool
				if l.Here {
					btn, ok = keyboards.Button(c.T("shop.button.here", nil), AddrShops, l.Place.Code)
				} else {
					btn, ok = goThenButton(c.T("shop.button.at", map[string]any{"place": c.SpotName(l.Place)}),
						l.Place.Code, "shop.list", l.Place.Code)
				}
				if ok {
					shops = append(shops, btn)
				}
			}
		}
		list = body(lines...)
		kb.Grid(2, buttons...)
		kb.Grid(2, shops...)
	}

	if btn, ok := keyboards.Button(c.T("place.button.other_cities", nil), AddrCities); ok {
		kb.Row(btn)
	}
	if btn, ok := keyboards.Button(c.T("gov.button.city", nil), AddrGovCity); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrMap}))
	return c.respond(paragraphs(c.T("place.title", map[string]any{"city": city}), where, list, c.T("place.hint", nil)), kb.Build())
}

// WalkStartedView is a walk that has begun.
type WalkStartedView struct {
	From, To  Named
	Duration  time.Duration
	ArrivesAt time.Time
	Energy    int
	// Then is the catalogue key of what happens on arrival (place.then.*),
	// empty for a plain walk.
	Then string
}

// WalkStarted renders a walk under way.
func WalkStarted(c Context, v WalkStartedView) *presenter.Response {
	lines := []string{
		c.T("place.walk_started", map[string]any{
			"place": c.SpotName(v.To), "duration": FormatDuration(c, v.Duration),
		}),
		clockLine(c, "place.arrives_at", v.ArrivesAt),
	}
	if v.Energy > 0 {
		lines = append(lines, c.T("place.walk_energy", map[string]any{"energy": FormatNumber(c, int64(v.Energy))}))
	}
	if v.Then != "" {
		lines = append(lines, c.T(v.Then, map[string]any{"place": c.SpotName(v.To)}))
	}
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.map", nil), AddrMap); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build())
}

// NotHereView is a request that needs another place: the service or the
// departure is there, or the player is still on the way somewhere.
type NotHereView struct {
	// Need is the catalogue key naming what was asked for
	// (place.need.<service>, place.need.departure…), NeedArgs its values.
	Need     string
	NeedArgs map[string]any
	// Mode, Crime and Shop name what needs the place, for the sentences
	// that mention it: a departure's mode, a crime, a shop.
	Mode  string
	Crime Named
	Shop  Named
	// Place is where it is; Here where the player stands; Walk how long
	// the walk there takes.
	Place Named
	Here  Named
	Walk  time.Duration
	// Walking: the player is on the way to Place, Remaining left.
	Walking   bool
	Remaining time.Duration
	ArrivesAt time.Time
	// Then, with ThenArgs, is the screen the walk button opens on arrival
	// (the one the player asked for), so one press walks there and carries
	// on. Empty: the button only walks.
	Then     string
	ThenArgs []string
}

// GoThen is the address of a walk to place that runs then, with its
// arguments, on arrival. It falls back to the plain walk when the whole
// address would not fit Telegram's 64 bytes, and is empty when not even that
// fits.
func GoThen(place, then string, args ...string) string {
	if then != "" {
		if data := keyboards.Data(append([]string{AddrPlaceGo, place, then}, args...)...); data != "" {
			return data
		}
	}
	return keyboards.Data(AddrPlaceGo, place)
}

// goThenButton is a button that walks to place and then runs then.
func goThenButton(label, place, then string, args ...string) (presenter.Button, bool) {
	data := GoThen(place, then, args...)
	if label == "" || data == "" {
		return presenter.Button{}, false
	}
	return presenter.Button{Text: label, CallbackData: data}, true
}

// NotHere renders a request made at the wrong place, with the walk to the
// right one a press away.
func NotHere(c Context, v NotHereView) *presenter.Response {
	kb := keyboards.New()
	if v.Walking {
		if btn, ok := keyboards.Button(c.T("button.map", nil), AddrMap); ok {
			kb.Row(btn)
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
		return c.respond(body(
			c.T("place.still_walking", map[string]any{
				"place": c.SpotName(v.Place), "remaining": FormatDuration(c, v.Remaining),
			}),
			clockLine(c, "place.arrives_at", v.ArrivesAt),
		), kb.Build())
	}
	args := map[string]any{"place": c.SpotName(v.Place)}
	for k, val := range v.NeedArgs {
		args[k] = val
	}
	if v.Mode != "" {
		args["mode"] = c.ModeName(v.Mode, "")
	}
	if v.Crime.Code != "" {
		args["crime"] = c.CrimeName(v.Crime)
	}
	if v.Shop.Code != "" {
		args["shop"] = c.ShopName(v.Shop)
	}
	need := c.T(v.Need, args)
	walkArgs := map[string]any{"place": c.SpotName(v.Place), "walk": FormatDuration(c, v.Walk)}
	label, hint := c.T("place.button.walk", walkArgs), ""
	if v.Then != "" {
		// One press walks there and opens what the player asked for.
		label, hint = c.T("place.button.walk_then", walkArgs), c.T("place.then_hint", walkArgs)
	}
	if btn, ok := goThenButton(label, v.Place.Code, v.Then, v.ThenArgs...); ok {
		kb.Row(btn)
	}
	if v.Then == "" {
		if btn, ok := keyboards.Button(c.T("button.map", nil), AddrMap); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(need, c.T("place.you_are_at", map[string]any{"place": c.SpotName(v.Here)}), hint), kb.Build())
}
