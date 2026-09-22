// Package keyboards builds the inline keyboards every screen shares.
//
// It holds layout and addressing only. Not one word of a label is decided
// here: a caller passes labels that it has already resolved through the
// message catalogue, which is why this package imports no i18n and can be
// read without knowing which language a player speaks.
//
// # Callback data is an address, never a value
//
// Every button built here carries routing information and nothing else:
// which screen, which page, which row. It never carries a price, a balance,
// an outcome, a permission or any other authoritative value, because callback
// data is a string the client sends back to us — unsigned, editable, and
// craftable by anyone. The game core re-validates every press against its own
// state, so a forged callback can address a screen but cannot decide anything.
// See internal/gateway/routing for the same rule stated at the parsing edge.
//
// # The 64-byte budget
//
// Telegram limits callback_data to 64 bytes and internal/gateway/routing
// rejects anything longer or outside a conservative byte set. Both limits are
// restated here rather than imported, because a presentation package reaching
// into the gateway would invert the dependency this project is built on; the
// routing tests and the tests here both pin the number, so the two cannot
// drift apart unnoticed.
package keyboards

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// MaxCallbackDataBytes is Telegram's documented limit on callback_data,
// measured in bytes rather than runes — which matters the first time a
// Persian word or an emoji is put anywhere near it.
const MaxCallbackDataBytes = 64

// Separator joins the segments of a callback address. It matches the scheme
// internal/gateway/routing parses: <domain>:<action>[:arg ...].
const Separator = ":"

// Data assembles a callback address from its segments.
//
// It returns the empty string when the result would be unroutable, so a caller
// can tell "no address" from "an address that happens to be short".
func Data(parts ...string) string {
	data := strings.Join(parts, Separator)
	if !Valid(data) {
		return ""
	}
	return data
}

// Page appends a page number to a callback prefix: Page("map:list", 2) is
// "map:list:2". Pages are numbered from one, the way they are shown.
func Page(prefix string, page int) string {
	if page < 1 {
		page = 1
	}
	return Data(prefix, strconv.Itoa(page))
}

// Valid reports whether data is something Telegram will carry and the gateway
// will accept: non-empty, within the byte budget, and built only from bytes
// that cannot end a subject token, introduce whitespace into a log line or
// need escaping on the way back out.
func Valid(data string) bool {
	if data == "" || len(data) > MaxCallbackDataBytes {
		return false
	}
	for i := 0; i < len(data); i++ {
		if !safeByte(data[i]) {
			return false
		}
	}
	return true
}

// safeByte is the conservative set: letters, digits, underscore, hyphen, dot
// and the separator. Every multi-byte character is excluded, which is exactly
// why a player-supplied search term cannot be pasted into a callback address.
func safeByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_', b == '-', b == '.', b == ':':
		return true
	}
	return false
}

// Builder accumulates rows of buttons.
//
// A button whose label is empty or whose callback address is unroutable is
// DROPPED rather than added. A button that cannot be addressed does nothing
// when pressed, and a dead button is worse than a missing one: the player
// presses it, nothing happens, and they conclude the game is broken. Dropped
// counts them so a test can prove a screen is not quietly losing its
// navigation.
type Builder struct {
	rows    [][]presenter.Button
	dropped int
}

// New returns an empty builder.
func New() *Builder { return &Builder{} }

// Button builds one button, reporting whether it is usable.
func Button(text string, parts ...string) (presenter.Button, bool) {
	if text == "" {
		return presenter.Button{}, false
	}
	data := Data(parts...)
	if data == "" {
		return presenter.Button{}, false
	}
	return presenter.Button{Text: text, CallbackData: data}, true
}

// Add appends one button on a row of its own.
func (b *Builder) Add(text string, parts ...string) *Builder {
	btn, ok := Button(text, parts...)
	if !ok {
		b.dropped++
		return b
	}
	return b.Row(btn)
}

// Row appends a row, skipping it when every button in it was dropped.
func (b *Builder) Row(buttons ...presenter.Button) *Builder {
	row := make([]presenter.Button, 0, len(buttons))
	for _, btn := range buttons {
		if btn.Text == "" || (btn.CallbackData == "" && btn.URL == "") {
			b.dropped++
			continue
		}
		row = append(row, btn)
	}
	if len(row) == 0 {
		return b
	}
	b.rows = append(b.rows, row)
	return b
}

// Grid appends buttons wrapped into rows of at most perRow.
func (b *Builder) Grid(perRow int, buttons ...presenter.Button) *Builder {
	if perRow < 1 {
		perRow = 1
	}
	for start := 0; start < len(buttons); start += perRow {
		end := start + perRow
		if end > len(buttons) {
			end = len(buttons)
		}
		b.Row(buttons[start:end]...)
	}
	return b
}

// Nav is the standard navigation block: page controls on one row, then back
// and refresh on the next.
//
// 17_TELEGRAM_UX.md asks for pagination, back and refresh on every screen, so
// they are described by one value rather than rebuilt per screen — a screen
// that forgets one of the three is then a missing field, not a missing idea.
//
// Every Text field is a label the caller has already resolved from the
// catalogue. Prefix is the callback address of the screen itself, for example
// "map:list"; the page number is appended to it. An empty Prefix, or a page
// control that would run off either end of the list, simply omits that button.
type Nav struct {
	PrevText    string
	NextText    string
	BackText    string
	RefreshText string

	// Prefix addresses this screen, without a page segment.
	Prefix string
	// Page is the page currently shown, numbered from one.
	Page int
	// HasPrev and HasNext say whether the neighbouring pages exist. The
	// screen knows this; the builder must not guess it from Page alone,
	// because the last page is not a property of the page number.
	HasPrev bool
	HasNext bool

	// BackData and RefreshData are full callback addresses. Refresh
	// defaults to this screen's own current page when left empty, which is
	// what refresh means.
	BackData    string
	RefreshData string
}

// Nav appends the navigation block described by n.
func (b *Builder) Nav(n Nav) *Builder {
	var pager []presenter.Button
	if n.HasPrev && n.Prefix != "" {
		if btn, ok := Button(n.PrevText, n.Prefix, strconv.Itoa(pageOrOne(n.Page)-1)); ok {
			pager = append(pager, btn)
		} else {
			b.dropped++
		}
	}
	if n.HasNext && n.Prefix != "" {
		if btn, ok := Button(n.NextText, n.Prefix, strconv.Itoa(pageOrOne(n.Page)+1)); ok {
			pager = append(pager, btn)
		} else {
			b.dropped++
		}
	}
	if len(pager) > 0 {
		b.Row(pager...)
	}

	refresh := n.RefreshData
	if refresh == "" && n.Prefix != "" {
		refresh = Page(n.Prefix, pageOrOne(n.Page))
	}

	var tail []presenter.Button
	if btn, ok := Button(n.BackText, n.BackData); ok {
		tail = append(tail, btn)
	}
	if btn, ok := Button(n.RefreshText, refresh); ok {
		tail = append(tail, btn)
	}
	if len(tail) > 0 {
		b.Row(tail...)
	}
	return b
}

func pageOrOne(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

// Dropped reports how many buttons were refused as unusable.
func (b *Builder) Dropped() int { return b.dropped }

// Rows reports how many rows have been added so far.
func (b *Builder) Rows() int { return len(b.rows) }

// Build returns the keyboard, or nil when nothing usable was added. A nil
// keyboard renders as a message with no buttons, which is the right outcome
// for a screen that has nowhere to go.
func (b *Builder) Build() *presenter.Keyboard {
	if len(b.rows) == 0 {
		return nil
	}
	return &presenter.Keyboard{Rows: b.rows}
}
