package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// depart is the request a player's "travel to Berlin" button produces.
func depart(city string) StartTravelRequest { return StartTravelRequest{City: city} }

func TestTravelStartWritesTheJourneyAndItsSchedule(t *testing.T) {
	h := newPhase1(t)
	p := h.player(100, "p-1", tehranID)
	handler := h.travelHandler(t)

	resp, err := handler.Start(context.Background(), command("travel.start", 100, "req-1"), depart("berlin"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := len(h.travels.started); n != 1 {
		t.Fatalf("wrote %d travel rows, want 1", n)
	}
	row := h.travels.started[0]
	if row.FromCityID != tehranID || row.ToCityID != berlinID {
		t.Errorf("journey goes %s -> %s, want %s -> %s", row.FromCityID, row.ToCityID, tehranID, berlinID)
	}
	if row.Status != string(travel.StatusInTransit) {
		t.Errorf("journey status %q, want in_transit", row.Status)
	}
	// 400 km at 100 km/h plus ten minutes boarding, from the injected clock.
	wantArrival := fixedNow.Add(4*time.Hour + 10*time.Minute)
	if !row.ArrivesAt.Equal(wantArrival) {
		t.Errorf("arrives at %s, want %s", row.ArrivesAt, wantArrival)
	}

	if n := len(h.actions.scheduled); n != 1 {
		t.Fatalf("scheduled %d actions, want 1", n)
	}
	action := h.actions.scheduled[0]
	if !action.FinishAt.Equal(row.ArrivesAt) {
		t.Errorf("action finishes at %s but the journey arrives at %s", action.FinishAt, row.ArrivesAt)
	}
	if action.ActionType != TravelActionType {
		t.Errorf("action type %q, want %q", action.ActionType, TravelActionType)
	}
	if action.ReferenceID != row.ID || row.GameActionID != action.ID {
		t.Error("the travel row and the scheduled action do not point at each other")
	}

	// The energy came out of the player through the domain rule.
	if got := h.stats.rows[p.ID].Energy; got != 100-testEnergyCost {
		t.Errorf("energy is %d, want %d", got, 100-testEnergyCost)
	}

	if n := len(h.uow.tx.outbox.records); n != 1 {
		t.Fatalf("appended %d outbox records, want 1", n)
	}
	if got := h.uow.tx.outbox.records[0].Subject; got != "game.event.travel.started.v1" {
		t.Errorf("outbox subject %q is not the versioned travel.started subject", got)
	}

	if resp.Type != presenter.ActionSendMessage {
		t.Errorf("got action %q, want send_message", resp.Type)
	}
	assertResolved(t, resp.Text)
}

// Travelling to the city you are standing in is refused by the DOMAIN, and
// the refusal must reach the player as the right sentence rather than as a
// charged fare for a trip that does not exist.
func TestTravelToTheSameCityIsRefused(t *testing.T) {
	h := newPhase1(t)
	h.player(101, "p-1", tehranID)
	handler := h.travelHandler(t)

	_, err := handler.Start(context.Background(), command("travel.start", 101, "req-1"), depart("tehran"))
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !stderrors.Is(err, travel.ErrSameCity) {
		t.Errorf("error %v does not carry travel.ErrSameCity", err)
	}
	if n := len(h.travels.started); n != 0 {
		t.Errorf("wrote %d travel rows for a refused departure, want 0", n)
	}
	if n := len(h.actions.scheduled); n != 0 {
		t.Errorf("scheduled %d actions for a refused departure, want 0", n)
	}
}

func TestTravelWithNoRouteIsRefused(t *testing.T) {
	h := newPhase1(t)
	h.player(102, "p-1", tehranID)
	handler := h.travelHandler(t)

	// Lima is a real city with no route to it: see testRoutes.
	_, err := handler.Start(context.Background(), command("travel.start", 102, "req-1"), depart("lima"))
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !stderrors.Is(err, world.ErrUnknownCity) && !stderrors.Is(err, world.ErrNoRoute) {
		t.Errorf("error %v does not explain the missing route", err)
	}
	if n := len(h.travels.started); n != 0 {
		t.Errorf("wrote %d travel rows for an unreachable city, want 0", n)
	}
}

// The energy check is the only limit phase 1 puts on travelling, so a refused
// departure must leave nothing behind: no travel row, no scheduled arrival.
func TestTravelWithoutEnergyIsRefusedAndWritesNoTravelRow(t *testing.T) {
	h := newPhase1(t)
	p := h.player(103, "p-1", tehranID)
	broke := defaultStats(p.ID, fixedNow)
	broke.Energy = testEnergyCost - 1
	h.stats.rows[p.ID] = broke

	handler := h.travelHandler(t)
	_, err := handler.Start(context.Background(), command("travel.start", 103, "req-1"), depart("berlin"))
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !stderrors.Is(err, player.ErrNotEnoughEnergy) {
		t.Errorf("error %v does not carry the domain's not-enough-energy rule", err)
	}
	if n := len(h.travels.started); n != 0 {
		t.Errorf("wrote %d travel rows without the energy to depart, want 0", n)
	}
	if n := len(h.actions.scheduled); n != 0 {
		t.Errorf("scheduled %d arrivals without the energy to depart, want 0", n)
	}
	if got := h.stats.rows[p.ID].Energy; got != testEnergyCost-1 {
		t.Errorf("energy moved to %d on a refused departure, want %d", got, testEnergyCost-1)
	}
}

func TestTravelWhileAlreadyTravellingIsRefused(t *testing.T) {
	h := newPhase1(t)
	h.player(104, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 104, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("first departure: %v", err)
	}
	_, err := handler.Start(ctx, command("travel.start", 104, "req-2"), depart("tokyo"))
	if !isSentinel(err, application.ErrAlreadyTravelling) {
		t.Fatalf("second departure gave %v, want ErrAlreadyTravelling", err)
	}
	if n := len(h.travels.started); n != 1 {
		t.Errorf("wrote %d travel rows, want 1", n)
	}
}

// One press, delivered twice. The second delivery must not depart again.
func TestTravelStartReplayDepartsOnce(t *testing.T) {
	h := newPhase1(t)
	p := h.player(105, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()
	m := command("travel.start", 105, "req-replay")

	if _, err := handler.Start(ctx, m, depart("berlin")); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	energyAfterFirst := h.stats.rows[p.ID].Energy

	resp, err := handler.Start(ctx, m, depart("berlin"))
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if n := len(h.travels.started); n != 1 {
		t.Errorf("replay wrote %d travel rows, want 1", n)
	}
	if n := len(h.actions.scheduled); n != 1 {
		t.Errorf("replay scheduled %d arrivals, want 1", n)
	}
	if got := h.stats.rows[p.ID].Energy; got != energyAfterFirst {
		t.Errorf("replay charged energy again: %d, want %d", got, energyAfterFirst)
	}
	if n := len(h.uow.tx.outbox.records); n != 1 {
		t.Errorf("replay appended %d outbox records, want 1", n)
	}
	// The replay shows the journey the player is on rather than a second
	// departure they did not make.
	assertResolved(t, resp.Text)
}

func TestTravelStatusShowsTheJourney(t *testing.T) {
	h := newPhase1(t)
	h.player(106, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 106, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}

	h.now = fixedNow.Add(2 * time.Hour)
	resp, err := handler.Status(ctx, command("travel.status", 106, "req-2"))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	assertResolved(t, resp.Text)
	// Two hours and ten minutes left of a four-hour-ten journey.
	if !strings.Contains(resp.Text, "2") {
		t.Errorf("status %q does not show the hours remaining", resp.Text)
	}
}

func TestTravelStatusWithoutAJourney(t *testing.T) {
	h := newPhase1(t)
	h.player(107, "p-1", tehranID)
	handler := h.travelHandler(t)

	_, err := handler.Status(context.Background(), command("travel.status", 107, "req-1"))
	if !isSentinel(err, application.ErrNoActiveTravel) {
		t.Fatalf("got %v, want ErrNoActiveTravel", err)
	}
}

func TestTravelCompleteLandsThePlayer(t *testing.T) {
	h := newPhase1(t)
	p := h.player(108, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 108, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}
	journey := h.travels.started[0]

	h.now = journey.ArrivesAt
	resp, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-1"), arrival(p.ID, journey.ID))
	if err != nil {
		t.Fatalf("arrival: %v", err)
	}
	if resp == nil {
		t.Fatal("an arrival must produce a notification")
	}
	// A notification is SENT: the player pressed nothing, so there is no
	// message of theirs to edit.
	if resp.Type != presenter.ActionSendMessage {
		t.Errorf("arrival used %q, want send_message", resp.Type)
	}
	assertResolved(t, resp.Text)

	if got := h.travels.moved[p.ID]; got != berlinID {
		t.Errorf("player landed in %q, want %q", got, berlinID)
	}
	if got := h.stats.rows[p.ID].XP; got != testArrivalXP {
		t.Errorf("awarded %d xp, want %d", got, testArrivalXP)
	}
	if n := len(h.uow.tx.outbox.records); n != 2 {
		t.Fatalf("appended %d outbox records, want 2 (started and completed)", n)
	}
	if got := h.uow.tx.outbox.records[1].Subject; got != "game.event.travel.completed.v1" {
		t.Errorf("outbox subject %q is not the versioned travel.completed subject", got)
	}
}

// The scheduler publishes at least once and mints a FRESH request id every
// dispatch, so a key derived from the request would not recognise the second
// delivery at all. A second arrival must move nobody twice and award no XP
// twice.
func TestTravelCompleteTwiceMovesNobodyTwice(t *testing.T) {
	h := newPhase1(t)
	p := h.player(109, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 109, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}
	journey := h.travels.started[0]
	h.now = journey.ArrivesAt

	if _, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-1"), arrival(p.ID, journey.ID)); err != nil {
		t.Fatalf("first arrival: %v", err)
	}
	xpAfterFirst := h.stats.rows[p.ID].XP

	resp, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-2"), arrival(p.ID, journey.ID))
	if err != nil {
		t.Fatalf("second arrival: %v", err)
	}
	if resp != nil {
		t.Errorf("a redelivered arrival announced itself again: %+v", resp)
	}
	if n := len(h.travels.completed); n != 1 {
		t.Errorf("completed the journey %d times, want 1", n)
	}
	if got := h.stats.rows[p.ID].XP; got != xpAfterFirst {
		t.Errorf("xp moved to %d on the second arrival, want %d", got, xpAfterFirst)
	}
	if n := len(h.uow.tx.outbox.records); n != 2 {
		t.Errorf("the second arrival appended an extra event: %d records, want 2", n)
	}
}

// This is the regression test for the XP that used to be lost on retry.
//
// The arrival (journey arrived, player moved) used to commit on its own
// connection. When a step after it failed, only the idempotency key and the
// outbox record rolled back, so the redelivery found no active journey,
// returned nil, and the player had landed without the XP. Now every write
// goes through the unit of work, so a failure at any step after the landing
// takes the landing with it, and the retry does the whole arrival exactly
// once.
func TestTravelCompleteRetryAfterAFailedStepAwardsXPOnce(t *testing.T) {
	tests := []struct {
		name string
		fail func(h *phase1)
	}{
		// The step immediately after travels.Complete.
		{"xp award fails", func(h *phase1) { h.stats.failSaves = 1 }},
		// The last step, so everything before it has already been written.
		{"outbox append fails", func(h *phase1) { h.uow.tx.outbox.failAppends = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newPhase1(t)
			p := h.player(120, "p-1", tehranID)
			handler := h.travelHandler(t)
			ctx := context.Background()

			if _, err := handler.Start(ctx, command("travel.start", 120, "req-1"), depart("berlin")); err != nil {
				t.Fatalf("departure: %v", err)
			}
			journey := h.travels.started[0]
			h.now = journey.ArrivesAt
			xpBefore := h.stats.rows[p.ID].XP
			outboxBefore := len(h.uow.tx.outbox.records)
			rollbacksBefore := h.uow.rollbacks

			tt.fail(h)
			if _, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-1"), arrival(p.ID, journey.ID)); !stderrors.Is(err, errInjected) {
				t.Fatalf("got %v, want the injected failure", err)
			}

			// The whole unit rolled back: the journey is still in transit,
			// the player has not moved, no XP and no event were kept.
			if h.uow.rollbacks != rollbacksBefore+1 {
				t.Errorf("rollbacks = %d, want %d", h.uow.rollbacks, rollbacksBefore+1)
			}
			if _, ok := h.travels.active[p.ID]; !ok {
				t.Fatal("the failed arrival still marked the journey arrived; a retry would find nothing to land")
			}
			if n := len(h.travels.completed); n != 0 {
				t.Errorf("the failed arrival kept %d completions, want 0", n)
			}
			if got, moved := h.travels.moved[p.ID]; moved {
				t.Errorf("the failed arrival moved the player to %q", got)
			}
			if got := h.stats.rows[p.ID].XP; got != xpBefore {
				t.Errorf("xp is %d after the failed arrival, want %d", got, xpBefore)
			}
			if n := len(h.uow.tx.outbox.records); n != outboxBefore {
				t.Errorf("the failed arrival kept %d outbox records, want %d", n, outboxBefore)
			}

			// The redelivery: a fresh request id, as the scheduler mints.
			resp, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-2"), arrival(p.ID, journey.ID))
			if err != nil {
				t.Fatalf("retried arrival: %v", err)
			}
			if resp == nil {
				t.Fatal("the retried arrival found nothing to land")
			}
			if got := h.travels.moved[p.ID]; got != berlinID {
				t.Errorf("player landed in %q, want %q", got, berlinID)
			}
			if got := h.stats.rows[p.ID].XP; got != xpBefore+testArrivalXP {
				t.Errorf("xp is %d after the retry, want %d", got, xpBefore+testArrivalXP)
			}

			// And a third delivery changes nothing: the award is once.
			if _, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-3"), arrival(p.ID, journey.ID)); err != nil {
				t.Fatalf("third delivery: %v", err)
			}
			if got := h.stats.rows[p.ID].XP; got != xpBefore+testArrivalXP {
				t.Errorf("xp is %d after a third delivery, want %d", got, xpBefore+testArrivalXP)
			}
			if n := len(h.travels.completed); n != 1 {
				t.Errorf("completed the journey %d times, want 1", n)
			}
			if n := len(h.uow.tx.outbox.records); n != outboxBefore+1 {
				t.Errorf("%d outbox records, want %d (one travel.completed)", n, outboxBefore+1)
			}
		})
	}
}

