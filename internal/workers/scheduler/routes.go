package scheduler

import "sort"

// This file is the one place a game_actions.action_type becomes a command
// subject. Nothing else in this package, and nothing in cmd/scheduler, may
// spell a domain or an action: adding a scheduled operation to the game means
// adding one line to the table below and nothing anywhere else.
//
// The single table is what makes an unrecognised action_type detectable at
// all. Spread over a switch in one file and a string literal in another, the
// missing case is the one nobody writes, and the row would be claimed, skipped
// and re-claimed forever while every metric said the scheduler was healthy.

// Route is the destination of one action type.
//
// Domain and Action are the two halves subjects.Command takes. The split is
// not cosmetic: the domain is what a JetStream consumer filters on, so
// "travel" must be one token, while the action may be several
// ("profile.get"), exactly as routing.SplitCommand defines it for a command
// arriving from a player.
type Route struct {
	Domain string
	Action string
}

// Command is the domain.action spelling carried in Metadata.Command, the same
// form the gateway puts there for a player's command.
func (r Route) Command() string { return r.Domain + "." + r.Action }

// Action types. They are the values written into game_actions.action_type by
// whoever schedules the work, and they are part of the database's contract:
// rows written under one spelling are still in the table after a rename.
const (
	// ActionTypeTravel is a journey between two cities reaching its
	// destination. It is the only scheduled operation phase 1 has
	// (ROADMAP.md, Phase 1).
	ActionTypeTravel = "travel"

	// ActionTypeEducation is a course reaching its end: the enrolment is
	// completed and its certificate and skill XP granted.
	ActionTypeEducation = "education"

	// ActionTypeShift is a shift of work reaching its end: the pay, tax, XP,
	// skill XP and performance are settled.
	ActionTypeShift = "work_shift"

	// ActionTypeCrime is a timed crime reaching its end: it is rolled and
	// settled.
	ActionTypeCrime = "crime"

	// ActionTypeInvestigation is a reported theft's investigation reaching
	// its end: solved or closed.
	ActionTypeInvestigation = "crime_investigation"

	// ActionTypeJailRelease is a sentence served.
	ActionTypeJailRelease = "jail_release"

	// ActionTypePlaceMove is a walk between two places of a city ending.
	ActionTypePlaceMove = "place_move"

	// ActionTypeMarketExpiry is a resting market order's time running out:
	// what is left of its escrow comes back.
	ActionTypeMarketExpiry = "market_expiry"

	// ActionTypeAuctionClose is an auction reaching its end: sold to the
	// standing bid or returned unsold.
	ActionTypeAuctionClose = "auction_close"

	// ActionTypeElectionVoting is an election's candidacy ending: the vote
	// opens.
	ActionTypeElectionVoting = "election_voting"

	// ActionTypeElectionCount is an election's vote ending: the count.
	ActionTypeElectionCount = "election_count"

	// ActionTypeElectionOpen is an elected office's term running out (or a
	// seat left unfilled): the next election opens.
	ActionTypeElectionOpen = "election_open"

	// ActionTypeCompanyPeriod is a city's company period ending: its
	// companies are paid by the population and pay their upkeep.
	ActionTypeCompanyPeriod = "company_period"

	// The production economy: a company's research, a production order
	// and a reverse engineering reaching their end.
	ActionTypeResearch   = "company_research"
	ActionTypeProduction = "production_order"
	ActionTypeReverse    = "reverse_engineering"

	// The armed forces (docs/adr/0022): a country's defence period ending,
	// and equipment reaching its garrison.
	ActionTypeMilitaryPeriod = "military_period"
	ActionTypeMilitaryMove   = "military_move"

	// War (docs/adr/0022, part two): an operation reaching its target.
	ActionTypeWarOperation = "war_operation"
)

// routes maps an action type to the command it is published as.
//
// Only travel, education and shifts are here, and the absence of the rest is
// deliberate. Salary, production, healing and auction expiry are all named in
// 19_SCHEDULER_WORKERS.md as future scheduled work, but nothing writes those
// rows yet and no handler consumes those subjects. An entry for a command
// nobody serves would turn a loud, findable "no route for this action type"
// into a message published into a stream with no consumer, which looks like
// success from here and is not.
var routes = map[string]Route{
	ActionTypeTravel:    {Domain: "travel", Action: "arrive"},
	ActionTypeEducation: {Domain: "education", Action: "complete"},
	ActionTypeShift:     {Domain: "job", Action: "finish_shift"},

	ActionTypeCrime:         {Domain: "crime", Action: "resolve"},
	ActionTypeInvestigation: {Domain: "crime", Action: "conclude"},
	ActionTypeJailRelease:   {Domain: "crime", Action: "release"},

	ActionTypePlaceMove: {Domain: "place", Action: "arrive"},

	ActionTypeMarketExpiry: {Domain: "market", Action: "expire"},
	ActionTypeAuctionClose: {Domain: "auction", Action: "close"},

	ActionTypeElectionVoting: {Domain: "election", Action: "voting"},
	ActionTypeElectionCount:  {Domain: "election", Action: "count"},
	ActionTypeElectionOpen:   {Domain: "election", Action: "open"},

	ActionTypeCompanyPeriod: {Domain: "company", Action: "settle"},

	ActionTypeResearch:   {Domain: "company", Action: "researched"},
	ActionTypeProduction: {Domain: "company", Action: "produced"},
	ActionTypeReverse:    {Domain: "company", Action: "reversed"},

	ActionTypeMilitaryPeriod: {Domain: "military", Action: "settle"},
	ActionTypeMilitaryMove:   {Domain: "military", Action: "arrive"},

	ActionTypeWarOperation: {Domain: "war", Action: "resolve"},
}

// RouteFor returns the route for an action type, and whether there is one.
//
// A missing route is a reported fact, never a default. Guessing a subject from
// the action type — travel -> travel.travel, say — would publish onto a
// subject no consumer filters for, and the action would be marked complete for
// work that never happened.
func RouteFor(actionType string) (Route, bool) {
	r, ok := routes[actionType]
	return r, ok
}

// ActionTypes lists every routable action type, sorted, so a process can say
// at startup what it is able to dispatch. An operator comparing that line with
// the action_types actually present in the table is how a row that this build
// cannot route is noticed before it comes due.
func ActionTypes() []string {
	out := make([]string, 0, len(routes))
	for t := range routes {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
