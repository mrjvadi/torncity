package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Go, then do (places_then.go): one press walks the player to where a thing
// is done and does it on arrival, exactly once.

// Every follow-up is a command a player may send, and its arguments carry
// the names the gateway gives them, so the command the arrival publishes
// decodes like the one a button would have sent.
func TestFollowUpsSpeakTheGatewaysLanguage(t *testing.T) {
	for command, names := range FollowUpCommands() {
		if !commands.FromPlayerCommand(command) {
			t.Errorf("%s follows a walk but is no command a player may send", command)
			continue
		}
		domain, action, _ := strings.Cut(command, ".")
		parts := []string{domain, action}
		for range names {
			parts = append(parts, "x")
		}
		got, payload, err := routing.ParseCallbackData(strings.Join(parts, ":"))
		if err != nil || got != command {
			t.Errorf("%s: parsed as %q, %v", command, got, err)
			continue
		}
		for _, name := range names {
			if payload[name] != "x" {
				t.Errorf("%s: argument %q is named %v by the gateway", command, name, payload)
			}
		}
	}
	// place.go carries the follow-up as its second argument.
	_, payload, err := routing.ParseCallbackData("place:go:university:education.view:first_aid")
	if err != nil || payload["place"] != "university" || payload["then"] != "education.view" {
		t.Fatalf("place.go payload = %v, %v", payload, err)
	}
	if args, _ := payload["args"].([]string); len(args) != 1 || args[0] != "first_aid" {
		t.Fatalf("place.go args = %v", payload["args"])
	}
}

// A walk may be followed only by what the table lists, with arguments a
// button of ours carries.
func TestFollowUpIsCheckedAgainstTheTable(t *testing.T) {
	m := meta("bot-1", 77, "req")
	for _, bad := range []struct {
		then string
		args []string
	}{
		{"bank.pay.send", nil},
		{"job.work", []string{"x"}},
		{"education.view", []string{"a b"}},
		{"shop.view", []string{"x", "y"}},
	} {
		if f, err := followUpFrom(m, bad.then, bad.args); err == nil {
			t.Errorf("followUpFrom(%q, %v) = %+v, want refused", bad.then, bad.args, f)
		}
	}
	f, err := followUpFrom(m, "education.view", []string{"first_aid"})
	if err != nil || f.Command != "education.view" || f.Payload["course"] != "first_aid" || f.BotID != "bot-1" || f.UserID != 77 {
		t.Fatalf("followUpFrom = %+v, %v", f, err)
	}
	if f, err := followUpFrom(m, "", nil); f != nil || err != nil {
		t.Errorf("no follow-up = %+v, %v", f, err)
	}
}

