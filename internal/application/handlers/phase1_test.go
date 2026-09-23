package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// This file holds the fakes and the fixed world the phase 1 handler tests
// share. Every port is faked; nothing here opens a connection, reads a clock
// or sleeps. The one thing that is NOT faked is the domain: the transport
// network below prices with the real travel rules over real world.Routes,
// because a fake price would only prove that the handler can call a function.

const (
	// testEnergyCost is what the free test bus costs; see testModes.
	testEnergyCost = 10
	testArrivalXP  = 25
	// testTimeScale of 1 keeps real waits equal to game time, so a test
	// reads a duration straight off the mode's numbers.
	testTimeScale = 1
	// testPageSize is deliberately tiny so a boundary is two rows away
	// rather than fifty.
	testPageSize = 2
)

// The fixed world: three connected cities and one that no route reaches.
const (
	tehranID = "city-tehran"
	berlinID = "city-berlin"
	tokyoID  = "city-tokyo"
	limaID   = "city-lima"
)

var fixedNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func testCities() []application.City {
	return []application.City{
		{ID: berlinID, Code: "berlin", Name: "Berlin", JurisdictionID: "jur-berlin", CostOfLiving: 1200, Population: 3},
		{ID: limaID, Code: "lima", Name: "Lima", JurisdictionID: "jur-lima", CostOfLiving: 500, Population: 9},
		{ID: tehranID, Code: "tehran", Name: "Tehran", JurisdictionID: "jur-tehran", CostOfLiving: 900, Population: 9},
		{ID: tokyoID, Code: "tokyo", Name: "Tokyo", JurisdictionID: "jur-tokyo", CostOfLiving: 2000, Population: 14},
	}
}

// testRoutes connects tehran, berlin and tokyo. Lima is in the city list and
// in no route, which is the "two islands" case world.NewRoutes documents.
func testRoutes(t *testing.T) world.Routes {
	t.Helper()
	routes, err := world.NewRoutes([]world.Edge{
		{From: "tehran", To: "berlin", Distance: 400},
		{From: "berlin", To: "tokyo", Distance: 900},
	})
	if err != nil {
		t.Fatalf("build routes: %v", err)
	}
	return routes
}

// testModes are the transport modes of the fixed world:
//
//   - bus: public and FREE, on every road — the mode the older tests depart
//     by, so they need no money;
//   - train: public and priced, on every road, with demand pricing;
//   - flight: private and priced, only between tehran and berlin.
func testModes() (bus, train, flight travel.Mode) {
	bus = travel.Mode{Code: "bus", Public: true, KMPerHour: 100, Boarding: 10 * time.Minute,
		EnergyCost: testEnergyCost,
		Demand:     travel.Demand{Window: 30 * time.Minute, FreeDepartures: 1, StepBPS: 1000, MaxBPS: 20000}}
	train = travel.Mode{Code: "train", Public: true, KMPerHour: 200, Boarding: 20 * time.Minute,
		BaseFare: 100, FarePerKM: 1, EnergyCost: 6,
		Demand: travel.Demand{Window: 30 * time.Minute, FreeDepartures: 1, StepBPS: 1000, MaxBPS: 20000}}
	flight = travel.Mode{Code: "flight", KMPerHour: 400, Boarding: 30 * time.Minute,
		BaseFare: 500, FarePerKM: 4, EnergyCost: 4,
		Demand: travel.Demand{Window: time.Hour, MaxBPS: travel.BasisPoints}}
	return bus, train, flight
}

// fakeNetwork is TransportNetwork over the fixed world, each mode with its own
// real world.Routes.
type fakeNetwork struct {
	modes  []travel.Mode
	routes map[string]world.Routes
}

func testTransport(t *testing.T) *fakeNetwork {
	t.Helper()
	bus, train, flight := testModes()
	air, err := world.NewRoutes([]world.Edge{{From: "tehran", To: "berlin", Distance: 400}})
	if err != nil {
		t.Fatalf("build air routes: %v", err)
	}
	roads := testRoutes(t)
	return &fakeNetwork{
		modes:  []travel.Mode{bus, train, flight},
		routes: map[string]world.Routes{"bus": roads, "train": roads, "flight": air},
	}
}

func (n *fakeNetwork) Options(from, to string) ([]TransportOption, int) {
	var out []TransportOption
	for _, m := range n.modes {
		if d, err := n.routes[m.Code].DistanceBetween(from, to); err == nil {
			out = append(out, TransportOption{Mode: m, Name: strings.ToUpper(m.Code), DistanceKM: d})
		}
	}
	return out, 7
}

