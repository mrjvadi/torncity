package groups

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The Persian screen a group sees, read from the real catalogue: the Mini App
// link, «📩 ارسال در پیوی من» under it, and the texts the private chat and the
// toast use.
func TestWebAppPersianSnapshot(t *testing.T) {
	catalog, err := i18n.Load("../../../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(catalog, set)
	kb := r.ForWebApp(&presenter.Keyboard{Rows: [][]presenter.Button{{{Text: "🌐 بازی وب", WebAppURL: "https://app.example/play"}}}}, "torn_bot", "fa")
	var got []string
	for _, row := range kb.Rows {
		for _, b := range row {
			got = append(got, b.Text+" | "+b.URL+b.CallbackData)
		}
	}
	want := []string{
		"🌐 بازی وب | https://t.me/torn_bot?startapp=redirect",
		"📩 ارسال در پیوی من | wapp:_",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("group buttons =\n%s", strings.Join(got, "\n"))
	}
	for _, key := range []string{KeyWebAppSent, KeyWebAppStartBot, KeyWebAppSlowDown, KeyWebAppUnavailable, KeyWebAppText, KeyWebAppTextFound, KeyWebAppOpen} {
		for _, lang := range []string{"fa", "en"} {
			if !catalog.Has(lang, key) {
				t.Errorf("%s missing in %s", key, lang)
			}
		}
		if n := utf8.RuneCountInString(catalog.T("fa", key, nil)); n > 200 {
			t.Errorf("%s is %d runes, a callback alert takes 200", key, n)
		}
	}
}

func webAppScreen() *presenter.Keyboard {
	return &presenter.Keyboard{Rows: [][]presenter.Button{
		{{Text: "play", WebAppURL: "https://app.example/play"}},
		{{Text: "found", MiniAppParam: "found_abc"}},
		{{Text: "plain", CallbackData: "x:y"}},
	}}
}

// In a group a raw web_app button is never emitted: it becomes the Mini App
// link, with «send to my private chat» right under it.
func TestForWebAppGroupDecision(t *testing.T) {
	out := newRenderer().ForWebApp(webAppScreen(), "torn_bot", "en")
	want := []struct{ url, cb string }{
		{"https://t.me/torn_bot?startapp=redirect", ""},
		{"", "wapp:_"},
		{"https://t.me/torn_bot?startapp=found_abc", ""},
		{"", "wapp:found_abc"},
		{"", "x:y"},
	}
	if len(out.Rows) != len(want) {
		t.Fatalf("rows = %+v", out.Rows)
	}
	for i, w := range want {
		b := out.Rows[i][0]
		if b.URL != w.url || b.CallbackData != w.cb || b.WebAppURL != "" || b.MiniAppParam != "" {
			t.Errorf("row %d = %+v, want %+v", i, b, w)
		}
	}
	if out.Rows[1][0].Text != KeySendPrivately {
		t.Errorf("send button text = %q", out.Rows[1][0].Text)
	}
	raw, _ := json.Marshal(Markup(out))
	if strings.Contains(string(raw), "web_app") {
		t.Errorf("group markup carries a web_app button: %s", raw)
	}
	// Without a username there is no link, but the private route stays.
	out = newRenderer().ForWebApp(webAppScreen(), "", "en")
	if len(out.Rows) != 3 || out.Rows[0][0].CallbackData != "wapp:_" {
		t.Errorf("no-username rows = %+v", out.Rows)
	}
	// No web-app button: untouched.
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{{Text: "a", CallbackData: "x:y"}}}}
	if newRenderer().ForWebApp(kb, "torn_bot", "en") != kb {
		t.Error("a keyboard without web-app buttons was rebuilt")
	}
}

// A group screen through Render carries the two buttons.
func TestRenderPublicWebApp(t *testing.T) {
	api := &fakeAPI{}
	resp := &presenter.Response{Type: presenter.ActionSendMessage, Text: "friends", Keyboard: webAppScreen()}
	if _, err := newRenderer().Render(t.Context(), api, bot, groupMeta("friend.list"), resp); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(api.recorded()[0].markup)
	s := string(raw)
	if strings.Contains(s, "web_app") || !strings.Contains(s, "startapp=redirect") || !strings.Contains(s, "wapp:_") {
		t.Errorf("markup = %s", s)
	}
}

