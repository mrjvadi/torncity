package routing

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// What players actually type, and what it must become. "/social mrjvadi" is
// the message that once got no reply at all: it was published as the command
// social.mrjvadi, which nothing consumes.
func TestRouteShortcuts(t *testing.T) {
	tests := []struct {
		text        string
		wantCommand string
		wantPayload map[string]any
	}{
		{"/social mrjvadi", "social.search", map[string]any{"query": "mrjvadi"}},
		{"/social Mrjvadi 2", "social.search", map[string]any{"query": "Mrjvadi", "page": "2"}},
		{"/social search ali", "social.search", map[string]any{"query": "ali"}},
		{"/social friend.list", "social.friend.list", map[string]any{}},
		{"/social", "social.friend.list", map[string]any{}},
		{"/map", "map.list", map[string]any{}},
		{"/map 2", "map.list", map[string]any{"page": "2"}},
		{"/map list 3", "map.list", map[string]any{"page": "3"}},
		{"/profile", "player.profile.get", map[string]any{}},
		{"/start", "player.profile.get", map[string]any{}},
		{"/start ref_abc", "player.profile.get", map[string]any{"args": []string{"ref_abc"}}},
		{"/settings", "player.settings", map[string]any{}},
		{"/skills", "skills.list", map[string]any{}},
		{"/settings@torncity_bot", "player.settings", map[string]any{}},
		{"/player language.set en", "player.language.set", map[string]any{"lang": "en"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			command, payload, err := Route(textUpdate(tt.text))
			if err != nil {
				t.Fatalf("Route(%q): %v", tt.text, err)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q", command, tt.wantCommand)
			}
			if !reflect.DeepEqual(payload, tt.wantPayload) {
				t.Errorf("payload = %#v, want %#v", payload, tt.wantPayload)
			}
		})
	}
}

// The settings buttons route like every other button.
func TestRouteSettingsButtons(t *testing.T) {
	command, payload, err := Route(callbackUpdate("player:language.set:en"))
	if err != nil || command != "player.language.set" || payload["lang"] != "en" {
		t.Fatalf("Route(player:language.set:en) = %q, %v, %v", command, payload, err)
	}
	if command, _, err := Route(callbackUpdate("player:settings")); err != nil || command != "player.settings" {
		t.Fatalf("Route(player:settings) = %q, %v", command, err)
	}
}

// Anything spelled like a command that the game does not serve to a player
// is refused, typed or pressed. travel.arrive is served — to the scheduler —
// and a player sending it is refused all the same.
func TestRouteRefusesWhatTheGameDoesNotServe(t *testing.T) {
	for _, tt := range []struct {
		name   string
		update client.Update
	}{
		{"unknown domain", textUpdate("/casino spin")},
		{"unknown action", textUpdate("/travel teleport berlin")},
		{"no shortcut words", textUpdate("/profile ada")},
		{"scheduler-only command", textUpdate("/travel arrive")},
		{"stale button", callbackUpdate("travel:cancel:abc")},
		{"future button", callbackUpdate("market:buy:123")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command, payload, err := Route(tt.update)
			if !errors.Is(err, ErrUnknownCommand) {
				t.Fatalf("Route = %q, %v, %v; want ErrUnknownCommand", command, payload, err)
			}
			if command != "" || payload != nil {
				t.Errorf("Route returned %q/%v alongside a refusal", command, payload)
			}
		})
	}
}

// A misspelling keeps its own error, so a log line says what was wrong.
func TestRouteKeepsParseErrors(t *testing.T) {
	if _, _, err := Route(textUpdate("/casino")); !errors.Is(err, ErrMissingAction) {
		t.Errorf("Route(/casino) = %v, want ErrMissingAction", err)
	}
	if _, _, err := Route(textUpdate("hello")); !errors.Is(err, ErrNotACommand) {
		t.Errorf("Route(hello) = %v, want ErrNotACommand", err)
	}
}

// Who gets an answer. Nothing that carries no command, and no plain text in a
// group; everything else a player sent deserves one.
func TestNeedsHelp(t *testing.T) {
	tests := []struct {
		err      error
		chatType string
		want     bool
	}{
		{nil, "private", false},
		{ErrNoCommand, "private", false},
		{ErrNotACommand, "private", true},
		{ErrNotACommand, "group", false},
		{ErrNotACommand, "supergroup", false},
		{ErrUnknownCommand, "private", true},
		{ErrUnknownCommand, "group", true},
		{ErrMissingAction, "private", true},
		{ErrMalformedCommand, "private", true},
		{ErrCallbackDataUnsafe, "private", true},
	}
	for _, tt := range tests {
		if got := NeedsHelp(tt.err, tt.chatType); got != tt.want {
			t.Errorf("NeedsHelp(%v, %q) = %v, want %v", tt.err, tt.chatType, got, tt.want)
		}
	}
}

// Every shortcut leads to a command the game serves to players, or it would
// route a player's message into a subject nobody reads.
func TestEveryShortcutIsServed(t *testing.T) {
	for head, s := range shortcuts {
		for _, command := range []string{s.Bare, s.Words} {
			if command != "" && !commands.FromPlayerCommand(command) {
				t.Errorf("/%s leads to %q, which the game does not serve to players", head, command)
			}
		}
	}
}

// Every command a player can send has its arguments named, so a typed command
// and a pressed button decode into the same payload.
func TestEveryPlayerCommandHasNamedArguments(t *testing.T) {
	for _, sub := range commands.All() {
		if sub.Origin != commands.FromPlayer {
			continue
		}
		if _, ok := argNames[sub.Command()]; !ok {
			t.Errorf("%s is served to players but has no argNames entry", sub.Command())
		}
	}
}
