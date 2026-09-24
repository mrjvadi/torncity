package notification

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Route is one row of the notification table: an event, and how to turn it
// into a screen. The next notification is one more row in Routes.
type Route struct {
	// Domain and Event name the event as subjects.Event spells it, for
	// example "travel" and "completed".
	Domain string
	Event  string
	// Name tells two routes on one event apart ("announce"); it is part of
	// the consumer's name. Empty for the event's private notice.
	Name string

	// Render reads the event and returns what to tell whom, or nil when the
	// event is nothing to tell anyone about.
	Render Renderer
	// Announce, instead of Render, turns the event into a public line in a
	// city's groups (announce.go).
	Announce Announcer
}

// Subject is the event subject this route consumes.
func (r Route) Subject() string { return subjects.Event(r.Domain, r.Event) }

// Durable is this route's consumer name, and its inbox `consumer` column.
//
// One durable per event type, as cmd/game has one per command: a slow
// renderer for one event must not sit in front of every other, and the inbox
// key (message_id, consumer) then says "notified about THIS event" rather
// than "notified about something from this request".
func (r Route) Durable() string {
	d := "notifier-" + r.Domain + "-" + strings.ReplaceAll(r.Event, ".", "-")
	if r.Name != "" {
		d += "-" + r.Name
	}
	return d
}

// Renderer turns one event into a Draft.
//
// An error classified as anything but internal (apperrors.CodeOf) means the
// event can never be rendered — a payload that does not parse, a city that
// does not exist — and the event is dropped with a log line. An internal
// error is retried.
type Renderer func(ctx context.Context, deps Deps, env *envelope.Envelope) (*Draft, error)

// Deps is what a renderer may read. Everything is read-only.
type Deps struct {
	Cities Cities
}

// Draft is a notification before it knows its language.
//
// The renderer names the player and lays out the screen; the worker then
// reads the player's stored language and chooses the bot. Keeping the
// language out of the renderer is what keeps every notification on the one
// language rule (handlers.RenderLanguage).
type Draft struct {
	PlayerID string
	Screen   func(c screens.Context) *presenter.Response
}

// Routes is the table. Add a row here, and a renderer below, for every new
// notification; nothing else changes.
func Routes() []Route {
	return []Route{
		{Domain: "travel", Event: "completed", Render: renderTravelCompleted},
		{Domain: "bank", Event: "payment_received", Render: renderPaymentReceived},
		{Domain: "social", Event: "friend_requested", Render: renderFriendRequested},
		{Domain: "social", Event: "friend_accepted", Render: renderFriendAccepted},
		{Domain: "education", Event: "completed", Render: renderCourseCompleted},
		{Domain: "job", Event: "shift_worked", Render: renderShiftWorked},

		// Crime (docs/adr/0019-crime-engine.md): see crime.go.
		{Domain: "crime", Event: "victimised", Render: renderVictimised},
		{Domain: "crime", Event: "take", Render: renderCrimeResult},
		{Domain: "crime", Event: "resolved", Render: renderCrimeResult},
		{Domain: "crime", Event: "released", Render: renderReleased},
		{Domain: "crime", Event: "case_solved", Render: renderCaseSolved},
		{Domain: "crime", Event: "case_closed", Render: renderCaseClosed},
		{Domain: "crime", Event: "convicted", Render: renderConvicted},

		{Domain: "inventory", Event: "given", Render: renderItemGiven},
		{Domain: "market", Event: "filled", Render: renderMarketFilled},
		{Domain: "market", Event: "expired", Render: renderMarketExpired},
		{Domain: "auction", Event: "outbid", Render: renderAuction("outbid")},
		{Domain: "auction", Event: "won", Render: renderAuction("won")},
		{Domain: "auction", Event: "sold", Render: renderAuction("sold")},
		{Domain: "auction", Event: "unsold", Render: renderAuction("unsold")},
		{Domain: "election", Event: "result", Render: renderElectionResult},

		// Public lines in a city's groups (announce.go). Each has its own
		// consumer beside the event's private notice, if any.
		{Domain: "travel", Event: "completed", Name: "announce", Announce: arrivalAnnouncement},
		{Domain: "crime", Event: "jailed", Name: "announce", Announce: jailAnnouncement},
		{Domain: "election", Event: "opened", Name: "announce", Announce: electionOpenedAnnouncement},
		{Domain: "election", Event: "stood", Name: "announce", Announce: electionStoodAnnouncement},
		{Domain: "election", Event: "voting", Name: "announce", Announce: electionVotingAnnouncement},
		{Domain: "election", Event: "counted", Name: "announce", Announce: electionCountedAnnouncement},
		{Domain: "bank", Event: "payment_received", Name: "announce", Announce: paymentAnnouncement},
	}
}

// travelCompleted is the payload TravelHandler.Complete writes. Only the
// fields a notification needs are read.
type travelCompleted struct {
	PlayerID string `json:"player_id"`
	ToCityID string `json:"to_city_id"`
	XP       int64  `json:"xp"`
	Levels   []int  `json:"levels"`
}

// renderTravelCompleted is the arrival notice.
//
// The event carries the destination's id, not its code or name: a city is
// looked up so the player reads its name in their own language (city.<code>
// in the catalogue), and never the id.
func renderTravelCompleted(ctx context.Context, deps Deps, env *envelope.Envelope) (*Draft, error) {
	var ev travelCompleted
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("travel.completed payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.ToCityID == "" {
		return nil, apperrors.InvalidInput("travel.completed names no player or no city")
	}

	city, err := deps.Cities.ByID(ctx, ev.ToCityID)
	if err != nil {
		return nil, err
	}

	view := screens.ArrivalNoticeView{
		TravelArrivedView: screens.TravelArrivedView{CityCode: city.Code, City: city.Name, XP: ev.XP},
		Levels:            ev.Levels,
	}
	return &Draft{
		PlayerID: ev.PlayerID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.ArrivalNotice(c, view) },
	}, nil
}

// Cities is the read a renderer needs to name a city.
type Cities interface {
	// ByID returns the city, or an error classified NOT_FOUND
	// (application.ErrCityNotFound) when there is none.
	ByID(ctx context.Context, id string) (*application.City, error)
}
