// Package groups is what lets the game be played in a Telegram group without
// showing one player's business to the whole room.
//
// # The rule
//
// Every screen is private unless it says otherwise (presenter.Response.Public
// is false by default). In a group, a private screen never reaches the
// group's timeline. It is delivered, in order of preference:
//
//  1. as an ephemeral message (Bot API 10.2, reshaped in 10.3): a message on
//     the group's timeline that only the player and the bot can see. The Bot
//     API allows one within 15 seconds of an eligible action — a button press
//     (callback_query_id) or an ephemeral command (reply_parameters
//     .ephemeral_message_id) — or at any time when the bot is an
//     administrator of the group. Navigation inside such a screen edits it
//     with editEphemeralMessageText.
//  2. in the player's private chat with the bot, with a neutral line in the
//     group — or, for a button press, a popup only the presser sees — saying
//     where the screen went.
//  3. when the player has never started the bot (Telegram answers 403), the
//     neutral line or popup carries a t.me/<bot>?start=<payload> link that
//     opens the bot and replays the command there.
//
// A public screen (help, a short neutral confirmation) is posted in the group
// as usual. Every keyboard posted in a group carries its owner in the callback
// data, and a press by anybody else is refused with a popup before anything is
// published (see BindOwner).
//
// This is enforced here, in the gateway's one render path, not per handler: a
// handler that forgets about groups produces a private screen, which is the
// safe outcome.
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
)

// Telegram chat types that are groups. A channel is not a place to play.
const (
	chatTypeGroup      = "group"
	chatTypeSupergroup = "supergroup"
)

// IsGroupChat reports whether a chat is a group the gateway must treat as
// shared: its type says so, or its id is negative. Telegram gives a private
// chat the user's own, positive id; every group, supergroup and channel id is
// negative. The id is checked as well so a response whose chat type was lost
// on the way is still treated as the room it is.
func IsGroupChat(chatType string, chatID int64) bool {
	return chatType == chatTypeGroup || chatType == chatTypeSupergroup || chatID < 0
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
