package context

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// receivedAt is a fixed instant, deliberately in a non-UTC zone, so the tests
// can prove Build normalises it instead of passing the caller's zone through.
var receivedAt = time.Date(2026, 9, 22, 12, 0, 0, 0, time.FixedZone("Tehran", 3*3600+1800))

func messageUpdate() client.Update {
	return client.Update{
		UpdateID: 4711,
		Message: &client.Message{
			MessageID: 1234,
			From: &client.User{
				ID:           123456789,
				FirstName:    "Ali",
				LastName:     "Rezaei",
				Username:     "ali",
				LanguageCode: "fa-IR",
			},
			Chat: client.Chat{ID: -100123456789, Type: "supergroup", Title: "Torn City"},
			Date: receivedAt.Unix(),
			Text: "/job apply developer",
		},
	}
}

func callbackUpdate() client.Update {
	return client.Update{
		UpdateID: 4712,
		CallbackQuery: &client.CallbackQuery{
			ID:   "cbq-987",
			From: client.User{ID: 123456789, FirstName: "Ali", LanguageCode: "en"},
			Message: &client.Message{
				MessageID: 1234,
				Chat:      client.Chat{ID: 555, Type: "private"},
			},
			Data: "market:buy:123",
		},
	}
}

// TestBuildMessageUpdate checks every field section 6 asks for on the most
// common update there is.
func TestBuildMessageUpdate(t *testing.T) {
	meta, err := Build(messageUpdate(), "bot07", "gateway-02", receivedAt)
	if err != nil {
		t.Fatalf("Build returned an error: %v", err)
	}

	if meta.UpdateType != UpdateTypeMessage {
		t.Errorf("UpdateType = %q, want %q", meta.UpdateType, UpdateTypeMessage)
	}
	if meta.TelegramUserID != 123456789 {
		t.Errorf("TelegramUserID = %d, want 123456789", meta.TelegramUserID)
	}
	if meta.TelegramChatID != -100123456789 {
		t.Errorf("TelegramChatID = %d, want -100123456789", meta.TelegramChatID)
	}
	if meta.TelegramMessageID != 1234 {
		t.Errorf("TelegramMessageID = %d, want 1234", meta.TelegramMessageID)
	}
	if meta.ChatType != "supergroup" {
		t.Errorf("ChatType = %q, want %q", meta.ChatType, "supergroup")
	}
	if meta.BotID != "bot07" || meta.GatewayInstanceID != "gateway-02" {
		t.Errorf("BotID/GatewayInstanceID = %q/%q, want bot07/gateway-02", meta.BotID, meta.GatewayInstanceID)
	}
	if meta.Language != "fa" {
		t.Errorf("Language = %q, want %q (fa-IR must collapse to its primary subtag)", meta.Language, "fa")
	}
	if meta.CallbackQueryID != nil {
		t.Errorf("CallbackQueryID = %v, want nil on a message update", *meta.CallbackQueryID)
	}
	if meta.SchemaVersion != envelope.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", meta.SchemaVersion, envelope.SchemaVersion)
	}
	if !meta.ReceivedAt.Equal(receivedAt) {
		t.Errorf("ReceivedAt = %s, want the instant that was passed in", meta.ReceivedAt)
	}
	if meta.ReceivedAt.Location() != time.UTC {
		t.Errorf("ReceivedAt location = %s, want UTC", meta.ReceivedAt.Location())
	}

	// Command is routing's job. Proving it is still empty here is what keeps
	// the two responsibilities from drifting into one another.
	if meta.Command != "" || meta.Action != "" || meta.PlayerID != "" {
		t.Errorf("Build filled fields it does not own: command=%q action=%q player=%q",
			meta.Command, meta.Action, meta.PlayerID)
	}
	// ... and the envelope must refuse it until routing has done that job.
	if err := meta.Validate(); err != envelope.ErrNoCommand {
		t.Errorf("Validate() = %v, want %v before routing sets the command", err, envelope.ErrNoCommand)
	}
}

