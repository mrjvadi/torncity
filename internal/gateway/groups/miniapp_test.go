package groups

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func TestMiniAppLink(t *testing.T) {
	for _, tc := range []struct{ user, param, want string }{
		{"torn_bot", "found_abc123", "https://t.me/torn_bot?startapp=found_abc123"},
		{"@torn_bot", "found_abc123", "https://t.me/torn_bot?startapp=found_abc123"},
		{"", "found_abc", ""},
		{"torn_bot", "", ""},
		{"torn_bot", "has space", ""},
		{"torn_bot", "a?b=c&d", ""},
		{"torn_bot", string(make([]byte, 65)), ""},
	} {
		if got := MiniAppLink(tc.user, tc.param); got != tc.want {
			t.Errorf("MiniAppLink(%q, %q) = %q, want %q", tc.user, tc.param, got, tc.want)
		}
	}
	if got := MiniAppDeepLink("torn_bot"); got != "https://t.me/torn_bot?startapp=redirect" {
		t.Errorf("MiniAppDeepLink = %q", got)
	}
}

// A group's founding button names only a start parameter; the gateway, which
// knows the bot's username, makes it a `url` button - a `web_app` button is
// not allowed in a group.
func TestResolveMiniAppMakesAUrlButton(t *testing.T) {
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{
		{{Text: "open", MiniAppParam: "found_abc"}},
		{{Text: "plain", CallbackData: "x:y"}},
	}}
	out := ResolveMiniApp(kb, "torn_bot")
	if got := out.Rows[0][0]; got.URL != "https://t.me/torn_bot?startapp=found_abc" || got.MiniAppParam != "" || got.WebAppURL != "" {
		t.Errorf("resolved button = %+v", got)
	}
	if kb.Rows[0][0].URL != "" {
		t.Error("ResolveMiniApp changed its input")
	}
	if out.Rows[1][0].CallbackData != "x:y" {
		t.Error("an ordinary button was touched")
	}

	// Without a username there is no link: the button is dropped, and a row
	// left empty goes with it, rather than sending a button that does nothing.
	out = ResolveMiniApp(kb, "")
	if len(out.Rows) != 1 || out.Rows[0][0].Text != "plain" {
		t.Errorf("unresolvable button was kept: %+v", out.Rows)
	}
}

// A button that does nothing would make Telegram refuse the whole keyboard.
func TestMarkupSkipsButtonsThatDoNothing(t *testing.T) {
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{
		{Text: "unresolved", MiniAppParam: "found_abc"},
		{Text: "kept", CallbackData: "x:y"},
	}}}
	raw, err := json.Marshal(Markup(kb))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "unresolved") || !strings.Contains(string(raw), "kept") {
		t.Errorf("markup = %s", raw)
	}
}
