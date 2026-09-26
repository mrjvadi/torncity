package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The notification inbox (migrations/0037_notification_inbox): the badge
// message cmd/notifier keeps edited in place as inbox-mode notices arrive,
// the hub screen a player opens it into, one category's compact list, and
// the 24h-unread reminder. See internal/workers/notification/badge.go for
// who calls these and when; the /inbox command itself is
// handlers.InboxHandler.

const (
	ScreenInboxBadge    = "inbox_badge"
	ScreenInboxHub      = "inbox_hub"
	ScreenInboxCategory = "inbox_category"
	ScreenInboxReminder = "inbox_reminder"

	// AddrInboxShow opens the hub. AddrInboxCategory takes a category code
	// and a page ("inbox:category:companies:2"). AddrInboxReadAll marks
	// everything read.
	AddrInboxShow     = "inbox:show"
	AddrInboxCategory = "inbox:category"
	AddrInboxReadAll  = "inbox:read_all"
)

// InboxCategoryCount is one category's unread tally, shown on the badge and
// the hub alike.
type InboxCategoryCount struct {
	Category string
	Count    int
}

// InboxBadgeView is the one message a player's inbox count is edited onto.
type InboxBadgeView struct {
	Unread     int
	Categories []InboxCategoryCount
	// Teaser is the one or two most recent items' own text, already in this
	// context's language.
	Teaser []string
}

// InboxBadge renders the badge. Its MessageID (Context) decides whether the
// gateway sends it fresh or edits the one it already sent — badge.go sets
// it, never this function.
func InboxBadge(c Context, v InboxBadgeView) *presenter.Response {
	return c.withView(renderInboxBadge(c, v), ScreenInboxBadge, v)
}

func renderInboxBadge(c Context, v InboxBadgeView) *presenter.Response {
	kb := keyboards.New()
	if v.Unread == 0 {
		kb.Add(c.T("button.back", nil), AddrHome)
		return c.respond(c.T("inbox.badge.empty", nil), kb.Build()).MarkPrivate()
	}
	lines := []string{c.T("inbox.badge.title", map[string]any{"count": v.Unread})}
	for _, cat := range v.Categories {
		lines = append(lines, c.T("inbox.badge.category_line",
			map[string]any{"category": c.T("inbox.category."+cat.Category, nil), "count": cat.Count}))
	}
	for _, t := range v.Teaser {
		lines = append(lines, c.T("inbox.badge.teaser_line", map[string]any{"text": t}))
	}
	if btn, ok := keyboards.Button(c.T("inbox.badge.open_button", nil), AddrInboxShow); ok {
		kb.Row(btn)
	}
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// InboxHubCategory is one category's row on the hub screen.
type InboxHubCategory struct {
	Category string
	Count    int
}

// InboxHubView is the inbox opened: every category with unread items, and
// the total.
type InboxHubView struct {
	Total      int
	Categories []InboxHubCategory
}

// InboxHub renders the hub, opening the badge marks everything read first
// (handlers.InboxHandler.Show), so this always shows Total 0 or the unread
// that arrived since.
func InboxHub(c Context, v InboxHubView) *presenter.Response {
	return c.withView(renderInboxHub(c, v), ScreenInboxHub, v)
}

func renderInboxHub(c Context, v InboxHubView) *presenter.Response {
	kb := keyboards.New()
	if len(v.Categories) == 0 {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrInboxShow}))
		return c.respond(c.T("inbox.hub.empty", nil), kb.Build()).MarkPrivate()
	}
	lines := []string{c.T("inbox.hub.title", map[string]any{"count": v.Total})}
	for _, cat := range v.Categories {
		label := c.T("inbox.category."+cat.Category, nil)
		lines = append(lines, c.T("inbox.hub.category_line", map[string]any{"category": label, "count": cat.Count}))
		if btn, ok := keyboards.Button(
			c.T("inbox.hub.open_button", map[string]any{"category": label, "count": cat.Count}),
			AddrInboxCategory, cat.Category, "1"); ok {
			kb.Row(btn)
		}
	}
	if btn, ok := keyboards.Button(c.T("inbox.hub.read_all_button", nil), AddrInboxReadAll); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrInboxShow}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// InboxItemLine is one stored notification, as the category list shows it.
type InboxItemLine struct {
	Text string
	// Ago is how long ago it arrived, as of when the screen was built.
	Ago time.Duration
	// LinkAddr is a callback address (keyboards.Data) that opens the
	// item's own screen; empty means the item has no button of its own.
	LinkAddr string
}

// InboxCategoryView is one category's compact, paginated list.
type InboxCategoryView struct {
	Category         string
	Items            []InboxItemLine
	Page, TotalPages int
}

// InboxCategory renders one category's list.
func InboxCategory(c Context, v InboxCategoryView) *presenter.Response {
	return c.withView(renderInboxCategory(c, v), ScreenInboxCategory, v)
}

func renderInboxCategory(c Context, v InboxCategoryView) *presenter.Response {
	label := c.T("inbox.category."+v.Category, nil)
	lines := []string{c.T("inbox.category_title", map[string]any{"category": label})}
	kb := keyboards.New()
	if len(v.Items) == 0 {
		lines = append(lines, c.T("inbox.category_empty", nil))
	}
	for i, item := range v.Items {
		lines = append(lines, c.T("inbox.item_line", map[string]any{"text": item.Text, "ago": FormatSpan(c, item.Ago)}))
		if item.LinkAddr != "" {
			if btn, ok := keyboards.Button(c.T("inbox.open_item_button", map[string]any{"n": i + 1}), item.LinkAddr); ok {
				kb.Row(btn)
			}
		}
	}
	prefix := keyboards.Data(AddrInboxCategory, v.Category)
	kb.Nav(c.nav(keyboards.Nav{
		Prefix: prefix, Page: v.Page, HasPrev: v.Page > 1, HasNext: v.Page < v.TotalPages,
		BackData: AddrInboxShow,
	}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// InboxReminderView is the 24h-unread nudge.
type InboxReminderView struct {
	Unread int
}

// InboxReminder renders the reminder. It always sends (a notice never
// edits, badge.go's own edit aside), so its Context carries no MessageID.
func InboxReminder(c Context, v InboxReminderView) *presenter.Response {
	return c.withView(renderInboxReminder(c, v), ScreenInboxReminder, v)
}

func renderInboxReminder(c Context, v InboxReminderView) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("inbox.badge.open_button", nil), AddrInboxShow); ok {
		kb.Row(btn)
	}
	return c.respond(c.T("inbox.reminder", map[string]any{"count": v.Unread}), kb.Build()).MarkPrivate()
}
