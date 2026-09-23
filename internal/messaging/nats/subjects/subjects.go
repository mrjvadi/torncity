// Package subjects builds NATS subject names.
//
// Subject strings are never concatenated at a call site. Every subject in the
// system is produced here, so the grammar in MASTER_PROMPT section 10 has one
// enforcement point and a typo cannot silently create a new stream.
package subjects

import (
	"fmt"
	"regexp"
)

// Version is the schema version suffix carried by every subject.
const Version = "v1"

// Grammar is the shape every subject produced by this package must match.
//
// There are two kinds of subject. A command or an event is NAMED: its middle
// is a domain and an action written in [a-z_] segments, the same vocabulary
// the gateway's router accepts. A response or a notice is ADDRESSED: its
// middle is exactly one identifier — a request id or a player id — which may
// hold digits and dashes but never a dot, because a dot would split it into
// two tokens and a wildcard subscriber would no longer see it.
var Grammar = regexp.MustCompile(
	`^game\.((command|event)\.[a-z_]+(\.[a-z_]+)*|(response|notify)\.[A-Za-z0-9_-]+)\.v[0-9]+$`)

// Command returns the subject a gateway publishes a player command to,
// for example Command("player", "profile.get") -> game.command.player.profile.get.v1
func Command(domain, action string) string {
	return fmt.Sprintf("game.command.%s.%s.%s", domain, action, Version)
}

// Event returns the subject a domain event is published to,
// for example Event("player", "created") -> game.event.player.created.v1
func Event(domain, event string) string {
	return fmt.Sprintf("game.event.%s.%s.%s", domain, event, Version)
}

// Response returns the subject a command's reply is published to.
func Response(requestID string) string {
	return fmt.Sprintf("game.response.%s.%s", requestID, Version)
}

// Notify returns the subject a notice for one player is sent on, for example
// Notify("<player_id>") -> game.notify.<player_id>.v1.
//
// A notice is a request from cmd/notifier that the gateway answers with a
// receipt (internal/workers/notification). The player id is in the subject so
// a notice can be traced, filtered or tapped per player without decoding a
// payload.
func Notify(playerID string) string {
	return fmt.Sprintf("game.notify.%s.%s", playerID, Version)
}

// CommandStream is the JetStream wildcard covering every command subject.
const CommandStream = "game.command.>"

// EventStream is the JetStream wildcard covering every event subject.
const EventStream = "game.event.>"

// NotifyAll is the wildcard the gateway subscribes to: every player's notices
// at this version. It is a core NATS subscription, not a stream: what must
// survive a crash is the event behind a notice, and that is already durable.
const NotifyAll = "game.notify.*." + Version
