package render

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/notices"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// adapt turns a neutral view into the view type the Telegram screen function
// takes. The two types carry the same facts under the same field names, and
// both are written by presentation.EncodeView, so the conversion is an encode
// and a decode; TestNoticeViewsMatchTheTelegramViews keeps the shapes equal.
func adapt[S any](v any) S {
	var s S
	raw, err := presentation.EncodeView(v)
	if err != nil {
		return s
	}
	_ = presentation.DecodeView(raw, &s)
	return s
}

// registerAs joins a neutral screen, whose view is N, to the Telegram screen
// function that words it from a view of the type S.
func registerAs[N, S any](screen string, fn func(screens.Context, S) *presenter.Response) {
	Register(screen, func(c screens.Context, v N) *presenter.Response { return fn(c, adapt[S](v)) })
}

// The notices area: what workers push to a player or a group. Each notice is
// worded by the screen function internal/telegram/screens has always had for
// it, from the facts the producer sent as data.
func init() {
	registerAs[notices.PaymentView](notices.ScreenPaymentNotice, screens.PaymentNotice)
	registerAs[notices.AchievementView](notices.ScreenAchievementNotice, screens.AchievementNotice)
	registerAs[notices.OfficeView](notices.ScreenOfficeNotice, screens.OfficeNotice)
	registerAs[notices.AuctionView](notices.ScreenAuctionNotice, screens.AuctionNotice)
	registerAs[notices.VictimView](notices.ScreenVictimNotice, screens.VictimNotice)
	registerAs[notices.CaseOutcomeView](notices.ScreenCaseSolvedNotice, screens.CaseSolvedNotice)
	registerAs[notices.CaseOutcomeView](notices.ScreenConvictedNotice, screens.ConvictedNotice)
	registerAs[notices.TreatyView](notices.ScreenTreatyProposedNotice, screens.TreatyProposedNotice)
	registerAs[notices.ElectionResultView](notices.ScreenElectionResultNotice, screens.ElectionResultNotice)
	registerAs[notices.FactionRequestView](notices.ScreenFactionRequestNotice, screens.FactionRequestNotice)
	registerAs[notices.FactionAnswerView](notices.ScreenFactionAnswerNotice, screens.FactionAnswerNotice)
	registerAs[notices.FactionCrimeView](notices.ScreenFactionCrimeNotice, screens.FactionCrimeNotice)
	registerAs[notices.FinanceView](notices.ScreenFinanceNotice, screens.FinanceNotice)
	registerAs[notices.HospitalisedView](notices.ScreenHospitalisedNotice, screens.HospitalisedNotice)
	registerAs[notices.ClinicTreatedView](notices.ScreenClinicTreatedNotice, screens.ClinicTreatedNotice)
	registerAs[notices.BillDecidedView](notices.ScreenBillDecidedNotice, screens.BillDecidedNotice)
	registerAs[notices.RankView](notices.ScreenRankNotice, screens.RankNotice)
	RegisterEmpty(notices.ScreenHungerNotice, screens.HungerNotice)
	registerAs[notices.MarketFilledView](notices.ScreenMarketFilledNotice, screens.MarketFilledNotice)
	registerAs[notices.MissionCompletedView](notices.ScreenMissionCompleted, screens.MissionCompletedNotice)
	registerAs[notices.PropertyView](notices.ScreenPropertyNotice, screens.PropertyNotice)
	registerAs[notices.RecruitView](notices.ScreenRecruitNotice, screens.RecruitNotice)
	registerAs[notices.StockView](notices.ScreenStockNotice, screens.StockNotice)
	registerAs[notices.VillageNewsView](notices.ScreenVillageNews, screens.VillageNews)
	registerAs[notices.InboxReminderView](notices.ScreenInboxReminder, screens.InboxReminder)
	registerAs[notices.InboxHubView](notices.ScreenInboxHub, screens.InboxHub)

	// The inbox screens that carry stored notices word each of them with the
	// renderer of its own screen, in the reader's language.
	Register(notices.ScreenInboxBadge, func(c screens.Context, v notices.InboxBadgeView) *presenter.Response {
		s := screens.InboxBadgeView{Unread: v.Unread}
		for _, cat := range v.Categories {
			s.Categories = append(s.Categories, screens.InboxCategoryCount{Category: cat.Category, Count: cat.Count})
		}
		for _, t := range v.Teaser {
			s.Teaser = append(s.Teaser, storedText(c, t))
		}
		return screens.InboxBadge(c, s)
	})
	Register(notices.ScreenInboxCategory, func(c screens.Context, v notices.InboxCategoryView) *presenter.Response {
		s := screens.InboxCategoryView{Category: v.Category, Page: v.Page, TotalPages: v.TotalPages}
		for _, it := range v.Items {
			s.Items = append(s.Items, screens.InboxItemLine{
				ID: it.ID, Read: it.Read,
				Text: storedText(c, it.Notice), Ago: it.Ago, LinkAddr: it.Link.Address(),
			})
		}
		return screens.InboxCategory(c, s)
	})
}

// storedText is a stored notice as the text the Telegram inbox lists: the
// notice worded by the renderer of its own screen, or, for a notice of a
// screen not carried as data yet, the text its producer wrote.
func storedText(c screens.Context, n notices.StoredNotice) string {
	if n.Screen == "" {
		return n.Text
	}
	mu.RLock()
	fn, ok := renderers[n.Screen]
	mu.RUnlock()
	if !ok {
		return n.Text
	}
	out, err := fn(c, &presentation.Response{Contract: presentation.ContractNeutral, Screen: n.Screen, View: n.View, Lang: c.Lang})
	if err != nil || out == nil || out.Text == "" {
		return n.Text
	}
	return out.Text
}
