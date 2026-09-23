package content

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/world"
)

// chainPack is a line of cities: alpha - bravo - charlie. It gives the
// snapshot a path that is longer than any single edge, which is what proves
// the route network was really built rather than copied.
func chainPack() *Pack {
	return &Pack{
		Schema: 1,
		Cities: []CityDef{city("alpha"), city("bravo"), city("charlie")},
		Routes: []RouteDef{
			{From: "alpha", To: "bravo", Distance: 100},
			{From: "bravo", To: "charlie", Distance: 250},
		},
		Skills:        []SkillDef{{Code: "driving", Name: "Driving", Category: "technical"}},
		Levels:        homeLevels,
		Jurisdictions: homeCountry,
		CityIDs: map[string]string{
			"alpha": "id-alpha", "bravo": "id-bravo", "charlie": "id-charlie",
		},
	}
}

func TestBuildSnapshotProducesWorkingRoutes(t *testing.T) {
	snap, err := BuildSnapshot(7, chainPack())
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if snap.Version() != 7 {
		t.Errorf("version is %d, want 7", snap.Version())
	}

	routes := snap.Routes()
	tests := []struct {
		from, to string
		want     int
	}{
		{"alpha", "bravo", 100},
		{"bravo", "charlie", 250},
		// Not an authored edge: the shortest path through bravo. If this is
		// wrong, the snapshot is holding an edge list rather than a network.
		{"alpha", "charlie", 350},
		// Edges are bidirectional, so the reverse must answer too.
		{"charlie", "alpha", 350},
		{"alpha", "alpha", 0},
	}
	for _, tt := range tests {
		got, err := routes.DistanceBetween(tt.from, tt.to)
		if err != nil {
			t.Errorf("%s -> %s: %v", tt.from, tt.to, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s -> %s is %d, want %d", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestBuildSnapshotLooksCitiesUpBothWays(t *testing.T) {
	snap, err := BuildSnapshot(1, chainPack())
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	byCode, ok := snap.City("bravo")
	if !ok {
		t.Fatal("a declared city cannot be found by code")
	}
	if byCode.ID != "id-bravo" {
		t.Errorf("the storage id was not carried into the snapshot: %+v", byCode)
	}

	byID, ok := snap.CityByID("id-bravo")
	if !ok {
		t.Fatal("a city with a known id cannot be found by id")
	}
	if byID.Code != "bravo" {
		t.Errorf("the id lookup found %q", byID.Code)
	}

	if _, ok := snap.City("nowhere"); ok {
		t.Error("a city nobody declared was found")
	}
}

// A pack read out of yaml carries no ids, so a lookup by id must simply find
// nothing rather than matching the empty string against every city.
func TestBuildSnapshotWithoutIDs(t *testing.T) {
	p := chainPack()
	p.CityIDs = nil

	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if _, ok := snap.CityByID(""); ok {
		t.Error("the empty id matched a city")
	}
	if _, ok := snap.City("alpha"); !ok {
		t.Error("code lookups stopped working without ids")
	}
}

func TestSnapshotCitiesAreSortedAndCopied(t *testing.T) {
	snap, err := BuildSnapshot(1, chainPack())
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	cities := snap.Cities()
	for i, want := range []string{"alpha", "bravo", "charlie"} {
		if cities[i].Code != want {
			t.Fatalf("cities are not ordered by code: %+v", cities)
		}
	}

	// A caller that mutates what it was handed must not be able to change the
	// content every other goroutine is reading.
	cities[0].Name = "vandalised"
	if again := snap.Cities(); again[0].Name == "vandalised" {
		t.Error("Cities handed out a view into the snapshot instead of a copy")
	}

	skills := snap.Skills()
	skills[0].Name = "vandalised"
	if again := snap.Skills(); again[0].Name == "vandalised" {
		t.Error("Skills handed out a view into the snapshot instead of a copy")
	}
}

func TestBuildSnapshotRefusesInvalidContent(t *testing.T) {
	tests := []struct {
		name string
		pack *Pack
	}{
		{"no pack at all", nil},
		{"a route to nowhere", func() *Pack {
			p := chainPack()
			p.Routes[0].To = "nowhere"
			return p
		}()},
		{"no cities", &Pack{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildSnapshot(3, tt.pack); err == nil {
				t.Fatal("BuildSnapshot built a broken world")
			}
		})
	}
}

// A registry is usable from the first instant of the process, before anything
// has been loaded. Those accessors must answer, not panic.
func TestNewRegistryIsUsableBeforeAnyLoad(t *testing.T) {
	r := NewRegistry()

	if r.Version() != 0 {
		t.Errorf("version is %d before any load, want 0", r.Version())
	}
	if got := r.Cities(); len(got) != 0 {
		t.Errorf("got %d cities before any load", len(got))
	}
	if _, ok := r.City("alpha"); ok {
		t.Error("a city was found before any load")
	}
	if got := r.Skills(); len(got) != 0 {
		t.Errorf("got %d skills before any load", len(got))
	}
	if _, err := r.Routes().DistanceBetween("alpha", "bravo"); !errors.Is(err, world.ErrUnknownCity) {
		t.Errorf("got %v, want ErrUnknownCity from an empty network", err)
	}
}

func TestSwapReturnsTheOldSnapshot(t *testing.T) {
	r := NewRegistry()

	first, err := BuildSnapshot(1, chainPack())
	if err != nil {
		t.Fatal(err)
	}
	if old := r.Swap(first); old.Version() != 0 {
		t.Errorf("the replaced snapshot has version %d, want the empty 0", old.Version())
	}
	if r.Version() != 1 {
		t.Errorf("version is %d after the swap, want 1", r.Version())
	}

	second, err := BuildSnapshot(2, chainPack())
	if err != nil {
		t.Fatal(err)
	}
	if old := r.Swap(second); old != first {
		t.Error("Swap did not return the snapshot it replaced")
	}
	if r.Version() != 2 {
		t.Errorf("version is %d after the second swap, want 2", r.Version())
	}
}

// Swapping in nil would turn every later read into a panic in some unrelated
// goroutine, far from whatever caused it.
func TestSwapRefusesNil(t *testing.T) {
	r := NewRegistry()
	if _, err := BuildSnapshot(1, chainPack()); err != nil {
		t.Fatal(err)
	}

	r.Swap(nil)
	if r.Current() == nil {
		t.Fatal("a nil snapshot was installed")
	}
	if r.Version() != 0 {
		t.Errorf("version is %d, want the empty 0", r.Version())
	}
	if _, ok := r.City("alpha"); ok {
		t.Error("the empty snapshot found a city")
	}
}

// TestSwapUnderConcurrentReaders is ADR 0004 rule 2 as a test, and it is
// meaningful only under -race.
//
// Readers hammer the registry while a writer swaps versions underneath them.
// Two properties are asserted: no reader ever observes a torn snapshot — a
// version number from one load with the cities of another — and no data race
// is reported.
func TestSwapUnderConcurrentReaders(t *testing.T) {
	r := NewRegistry()

	// Each version is distinguishable by content as well as by number: version
	// 1 has three cities, version 2 has four. A reader that sees version 2
	// with three cities has seen half of each.
	v1, err := BuildSnapshot(1, chainPack())
	if err != nil {
		t.Fatal(err)
	}
	four := chainPack()
	four.Cities = append(four.Cities, city("delta"))
	four.CityIDs["delta"] = "id-delta"
	v2, err := BuildSnapshot(2, four)
	if err != nil {
		t.Fatal(err)
	}
	r.Swap(v1)

	const readers = 8
	var (
		wg      sync.WaitGroup
		started sync.WaitGroup
		stop    atomic.Bool
		torn    atomic.Int64
		reads   atomic.Int64
	)

	// Every reader completes one full read BEFORE the writer starts swapping,
	// and the writer waits for that. Without the barrier a fast writer could
	// finish all its swaps before the scheduler ran a single reader, and the
	// test would then prove nothing — which is what the earlier, flaky
	// "no reader ever ran" failure was reporting.
	started.Add(readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first := true
			for first || !stop.Load() {
				// The intended usage: take the snapshot ONCE and use that one
				// value for the whole unit of work. A swap landing in the
				// middle must not be visible to this iteration.
				snap := r.Current()

				want := map[int]int{1: 3, 2: 4}[snap.Version()]
				if len(snap.Cities()) != want {
					torn.Add(1)
				}
				if _, err := snap.Routes().DistanceBetween("alpha", "charlie"); err != nil {
					torn.Add(1)
				}
				if _, ok := snap.City("alpha"); !ok {
					torn.Add(1)
				}
				reads.Add(1)
				if first {
					first = false
					started.Done()
				}
			}
		}()
	}
	started.Wait()

	for i := 0; i < 2000; i++ {
		if i%2 == 0 {
			r.Swap(v2)
		} else {
			r.Swap(v1)
		}
	}
	stop.Store(true)
	wg.Wait()

	if n := torn.Load(); n != 0 {
		t.Errorf("%d reader(s) saw a torn snapshot", n)
	}
	// Guaranteed by the barrier above; kept as a guard against somebody
	// removing it.
	if reads.Load() < readers {
		t.Errorf("only %d read(s) happened, want at least one per reader", reads.Load())
	}
}

// The delegating accessors are two independent reads, so a swap can fall
// between them. They must still each be individually safe.
func TestRegistryAccessorsUnderConcurrentSwaps(t *testing.T) {
	r := NewRegistry()
	snap, err := BuildSnapshot(1, chainPack())
	if err != nil {
		t.Fatal(err)
	}
	r.Swap(snap)

	var wg, started sync.WaitGroup
	var stop atomic.Bool

	const readers = 4
	started.Add(readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first := true
			for first || !stop.Load() {
				_ = r.Version()
				_ = r.Cities()
				_, _ = r.City("bravo")
				_ = r.Routes()
				_ = r.Skills()
				if first {
					first = false
					started.Done()
				}
			}
		}()
	}
	started.Wait()

	for i := 0; i < 1000; i++ {
		fresh, err := BuildSnapshot(i+2, chainPack())
		if err != nil {
			t.Error(err)
			break
		}
		r.Swap(fresh)
	}
	stop.Store(true)
	wg.Wait()
}
