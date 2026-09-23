package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// transportModeKeyPrefix is the catalogue namespace that holds transport mode
// names, keyed on the mode's content code (configs/content/transport.yml).
const transportModeKeyPrefix = "transport_mode."

// ModeName resolves a transport mode's display name in this context's
// language: "transport_mode.<code>" in the catalogue, else the name
// transport.yml authored, else a generic word — never the code itself, which
// is an identifier and not something a player should read.
func (c Context) ModeName(code, name string) string {
	if code != "" {
		key := transportModeKeyPrefix + code
		if text := c.T(key, nil); text != key {
			return text
		}
	}
	if name != "" {
		return name
	}
	return c.T("travel.mode_unnamed", nil)
}

// TravelOption is one way to make the journey the options screen offers.
type TravelOption struct {
	// ModeCode addresses the button and names the mode through the
	// catalogue; ModeName is its authored fallback.
	ModeCode string
	ModeName string
	// Fare is what the journey costs now, in minor units: the price the
	// button promises and the most the departure may charge.
	Fare int64
	// Wait is the real time the journey takes.
	Wait   time.Duration
	Energy int
	// Busy says demand has raised the fare above its plain price.
	Busy bool
}

// TravelOptionsView is the choice of transport between two cities.
type TravelOptionsView struct {
	FromCode string
	From     string
	ToCode   string
	To       string
	Options  []TravelOption
	// Cash is the player's cash on hand, in minor units.
	Cash int64
	// Requoted says the player chose a price that is no longer on offer: the
	// fare rose since they saw it, so nothing was charged and the current
	// prices are shown instead.
	Requoted bool
}

// TravelOptions renders the transport choice: every mode that makes the
// journey, with its fare, its real wait and its energy, one button each.
//
// Each button's address carries the fare it shows. That is a ceiling the
// player agreed to, not a price the core trusts: the core prices the journey
// again and charges its own figure, and only if it is not above this one. A
// forged address can therefore agree to pay more, never pay less.
func TravelOptions(c Context, v TravelOptionsView) *presenter.Response {
	to := c.CityName(v.ToCode, v.To)
	lines := make([]string, 0, len(v.Options)+1)
	lines = append(lines, c.T("travel.options_choose", nil))

	kb := keyboards.New()
	for _, o := range v.Options {
		mode := c.ModeName(o.ModeCode, o.ModeName)
		key := "travel.option"
		if o.Busy {
			key = "travel.option_busy"
		}
		fare := FormatMoney(c, o.Fare)
		lines = append(lines, c.T(key, map[string]any{
			"mode":   mode,
			"fare":   fare,
			"wait":   FormatDuration(c, o.Wait),
			"energy": FormatNumber(int64(o.Energy)),
		}))
		kb.Add(c.T("button.travel_by", map[string]any{"mode": mode, "fare": fare}),
			AddrTravelStart, v.ToCode, o.ModeCode, strconv.FormatInt(o.Fare, 10))
	}

	text := paragraphs(
		c.T("travel.options_title", map[string]any{"to": to, "from": c.CityName(v.FromCode, v.From)}),
		requoteNotice(c, v.Requoted),
		body(lines...),
		c.T("travel.cash", map[string]any{"cash": FormatMoney(c, v.Cash)}),
	)

	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap, RefreshData: keyboards.Data(AddrTravelOptions, v.ToCode)}))
	return c.respond(text, kb.Build())
}

func requoteNotice(c Context, requoted bool) string {
	if !requoted {
		return ""
	}
	return c.T("travel.requoted", nil)
}

// TravelFundsView is a departure refused for want of cash.
type TravelFundsView struct {
	ToCode   string
	ModeCode string
	ModeName string
	Fare     int64
	Cash     int64
}

// TravelNoFunds renders a departure the player cannot pay for. Nothing was
// charged and nothing started; the way back is the choice of transport, where
// a cheaper mode may still be in reach.
func TravelNoFunds(c Context, v TravelFundsView) *presenter.Response {
	text := c.T("travel.insufficient_funds", map[string]any{
		"mode": c.ModeName(v.ModeCode, v.ModeName),
		"fare": FormatMoney(c, v.Fare),
		"cash": FormatMoney(c, v.Cash),
	})
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.travel_options", nil), AddrTravelOptions, v.ToCode); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap}))
	return c.respond(text, kb.Build())
}

// TravelStartedView is the confirmation a departure produces.
//
// Each city is its content code, resolved to a name in the player's language
// by the screen, and its authored name, the fallback for an untranslated code.
type TravelStartedView struct {
	FromCode string
	From     string
	ToCode   string
	To       string
	ModeCode string
	ModeName string
	// Duration is the real wait until arrival.
	Duration time.Duration
	// Energy is what the departure actually cost, as the domain charged it,
	// not what the screen thinks it should have cost.
	Energy int
	// Fare is what was charged, in minor units.
	Fare int64
}

// TravelStarted renders a departure.
//
// Its refresh button opens the journey itself, so there is no separate
// "my journey" button: two buttons with one destination is one too many.
func TravelStarted(c Context, v TravelStartedView) *presenter.Response {
	var fare string
	if v.Fare > 0 {
		fare = c.T("travel.started_fare", map[string]any{"fare": FormatMoney(c, v.Fare)})
	}
	text := body(c.T("travel.started", map[string]any{
		"from":     c.CityName(v.FromCode, v.From),
		"to":       c.CityName(v.ToCode, v.To),
		"mode":     c.ModeName(v.ModeCode, v.ModeName),
		"duration": FormatDuration(c, v.Duration),
		"energy":   FormatNumber(int64(v.Energy)),
	}), fare)

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(text, kb.Build())
}

// TravelStatusView is a journey in progress. Its cities are carried as in
// TravelStartedView.
type TravelStatusView struct {
	FromCode string
	From     string
	ToCode   string
	To       string
	// ModeCode and ModeName name the mode; both empty for a journey that
	// began before modes existed, which then shows no mode line.
	ModeCode  string
	ModeName  string
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
	var mode string
	if v.ModeCode != "" || v.ModeName != "" {
		mode = c.T("travel.status_mode", map[string]any{"mode": c.ModeName(v.ModeCode, v.ModeName)})
	}

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(body(c.T(key, args), mode), kb.Build())
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
