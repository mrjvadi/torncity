package groups

import (
	"context"
	"strings"
	"time"
)

// Deep links.
//
// When a private screen cannot be delivered in the group and the player has
// never started the bot, the only way to reach them is to have them open the
// bot. A t.me/<bot>?start=<payload> link does that, and Telegram then sends
// "/start <payload>" in the private chat. The payload names the command the
// player asked for — and, when the screen was about someone, whom — so
// opening the bot shows them the screen they wanted rather than the profile
// or an empty form.
//
// Telegram allows only A-Z, a-z, 0-9, "_" and "-" in a start parameter, up to
// 64 characters (core.telegram.org/bots/features#deep-linking). Three shapes
// are used:
//
//	run-<command>                    "run-skills-list": the command, its dots
//	                                 written as dashes
//	run-<command>--<arg>-<arg>...    "run-bank-pay--K7Q2M9A-5000": with its
//	                                 positional arguments
//	tok-<token>                      a link too long for the parameter, kept
//	                                 for a short while in a LinkStore
//
// A command is [a-z_.]+, so "--" cannot occur inside one and the mapping is
// exact and reversible. An argument must be [A-Za-z0-9_]+: a public code, an
// amount, a method. The payload carries nothing secret: whoever opens the
// link runs that command for their own player, exactly as if they had typed
// it, and the gateway still checks it against the commands the game serves.

// startPayloadPrefix marks a start payload as a command to replay, leaving
// every other payload shape free for other uses.
const startPayloadPrefix = "run-"

// tokenPayloadPrefix marks a start payload as a token to look up.
const tokenPayloadPrefix = "tok-"

// argsSeparator separates a payload's command from its arguments.
const argsSeparator = "--"

// maxStartPayload is Telegram's limit on a start parameter.
const maxStartPayload = 64

// telegramLinkBase is the host of Telegram's public deep links. It is part of
// the protocol, like the Bot API's own address, not a setting.
const telegramLinkBase = "https://t.me/"

// LinkStore keeps deep links that do not fit a start parameter, under a short
// token, for a while. internal/infrastructure/redis implements it.
type LinkStore interface {
	// Put keeps value for ttl and returns the token that finds it: at most
	// 60 characters of A-Z, a-z, 0-9, "_" and "-".
	Put(ctx context.Context, value string, ttl time.Duration) (string, error)
	// Get returns what a token keeps, "" when it has expired or never
	// existed.
	Get(ctx context.Context, token string) (string, error)
}

// StartPayload is the deep-link payload that replays command with args. It
// is empty when the command, or the command with those arguments, cannot be
// expressed as one: an argument outside [A-Za-z0-9_], or more than 64
// characters in all.
func StartPayload(command string, args ...string) string {
	if command == "" || strings.Contains(command, "-") {
		return ""
	}
	payload := startPayloadPrefix + strings.ReplaceAll(command, ".", "-")
	for i := 0; i < len(payload); i++ {
		if !payloadByte(payload[i]) {
			return ""
		}
	}
	if len(args) > 0 {
		for _, a := range args {
			if !argToken(a) {
				return ""
			}
		}
		payload += argsSeparator + strings.Join(args, "-")
	}
	if len(payload) > maxStartPayload {
		return ""
	}
	return payload
}

// LinkPayload is the payload that replays command with args: the stateless
// one when it fits, else a token kept in store for ttl, else — no store, or
// the store failing — the command alone, which at least opens its screen.
func LinkPayload(ctx context.Context, store LinkStore, ttl time.Duration, command string, args ...string) string {
	if len(args) == 0 {
		return StartPayload(command)
	}
	if p := StartPayload(command, args...); p != "" {
		return p
	}
	if store != nil && ttl > 0 && StartPayload(command) != "" && argsSpeakable(args) {
		token, err := store.Put(ctx, strings.Join(append([]string{command}, args...), " "), ttl)
		if err == nil && token != "" && len(tokenPayloadPrefix+token) <= maxStartPayload && allPayloadBytes(token) {
			return tokenPayloadPrefix + token
		}
	}
	return StartPayload(command)
}

// CommandFromStart reads a "/start <payload>" message that StartPayload
// produced and returns the command it replays and its arguments. ok is false
// for any other text, including a plain /start, a token (TokenFromStart) and
// a payload of another shape.
func CommandFromStart(text string) (command string, args []string, ok bool) {
	payload, found := startParameter(text)
	if !found || !strings.HasPrefix(payload, startPayloadPrefix) {
		return "", nil, false
	}
	body := strings.TrimPrefix(payload, startPayloadPrefix)
	cmd, rest, hasArgs := strings.Cut(body, argsSeparator)
	if cmd == "" {
		return "", nil, false
	}
	for i := 0; i < len(cmd); i++ {
		b := cmd[i]
		if (b < 'a' || b > 'z') && b != '_' && b != '-' {
			return "", nil, false
		}
	}
	if hasArgs {
		args = strings.Split(rest, "-")
		for _, a := range args {
			if !argToken(a) {
				return "", nil, false
			}
		}
	}
	return strings.ReplaceAll(cmd, "-", "."), args, true
}

// TokenFromStart reads a "/start tok-<token>" message and returns the token.
func TokenFromStart(text string) (string, bool) {
	payload, found := startParameter(text)
	if !found || !strings.HasPrefix(payload, tokenPayloadPrefix) {
		return "", false
	}
	token := strings.TrimPrefix(payload, tokenPayloadPrefix)
	if token == "" || !allPayloadBytes(token) {
		return "", false
	}
	return token, true
}

// ParseStored reads what LinkPayload kept under a token: the command and its
// arguments. ok is false for anything it did not write.
func ParseStored(value string) (command string, args []string, ok bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 || StartPayload(fields[0]) == "" {
		return "", nil, false
	}
	if !argsSpeakable(fields[1:]) {
		return "", nil, false
	}
	return fields[0], fields[1:], true
}

// startParameter is the parameter of a "/start <parameter>" message.
func startParameter(text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return "", false
	}
	head := strings.ToLower(fields[0])
	if at := strings.IndexByte(head, '@'); at >= 0 {
		head = head[:at]
	}
	if head != "/start" {
		return "", false
	}
	return fields[1], true
}

// DeepLink is the link that opens the bot's private chat, replaying payload
// when there is one. It is empty when the bot's username is unknown.
func DeepLink(username, payload string) string {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return ""
	}
	if payload == "" {
		return telegramLinkBase + username
	}
	return telegramLinkBase + username + "?start=" + payload
}

func payloadByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

func allPayloadBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if !payloadByte(s[i]) {
			return false
		}
	}
	return true
}

// argToken reports whether a is one argument a stateless payload can carry.
func argToken(a string) bool {
	if a == "" {
		return false
	}
	for i := 0; i < len(a); i++ {
		b := a[i]
		if b == '-' || !payloadByte(b) {
			return false
		}
	}
	return true
}

// argsSpeakable reports whether args can be replayed as typed words: no
// empty word, no whitespace, nothing a command line would misread.
func argsSpeakable(args []string) bool {
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\r\n") || len(a) > maxStartPayload {
			return false
		}
		if !allPayloadBytes(strings.ReplaceAll(a, ".", "_")) {
			return false
		}
	}
	return true
}
