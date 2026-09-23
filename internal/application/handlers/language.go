package handlers

import (
	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// RenderLanguage is the language a response to this player is written in.
//
// The player's stored language wins. The gateway fills meta.Language from the
// Telegram client on EVERY update, so a language chosen on the settings screen
// would otherwise last exactly until the player's next message. The client's
// language is used only when there is no stored one: no player record yet, or
// a record that somehow carries none.
//
// Every handler, and the error path in cmd/game, asks this one function, so
// the rule cannot drift between screens: a player never reads one screen in
// the language they chose and the next in the one their phone is set to.
func RenderLanguage(meta envelope.Metadata, p *application.Player) string {
	if p != nil && p.Language != "" {
		return p.Language
	}
	return meta.Language
}
