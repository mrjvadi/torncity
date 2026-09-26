package screens

import "strings"

// Telegram's HTML formatting, opted into per screen.
//
// A screen that wants a bold title or a collapsible detail (Bot API 7.4's
// "expandable_blockquote", <blockquote expandable>) builds its text exactly
// as every other screen does — through c.T, body and paragraphs — and then
// wraps the pieces that want it with the helpers below, ESCAPE THEN WRAP,
// never the other way around: htmlEscape first, so a player's own name, a
// company's name or anything else that reached the screen as data can never
// close a tag early or be read as one; the literal tag characters this file
// writes are added after, and are never escaped.
//
// A screen that uses any of this MUST end with .AsHTML() on its response
// (presenter.Response.HTML), or Telegram receives the tags as literal text.
// screentest.ValidTelegramHTML checks every HTML-flagged snapshot fixture
// for balanced tags from Telegram's allowed set and for anything that
// reached Telegram unescaped — screens.go's own tests run it on every
// screen this package renders, so a mistake here fails the build, not a
// player's message.

// htmlEscape escapes the three characters Telegram's HTML parse mode
// requires escaped in ordinary text: "&" first, so escaping "<" and ">"
// next can never produce a second "&" that looks like part of an entity.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// htmlBold wraps already-escaped text in Telegram's bold tag, for a
// screen's title line.
func htmlBold(escaped string) string {
	return "<b>" + escaped + "</b>"
}

// htmlExpandableQuote wraps already-escaped text (its own newlines are fine)
// in a blockquote collapsed to a few lines until the player taps to expand
// it (Bot API 7.4) — for the second half of a screen, once the key facts
// are already on it: a long rules explanation, a list of what is still
// missing, a history. Never for the top of a screen, and never around
// anything with a button of its own beneath it that the collapsed text is
// the only reason to press.
func htmlExpandableQuote(escaped string) string {
	return "<blockquote expandable>" + escaped + "</blockquote>"
}
