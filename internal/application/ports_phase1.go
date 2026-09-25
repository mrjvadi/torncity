package application

import (
	"context"
	"time"
)

// This file holds the ports phase 1 adds: the world a player moves through,
// their stats and skills, the durable schedule, and the social graph.
//
// Same rule as ports.go: every dependency is an interface declared here, and
// infrastructure implements it. A use case must compile without a driver.

// City is a place in the world. It mirrors the cities row, and the domain's
// world.City is built from it.
//
// It deliberately carries NO tax rate. What a city charges is a policy, set by
// its mayor within the operator's bounds (docs/adr/0015-player-held-offices.md),
// and the only way to read it is PolicyReader.Get(ctx, JurisdictionID,
// "city.tax_rate"). The cities.tax_rate_bps column is the lever's per-city
// default, which the resolver reads; a field here would be a second, wrong
// answer one line away from every handler.
type City struct {
	ID   string
	Code string
	Name string
	// JurisdictionID is the city's own jurisdiction, which policy is read
	// against. Empty for a city no content load has placed in the tree.
	JurisdictionID string
	CostOfLiving   int64
	Population     int
}

// Stats is a player's live condition.
type Stats struct {
	PlayerID   string
	Level      int
	XP         int64
	Health     int
	MaxHealth  int
	Energy     int
	MaxEnergy  int
	Happiness  int
	Stamina    int
	Reputation int
	UpdatedAt  time.Time
	// RegenBPS is how fast energy comes back, 10000 = as always; zero reads
	// as 10000. A hard-pressed body regenerates slower (docs/adr/0025). It
	// is written only through LifeRepository.SetRegen, never by Save.
	RegenBPS int
}

// Skill is one trained ability.
type Skill struct {
	PlayerID  string
	Code      string
	Level     int
	XP        int64
	UpdatedAt time.Time
}

// Travel is a journey in progress or finished.
type Travel struct {
	ID           string
	PlayerID     string
	FromCityID   string
	ToCityID     string
	Cost         int64
	GameActionID string
	Status       string
	DepartedAt   time.Time
	ArrivesAt    time.Time
	// Mode is the transport mode's content code; empty for a journey that
	// began before modes existed.
	Mode string
	// LedgerTransactionID is the transaction that paid Cost; empty when the
	// fare was zero or the journey predates fares.
	LedgerTransactionID string
	// ContentVersion is the content version that priced the journey; zero
	// when unknown.
	ContentVersion int
	// VehicleID is the item piece the player drove (docs/adr/0024); empty
	// for a hired or public mode.
	VehicleID string
}

// GameAction is a unit of work due at a point in time.
//
// This is the durable schedule. Redis may accelerate lookups but this row is
// the source of truth: a service that restarts mid-travel must still land the
// player, and it can only do that if the work outlived the process.
type GameAction struct {
	ID            string
	ActionType    string
	ActorType     string
	ActorID       string
	ReferenceType string
	ReferenceID   string
	Payload       []byte
	Status        string
	RetryCount    int
	StartedAt     time.Time
	FinishAt      time.Time
	CompletedAt   *time.Time
}

// Friendship is one direction of a social edge.
//
// As FriendshipRepository.List returns it, PlayerID is always the player whose
// list it is and FriendPlayerID the other one; Incoming says which way the
// edge points. An outgoing edge (Incoming false) is the player's own row: a
// friend, a request they SENT, or a block. An incoming edge is the other
// player's pending request TO them, the only kind they can accept.
type Friendship struct {
	ID             string
	PlayerID       string
	FriendPlayerID string
	Status         string
	CreatedAt      time.Time
	Incoming       bool
}

// CityRepository reads the world's places.
//
// Cities are content: they arrive through the content loader, not through
// gameplay, so there is no Create here on purpose.
type CityRepository interface {
	List(ctx context.Context) ([]City, error)
	ByID(ctx context.Context, id string) (*City, error)
	ByCode(ctx context.Context, code string) (*City, error)
}

// StatsRepository persists a player's condition.
type StatsRepository interface {
	Get(ctx context.Context, playerID string) (*Stats, error)
	// EnsureDefaults creates the row on first contact and returns it. It must
	// be safe under a race, like PlayerRepository.Create.
	EnsureDefaults(ctx context.Context, playerID string, s Stats) (*Stats, error)
	Save(ctx context.Context, s Stats) error
}