// fakePolicy answers every lever with one value and records what it was
// asked, so a test can prove the fare policy is read through the resolver.
type fakePolicy struct {
	value int64
	asked []string
}

func (f *fakePolicy) Get(_ context.Context, jurisdictionID, lever string) (application.PolicyValue, error) {
	f.asked = append(f.asked, jurisdictionID+"/"+lever)
	return application.PolicyValue{JurisdictionID: jurisdictionID, Lever: lever, Value: f.value}, nil
}

// --- fakes -------------------------------------------------------------

type fakeCities struct {
	cities []application.City
	err    error
}

func newFakeCities() *fakeCities { return &fakeCities{cities: testCities()} }

func (f *fakeCities) List(context.Context) ([]application.City, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]application.City, len(f.cities))
	copy(out, f.cities)
	return out, nil
}

func (f *fakeCities) ByID(_ context.Context, id string) (*application.City, error) {
	if f.err != nil {
		return nil, f.err
	}
	for i := range f.cities {
		if f.cities[i].ID == id {
			c := f.cities[i]
			return &c, nil
		}
	}
	return nil, application.ErrCityNotFound
}

func (f *fakeCities) ByCode(_ context.Context, code string) (*application.City, error) {
	if f.err != nil {
		return nil, f.err
	}
	for i := range f.cities {
		if f.cities[i].Code == code {
			c := f.cities[i]
			return &c, nil
		}
	}
	return nil, application.ErrCityNotFound
}

type fakeStats struct {
	rows  map[string]application.Stats
	saves int
	// failSaves makes the next n calls to Save fail with errInjected, so a
	// test can break a unit of work at the step after a journey lands.
	failSaves int
}

// errInjected is the failure a fake returns when a test asks it to.
var errInjected = stderrors.New("injected failure")

func (f *fakeStats) snapshot() func() {
	rows := make(map[string]application.Stats, len(f.rows))
	for k, v := range f.rows {
		rows[k] = v
	}
	saves := f.saves
	return func() { f.rows, f.saves = rows, saves }
}

func newFakeStats() *fakeStats { return &fakeStats{rows: map[string]application.Stats{}} }

func (f *fakeStats) Get(_ context.Context, playerID string) (*application.Stats, error) {
	row, ok := f.rows[playerID]
	if !ok {
		return nil, application.ErrPlayerNotFound
	}
	return &row, nil
}

func (f *fakeStats) EnsureDefaults(_ context.Context, playerID string, s application.Stats) (*application.Stats, error) {
	row, ok := f.rows[playerID]
	if !ok {
		f.rows[playerID] = s
		row = s
	}
	return &row, nil
}

func (f *fakeStats) Save(_ context.Context, s application.Stats) error {
	if f.failSaves > 0 {
		f.failSaves--
		return errInjected
	}
	f.saves++
	f.rows[s.PlayerID] = s
	return nil
}

type fakeSkills struct {
	rows map[string][]application.Skill
}

func newFakeSkills() *fakeSkills { return &fakeSkills{rows: map[string][]application.Skill{}} }

func (f *fakeSkills) snapshot() func() {
	rows := make(map[string][]application.Skill, len(f.rows))
	for k, v := range f.rows {
		rows[k] = append([]application.Skill(nil), v...)
	}
	return func() { f.rows = rows }
}

func (f *fakeSkills) List(_ context.Context, playerID string) ([]application.Skill, error) {
	return f.rows[playerID], nil
}

func (f *fakeSkills) Get(_ context.Context, playerID, code string) (*application.Skill, error) {
	for _, s := range f.rows[playerID] {
		if s.Code == code {
			return &s, nil
		}
	}
	return nil, application.ErrSkillNotFound
}

func (f *fakeSkills) Upsert(_ context.Context, s application.Skill) error {
	rows := f.rows[s.PlayerID]
	for i := range rows {
		if rows[i].Code == s.Code {
			rows[i] = s
			f.rows[s.PlayerID] = rows
			return nil
		}
	}
	f.rows[s.PlayerID] = append(rows, s)
	return nil
}

// fakeTravels mirrors the constraint the schema enforces: one in-transit row
// per player, and completing a journey moves the player in the same step.
type fakeTravels struct {
	active  map[string]application.Travel
	started []application.Travel
	// completed records every journey id Complete was called with, so a
	// second delivery is visible as a second entry rather than inferred.
	completed []string
	// moved is the player's city as Complete left it.
	moved map[string]string
}

func newFakeTravels() *fakeTravels {
	return &fakeTravels{active: map[string]application.Travel{}, moved: map[string]string{}}
}

