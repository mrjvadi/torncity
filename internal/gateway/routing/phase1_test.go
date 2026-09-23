package routing

import (
	"reflect"
	"testing"
)

// The phase 1 commands, as a player reaches them: typed as a slash command or
// pressed as a button. Both forms have to produce the same command name and
// the same payload keys, because the game core decodes one shape.
func TestPhase1CommandsRoute(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		callback    string
		wantCommand string
		wantPayload map[string]any
	}{
		{
			name:        "travel to a city",
			text:        "/travel start berlin",
			callback:    "travel:start:berlin",
			wantCommand: "travel.start",
			wantPayload: map[string]any{"city": "berlin"},
		},
		{
			name:        "the choice of transport to a city",
			text:        "/travel options berlin",
			callback:    "travel:options:berlin",
			wantCommand: "travel.options",
			wantPayload: map[string]any{"city": "berlin"},
		},
		{
			name:        "travel by a chosen mode at an accepted fare",
			text:        "/travel start berlin train 1200",
			callback:    "travel:start:berlin:train:1200",
			wantCommand: "travel.start",
			wantPayload: map[string]any{"city": "berlin", "mode": "train", "max": "1200"},
		},
		{
			name:        "travel status",
			text:        "/travel status",
			callback:    "travel:status",
			wantCommand: "travel.status",
			wantPayload: map[string]any{},
		},
		{
			name:        "skills",
			text:        "/skills list 2",
			callback:    "skills:list:2",
			wantCommand: "skills.list",
			wantPayload: map[string]any{"page": "2"},
		},
		{
			name:        "search",
			text:        "/social search ali",
			callback:    "social:search:ali",
			wantCommand: "social.search",
			wantPayload: map[string]any{"query": "ali"},
		},
		{
			// A search has no page: every word after the command is the
			// query, so an old "next page" button arrives as one query the
			// game refuses, rather than as a page of a search it no longer
			// runs.
			name:        "search words are one query",
			text:        "/social search ali 2",
			callback:    "social:search:ali:2",
			wantCommand: "social.search",
			wantPayload: map[string]any{"query": "ali 2"},
		},
		{
			name:        "search by code",
			text:        "/social search K7Q2M9A",
			callback:    "social:search:K7Q2M9A",
			wantCommand: "social.search",
			wantPayload: map[string]any{"query": "K7Q2M9A"},
		},
		{
			name:        "add a friend",
			text:        "/social friend.add 9f1c-22",
			callback:    "social:friend.add:9f1c-22",
			wantCommand: "social.friend.add",
			wantPayload: map[string]any{"player": "9f1c-22"},
		},
		{
			name:        "accept a friend",
			text:        "/social friend.accept 9f1c-22",
			callback:    "social:friend.accept:9f1c-22",
			wantCommand: "social.friend.accept",
			wantPayload: map[string]any{"player": "9f1c-22"},
		},
		{
			name:        "the friend list",
			text:        "/social friend.list 3",
			callback:    "social:friend.list:3",
			wantCommand: "social.friend.list",
			wantPayload: map[string]any{"page": "3"},
		},
		{
			name:        "the map",
			text:        "/map list 2",
			callback:    "map:list:2",
			wantCommand: "map.list",
			wantPayload: map[string]any{"page": "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" (typed)", func(t *testing.T) {
			command, payload, err := ParseText(tt.text)
			if err != nil {
				t.Fatalf("ParseText(%q): %v", tt.text, err)
			}
			if command != tt.wantCommand {
				t.Errorf("command %q, want %q", command, tt.wantCommand)
			}
			if !reflect.DeepEqual(payload, tt.wantPayload) {
				t.Errorf("payload %#v, want %#v", payload, tt.wantPayload)
			}
		})

		t.Run(tt.name+" (pressed)", func(t *testing.T) {
			command, payload, err := ParseCallbackData(tt.callback)
			if err != nil {
				t.Fatalf("ParseCallbackData(%q): %v", tt.callback, err)
			}
			if command != tt.wantCommand {
				t.Errorf("command %q, want %q", command, tt.wantCommand)
			}
			if !reflect.DeepEqual(payload, tt.wantPayload) {
				t.Errorf("payload %#v, want %#v", payload, tt.wantPayload)
			}
		})
	}
}

// SplitCommand has to handle the two-token actions the social graph uses:
// the domain is one token, the action may be several.
func TestSplitCommandHandlesPhase1(t *testing.T) {
	tests := []struct {
		command    string
		wantDomain string
		wantAction string
	}{
		{"travel.start", "travel", "start"},
		{"travel.options", "travel", "options"},
		{"travel.status", "travel", "status"},
		{"skills.list", "skills", "list"},
		{"social.search", "social", "search"},
		{"social.friend.add", "social", "friend.add"},
		{"social.friend.accept", "social", "friend.accept"},
		{"social.friend.list", "social", "friend.list"},
		{"map.list", "map", "list"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			domain, action, err := SplitCommand(tt.command)
			if err != nil {
				t.Fatalf("SplitCommand(%q): %v", tt.command, err)
			}
			if domain != tt.wantDomain || action != tt.wantAction {
				t.Errorf("got (%q, %q), want (%q, %q)", domain, action, tt.wantDomain, tt.wantAction)
			}
		})
	}
}

// Every address the keyboards package emits for a phase 1 screen has to
// survive this parser, budget and byte set included. The strings are written
// out here rather than imported so that a change on either side of the
// boundary shows up as a failing test instead of as a silent agreement.
func TestPhase1CallbackAddressesFitTheBudget(t *testing.T) {
	addresses := []string{
		"player:profile.get",
		"map:list",
		"map:list:9",
		"travel:start:tehran",
		"travel:status",
		"skills:list",
		"social:search:K7Q2M9A",
		"social:friend.add:0f8c6a1e-4b2d-4c9f-9a1b-2d3e4f506172",
		"social:friend.accept:0f8c6a1e-4b2d-4c9f-9a1b-2d3e4f506172",
		"social:friend.list:3",
	}
	for _, data := range addresses {
		t.Run(data, func(t *testing.T) {
			if len(data) > MaxCallbackDataBytes {
				t.Fatalf("%q is %d bytes, over the %d-byte limit", data, len(data), MaxCallbackDataBytes)
			}
			if _, _, err := ParseCallbackData(data); err != nil {
				t.Errorf("the gateway rejects an address our own keyboards emit: %v", err)
			}
		})
	}
}

// A command that is not in argNames still PARSES: argNames only names
// arguments. Whether the game serves the command is Route's question, and it
// is answered from internal/commands, not from this table; travel.cancel is
// not served, so Route refuses it (see TestRouteRefusesWhatTheGameDoesNotServe).
func TestUnlistedPhase1CommandStillParses(t *testing.T) {
	command, payload, err := ParseCallbackData("travel:cancel:abc")
	if err != nil {
		t.Fatalf("ParseCallbackData: %v", err)
	}
	if command != "travel.cancel" {
		t.Errorf("command %q, want travel.cancel", command)
	}
	args, ok := payload["args"].([]string)
	if !ok || len(args) != 1 || args[0] != "abc" {
		t.Errorf("unnamed arguments landed as %#v, want []string{\"abc\"} under \"args\"", payload)
	}
}
