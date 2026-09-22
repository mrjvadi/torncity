// Package envelope defines the wire format carried over NATS.
//
// Metadata and payload are separate by contract (MASTER_PROMPT section 11):
// infrastructure reads metadata for routing, tracing and idempotency without
// ever having to understand a domain payload.
package envelope

import (
	"encoding/json"
	"errors"
	"time"
)

// SchemaVersion is the current envelope version. It is carried on every
// message so a consumer can reject or migrate what it does not understand.
const SchemaVersion = 1

// Common validation failures.
var (
	ErrNoRequestID = errors.New("envelope: request_id is required")
	ErrNoTraceID   = errors.New("envelope: trace_id is required")
	ErrNoCommand   = errors.New("envelope: command is required")
	ErrBadVersion  = errors.New("envelope: unsupported schema_version")
)

// Metadata is the request context from MASTER_PROMPT section 6. It travels
// with every message so any hop can answer who, what, where and when.
//
// It deliberately contains no bot token and no secret of any kind.
type Metadata struct {
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`

	PlayerID string `json:"player_id,omitempty"`

	TelegramUserID    int64 `json:"telegram_user_id"`
	TelegramChatID    int64 `json:"telegram_chat_id"`
	TelegramMessageID int64 `json:"telegram_message_id,omitempty"`

	TelegramThreadID *int64  `json:"telegram_thread_id,omitempty"`
	ReplyToMessageID *int64  `json:"reply_to_message_id,omitempty"`
	CallbackQueryID  *string `json:"callback_query_id,omitempty"`

	BotID             string `json:"bot_id"`
	GatewayInstanceID string `json:"gateway_instance_id"`

	ChatType   string `json:"chat_type"`
	UpdateType string `json:"update_type"`
	Command    string `json:"command"`
	Action     string `json:"action,omitempty"`

	Language       string `json:"language"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	ReceivedAt    time.Time `json:"received_at"`
	SchemaVersion int       `json:"schema_version"`
}

// Validate reports whether the metadata carries the fields the system relies
// on. A message that fails this must never reach a domain handler.
func (m Metadata) Validate() error {
	switch {
	case m.RequestID == "":
		return ErrNoRequestID
	case m.TraceID == "":
		return ErrNoTraceID
	case m.Command == "":
		return ErrNoCommand
	case m.SchemaVersion != SchemaVersion:
		return ErrBadVersion
	}
	return nil
}

// Envelope is one NATS message: routing context plus an opaque domain payload.
type Envelope struct {
	Metadata Metadata        `json:"metadata"`
	Payload  json.RawMessage `json:"payload"`
}

// New builds an envelope, marshalling payload into the opaque section.
func New(meta Metadata, payload any) (*Envelope, error) {
	if err := meta.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Envelope{Metadata: meta, Payload: raw}, nil
}

// Decode unmarshals the payload into dst.
func (e *Envelope) Decode(dst any) error { return json.Unmarshal(e.Payload, dst) }
