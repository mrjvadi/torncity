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

// TravelOptions renders the transport choice: every mode that makes the
// journey, with its fare, its real wait and its energy, one button each.
//
// Each button's address carries the fare it shows. That is a ceiling the
// player agreed to, not a price the core trusts: the core prices the journey
// again and charges its own figure, and only if it is not above this one. A
// forged address can therefore agree to pay more, never pay less.
func TravelOptions(c Context, v TravelOptionsView) *presenter.Response {
	return c.withView(renderTravelOptions(c, v), ScreenTravelOptions, v)
}

func renderTravelOptions(c Context, v TravelOptionsView) *presenter.Response {
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
		if o.Fare == 0 {
			// A journey that costs nothing says so, rather than quoting a
			// price of zero.
			fare = c.T("travel.free", nil)
		}
		if o.Vehicle != nil {
			key = "travel.option_own"
			mode = c.ItemName(*o.Vehicle)
		}
		lines = append(lines, c.T(key, map[string]any{
			"mode":      mode,
			"fare":      fare,
			"wait":      FormatDuration(c, o.Wait),
			"energy":    FormatNumber(c, int64(o.Energy)),
			"condition": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(o.Condition))}),
		}))
		kb.Add(c.T("button.travel_by", map[string]any{"mode": mode, "fare": fare}),
			AddrTravelStart, v.ToCode, o.ModeCode, strconv.FormatInt(o.Fare, 10))
	}

	text := paragraphs(
		c.T("travel.options_title", map[string]any{"to": to, "from": c.CityName(v.FromCode, v.From)}),
		requoteNotice(c, v.Requoted),
		body(lines...),
		cashLine(c, v.Cash),
	)

	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap, RefreshData: keyboards.Data(AddrTravelOptions, v.ToCode)}))
	return c.respond(text, kb.Build())
}

// cashLine is what the player has to pay a fare with. A shared screen (a
// group) leaves it out: the fares are for everyone, the purse is not.
func cashLine(c Context, cash int64) string {
	if c.Shared {
		return ""
	}
	return c.T("travel.cash", map[string]any{"cash": FormatMoney(c, cash)})
}

func requoteNotice(c Context, requoted bool) string {
	if !requoted {
		return ""
	}
	return c.T("travel.requoted", nil)
}

// TravelCheckout renders the fare of the chosen mode with a button per way
// the player can pay it. Each button carries the fare as the ceiling the
// player agreed to, and the method; the departure re-prices and honours or
// re-quotes, as a mode button does. Balances appear only outside a group.
func TravelCheckout(c Context, v TravelCheckoutView) *presenter.Response {
	return c.withView(renderTravelCheckout(c, v), ScreenTravelCheckout, v)
}

func renderTravelCheckout(c Context, v TravelCheckoutView) *presenter.Response {
	mode := c.ModeName(v.ModeCode, v.ModeName)
	fareKey := "travel.checkout_fare"
	if v.Busy {
		fareKey = "travel.checkout_fare_busy"
	}
	facts := body(
		c.T(fareKey, map[string]any{"fare": FormatMoney(c, v.Fare)}),
		c.T("travel.checkout_wait", map[string]any{"wait": FormatDuration(c, v.Wait)}),
		c.T("travel.checkout_energy", map[string]any{"energy": FormatNumber(c, int64(v.Energy))}),
	)
	kb := keyboards.New()
	var pay string
	if len(v.Payment.Usable) > 0 {
		pay = body(c.T("payment.choose", nil), c.paymentNote(v.Payment))
		fare := strconv.FormatInt(v.Fare, 10)
		c.paymentButtons(kb, v.Payment, func(m string) []string {
			return []string{AddrTravelStart, v.ToCode, v.ModeCode, fare, m}
		})
	} else {
		pay = body(c.T("payment.cannot_afford", nil), c.paymentNote(v.Payment))
		if btn, ok := keyboards.Button(c.T("button.bank", nil), AddrBank); ok {
			kb.Row(btn)
		}
	}
	if btn, ok := keyboards.Button(c.T("button.travel_options", nil), AddrTravelOptions, v.ToCode); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap}))
	return c.respond(paragraphs(
		c.T("travel.checkout_title", map[string]any{
			"to": c.CityName(v.ToCode, v.To), "from": c.CityName(v.FromCode, v.From), "mode": mode,
		}),
		facts,
		pay,
	), kb.Build())
}

// TravelStarted renders a departure.
//
// Its refresh button opens the journey itself, so there is no separate
// "my journey" button: two buttons with one destination is one too many.
func TravelStarted(c Context, v TravelStartedView) *presenter.Response {
	return c.withView(renderTravelStarted(c, v), ScreenTravelStarted, v)
}

func renderTravelStarted(c Context, v TravelStartedView) *presenter.Response {
	var fare string
	if v.Fare > 0 {
		fare = c.T("travel.started_fare", map[string]any{"fare": FormatMoney(c, v.Fare)})
	}
	text := body(c.T("travel.started", map[string]any{
		"from":     c.CityName(v.FromCode, v.From),
		"to":       c.CityName(v.ToCode, v.To),
		"mode":     c.ModeName(v.ModeCode, v.ModeName),
		"duration": FormatDuration(c, v.Duration),
		"energy":   FormatNumber(c, int64(v.Energy)),
	}), clockLine(c, "travel.arrives_at", v.ArrivesAt), fare)

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(text, kb.Build())
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
	return c.withView(renderTravelStatus(c, v), ScreenTravelStatus, v)
}

func renderTravelStatus(c Context, v TravelStatusView) *presenter.Response {
	args := map[string]any{
		"from": c.CityName(v.FromCode, v.From),
		"to":   c.CityName(v.ToCode, v.To),
	}
	key := "travel.status_arriving"
	var arrives string
	if v.Remaining >= arrivingThreshold {
		key = "travel.status"
		args["remaining"] = FormatDuration(c, v.Remaining)
		arrives = clockLine(c, "travel.arrives_at", v.ArrivesAt)
	}
	var mode string
	if v.ModeCode != "" || v.ModeName != "" {
		mode = c.T("travel.status_mode", map[string]any{"mode": c.ModeName(v.ModeCode, v.ModeName)})
	}

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrTravelStatus}))

	return c.respond(body(c.T(key, args), arrives, mode), kb.Build())
}

// TravelArrived renders the arrival notification.
func TravelArrived(c Context, v TravelArrivedView) *presenter.Response {
	return c.withView(renderTravelArrived(c, v), ScreenTravelArrived, v)
}

func renderTravelArrived(c Context, v TravelArrivedView) *presenter.Response {
	var xp string
	if v.XP > 0 {
		xp = c.T("travel.arrived_xp", map[string]any{"xp": FormatNumber(c, v.XP)})
	}
	text := body(c.T("travel.arrived", map[string]any{"city": c.CityName(v.CityCode, v.City)}), xp)

	kb := keyboards.New()
	worldMap, _ := keyboards.Button(c.T("button.map", nil), AddrMap)
	home, _ := keyboards.Button(c.T("button.profile", nil), AddrHome)
	kb.Row(home, worldMap)

	return presenter.Message(text, kb.Build())
}
