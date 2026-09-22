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
var Grammar = regexp.MustCompile(`^game\.(command|event|response)\.[a-z_]+(\.[a-z_]+)*\.v[0-9]+$`)

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

// CommandStream is the JetStream wildcard covering every command subject.
const CommandStream = "game.command.>"

// EventStream is the JetStream wildcard covering every event subject.
const EventStream = "game.event.>"
