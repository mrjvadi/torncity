package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The fares of the fixed world, tehran -> berlin (400): see testModes.
const (
	trainFare  = 100 + 400*1 // public
	flightFare = 500 + 400*4 // private
)

// withSink opens the system sink in the fake ledger, as migration 0006 does.
func withSink(h *phase1) {
	h.uow.tx.ledger.accounts[application.SystemSinkAccountID] = &application.Account{
		ID: application.SystemSinkAccountID, Kind: application.AccountSystemSink,
	}
}

func by(city, mode string, max int64) StartTravelRequest {
	return StartTravelRequest{City: city, Mode: mode, Max: strconv.FormatInt(max, 10), Method: "cash"}
}

// buttons lists every callback address on a response.
func buttons(resp *presenter.Response) []string {
	var out []string
	if resp == nil || resp.Keyboard == nil {
		return out
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			out = append(out, b.CallbackData)
		}
	}
	return out
}

func hasExactButton(resp *presenter.Response, data string) bool {
	for _, b := range buttons(resp) {
		if b == data {
			return true
		}
	}
	return false
}

func TestTravelOptionsListEveryModeWithItsPriceAndWriteNothing(t *testing.T) {
	h := newPhase1(t)
	h.player(300, "p-1", tehranID)
	handler := h.travelHandler(t)

	resp, err := handler.Options(context.Background(), command("travel.options", 300, "req-1"), TravelOptionsRequest{City: "berlin"})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	assertResolved(t, resp.Text)
	for _, want := range []string{
		"travel:start:berlin:bus:0",
		fmt.Sprintf("travel:start:berlin:train:%d", trainFare),
		fmt.Sprintf("travel:start:berlin:flight:%d", flightFare),
	} {
		if !hasExactButton(resp, want) {
			t.Errorf("no button %q among %v", want, buttons(resp))
		}
	}
	if n := len(h.travels.started) + len(h.actions.scheduled) + len(h.uow.tx.outbox.records); n != 0 {
		t.Errorf("the choice of transport wrote %d rows", n)
	}
	// The public fare policy was read through the resolver, for the origin.
	if len(h.policy.asked) != 1 || h.policy.asked[0] != "jur-tehran/"+TransitFareLever {
		t.Errorf("policy reads = %v, want one read of %s for tehran", h.policy.asked, TransitFareLever)
	}

	// Tokyo is reachable by road only: no flight is offered.
	resp, err = handler.Options(context.Background(), command("travel.options", 300, "req-2"), TravelOptionsRequest{City: "tokyo"})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	for _, b := range buttons(resp) {
		if strings.Contains(b, ":flight:") {
			t.Errorf("a flight is offered to a city with no air route: %v", buttons(resp))
		}
	}
}

func TestTravelStartWithoutAChoiceShowsTheChoice(t *testing.T) {
	h := newPhase1(t)
	h.player(301, "p-1", tehranID)
	handler := h.travelHandler(t)

	for _, req := range []StartTravelRequest{
		{City: "berlin"},
		{City: "berlin", Mode: "train"},
		{City: "berlin", Mode: "train", Max: "cheap"},
		{City: "berlin", Mode: "train", Max: "-1"},
	} {
		resp, err := handler.Start(context.Background(), command("travel.start", 301, "req-"+req.Mode+req.Max), req)
		if err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
		if !hasExactButton(resp, fmt.Sprintf("travel:start:berlin:train:%d", trainFare)) {
			t.Errorf("%+v did not answer with the choice of transport: %v", req, buttons(resp))
		}
	}
	if n := len(h.travels.started); n != 0 {
		t.Errorf("a departure without a chosen mode and price wrote %d journeys", n)
	}
}

func TestPublicFareIsPaidToTheOriginCityInTheSameTransaction(t *testing.T) {
	h := newPhase1(t)
	p := h.player(302, "p-1", tehranID)
	h.uow.tx.ledger.give(t, application.AccountPlayerCash, p.ID, 1000)
	handler := h.travelHandler(t)

	resp, err := handler.Start(context.Background(), command("travel.start", 302, "req-1"), by("berlin", "train", trainFare))
	if err != nil {
		t.Fatalf("departure: %v", err)
	}
	assertResolved(t, resp.Text)

	ledger := h.uow.tx.ledger
	if got := ledger.balance(application.AccountPlayerCash, p.ID); got != 1000-trainFare {
		t.Errorf("cash %d, want %d", got, 1000-trainFare)
	}
	if got := ledger.balance(application.AccountCityTreasury, tehranID); got != trainFare {
		t.Errorf("tehran's treasury holds %d, want the fare %d", got, trainFare)
	}
	last := ledger.posted[len(ledger.posted)-1]
	if last.Reason != application.ReasonTransitFare || last.ReferenceType != "travels" {
		t.Errorf("fare posted as %s on %s, want transit_fare on travels", last.Reason, last.ReferenceType)
	}

	row := h.travels.started[0]
	if row.Mode != "train" || row.Cost != trainFare || row.LedgerTransactionID == "" || row.ContentVersion != 7 {
		t.Errorf("journey row %+v does not record its mode, fare, transaction and content version", row)
	}
	if last.ReferenceID != row.ID {
		t.Errorf("the fare references %s, the journey is %s", last.ReferenceID, row.ID)
	}
	// 20m boarding + 400/200 h at a time scale of 1.
	if want := fixedNow.Add(2*time.Hour + 20*time.Minute); !row.ArrivesAt.Equal(want) {
		t.Errorf("arrives %s, want %s", row.ArrivesAt, want)
	}
	payload := string(h.uow.tx.outbox.records[0].Payload)
	for _, want := range []string{`"mode":"train"`, fmt.Sprintf(`"fare":%d`, trainFare), `"content_version":7`} {
		if !strings.Contains(payload, want) {
			t.Errorf("travel.started payload lacks %s: %s", want, payload)
		}
	}
}

