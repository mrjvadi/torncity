package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Presence (docs/adr/0030-realtime-interest-and-presence.md section 3): the
// «who is around» group screen of a settlement, and the «last seen» setting
// on the private settings screen.
//
// The group screen is the settlement's own roster, a civic fact everybody in
// the group already sees; it never shows one member's personal setting. The
// setting itself is a personal screen and lives only in the private chat.

// Addresses.
const (
	// AddrSettlementWho is the group screen listing who is around.
	AddrSettlementWho = "settlement:who"
	// AddrPresenceSet changes the «last seen» setting: player:presence.set:<v>.
	AddrPresenceSet = "player:presence.set"
)

// MaxWhoLines bounds how many online players the group screen names; the
// rest are counted. A group message stays short however big the village.
const MaxWhoLines = 20

// ScreenSettlementWho is the client name of the settlement roster view.
const ScreenSettlementWho = village.ScreenSettlementWho

// WhoLine and SettlementWhoView are defined with the village area's other
// views (internal/presentation/village).
type (
	WhoLine           = village.WhoLine
	SettlementWhoView = village.SettlementWhoView
)

// SettlementWho renders the group screen.
func SettlementWho(c Context, v SettlementWhoView) *presenter.Response {
	return c.withGroupView(renderSettlementWho(c, v), ScreenSettlementWho, v)
}

// ActivityLabel is the words for an activity code (presence.activity.<code>);
// an unknown code reads as idle.
func ActivityLabel(c Context, code string) string {
	key := "presence.activity." + code
	if text := c.T(key, nil); text != key {
		return text
	}
	return c.T("presence.activity.idle", nil)
}

func renderSettlementWho(c Context, v SettlementWhoView) *presenter.Response {
	title := c.T("presence.who.title", map[string]any{"name": v.Name})
	online := int64(len(v.Online))
	counts := c.T("presence.who.counts", map[string]any{
		"online": FormatNumber(c, online), "offline": FormatNumber(c, int64(v.Offline)),
	})

	var lines []string
	shown := v.Online
	if len(shown) > MaxWhoLines {
		shown = shown[:MaxWhoLines]
	}
	for _, l := range shown {
		text := c.T("presence.who.line", map[string]any{"name": l.Name, "activity": ActivityLabel(c, l.Activity)})
		if l.Place != "" {
			if place := c.VenueName(Named{Code: l.Place}); place != "" {
				text = c.T("presence.who.line_place", map[string]any{
					"name": l.Name, "activity": ActivityLabel(c, l.Activity), "place": place,
				})
			}
		}
		lines = append(lines, text)
	}
	if more := len(v.Online) - len(shown); more > 0 {
		lines = append(lines, c.T("presence.who.more", map[string]any{"count": FormatNumber(c, int64(more))}))
	}
	list := body(lines...)
	if list == "" {
		list = c.T("presence.who.nobody", nil)
	}

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrSettlementWho}))
	return c.respond(paragraphs(title, counts, list), kb.Build())
}

// presenceSetting states the «last seen» setting and offers the other two.
// It adds nothing when the view carries no setting (a render that predates
// the feature, a client that does not offer it).
func presenceSetting(c Context, v SettingsView, kb *keyboards.Builder) string {
	if v.PresenceVisibility == "" {
		return ""
	}
	for _, opt := range PresenceOptions {
		if opt == v.PresenceVisibility {
			continue
		}
		kb.Add(c.T("button.change_presence", map[string]any{"visibility": PresenceName(c, opt)}), AddrPresenceSet, opt)
	}
	line := c.T("settings.presence", map[string]any{"visibility": PresenceName(c, v.PresenceVisibility)})
	if v.PresenceVisibility == "nobody" {
		line += "\n" + c.T("settings.presence_nobody_hint", nil)
	}
	return line
}

// PresenceOptions are the «last seen» settings, in the order offered.
var PresenceOptions = []string{"everyone", "contacts", "nobody"}

// PresenceName is a setting's name in this context's language.
func PresenceName(c Context, v string) string { return c.T("presence.visibility."+v, nil) }
