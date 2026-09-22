package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TravelStartedView is the confirmation a departure produces.
type TravelStartedView struct {
	From     string
	To       string
	Duration time.Duration
	// Energy is what the departure actually cost, as the domain charged it,
	// not what the screen thinks it should have cost.
	Energy int
}

// TravelStarted renders a departure.
func TravelStarted(c Context, v TravelStartedView) *presenter.Response {
	text := body(
		c.T("travel.title", nil),
		"",
		c.T("travel.started", map[string]any{
			"from":     v.From,
			"to":       v.To,
			"duration": FormatDuration(c, v.Duration),
			"energy":   v.Energy,
		}),
	)

	kb := keyboards.New()
	status, _ := keyboards.Button(c.T("button.journey", nil), AddrTravelStatus)
	kb.Row(status)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(text, kb.Build())
}

// TravelStatusView is a journey in progress.
type TravelStatusView struct {
	From      string
	To        string
	Remaining time.Duration
	ArrivesAt time.Time
}

// TravelStatus renders the journey a player is on.
func TravelStatus(c Context, v TravelStatusView) *presenter.Response {
	text := body(
		c.T("travel.title", nil),
		"",
		c.T("travel.status", map[string]any{
			"from":      v.From,
			"to":        v.To,
			"remaining": FormatDuration(c, v.Remaining),
		}),
	)

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(text, kb.Build())
}

// TravelArrivedView is the notification a landed journey produces.
//
// It is the one screen in this package a player did not ask for: the
// scheduler produces it when the journey finishes, so it always SENDS. There
// is no message of the player's to edit, and editing one from an hour ago
// would replace something they may still be reading.
type TravelArrivedView struct {
	City string
	XP   int64
}

// TravelArrived renders the arrival notification.
func TravelArrived(c Context, v TravelArrivedView) *presenter.Response {
	text := c.T("travel.arrived", map[string]any{
		"city": v.City,
		"xp":   v.XP,
	})

	kb := keyboards.New()
	worldMap, _ := keyboards.Button(c.T("button.map", nil), AddrMap)
	home, _ := keyboards.Button(c.T("button.profile", nil), AddrHome)
	kb.Row(worldMap, home)

	return presenter.Message(text, kb.Build())
}