func (f *fakeTravels) snapshot() func() {
	active := make(map[string]application.Travel, len(f.active))
	for k, v := range f.active {
		active[k] = v
	}
	moved := make(map[string]string, len(f.moved))
	for k, v := range f.moved {
		moved[k] = v
	}
	started := append([]application.Travel(nil), f.started...)
	completed := append([]string(nil), f.completed...)
	return func() {
		f.active, f.moved, f.started, f.completed = active, moved, started, completed
	}
}

func (f *fakeTravels) Active(_ context.Context, playerID string) (*application.Travel, error) {
	if t, ok := f.active[playerID]; ok {
		return &t, nil
	}
	return nil, application.ErrNoActiveTravel
}

func (f *fakeTravels) Start(_ context.Context, t application.Travel) error {
	if _, ok := f.active[t.PlayerID]; ok {
		return application.ErrAlreadyTravelling
	}
	f.active[t.PlayerID] = t
	f.started = append(f.started, t)
	return nil
}

func (f *fakeTravels) Complete(_ context.Context, travelID string) error {
	for playerID, t := range f.active {
		if t.ID != travelID {
			continue
		}
		delete(f.active, playerID)
		f.completed = append(f.completed, travelID)
		f.moved[playerID] = t.ToCityID
		return nil
	}
	return application.ErrNoActiveTravel
}

