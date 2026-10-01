package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/notices"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// noticePair joins a neutral view to the Telegram view its wording is drawn
// from: the two carry the same facts under the same field names.
type noticePair struct {
	screen   string
	neutral  any
	telegram any
}

func noticePairs() []noticePair {
	return []noticePair{
		{notices.ScreenPaymentNotice, notices.PaymentView{}, screens.PaymentNoticeView{}},
		{notices.ScreenAchievementNotice, notices.AchievementView{}, screens.AchievementNoticeView{}},
		{notices.ScreenOfficeNotice, notices.OfficeView{}, screens.OfficeNoticeView{}},
		{notices.ScreenAuctionNotice, notices.AuctionView{}, screens.AuctionNoticeView{}},
		{notices.ScreenVictimNotice, notices.VictimView{}, screens.VictimNoticeView{}},
		{notices.ScreenCaseSolvedNotice, notices.CaseOutcomeView{}, screens.CaseOutcomeView{}},
		{notices.ScreenTreatyProposedNotice, notices.TreatyView{}, screens.TreatyNoticeView{}},
		{notices.ScreenElectionResultNotice, notices.ElectionResultView{}, screens.ElectionResultView{}},
		{notices.ScreenFactionRequestNotice, notices.FactionRequestView{}, screens.FactionRequestNoticeView{}},
		{notices.ScreenFactionAnswerNotice, notices.FactionAnswerView{}, screens.FactionAnsweredView{}},
		{notices.ScreenFactionCrimeNotice, notices.FactionCrimeView{}, screens.FactionCrimeNoticeView{}},
		{notices.ScreenFinanceNotice, notices.FinanceView{}, screens.FinanceNoticeView{}},
		{notices.ScreenHospitalisedNotice, notices.HospitalisedView{}, screens.HospitalisedNoticeView{}},
		{notices.ScreenClinicTreatedNotice, notices.ClinicTreatedView{}, screens.ClinicTreatedNoticeView{}},
		{notices.ScreenBillDecidedNotice, notices.BillDecidedView{}, screens.BillView{}},
		{notices.ScreenRankNotice, notices.RankView{}, screens.RankNoticeView{}},
		{notices.ScreenMarketFilledNotice, notices.MarketFilledView{}, screens.MarketFilledView{}},
		{notices.ScreenMissionCompleted, notices.MissionCompletedView{}, screens.MissionCompletedView{}},
		{notices.ScreenPropertyNotice, notices.PropertyView{}, screens.PropertyNoticeView{}},
		{notices.ScreenRecruitNotice, notices.RecruitView{}, screens.RecruitNoticeView{}},
		{notices.ScreenStockNotice, notices.StockView{}, screens.StockNoticeView{}},
		{notices.ScreenVillageNews, notices.VillageNewsView{}, screens.VillageNewsView{}},
		{notices.ScreenInboxHub, notices.InboxHubView{}, screens.InboxHubView{}},
		{notices.ScreenInboxReminder, notices.InboxReminderView{}, screens.InboxReminderView{}},
		{village.ScreenFoundingChecked, village.FoundingCheckedView{}, screens.FoundingCheckedView{}},
		{village.ScreenFoundingRefusal, village.FoundingRefusalView{}, screens.FoundingRefusalView{}},
		{village.ScreenSettlementRefus, village.SettlementRefusalView{}, screens.SettlementRefusalView{}},
	}
}

// subset reports whether every key of a (recursively) is in b with an equal
// value, naming the first path that is not.
func subset(a, b any, path string) string {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return path + " is not an object in the Telegram view"
		}
		for k, x := range av {
			y, found := bv[k]
			if !found {
				return path + "." + k + " is missing from the Telegram view"
			}
			if why := subset(x, y, path+"."+k); why != "" {
				return why
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok || len(bv) != len(av) {
			return path + " differs in length"
		}
		for i := range av {
			if why := subset(av[i], bv[i], path+"[]"); why != "" {
				return why
			}
		}
	default:
		if !reflect.DeepEqual(a, b) {
			return path + " differs"
		}
	}
	return ""
}