// SkillRepository persists trained abilities.
type SkillRepository interface {
	List(ctx context.Context, playerID string) ([]Skill, error)
	Get(ctx context.Context, playerID, code string) (*Skill, error)
	// Upsert adds or updates one skill. Keyed on (player_id, skill_code).
	Upsert(ctx context.Context, s Skill) error
}

// TravelRepository persists journeys.
type TravelRepository interface {
	// Active returns the player's journey in progress, or ErrNoActiveTravel.
	Active(ctx context.Context, playerID string) (*Travel, error)
	// Start inserts a journey. The schema carries a partial unique index that
	// refuses a second in-transit row for one player; this must surface that
	// as ErrAlreadyTravelling rather than a raw driver error, because a player
	// pressing a button twice is ordinary, not exceptional.
	Start(ctx context.Context, t Travel) error
	// Complete marks the journey arrived and moves the player's city in the
	// same transaction. Doing those separately can strand a player between
	// two cities if the process dies in between.
	Complete(ctx context.Context, travelID string) error
	Cancel(ctx context.Context, travelID string) error
	// RecentDepartures counts the journeys that left fromCityID for
	// toCityID by mode at or after since, whatever became of them. It is
	// the demand a fare is priced on (travel.Demand), so it must count
	// arrived and cancelled journeys too: a trip that happened was demand.
	RecentDepartures(ctx context.Context, fromCityID, toCityID, mode string, since time.Time) (int, error)
}

// GameActionRepository is the durable schedule.
type GameActionRepository interface {
	Schedule(ctx context.Context, a GameAction) error
	// Due claims up to limit actions whose finish_at has passed, oldest
	// first. It MUST NOT return the same row to two workers: fold the
	// locking select into the claiming update, as OutboxStore.FetchPending
	// already does. A standalone FOR UPDATE SKIP LOCKED releases its locks
	// the moment the statement commits and claims nothing.
	Due(ctx context.Context, now time.Time, limit int) ([]GameAction, error)
	Complete(ctx context.Context, id string) error
	Fail(ctx context.Context, id string, reason string) error
}

// FriendshipRepository persists the social graph.
//
// An edge is directed: a mutual friendship is two rows, and A blocking B does
// not imply B blocking A.
type FriendshipRepository interface {
	// List returns the player's own edges in every status, and the pending
	// requests other players sent them (Incoming), one line per other
	// player: when both asked each other, the request to answer wins.
	List(ctx context.Context, playerID string) ([]Friendship, error)
	Request(ctx context.Context, playerID, friendPlayerID string) error
	Accept(ctx context.Context, playerID, friendPlayerID string) error
	Block(ctx context.Context, playerID, friendPlayerID string) error
	Remove(ctx context.Context, playerID, friendPlayerID string) error
}

// PlayerQueryKind says which identifier a PlayerQuery carries.
type PlayerQueryKind int

// The three ways a player can be named in a search. There is deliberately no
// fourth: display names are not unique, so searching them answered "who is
// Ali?" with a list of strangers, which is the ambiguity the search exists to
// remove.
const (
	// PlayerQueryUsername is a Telegram username, matched without regard
	// to case.
	PlayerQueryUsername PlayerQueryKind = iota + 1
	// PlayerQueryTelegramUserID is a Telegram user id, matched exactly.
	PlayerQueryTelegramUserID
	// PlayerQueryPublicCode is a public player code, matched exactly.
	PlayerQueryPublicCode
)

// PlayerQuery names exactly one player by one identifier. Build it with
// handlers.ClassifyPlayerQuery, which normalises what a player typed; the
// field that matches Kind is the one that is set.
type PlayerQuery struct {
	Kind PlayerQueryKind
	// Username is lower-case and has no leading @.
	Username       string
	TelegramUserID int64
	// PublicCode is upper-case (playercode.Normalize).
	PublicCode string
}

// PlayerSearch finds another player by an exact identifier, for the social
// screens.
//
// It used to search display names by substring, a page at a time; that
// method is gone rather than kept beside this one, so nothing can quietly go
// back to fuzzy matching.
type PlayerSearch interface {
	// Find returns the ACTIVE player q names, or ErrPlayerNotFound. A banned
	// or deleted account is never returned: surfacing one would confirm a
	// ban to anyone who asked, and offer a friend request that can never be
	// answered.
	Find(ctx context.Context, q PlayerQuery) (*Player, error)
}
