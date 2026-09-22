package subjects

import "testing"

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