// Even with the idempotency store wiped — a key that expired, a store that
// lost a row — a second arrival must still find nothing to do, because the
// journey is no longer the player's active one.
func TestTravelCompleteIsIdempotentWithoutTheKey(t *testing.T) {
	h := newPhase1(t)
	p := h.player(110, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 110, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}
	journey := h.travels.started[0]
	h.now = journey.ArrivesAt

	if _, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-1"), arrival(p.ID, journey.ID)); err != nil {
		t.Fatalf("first arrival: %v", err)
	}

	h.uow.tx.idem.seen = map[string]bool{}

	resp, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-2"), arrival(p.ID, journey.ID))
	if err != nil {
		t.Fatalf("second arrival: %v", err)
	}
	if resp != nil {
		t.Errorf("a second arrival announced itself: %+v", resp)
	}
	if n := len(h.travels.completed); n != 1 {
		t.Errorf("completed the journey %d times, want 1", n)
	}
}

func TestTravelCompleteRejectsAPayloadWithNoJourney(t *testing.T) {
	h := newPhase1(t)
	handler := h.travelHandler(t)

	_, err := handler.Complete(context.Background(), scheduled("travel.arrive", "dispatch-1"), ArriveTravelRequest{})
	if err == nil {
		t.Fatal("expected a refusal for an arrival that names no journey")
	}
}

