package groups

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TestButtonStyleFollowsKind pins the one mapping configs/actions.yml's kind
// drives Telegram's button colour by (style.go): primary, confirm and danger
// get a colour; every other kind, and a command the resolver knows nothing
// about, gets none.
func TestButtonStyleFollowsKind(t *testing.T) {
	SetKindResolver(func(command string) string {
		switch command {
		case "bank.deposit":
			return "primary"
		case "gov.confirm":
			return "confirm"
		case "job.quit":
			return "danger"
		case "map.list":
			return "navigation"
		case "player.settings":
			return "secondary"
		}
		return ""
	})
	t.Cleanup(func() { SetKindResolver(func(string) string { return "" }) })

	for command, want := range map[string]string{
		"bank.deposit":    "primary",
		"gov.confirm":     "success",
		"job.quit":        "danger",
		"map.list":        "",
		"player.settings": "",
		"unknown.command": "",
		"":                "",
	} {
		if got := buttonStyle(command); got != want {
			t.Errorf("buttonStyle(%q) = %q, want %q", command, got, want)
		}
	}
}

// TestMarkupColoursButtonsFromCallbackData proves the colour is read off the
// button's own callback data end to end through Markup, and that a URL
// button (never a game command) is never coloured even when its text is
// indistinguishable from a callback button's.
func TestMarkupColoursButtonsFromCallbackData(t *testing.T) {
	SetKindResolver(func(command string) string {
		if command == "bank.deposit" {
			return "primary"
		}
		return ""
	})
	t.Cleanup(func() { SetKindResolver(func(string) string { return "" }) })

	kb := &presenter.Keyboard{Rows: [][]presenter.Button{
		{{Text: "Deposit", CallbackData: "bank:deposit:1000"}},
		{{Text: "Back", CallbackData: "player:profile.get"}},
		{{Text: "Open", URL: "https://t.me/example"}},
	}}
	raw, err := json.Marshal(Markup(kb))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"callback_data":"bank:deposit:1000","style":"primary"`) {
		t.Errorf("the deposit button is not styled primary: %s", got)
	}
	if strings.Contains(got, `"callback_data":"player:profile.get","style"`) {
		t.Errorf("an unstyled button carries a style field: %s", got)
	}
	if strings.Contains(got, `"url":"https://t.me/example","style"`) {
		t.Errorf("a URL button carries a style field: %s", got)
	}
}
