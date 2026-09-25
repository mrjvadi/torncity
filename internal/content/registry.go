package content

import (
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/mrjvadi/torncity/internal/domain/education"
	"github.com/mrjvadi/torncity/internal/domain/job"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/domain/world"
)

// Snapshot is one version of the world, finished and frozen.
//
// Everything in it is built once, in BuildSnapshot, and never written again.
// That is what makes it safe to share between goroutines without a lock and
// what makes ADR 0004 rule 2 — an atomic swap, with no request ever seeing
// half of one version and half of another — a property of the type rather than
// a discipline callers have to keep.
//
// Its fields are unexported and its accessors copy what they return, because
// a caller holding a slice or a map out of a snapshot could mutate content
// every other goroutine is reading.
type Snapshot struct {
	version int
	routes  world.Routes
	byCode  map[string]world.City
	byID    map[string]world.City
	codes   []string // sorted, so Cities has a stable order
	skills  []SkillDef

	// Work and study, in file order; see jobs.go.
	careers []CareerDef
	courses []CourseDef
	career  map[string]job.Career
	course  map[string]education.Course

	// Transport: every mode with the network it serves; see transport.go.
	transport []transportNetwork

	// Crime: tiers, venues, categories and crimes; see crime.go.
	crime crimeContent

	// Payments: the methods each service accepts; see payment.go.
	payments map[payment.Service]payment.Accepts

	// Places: every place a city may have; see place.go.
	places []place.Place

	// Items and shops; see items.go.
	items itemContent

	// Elections by office; see election.go.
	elections map[string]ElectionDef

	// Companies; see company.go.
	companies companyContent

	// Production: components, method timings, technologies, suppliers; see
	// production.go.
	production productionContent

	// Military and diplomacy; see military.go.
	military militaryContent
	// war is military.yml's war section, nil without one; see war.go.
	war *WarDef

	// Stage E: health, missions and factions; see health.go, mission.go,
	// faction.go.
	health   *HealthDef
	missions missionContent
	faction  *FactionDef
}

// BuildSnapshot turns a pack into a snapshot, or explains why it cannot.
//
// version is the load number the database assigned, not the schema version the
// files declare. It is carried so that every event produced while this
// snapshot was live can name the content that produced it (ADR 0004 rule 3).
//
// The pack is validated again here even though nothing should reach this point
// unvalidated. It is not distrust of the loader: a pack also arrives from the
// database, which may hold a version written by an older binary with a rule
// this one has since added, and building a broken world in memory is far worse
// than refusing to.
//
// The snapshot borrows the pack's slices through the values it copies out of
// them, so the pack must not be modified afterwards.
func BuildSnapshot(version int, p *Pack) (*Snapshot, error) {
	if p == nil {
		return nil, fmt.Errorf("content: cannot build a snapshot from no pack")
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("content: version %d is not valid: %w", version, err)
	}

	routes, err := world.NewRoutes(p.Edges())
	if err != nil {
		return nil, fmt.Errorf("content: version %d: %w", version, err)
	}

	cities := p.WorldCities()
	snap := &Snapshot{
		version: version,
		routes:  routes,
		byCode:  make(map[string]world.City, len(cities)),
		byID:    make(map[string]world.City, len(cities)),
		codes:   make([]string, 0, len(cities)),
		skills:  append([]SkillDef(nil), p.Skills...),
	}
	for _, c := range cities {
		snap.byCode[c.Code] = c
		if c.ID != "" {
			snap.byID[c.ID] = c
		}
		snap.codes = append(snap.codes, c.Code)
	}
	sort.Strings(snap.codes)

	if err := snap.buildJobs(p); err != nil {
		return nil, fmt.Errorf("content: version %d: %w", version, err)
	}

	if snap.transport, err = buildTransport(p); err != nil {
		return nil, fmt.Errorf("content: version %d: %w", version, err)
	}

	if err := snap.buildCrimes(p); err != nil {
		return nil, fmt.Errorf("content: version %d: %w", version, err)
	}

	snap.buildPayments(p)
	snap.buildPlaces(p)
	snap.buildItems(p)
	snap.buildElections(p)
	snap.buildCompanies(p)
	snap.buildProduction(p)
	snap.buildMilitary(p)
	if len(p.War) > 0 {
		w := p.War[0]
		snap.war = &w
	}
	if len(p.Health) > 0 {
		h := p.Health[0]
		snap.health = &h
	}
	snap.buildMissions(p)
	if len(p.Factions) > 0 {
		f := p.Factions[0]
		snap.faction = &f
	}

	return snap, nil
}

