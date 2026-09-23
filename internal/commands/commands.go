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

	// Work: the player's job, the openings in their city, applying, a shift,
	// promotion and leaving. The base employer pays each shift as it is
	// worked.
	{Domain: "job", Action: "status", Origin: FromPlayer},
	{Domain: "job", Action: "list", Origin: FromPlayer},
	{Domain: "job", Action: "view", Origin: FromPlayer},
	{Domain: "job", Action: "apply", Origin: FromPlayer},
	{Domain: "job", Action: "work", Origin: FromPlayer},
	{Domain: "job", Action: "promote", Origin: FromPlayer},
	{Domain: "job", Action: "quit", Origin: FromPlayer},

	// Study. education.complete, like travel.arrive, only the scheduler
	// sends: a course finishes when its time is up.
	{Domain: "education", Action: "list", Origin: FromPlayer},
	{Domain: "education", Action: "view", Origin: FromPlayer},
	{Domain: "education", Action: "enroll", Origin: FromPlayer},
	{Domain: "education", Action: "complete", Origin: FromScheduler},
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
