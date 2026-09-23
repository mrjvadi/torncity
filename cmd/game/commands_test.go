package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/cmd/game/subscriptions"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TestEverySubscriptionIsBound: the process refuses to start on a mismatch,
// and this makes the refusal a test failure instead of a failed deploy.
func TestEverySubscriptionIsBound(t *testing.T) {
	if _, err := bindAll(subscriptions.All(), phaseHandlers{}.bind()); err != nil {
		t.Fatal(err)
	}
}

func TestBindAllRefusesAGap(t *testing.T) {
	bound := phaseHandlers{}.bind()
	delete(bound, "travel.arrive")
	if _, err := bindAll(subscriptions.All(), bound); err == nil || !strings.Contains(err.Error(), "travel.arrive") {
		t.Fatalf("bindAll = %v, want a refusal naming travel.arrive", err)
	}

	bound = phaseHandlers{}.bind()
	bound["travel.teleport"] = func(context.Context, *envelope.Envelope) (*presenter.Response, error) { return nil, nil }
	if _, err := bindAll(subscriptions.All(), bound); err == nil || !strings.Contains(err.Error(), "travel.teleport") {
		t.Fatalf("bindAll = %v, want a refusal naming travel.teleport", err)
	}
}

func TestSubscriptionsAreDistinctAndWellFormed(t *testing.T) {
	seenSubject := map[string]bool{}
	seenDurable := map[string]bool{}
	for _, sub := range subscriptions.All() {
		if !subjects.Grammar.MatchString(sub.Subject()) {
			t.Errorf("%s does not match the subject grammar", sub.Subject())
		}
		if seenSubject[sub.Subject()] || seenDurable[sub.Durable()] {
			t.Errorf("%s is listed twice; the command stream is a work queue and filters must not overlap", sub.Command())
		}
		seenSubject[sub.Subject()] = true
		seenDurable[sub.Durable()] = true
	}

	// The phase 0 durable must keep its name, or a deploy starts a new
	// consumer from the head of the stream.
	if !seenDurable["game-player-profile-get"] {
		t.Error("the phase 0 durable game-player-profile-get was renamed")
	}
}

func TestDecodeClassifiesABadPayloadAsInput(t *testing.T) {
	var dst struct{ City string }
	err := decode(&envelope.Envelope{Payload: []byte(`"not an object"`)}, &dst)
	if apperrors.CodeOf(err) != apperrors.CodeInvalidInput {
		t.Fatalf("decode error %v has code %q, want INVALID_INPUT so it is answered, not retried", err, apperrors.CodeOf(err))
	}
	if err := decode(&envelope.Envelope{}, &dst); err != nil {
		t.Fatalf("an empty payload is a command with no arguments, got %v", err)
	}
}

func TestNewTariffFromDefaults(t *testing.T) {
	if _, err := newTariff(240, 1, 720, 1); err != nil {
		t.Fatalf("newTariff: %v", err)
	}
	if _, err := newTariff(0, 1, 720, 1); err == nil {
		t.Fatal("a speed of zero was accepted")
	}
}