// emptySnapshot is what a registry holds before anything has been loaded.
//
// It is a real snapshot rather than a nil pointer so that every accessor works
// from the first instant of the process: Version reports 0, no city is found,
// and world.Routes answers ErrUnknownCity for every lookup. Those are the
// honest answers before content exists, and they arrive as ordinary values
// instead of a panic in whichever goroutine happened to read first.
func emptySnapshot() *Snapshot {
	return &Snapshot{
		byCode: map[string]world.City{},
		byID:   map[string]world.City{},
	}
}

// Version is the load number this snapshot was built from. Zero means nothing
// has been loaded yet.
func (s *Snapshot) Version() int { return s.version }

// Routes is the travel network. world.Routes is a value with no exported
// mutable state, so returning it by value hands out a usable copy rather than
// a handle into the snapshot.
func (s *Snapshot) Routes() world.Routes { return s.routes }

// Cities returns every city of this version, ordered by code. The slice is
// freshly allocated, so a caller may sort or filter it.
func (s *Snapshot) Cities() []world.City {
	out := make([]world.City, 0, len(s.codes))
	for _, code := range s.codes {
		out = append(out, s.byCode[code])
	}
	return out
}

// City looks a city up by its stable code.
func (s *Snapshot) City(code string) (world.City, bool) {
	c, ok := s.byCode[code]
	return c, ok
}

// CityByID looks a city up by its storage identifier. It finds nothing in a
// snapshot built from files, which carry no identifiers.
func (s *Snapshot) CityByID(id string) (world.City, bool) {
	c, ok := s.byID[id]
	return c, ok
}

// Skills returns the skill definitions of this version. The slice is a copy.
func (s *Snapshot) Skills() []SkillDef {
	return append([]SkillDef(nil), s.skills...)
}

// Registry holds the snapshot a process is currently serving.
//
// # Reads are lock-free
//
// The whole snapshot is behind one atomic pointer and a reload replaces it
// with a different one. No mutex is taken on the read path, which matters
// because every command in the game touches content and a shared lock there
// would serialise the entire process against a reload that happens once a
// week.
//
// # A request keeps the version it started with
//
// Swap publishes a new pointer; it does not modify the snapshot anyone is
// already reading. A request that calls Current once and uses the result for
// its whole life therefore sees one consistent world from beginning to end,
// even if a reload lands in the middle of it. That is the intended usage, and
// it is why Current exists rather than only the delegating accessors below.
//
// Calling Registry.City and then Registry.Routes is NOT the same thing: those
// are two independent reads and a swap can fall between them. They are there
// for callers that genuinely want "whatever is current right now" — a status
// line, an admin listing — and a request handler should take a Snapshot once
// instead.
//
// The zero value is not usable; call NewRegistry.
type Registry struct {
	current atomic.Pointer[Snapshot]
}

// NewRegistry returns a registry holding empty content.
func NewRegistry() *Registry {
	r := &Registry{}
	r.current.Store(emptySnapshot())
	return r
}

// Swap installs a new snapshot and returns the one it replaced.
//
// It is atomic: a reader either sees the whole old version or the whole new
// one. Swapping in nil is refused rather than accepted, because a nil snapshot
// would turn every subsequent read into a panic in some unrelated goroutine,
// far from whatever caused it.
func (r *Registry) Swap(s *Snapshot) *Snapshot {
	if s == nil {
		s = emptySnapshot()
	}
	return r.current.Swap(s)
}

// Current returns the snapshot in force now.
//
// A request should call this ONCE and pass the result down. See the type
// comment: that is what makes a request immune to a reload happening while it
// runs.
func (r *Registry) Current() *Snapshot { return r.current.Load() }

// Version is the load number currently in force. Zero before the first load.
func (r *Registry) Version() int { return r.Current().Version() }

// Cities returns every city currently in force, ordered by code.
func (r *Registry) Cities() []world.City { return r.Current().Cities() }

// City looks a city up by code in the snapshot currently in force.
func (r *Registry) City(code string) (world.City, bool) { return r.Current().City(code) }

// Routes returns the travel network currently in force.
func (r *Registry) Routes() world.Routes { return r.Current().Routes() }

// Skills returns the skill definitions currently in force.
func (r *Registry) Skills() []SkillDef { return r.Current().Skills() }
