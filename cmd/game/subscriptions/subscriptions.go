// Package subscriptions is the list of commands the game service consumes.
//
// It lives apart from cmd/game's main package for one reason: other packages
// must be able to read it. The scheduler publishes commands that only this
// service handles, and the two sides agreeing on a subject is otherwise a
// matter of two string literals in two processes that nobody compares. A test
// in internal/workers/scheduler imports this package and asserts that every
// subject the scheduler can publish is one listed here, so a rename on either
// side fails the build's tests instead of silently publishing into a stream
// no consumer filters for.
//
// The table holds names only. What each command does is the handler's
// business; cmd/game binds every entry to one and refuses to start if any
// entry is left unbound.
package subscriptions

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

// Subscription is one command the game service consumes.
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

	// Phase 1: travel. travel.arrive is the one command in the game that no
	// player can send; the scheduler publishes it when a journey comes due.
	{Domain: "travel", Action: "start", Origin: FromPlayer},
	{Domain: "travel", Action: "status", Origin: FromPlayer},
	{Domain: "travel", Action: "arrive", Origin: FromScheduler},

	// Phase 1: skills, the world map and the social graph.
	{Domain: "skills", Action: "list", Origin: FromPlayer},
	{Domain: "map", Action: "list", Origin: FromPlayer},
	{Domain: "social", Action: "search", Origin: FromPlayer},
	{Domain: "social", Action: "friend.add", Origin: FromPlayer},
	{Domain: "social", Action: "friend.accept", Origin: FromPlayer},
	{Domain: "social", Action: "friend.list", Origin: FromPlayer},
}

// All returns every subscription. The slice is a copy.
func All() []Subscription {
	out := make([]Subscription, len(all))
	copy(out, all)
	return out
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