// The scheduler carries the player and the journey in the ROW's columns, but
// the departure also writes them into the jsonb. A dispatch that lost the
// columns must still land the player.
func TestTravelCompleteFallsBackToTheActionPayload(t *testing.T) {
	h := newPhase1(t)
	p := h.player(111, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 111, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}
	journey := h.travels.started[0]
	h.now = journey.ArrivesAt

	req := ArriveTravelRequest{Payload: h.actions.scheduled[0].Payload}
	if _, err := handler.Complete(ctx, scheduled("travel.arrive", "dispatch-1"), req); err != nil {
		t.Fatalf("arrival: %v", err)
	}
	if got := h.travels.moved[p.ID]; got != berlinID {
		t.Errorf("player landed in %q, want %q", got, berlinID)
	}
}

// A button press edits the message it sits on; a typed command cannot,
// because the message belongs to the player.
func TestTravelStatusEditsWhenPressed(t *testing.T) {
	h := newPhase1(t)
	h.player(112, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 112, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}

	typed, err := handler.Status(ctx, command("travel.status", 112, "req-2"))
	if err != nil {
		t.Fatalf("typed status: %v", err)
	}
	if typed.Type != presenter.ActionSendMessage {
		t.Errorf("a typed command produced %q, want send_message", typed.Type)
	}

	press, err := handler.Status(ctx, pressed(command("travel.status", 112, "req-3"), 4242))
	if err != nil {
		t.Fatalf("pressed status: %v", err)
	}
	if press.Type != presenter.ActionEditMessage {
		t.Errorf("a button press produced %q, want edit_message", press.Type)
	}
	if press.MessageID != 4242 {
		t.Errorf("edited message %d, want 4242", press.MessageID)
	}
}

