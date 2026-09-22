package world

import (
	"errors"
	"fmt"
	"testing"
)

// testEdges is the small network every test in this file works from. It is
// declared here, in the test, because route content is injected rather than
// built in: if this package ever grows a route table of its own, these tests
// stop proving that the injection works.
//
//	alpha --100-- bravo --150-- charlie
//	  \______________400______________/
//
//	delta --50-- echo          (a second, unconnected component)
func testEdges() []Edge {
	return []Edge{
		{From: "alpha", To: "bravo", Distance: 100},
		{From: "bravo", To: "charlie", Distance: 150},
		{From: "alpha", To: "charlie", Distance: 400},
		{From: "delta", To: "echo", Distance: 50},
	}
}

func mustRoutes(t *testing.T, edges []Edge) Routes {
	t.Helper()
	r, err := NewRoutes(edges)
	if err != nil {
		t.Fatalf("NewRoutes: %v", err)
	}
	return r
}

func TestDistance(t *testing.T) {
	r := mustRoutes(t, testEdges())

	tests := []struct {
		name    string
		from    string
		to      string
		want    int
		wantErr error
	}{
		{name: "a direct route", from: "alpha", to: "bravo", want: 100},
		{name: "the same route backwards", from: "bravo", to: "alpha", want: 100},
		{
			name: "a detour beats the direct route",
			from: "alpha", to: "charlie", want: 250,
			// The direct edge says 400; alpha->bravo->charlie is 250, and the
			// shortest path is what travel is priced on, so no player can
			// save money by booking the trip in two legs.
		},
		{name: "and backwards", from: "charlie", to: "alpha", want: 250},
		{name: "a city is zero from itself", from: "bravo", to: "bravo", want: 0},
		{name: "the other component", from: "delta", to: "echo", want: 50},
		{
			name: "no route between components",
			from: "alpha", to: "delta", wantErr: ErrNoRoute,
		},
		{
			name: "unknown origin",
			from: "nowhere", to: "alpha", wantErr: ErrUnknownCity,
		},
		{
			name: "unknown destination",
			from: "alpha", to: "nowhere", wantErr: ErrUnknownCity,
		},
		{
			name: "empty code is just an unknown city",
			from: "", to: "alpha", wantErr: ErrUnknownCity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Distance(City{Code: tt.from}, City{Code: tt.to})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Distance(%q,%q) error = %v, want %v", tt.from, tt.to, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Distance(%q,%q) = %d, want %d", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestNewRoutesRejectsBadContent(t *testing.T) {
	tests := []struct {
		name    string
		edges   []Edge
		wantErr error
	}{
		{
			name:  "no content at all is valid, just empty",
			edges: nil,
		},
		{
			name:    "missing origin code",
			edges:   []Edge{{From: "", To: "bravo", Distance: 10}},
			wantErr: ErrMissingEdgeCode,
		},
		{
			name:    "missing destination code",
			edges:   []Edge{{From: "alpha", To: "", Distance: 10}},
			wantErr: ErrMissingEdgeCode,
		},
		{
			name:    "a city routed to itself",
			edges:   []Edge{{From: "alpha", To: "alpha", Distance: 10}},
			wantErr: ErrSelfRoute,
		},
		{
			name:    "zero distance would make two cities the same place",
			edges:   []Edge{{From: "alpha", To: "bravo", Distance: 0}},
			wantErr: ErrInvalidEdgeDistance,
		},
		{
			name:    "negative distance",
			edges:   []Edge{{From: "alpha", To: "bravo", Distance: -5}},
			wantErr: ErrInvalidEdgeDistance,
		},
		{
			name:    "an obviously mistyped distance",
			edges:   []Edge{{From: "alpha", To: "bravo", Distance: MaxEdgeDistance + 1}},
			wantErr: ErrInvalidEdgeDistance,
		},
		{
			name:  "the largest allowed distance is accepted",
			edges: []Edge{{From: "alpha", To: "bravo", Distance: MaxEdgeDistance}},
		},
		{
			name: "the same pair twice",
			edges: []Edge{
				{From: "alpha", To: "bravo", Distance: 100},
				{From: "alpha", To: "bravo", Distance: 120},
			},
			wantErr: ErrDuplicateRoute,
		},
		{
			name: "the same pair twice, written the other way round",
			edges: []Edge{
				{From: "alpha", To: "bravo", Distance: 100},
				{From: "bravo", To: "alpha", Distance: 100},
			},
			wantErr: ErrDuplicateRoute,
			// Routes are bidirectional, so writing both directions is not a
			// way of stating the same thing twice: it is the mistake that
			// would otherwise let the two drift apart unnoticed.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRoutes(tt.edges)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("NewRoutes() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewRoutesRejectsTooManyCities(t *testing.T) {
	edges := make([]Edge, 0, MaxCities)
	for i := 0; i <= MaxCities; i++ {
		edges = append(edges, Edge{
			From:     fmt.Sprintf("city-%04d", i),
			To:       fmt.Sprintf("city-%04d", i+1),
			Distance: 10,
		})
	}
	if _, err := NewRoutes(edges); !errors.Is(err, ErrTooManyCities) {
		t.Errorf("NewRoutes() with %d cities = %v, want ErrTooManyCities", len(edges)+1, err)
	}
}

// TestRoutesAreDeterministic proves the same content always produces the same
// distances whatever order the file listed the routes in. Without it, a reload
// could silently reprice every journey in the game.
func TestRoutesAreDeterministic(t *testing.T) {
	forward := mustRoutes(t, testEdges())

	reversed := testEdges()
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	backward := mustRoutes(t, reversed)

	for _, from := range forward.Codes() {
		for _, to := range forward.Codes() {
			a, errA := forward.DistanceBetween(from, to)
			b, errB := backward.DistanceBetween(from, to)
			if a != b || (errA == nil) != (errB == nil) {
				t.Errorf("%q->%q: %d (%v) from one ordering, %d (%v) from the other",
					from, to, a, errA, b, errB)
			}
		}
	}
}

// TestDistanceObeysTriangleInequality guards the property that makes a fare
// derived from distance impossible to undercut by splitting a trip.
func TestDistanceObeysTriangleInequality(t *testing.T) {
	r := mustRoutes(t, testEdges())
	codes := r.Codes()

	for _, a := range codes {
		for _, b := range codes {
			for _, c := range codes {
				ab, err1 := r.DistanceBetween(a, b)
				bc, err2 := r.DistanceBetween(b, c)
				ac, err3 := r.DistanceBetween(a, c)
				if err1 != nil || err2 != nil || err3 != nil {
					continue // a pair in the other component; nothing to compare
				}
				if ac > ab+bc {
					t.Errorf("%q->%q is %d, but going via %q is only %d", a, c, ac, b, ab+bc)
				}
			}
		}
	}
}

func TestRoutesMembership(t *testing.T) {
	r := mustRoutes(t, testEdges())

	if !r.Has("alpha") {
		t.Error("Has(alpha) = false for a city that is in the content")
	}
	if r.Has("nowhere") {
		t.Error("Has(nowhere) = true for a city that is not")
	}
	if r.IsEmpty() {
		t.Error("IsEmpty() = true for a loaded network")
	}

	want := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	got := r.Codes()
	if len(got) != len(want) {
		t.Fatalf("Codes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Codes() = %v, want %v in sorted order", got, want)
		}
	}

	got[0] = "tampered"
	if r.Codes()[0] != "alpha" {
		t.Error("writing into the slice from Codes() changed the loaded network")
	}
}

// TestZeroRoutesIsUsable checks the state the system is in before any content
// has been loaded: nothing panics, and every lookup says so plainly.
func TestZeroRoutesIsUsable(t *testing.T) {
	var r Routes

	if !r.IsEmpty() {
		t.Error("IsEmpty() = false for a zero route set")
	}
	if r.Has("alpha") {
		t.Error("Has(alpha) = true for a zero route set")
	}
	if len(r.Codes()) != 0 {
		t.Errorf("Codes() = %v, want empty", r.Codes())
	}
	if _, err := r.DistanceBetween("alpha", "bravo"); !errors.Is(err, ErrUnknownCity) {
		t.Errorf("DistanceBetween on a zero route set = %v, want ErrUnknownCity", err)
	}
}

// TestSingleEdgeNetwork is the smallest useful content: one route, two cities.
func TestSingleEdgeNetwork(t *testing.T) {
	r := mustRoutes(t, []Edge{{From: "alpha", To: "bravo", Distance: 7}})

	if d, err := r.DistanceBetween("alpha", "bravo"); err != nil || d != 7 {
		t.Errorf("alpha->bravo = %d (%v), want 7", d, err)
	}
	if d, err := r.DistanceBetween("alpha", "alpha"); err != nil || d != 0 {
		t.Errorf("alpha->alpha = %d (%v), want 0", d, err)
	}
}
