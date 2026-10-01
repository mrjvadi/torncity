package render

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/notices"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// noticeFixtures are the notices and the founding screens with the facts a
// producer would send, each as the neutral response and (through the
// registered renderer, as the edge does) as Telegram's screen.
func noticeFixtures() []fixture {
	c := presentation.Ctx{Lang: "fa"}
	n := func(code string) notices.Named { return notices.Named{Code: code, Name: code} }
	view, _ := presentation.EncodeView(notices.PaymentView{PayerName: "Ada", Method: "card", Amount: 5})
	neutral := []*presentation.Response{
		notices.PaymentNotice(c, notices.PaymentView{PayerName: "Ada", PayerCode: "A1", Method: "cash", Amount: 5}),
		notices.AchievementNotice(c, notices.AchievementView{Achievement: n("first"), Cash: 10, Withheld: 5}),
		notices.OfficeNotice(c, notices.OfficeView{Office: "mayor", Place: notices.Place{Kind: "city", Code: "x"}}),
		notices.AuctionNotice(c, notices.AuctionView{Kind: "outbid", No: 4, Item: n("axe"), Amount: 9}),
		notices.AuctionNotice(c, notices.AuctionView{Kind: "won", No: 4, Item: n("axe"), Amount: 9}),
		notices.VictimNotice(c, notices.VictimView{Crime: n("pickpocket"), Venue: n("market"), Amount: 3, CrimeID: "c1", ReportFee: 2, ReportWithin: time.Hour}),
		notices.CaseSolvedNotice(c, notices.CaseOutcomeView{Crime: n("pickpocket"), Solved: true, Thief: "Kaveh", Restored: 3}),
		notices.ConvictedNotice(c, notices.CaseOutcomeView{Crime: n("pickpocket"), Solved: true, Restored: 3, Term: time.Hour}),
		notices.TreatyProposedNotice(c, notices.TreatyView{Country: notices.Place{Kind: "country", Code: "ir"}, Other: notices.Place{Kind: "country", Code: "tr"}, Kind: n("trade"), No: 7, TTL: time.Hour}),
		notices.ElectionResultNotice(c, notices.ElectionResultView{No: 3, Office: "mayor", Place: notices.Place{Kind: "city", Code: "x"}, Elected: true, Deposit: 5}),
		notices.FactionRequestNotice(c, notices.FactionRequestView{No: 8, Kind: "invite", Ref: notices.FactionRef{Code: "f"}, Player: notices.Person{Name: "Ada"}}),
		notices.FactionAnswerNotice(c, notices.FactionAnswerView{Ref: notices.FactionRef{Code: "f"}, Kind: "apply", Accepted: true}),
		notices.FactionAnswerNotice(c, notices.FactionAnswerView{Ref: notices.FactionRef{Code: "f"}, Kind: "invite"}),
		notices.FactionCrimeNotice(c, notices.FactionCrimeView{Ref: notices.FactionRef{Code: "f"}, Crime: n("heist"), Result: "succeeded", Injury: &notices.Injury{Damage: 3, Hospital: true}}),
		notices.FactionCrimeNotice(c, notices.FactionCrimeView{Ref: notices.FactionRef{Code: "f"}, Crime: n("heist"), Result: "caught", Jail: &notices.Sentence{Remaining: time.Hour}}),
		notices.FactionCrimeNotice(c, notices.FactionCrimeView{Ref: notices.FactionRef{Code: "f"}, Crime: n("heist"), Result: "succeeded"}),
		notices.FinanceNotice(c, notices.FinanceView{Kind: "due", No: 2, Product: n("loan")}),
		notices.FinanceNotice(c, notices.FinanceView{Kind: "repaid", No: 2, Product: n("loan")}),
		notices.FinanceNotice(c, notices.FinanceView{Kind: "claimed", No: 2, Product: n("policy")}),
		notices.HospitalisedNotice(c, notices.HospitalisedView{CityCode: "x", Cause: "crime", Damage: 5, Health: 10, Max: 100, Remaining: time.Hour}),
		notices.ClinicTreatedNotice(c, notices.ClinicTreatedView{Ref: notices.CompanyRef{Code: "cl"}, Patient: "Ada", Price: 5, Item: n("pill"), Units: 1}),
		notices.BillDecidedNotice(c, notices.BillDecidedView{No: 6, Place: notices.Place{Kind: "city", Code: "x"}, Status: "passed", Yes: 3}),
		notices.RankNotice(c, notices.RankView{Rank: notices.Rank{Code: "r2"}, From: notices.Rank{Code: "r1"}, Up: true, Worth: 9}),
		notices.HungerNotice(c),
		notices.MarketFilledNotice(c, notices.MarketFilledView{Side: "buy", Item: n("axe"), Qty: 1, Price: 3, Amount: 3}),
		notices.MissionCompletedNotice(c, notices.MissionCompletedView{Mission: n("m"), Cash: 5, Items: []notices.Loot{{Item: n("axe"), Qty: 1}}}),
		notices.PropertyNotice(c, notices.PropertyView{Kind: "sold", Type: n("house"), No: 1, City: notices.Place{Kind: "city", Code: "x"}}),
		notices.RecruitNotice(c, notices.RecruitView{Kind: "applied", Company: notices.CompanyRef{Code: "co", Name: "Co"}, CampaignNo: 5, Count: 2}),
		notices.RecruitNotice(c, notices.RecruitView{Kind: "hired", Company: notices.CompanyRef{Code: "co", Name: "Co"}, CampaignNo: 5}),
		notices.StockNotice(c, notices.StockView{Kind: "filled", Company: n("acme"), Side: "buy", Qty: 1, Price: 2, Amount: 2}),
		notices.VillageNews(c, notices.VillageNewsView{Village: "v", Items: []notices.VillageNewsItem{{Kind: "built", Building: n("mill")}}}),
		notices.VillageNews(c, notices.VillageNewsView{Village: "v", Items: []notices.VillageNewsItem{{Kind: "built", Building: n("mill")}, {Kind: "researched", Knowledge: n("k")}}}),
		notices.InboxBadge(c, notices.InboxBadgeView{Unread: 2, Categories: []notices.InboxCategoryCount{{Category: "finance", Count: 2}},
			Teaser: []notices.StoredNotice{{Screen: notices.ScreenPaymentNotice, View: view}}}),
		notices.InboxBadge(c, notices.InboxBadgeView{}),
		notices.InboxHub(c, notices.InboxHubView{Total: 2, Categories: []notices.InboxHubCategory{{Category: "finance", Count: 2}}}),
		notices.InboxHub(c, notices.InboxHubView{}),
		notices.InboxCategory(c, notices.InboxCategoryView{Category: "finance", Page: 2, TotalPages: 3,
			Items: []notices.InboxItemLine{{Kind: "bank.payment_received", Notice: notices.StoredNotice{Screen: notices.ScreenPaymentNotice, View: view}, Ago: time.Hour,
				Link: presentation.RefOfAddress(notices.AddrBank)}}}),
		notices.InboxReminder(c, notices.InboxReminderView{Unread: 4}),
		village.FoundDraft(c, village.FoundDraftView{Founder: "Ada", Minutes: 20, DraftID: "0f0f0f0f-0000-4000-8000-000000000001"}),
		village.FoundingRefusal(c, village.FoundingRefusalView{Kind: village.FoundingInvalid, Problems: []village.FoundingProblem{{Field: "name", Code: "name_short"}}}),
		village.SettlementFounded(c, village.SettlementFoundedView{Name: "v", BiomeCode: "plain", Buildings: []string{"granary"},
			Emblem: village.FoundingEmblemView{Shape: "shield", ColorA: "gold", ColorB: "azure", Icon: "tree"}}),
		village.SettlementRefusal(c, village.SettlementRefusalView{Kind: village.SettlementAlready, Name: "v"}),
	}
	out := make([]fixture, 0, len(neutral))
	for _, r := range neutral {
		r := r
		out = append(out, fixture{name: r.Screen, neutral: r, telegram: func(x screens.Context) *presenter.Response {
			// as the Telegram edge words it, from the neutral response
			mu.RLock()
			fn := renderers[r.Screen]
			mu.RUnlock()
			resp, err := fn(x, r)
			if err != nil {
				panic(err)
			}
			return resp
		}})
	}
	return out
}

