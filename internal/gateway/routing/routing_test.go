package routing

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

func textUpdate(text string) client.Update {
	return client.Update{
		UpdateID: 1,
		Message: &client.Message{
			MessageID: 2,
			From:      &client.User{ID: 3},
			Chat:      client.Chat{ID: 4, Type: "private"},
			Text:      text,
		},
	}
}

func callbackUpdate(data string) client.Update {
	return client.Update{
		UpdateID: 1,
		CallbackQuery: &client.CallbackQuery{
			ID:      "cbq",
			From:    client.User{ID: 3},
			Message: &client.Message{MessageID: 2, Chat: client.Chat{ID: 4, Type: "private"}},
			Data:    data,
		},
	}
}

// TestParse walks every accepted shape and every refusal, from one table, so
// that adding a shape means adding a row rather than a function.
func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		update      client.Update
		wantCommand string
		wantPayload map[string]any
		wantErr     error
	}{
		{
			name:        "domain action with one argument",
			update:      textUpdate("/job apply developer"),
			wantCommand: "job.apply",
			wantPayload: map[string]any{"role": "developer"},
		},
		{
			name:        "start maps to the profile screen",
			update:      textUpdate("/start"),
			wantCommand: "player.profile.get",
			wantPayload: map[string]any{},
		},
		{
			name:        "start carries a deep link payload as an argument",
			update:      textUpdate("/start ref_abc"),
			wantCommand: "player.profile.get",
			wantPayload: map[string]any{"args": []string{"ref_abc"}},
		},
		{
			name:        "callback data becomes a command",
			update:      callbackUpdate("market:buy:123"),
			wantCommand: "market.buy",
			wantPayload: map[string]any{"id": "123"},
		},
		{
			name:        "bot mention on the slash command is stripped",
			update:      textUpdate("/job@torncity_bot apply developer"),
			wantCommand: "job.apply",
			wantPayload: map[string]any{"role": "developer"},
		},
		{
			name:        "case and padding are normalised",
			update:      textUpdate("   /JOB   Apply   developer  "),
			wantCommand: "job.apply",
			wantPayload: map[string]any{"role": "developer"},
		},
		{
			name:        "unnamed arguments land under args",
			update:      textUpdate("/faction invite alice bob"),
			wantCommand: "faction.invite",
			wantPayload: map[string]any{"args": []string{"alice", "bob"}},
		},
		{
			name:        "arguments past the named ones land under args",
			update:      callbackUpdate("market:buy:123:extra"),
			wantCommand: "market.buy",
			wantPayload: map[string]any{"id": "123", "args": []string{"extra"}},
		},
		{
			name:        "a command with no arguments gets an empty payload",
			update:      textUpdate("/player profile"),
			wantCommand: "player.profile",
			wantPayload: map[string]any{},
		},

		{name: "plain text is not a command", update: textUpdate("hello"), wantErr: ErrNotACommand},
		{name: "empty text", update: textUpdate("   "), wantErr: ErrNoCommand},
		{name: "a lone slash", update: textUpdate("/"), wantErr: ErrMalformedCommand},
		{name: "domain without an action", update: textUpdate("/factory"), wantErr: ErrMissingAction},
		{name: "digits are not a subject token", update: textUpdate("/job apply2"), wantErr: ErrMalformedCommand},
		{name: "a dash is not a subject token", update: textUpdate("/job re-apply"), wantErr: ErrMalformedCommand},
		{name: "empty callback data", update: callbackUpdate(""), wantErr: ErrEmptyCallbackData},
		{name: "callback data without an action", update: callbackUpdate("market"), wantErr: ErrMissingAction},
		{name: "callback data with an empty segment", update: callbackUpdate("market::123"), wantErr: ErrMalformedCommand},
		{name: "callback data with a trailing colon", update: callbackUpdate("market:buy:"), wantErr: ErrMalformedCommand},
		{
			name:    "callback data with a space",
			update:  callbackUpdate("market:buy 123"),
			wantErr: ErrCallbackDataUnsafe,
		},
		{
			name:    "callback data with a non-ascii character",
			update:  callbackUpdate("market:buy:۱۲۳"),
			wantErr: ErrCallbackDataUnsafe,
		},
		{
			name:    "callback data over the 64-byte limit",
			update:  callbackUpdate("market:buy:" + strings.Repeat("9", MaxCallbackDataBytes)),
			wantErr: ErrCallbackDataTooLong,
		},
		{
			name:    "an edited message is not replayed as a command",
			update:  client.Update{EditedMessage: &client.Message{Text: "/job apply developer"}},
			wantErr: ErrNoCommand,
		},
		{name: "an update of an unhandled type", update: client.Update{UpdateID: 9}, wantErr: ErrNoCommand},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, payload, err := Parse(tt.update)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("Parse error = %v, want %v", err, tt.wantErr)
				}
				if command != "" || payload != nil {
					t.Errorf("Parse returned %q/%v alongside an error", command, payload)
				}
				return
			}

			if err != nil {
				t.Fatalf("Parse returned an unexpected error: %v", err)
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

// TestCallbackDataAtTheLimitIsAccepted pins the boundary: 64 bytes is legal,
// 65 is not. An off-by-one here would reject real buttons.
func TestCallbackDataAtTheLimitIsAccepted(t *testing.T) {
	prefix := "market:buy:"
	data := prefix + strings.Repeat("9", MaxCallbackDataBytes-len(prefix))
	if len(data) != MaxCallbackDataBytes {
		t.Fatalf("test builds %d bytes, want %d", len(data), MaxCallbackDataBytes)
	}

	if _, _, err := ParseCallbackData(data); err != nil {
		t.Errorf("ParseCallbackData rejected exactly %d bytes: %v", MaxCallbackDataBytes, err)
	}
	if _, _, err := ParseCallbackData(data + "9"); err != ErrCallbackDataTooLong {
		t.Errorf("ParseCallbackData(%d bytes) error = %v, want %v",
			MaxCallbackDataBytes+1, err, ErrCallbackDataTooLong)
	}
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantDomain string
		wantAction string
		wantErr    error
	}{
		{name: "two tokens", command: "job.apply", wantDomain: "job", wantAction: "apply"},
		{name: "multi level action", command: "player.profile.get", wantDomain: "player", wantAction: "profile.get"},
		{name: "underscores are legal", command: "company.hire_employee", wantDomain: "company", wantAction: "hire_employee"},
		{name: "empty", command: "", wantErr: ErrNoCommand},
		{name: "domain only", command: "job", wantErr: ErrMissingAction},
		{name: "empty segment", command: "job..apply", wantErr: ErrMalformedCommand},
		{name: "upper case", command: "Job.apply", wantErr: ErrMalformedCommand},
		{name: "digit", command: "job.apply2", wantErr: ErrMalformedCommand},
		{name: "space", command: "job.ap ply", wantErr: ErrMalformedCommand},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domain, action, err := SplitCommand(tt.command)
			if err != tt.wantErr {
				t.Fatalf("SplitCommand(%q) error = %v, want %v", tt.command, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if domain != tt.wantDomain || action != tt.wantAction {
				t.Errorf("SplitCommand(%q) = (%q, %q), want (%q, %q)",
					tt.command, domain, action, tt.wantDomain, tt.wantAction)
			}
		})
	}
}

// TestParsedCommandsBuildLegalSubjects is the reason SplitCommand validates
// tokens at all: whatever Parse accepts must survive subjects.Grammar. Without
// this, a bad token would only be noticed by NATS, far from its cause.
func TestParsedCommandsBuildLegalSubjects(t *testing.T) {
	updates := []client.Update{
		textUpdate("/job apply developer"),
		textUpdate("/start"),
		textUpdate("/company hire_employee alice"),
		callbackUpdate("market:buy:123"),
	}

	for _, update := range updates {
		command, _, err := Parse(update)
		if err != nil {
			t.Fatalf("Parse returned an error: %v", err)
		}
		domain, action, err := SplitCommand(command)
		if err != nil {
			t.Fatalf("SplitCommand(%q) returned an error: %v", command, err)
		}
		subject := subjects.Command(domain, action)
		if !subjects.Grammar.MatchString(subject) {
			t.Errorf("command %q produced subject %q, which subjects.Grammar rejects", command, subject)
		}
	}
}
