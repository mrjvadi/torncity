package main

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// Every military and war command a player can send goes through the barracks
// gate; the scheduler's three do not. A command refused by the gate never
// reaches its handler (the handlers here are nil: reaching one would panic).
func TestMilitaryAndWarCommandsNeedABarracks(t *testing.T) {
	refused := errors.NotFound("there is no such place here")
	calls := 0
	h := phaseHandlers{militaryGate: func(context.Context, envelope.Metadata) error { calls++; return refused }}
	table := h.bindMilitary()

	scheduler := map[string]bool{"military.settle": true, "military.arrive": true, "war.resolve": true}
	var gated []string
	for name, f := range table {
		if !strings.HasPrefix(name, "military.") && !strings.HasPrefix(name, "war.") {
			continue
		}
		if scheduler[name] {
			continue
		}
		gated = append(gated, name)
		before := calls
		_, err := f(context.Background(), &envelope.Envelope{})
		if err != refused {
			t.Errorf("%s: err = %v, want the barracks refusal", name, err)
		}
		if calls != before+1 {
			t.Errorf("%s did not go through the gate", name)
		}
	}
	sort.Strings(gated)
	if len(gated) < 15 {
		t.Fatalf("only %d gated commands (%v); the table lost its military or war commands", len(gated), gated)
	}
}