// Every action a notice lists is a command the game serves to players, whose
// address routing parses back into it.
func TestNoticeActionsAreServedCommands(t *testing.T) {
	for _, f := range noticeFixtures() {
		for _, a := range f.neutral.Actions {
			if !commands.FromPlayerCommand(a.Command) {
				t.Errorf("%s: action %s names %q, which the game does not serve to players", f.name, a.Key(), a.Command)
				continue
			}
			cmd, _, err := routing.ParseCallbackData(a.Address())
			if err != nil || cmd != a.Command {
				t.Errorf("%s: action %s address %q parses to %q (%v)", f.name, a.Key(), a.Address(), cmd, err)
			}
		}
	}
}

// What Telegram offers under a notice is a subset of the actions the core
// lists: the two presentations cannot disagree about where the player may go.
func TestTelegramNoticeButtonsAreNeutralActions(t *testing.T) {
	for _, f := range noticeFixtures() {
		neutral := map[string]bool{}
		for _, a := range f.neutral.Actions {
			neutral[a.Address()] = true
		}
		out := f.telegram(screens.Context{Msgs: keyTranslator{}, Lang: "fa"})
		if out.Keyboard == nil {
			continue
		}
		for _, row := range out.Keyboard.Rows {
			for _, b := range row {
				if b.CallbackData != "" && !neutral[b.CallbackData] {
					t.Errorf("%s: Telegram offers %q (%s) but the core lists no such action", f.name, b.CallbackData, strings.TrimSpace(b.Text))
				}
			}
		}
	}
}