func TestParseWebAppCallback(t *testing.T) {
	for data, want := range map[string]string{"wapp:_": "_", "wapp:found_abc": "found_abc"} {
		if got, ok := ParseWebAppCallback(data); !ok || got != want {
			t.Errorf("%q = %q, %v", data, got, ok)
		}
	}
	for _, data := range []string{"wapp:", "wapp:a b", "wapp:a:b", "other:x", "wapp"} {
		if _, ok := ParseWebAppCallback(data); ok {
			t.Errorf("%q parsed", data)
		}
	}
}

func TestPrivateWebAppURL(t *testing.T) {
	if got := PrivateWebAppURL("https://app.example/play", "_"); got != "https://app.example/play" {
		t.Errorf("plain = %q", got)
	}
	if got := PrivateWebAppURL("https://app.example/play", "found_abc"); got != "https://app.example/play?tgWebAppStartParam=found_abc" {
		t.Errorf("param = %q", got)
	}
	if PrivateWebAppURL("", "x") != "" {
		t.Error("no base must give no URL")
	}
}

func TestSendWebApp(t *testing.T) {
	const base = "https://app.example/play"
	meta := withCallback(groupMeta(""))

	t.Run("sent", func(t *testing.T) {
		api := &fakeAPI{}
		if err := newRenderer().SendWebApp(t.Context(), api, bot, meta, "found_abc", base, true); err != nil {
			t.Fatal(err)
		}
		calls := api.recorded()
		if len(calls) != 2 || calls[0].chatID != player || calls[0].text != KeyWebAppTextFound {
			t.Fatalf("calls = %+v", calls)
		}
		raw, _ := json.Marshal(calls[0].markup)
		if !strings.Contains(string(raw), `"web_app":{"url":"https://app.example/play?tgWebAppStartParam=found_abc"}`) {
			t.Errorf("private markup = %s", raw)
		}
		if a := calls[1].answer; a.Text != KeyWebAppSent || a.ShowAlert || a.CallbackQueryID != "cbq-1" {
			t.Errorf("answer = %+v", a)
		}
	})

	t.Run("never started the bot", func(t *testing.T) {
		api := &fakeAPI{onSend: func(int64) (*client.Message, error) {
			return nil, &client.APIError{Code: 403, Description: "Forbidden: bot can't initiate conversation with a user"}
		}}
		if err := newRenderer().SendWebApp(t.Context(), api, bot, meta, "_", base, true); err != nil {
			t.Fatal(err)
		}
		a := api.recorded()[1].answer
		if !a.ShowAlert || a.Text != KeyWebAppStartBot || a.URL != "https://t.me/torn_bot" {
			t.Errorf("answer = %+v", a)
		}
	})

	t.Run("rate limited", func(t *testing.T) {
		api := &fakeAPI{}
		if err := newRenderer().SendWebApp(t.Context(), api, bot, meta, "_", base, false); err != nil {
			t.Fatal(err)
		}
		calls := api.recorded()
		if len(calls) != 1 || calls[0].method != "answerCallbackQuery" || calls[0].answer.Text != KeyWebAppSlowDown {
			t.Errorf("calls = %+v", calls)
		}
	})

	t.Run("not configured", func(t *testing.T) {
		api := &fakeAPI{}
		_ = newRenderer().SendWebApp(t.Context(), api, bot, meta, "_", "", true)
		calls := api.recorded()
		if len(calls) != 1 || !calls[0].answer.ShowAlert || calls[0].answer.Text != KeyWebAppUnavailable {
			t.Errorf("calls = %+v", calls)
		}
	})

	t.Run("other failure is reported and answered", func(t *testing.T) {
		boom := errors.New("boom")
		api := &fakeAPI{onSend: func(int64) (*client.Message, error) { return nil, boom }}
		if err := newRenderer().SendWebApp(t.Context(), api, bot, meta, "_", base, true); !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
		if calls := api.recorded(); len(calls) != 2 || calls[1].answer.Text != KeyWebAppUnavailable {
			t.Errorf("calls = %+v", calls)
		}
	})
}
