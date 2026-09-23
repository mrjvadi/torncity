package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The map lists the cities a route reaches from where the player stands,
// nearest first, with the distance — the one number a traveller decides on.
func TestMapListsReachableDestinationsNearestFirst(t *testing.T) {
	h := newPhase1(t)
	h.player(400, "p-1", berlinID)
	handler := h.mapHandler(t)

	// From Berlin: Tehran at 400 km and Tokyo at 900 km. Lima has no route.
	resp, err := handler.List(context.Background(), command("map.list", 400, "req-1"), PageRequest{Page: "1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)

	tehran, tokyo := strings.Index(resp.Text, "Tehran"), strings.Index(resp.Text, "Tokyo")
	if tehran < 0 || tokyo < 0 {
		t.Fatalf("the reachable cities are missing: %q", resp.Text)
	}
	if tehran > tokyo {
		t.Errorf("Tokyo (900 km) is listed before Tehran (400 km): %q", resp.Text)
	}
	if !strings.Contains(resp.Text, "400") || !strings.Contains(resp.Text, "900") {
		t.Errorf("the distances are missing: %q", resp.Text)
	}
	if strings.Contains(resp.Text, "Lima") {
		t.Errorf("a city no route reaches is listed: %q", resp.Text)
	}
	// Tax and cost of living are not travel decisions; they stay off the
	// departures board. Tokyo's 1234 bps would read 12.34, its cost 2,000.
	for _, noise := range []string{"12.34", "2,000", "2000"} {
		if strings.Contains(resp.Text, noise) {
			t.Errorf("the map shows %q, which does not help choose a destination: %q", noise, resp.Text)
		}
	}
}

// Reachability is a fact about the route network. A city no route reaches is
// not a choice the player has, so it is neither listed nor offered.
func TestMapMarksWhatIsReachableFromHere(t *testing.T) {
	h := newPhase1(t)
	h.player(401, "p-1", tehranID)
	handler := h.mapHandler(t)

	resp, err := handler.List(context.Background(), command("map.list", 401, "req-1"), PageRequest{Page: "1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// From Tehran the only destination is Berlin, 400 km away.
	if !strings.Contains(resp.Text, "400") {
		t.Errorf("the distance to Berlin is missing: %q", resp.Text)
	}

	departures := map[string]bool{}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "travel:options:") {
				departures[strings.TrimPrefix(b.CallbackData, "travel:options:")] = true
			}
		}
	}
	if !departures["berlin"] {
		t.Error("no way to leave for Berlin, which is reachable")
	}
	if departures["lima"] {
		t.Error("the map offers a journey to Lima, which no route reaches")
	}
	if strings.Contains(resp.Text, "Lima") {
		t.Errorf("the map lists Lima, which no route reaches: %q", resp.Text)
	}
}

// The city the player is standing in gets no departure button: the domain
// refuses that trip, so offering it would be an invitation to a refusal.
func TestMapOffersNoJourneyToTheCityYouAreIn(t *testing.T) {
	h := newPhase1(t)
	h.player(402, "p-1", tehranID)
	handler := h.mapHandler(t)

	resp, err := handler.List(context.Background(), command("map.list", 402, "req-1"), PageRequest{Page: "2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if b.CallbackData == "travel:options:tehran" {
				t.Error("the map offers a journey to the city the player is in")
			}
		}
	}
}

func TestMapOffersNoJourneyWhileTravelling(t *testing.T) {
	h := newPhase1(t)
	h.player(403, "p-1", tehranID)
	travelHandler := h.travelHandler(t)
	ctx := context.Background()
	if _, err := travelHandler.Start(ctx, command("travel.start", 403, "req-1"), depart("berlin")); err != nil {
		t.Fatalf("departure: %v", err)
	}

	resp, err := h.mapHandler(t).List(ctx, command("map.list", 403, "req-2"), PageRequest{Page: "1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "travel:options:") {
				t.Errorf("the map offers a second journey to a player already travelling: %q", b.CallbackData)
			}
		}
	}
}

// A player who has not been placed in a city yet still gets a map.
func TestMapWorksForAPlayerWithNoCity(t *testing.T) {
	h := newPhase1(t)
	h.player(404, "p-1", "")
	handler := h.mapHandler(t)

	resp, err := handler.List(context.Background(), command("map.list", 404, "req-1"), PageRequest{Page: "1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertResolved(t, resp.Text)
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "travel:options:") {
				t.Errorf("a player with no city was offered a journey: %q", b.CallbackData)
			}
		}
	}
}

func TestMapRefusesAnUnknownPlayer(t *testing.T) {
	h := newPhase1(t)
	handler := h.mapHandler(t)

	_, err := handler.List(context.Background(), command("map.list", 999, "req-1"), PageRequest{})
	if !isSentinel(err, application.ErrPlayerNotFound) {
		t.Fatalf("got %v, want ErrPlayerNotFound", err)
	}
}

func TestMapEditsWhenPressed(t *testing.T) {
	h := newPhase1(t)
	h.player(405, "p-1", tehranID)
	handler := h.mapHandler(t)

	resp, err := handler.List(context.Background(), pressed(command("map.list", 405, "req-1"), 12), PageRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != presenter.ActionEditMessage || resp.MessageID != 12 {
		t.Errorf("a button press produced %+v, want an edit of message 12", resp)
	}
}

// Every callback address a screen emits has to fit what the gateway accepts,
// or the button is dead on arrival.
func TestMapCallbackDataFitsTheBudget(t *testing.T) {
	h := newPhase1(t)
	h.player(406, "p-1", tehranID)
	handler := h.mapHandler(t)

	resp, err := handler.List(context.Background(), command("map.list", 406, "req-1"), PageRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if len(b.CallbackData) > 64 {
				t.Errorf("callback data %q is %d bytes, over the 64-byte budget", b.CallbackData, len(b.CallbackData))
			}
			if b.CallbackData == "" {
				t.Errorf("button %q has no callback address", b.Text)
			}
		}
	}
}

func TestNewMapHandlerRefusesAZeroPageSize(t *testing.T) {
	h := newPhase1(t)
	routes := testRoutes(t)
	defer func() {
		if recover() == nil {
			t.Error("expected a panic, got none")
		}
	}()
	NewMapHandler(h.uow, nil, h.cities, h.travels, routes, 0, h.clock())
}
