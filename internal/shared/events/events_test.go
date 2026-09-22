package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type playerCreated struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
}

func TestNew(t *testing.T) {
	e, err := New("player.created", "player", "player-1", playerCreated{PlayerID: "player-1", Name: "ana"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Name != "player.created" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", e.SchemaVersion, SchemaVersion)
	}
	if e.ID == "" {
		t.Error("ID was not set")
	}
	if e.AggregateType != "player" || e.AggregateID != "player-1" {
		t.Errorf("aggregate = %q/%q", e.AggregateType, e.AggregateID)
	}

	var got playerCreated
	if err := e.Decode(&got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Name != "ana" {
		t.Errorf("payload round trip lost data: %+v", got)
	}
}

// TestOccurredAtIsUTC guards the ordering guarantee: event timestamps are
// compared across services, so a local offset would reorder history.
func TestOccurredAtIsUTC(t *testing.T) {
	e, err := New("player.created", "player", "player-1", playerCreated{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loc := e.OccurredAt.Location(); loc != time.UTC {
		t.Errorf("OccurredAt location = %v, want UTC", loc)
	}
	if _, offset := e.OccurredAt.Zone(); offset != 0 {
		t.Errorf("OccurredAt zone offset = %d seconds, want 0", offset)
	}
	// The serialised form is what a consumer actually reads.
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Event
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, offset := decoded.OccurredAt.Zone(); offset != 0 {
		t.Errorf("round-tripped OccurredAt is not UTC: %s", string(raw))
	}
}

func TestNewRejectsUnmarshalablePayload(t *testing.T) {
	if _, err := New("player.created", "player", "player-1", make(chan int)); err == nil {
		t.Error("expected an error for a payload JSON cannot encode")
	}
}

func TestValidate(t *testing.T) {
	valid := Event{
		ID:            "abc",
		Name:          "player.created",
		SchemaVersion: SchemaVersion,
		OccurredAt:    time.Now().UTC(),
		AggregateType: "player",
		AggregateID:   "player-1",
		Payload:       json.RawMessage(`{}`),
	}

	tests := []struct {
		name   string
		mutate func(*Event)
		want   error
	}{
		{"valid", func(*Event) {}, nil},
		{"no name", func(e *Event) { e.Name = "" }, ErrNoName},
		// The spec forbids unversioned events: a consumer cannot tell which
		// payload shape it is holding without a version.
		{"zero schema version", func(e *Event) { e.SchemaVersion = 0 }, ErrNoSchemaVersion},
		{"zero occurred at", func(e *Event) { e.OccurredAt = time.Time{} }, ErrNoOccurredAt},
		{"no aggregate type", func(e *Event) { e.AggregateType = "" }, ErrNoAggregate},
		{"no aggregate id", func(e *Event) { e.AggregateID = "" }, ErrNoAggregate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := valid
			tt.mutate(&e)
			err := e.Validate()
			if !errors.Is(err, tt.want) {
				t.Errorf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestUnversionedEventIsRejectedAfterDecoding covers the realistic case: an
// event produced before versioning existed arrives with no schema_version at
// all and must be refused rather than guessed at.
func TestUnversionedEventIsRejectedAfterDecoding(t *testing.T) {
	raw := `{"id":"abc","name":"player.created","occurred_at":"2024-01-01T00:00:00Z","aggregate_type":"player","aggregate_id":"player-1","payload":{}}`
	var e Event
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !errors.Is(e.Validate(), ErrNoSchemaVersion) {
		t.Errorf("an event with no schema_version was accepted: %v", e.Validate())
	}
}
