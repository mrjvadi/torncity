package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"regexp"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Go, then do.
//
// A service is found at its place, so a player elsewhere in the city has to
// walk there first. Instead of telling them to open the map, the game offers
// ONE press that walks there and then does what they asked: the job screen's
// «🛠 شروع شیفت (۱۵ ثانیه پیاده‌روی تا محل کار + ۴ دقیقه کار)», a course's
// «🚶 رفتن به دانشگاه - ۲۰ ثانیه». The mechanism is the same for every
// feature:
//
//   - the walk is an ordinary walk (place.go): timed on the game clock, one
//     at a time, charged when it starts;
//   - it carries a follow-up — a command of the game and its arguments, with
//     the chat and the bot it was asked from — in its scheduled action's
//     payload;
//   - when the walk ends, place.arrive puts the player at the place and, in
//     the same transaction, writes the follow-up to the outbox as the command
//     itself, addressed to the player. The outbox publishes it onto the
//     command stream, the game runs it exactly as if the player had pressed
//     it there, and its reply reaches them as a new message.
//
// Exactly once: the arrival runs once per walk (its idempotency key is
// derived from the walk), the follow-up is appended only by the arrival that
// finished the walk, and the command it becomes carries an idempotency key
// derived from the walk, so a redelivery replays rather than repeats. A walk
// cut short (jail) never arrives, so its follow-up never runs. A follow-up
// that cannot run on arrival — no energy for the shift, jailed meanwhile —
// answers with the refusal screen and charges nothing.
//
// What may follow a walk is a closed table: a press is untrusted input, and
// only these commands, with these arguments, are run on the player's behalf.
// Each one checks everything again when it runs.

// FollowUp is what a walk does on arrival.
type FollowUp struct {
	// Command is the command to run, "job.work".
	Command string `json:"command"`
	// Payload holds its arguments by name.
	Payload map[string]string `json:"payload,omitempty"`
	// BotID, ChatID and ChatType are where the walk was asked from, which
	// is where the answer goes; UserID and Language are the player's.
	BotID    string `json:"bot_id"`
	ChatID   int64  `json:"chat_id"`
	ChatType string `json:"chat_type,omitempty"`
	UserID   int64  `json:"user_id"`
	Language string `json:"language,omitempty"`
}

// followUps is what may follow a walk: each command with the names of its
// positional arguments, in the order a button carries them — the same names
// internal/gateway/routing gives them (a test holds the two together).
var followUps = map[string][]string{
	"job.work":       nil,
	"education.list": nil,
	"education.view": {"course"},
	"shop.list":      nil,
	"shop.view":      {"shop"},
	"market.list":    nil,
	"market.book":    {"item"},
	"auction.list":   nil,
	"auction.view":   {"no"},
	"travel.options": {"city"},
	"election.list":  nil,
	"election.view":  {"no"},
	"crime.hub":      nil,
	"crime.view":     {"crime"},
	"bank.show":      nil,
	"company.type":   {"type"},
}

