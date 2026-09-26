package notification

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The notice is the contract between this worker and the gateway. The payload
// and the receipt live here, beside the only producer, and the gateway imports
// them; the subject is subjects.Notify, beside every other subject in the
// system. Neither side spells the subject or the fields on its own.
//
// # Why request and reply over core NATS
//
// The gateway already renders command replies it receives on core NATS
// (game.response.*). A notice travels the same way, with one difference: it
// is a REQUEST, and the gateway answers with a Receipt once Telegram has
// accepted or refused the message. That answer is what lets this worker keep
// the durable part of the job — the event stays unacknowledged in the event
// stream until a notice has actually been delivered, and a blocked bot is
// recorded here, next to the database, rather than in the edge.
//
// A notice is not persisted on its own stream. What must survive a crash is
// the event, and it already does.

// Notice is the payload of a notice envelope. The envelope's metadata names
// the bot (BotID, a telegram_bots.id) and the chat (TelegramChatID) to send
// through; this is what to send, and until when.
type Notice struct {
	// DeliverBy is the moment after which the gateway must not START a send.
	// The worker stops waiting for a receipt shortly after it, and treats
	// the silence as a failure to redeliver later; a gateway that sent after
	// that point would make the redelivery a duplicate. It is a wall-clock
	// time, so the two processes' clocks must roughly agree, which they do
	// on any host running NTP.
	DeliverBy time.Time `json:"deliver_by"`

	// Response is the screen to send. By default it is always a new
	// message, whatever its own Type says: a notice sent without Edit set
	// cannot turn into a silent edit of whatever the player is looking at.
	Response presenter.Response `json:"response"`

	// Edit allows Response to edit the message it names (Response.MessageID)
	// instead of always sending a new one, when Response.Type is itself
	// ActionEditMessage. It exists for exactly one caller today: the inbox
	// badge (internal/workers/notification/badge.go), which must update the
	// one message its count is shown on rather than post a new one every
	// time. Every other notice leaves it false, and the gateway enforces
	// that default (cmd/gateway/notify.go) so a malformed notice can never
	// edit a message the player is reading.
	Edit bool `json:"edit,omitempty"`

	// Announcement marks a public line for a group chat (announce.go). A
	// notice addressed to a group is otherwise delivered to the player's
	// private chat instead, never to the room; an announcement is meant for
	// the room and is posted there as it stands.
	Announcement bool `json:"announcement,omitempty"`
}

// Outcome is what became of one notice.
type Outcome string

const (
	// OutcomeDelivered means Telegram accepted the message.
	OutcomeDelivered Outcome = "delivered"

	// OutcomeUnreachable means Telegram refused the chat for good: the
	// player blocked the bot, deleted their account, or the chat no longer
	// exists. Retrying through the same bot will never succeed.
	OutcomeUnreachable Outcome = "unreachable"

	// OutcomeUnknownBot means the gateway does not serve the bot the notice
	// named — disabled or removed from the fleet. Another bot may still
	// reach the player.
	OutcomeUnknownBot Outcome = "unknown_bot"

	// OutcomeExpired means DeliverBy passed before the message could be
	// sent, usually behind a flood wait. Nothing was sent.
	OutcomeExpired Outcome = "expired"

	// OutcomeFailed is every other failure: the Bot API is down, a request
	// timed out, the notice was malformed. It may succeed later.
	OutcomeFailed Outcome = "failed"
)

// Receipt is the gateway's answer to a notice.
type Receipt struct {
	Outcome Outcome `json:"outcome"`
	// Detail is for the log line, never for a player. It carries no token.
	Detail string `json:"detail,omitempty"`
	// MessageID is the id Telegram assigned a message this notice SENT
	// (never an edit, which already names the id it targeted). It is how
	// the inbox badge learns what to edit next time; every other notice
	// ignores it. Zero unless Outcome is OutcomeDelivered and Response.Type
	// was ActionSendMessage.
	MessageID int64 `json:"message_id,omitempty"`
}
