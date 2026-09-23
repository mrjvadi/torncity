package groups

import (
	"strings"
)

// Deep links.
//
// When a private screen cannot be delivered in the group and the player has
// never started the bot, the only way to reach them is to have them open the
// bot. A t.me/<bot>?start=<payload> link does that, and Telegram then sends
// "/start <payload>" in the private chat. The payload names the command the
// player asked for, so opening the bot shows them the screen they wanted
// rather than the profile.
//
// The payload is stateless: "run-" followed by the command with its dots
// written as dashes ("skills.list" is "run-skills-list"). Telegram allows
// only A-Z, a-z, 0-9, "_" and "-" in a start payload, up to 64 characters;
// a command is [a-z_.]+, so the mapping is exact and reversible and needs no
// store and no expiry. It carries no arguments and nothing secret: whoever
// opens the link runs that command for their own player, exactly as if they
// had typed it, and the gateway still checks it against the commands the game
// serves.

// startPayloadPrefix marks a start payload as a command to replay, leaving
// every other payload shape free for other uses.
const startPayloadPrefix = "run-"

// maxStartPayload is Telegram's limit on a start parameter.
const maxStartPayload = 64

// telegramLinkBase is the host of Telegram's public deep links. It is part of
// the protocol, like the Bot API's own address, not a setting.
const telegramLinkBase = "https://t.me/"

// StartPayload is the deep-link payload that replays command. It is empty
// when command cannot be expressed as one.
func StartPayload(command string) string {
	if command == "" || strings.Contains(command, "-") {
		return ""
	}
	payload := startPayloadPrefix + strings.ReplaceAll(command, ".", "-")
	if len(payload) > maxStartPayload {
		return ""
	}
	for i := 0; i < len(payload); i++ {
		if !payloadByte(payload[i]) {
			return ""
		}
	}
	return payload
}

// CommandFromStart reads a "/start <payload>" message that StartPayload
// produced and returns the command it replays. ok is false for any other text,
// including a plain /start and a payload of another shape.
func CommandFromStart(text string) (command string, ok bool) {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return "", false
	}
	head := strings.ToLower(fields[0])
	if at := strings.IndexByte(head, '@'); at >= 0 {
		head = head[:at]
	}
	if head != "/start" || !strings.HasPrefix(fields[1], startPayloadPrefix) {
		return "", false
	}
	body := strings.TrimPrefix(fields[1], startPayloadPrefix)
	if body == "" {
		return "", false
	}
	for i := 0; i < len(body); i++ {
		b := body[i]
		if (b < 'a' || b > 'z') && b != '_' && b != '-' {
			return "", false
		}
	}
	return strings.ReplaceAll(body, "-", "."), true
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