// The owner's report: starting a shift from another place left the player
// where they were. Now the start walks them to the workplace; the arrival
// publishes job.work for them, once; the shift starts there, and they stand
// at the workplace.
func TestAShiftFromAnotherPlaceWalksThereAndStartsOnArrival(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	places := NewPlacesHandler(h.uow, &seqIDs{}, messages(t), h.jobs.content, h.jobs.cities, workScale, testIdempotencyTTL,
		func() time.Time { return h.now })
	// The player stands at the university quarter; retail is worked in the
	// business district.
	h.uow.tx.places.at[h.player.ID] = "university"

	status, err := h.jobs.Status(ctx, h.meta("req-status", "job.status"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(workText(status), "15s walk to work + 4m of work") {
		t.Errorf("job screen = %q, want the walk and the shift on its button", workText(status))
	}

	walk, err := h.jobs.Work(ctx, h.meta("req-work", "job.work"))
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if !strings.Contains(walk.Text, "business district") || !strings.Contains(walk.Text, "shift starts as soon as you arrive") {
		t.Errorf("walk screen = %q", walk.Text)
	}
	if len(h.uow.w.jobs.working) != 0 {
		t.Fatal("a shift started before the player reached the workplace")
	}
	if e := h.uow.tx.stats.rows[h.player.ID].Energy; e != 100 {
		t.Errorf("energy after setting off = %d, want nothing of the shift charged yet", e)
	}
	move, ok := h.uow.tx.places.moving[h.player.ID]
	if !ok || move.To != "business_district" {
		t.Fatalf("walk = %+v, %v", move, ok)
	}
	var action application.GameAction
	for _, a := range h.uow.tx.actions.scheduled {
		if a.ReferenceID == move.ID {
			action = a
		}
	}

	// The scheduler ends the walk: twice, as a redelivery would.
	h.now = move.ArrivesAt.Add(time.Second)
	before := len(h.uow.tx.outbox.records)
	for i := 0; i < 2; i++ {
		sched := h.meta("req-arrive", "place.arrive")
		sched.TelegramUserID = 0
		if _, err := places.Arrive(ctx, sched, PlaceScheduledRequest{
			ActorID: h.player.ID, ReferenceType: application.PlaceMoveReference, ReferenceID: move.ID, Payload: action.Payload,
		}); err != nil {
			t.Fatalf("Arrive #%d: %v", i+1, err)
		}
	}
	if at := h.uow.tx.places.at[h.player.ID]; at != "business_district" {
		t.Fatalf("after the walk the player stands at %q", at)
	}
	var follow []application.OutboxRecord
	for _, r := range h.uow.tx.outbox.records[before:] {
		if r.Subject == "game.command.job.work.v1" {
			follow = append(follow, r)
		}
	}
	if len(follow) != 1 {
		t.Fatalf("job.work published %d times on arrival, want once", len(follow))
	}
	fm := follow[0].Metadata
	if err := fm.Validate(); err != nil || fm.Command != "job.work" || fm.TelegramUserID != workTelegramID ||
		fm.BotID != "bot-1" || fm.PlayerID != h.player.ID || !strings.HasPrefix(fm.IdempotencyKey, followUpIdempotencyPrefix) {
		t.Fatalf("follow-up metadata = %+v, %v", fm, err)
	}

	// The game runs the published command: the shift starts, at the
	// workplace, and a second delivery of it starts nothing more.
	for i := 0; i < 2; i++ {
		if _, err := h.jobs.Work(ctx, fm); err != nil {
			t.Fatalf("follow-up job.work #%d: %v", i+1, err)
		}
	}
	if len(h.uow.w.jobs.working) != 1 {
		t.Fatalf("shifts working = %d, want one", len(h.uow.w.jobs.working))
	}
	if e := h.uow.tx.stats.rows[h.player.ID].Energy; e != 85 {
		t.Errorf("energy after the shift started = %d, want 85", e)
	}
	if _, err := h.finishShift(t, "req-finish"); err != nil {
		t.Fatal(err)
	}
	if at := h.uow.tx.places.at[h.player.ID]; at != "business_district" {
		t.Errorf("after the shift the player stands at %q, want the workplace", at)
	}
}

// A shift that could not start is refused before any walk: nothing is
// charged, nobody moves.
func TestAShiftThatCannotStartDoesNotWalk(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	if _, err := h.jobs.Apply(ctx, h.meta("req-apply", "job.apply"), JobRequest{Role: "retail"}); err != nil {
		t.Fatal(err)
	}
	row := h.uow.tx.stats.rows[h.player.ID]
	row.Energy = 5
	h.uow.tx.stats.rows[h.player.ID] = row
	// The refusal is the energy one the game shows for any shift.
	_, err := h.jobs.Work(ctx, h.meta("req-work", "job.work"))
	if err == nil || !strings.Contains(err.Error(), "energy") {
		t.Fatalf("Work = %v, want the energy refusal", err)
	}
	if _, walking := h.uow.tx.places.moving[h.player.ID]; walking {
		t.Fatal("set off for a shift without the energy for it")
	}
}

// A walk's follow-up rides on its scheduled action and nowhere else.
func TestAWalkCarriesItsFollowUpInItsAction(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	places := NewPlacesHandler(h.uow, &seqIDs{}, messages(t), h.jobs.content, h.jobs.cities, workScale, testIdempotencyTTL,
		func() time.Time { return h.now })
	h.uow.tx.places.at[h.player.ID] = ""
	m := h.meta("req-go", "place.go")
	if _, err := places.Go(ctx, m, PlaceRequest{Place: "university", Then: "education.view", Args: []string{"first_aid"}}); err != nil {
		t.Fatal(err)
	}
	if len(h.uow.tx.actions.scheduled) != 1 {
		t.Fatalf("scheduled %d actions", len(h.uow.tx.actions.scheduled))
	}
	var p PlaceActionPayload
	if err := json.Unmarshal(h.uow.tx.actions.scheduled[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Then == nil || p.Then.Command != "education.view" || p.Then.Payload["course"] != "first_aid" || p.Then.ChatID == 0 {
		t.Fatalf("follow-up = %+v", p.Then)
	}
	// A follow-up outside the table is refused and nothing moves.
	h2 := newWorkHarness(t)
	places2 := NewPlacesHandler(h2.uow, &seqIDs{}, messages(t), h2.jobs.content, h2.jobs.cities, workScale, testIdempotencyTTL,
		func() time.Time { return h2.now })
	if _, err := places2.Go(ctx, h2.meta("req-go", "place.go"), PlaceRequest{Place: "bazaar", Then: "bank.pay.send"}); err == nil {
		t.Error("a walk followed by a payment was accepted")
	}
	if len(h2.uow.tx.places.moving) != 0 {
		t.Error("a refused walk moved the player")
	}
}

// Already there: what was to follow the walk runs at once.
func TestAWalkToWhereThePlayerStandsRunsTheFollowUpNow(t *testing.T) {
	h := newWorkHarness(t)
	ctx := context.Background()
	var ran string
	places := NewPlacesHandler(h.uow, &seqIDs{}, messages(t), h.jobs.content, h.jobs.cities, workScale, testIdempotencyTTL,
		func() time.Time { return h.now }).WithRunner(func(_ context.Context, m envelope.Metadata, command string, payload map[string]string) (*presenter.Response, error) {
		ran = command + ":" + payload["course"] + ":" + m.Command
		return nil, nil
	})
	if _, err := places.Go(ctx, h.meta("req-go", "place.go"), PlaceRequest{Place: "university", Then: "education.view", Args: []string{"first_aid"}}); err != nil {
		t.Fatal(err)
	}
	if ran != "education.view:first_aid:education.view" {
		t.Errorf("ran %q", ran)
	}
}