// FollowUpCommands lists the commands a walk may be followed by.
func FollowUpCommands() map[string][]string {
	out := make(map[string][]string, len(followUps))
	for k, v := range followUps {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// followUpArg is what one argument of a follow-up may look like: a content
// code, a public number. Anything else is not something a button of ours
// carries.
var followUpArg = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// followUpFrom reads a follow-up from a place.go request: the command and
// its positional arguments, checked against followUps. It is nil when the
// request names none; a named command outside the table, or arguments that
// do not fit it, are refused as bad input.
func followUpFrom(meta envelope.Metadata, then string, args []string) (*FollowUp, error) {
	then = strings.ToLower(strings.TrimSpace(then))
	if then == "" {
		return nil, nil
	}
	names, ok := followUps[then]
	if !ok || len(args) > len(names) {
		return nil, errors.InvalidInput("nothing of that kind follows a walk")
	}
	f := &FollowUp{
		Command: then, BotID: meta.BotID, ChatID: meta.TelegramChatID, ChatType: meta.ChatType,
		UserID: meta.TelegramUserID, Language: meta.Language,
	}
	for i, a := range args {
		if !followUpArg.MatchString(a) {
			return nil, errors.InvalidInput("a follow-up argument is malformed")
		}
		if f.Payload == nil {
			f.Payload = map[string]string{}
		}
		f.Payload[names[i]] = a
	}
	return f, nil
}

// followUpNote is the catalogue key of the line a started walk adds about
// what happens on arrival.
func followUpNote(f *FollowUp) string {
	switch {
	case f == nil:
		return ""
	case f.Command == "job.work":
		return "place.then.work"
	}
	return "place.then.open"
}

// PlaceActionPayload is the jsonb a walk writes onto its game_actions row: the
// row's columns repeated, as every scheduled action does, and the follow-up
// to run on arrival.
type PlaceActionPayload struct {
	ReferenceID string    `json:"reference_id"`
	PlayerID    string    `json:"player_id"`
	Then        *FollowUp `json:"then,omitempty"`
}

// walkStart is one walk to begin, with everything already checked that is
// the caller's to check: not travelling, not at work, not jailed, not
// already walking, the stats row locked.
type walkStart struct {
	player *application.Player
	// stats is the player's stats row, locked and caught up to now.
	stats application.Stats
	where whereabouts
	to    string
	then  *FollowUp
}

// errAlreadyThere is beginWalk's answer to a walk to where the player stands.
var errAlreadyThere = place.ErrAlreadyThere

// beginWalk starts a walk: the energy it costs, its scheduled end carrying
// the follow-up, the walk row and its event, all in the caller's unit of
// work. It is the one way a walk begins, from the map or from a feature that
// walks the player to its place first.
func beginWalk(ctx context.Context, tx application.Tx, ids IDGenerator, scale gametime.Scale,
	snap *content.Snapshot, meta envelope.Metadata, ws walkStart, now time.Time,
) (screens.WalkStartedView, error) {
	w, p := ws.where, ws.player
	mv, err := place.StartMove(w.cmap, w.here.Code, ws.to, now, scale)
	switch {
	case stderrors.Is(err, place.ErrAlreadyThere):
		return screens.WalkStartedView{}, errAlreadyThere
	case stderrors.Is(err, place.ErrUnknownPlace):
		return screens.WalkStartedView{}, errors.NotFound("no such place in this city").WithCause(err)
	case err != nil:
		return screens.WalkStartedView{}, errors.Internal(err)
	}
	spent, err := domainStats(ws.stats).SpendEnergy(mv.Energy)
	if err != nil {
		return screens.WalkStartedView{}, errors.InvalidInput("not enough energy to walk there").
			WithCause(err).WithDetail("needed", mv.Energy).WithDetail("current", ws.stats.Energy)
	}
	next := storedStats(ws.stats, spent)
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = now
	}
	if err := tx.Stats().Save(ctx, next); err != nil {
		return screens.WalkStartedView{}, err
	}

	moveID, actionID := ids.NewID(), ids.NewID()
	payload, err := json.Marshal(PlaceActionPayload{ReferenceID: moveID, PlayerID: p.ID, Then: ws.then})
	if err != nil {
		return screens.WalkStartedView{}, err
	}
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: actionID, ActionType: application.PlaceMoveActionType, ActorType: "player", ActorID: p.ID,
		ReferenceType: application.PlaceMoveReference, ReferenceID: moveID, Payload: payload,
		StartedAt: mv.StartedAt, FinishAt: mv.ArrivesAt,
	}); err != nil {
		return screens.WalkStartedView{}, err
	}
	if err := tx.Places().StartMove(ctx, application.PlaceMove{
		ID: moveID, PlayerID: p.ID, CityID: w.city.ID, From: mv.From, To: mv.To, Energy: mv.Energy,
		GameActionID: actionID, StartedAt: mv.StartedAt, ArrivesAt: mv.ArrivesAt,
	}); err != nil {
		return screens.WalkStartedView{}, err
	}
	fields := map[string]any{
		"move_id": moveID, "player_id": p.ID, "city_id": w.city.ID, "from": mv.From, "to": mv.To,
		"energy": mv.Energy, "arrives_at": mv.ArrivesAt, "content_version": snap.Version(),
	}
	if ws.then != nil {
		fields["then"] = ws.then.Command
	}
	ev, err := events.New("place.walk_started", "place_move", moveID, fields)
	if err != nil {
		return screens.WalkStartedView{}, err
	}
	if err := tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("place", "walk_started"), Metadata: meta, Payload: ev.Payload,
	}); err != nil {
		return screens.WalkStartedView{}, err
	}
	return screens.WalkStartedView{
		To: placeNamed(snap, mv.To), From: placeNamed(snap, mv.From),
		Duration: mv.Duration(), ArrivesAt: mv.ArrivesAt, Energy: mv.Energy,
		Then: followUpNote(ws.then),
	}, nil
}