func TestPrivateFareLeavesTheEconomy(t *testing.T) {
	h := newPhase1(t)
	withSink(h)
	p := h.player(303, "p-1", tehranID)
	h.uow.tx.ledger.give(t, application.AccountPlayerCash, p.ID, 5000)
	handler := h.travelHandler(t)

	if _, err := handler.Start(context.Background(), command("travel.start", 303, "req-1"), by("berlin", "flight", flightFare)); err != nil {
		t.Fatalf("departure: %v", err)
	}
	ledger := h.uow.tx.ledger
	if got := ledger.accounts[application.SystemSinkAccountID].Balance.Minor(); got != flightFare {
		t.Errorf("the sink received %d, want %d", got, flightFare)
	}
	if got := ledger.balance(application.AccountCityTreasury, tehranID); got != 0 {
		t.Errorf("a private fare reached the city's treasury: %d", got)
	}
	if last := ledger.posted[len(ledger.posted)-1]; last.Reason != application.ReasonTravelFare {
		t.Errorf("fare posted as %s, want travel_fare", last.Reason)
	}
	if ledger.total() != 0 {
		t.Errorf("the ledger no longer sums to zero: %d", ledger.total())
	}
}

func TestInsufficientFundsWritesNothing(t *testing.T) {
	h := newPhase1(t)
	p := h.player(304, "p-1", tehranID)
	h.uow.tx.ledger.give(t, application.AccountPlayerCash, p.ID, trainFare-1)
	handler := h.travelHandler(t)

	resp, err := handler.Start(context.Background(), command("travel.start", 304, "req-1"), by("berlin", "train", trainFare))
	if err != nil {
		t.Fatalf("a player who cannot pay is answered, not failed: %v", err)
	}
	assertResolved(t, resp.Text)
	if !hasExactButton(resp, "travel:options:berlin") {
		t.Errorf("the refusal offers no way back to the choice of transport: %v", buttons(resp))
	}
	if n := len(h.travels.started) + len(h.actions.scheduled) + len(h.uow.tx.outbox.records); n != 0 {
		t.Errorf("a refused departure wrote %d rows", n)
	}
	if got := h.uow.tx.ledger.balance(application.AccountPlayerCash, p.ID); got != trainFare-1 {
		t.Errorf("cash moved to %d on a refused departure", got)
	}
	if _, ok := h.stats.rows[p.ID]; ok && h.stats.rows[p.ID].Energy != 100 {
		t.Errorf("energy was spent on a refused departure: %d", h.stats.rows[p.ID].Energy)
	}
	// The key was rolled back too: paying and pressing again departs.
	h.uow.tx.ledger.give(t, application.AccountPlayerCash, p.ID, 1)
	if _, err := handler.Start(context.Background(), command("travel.start", 304, "req-1"), by("berlin", "train", trainFare)); err != nil {
		t.Fatal(err)
	}
	if len(h.travels.started) != 1 {
		t.Error("the same press, once affordable, did not depart")
	}
}

