package subjects

import (
	"strings"
	"testing"
)

func TestSubjectGrammar(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"command", Command("player", "profile.get"), "game.command.player.profile.get.v1"},
		{"command single action", Command("market", "buy"), "game.command.market.buy.v1"},
		{"event", Event("player", "created"), "game.event.player.created.v1"},
		{"event underscore", Event("market", "price_changed"), "game.event.market.price_changed.v1"},
		{"response", Response("req_9f1c0a73b5e24d8a"), "game.response.req_9f1c0a73b5e24d8a.v1"},
		{"notify", Notify("0b6c2f0e-4c1a-4e7b-9a53-1f0d2c3b4a59"), "game.notify.0b6c2f0e-4c1a-4e7b-9a53-1f0d2c3b4a59.v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
			if !Grammar.MatchString(tt.got) {
				t.Errorf("%q does not match the section 10 grammar", tt.got)
			}
		})
	}
}

func TestResponseCarriesRequestID(t *testing.T) {
	got := Response("req_abc123")
	want := "game.response.req_abc123.v1"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The grammar keeps the two kinds of subject apart: a named subject may not
// carry an identifier's digits, and an addressed one is exactly one token.
func TestGrammarRejects(t *testing.T) {
	for _, subject := range []string{
		"game.command.player2.get.v1",             // a digit in a name
		"game.event.Player.created.v1",            // upper case in a name
		"game.notify.a.b.v1",                      // an id split by a dot
		"game.notify..v1",                         // no id at all
		"game.notify.p1",                          // no version
		"game.response.*.v1",                      // a wildcard is not a subject
		"game.notify.p1.v1.extra",                 // trailing tokens
		"game.whisper.p1.v1",                      // an unknown kind
		"other.notify.p1.v1",                      // an unknown root
		"game.notify.p1.version1",                 // a malformed version
		Notify("p1") + "." + Version,              // a version twice
		strings.Replace(Notify("p1"), ".", "", 1), // a missing separator
	} {
		if Grammar.MatchString(subject) {
			t.Errorf("%q matches the grammar and should not", subject)
		}
	}
}

// NotifyAll must cover exactly the subjects Notify builds: one token in the
// player position, at this version.
func TestNotifyAllCoversNotify(t *testing.T) {
	if NotifyAll != "game.notify.*.v1" {
		t.Fatalf("NotifyAll = %q", NotifyAll)
	}
	want := strings.Split(NotifyAll, ".")
	got := strings.Split(Notify("0b6c2f0e-4c1a-4e7b-9a53-1f0d2c3b4a59"), ".")
	if len(got) != len(want) {
		t.Fatalf("Notify has %d tokens, NotifyAll has %d", len(got), len(want))
	}
	for i := range want {
		if want[i] != "*" && want[i] != got[i] {
			t.Errorf("token %d: Notify has %q, NotifyAll has %q", i, got[i], want[i])
		}
	}
}
