// Package context builds the request context that MASTER_PROMPT section 6
// requires on every message entering the game core.
//
// The rule it enforces is blunt: no request reaches a domain handler without a
// context, because section 97 demands that any request can be answered for
// later — who asked, what they asked, through which bot, on which gateway, and
// when. That is only possible if the answer is attached at the single place
// where the facts still exist, which is here, the moment a Telegram update is
// received. Anything reconstructed later is a guess.
//
// This package produces envelope.Metadata and nothing else. It performs no
// game logic, reads no database and resolves no player: it copies what
// Telegram sent and mints the two identifiers that make a request traceable.
//
// # What Build does not fill in
//
// Three fields are deliberately left empty because this package cannot know
// them, and filling them with a plausible value would be a lie:
//
//   - Command and Action belong to internal/gateway/routing, which decides
//     what the update asked for.
//   - PlayerID belongs to internal/gateway/identity, which resolves the global
//     player behind the Telegram user.
//
// Metadata.Validate therefore fails on the value Build returns until routing
// has set Command. That is intended: envelope.New is the gate, and a message
// published without a command would be unroutable.
//
// The package is named context and will collide with the standard library at
// an import site. Import it with an alias, conventionally gwcontext. The
// directory name comes from the project structure in MASTER_PROMPT section 71.
package context

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// The language a player gets when Telegram tells us nothing usable about
// theirs is NOT declared here. It is player.default_language in
// configs/config.yml, loaded by internal/config and passed in by the caller,
// because it is a product setting an operator changes without a deploy and
// because the same value was previously written out independently in three
// packages, where nothing kept the three copies in step.

// Identifier format.
//
// A request id looks like req_9f1c0a73b5e24d8a9c6f1b2d3e4f5a6b and a trace id
// like trc_<same shape>: a fixed three-letter prefix, an underscore, then
// idRandomBytes bytes of crypto/rand output in lower-case hex. The prefix is
// there so an identifier found in a log line, a NATS subject or a database row
// is recognisable without knowing where it came from; the hex body is 128 bits
// of randomness, which makes a collision across the fleet a non-issue without
// requiring any coordination between gateway instances.
//
// crypto/rand rather than math/rand is not superstition: these identifiers end
// up in NATS response subjects (subjects.Response), so a predictable one would
// let anyone with publish rights guess where another player's answer is going
// to arrive.
const (
	RequestIDPrefix = "req_"
	TraceIDPrefix   = "trc_"
	idRandomBytes   = 16
)

// Update types this package recognises. They are the values that appear in
// Metadata.UpdateType and they are part of the wire contract, so they must not
// be renamed once shipped.
const (
	UpdateTypeMessage       = "message"
	UpdateTypeEditedMessage = "edited_message"
	UpdateTypeCallbackQuery = "callback_query"
)

// Failures Build can report. They are plain sentinel errors compared with
// errors.Is rather than values of internal/shared/errors, because that type's
// Is matches on Code alone: two INVALID_INPUT sentinels from this package
// would be indistinguishable, and the caller has to tell "not a supported
// update" apart from "no chat to answer in" to decide what to do next.
var (
	// ErrUnsupportedUpdate means the update is of a type phase 0 does not
	// handle (a channel post, an inline query, a poll answer). It is not a
	// fault; the caller skips the update.
	ErrUnsupportedUpdate = errors.New("gateway/context: update carries neither a message nor a callback query")

	// ErrNoSender means the update has no from-user, as happens with posts
	// made by a channel itself. There is no player behind it.
	ErrNoSender = errors.New("gateway/context: update has no sender")

	// ErrNoChat means a callback query arrived without its original message,
	// which Telegram does when that message is too old. Nothing can be edited
	// or replied to, so no command is published; the caller should answer the
	// callback query with a "this menu expired" toast instead.
	ErrNoChat = errors.New("gateway/context: callback query has no originating chat")

	// ErrNoBotID and ErrNoGatewayInstanceID guard the two facts that make a
	// request attributable to one bot and one process. Section 7 is
	// unsatisfiable without them.
	ErrNoBotID             = errors.New("gateway/context: bot id is required")
	ErrNoGatewayInstanceID = errors.New("gateway/context: gateway instance id is required")

	// ErrNoDefaultLanguage means the caller did not say what an unknown
	// locale falls back to. It is required rather than defaulted: a fallback
	// chosen here would be a fourth copy of a value that has one home in
	// configs/config.yml, and the copies would drift.
	ErrNoDefaultLanguage = errors.New("gateway/context: default language is required")
)

