package world

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Sentinel errors from building or querying a route set.
var (
	// ErrMissingEdgeCode means an edge named a city with an empty code.
	ErrMissingEdgeCode = errors.New("world: route endpoint code is required")

	// ErrSelfRoute means an edge connected a city to itself. The distance
	// from a city to itself is zero by definition, so an explicit self route
	// is either a typo or an attempt to state something that cannot be true.
	ErrSelfRoute = errors.New("world: route cannot connect a city to itself")

	// ErrInvalidEdgeDistance means an edge distance was not a positive number
	// within MaxEdgeDistance. Zero would make two cities the same place and
	// travel between them instant; negative would let a path get shorter the
	// further it goes.
	ErrInvalidEdgeDistance = errors.New("world: route distance must be positive and within MaxEdgeDistance")

	// ErrDuplicateRoute means the same pair of cities was given a direct route
	// twice. Rejecting it is deliberate: silently keeping the first or the
	// shorter one hides an edit that somebody expected to take effect.
	ErrDuplicateRoute = errors.New("world: duplicate route between the same two cities")

	// ErrTooManyCities means the edge set named more cities than MaxCities.
	ErrTooManyCities = errors.New("world: route set names more cities than MaxCities")

	// ErrUnknownCity means a city code does not appear anywhere in the route
	// set, so nothing is known about how to reach it.
	ErrUnknownCity = errors.New("world: city is not in the route set")

	// ErrNoRoute means both cities are known but no chain of routes connects
	// them. See NewRoutes for why that is refused here rather than at load.
	ErrNoRoute = errors.New("world: no route connects these cities")
)

// Limits on a route set. They exist because this data is authored in a file
// and loaded at runtime: without them, one careless number turns a content
// reload into an outage.
//
// MaxCities bounds the all-pairs computation in NewRoutes, which is cubic in
// the number of cities. MaxEdgeDistance keeps any path sum comfortably inside
// int64 and rejects an obviously mistyped figure at the door.
const (
	MaxCities       = 1024
	MaxEdgeDistance = 1_000_000
)

// Edge is one direct route between two cities, by city code, in kilometres.
//
// DIRECTION: an edge is BIDIRECTIONAL. Declaring a route from A to B declares
// the same route from B to A at the same distance. This is the right default
// for the content it describes — a road, a flight path or a shipping lane in
// this game is travelled both ways — and it halves the file a human maintains,
// which removes the most likely authoring mistake: a pair of directions that
// were meant to match and drifted apart.
//
// If the game ever needs a genuinely one-way route, the extension point is a
// flag on this struct rather than a second distance field, so that the common
// case stays a single line.
type Edge struct {
	From     string
	To       string
	Distance int
}

// Routes is a loaded, validated route network: the cities that exist as far as
// travel is concerned, and the shortest distance between every pair of them.
//
// It is built once from content and then only read, so it is safe to share
// between goroutines. It is opaque on purpose: the shortest paths are
// precomputed at construction, and exposing the table would invite a caller to
// hold on to a slice that the next reload replaces.
//
// The zero value is an empty network. It is usable and every lookup in it
// fails with ErrUnknownCity, which is the honest answer before content has
// been loaded.
type Routes struct {
	codes []string
	index map[string]int
	// dist is an n*n matrix flattened row-major, in kilometres, with
	// unreachable pairs held at infinity. int64 so that summing a long path
	// cannot overflow before the limits above ever come into play.
	dist []int64
}

const unreachable = math.MaxInt64

