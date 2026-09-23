package scheduler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// These tests hold the two ends of every scheduled command together.
//
// The scheduler and the game service are separate processes that meet only on
// a subject name and a json shape. When the two drifted apart once already —
// the scheduler publishing travel.arrive with nothing subscribed to it — every
// test on both sides still passed, and a journey that came due was published
// into a stream no consumer read. The player never landed. What catches that
// is a test that reads both tables at once, which is this file.

// TestEveryRouteHasAConsumer: every subject the scheduler can publish is one
// the game service subscribes to, and subscribes to as a scheduled command.
func TestEveryRouteHasAConsumer(t *testing.T) {
	consumed := map[string]commands.Subscription{}
	for _, sub := range commands.All() {
		consumed[sub.Subject()] = sub
	}

	for _, actionType := range ActionTypes() {
		route, _ := RouteFor(actionType)
		subject := subjects.Command(route.Domain, route.Action)

		sub, ok := consumed[subject]
		if !ok {
			t.Errorf("action type %q is published on %s, which cmd/game does not subscribe to; subscribed: %v",
				actionType, subject, commands.Subjects())
			continue
		}
		if sub.Origin != commands.FromScheduler {
			t.Errorf("%s is consumed as a player command; a scheduled command has no bot to reply through", subject)
		}
		if sub.Command() != route.Command() {
			t.Errorf("metadata command %q does not match the subscription's %q", route.Command(), sub.Command())
		}
	}
}

// TestTravelArrivalSubject pins the one route phase 1 depends on, by value,
// so a rename has to be made on purpose on both sides.
func TestTravelArrivalSubject(t *testing.T) {
	route, ok := RouteFor(handlers.TravelActionType)
	if !ok {
		t.Fatalf("the travel handler schedules action type %q, which the scheduler cannot route", handlers.TravelActionType)
	}
	if handlers.TravelActionType != ActionTypeTravel {
		t.Errorf("handler writes action type %q, scheduler routes %q", handlers.TravelActionType, ActionTypeTravel)
	}

	got := subjects.Command(route.Domain, route.Action)
	const want = "game.command.travel.arrive.v1"
	if got != want {
		t.Errorf("travel publishes on %s, want %s", got, want)
	}

	found := false
	for _, s := range commands.Subjects() {
		if s == got {
			found = true
		}
	}
	if !found {
		t.Errorf("cmd/game does not subscribe to %s; subscribed: %v", got, commands.Subjects())
	}
}

// TestArrivalPayloadDecodesIntoTheHandlerRequest: the json the scheduler
// publishes is the json the travel handler reads.
func TestArrivalPayloadDecodesIntoTheHandlerRequest(t *testing.T) {
	h := newHarness(t, false, travelAction(actionID, epoch))
	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	var req handlers.ArriveTravelRequest
	if err := h.broker.messages()[0].env.Decode(&req); err != nil {
		t.Fatalf("the scheduler's payload does not decode as the handler's request: %v", err)
	}
	if req.ActionID != actionID || req.ActorID != playerID || req.ReferenceID != travelID || req.ReferenceType != "travel" {
		t.Errorf("request %+v lost the row's identity", req)
	}

	var inner handlers.TravelActionPayload
	if err := json.Unmarshal(req.Payload, &inner); err != nil {
		t.Fatalf("forwarded payload: %v", err)
	}
	if inner.TravelID != travelID || inner.PlayerID != playerID {
		t.Errorf("forwarded payload %+v lost the journey", inner)
	}
}