func TestTravelRejectsMalformedContext(t *testing.T) {
	h := newPhase1(t)
	h.player(113, "p-1", tehranID)
	handler := h.travelHandler(t)

	broken := command("travel.start", 113, "req-1")
	broken.TraceID = ""
	if _, err := handler.Start(context.Background(), broken, depart("berlin")); err == nil {
		t.Error("expected a refusal for metadata with no trace id")
	}

	if _, err := handler.Start(context.Background(), command("travel.start", 113, "req-2"), StartTravelRequest{}); err == nil {
		t.Error("expected a refusal for a departure with no destination")
	}
}

func TestNewTravelHandlerRefusesZeroTuning(t *testing.T) {
	h := newPhase1(t)
	planner := testPlanner(t)

	tests := []struct {
		name   string
		energy int
		xp     int64
		ttl    time.Duration
	}{
		{"free travel", 0, testArrivalXP, testIdempotencyTTL},
		{"pointless arrival", testEnergyCost, 0, testIdempotencyTTL},
		{"no replay window", testEnergyCost, testArrivalXP, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected a panic, got none")
				}
			}()
			NewTravelHandler(h.uow, h.ids, nil, h.cities, planner, tt.energy, tt.xp, tt.ttl, h.clock())
		})
	}
}

// --- helpers -----------------------------------------------------------

// arrival is the command the scheduler publishes for a due journey.
func arrival(playerID, travelID string) ArriveTravelRequest {
	return ArriveTravelRequest{
		ActionID:      "action-1",
		ActorID:       playerID,
		ReferenceType: "travel",
		ReferenceID:   travelID,
	}
}

// assertResolved fails when text still looks like a catalogue key or carries
// an unfilled placeholder. It deliberately never asserts on wording: a
// translator rewording a screen is not a broken handler.
func assertResolved(t *testing.T, text string) {
	t.Helper()
	if text == "" {
		t.Fatal("empty response text")
	}
	if strings.Contains(text, "{") {
		t.Errorf("text has an unfilled placeholder: %q", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		if looksLikeKey(line) {
			t.Errorf("line %q rendered as a catalogue key", line)
		}
	}
}

// looksLikeKey reports whether a whole line is a dotted lower-case
// identifier, which is what an unresolved key looks like on screen.
func looksLikeKey(line string) bool {
	if !strings.Contains(line, ".") || strings.Contains(line, " ") {
		return false
	}
	for _, r := range line {
		switch {
		case r >= 'a' && r <= 'z', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}
