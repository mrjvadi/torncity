package screens

import (
	"strings"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The few lines the gateway answers itself, without the game: a command sent
// where it does not run (configs/commands.yml), and free-text input — the
// question a «✏️» button asks and its cancellation (internal/gateway/input).
// They are screens like any other so the snapshot harness reads them.

// PrivateOnly answers, in a group, a command that runs only in the private
// chat. link opens the private chat and runs it there; empty when the bot's
// username is unknown, and the line then says where to go instead.
func PrivateOnly(c Context, link string) *presenter.Response {
	if link == "" {
		return presenter.Message(c.T("channel.private_only_nolink", nil), nil)
	}
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: c.T("channel.open_private", nil), URL: link}}}}
	return presenter.Message(c.T("channel.private_only", nil), kb)
}

// GroupOnly answers, in the private chat, a command that runs only in a
// group. city is the player's city when it has a group, for the hint where.
func GroupOnly(c Context, city string) *presenter.Response {
	text := c.T("channel.group_only", nil)
	if city != "" {
		text = c.T("channel.group_only_city", map[string]any{"city": city})
	}
	kb := keyboards.New()
	kb.Add(c.T("button.profile", nil), AddrProfile)
	return presenter.Message(text, kb.Build())
}

// AddrAsk is the callback address of a button that asks the player to type a
// value: "ask:<command>[:<arg>...]" (internal/gateway/input). The command
// must be listed under input in configs/commands.yml.
const AddrAsk = "ask"

// inputKey is the catalogue key of one command's question: the command with
// its dots written as underscores, "input.prompt.bank_deposit".
func inputKey(section, command string) string {
	return "input." + section + "." + strings.ReplaceAll(command, ".", "_")
}

// InputPrompt is the question a «✏️» button asks for command. mention, when
// set, is the @username the question is addressed to in a group.
func InputPrompt(c Context, command, mention string) string {
	key := inputKey("prompt", command)
	question := c.T(key, nil)
	if question == key {
		question = c.T("input.prompt.default", nil)
	}
	return body(mention, question, c.T("input.cancel_hint", nil))
}

// InputPlaceholder is the grey text in the reply box while the question
// waits. Telegram takes 1-64 characters.
func InputPlaceholder(c Context, command string) string {
	key := inputKey("placeholder", command)
	text := c.T(key, nil)
	if text == key {
		text = c.T("input.placeholder.default", nil)
	}
	if r := []rune(text); len(r) > 64 {
		text = string(r[:64])
	}
	return text
}

// InputCancelled answers the cancel word while a question waited.
func InputCancelled(c Context) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("button.profile", nil), AddrProfile)
	return presenter.Message(c.T("input.cancelled", nil), kb.Build())
}

// Announcements: the public lines a city's group reads
// (internal/workers/notification/announce.go). They name players and cities,
// never an amount, and carry no buttons.

// ArrivalAnnouncement is the line for a player who arrived in a city.
func ArrivalAnnouncement(c Context, player, cityCode, city string) string {
	return c.T("announce.arrived", map[string]any{"player": c.playerName(player), "city": c.CityName(cityCode, city)})
}

// JailAnnouncement is the line for a player jailed in a city.
func JailAnnouncement(c Context, player, cityCode, city string) string {
	return c.T("announce.jailed", map[string]any{"player": c.playerName(player), "city": c.CityName(cityCode, city)})
}

// Announcement finishes a line: held, when lines were held back since the
// last one a busy group received, is said after it.
func Announcement(c Context, line string, held int) string {
	if held <= 0 {
		return line
	}
	return body(line, c.T("announce.held", map[string]any{"count": held}))
}

// OperatorAnnouncement is an operator's announcement to every city's groups
// (admin announce): their words, as written, under a heading that says who
// speaks.
func OperatorAnnouncement(c Context, text string) string {
	return c.T("announce.operator", map[string]any{"text": text})
}
