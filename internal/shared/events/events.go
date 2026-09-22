// Package events defines the domain event every state change is published as.
//
// Events outlive the code that wrote them: they sit in a stream and in an
// outbox table, and a consumer deployed months later still has to read them.
// That is why SchemaVersion is mandatory rather than optional. A consumer
// checks the version before it trusts the payload, so an unversioned event is
// unreadable by contract and Validate rejects it outright.
//
// Payload stays json.RawMessage so this package never has to know about any
// domain type, and so a consumer can route on Name without parsing a payload
// whose shape it may not understand.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// SchemaVersion is the current event envelope version, stamped on every event
// this build produces.
const SchemaVersion = 1

// Validation failures. An event that fails any of these must not be published.
var (
	ErrNoName          = errors.New("events: name is required")
	ErrNoSchemaVersion = errors.New("events: schema_version is required")
	ErrNoOccurredAt    = errors.New("events: occurred_at is required")
	ErrNoAggregate     = errors.New("events: aggregate type and id are required")
)

// Event is one thing that happened, stated in the past tense.
//
// Name is the routing key, dotted and lowercase, for example "player.created".
// AggregateType and AggregateID say what it happened to, which is what lets a
// consumer order events per entity and what the outbox partitions on.
type Event struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	SchemaVersion int             `json:"schema_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
}

// New builds a valid event around payload.
//
// OccurredAt is set in UTC, never in local time: these timestamps are compared
// across services and stored in a database, and a local offset would make the
// ordering of two events depend on where the process happened to run.
func New(name string, aggregateType, aggregateID string, payload any) (*Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	e := &Event{
		ID:            newID(),
		Name:          name,
		SchemaVersion: SchemaVersion,
		OccurredAt:    time.Now().UTC(),
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		Payload:       raw,
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return e, nil
}

// Validate reports whether the event carries what a consumer needs to read it
// at all. This runs before publishing and again after decoding, because an
// event may arrive from an older producer.
func (e *Event) Validate() error {
	switch {
	case e.Name == "":
		return ErrNoName
	case e.SchemaVersion == 0:
		return ErrNoSchemaVersion
	case e.OccurredAt.IsZero():
		return ErrNoOccurredAt
	case e.AggregateType == "" || e.AggregateID == "":
		return ErrNoAggregate
	}
	return nil
}

// Decode unmarshals the payload into dst.
func (e *Event) Decode(dst any) error { return json.Unmarshal(e.Payload, dst) }

// newID returns a random 128-bit hex identifier. It is random rather than
// sequential so that two producers can mint ids without coordinating.
func newID() string {
	var b [16]byte
	// crypto/rand.Read is documented never to return an error.
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