// followUpIdempotencyPrefix marks the idempotency key of a command a walk
// ran on arrival; the walk's id follows it.
const followUpIdempotencyPrefix = "place-then:"

// dispatchFollowUp writes a walk's follow-up to the outbox as the command
// itself, addressed to the player in the chat the walk was asked from. It
// runs inside the arrival's unit of work, so the command is published if and
// only if the walk arrived.
func dispatchFollowUp(ctx context.Context, tx application.Tx, ids IDGenerator, arrival envelope.Metadata,
	playerID, moveID string, f *FollowUp, now time.Time,
) error {
	if f == nil {
		return nil
	}
	names, ok := followUps[f.Command]
	if !ok || f.BotID == "" || f.UserID == 0 || f.ChatID == 0 {
		// A payload this code did not write, or one from before the table
		// changed: the arrival stands, nothing follows it.
		return nil
	}
	domain, action, _ := strings.Cut(f.Command, ".")
	payload := map[string]any{}
	for _, name := range names {
		if v, ok := f.Payload[name]; ok && followUpArg.MatchString(v) {
			payload[name] = v
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	chatType := f.ChatType
	if chatType == "" && f.ChatID == f.UserID {
		chatType = "private"
	}
	meta := envelope.Metadata{
		RequestID:      ids.NewID(),
		TraceID:        arrival.TraceID,
		PlayerID:       playerID,
		TelegramUserID: f.UserID,
		TelegramChatID: f.ChatID,
		BotID:          f.BotID,
		ChatType:       chatType,
		UpdateType:     "follow_up",
		Command:        f.Command,
		Action:         action,
		Language:       f.Language,
		IdempotencyKey: followUpIdempotencyPrefix + moveID,
		ReceivedAt:     now,
		SchemaVersion:  envelope.SchemaVersion,
	}
	if meta.TraceID == "" {
		meta.TraceID = meta.RequestID
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ids.NewID(), Subject: subjects.Command(domain, action), Metadata: meta, Payload: body,
	})
}

// thenFor fills in what a refusal's walk button follows up with: the screen
// the player was on, opened again once they are there.
func thenFor(err error, command string, args ...string) error {
	if n, ok := err.(*notHere); ok && !n.view.Walking {
		if _, known := followUps[command]; known {
			n.view.Then, n.view.ThenArgs = command, args
		}
	}
	return err
}

// wayTo is the walk to the city's place offering s, for a screen to offer
// when the player is elsewhere: nil when they are there, on the way
// somewhere, or in a city without the place.
func wayTo(w whereabouts, snap *content.Snapshot, s place.Service, scale gametime.Scale) *screens.Way {
	if !w.placed() || w.walk != nil {
		return nil
	}
	target, ok := w.cmap.ForService(s)
	if !ok || target.Code == w.here.Code {
		return nil
	}
	return &screens.Way{Place: placeNamed(snap, target.Code), Walk: scale.RealWait(target.MoveTime)}
}
