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

	// Link is a callback address (keyboards.Data) the inbox item's "open"
	// button replays, for a notice with a screen of its own worth pointing
	// at (a company's period report opens that company). Empty is common
	// and fine: the item simply has no button beyond the category list's
	// own navigation. Only read for a route classified ModeInbox
	// (badge.go); an instant notice's own keyboard already does this job.
	Link string
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

		// Companies: see company.go.
		{Domain: "company", Event: "applied", Render: renderCompanyApplied},
		{Domain: "company", Event: "employee", Render: renderCompanyEmployee},
		{Domain: "company", Event: "manager_appointed", Render: renderCompanyManager},
		{Domain: "company", Event: "period_settled", Render: renderCompanyPeriod},

		// The production economy: see production.go.
		{Domain: "company", Event: "researched", Render: renderProduction("researched")},
		{Domain: "company", Event: "produced", Render: renderProduction("produced")},
		{Domain: "company", Event: "reversed", Render: renderProduction("reversed")},
		{Domain: "company", Event: "license_sold", Render: renderProduction("license_sold")},
		{Domain: "company", Event: "sold", Render: renderProduction("sold")},

		// Specialist recruitment: see recruit.go.
		{Domain: "company", Event: "recruit", Render: renderRecruit},

		// The armed forces, diplomacy and appointments: see military.go.
		{Domain: "military", Event: "arrived", Render: renderMoveArrived},
		{Domain: "military", Event: "licence_applied", Render: renderLicence("licence_applied")},
		{Domain: "military", Event: "licence_decided", Render: renderLicence("licence_decided")},
		{Domain: "diplomacy", Event: "treaty_proposed", Render: renderTreatyProposed},
		{Domain: "governance", Event: "appointed", Render: renderOffice(false)},
		{Domain: "governance", Event: "dismissed", Render: renderOffice(true)},

		// War: see war.go.
		{Domain: "war", Event: "report", Render: renderWarReport},
		{Domain: "war", Event: "ally_called", Render: renderWarNotice("war.ally_called")},
		{Domain: "war", Event: "proposed", Render: renderWarNotice("war.proposed")},
		{Domain: "war", Event: "city_struck", Render: renderWarNotice("war.city_struck")},

		// Health: see health.go.
		{Domain: "health", Event: "hospitalised", Render: renderHospitalised},
		{Domain: "health", Event: "discharged", Render: renderDischarged},
		{Domain: "health", Event: "clinic_treated", Render: renderClinicTreated},

		// Factions: see faction.go.
		{Domain: "faction", Event: "invited", Render: renderFactionRequest("invite")},
		{Domain: "faction", Event: "applied", Render: renderFactionRequest("apply")},
		{Domain: "faction", Event: "answered", Render: renderFactionAnswer},
		{Domain: "faction", Event: "kicked", Render: renderFactionKicked},
		{Domain: "faction", Event: "crime_settled", Render: renderFactionCrime},

		// Missions: see mission.go.
		{Domain: "mission", Event: "completed", Render: renderMissionCompleted},

		// Legislatures: see legislature.go.
		{Domain: "legislature", Event: "decided", Render: renderBillDecided},

		// Achievements: see achievement.go.
		{Domain: "achievement", Event: "awarded", Render: renderAchievement},
		// A character's life: see life.go.
		{Domain: "life", Event: "rank_changed", Render: renderRankChanged},
		// The one urgent need alert this feature adds (docs/adr/0025's
		// needs, life_common.go's alertHunger): always instant, by
		// configs/notifications/delivery.yml.
		{Domain: "life", Event: "hunger_low", Render: renderHungerLow},
		// Finance: see finance.go.
		{Domain: "loan", Event: "due", Render: renderFinanceNotice},
		{Domain: "loan", Event: "missed", Render: renderFinanceNotice},
		{Domain: "loan", Event: "defaulted", Render: renderFinanceNotice},
		{Domain: "loan", Event: "repaid", Render: renderFinanceNotice},
		{Domain: "insurance", Event: "claimed", Render: renderFinanceNotice},
		{Domain: "insurance", Event: "lapsed", Render: renderFinanceNotice},
		{Domain: "insurance", Event: "gone", Render: renderFinanceNotice},
		{Domain: "stock", Event: "filled", Render: renderStockNotice},
		{Domain: "stock", Event: "dividend", Render: renderStockNotice},
		{Domain: "stock", Event: "takeover", Render: renderStockNotice},

		// Property: see property.go.
		{Domain: "property", Event: "sold", Render: renderPropertyNotice},
		{Domain: "property", Event: "let", Render: renderPropertyNotice},
		{Domain: "property", Event: "tenant_left", Render: renderPropertyNotice},
		{Domain: "property", Event: "foreclosed", Render: renderPropertyNotice},
		{Domain: "property", Event: "evicted", Render: renderPropertyNotice},
		{Domain: "property", Event: "evicted_tenant", Render: renderPropertyNotice},

		// Public lines in a city's groups (announce.go). Each has its own
		// consumer beside the event's private notice, if any.
		{Domain: "travel", Event: "completed", Name: "announce", Announce: arrivalAnnouncement},
		{Domain: "crime", Event: "jailed", Name: "announce", Announce: jailAnnouncement},
		{Domain: "election", Event: "opened", Name: "announce", Announce: electionOpenedAnnouncement},
		{Domain: "election", Event: "stood", Name: "announce", Announce: electionStoodAnnouncement},
		{Domain: "election", Event: "voting", Name: "announce", Announce: electionVotingAnnouncement},
		{Domain: "election", Event: "counted", Name: "announce", Announce: electionCountedAnnouncement},
		{Domain: "bank", Event: "payment_received", Name: "announce", Announce: paymentAnnouncement},
		{Domain: "company", Event: "founded", Name: "announce", Announce: companyFoundedAnnouncement},
		{Domain: "company", Event: "closed", Name: "announce", Announce: companyClosedAnnouncement},
		{Domain: "company", Event: "tech_published", Name: "announce", Announce: techPublishedAnnouncement},
		{Domain: "company", Event: "product_launched", Name: "announce", Announce: productLaunchedAnnouncement},
		{Domain: "company", Event: "recruit_ad", Name: "announce", Announce: recruitAdAnnouncement},
		{Domain: "military", Event: "procured", Name: "announce", Announce: procuredAnnouncement},
		{Domain: "military", Event: "licence_granted", Name: "announce", Announce: licenceAnnouncement("granted")},
		{Domain: "military", Event: "licence_revoked", Name: "announce", Announce: licenceAnnouncement("revoked")},
		{Domain: "diplomacy", Event: "sanction_imposed", Name: "announce", Announce: sanctionImposedAnnouncement},
		{Domain: "diplomacy", Event: "sanction_lifted", Name: "announce", Announce: sanctionLiftedAnnouncement},
		{Domain: "diplomacy", Event: "treaty_signed", Name: "announce", Announce: treatySignedAnnouncement},
		{Domain: "diplomacy", Event: "treaty_terminated", Name: "announce", Announce: treatyEndedAnnouncement},
		{Domain: "governance", Event: "appointed", Name: "announce", Announce: appointedAnnouncement},
		{Domain: "war", Event: "declared", Name: "announce", Announce: warDeclaredAnnouncement},
		{Domain: "war", Event: "joined", Name: "announce", Announce: warJoinedAnnouncement},
		{Domain: "war", Event: "settled", Name: "announce", Announce: warSettledAnnouncement},
		{Domain: "war", Event: "struck", Name: "announce", Announce: warStruckAnnouncement},
		{Domain: "war", Event: "taken", Name: "announce", Announce: warTakenAnnouncement},
		{Domain: "health", Event: "hospitalised", Name: "announce", Announce: hospitalisedAnnouncement},
		{Domain: "faction", Event: "founded", Name: "announce", Announce: factionFoundedLine},
		{Domain: "faction", Event: "joined", Name: "announce", Announce: factionGroupLine("joined")},
		{Domain: "faction", Event: "left", Name: "announce", Announce: factionGroupLine("left")},
		{Domain: "faction", Event: "linked", Name: "announce", Announce: factionGroupLine("linked")},
		{Domain: "faction", Event: "ranked", Name: "announce", Announce: factionGroupLine("ranked")},
		{Domain: "faction", Event: "disbanded", Name: "announce", Announce: factionGroupLine("disbanded")},
		{Domain: "faction", Event: "planned", Name: "announce", Announce: factionGroupLine("planned")},
		{Domain: "faction", Event: "launched", Name: "announce", Announce: factionGroupLine("launched")},
		{Domain: "faction", Event: "crime_resolved", Name: "announce", Announce: factionGroupLine("crime_resolved")},
		{Domain: "legislature", Event: "proposed", Name: "announce", Announce: billOpenedAnnouncement},
		{Domain: "legislature", Event: "decided", Name: "announce", Announce: billDecidedAnnouncement},
		{Domain: "admin", Event: "announced", Name: "announce", Announce: operatorAnnouncement},
		{Domain: "admin", Event: "broadcast", Render: renderBroadcast},
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
