// Package routing turns a Telegram update into a command name and a payload.
//
// That sentence is the whole contract. MASTER_PROMPT section 73 forbids the
// gateway from running business logic, and section 13 says a callback must be
// translated, not executed: market:buy:123 becomes the command market.buy and
// the game core is the one that checks ownership, availability, the wallet and
// the cooldown. Nothing in this package knows what a market, a job or an item
// is; it knows how a command is spelled.
//
// # The parsing scheme
//
// Text commands:
//
//	/<domain> <action> [arg ...]
//
// The first token is the slash-command and names the domain; the token after
// it names the action; everything that follows is a positional argument. So
// "/job apply developer" is domain "job", action "apply", one argument
// "developer", which makes the command "job.apply". A trailing @botname on the
// slash-command is stripped, because Telegram appends it in groups
// ("/job@torncity_bot apply"). Tokens are lower-cased.
//
// Callback data:
//
//	<domain>:<action>[:arg ...]
//
// Same shape with colons, because callback data is limited to 64 bytes and
// every character counts. So "market:buy:123" is the command "market.buy" with
// one argument, "123".
//
// A small number of bare commands have no action token and are mapped by name;
// the only one phase 0 needs is /start, which opens the player's profile
// screen. See aliases.
//
// # How positional arguments become a payload
//
// Payload keys come from argNames, a table that gives a command's positional
// arguments their names: job.apply names its first argument "role", so
// "/job apply developer" yields {"role":"developer"}. The table holds names
// only. It encodes no rule, no validation and no default; a wrong name costs a
// rejected command from the domain handler, never a wrong game outcome.
//
// A command that is absent from the table still routes. Its arguments arrive
// under the key "args" as a []string, and so do any arguments beyond the names
// the table lists. The gateway deliberately does not keep a whitelist of valid
// commands: the list of commands is the game core's business, it changes every
// time a domain grows a feature, and a copy of it here would be wrong within a
// week. An unknown command produces a subject nobody consumes, which surfaces
// as a no-responder and is answered as "unknown command" — one place to handle
// it instead of two.
//
// # Callback data is untrusted input
//
// Callback data is a string the client sends back to us. Anyone can craft one,
// it is not signed, and Telegram neither hides it nor validates it. Therefore:
//
//   - NEVER put anything sensitive in callback data. No token, no cost, no
//     wallet total, no server-side decision, no "already authorised" flag.
//     Treat every byte of it as if a player typed it, because a player can.
//   - What it carries is an addressing hint: which screen, which row. The
//     domain re-checks that the player may act on that row (section 13).
//
// Parse enforces Telegram's 64-byte limit and a conservative character set
// (see safeCallbackByte) so that malformed or hostile data is rejected at the
// edge rather than becoming a strange NATS subject.
package routing

import (
	"errors"
	"regexp"
	"strings"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// MaxCallbackDataBytes is Telegram's documented limit on callback_data:
// 1-64 bytes. It is measured in bytes, not runes, which matters the moment a
// Persian or emoji character ends up in a button.
const MaxCallbackDataBytes = 64

// Parse failures. They are plain sentinels so errors.Is can tell them apart;
// internal/shared/errors matches on Code, which would make every one of these
// equal to every other INVALID_INPUT error. A caller answering a player maps
// them to errors.InvalidInput with wording from the i18n layer.
var (
	// ErrNoCommand means the update carries nothing routable: a photo with no
	// caption, an edited message, an update type phase 0 ignores.
	ErrNoCommand = errors.New("routing: update carries no command")

	// ErrNotACommand means the message was plain text, not a slash command.
	// Phase 0 has no free-text input, so this is a normal, frequent outcome.
	ErrNotACommand = errors.New("routing: message text is not a slash command")

	// ErrMissingAction means a domain arrived with no action after it
	// ("/job"), and it is not one of the bare aliases.
	ErrMissingAction = errors.New("routing: command has no action")

	// ErrMalformedCommand means the domain or action contains something that
	// is not a legal subject token.
	ErrMalformedCommand = errors.New("routing: command is not a valid domain.action")

	// ErrEmptyCallbackData means a button sent an empty data field.
	ErrEmptyCallbackData = errors.New("routing: callback data is empty")

	// ErrCallbackDataTooLong means the data exceeded MaxCallbackDataBytes.
	// Telegram would normally have refused to send such a button, so this is
	// either a client that is not Telegram or a bug in our own keyboards.
	ErrCallbackDataTooLong = errors.New("routing: callback data exceeds Telegram's 64-byte limit")

	// ErrCallbackDataUnsafe means the data contained a byte outside the safe
	// set. See safeCallbackByte for what is allowed and why.
	ErrCallbackDataUnsafe = errors.New("routing: callback data contains characters outside the safe set")
)

// tokenPattern is what a single subject token may look like.
//
// It is intentionally narrower than "anything without a dot": subjects.Grammar
// accepts [a-z_]+ per segment, so a command with a digit or a dash in it would
// build a subject that the grammar rejects, and the failure would appear far
// from its cause. Rejecting it here makes the error land on the input that
// caused it.
var tokenPattern = regexp.MustCompile(`^[a-z_]+$`)

// aliases maps a bare slash-command to a full domain.action.
//
// Keep this table tiny. Every entry is a name the gateway has to know, and the
// general /domain action form needs no entry at all. /start earns its place
// because Telegram itself sends it as the first message of every chat.
var aliases = map[string]string{
	"start": "player.profile.get",
}

// argNames names the positional arguments of a command, in order.
//
// Adding a command here is a one-line change and requires no code. Leaving one
// out is also fine: its arguments arrive under "args". Names must match what
// the domain handler decodes, which is why the payload contract belongs to
// internal/application/dto and this table only mirrors it.
var argNames = map[string][]string{
	"job.apply":  {"role"},
	"market.buy": {"id"},
}

// Parse extracts the command and payload from one update.
//
// The payload is always non-nil on success, so a caller can marshal it without
// a nil check and a command with no arguments serialises as {} rather than
// null.
func Parse(update client.Update) (command string, payload map[string]any, err error) {
	switch {
	case update.CallbackQuery != nil:
		return parseCallbackData(update.CallbackQuery.Data)
	case update.Message != nil:
		return parseText(update.Message.Text)
	default:
		// An edited message is deliberately not routed. Re-running a command
		// because someone edited the text they sent five minutes ago is a
		// replay, and the gateway should not invent one.
		return "", nil, ErrNoCommand
	}
}

// ParseText exposes the text branch on its own, for callers that already have
// the message text in hand (the command middleware, and tests).
func ParseText(text string) (command string, payload map[string]any, err error) {
	return parseText(text)
}

// ParseCallbackData exposes the callback branch on its own. Use it wherever
// callback data is handled outside an update, and never skip it: the length
// and character checks live here.
func ParseCallbackData(data string) (command string, payload map[string]any, err error) {
	return parseCallbackData(data)
}

func parseText(text string) (string, map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil, ErrNoCommand
	}
	if !strings.HasPrefix(text, "/") {
		return "", nil, ErrNotACommand
	}

	fields := strings.Fields(text)
	head := strings.ToLower(strings.TrimPrefix(fields[0], "/"))

	// "/job@torncity_bot apply" — in a group Telegram addresses the bot by
	// name, and the suffix is not part of the command.
	if at := strings.IndexByte(head, '@'); at >= 0 {
		head = head[:at]
	}
	if head == "" {
		return "", nil, ErrMalformedCommand
	}

	if full, ok := aliases[head]; ok {
		// Anything after an alias is an argument, which is how Telegram deep
		// links arrive: "/start <payload>".
		return finish(full, fields[1:])
	}

	if len(fields) < 2 {
		return "", nil, ErrMissingAction
	}
	action := strings.ToLower(fields[1])
	return finish(head+"."+action, fields[2:])
}

