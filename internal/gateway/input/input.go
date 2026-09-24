// Package input is free-text input: a button that asks the player to type a
// value, and the message that answers it.
//
// Buttons carry fixed choices. Some choices are not fixed — how much to
// deposit, how much to pay — and the player must type them. A button whose
// callback data is "ask:<command>[:<arg>...]" asks for one: the gateway sends
// a prompt with Telegram's ForceReply, which opens the reply box on the
// player's side, and remembers that this player in this chat owes that
// command a value. Their next message is that value:
//
//   - in the private chat, the next text message that is neither a command nor
//     an alias of one;
//   - in a group, only a reply to the prompt itself. A bot in privacy mode
//     receives replies to its own messages in any case (Telegram's "Replies
//     to any messages implicitly or explicitly meant for this bot"), so this
//     works without the bot being an admin.
//
// The value fills one field of the command's payload (configs/commands.yml,
// section input) and the command is published like any other; the game core
// validates it like any other. Nothing here trusts or interprets the value
// beyond turning Persian digits into ASCII ones.
//
// # State
//
// The waiting input lives in Redis, keyed on bot, chat and user, with a TTL
// (input.ttl): a prompt nobody answers expires on its own. Taking it is one
// Lua script — read, check the prompt it answers, delete — so two deliveries
// of the same reply, or two gateway instances, consume it at most once. A new
// prompt replaces the old one. Sending another command, typing the cancel
// word, or waiting out the TTL drops it.
//
// # Spam
//
// A prompt is a message in the chat, so asking is throttled per player and
// chat (input.cooldown): a second press inside the window re-arms nothing and
// sends nothing. The typed value is capped (input.max_length).
package input

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// AskPrefix is the first segment of a button's callback data that asks for
// input: "ask:bank.deposit", "ask:bank.pay:K7Q2M9A:card".
const AskPrefix = "ask"

// Key names one waiting input: one player in one chat through one bot.
type Key struct {
	BotID  string
	ChatID int64
	UserID int64
}

// String is the key's spelling inside the store's namespace.
func (k Key) String() string {
	return k.BotID + ":" + strconv.FormatInt(k.ChatID, 10) + ":" + strconv.FormatInt(k.UserID, 10)
}

// Pending is one waiting input.
type Pending struct {
	// Command is the command the value is for, "bank.deposit".
	Command string `json:"command"`
	// Payload holds the fixed arguments the asking button carried.
	Payload map[string]string `json:"payload,omitempty"`
	// Field is the payload field the typed value fills.
	Field string `json:"field"`
	// Prompt is the message id of the prompt, which a reply in a group must
	// answer.
	Prompt int64 `json:"prompt"`
}

// Store keeps waiting inputs. internal/infrastructure/redis implements it.
type Store interface {
	// Arm reports whether a prompt may be sent now, and if so starts the
	// cooldown before the next one.
	Arm(ctx context.Context, key Key, cooldown time.Duration) (bool, error)
	// Put records a waiting input for ttl, replacing any other.
	Put(ctx context.Context, key Key, value string, ttl time.Duration) error
	// Take removes and returns the waiting input when there is one and, if
	// prompt is not zero, it was asked by that prompt. Empty means none.
	Take(ctx context.Context, key Key, prompt int64) (string, error)
	// Drop removes the waiting input, if any.
	Drop(ctx context.Context, key Key) error
}

// Encode is how a Pending is stored: the prompt id, a bar, then the JSON. The
// prefix lets Take check the prompt inside Redis without decoding JSON there.
func Encode(p Pending) (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(p.Prompt, 10) + "|" + string(data), nil
}

// Decode reads what Encode wrote.
func Decode(value string) (Pending, error) {
	_, data, ok := strings.Cut(value, "|")
	if !ok {
		return Pending{}, errors.New("input: stored value has no prompt prefix")
	}
	var p Pending
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return Pending{}, err
	}
	return p, nil
}

// ParseAsk reads callback data that asks for input: the command and the
// fixed arguments. ok is false for any other data.
func ParseAsk(data string) (command string, args []string, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) < 2 || parts[0] != AskPrefix || parts[1] == "" {
		return "", nil, false
	}
	return parts[1], parts[2:], true
}

// Build is the command's payload: the waiting input's fixed arguments and the typed
// value.
func (p Pending) Build(value string) map[string]any {
	out := make(map[string]any, len(p.Payload)+1)
	for k, v := range p.Payload {
		out[k] = v
	}
	out[p.Field] = value
	return out
}

// CleanValue trims what the player typed, writes Persian and Arabic digits in
// ASCII and caps its length. Empty means there is nothing to use.
func CleanValue(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	var b strings.Builder
	n := 0
	for _, r := range text {
		if maxRunes > 0 && n >= maxRunes {
			break
		}
		switch {
		case r >= '۰' && r <= '۹':
			r = '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			r = '0' + (r - '٠')
		case r == '٬' || r == '،':
			r = ','
		case r == '\u200c' || r == '\u200e' || r == '\u200f':
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
