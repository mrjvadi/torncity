// Package commands is the table of every command the game serves.
//
// It is read from three places, which is why it lives here and not inside any
// one of them:
//
//   - cmd/game subscribes to every entry and binds each to a handler, and
//     refuses to start if an entry is left unbound or a handler has no entry.
//   - the scheduler publishes commands that only the game handles, and a test
//     in internal/workers/scheduler asserts that every subject it can publish
//     is listed here as a scheduled command.
//   - the gateway checks every command a player sends against this table
//     BEFORE publishing it (internal/gateway/routing.Route).
//
// The last one is not a formality. The command stream is a JetStream work
// queue: a publish to a subject nobody consumes SUCCEEDS, with no error and no
// consumer, so a command that is not in this table does not fail — it
// vanishes, and the player who sent it waits for a reply that never comes.
// Checking at the edge turns that silence into an answer.
//
// The table holds names only. What each command does is the handler's
// business.
package commands

import (
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// Origin says who sends a command.
type Origin int

const (
	// FromPlayer is a command a player sent through the gateway. It carries
	// a bot and a chat, and the player is waiting for a reply.
	FromPlayer Origin = iota

	// FromScheduler is a command a clock produced. No bot, no chat, and
	// nobody waiting: its outcome reaches the player as an event.
	FromScheduler
)

// Subscription is one command the game service consumes: one entry of the
// table.
type Subscription struct {
	// Domain and Action are the two halves subjects.Command takes. The
	// domain is one token; the action may be several ("friend.add").
	Domain string
	Action string
	Origin Origin
}

// Command is the domain.action spelling carried in Metadata.Command.
func (s Subscription) Command() string { return s.Domain + "." + s.Action }

// Subject is the JetStream subject the command arrives on.
func (s Subscription) Subject() string { return subjects.Command(s.Domain, s.Action) }

// Durable is the name of the durable consumer for this command, and also the
// inbox `consumer` column for it: the two must be the same string, because
// the inbox key is (message_id, consumer).
//
// It is derived rather than listed so that the phase 0 name,
// game-player-profile-get, is reproduced exactly — a renamed durable would be
// a new consumer that starts from the head of the stream.
func (s Subscription) Durable() string {
	return "game-" + s.Domain + "-" + strings.ReplaceAll(s.Action, ".", "-")
}

// all is the table. The command stream is a work queue, so no two entries may
// overlap; each is a single literal subject.
var all = []Subscription{
	// Phase 0: first contact and the profile screen.
	{Domain: "player", Action: "profile.get", Origin: FromPlayer},

	// Settings: the screen, and the one setting it has so far.
	{Domain: "player", Action: "settings", Origin: FromPlayer},
	{Domain: "player", Action: "language.set", Origin: FromPlayer},

	// Phase 1: travel. travel.arrive is the one command in the game that no
	// player can send; the scheduler publishes it when a journey comes due.
	{Domain: "travel", Action: "start", Origin: FromPlayer},
	// travel.options is the choice of transport to one city, each mode with
	// its fare and wait; travel.start departs by the mode chosen there.
	{Domain: "travel", Action: "options", Origin: FromPlayer},
	{Domain: "travel", Action: "status", Origin: FromPlayer},
	{Domain: "travel", Action: "arrive", Origin: FromScheduler},

	// Phase 1: skills, the world map and the social graph.
	{Domain: "skills", Action: "list", Origin: FromPlayer},
	{Domain: "map", Action: "list", Origin: FromPlayer},
	// City places (configs/content/places.yml): map.list is the map of the
	// player's own city, map.cities the other cities to travel to, place.go
	// a walk to another place; only the scheduler sends place.arrive, when
	// the walk is over.
	{Domain: "map", Action: "cities", Origin: FromPlayer},
	{Domain: "place", Action: "go", Origin: FromPlayer},
	{Domain: "place", Action: "arrive", Origin: FromScheduler},
	{Domain: "social", Action: "search", Origin: FromPlayer},
	{Domain: "social", Action: "friend.add", Origin: FromPlayer},
	{Domain: "social", Action: "friend.accept", Origin: FromPlayer},
	{Domain: "social", Action: "friend.list", Origin: FromPlayer},

	// The bank: balances, deposits and withdrawals in a city, and payments
	// between players — cash face to face, card from anywhere. bank.pay is
	// the screen and the confirmation; only bank.pay.send moves money.
	{Domain: "bank", Action: "show", Origin: FromPlayer},
	{Domain: "bank", Action: "deposit", Origin: FromPlayer},
	{Domain: "bank", Action: "withdraw", Origin: FromPlayer},
	{Domain: "bank", Action: "pay", Origin: FromPlayer},
	{Domain: "bank", Action: "pay.send", Origin: FromPlayer},

	// Player-held offices (ADR 0015): a city's offices and policies, its
	// public history, the office holder's screen and the change flow.
	{Domain: "gov", Action: "city", Origin: FromPlayer},
	{Domain: "gov", Action: "history", Origin: FromPlayer},
	{Domain: "gov", Action: "office", Origin: FromPlayer},
	{Domain: "gov", Action: "lever", Origin: FromPlayer},
	{Domain: "gov", Action: "confirm", Origin: FromPlayer},
	{Domain: "gov", Action: "set", Origin: FromPlayer},
	// Appointments by office holders (docs/adr/0022): the appointer names a
	// player and confirms; a holder who may remove another confirms that.
	{Domain: "gov", Action: "appoint", Origin: FromPlayer},
	{Domain: "gov", Action: "seat", Origin: FromPlayer},
	{Domain: "gov", Action: "dismiss", Origin: FromPlayer},
	{Domain: "gov", Action: "unseat", Origin: FromPlayer},

	// Work: the player's job, the openings in their city, applying, starting
	// a shift, promotion and leaving. job.work STARTS a shift; only the
	// scheduler sends job.finish_shift, when the shift's time is up, and the
	// base employer pays it then.
	{Domain: "job", Action: "status", Origin: FromPlayer},
	{Domain: "job", Action: "list", Origin: FromPlayer},
	{Domain: "job", Action: "view", Origin: FromPlayer},
	{Domain: "job", Action: "apply", Origin: FromPlayer},
	{Domain: "job", Action: "work", Origin: FromPlayer},
	{Domain: "job", Action: "promote", Origin: FromPlayer},
	{Domain: "job", Action: "quit", Origin: FromPlayer},
	{Domain: "job", Action: "finish_shift", Origin: FromScheduler},

	// Study. education.complete, like travel.arrive, only the scheduler
	// sends: a course finishes when its time is up.
	{Domain: "education", Action: "list", Origin: FromPlayer},
	{Domain: "education", Action: "view", Origin: FromPlayer},
	{Domain: "education", Action: "enroll", Origin: FromPlayer},
	{Domain: "education", Action: "complete", Origin: FromScheduler},

	// Crime (docs/adr/0019-crime-engine.md): the hub, a category's crimes,
	// one crime, committing it where the player stands (no victim is ever
	// named: chance picks one from who is nearby), the record, jail and bail,
	// and the victim's report and cases. Only the scheduler sends the last
	// three: a timed crime's end, an investigation's end, a sentence served.
	{Domain: "crime", Action: "hub", Origin: FromPlayer},
	{Domain: "crime", Action: "list", Origin: FromPlayer},
	{Domain: "crime", Action: "view", Origin: FromPlayer},
	{Domain: "crime", Action: "commit", Origin: FromPlayer},
	{Domain: "crime", Action: "record", Origin: FromPlayer},
	{Domain: "crime", Action: "jail", Origin: FromPlayer},
	{Domain: "crime", Action: "bail", Origin: FromPlayer},
	{Domain: "crime", Action: "report", Origin: FromPlayer},
	{Domain: "crime", Action: "cases", Origin: FromPlayer},
	{Domain: "crime", Action: "resolve", Origin: FromScheduler},
	{Domain: "crime", Action: "conclude", Origin: FromScheduler},
	{Domain: "crime", Action: "release", Origin: FromScheduler},

	// Goods (migrations/0017_items_and_trade.up.sql): what the player
	// carries, using, giving and dropping it; the city shops, buying and
	// selling back; the player market's books, orders and cancels; the
	// auction house. Only the scheduler sends market.expire and
	// auction.close, when a resting order's or an auction's time is up.
	{Domain: "inventory", Action: "show", Origin: FromPlayer},
	{Domain: "inventory", Action: "item", Origin: FromPlayer},
	{Domain: "inventory", Action: "use", Origin: FromPlayer},
	{Domain: "inventory", Action: "give", Origin: FromPlayer},
	{Domain: "inventory", Action: "drop", Origin: FromPlayer},
	{Domain: "shop", Action: "list", Origin: FromPlayer},
	{Domain: "shop", Action: "view", Origin: FromPlayer},
	{Domain: "shop", Action: "buy", Origin: FromPlayer},
	{Domain: "shop", Action: "offers", Origin: FromPlayer},
	{Domain: "shop", Action: "sell", Origin: FromPlayer},
	{Domain: "market", Action: "list", Origin: FromPlayer},
	{Domain: "market", Action: "book", Origin: FromPlayer},
	{Domain: "market", Action: "order", Origin: FromPlayer},
	{Domain: "market", Action: "cancel", Origin: FromPlayer},
	{Domain: "market", Action: "mine", Origin: FromPlayer},
	{Domain: "market", Action: "expire", Origin: FromScheduler},
	{Domain: "auction", Action: "list", Origin: FromPlayer},
	{Domain: "auction", Action: "view", Origin: FromPlayer},
	{Domain: "auction", Action: "new", Origin: FromPlayer},
	{Domain: "auction", Action: "bid", Origin: FromPlayer},
	{Domain: "auction", Action: "mine", Origin: FromPlayer},
	{Domain: "auction", Action: "close", Origin: FromScheduler},

	// Elections (migrations/0018_elections.up.sql): a city's and its
	// country's elections, one election, standing and voting. Only the
	// scheduler sends election.voting, when the candidacy is over,
	// election.count, when the vote is over, and election.open, when a term
	// is running out.
	{Domain: "election", Action: "list", Origin: FromPlayer},
	{Domain: "election", Action: "view", Origin: FromPlayer},
	{Domain: "election", Action: "stand", Origin: FromPlayer},
	{Domain: "election", Action: "vote", Origin: FromPlayer},
	{Domain: "election", Action: "voting", Origin: FromScheduler},
	{Domain: "election", Action: "count", Origin: FromScheduler},
	{Domain: "election", Action: "open", Origin: FromScheduler},

	// Companies (migrations/0019_companies.up.sql): a city's registry and a
	// company's page; founding one at city hall; running it — money in and
	// out, prices, openings, staff, a manager, closing; an opening as an
	// applicant sees it, and applying. Only the scheduler sends
	// company.settle, when a city's period is over.
	{Domain: "company", Action: "list", Origin: FromPlayer},
	{Domain: "company", Action: "view", Origin: FromPlayer},
	{Domain: "company", Action: "register", Origin: FromPlayer},
	{Domain: "company", Action: "type", Origin: FromPlayer},
	{Domain: "company", Action: "found", Origin: FromPlayer},
	{Domain: "company", Action: "mine", Origin: FromPlayer},
	{Domain: "company", Action: "manage", Origin: FromPlayer},
	{Domain: "company", Action: "deposit", Origin: FromPlayer},
	{Domain: "company", Action: "withdraw", Origin: FromPlayer},
	{Domain: "company", Action: "price", Origin: FromPlayer},
	{Domain: "company", Action: "auto", Origin: FromPlayer},
	{Domain: "company", Action: "manager", Origin: FromPlayer},
	{Domain: "company", Action: "close", Origin: FromPlayer},
	{Domain: "company", Action: "openings", Origin: FromPlayer},
	{Domain: "company", Action: "post", Origin: FromPlayer},
	{Domain: "company", Action: "slots", Origin: FromPlayer},
	{Domain: "company", Action: "staff", Origin: FromPlayer},
	{Domain: "company", Action: "decide", Origin: FromPlayer},
	{Domain: "company", Action: "fire", Origin: FromPlayer},
	{Domain: "company", Action: "opening", Origin: FromPlayer},
	{Domain: "company", Action: "apply", Origin: FromPlayer},
	{Domain: "company", Action: "settle", Origin: FromScheduler},

	// The production economy (migrations/0020_production.up.sql): a
	// company's warehouse and the NPC suppliers it buys from; its research
	// lab, a research, how a technology is shared, a license bought; its
	// design studio — a new draft, a design, a slot filled, a quantity, a
	// name, the final design; its production orders; its reverse
	// engineering; its listings; and the city's company goods and buying
	// them. Only the scheduler sends company.researched, company.produced
	// and company.reversed, when a research, an order or a reverse
	// engineering is done.
	{Domain: "company", Action: "warehouse", Origin: FromPlayer},
	{Domain: "company", Action: "suppliers", Origin: FromPlayer},
	{Domain: "company", Action: "supply", Origin: FromPlayer},
	{Domain: "company", Action: "lab", Origin: FromPlayer},
	{Domain: "company", Action: "research", Origin: FromPlayer},
	{Domain: "company", Action: "techmode", Origin: FromPlayer},
	{Domain: "company", Action: "license", Origin: FromPlayer},
	{Domain: "company", Action: "studio", Origin: FromPlayer},
	{Domain: "company", Action: "dnew", Origin: FromPlayer},
	{Domain: "company", Action: "design", Origin: FromPlayer},
	{Domain: "company", Action: "dfill", Origin: FromPlayer},
	{Domain: "company", Action: "dqty", Origin: FromPlayer},
	{Domain: "company", Action: "dname", Origin: FromPlayer},
	{Domain: "company", Action: "dfinal", Origin: FromPlayer},
	{Domain: "company", Action: "produce", Origin: FromPlayer},
	{Domain: "company", Action: "orders", Origin: FromPlayer},
	{Domain: "company", Action: "relab", Origin: FromPlayer},
	{Domain: "company", Action: "reverse", Origin: FromPlayer},
	{Domain: "company", Action: "sell", Origin: FromPlayer},
	{Domain: "company", Action: "listings", Origin: FromPlayer},
	{Domain: "company", Action: "unlist", Origin: FromPlayer},
	{Domain: "company", Action: "goods", Origin: FromPlayer},
	{Domain: "company", Action: "buy", Origin: FromPlayer},
	// Staged production (docs/adr/0021, section 14): one tap buys an
	// order's missing inputs; and a company's defence licence
	// (docs/adr/0022, section 2.14).
	{Domain: "company", Action: "stockup", Origin: FromPlayer},
	{Domain: "company", Action: "defence", Origin: FromPlayer},
	{Domain: "company", Action: "researched", Origin: FromScheduler},
	{Domain: "company", Action: "produced", Origin: FromScheduler},
	{Domain: "company", Action: "reversed", Origin: FromScheduler},
	// Specialist recruitment (docs/adr/0027): the hub, the campaign
	// builder, posting, a campaign and its candidates, the company's
	// specialists; and a campaign's check from the scheduler.
	{Domain: "company", Action: "recruit", Origin: FromPlayer},
	{Domain: "company", Action: "rnew", Origin: FromPlayer},
	{Domain: "company", Action: "rdraft", Origin: FromPlayer},
	{Domain: "company", Action: "rset", Origin: FromPlayer},
	{Domain: "company", Action: "ramount", Origin: FromPlayer},
	{Domain: "company", Action: "rpost", Origin: FromPlayer},
	{Domain: "company", Action: "rcamp", Origin: FromPlayer},
	{Domain: "company", Action: "rdecide", Origin: FromPlayer},
	{Domain: "company", Action: "rcancel", Origin: FromPlayer},
	{Domain: "company", Action: "npcs", Origin: FromPlayer},
	{Domain: "company", Action: "npc", Origin: FromPlayer},
	{Domain: "company", Action: "rcheck", Origin: FromScheduler},

	// The armed forces (docs/adr/0022-military-and-diplomacy.md): a
	// country's ministry of defence and its forces, one branch in full,
	// stationing equipment, and arms procurement. Only the scheduler sends
	// military.settle, when a country's defence period ends, and
	// military.arrive, when equipment reaches its garrison.
	{Domain: "military", Action: "ministry", Origin: FromPlayer},
	{Domain: "military", Action: "forces", Origin: FromPlayer},
	{Domain: "military", Action: "branch", Origin: FromPlayer},
	{Domain: "military", Action: "station", Origin: FromPlayer},
	{Domain: "military", Action: "procure", Origin: FromPlayer},
	{Domain: "military", Action: "buy", Origin: FromPlayer},
	{Domain: "military", Action: "licences", Origin: FromPlayer},
	{Domain: "military", Action: "licence", Origin: FromPlayer},
	{Domain: "military", Action: "settle", Origin: FromScheduler},
	{Domain: "military", Action: "arrive", Origin: FromScheduler},

	// Diplomacy: the sanctions board and imposing and lifting a sanction;
	// the treaties board and proposing, answering and ending a treaty; the
	// public record.
	{Domain: "diplomacy", Action: "sanctions", Origin: FromPlayer},
	{Domain: "diplomacy", Action: "impose", Origin: FromPlayer},
	{Domain: "diplomacy", Action: "lift", Origin: FromPlayer},
	{Domain: "diplomacy", Action: "treaties", Origin: FromPlayer},
	{Domain: "diplomacy", Action: "propose", Origin: FromPlayer},
	{Domain: "diplomacy", Action: "answer", Origin: FromPlayer},
	{Domain: "diplomacy", Action: "end", Origin: FromPlayer},
	{Domain: "diplomacy", Action: "history", Origin: FromPlayer},

	// War (docs/adr/0022, part two): the war board; declaring, joining,
	// proposing and answering a ceasefire or a peace, resuming; the war room,
	// a target, launching an operation. Only the scheduler sends war.resolve,
	// when an operation reaches its target.
	{Domain: "war", Action: "board", Origin: FromPlayer},
	{Domain: "war", Action: "declare", Origin: FromPlayer},
	{Domain: "war", Action: "join", Origin: FromPlayer},
	{Domain: "war", Action: "propose", Origin: FromPlayer},
	{Domain: "war", Action: "answer", Origin: FromPlayer},
	{Domain: "war", Action: "resume", Origin: FromPlayer},
	{Domain: "war", Action: "room", Origin: FromPlayer},
	{Domain: "war", Action: "target", Origin: FromPlayer},
	{Domain: "war", Action: "launch", Origin: FromPlayer},
	{Domain: "war", Action: "resolve", Origin: FromScheduler},

	// Health and hospitals (docs/adr/0023-health-missions-factions.md): the
	// hospital screen, a treatment, a clinic's desk, its price and whether
	// it takes patients. Only the scheduler sends health.discharge, when a
	// stay ends.
	{Domain: "health", Action: "hospital", Origin: FromPlayer},
	{Domain: "health", Action: "treat", Origin: FromPlayer},
	{Domain: "health", Action: "clinic", Origin: FromPlayer},
	{Domain: "health", Action: "price", Origin: FromPlayer},
	{Domain: "health", Action: "open", Origin: FromPlayer},
	{Domain: "health", Action: "discharge", Origin: FromScheduler},

	// Factions (docs/adr/0023): the factions of a city and a faction's page;
	// founding one; a member's faction, its members, inviting, applying,
	// answering, kicking, ranks and leaving; its bank; linking its group;
	// its organised crimes — the board, planning, joining, launching,
	// calling off. Only the scheduler sends faction.resolve, when an
	// organised crime ends.
	{Domain: "faction", Action: "list", Origin: FromPlayer},
	{Domain: "faction", Action: "view", Origin: FromPlayer},
	{Domain: "faction", Action: "found", Origin: FromPlayer},
	{Domain: "faction", Action: "mine", Origin: FromPlayer},
	{Domain: "faction", Action: "members", Origin: FromPlayer},
	{Domain: "faction", Action: "invite", Origin: FromPlayer},
	{Domain: "faction", Action: "apply", Origin: FromPlayer},
	{Domain: "faction", Action: "answer", Origin: FromPlayer},
	{Domain: "faction", Action: "kick", Origin: FromPlayer},
	{Domain: "faction", Action: "rank", Origin: FromPlayer},
	{Domain: "faction", Action: "leave", Origin: FromPlayer},
	{Domain: "faction", Action: "bank", Origin: FromPlayer},
	{Domain: "faction", Action: "deposit", Origin: FromPlayer},
	{Domain: "faction", Action: "withdraw", Origin: FromPlayer},
	{Domain: "faction", Action: "link", Origin: FromPlayer},
	{Domain: "faction", Action: "crime", Origin: FromPlayer},
	{Domain: "faction", Action: "plan", Origin: FromPlayer},
	{Domain: "faction", Action: "join", Origin: FromPlayer},
	{Domain: "faction", Action: "launch", Origin: FromPlayer},
	{Domain: "faction", Action: "calloff", Origin: FromPlayer},
	{Domain: "faction", Action: "resolve", Origin: FromScheduler},

	// Missions (docs/adr/0023): a city's boards and a board's missions, one
	// mission, taking it, the player's missions, handing goods in, and
	// giving one up. Missions move on from the game's own events, which
	// cmd/game consumes beside these commands.
	{Domain: "mission", Action: "board", Origin: FromPlayer},
	{Domain: "mission", Action: "view", Origin: FromPlayer},
	{Domain: "mission", Action: "accept", Origin: FromPlayer},
	{Domain: "mission", Action: "mine", Origin: FromPlayer},
	{Domain: "mission", Action: "deliver", Origin: FromPlayer},
	{Domain: "mission", Action: "abandon", Origin: FromPlayer},

	// Stage F (docs/adr/0024-property-and-politics.md). An allocation lever
	// — a city's budget — is edited, reviewed and set through gov.alloc,
	// gov.allocok and gov.allocset. Votes of a body: the proposals of the
	// player's places, one proposal, a member's vote; only the scheduler
	// sends law.close, when a proposal's window ends. A city's budget, and
	// — from the scheduler — the end of a city period.
	{Domain: "gov", Action: "alloc", Origin: FromPlayer},
	{Domain: "gov", Action: "allocok", Origin: FromPlayer},
	{Domain: "gov", Action: "allocset", Origin: FromPlayer},
	{Domain: "law", Action: "list", Origin: FromPlayer},
	{Domain: "law", Action: "view", Origin: FromPlayer},
	{Domain: "law", Action: "vote", Origin: FromPlayer},
	{Domain: "law", Action: "close", Origin: FromScheduler},
	{Domain: "city", Action: "budget", Origin: FromPlayer},
	{Domain: "city", Action: "settle", Origin: FromScheduler},

	// Property (docs/adr/0024): a city's market, one kind it sells, buying
	// from the city; the player's own, one property, offering it for sale or
	// to let and withdrawing the offer; an owner's offer, buying it, renting
	// it; leaving a rented home; resting at home. Each city period's charges
	// and rent run inside city.settle.
	{Domain: "property", Action: "list", Origin: FromPlayer},
	{Domain: "property", Action: "type", Origin: FromPlayer},
	{Domain: "property", Action: "purchase", Origin: FromPlayer},
	{Domain: "property", Action: "mine", Origin: FromPlayer},
	{Domain: "property", Action: "view", Origin: FromPlayer},
	{Domain: "property", Action: "sell", Origin: FromPlayer},
	{Domain: "property", Action: "let", Origin: FromPlayer},
	{Domain: "property", Action: "cancel", Origin: FromPlayer},
	{Domain: "property", Action: "offer", Origin: FromPlayer},
	{Domain: "property", Action: "buy", Origin: FromPlayer},
	{Domain: "property", Action: "rent", Origin: FromPlayer},
	{Domain: "property", Action: "leave", Origin: FromPlayer},
	{Domain: "property", Action: "rest", Origin: FromPlayer},

	// Achievements (docs/adr/0024): the player's achievements. They move on
	// from the game's own events, which cmd/game consumes beside these
	// commands.
	{Domain: "achievement", Action: "list", Origin: FromPlayer},

	// A character's life (docs/adr/0025-life-and-legacy.md): «🧬 زندگی من»,
	// a player's card, a life history, the bio and the avatar, a night at a
	// hostel or on a bench, and the leaderboards. Only the scheduler sends
	// life.refresh, when a leaderboard period ends. The game's own events
	// touch a life through a consumer cmd/game runs beside these commands.
	{Domain: "life", Action: "me", Origin: FromPlayer},
	{Domain: "life", Action: "card", Origin: FromPlayer},
	{Domain: "life", Action: "history", Origin: FromPlayer},
	{Domain: "life", Action: "bio", Origin: FromPlayer},
	{Domain: "life", Action: "avatar", Origin: FromPlayer},
	{Domain: "life", Action: "sleep", Origin: FromPlayer},
	{Domain: "life", Action: "top", Origin: FromPlayer},
	{Domain: "life", Action: "refresh", Origin: FromScheduler},

	// Finance (docs/adr/0026-finance.md): the national bank's loans and the
	// credit score, savings, insurance, the stock exchange and the gold
	// dealer. Only the scheduler sends finance.settle, when a finance period
	// ends.
	{Domain: "loan", Action: "hub", Origin: FromPlayer},
	{Domain: "loan", Action: "offer", Origin: FromPlayer},
	{Domain: "loan", Action: "take", Origin: FromPlayer},
	{Domain: "loan", Action: "view", Origin: FromPlayer},
	{Domain: "loan", Action: "repay", Origin: FromPlayer},
	{Domain: "save", Action: "show", Origin: FromPlayer},
	{Domain: "save", Action: "deposit", Origin: FromPlayer},
	{Domain: "save", Action: "withdraw", Origin: FromPlayer},
	{Domain: "insure", Action: "list", Origin: FromPlayer},
	{Domain: "insure", Action: "buy", Origin: FromPlayer},
	{Domain: "insure", Action: "cancel", Origin: FromPlayer},
	{Domain: "stock", Action: "list", Origin: FromPlayer},
	{Domain: "stock", Action: "view", Origin: FromPlayer},
	{Domain: "stock", Action: "buy", Origin: FromPlayer},
	{Domain: "stock", Action: "sell", Origin: FromPlayer},
	{Domain: "stock", Action: "cancel", Origin: FromPlayer},
	{Domain: "stock", Action: "mine", Origin: FromPlayer},
	{Domain: "stock", Action: "ipo", Origin: FromPlayer},
	{Domain: "stock", Action: "dividend", Origin: FromPlayer},
	{Domain: "gold", Action: "show", Origin: FromPlayer},
	{Domain: "gold", Action: "buy", Origin: FromPlayer},
	{Domain: "gold", Action: "sell", Origin: FromPlayer},
	{Domain: "finance", Action: "settle", Origin: FromScheduler},
}

// All returns every subscription. The slice is a copy.
func All() []Subscription {
	out := make([]Subscription, len(all))
	copy(out, all)
	return out
}

// Lookup returns the entry for a command in its domain.action spelling.
func Lookup(command string) (Subscription, bool) {
	for _, s := range all {
		if s.Command() == command {
			return s, true
		}
	}
	return Subscription{}, false
}

// FromPlayerCommand reports whether a player may send this command: it is in
// the table and a player, not a clock, is its origin. travel.arrive is in the
// table and is still not one, because only the scheduler lands a journey.
func FromPlayerCommand(command string) bool {
	s, ok := Lookup(command)
	return ok && s.Origin == FromPlayer
}

// Subjects returns every subject the game service consumes, sorted.
func Subjects() []string {
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, s.Subject())
	}
	sort.Strings(out)
	return out
}