// Build turns one Telegram update into the request context that travels with
// it for the rest of its life.
//
// now is passed in rather than read from the clock so that a caller can stamp
// a whole batch of updates with one receive time and so that tests are not
// forced to compare against time.Now. It is normalised to UTC: mixing zones in
// a field that is compared across services is a bug waiting to happen.
//
// botID identifies the bot that received the update and gatewayInstanceID the
// process that polled it. Neither is a secret, and neither is ever combined
// with the player's identity: per ADR 0001 constraint 1 a player is global,
// and bot_id is transport metadata only.
//
// defaultLanguage is what Metadata.Language becomes when Telegram sent no
// usable language_code. It is a parameter because it is configuration
// (player.default_language), not a property of this package.
func Build(update client.Update, botID, gatewayInstanceID, defaultLanguage string, now time.Time) (envelope.Metadata, error) {
	botID = strings.TrimSpace(botID)
	if botID == "" {
		return envelope.Metadata{}, ErrNoBotID
	}
	gatewayInstanceID = strings.TrimSpace(gatewayInstanceID)
	if gatewayInstanceID == "" {
		return envelope.Metadata{}, ErrNoGatewayInstanceID
	}
	defaultLanguage = strings.TrimSpace(defaultLanguage)
	if defaultLanguage == "" {
		return envelope.Metadata{}, ErrNoDefaultLanguage
	}

	meta := envelope.Metadata{
		BotID:             botID,
		GatewayInstanceID: gatewayInstanceID,
		ReceivedAt:        now.UTC(),
		SchemaVersion:     envelope.SchemaVersion,
	}

	// A callback query is checked first: an update of that kind carries only
	// the CallbackQuery field, and the message hanging off it is the screen
	// that is about to be edited, not a new message from the player.
	switch {
	case update.CallbackQuery != nil:
		if err := fillFromCallback(&meta, update.CallbackQuery, defaultLanguage); err != nil {
			return envelope.Metadata{}, err
		}
	case update.Message != nil:
		if err := fillFromMessage(&meta, update.Message, UpdateTypeMessage, defaultLanguage); err != nil {
			return envelope.Metadata{}, err
		}
	case update.EditedMessage != nil:
		if err := fillFromMessage(&meta, update.EditedMessage, UpdateTypeEditedMessage, defaultLanguage); err != nil {
			return envelope.Metadata{}, err
		}
	default:
		return envelope.Metadata{}, ErrUnsupportedUpdate
	}

	requestID, err := NewRequestID()
	if err != nil {
		return envelope.Metadata{}, err
	}
	traceID, err := NewTraceID()
	if err != nil {
		return envelope.Metadata{}, err
	}
	meta.RequestID = requestID

	// The trace id is minted here as well because the gateway is the start of
	// the trace. When an inbound carrier (a header from an upstream hop) is
	// ever introduced, this is the one line that changes: a trace id received
	// from outside is adopted, a request id never is.
	meta.TraceID = traceID

	return meta, nil
}

// fillFromMessage copies the chat context of a plain or edited message.
func fillFromMessage(meta *envelope.Metadata, msg *client.Message, updateType, defaultLanguage string) error {
	if msg.From == nil {
		return ErrNoSender
	}
	meta.UpdateType = updateType
	meta.TelegramUserID = msg.From.ID
	meta.TelegramChatID = msg.Chat.ID
	meta.TelegramMessageID = msg.MessageID
	meta.ChatType = msg.Chat.Type
	meta.Language = NormalizeLanguage(msg.From.LanguageCode, defaultLanguage)

	// TelegramThreadID and ReplyToMessageID stay nil on purpose. The Bot API
	// carries both, but client.Message does not model them yet (see the note
	// at the top of the client's types.go: a field is added when a feature
	// needs it). When the client gains message_thread_id and
	// reply_to_message, copy them here; the envelope already has the fields.
	return nil
}

// fillFromCallback copies the chat context of an inline-keyboard press.
func fillFromCallback(meta *envelope.Metadata, cq *client.CallbackQuery, defaultLanguage string) error {
	if cq.Message == nil {
		return ErrNoChat
	}
	meta.UpdateType = UpdateTypeCallbackQuery
	meta.TelegramUserID = cq.From.ID
	meta.TelegramChatID = cq.Message.Chat.ID

	// The message id is the screen that carries the keyboard. Section 53's UI
	// edits one message in place instead of appending, so this is the handle
	// the presenter needs on the way back.
	meta.TelegramMessageID = cq.Message.MessageID
	meta.ChatType = cq.Message.Chat.Type
	meta.Language = NormalizeLanguage(cq.From.LanguageCode, defaultLanguage)

	// Copied into a local so the pointer cannot alias the caller's update.
	id := cq.ID
	meta.CallbackQueryID = &id
	return nil
}

// NewRequestID mints the identifier that follows one request end to end. See
// the identifier format block above.
func NewRequestID() (string, error) { return newID(RequestIDPrefix) }

// NewTraceID mints the identifier that ties a request to everything it caused:
// the command, its events, and the response rendered back to the player.
func NewTraceID() (string, error) { return newID(TraceIDPrefix) }

func newID(prefix string) (string, error) {
	buf := make([]byte, idRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		// Worth surfacing rather than falling back to a weaker source: a
		// gateway that cannot read randomness must stop, not start issuing
		// guessable response subjects.
		return "", fmt.Errorf("gateway/context: reading randomness for %sid: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(buf), nil
}

// NormalizeLanguage reduces a Telegram language_code to the tag the game's
// i18n layer uses.
//
// Telegram sends IETF tags such as "fa", "fa-IR", "en-GB" or nothing at all.
// Only the primary subtag is kept, because the game translates per language,
// not per region, and a table keyed by "en-GB" would silently miss "en-US".
// Anything unusable (empty, or not two or three letters) becomes
// defaultLanguage, so no code path downstream has to handle an empty language.
//
// defaultLanguage is player.default_language from the configuration. It is a
// parameter rather than a constant here so the one configured value is the
// one every caller falls back to.
func NormalizeLanguage(code, defaultLanguage string) string {
	code = strings.TrimSpace(strings.ToLower(code))
	if code == "" {
		return defaultLanguage
	}
	if i := strings.IndexAny(code, "-_"); i >= 0 {
		code = code[:i]
	}
	if len(code) < 2 || len(code) > 3 {
		return defaultLanguage
	}
	for i := 0; i < len(code); i++ {
		if code[i] < 'a' || code[i] > 'z' {
			return defaultLanguage
		}
	}
	return code
}