// NewRoutes builds a route network from an edge list, or explains why it
// cannot.
//
// The cities of the network are exactly the cities the edges mention. There is
// no separate city list to keep in step, so a city cannot be in the network
// and unreachable by construction.
//
// Validation covers what a human editing a file gets wrong: an empty code, a
// city routed to itself, a distance that is zero, negative or absurd, and the
// same pair given twice. Every one returns an error naming the offending
// cities rather than panicking, because this runs on a content reload in a
// live process and a bad file must fail the reload, not the service.
//
// A DISCONNECTED NETWORK IS ACCEPTED. Two islands with no route between them
// load fine, and only a journey that actually needs the missing link fails,
// with ErrNoRoute. Refusing to load would mean a city could never be added
// before its routes were authored, and would turn a gap in content into a
// total outage instead of one refused trip.
func NewRoutes(edges []Edge) (Routes, error) {
	// Collect the city codes first, so the matrix indices follow a stable
	// sorted order rather than the order the file happened to list them in.
	// Determinism matters here: the same content must always produce the same
	// distances, whichever machine loaded it.
	seen := make(map[string]struct{}, len(edges)*2)
	for _, e := range edges {
		if e.From == "" || e.To == "" {
			return Routes{}, fmt.Errorf("%w: route %q -> %q", ErrMissingEdgeCode, e.From, e.To)
		}
		if e.From == e.To {
			return Routes{}, fmt.Errorf("%w: %q", ErrSelfRoute, e.From)
		}
		if e.Distance <= 0 || e.Distance > MaxEdgeDistance {
			return Routes{}, fmt.Errorf("%w: route %q -> %q has distance %d",
				ErrInvalidEdgeDistance, e.From, e.To, e.Distance)
		}
		seen[e.From] = struct{}{}
		seen[e.To] = struct{}{}
	}

	if len(seen) > MaxCities {
		return Routes{}, fmt.Errorf("%w: %d cities", ErrTooManyCities, len(seen))
	}

	codes := make([]string, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	index := make(map[string]int, len(codes))
	for i, code := range codes {
		index[code] = i
	}

	n := len(codes)
	dist := make([]int64, n*n)
	for i := range dist {
		dist[i] = unreachable
	}
	for i := 0; i < n; i++ {
		dist[i*n+i] = 0
	}

	for _, e := range edges {
		i, j := index[e.From], index[e.To]
		if dist[i*n+j] != unreachable {
			return Routes{}, fmt.Errorf("%w: %q and %q", ErrDuplicateRoute, e.From, e.To)
		}
		d := int64(e.Distance)
		dist[i*n+j] = d
		dist[j*n+i] = d
	}

	// Floyd-Warshall: every pair, shortest path. The network is small by
	// construction (MaxCities), it is rebuilt only on a content reload, and
	// doing it once here means Distance is a single array read on the hot
	// path of every travel plan.
	//
	// Computing shortest paths rather than taking direct edges also buys the
	// triangle inequality for free: no detour through a third city can ever
	// come out shorter than the distance this reports, so a fare derived from
	// it cannot be undercut by splitting the trip into legs.
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			ik := dist[i*n+k]
			if ik == unreachable {
				continue
			}
			for j := 0; j < n; j++ {
				kj := dist[k*n+j]
				if kj == unreachable {
					continue
				}
				if through := ik + kj; through < dist[i*n+j] {
					dist[i*n+j] = through
				}
			}
		}
	}

	return Routes{codes: codes, index: index, dist: dist}, nil
}

// Distance returns the shortest distance in kilometres between two cities.
//
// It is a pure function of the loaded content: the same route set and the same
// pair always give the same number, in either order, and the distance from a
// city to itself is zero.
//
// It returns an error instead of a fallback number. A guessed distance would
// price a journey nobody can verify and would hide missing content until a
// player complained about the fare.
func (r Routes) Distance(from, to City) (int, error) {
	return r.DistanceBetween(from.Code, to.Code)
}

// DistanceBetween is Distance by city code, for callers that hold codes rather
// than whole cities.
func (r Routes) DistanceBetween(from, to string) (int, error) {
	i, ok := r.index[from]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownCity, from)
	}
	j, ok := r.index[to]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownCity, to)
	}

	d := r.dist[i*len(r.codes)+j]
	if d == unreachable {
		return 0, fmt.Errorf("%w: %q and %q", ErrNoRoute, from, to)
	}
	return int(d), nil
}

// Has reports whether a city code is part of the loaded network.
func (r Routes) Has(code string) bool {
	_, ok := r.index[code]
	return ok
}

// Codes returns the city codes in the network, sorted. The slice is a copy, so
// a caller cannot reach into a shared route set and reorder it.
func (r Routes) Codes() []string {
	out := make([]string, len(r.codes))
	copy(out, r.codes)
	return out
}

// IsEmpty reports whether any content has been loaded at all. It separates
// "this city is unknown" from "nothing has been loaded yet", which are the
// same symptom and very different faults.
func (r Routes) IsEmpty() bool { return len(r.codes) == 0 }
