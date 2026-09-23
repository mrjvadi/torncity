package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TravelStartedView is the confirmation a departure produces.
//
// Each city is its content code, resolved to a name in the player's language
// by the screen, and its authored name, the fallback for an untranslated code.
type TravelStartedView struct {
	FromCode string
	From     string
	ToCode   string
	To       string
	Duration time.Duration
	// Energy is what the departure actually cost, as the domain charged it,
	// not what the screen thinks it should have cost.
	Energy int
}

// TravelStarted renders a departure.
//
// Its refresh button opens the journey itself, so there is no separate
// "my journey" button: two buttons with one destination is one too many.
func TravelStarted(c Context, v TravelStartedView) *presenter.Response {
	text := c.T("travel.started", map[string]any{
		"from":     c.CityName(v.FromCode, v.From),
		"to":       c.CityName(v.ToCode, v.To),
		"duration": FormatDuration(c, v.Duration),
		"energy":   FormatNumber(int64(v.Energy)),
	})

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(text, kb.Build())
}

// TravelStatusView is a journey in progress. Its cities are carried as in
// TravelStartedView.
type TravelStatusView struct {
	FromCode  string
	From      string
	ToCode    string
	To        string
	Remaining time.Duration
	ArrivesAt time.Time
}

// arrivingThreshold is how close to arrival a journey reads as "any moment
// now" rather than as a countdown of seconds. The arrival is landed by the
// scheduler, which can lag the clock by a little; a countdown stuck at "1s"
// would look broken.
const arrivingThreshold = time.Minute

// TravelStatus renders the journey a player is on.
//
// There is deliberately no map or departure button here: the player cannot
// leave again until they land, and a button that only leads to a refusal is
// noise.
func TravelStatus(c Context, v TravelStatusView) *presenter.Response {
	args := map[string]any{
		"from": c.CityName(v.FromCode, v.From),
		"to":   c.CityName(v.ToCode, v.To),
	}
	key := "travel.status_arriving"
	if v.Remaining >= arrivingThreshold {
		key = "travel.status"
		args["remaining"] = FormatDuration(c, v.Remaining)
	}

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(c.T(key, args), kb.Build())
}

// TravelArrivedView is the notification a landed journey produces.
//
// It is the one screen in this package a player did not ask for: the
// scheduler produces it when the journey finishes, so it always SENDS. There
// is no message of the player's to edit, and editing one from an hour ago
// would replace something they may still be reading.
type TravelArrivedView struct {
	// CityCode and City are the destination, carried as in
	// TravelStartedView.
	CityCode string
	City     string
	XP       int64
}

// TravelArrived renders the arrival notification.
func TravelArrived(c Context, v TravelArrivedView) *presenter.Response {
	var xp string
	if v.XP > 0 {
		xp = c.T("travel.arrived_xp", map[string]any{"xp": FormatNumber(v.XP)})
	}
	text := body(c.T("travel.arrived", map[string]any{"city": c.CityName(v.CityCode, v.City)}), xp)

	kb := keyboards.New()
	worldMap, _ := keyboards.Button(c.T("button.map", nil), AddrMap)
	home, _ := keyboards.Button(c.T("button.profile", nil), AddrHome)
	kb.Row(home, worldMap)

	return presenter.Message(text, kb.Build())
}