// TestNoticeViewsMatchTheTelegramViews is the contract between a notice's
// neutral view and the Telegram view its wording is drawn from: a fully
// populated neutral view, converted, loses no fact. A field added to one side
// and not the other fails here, not in a player's chat.
func TestNoticeViewsMatchTheTelegramViews(t *testing.T) {
	for _, p := range noticePairs() {
		p := p
		t.Run(p.screen, func(t *testing.T) {
			filled := fill(reflect.TypeOf(p.neutral), 0).Interface()
			raw, err := presentation.EncodeView(filled)
			if err != nil {
				t.Fatal(err)
			}
			tv := reflect.New(reflect.TypeOf(p.telegram))
			if err := presentation.DecodeView(raw, tv.Interface()); err != nil {
				t.Fatal(err)
			}
			back, err := presentation.EncodeView(tv.Elem().Interface())
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			_ = json.Unmarshal(raw, &want)
			_ = json.Unmarshal(back, &got)
			if why := subset(want, got, ""); why != "" {
				t.Errorf("the neutral view of %s does not fit the Telegram view: %s", p.screen, why)
			}
		})
	}
}

// What Telegram needs beyond a neutral view, it words from its own locale
// layer: every emblem choice the content offers has its emoji there, equal to
// the one the content authors.
func TestTelegramHasTheEmblemEmoji(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "configs", "content", "founding.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Founding struct {
			Shapes  []struct{ Code, Emoji string } `yaml:"shapes"`
			Palette []struct{ Code, Emoji string } `yaml:"palette"`
			Icons   []struct{ Code, Emoji string } `yaml:"icons"`
		} `yaml:"founding"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	msgs, err := i18n.Load(filepath.Join("..", "..", "..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"fa", "en"} {
		c := screens.Context{Msgs: msgs, Lang: lang}
		check := func(kind string, list []struct{ Code, Emoji string }) {
			if len(list) == 0 {
				t.Errorf("%s: the content lists no %s", lang, kind)
			}
			for _, e := range list {
				if got := c.FoundingEmoji(kind, e.Code); got != e.Emoji {
					t.Errorf("%s: founding.emoji.%s.%s = %q, the content says %q", lang, kind, e.Code, got, e.Emoji)
				}
			}
		}
		check("shape", doc.Founding.Shapes)
		check("color", doc.Founding.Palette)
		check("icon", doc.Founding.Icons)
	}
}

// A stored notice is worded by the renderer of its own screen, in the
// reader's language, and a notice of a screen not carried as data shows the
// text its producer wrote.
func TestInboxWordsStoredNoticesFromTheirScreens(t *testing.T) {
	view, err := presentation.EncodeView(notices.PaymentView{PayerName: "Ada", Method: "card", Amount: 12500})
	if err != nil {
		t.Fatal(err)
	}
	r := notices.InboxCategory(presentation.Ctx{Lang: "en"}, notices.InboxCategoryView{
		Category: "finance", Page: 1, TotalPages: 1,
		Items: []notices.InboxItemLine{
			{Kind: "bank.payment_received", Notice: notices.StoredNotice{Screen: notices.ScreenPaymentNotice, View: view}},
			{Kind: "travel.completed", Notice: notices.StoredNotice{Text: "You arrived in Calderis."}},
		},
	})
	out, err := Render(keyTranslator{}, Delivery{}, r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "pay.received_card") || !strings.Contains(out.Text, "Ada") {
		t.Errorf("the stored payment notice is not worded from its screen: %s", out.Text)
	}
	if !strings.Contains(out.Text, "You arrived in Calderis.") {
		t.Errorf("a notice of a screen not carried as data lost its text: %s", out.Text)
	}
}
