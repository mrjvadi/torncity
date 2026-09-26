package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The notification inbox (migrations/0037_notification_inbox) joins the
// snapshot harness as its own area, testdata/snapshots/<language>/
// inbox.txt: the badge message cmd/notifier edits in place, the hub, one
// category's list and the 24h-unread reminder.
func init() { snapshotAreas["inbox"] = inboxSnapshots }

// inboxSampleText is sample item text in the language of the screen: an
// inbox item's text is never translated at read time (it was rendered once,
// in the player's language, when the event arrived — see badge.go), so a
// sample must already be in that language, exactly like a real one would be.
type inboxSampleText struct{ periodSettled, marketFilled, salaryPaid, loanDue string }

var inboxSamples = map[string]inboxSampleText{
	"fa": {
		periodSettled: "دورهٔ شرکت شما تسویه شد: سود 42,000.",
		marketFilled:  "سفارش بازار شما برای «صفحهٔ فولاد» تکمیل شد.",
		salaryPaid:    "حقوق شما به مبلغ 1,200 پرداخت شد.",
		loanDue:       "قسط وام شما به مبلغ 800 فردا سررسید می‌شود.",
	},
	"en": {
		periodSettled: "Your company's period settled: profit 42,000.",
		marketFilled:  "Your market order for Steel Plate filled.",
		salaryPaid:    "Your salary of 1,200 was paid.",
		loanDue:       "A loan payment of 800 is due tomorrow.",
	},
}

func inboxSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	s := inboxSamples[c.Lang]

	add("Badge · no new notifications", InboxBadge(sent(c), InboxBadgeView{}))
	add("Badge · a few, across categories", InboxBadge(sent(c), InboxBadgeView{
		Unread: 5,
		Categories: []InboxCategoryCount{
			{Category: "companies", Count: 2},
			{Category: "finance", Count: 2},
			{Category: "market", Count: 1},
		},
		Teaser: []string{s.periodSettled, s.marketFilled},
	}))

	add("Hub · empty", InboxHub(sent(c), InboxHubView{}))
	add("Hub · grouped by category", InboxHub(sent(c), InboxHubView{
		Total: 6,
		Categories: []InboxHubCategory{
			{Category: "companies", Count: 2},
			{Category: "finance", Count: 2},
			{Category: "market", Count: 1},
			{Category: "government", Count: 1},
		},
	}))

	add("Category · a page of items", InboxCategory(sent(c), InboxCategoryView{
		Category: "finance",
		Page:     1, TotalPages: 2,
		Items: []InboxItemLine{
			{Text: s.salaryPaid, Ago: 5 * time.Minute},
			{Text: s.loanDue, Ago: 3 * time.Hour, LinkAddr: AddrLoanHub},
		},
	}))
	add("Category · empty", InboxCategory(sent(c), InboxCategoryView{Category: "market", Page: 1, TotalPages: 1}))

	add("Reminder · unread for a day", InboxReminder(sent(c), InboxReminderView{Unread: 4}))
}
