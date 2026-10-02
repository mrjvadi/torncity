package clientapi

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// What marks a string as written for Telegram (docs/adr/0039-presentation-split.md):
// the web draws its own words, so none of these may ever reach it.
var (
	telegramMarkup = regexp.MustCompile(`(?i)</?(b|i|u|s|a|em|strong|code|pre|tg-spoiler|blockquote)(\s[^>]*)?>`)
	// «دکمهٔ زیر» / «دکمه پایین» (written as code points: no wording lives in source) and "the button below".
	telegramPhrase = regexp.MustCompile(`(?i)(\x{062F}[\x{06A9}\x{0643}]\x{0645}\x{0647}[\x{0654}\x{200C}\s]*([\x{06CC}\x{064A}][\x{200C}\s]*)?(\x{0647}\x{0627}\x{06CC}[\x{200C}\s]*)?(\x{0632}[\x{06CC}\x{064A}]\x{0631}|\x{067E}\x{0627}[\x{06CC}\x{064A}]{2}\x{0646})|buttons? (below|underneath)|press (a|the) button)`)
)

// isEmojiLead reports whether r is an emoji or pictograph a Telegram text
// starts its lines with.
func isEmojiLead(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2B00 && r <= 0x2BFF:
		return true
	case r == 0x2B50 || r == 0x2B06 || r == 0x23F3 || r == 0x231B || r == 0x23F8 || r == 0x2705 || r == 0x274C:
		return true
	}
	return false
}

// TelegramLeftover says why s reads as Telegram text: HTML markup, a line
// that starts with an emoji, or «دکمهٔ زیر» ("the button below") wording. It
// returns "" for a string with none of them.
func TelegramLeftover(s string) string {
	if telegramMarkup.MatchString(s) {
		return "html"
	}
	if telegramPhrase.MatchString(s) {
		return "button-below"
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if r, n := utf8.DecodeRuneInString(line); n > 0 && isEmojiLead(r) {
			return "emoji-line"
		}
	}
	return ""
}

// ScreenLeftovers lists the Telegram text a screen carries to the client:
// its text, an action's label, an error's message or a notice's text. A
// neutral screen has none.
func ScreenLeftovers(s Screen) []string {
	var out []string
	add := func(where, v string) {
		if v != "" {
			out = append(out, where+": "+v)
		}
	}
	add("text", s.Text)
	for _, a := range s.Actions {
		add("label "+a.ID, a.Label)
	}
	if s.Error != nil {
		add("error.message", s.Error.Message)
	}
	if s.Notice != nil {
		add("notice.text", s.Notice.Text)
	}
	return out
}