func parseCallbackData(data string) (string, map[string]any, error) {
	if data == "" {
		return "", nil, ErrEmptyCallbackData
	}
	if len(data) > MaxCallbackDataBytes {
		return "", nil, ErrCallbackDataTooLong
	}
	for i := 0; i < len(data); i++ {
		if !safeCallbackByte(data[i]) {
			return "", nil, ErrCallbackDataUnsafe
		}
	}

	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return "", nil, ErrMissingAction
	}
	for _, part := range parts {
		if part == "" {
			// "market::123" or a trailing colon: the shape is wrong, and
			// guessing which segment was meant would route the press to the
			// wrong handler.
			return "", nil, ErrMalformedCommand
		}
	}

	domain := strings.ToLower(parts[0])
	action := strings.ToLower(parts[1])
	return finish(domain+"."+action, parts[2:])
}

// safeCallbackByte reports whether a byte may appear in callback data.
//
// The set is letters, digits, underscore, hyphen, dot and the colon separator.
// It is chosen to be boring: these bytes cannot terminate a NATS subject
// token, cannot introduce whitespace or control characters into a log line,
// and are safe to echo back into an error message. Everything else, including
// every multi-byte character, is rejected. Our own keyboards only ever emit
// this set, so a rejection means the data did not come from a button we built.
func safeCallbackByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_', b == '-', b == '.', b == ':':
		return true
	}
	return false
}

// finish validates the assembled command and builds its payload.
func finish(command string, args []string) (string, map[string]any, error) {
	if _, _, err := SplitCommand(command); err != nil {
		return "", nil, err
	}
	return command, buildPayload(command, args), nil
}

// buildPayload maps positional arguments onto the names in argNames, putting
// whatever is left over under "args".
func buildPayload(command string, args []string) map[string]any {
	payload := make(map[string]any, len(args))

	names := argNames[command]
	used := 0
	for used < len(names) && used < len(args) {
		payload[names[used]] = args[used]
		used++
	}
	if used < len(args) {
		// Copied so the payload never aliases the caller's slice.
		rest := make([]string, len(args)-used)
		copy(rest, args[used:])
		payload["args"] = rest
	}
	return payload
}

// SplitCommand splits a command into the two parts subjects.Command needs.
//
//	job.apply          -> ("job", "apply")
//	player.profile.get -> ("player", "profile.get")
//
// The domain is the first token; the action is everything after it, dots and
// all, because a multi-level action is a legal subject ("profile.get") while a
// multi-level domain is not: the domain is what a JetStream consumer filters
// on and what a team owns.
//
// Every token is validated against tokenPattern, so the result is always safe
// to hand to subjects.Command.
func SplitCommand(command string) (domain, action string, err error) {
	if command == "" {
		return "", "", ErrNoCommand
	}

	tokens := strings.Split(command, ".")
	if len(tokens) < 2 {
		return "", "", ErrMissingAction
	}
	for _, token := range tokens {
		if !tokenPattern.MatchString(token) {
			return "", "", ErrMalformedCommand
		}
	}
	return tokens[0], strings.Join(tokens[1:], "."), nil
}