// TestBuildCallbackUpdate covers the other half of phase 0's traffic.
func TestBuildCallbackUpdate(t *testing.T) {
	meta, err := Build(callbackUpdate(), "bot01", "gateway-01", receivedAt)
	if err != nil {
		t.Fatalf("Build returned an error: %v", err)
	}

	if meta.UpdateType != UpdateTypeCallbackQuery {
		t.Errorf("UpdateType = %q, want %q", meta.UpdateType, UpdateTypeCallbackQuery)
	}
	if meta.CallbackQueryID == nil {
		t.Fatal("CallbackQueryID is nil; the gateway could never answer the button press")
	}
	if *meta.CallbackQueryID != "cbq-987" {
		t.Errorf("CallbackQueryID = %q, want %q", *meta.CallbackQueryID, "cbq-987")
	}
	if meta.TelegramChatID != 555 || meta.TelegramMessageID != 1234 {
		t.Errorf("chat/message = %d/%d, want 555/1234 (the screen to edit in place)",
			meta.TelegramChatID, meta.TelegramMessageID)
	}
	if meta.ChatType != "private" {
		t.Errorf("ChatType = %q, want %q", meta.ChatType, "private")
	}
	if meta.Language != "en" {
		t.Errorf("Language = %q, want %q", meta.Language, "en")
	}
}

// TestRequestIDsDiffer is the point of minting the identifiers with
// crypto/rand: two updates, even identical ones, are two requests.
func TestRequestIDsDiffer(t *testing.T) {
	first, err := Build(messageUpdate(), "bot07", "gateway-02", receivedAt)
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	second, err := Build(messageUpdate(), "bot07", "gateway-02", receivedAt)
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}

	if first.RequestID == second.RequestID {
		t.Errorf("two Build calls produced the same request id %q", first.RequestID)
	}
	if first.TraceID == second.TraceID {
		t.Errorf("two Build calls produced the same trace id %q", first.TraceID)
	}
	if first.RequestID == first.TraceID {
		t.Errorf("request id and trace id are identical (%q); they must be independent", first.RequestID)
	}

	for _, tc := range []struct {
		name, id, prefix string
	}{
		{"request", first.RequestID, RequestIDPrefix},
		{"trace", first.TraceID, TraceIDPrefix},
	} {
		if !strings.HasPrefix(tc.id, tc.prefix) {
			t.Errorf("%s id %q does not start with %q", tc.name, tc.id, tc.prefix)
		}
		if want := len(tc.prefix) + 2*idRandomBytes; len(tc.id) != want {
			t.Errorf("%s id %q has length %d, want %d", tc.name, tc.id, len(tc.id), want)
		}
	}
}

// TestBuildRejects covers every refusal path, so none of them can turn into a
// panic or, worse, into a context with a zero user id.
func TestBuildRejects(t *testing.T) {
	channelPost := client.Update{Message: &client.Message{MessageID: 1, Chat: client.Chat{ID: 9, Type: "channel"}}}
	staleCallback := client.Update{CallbackQuery: &client.CallbackQuery{ID: "x", From: client.User{ID: 7}}}

	tests := []struct {
		name      string
		update    client.Update
		botID     string
		gatewayID string
		want      error
	}{
		{"no bot id", messageUpdate(), "   ", "gateway-01", ErrNoBotID},
		{"no gateway instance id", messageUpdate(), "bot01", "", ErrNoGatewayInstanceID},
		{"unsupported update type", client.Update{UpdateID: 1}, "bot01", "gateway-01", ErrUnsupportedUpdate},
		{"channel post has no sender", channelPost, "bot01", "gateway-01", ErrNoSender},
		{"callback without its message", staleCallback, "bot01", "gateway-01", ErrNoChat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, err := Build(tt.update, tt.botID, tt.gatewayID, receivedAt)
			if err != tt.want {
				t.Fatalf("Build error = %v, want %v", err, tt.want)
			}
			if meta != (envelope.Metadata{}) {
				t.Errorf("Build returned %+v alongside an error, want the zero value", meta)
			}
		})
	}
}

// TestBuildEditedMessage records the decision that an edit gets its own update
// type rather than masquerading as a fresh message.
func TestBuildEditedMessage(t *testing.T) {
	update := messageUpdate()
	update.EditedMessage = update.Message
	update.Message = nil

	meta, err := Build(update, "bot01", "gateway-01", receivedAt)
	if err != nil {
		t.Fatalf("Build returned an error: %v", err)
	}
	if meta.UpdateType != UpdateTypeEditedMessage {
		t.Errorf("UpdateType = %q, want %q", meta.UpdateType, UpdateTypeEditedMessage)
	}
}

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"plain tag", "fa", "fa"},
		{"region stripped", "fa-IR", "fa"},
		{"underscore separator", "en_US", "en"},
		{"upper case", "EN", "en"},
		{"padded", "  en-GB  ", "en"},
		{"three letter tag", "ckb", "ckb"},
		{"empty falls back", "", DefaultLanguage},
		{"single letter is not a tag", "f", DefaultLanguage},
		{"digits are not a tag", "12", DefaultLanguage},
		{"too long", "farsi", DefaultLanguage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeLanguage(tt.code); got != tt.want {
				t.Errorf("NormalizeLanguage(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}
