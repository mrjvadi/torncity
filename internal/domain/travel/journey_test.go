package travel

import (
	"errors"
	"testing"
	"time"
)

// allStatuses includes a value that is not a real status, because reading a
// corrupt or newer row is one of the ways an illegal transition arrives.
var allStatuses = []Status{StatusInTransit, StatusArrived, StatusCancelled, Status("boarding"), Status("")}

func TestStatusValidate(t *testing.T) {
	tests := []struct {
		status  Status
		wantErr error
	}{
		{StatusInTransit, nil},
		{StatusArrived, nil},
		{StatusCancelled, nil},
		{Status(""), ErrUnknownStatus},
		{Status("in transit"), ErrUnknownStatus},
		{Status("IN_TRANSIT"), ErrUnknownStatus},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if err := tt.status.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Status(%q).Validate() = %v, want %v", string(tt.status), err, tt.wantErr)
			}
		})
	}
}

// TestCanTransitionTo walks every ordered pair of statuses, legal and not. The
// exhaustive form is the point: a new status added later fails this test until
// somebody states what it may become.
func TestCanTransitionTo(t *testing.T) {
	legal := map[Status]map[Status]bool{
		StatusInTransit: {StatusArrived: true, StatusCancelled: true},
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			want := legal[from][to]
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("Status(%q).CanTransitionTo(%q) = %v, want %v",
					string(from), string(to), got, want)
			}
		}
	}
}

func TestTransitionTo(t *testing.T) {
	base := Journey{
		FromCityID: "city-a",
		ToCityID:   "city-b",
		DepartedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		ArrivesAt:  time.Date(2026, 3, 1, 13, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name    string
		from    Status
		to      Status
		wantErr error
	}{
		{name: "arriving", from: StatusInTransit, to: StatusArrived},
		{name: "cancelling", from: StatusInTransit, to: StatusCancelled},
		{
			name: "arriving twice", from: StatusArrived, to: StatusArrived,
			wantErr: ErrIllegalTransition,
		},
		{
			name: "cancelling an arrived journey", from: StatusArrived, to: StatusCancelled,
			wantErr: ErrIllegalTransition,
		},
		{
			name: "arriving a cancelled journey", from: StatusCancelled, to: StatusArrived,
			wantErr: ErrIllegalTransition,
		},
		{
			name: "cancelling twice", from: StatusCancelled, to: StatusCancelled,
			wantErr: ErrIllegalTransition,
		},
		{
			name: "restarting an arrived journey", from: StatusArrived, to: StatusInTransit,
			wantErr: ErrIllegalTransition,
		},
		{
			name: "restarting a cancelled journey", from: StatusCancelled, to: StatusInTransit,
			wantErr: ErrIllegalTransition,
		},
		{
			name: "re-entering transit from transit", from: StatusInTransit, to: StatusInTransit,
			wantErr: ErrIllegalTransition,
		},
		{
			name: "moving to something that is not a status", from: StatusInTransit, to: Status("lost"),
			wantErr: ErrUnknownStatus,
		},
		{
			name: "moving from a status that is not a status", from: Status("lost"), to: StatusArrived,
			wantErr: ErrIllegalTransition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := base
			start.Status = tt.from

			got, err := start.TransitionTo(tt.to)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("TransitionTo(%q) from %q = %v, want %v",
					string(tt.to), string(tt.from), err, tt.wantErr)
			}

			wantStatus := tt.to
			if tt.wantErr != nil {
				// A refused transition must leave the journey exactly as it
				// was; a half-applied state change is the failure mode this
				// value-semantics design exists to prevent.
				wantStatus = tt.from
			}
			if got.Status != wantStatus {
				t.Errorf("status after the call = %q, want %q", string(got.Status), string(wantStatus))
			}
			if start.Status != tt.from {
				t.Errorf("receiver mutated: %q, want %q", string(start.Status), string(tt.from))
			}
		})
	}
}

func TestArrived(t *testing.T) {
	depart := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	arrive := depart.Add(90 * time.Minute)

	tests := []struct {
		name   string
		status Status
		now    time.Time
		want   bool
	}{
		{name: "still travelling", status: StatusInTransit, now: arrive.Add(-time.Second)},
		{name: "exactly on time counts as arrived", status: StatusInTransit, now: arrive, want: true},
		{name: "the scheduler was late", status: StatusInTransit, now: arrive.Add(time.Hour), want: true},
		{name: "before departure", status: StatusInTransit, now: depart.Add(-time.Hour)},
		{name: "already marked arrived", status: StatusArrived, now: depart, want: true},
		{name: "cancelled journeys never arrive", status: StatusCancelled, now: arrive.Add(time.Hour)},
		{name: "an unknown status never arrives", status: Status("lost"), now: arrive.Add(time.Hour)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := Journey{
				FromCityID: "city-a", ToCityID: "city-b",
				DepartedAt: depart, ArrivesAt: arrive, Status: tt.status,
			}
			if got := Arrived(j, tt.now); got != tt.want {
				t.Errorf("Arrived() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemaining(t *testing.T) {
	depart := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	arrive := depart.Add(2 * time.Hour)

	tests := []struct {
		name   string
		status Status
		now    time.Time
		want   time.Duration
	}{
		{name: "half way", status: StatusInTransit, now: depart.Add(time.Hour), want: time.Hour},
		{name: "at departure", status: StatusInTransit, now: depart, want: 2 * time.Hour},
		{name: "on arrival", status: StatusInTransit, now: arrive, want: 0},
		{name: "overdue never goes negative", status: StatusInTransit, now: arrive.Add(time.Hour), want: 0},
		{name: "cancelled", status: StatusCancelled, now: depart, want: 0},
		{name: "arrived", status: StatusArrived, now: depart, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := Journey{
				FromCityID: "city-a", ToCityID: "city-b",
				DepartedAt: depart, ArrivesAt: arrive, Status: tt.status,
			}
			if got := Remaining(j, tt.now); got != tt.want {
				t.Errorf("Remaining() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestJourneyValidate(t *testing.T) {
	depart := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	valid := Journey{
		FromCityID: "city-a",
		ToCityID:   "city-b",
		DepartedAt: depart,
		ArrivesAt:  depart.Add(time.Hour),
		Status:     StatusInTransit,
	}

	tests := []struct {
		name    string
		mutate  func(Journey) Journey
		wantErr error
	}{
		{"a well formed journey", func(j Journey) Journey { return j }, nil},
		{"instant arrival is allowed", func(j Journey) Journey { j.ArrivesAt = j.DepartedAt; return j }, nil},
		{"no origin", func(j Journey) Journey { j.FromCityID = ""; return j }, ErrMissingCity},
		{"no destination", func(j Journey) Journey { j.ToCityID = ""; return j }, ErrMissingCity},
		{"a trip to itself", func(j Journey) Journey { j.ToCityID = j.FromCityID; return j }, ErrSameCity},
		{"an unknown status", func(j Journey) Journey { j.Status = "lost"; return j }, ErrUnknownStatus},
		{
			"arriving before departing",
			func(j Journey) Journey { j.ArrivesAt = j.DepartedAt.Add(-time.Minute); return j },
			ErrArrivalBeforeDeparture,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.mutate(valid).Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