// RecentDepartures counts every journey ever started on the route by the
// mode since the given time, the way the repository counts travels rows.
func (f *fakeTravels) RecentDepartures(_ context.Context, from, to, mode string, since time.Time) (int, error) {
	n := 0
	for _, t := range f.started {
		if t.FromCityID == from && t.ToCityID == to && t.Mode == mode && !t.DepartedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *fakeTravels) Cancel(_ context.Context, travelID string) error {
	for playerID, t := range f.active {
		if t.ID == travelID {
			delete(f.active, playerID)
			return nil
		}
	}
	return application.ErrNoActiveTravel
}

type fakeActions struct {
	scheduled []application.GameAction
}

func (f *fakeActions) snapshot() func() {
	scheduled := append([]application.GameAction(nil), f.scheduled...)
	return func() { f.scheduled = scheduled }
}

func (f *fakeActions) Schedule(_ context.Context, a application.GameAction) error {
	f.scheduled = append(f.scheduled, a)
	return nil
}

func (f *fakeActions) Due(context.Context, time.Time, int) ([]application.GameAction, error) {
	return nil, nil
}
func (f *fakeActions) Complete(context.Context, string) error     { return nil }
func (f *fakeActions) Fail(context.Context, string, string) error { return nil }

type fakeFriendships struct {
	edges     map[string][]application.Friendship
	requested [][2]string
	accepted  [][2]string
}

func newFakeFriendships() *fakeFriendships {
	return &fakeFriendships{edges: map[string][]application.Friendship{}}
}

func (f *fakeFriendships) snapshot() func() {
	edges := make(map[string][]application.Friendship, len(f.edges))
	for k, v := range f.edges {
		edges[k] = append([]application.Friendship(nil), v...)
	}
	requested := append([][2]string(nil), f.requested...)
	accepted := append([][2]string(nil), f.accepted...)
	return func() { f.edges, f.requested, f.accepted = edges, requested, accepted }
}

func (f *fakeFriendships) List(_ context.Context, playerID string) ([]application.Friendship, error) {
	return f.edges[playerID], nil
}

func (f *fakeFriendships) Request(_ context.Context, playerID, friendPlayerID string) error {
	f.requested = append(f.requested, [2]string{playerID, friendPlayerID})
	f.edges[playerID] = append(f.edges[playerID], application.Friendship{
		PlayerID:       playerID,
		FriendPlayerID: friendPlayerID,
		Status:         friendPending,
	})
	return nil
}

func (f *fakeFriendships) Accept(_ context.Context, playerID, friendPlayerID string) error {
	for i, e := range f.edges[playerID] {
		if e.FriendPlayerID == friendPlayerID {
			f.edges[playerID][i].Status = friendAccepted
			f.accepted = append(f.accepted, [2]string{playerID, friendPlayerID})
			return nil
		}
	}
	return application.ErrNotFriends
}

func (f *fakeFriendships) Block(context.Context, string, string) error  { return nil }
func (f *fakeFriendships) Remove(context.Context, string, string) error { return nil }

// fakeSearch answers Find the way the repository does — an exact match on
// the one identifier the query names, a username without regard to case — and
// records every query. It deliberately does NOT filter on status, so a test
// can prove the handler refuses a banned account even if the port returned
// one.
type fakeSearch struct {
	players []application.Player
	calls   []application.PlayerQuery
}

func (f *fakeSearch) Find(_ context.Context, q application.PlayerQuery) (*application.Player, error) {
	f.calls = append(f.calls, q)
	for i := range f.players {
		p := f.players[i]
		var match bool
		switch q.Kind {
		case application.PlayerQueryUsername:
			match = p.Username != "" && strings.EqualFold(p.Username, q.Username)
		case application.PlayerQueryTelegramUserID:
			match = p.TelegramUserID == q.TelegramUserID
		case application.PlayerQueryPublicCode:
			match = p.PublicCode == q.PublicCode
		}
		if match {
			return &p, nil
		}
	}
	return nil, application.ErrPlayerNotFound
}

// --- harness -----------------------------------------------------------

// phase1 wires one set of fakes and the handlers over them.
type phase1 struct {
	uow         *fakeUOW
	cities      *fakeCities
	stats       *fakeStats
	skills      *fakeSkills
	travels     *fakeTravels
	actions     *fakeActions
	friendships *fakeFriendships
	search      *fakeSearch
	policy      *fakePolicy
	ids         *seqIDs
	now         time.Time
}

func newPhase1(t *testing.T) *phase1 {
	t.Helper()
	// The repositories a handler writes through live on the transaction, and
	// the harness keeps a pointer to the same fakes so an assertion reads
	// exactly what the unit of work left behind — including what it rolled
	// back.
	tx := newFakeTx()
	return &phase1{
		uow:         &fakeUOW{tx: tx},
		cities:      newFakeCities(),
		stats:       tx.stats,
		skills:      tx.skills,
		travels:     tx.travels,
		actions:     tx.actions,
		friendships: tx.friendships,
		search:      &fakeSearch{},
		policy:      &fakePolicy{value: travel.BasisPoints},
		ids:         &seqIDs{},
		now:         fixedNow,
	}
}

func (h *phase1) clock() func() time.Time { return func() time.Time { return h.now } }

// player registers a player in the fake repository and returns it.
func (h *phase1) player(telegramUserID int64, id, cityID string) *application.Player {
	p := &application.Player{
		ID:             id,
		TelegramUserID: telegramUserID,
		DisplayName:    id,
		Language:       "fa",
		Status:         "active",
		CreatedAt:      fixedNow,
	}
	if cityID != "" {
		city := cityID
		p.CityID = &city
	}
	h.uow.tx.players.byTelegramID[telegramUserID] = p
	return p
}

func (h *phase1) travelHandler(t *testing.T) *TravelHandler {
	t.Helper()
	return NewTravelHandler(h.uow, h.ids, messages(t), h.cities, testTransport(t), h.policy,
		testTimeScale, testArrivalXP, testIdempotencyTTL, h.clock())
}

func (h *phase1) skillsHandler(t *testing.T) *SkillsHandler {
	t.Helper()
	return NewSkillsHandler(h.uow, messages(t), h.skills, h.clock())
}

func (h *phase1) socialHandler(t *testing.T) *SocialHandler {
	t.Helper()
	return NewSocialHandler(h.uow, h.ids, messages(t), h.search, testPageSize, testIdempotencyTTL, h.clock())
}

func (h *phase1) mapHandler(t *testing.T) *MapHandler {
	t.Helper()
	return NewMapHandler(h.uow, messages(t), h.cities, h.travels, testRoutes(t), testPageSize, h.clock())
}

func (h *phase1) profileHandler(t *testing.T) *ProfileHandler {
	t.Helper()
	return NewProfileHandler(h.uow, h.ids, messages(t), h.cities,
		testDefaultLanguage, testIdempotencyTTL, h.clock())
}

// command builds request metadata for one command.
func command(name string, telegramUserID int64, requestID string) envelope.Metadata {
	m := meta("bot01", telegramUserID, requestID)
	m.Command = name
	return m
}

// scheduled builds the metadata a scheduler-driven command carries: no
// Telegram user, no chat, a fresh request id every dispatch.
func scheduled(name, requestID string) envelope.Metadata {
	return envelope.Metadata{
		RequestID:         requestID,
		TraceID:           "trace-scheduled",
		BotID:             "",
		GatewayInstanceID: "scheduler-01",
		UpdateType:        "scheduled",
		Command:           name,
		Language:          "fa",
		ReceivedAt:        fixedNow,
		SchemaVersion:     envelope.SchemaVersion,
	}
}

// pressed marks metadata as a button press, which is what makes a screen
// edit the message instead of sending a new one.
func pressed(m envelope.Metadata, messageID int64) envelope.Metadata {
	id := "callback-1"
	m.CallbackQueryID = &id
	m.TelegramMessageID = messageID
	m.UpdateType = "callback_query"
	return m
}
