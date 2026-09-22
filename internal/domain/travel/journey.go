// Package travel holds the rules of moving a player between cities: what a
// journey is, what it costs, how long it takes and which state changes are
// legal once it is under way.
//
// TIME IS AN INPUT, NEVER A WAIT. There are no timers here, no goroutines and
// nothing that sleeps. Every function that cares what time it is takes a
// now time.Time argument and answers for that instant. Scheduling a journey's
// completion is infrastructure's job — docs/database.md puts it in
// game_actions, with the Redis sorted set as an accelerator — and a domain
// package that started its own timer would produce a second, invisible
// schedule that disappears on restart and disagrees with the stored one.
// Everything here can therefore be tested at any point in the future or the
// past without a clock running.
//
// RULES LIVE HERE, CONTENT DOES NOT. Which speeds exist and how a fare is
// derived from distance and speed are rules. The numbers they use — the route
// network, how fast each speed actually is, what it charges — are content,
// authored outside the code and injected as values (see world.Routes and
// Tariff in plan.go). This package defines no city, no route and no default
// price list.
package travel

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors from the journey rules.
var (
	// ErrIllegalTransition means a status change is not allowed from the
	// journey's current status. It is an error rather than a silent no-op:
	// something tried to arrive a cancelled trip or cancel an arrived one,
	// and swallowing that would leave the caller believing it had happened.
	ErrIllegalTransition = errors.New("travel: illegal journey status transition")

	// ErrUnknownStatus means a status value is not one of the three below,
	// which is what reading a corrupt or newer row looks like.
	ErrUnknownStatus = errors.New("travel: unknown journey status")

	// ErrMissingCity means a journey was built without one of its endpoints.
	ErrMissingCity = errors.New("travel: journey needs both a source and a destination city")

	// ErrArrivalBeforeDeparture means a journey would arrive before it left.
	ErrArrivalBeforeDeparture = errors.New("travel: arrival time is before departure time")
)

// Status is the state of a journey, matching travels.status in
// docs/database.md; the string values are the ones stored, so renaming one is
// a migration rather than a refactor.
//
// The constants are named StatusX rather than X because Arrived is also the
// name of the function that reports whether a journey has landed, and a
// package cannot hold both. The prefix is the smaller cost of the two.
type Status string

const (
	StatusInTransit Status = "in_transit"
	StatusArrived   Status = "arrived"
	StatusCancelled Status = "cancelled"
)

// Validate rejects a status that is not one of the three known values.
func (s Status) Validate() error {
	switch s {
	case StatusInTransit, StatusArrived, StatusCancelled:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownStatus, string(s))
}

// CanTransitionTo reports whether a journey in status s may move to next.
//
// There is exactly one live state. A journey in transit either arrives or is
// cancelled; both are terminal and nothing leaves them. In particular:
//
//   - a transition to the same status is NOT legal. Re-arriving an arrived
//     journey is a duplicate delivery, not a harmless repeat, and the
//     at-least-once consumer that might do it must be told so it can treat
//     the second attempt as the no-op it is, at the layer that owns
//     idempotency.
//   - nothing transitions INTO in_transit. A journey is born in transit, by
//     Plan; there is no path back to it from anywhere.
func (s Status) CanTransitionTo(next Status) bool {
	if s == StatusInTransit {
		return next == StatusArrived || next == StatusCancelled
	}
	return false
}

// Journey is one player's trip between two cities, mirroring the columns of
// the travels table that carry rules.
//
// player_id, vehicle_id and game_action_id are absent on purpose: they are
// foreign keys that belong to the row, not to the rule. The city fields are
// the storage identifiers, kept as strings because this package never looks
// inside one.
type Journey struct {
	FromCityID string
	ToCityID   string
	DepartedAt time.Time
	ArrivesAt  time.Time
	Status     Status
}

// Duration is how long the journey takes end to end.
func (j Journey) Duration() time.Duration { return j.ArrivesAt.Sub(j.DepartedAt) }

// Validate checks a journey that came from somewhere other than Plan, such as
// a row read back out of storage.
func (j Journey) Validate() error {
	if j.FromCityID == "" || j.ToCityID == "" {
		return ErrMissingCity
	}
	if j.FromCityID == j.ToCityID {
		return fmt.Errorf("%w: %q", ErrSameCity, j.FromCityID)
	}
	if err := j.Status.Validate(); err != nil {
		return err
	}
	if j.ArrivesAt.Before(j.DepartedAt) {
		return ErrArrivalBeforeDeparture
	}
	return nil
}

// TransitionTo returns the journey in its new status, or ErrIllegalTransition.
//
// The returned journey is a new value; the original is untouched, so a refused
// transition cannot leave a half-changed journey behind.
func (j Journey) TransitionTo(next Status) (Journey, error) {
	if err := next.Validate(); err != nil {
		return j, err
	}
	if !j.Status.CanTransitionTo(next) {
		return j, fmt.Errorf("%w: %q -> %q", ErrIllegalTransition, string(j.Status), string(next))
	}
	moved := j
	moved.Status = next
	return moved, nil
}

// Arrived reports whether the journey has reached its destination as of now.
//
// A journey in transit has arrived once now is at or past ArrivesAt; the
// instant of arrival counts as arrived, so a scheduler that fires exactly on
// time is not told to wait another tick. A journey already marked arrived
// answers true whatever the clock says, and a cancelled one answers false: it
// never got there and never will.
//
// This only reports a fact about time. Moving the player, writing the row and
// publishing the event are the outer layer's work, driven by game_actions.
func Arrived(j Journey, now time.Time) bool {
	switch j.Status {
	case StatusArrived:
		return true
	case StatusInTransit:
		return !now.Before(j.ArrivesAt)
	}
	return false
}

// Remaining reports how much of the journey is left as of now, floored at
// zero. It is display text's input, and it never goes negative because "minus
// four minutes remaining" is not something to show a player.
func Remaining(j Journey, now time.Time) time.Duration {
	if Arrived(j, now) || j.Status == StatusCancelled {
		return 0
	}
	if left := j.ArrivesAt.Sub(now); left > 0 {
		return left
	}
	return 0
}
