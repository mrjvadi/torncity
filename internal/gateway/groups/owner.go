package groups

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// MaxCallbackDataBytes is Telegram's limit on callback_data, in bytes.
const MaxCallbackDataBytes = 64

// ownerMark starts an owner tag. The tag is "-<owner id in base 36>:" in front
// of the ordinary "<domain>:<action>[:arg...]" data. A domain is [a-z_]+, so
// no untagged datum can start with "-", and "-" is inside the character set
// routing accepts, so a tagged datum is still well-formed callback data.
//
// Base 36 keeps the tag short: a Telegram user id fits in at most eleven
// characters, so the tag costs at most thirteen of the sixty-four bytes.
const ownerMark = "-"

// BindOwner tags callback data with the Telegram user who may press it.
//
// ok is false, and data is returned unchanged, when there is no owner or the
// tagged datum would not fit Telegram's 64 bytes. An untagged button still
// works; anybody in the group may press it, and the game core acts on the
// presser's own player as it always does.
func BindOwner(data string, owner int64) (string, bool) {
	if data == "" || owner <= 0 || strings.HasPrefix(data, ownerMark) {
		return data, false
	}
	bound := ownerMark + strconv.FormatInt(owner, 36) + ":" + data
	if len(bound) > MaxCallbackDataBytes {
		return data, false
	}
	return bound, true
}

// SplitOwner reads an owner tag off callback data.
//
// bound is false for untagged data, which comes back unchanged. A tag that
// does not parse is reported as bound to nobody (owner 0), so it is refused
// rather than routed with the tag still attached.
func SplitOwner(data string) (owner int64, rest string, bound bool) {
	if !strings.HasPrefix(data, ownerMark) {
		return 0, data, false
	}
	tag, rest, found := strings.Cut(data[len(ownerMark):], ":")
	if !found {
		return 0, "", true
	}
	id, err := strconv.ParseInt(tag, 36, 64)
	if err != nil || id <= 0 {
		return 0, "", true
	}
	return id, rest, true
}

// BindKeyboard returns a copy of kb whose callback buttons are tagged with
// owner. URL buttons are copied as they are. kb itself is never modified.
func BindKeyboard(kb *presenter.Keyboard, owner int64) *presenter.Keyboard {
	if kb == nil {
		return nil
	}
	out := &presenter.Keyboard{Rows: make([][]presenter.Button, 0, len(kb.Rows))}
	for _, row := range kb.Rows {
		next := make([]presenter.Button, 0, len(row))
		for _, b := range row {
			if b.CallbackData != "" {
				b.CallbackData, _ = BindOwner(b.CallbackData, owner)
			}
			next = append(next, b)
		}
		out.Rows = append(out.Rows, next)
	}
	return out
}

// Markup converts the presenter's grid into Telegram's reply markup.
//
// It returns nil, not an empty markup, when there are no buttons: an empty
// inline_keyboard is a valid object that Telegram renders as a blank strip.
func Markup(kb *presenter.Keyboard) any {
	if kb == nil || len(kb.Rows) == 0 {
		return nil
	}

	type button struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data,omitempty"`
		URL          string `json:"url,omitempty"`
	}

	rows := make([][]button, 0, len(kb.Rows))
	for _, row := range kb.Rows {
		out := make([]button, 0, len(row))
		for _, b := range row {
			out = append(out, button{Text: b.Text, CallbackData: b.CallbackData, URL: b.URL})
		}
		rows = append(rows, out)
	}

	return struct {
		InlineKeyboard [][]button `json:"inline_keyboard"`
	}{InlineKeyboard: rows}
}
