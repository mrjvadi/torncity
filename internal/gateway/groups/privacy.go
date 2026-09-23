package groups

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// What is private.
//
// The game is played in groups, and nearly every screen belongs there: the
// map, travel, jobs, education, city hall, offices, history, friends, skills,
// help and the profile (which a group sees without its money lines: see
// screens.Context.Shared). A few screens are the player's own business and
// go to their private chat instead:
//
//   - the bank: balances, deposits and withdrawals;
//   - payments: the chooser, the confirmation and the receipt;
//   - settings, including the language.
//
// Those are listed here by command, so every screen a command produces —
// its errors and refusals included — is private without each screen having
// to say so. A screen elsewhere that shows the player's exact balance marks
// itself instead (presenter.Response.Private).
var privateCommands = map[string]bool{
	"bank":                true, // the whole domain: show, deposit, withdraw, pay, pay.send
	"player.settings":     true,
	"player.language.set": true,
}

// IsPrivate reports whether a response to command must stay out of a group:
// the screen says so, or the command, or the domain it belongs to, is listed
// in privateCommands.
func IsPrivate(command string, resp *presenter.Response) bool {
	if resp != nil && resp.Private {
		return true
	}
	if privateCommands[command] {
		return true
	}
	domain, _, _ := strings.Cut(command, ".")
	return privateCommands[domain]
}
