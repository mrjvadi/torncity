package envelope

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validMeta() Metadata {
	return Metadata{
		RequestID:         "req_1",
		TraceID:           "trace_1",
		TelegramUserID:    123,
		TelegramChatID:    456,
		BotID:             "bot07",
		GatewayInstanceID: "gateway-02",
		ChatType:          "private",
		UpdateType:        "message",
		Command:           "player.profile.get",
		Language:          "fa",
		ReceivedAt:        time.Now().UTC(),
		SchemaVersion:     SchemaVersion,
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Metadata)
		want   error
	}{
		{"no request id", func(m *Metadata) { m.RequestID = "" }, ErrNoRequestID},
		{"no trace id", func(m *Metadata) { m.TraceID = "" }, ErrNoTraceID},
		{"no command", func(m *Metadata) { m.Command = "" }, ErrNoCommand},
		{"bad version", func(m *Metadata) { m.SchemaVersion = 99 }, ErrBadVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validMeta()
			tt.mutate(&m)
			if err := m.Validate(); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	type payload struct {
		ItemID string `json:"item_id"`
	}
	env, err := New(validMeta(), payload{ItemID: "phone"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var got payload
	if err := back.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ItemID != "phone" {
		t.Errorf("payload lost in transit: %+v", got)
	}
	if back.Metadata.BotID != "bot07" {
		t.Errorf("metadata lost in transit: %+v", back.Metadata)
	}
}

// The envelope must never be a place a secret can hide.
func TestMetadataHasNoTokenField(t *testing.T) {
	raw, err := json.Marshal(validMeta())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "secret", "api_hash", "password"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Errorf("metadata serialisation contains %q", forbidden)
		}
	}
}