// Demand: each departure beyond the free one raises the next fare, and a
// price above what the player accepted is re-quoted, never charged.
func TestDemandRaisesTheFareAndAHigherPriceIsRequoted(t *testing.T) {
	h := newPhase1(t)
	handler := h.travelHandler(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		tg := int64(310 + i)
		p := h.player(tg, fmt.Sprintf("p-%d", i), tehranID)
		h.uow.tx.ledger.give(t, application.AccountPlayerCash, p.ID, 10_000)
		if _, err := handler.Start(ctx, command("travel.start", tg, "req"), by("berlin", "train", trainFare)); err != nil {
			t.Fatalf("departure %d: %v", i, err)
		}
	}

	// Two departures in the window, one free: the fare is now 110%.
	busy := int64(trainFare * 11000 / 10000)
	late := h.player(320, "p-late", tehranID)
	h.uow.tx.ledger.give(t, application.AccountPlayerCash, late.ID, 10_000)
	resp, err := handler.Start(ctx, command("travel.start", 320, "req"), by("berlin", "train", trainFare))
	if err != nil {
		t.Fatalf("requote: %v", err)
	}
	if !hasExactButton(resp, fmt.Sprintf("travel:start:berlin:train:%d", busy)) {
		t.Errorf("the requote does not offer the new price %d: %v", busy, buttons(resp))
	}
	if got := h.uow.tx.ledger.balance(application.AccountPlayerCash, late.ID); got != 10_000 {
		t.Errorf("a requoted departure charged %d", 10_000-got)
	}
	if len(h.travels.started) != 2 {
		t.Errorf("a requoted departure departed")
	}

	// Accepting the new price departs at it.
	if _, err := handler.Start(ctx, command("travel.start", 320, "req-2"), by("berlin", "train", busy)); err != nil {
		t.Fatal(err)
	}
	if got := h.uow.tx.ledger.balance(application.AccountPlayerCash, late.ID); got != 10_000-busy {
		t.Errorf("charged %d, want the accepted %d", 10_000-got, busy)
	}

	// Once the window has passed, the route is quiet again.
	h.now = fixedNow.Add(31 * time.Minute)
	quiet := h.player(321, "p-quiet", tehranID)
	h.uow.tx.ledger.give(t, application.AccountPlayerCash, quiet.ID, 10_000)
	// A ceiling above the price charges the price of now, not the ceiling.
	if _, err := handler.Start(ctx, command("travel.start", 321, "req"), by("berlin", "train", busy)); err != nil {
		t.Fatal(err)
	}
	if got := h.uow.tx.ledger.balance(application.AccountPlayerCash, quiet.ID); got != 10_000-trainFare {
		t.Errorf("after the window charged %d, want the plain fare %d", 10_000-got, trainFare)
	}
}

// The city's transit policy scales public fares only.
func TestCityPolicyScalesPublicFaresOnly(t *testing.T) {
	h := newPhase1(t)
	h.policy.value = 5000
	h.player(330, "p-1", tehranID)
	handler := h.travelHandler(t)

	resp, err := handler.Options(context.Background(), command("travel.options", 330, "req"), TravelOptionsRequest{City: "berlin"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasExactButton(resp, fmt.Sprintf("travel:start:berlin:train:%d", trainFare/2)) {
		t.Errorf("the train fare ignores the city's policy: %v", buttons(resp))
	}
	if !hasExactButton(resp, fmt.Sprintf("travel:start:berlin:flight:%d", flightFare)) {
		t.Errorf("the flight fare was moved by a city policy: %v", buttons(resp))
	}
}

func TestTimeScaleShortensTheRealWait(t *testing.T) {
	h := newPhase1(t)
	h.player(340, "p-1", tehranID)
	handler := NewTravelHandler(h.uow, h.ids, messages(t), h.cities, testTransport(t), h.policy,
		60, testArrivalXP, testIdempotencyTTL, h.clock())

	if _, err := handler.Start(context.Background(), command("travel.start", 340, "req"), depart("berlin")); err != nil {
		t.Fatal(err)
	}
	// 4h10m of game time is 4m10s of waiting at 60.
	row := h.travels.started[0]
	if want := fixedNow.Add(4*time.Minute + 10*time.Second); !row.ArrivesAt.Equal(want) {
		t.Errorf("arrives %s, want %s", row.ArrivesAt, want)
	}
	if !h.actions.scheduled[0].FinishAt.Equal(row.ArrivesAt) {
		t.Error("the arrival is scheduled at another time than the journey ends")
	}
}

func TestAModeThatDoesNotServeTheJourneyIsRefused(t *testing.T) {
	h := newPhase1(t)
	h.player(350, "p-1", tehranID)
	handler := h.travelHandler(t)

	for _, mode := range []string{"flight", "teleport"} {
		_, err := handler.Start(context.Background(), command("travel.start", 350, "req-"+mode), by("tokyo", mode, 1_000_000))
		if !stderrors.Is(err, travel.ErrModeUnavailable) {
			t.Errorf("%s to tokyo gave %v, want ErrModeUnavailable", mode, err)
		}
	}
	if len(h.travels.started) != 0 {
		t.Error("an unavailable mode departed")
	}
}

func TestArrivalCarriesTheMode(t *testing.T) {
	h := newPhase1(t)
	p := h.player(360, "p-1", tehranID)
	handler := h.travelHandler(t)
	ctx := context.Background()

	if _, err := handler.Start(ctx, command("travel.start", 360, "req"), depart("berlin")); err != nil {
		t.Fatal(err)
	}
	row := h.travels.started[0]
	h.now = row.ArrivesAt
	resp, err := handler.Complete(ctx, scheduled("travel.arrive", "req-arrive"), arrival(p.ID, row.ID))
	if err != nil || resp == nil {
		t.Fatalf("arrival: %v, %v", resp, err)
	}
	payload := string(h.uow.tx.outbox.records[len(h.uow.tx.outbox.records)-1].Payload)
	if !strings.Contains(payload, `"mode":"bus"`) {
		t.Errorf("travel.completed does not carry the mode: %s", payload)
	}
}
