// Package groups is what lets the game be played in Telegram groups without
// showing one player's own business to the whole room.
//
// # The rule
//
// The game is group-oriented: a screen is an ordinary group message unless it
// is private (see privacy.go for what is). A private screen never reaches the
// group's timeline. It goes to the player's private chat with the bot, and the
// group sees one short neutral line — or, for a button press, a popup only the
// presser sees — with an "Open the bot" link. When the player has never
// started the bot (Telegram answers 403), the line asks them to, and the link
// (t.me/<bot>?start=<payload>) opens the bot and replays the command there.
// Buttons on a screen delivered to the private chat carry on there.
//
// Every keyboard posted in a group carries its owner in the callback data,
// and a press by anybody else is refused with a popup before anything is
// published (see BindOwner). When several of our bots share a group, exactly
// one answers a command (see Claimer).
//
// This is enforced here, in the gateway's one render path, not per handler.
//
// # What the owner tag is not
//
// Callback data is untrusted input. The owner tag is a courtesy that stops a
// stranger's press from opening their own copy of somebody else's screen; it
// is not an authorisation. Forging it only lets a user claim their own id,
// and the game core re-validates every command against the presser's own
// player anyway.
package groups

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// IsGroupChat reports whether a chat is a group the gateway must treat as
// shared. It is envelope.Metadata.InGroup for a chat known only by its type
// and id, as an update's chat is before any metadata exists.
func IsGroupChat(chatType string, chatID int64) bool {
	return envelope.Metadata{ChatType: chatType, TelegramChatID: chatID}.InGroup()
}

// CommandAddressee reads the slash-command at the start of text.
//
// isCommand is false when text is not a slash-command. name is the bot the
// command is addressed to ("/map@torn_bot" names "torn_bot"), empty when it
// names none.
func CommandAddressee(text string) (name string, isCommand bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	head := text
	if i := strings.IndexAny(text, " \t\n"); i >= 0 {
		head = text[:i]
	}
	if at := strings.IndexByte(head, '@'); at >= 0 {
		return head[at+1:], true
	}
	return "", true
}

// SameBot reports whether an @-addressee names the bot with this username.
// Telegram usernames are case-insensitive.
func SameBot(addressee, username string) bool {
	return strings.EqualFold(strings.TrimPrefix(addressee, "@"), strings.TrimPrefix(username, "@"))
}

// Membership statuses that mean the bot is in the chat.
var present = map[string]bool{
	"creator":       true,
	"administrator": true,
	"member":        true,
	"restricted":    true,
}

// Joined reports whether a my_chat_member change put the bot into the chat;
// Left reports whether it took the bot out.
func Joined(oldStatus, newStatus string) bool { return !present[oldStatus] && present[newStatus] }

// Left is the opposite of Joined.
func Left(oldStatus, newStatus string) bool { return present[oldStatus] && !present[newStatus] }
