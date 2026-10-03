package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds group founding (docs/adr/0028-world-and-settlements.md
// sections 2, 3 and 9): the world registry a generated planet is regenerated
// from, and the ports behind turning a Telegram group's founding command
// into a village.
//
// FoundedSettlement is deliberately its own type rather than a write path
// bolted onto City (ports_phase1.go): CityRepository is documented there as
// read-only by design — "cities arrive through the content loader, never
// through gameplay" — and this file does not change that. A founded
// settlement is still a cities row underneath (migration 0042), but the
// application-level write path is this one, separate port, so nothing that
// already holds a CityRepository gains a way to create a city by accident.

// DefaultFoundingCountryCode is the country a newly founded village's
// jurisdiction sits under until province/country formation (a later phase,
// ADR 0028 section 5.5) lets a group's own settlements form one of their
// own. governance.yml declares it as the one playable country: the far
// country, vantor_federation, is explicitly "where nobody is born"
// (docs/adr/0022).
const DefaultFoundingCountryCode = "default_country"

// World is one row of the world registry: the seed a planet is generated
// from, never the planet itself (section 2's seed-first rule). Everything
// about the planet's terrain is recomputed from Seed, GeneratorVersion and
// the world-gen content by internal/domain/worldgen.Generate — nothing here
// is stored tile by tile.
type World struct {
	ID               string
	Seed             uint64
	GeneratorVersion int
	// ParamsHash is a hex digest of the worldgen.Params (and world-gen
	// content) this seed was generated under: a replica checks its own
	// configured params against this before trusting its in-memory World for
	// this id, so two replicas whose configs/config.yml have drifted refuse
	// to silently serve two different planets under one world id.
	ParamsHash string
	Active     bool
	CreatedAt  time.Time
	CreatedBy  string
}

// Settlement founding sentinels.
var (
	// ErrNoActiveWorld means no world is active: `admin world create` has
	// not run yet. A group cannot found before it has.
	ErrNoActiveWorld = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoActiveWorld", "the world has not been created yet")

	// ErrWorldAlreadyActive means Create was asked to create a second world
	// while one is already active. A world is immutable once created
	// (ADR 0028 section 2); replacing the live one is a deliberate,
	// separate operator act this port does not perform.
	ErrWorldAlreadyActive = errors.Sentinel(errors.CodeConflict,
		"application.ErrWorldAlreadyActive", "a world is already active")

	// ErrSpawnCellTaken means the cell a caller chose for a new settlement
	// was claimed by another settlement (on the same world) since it was
	// chosen — a concurrent founding, on this or another replica, won the
	// race. The caller retries with FindSpawn's next candidate.
	ErrSpawnCellTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrSpawnCellTaken", "that spot was just claimed by another settlement")

	// ErrGroupAlreadyFounded means this Telegram chat already founded a
	// settlement: ADR 0028 section 3.1's "no group founds more than one
	// settlement this way". Also what makes founding idempotent under
	// at-least-once command delivery — a redelivered founding command
	// reaches this, not a second village.
	ErrGroupAlreadyFounded = errors.Sentinel(errors.CodeConflict,
		"application.ErrGroupAlreadyFounded", "this group has already founded a settlement")

	// ErrFoundingNameTaken, ErrFoundingCurrencyCodeTaken and
	// ErrFoundingCurrencyNameTaken mean another settlement holds the chosen
	// name, or another village has reserved the chosen currency code or name
	// (the unique indexes of migration 0051, hit by a concurrent founding
	// that won the race after the form's own check).
	ErrFoundingNameTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrFoundingNameTaken", "that village name is taken")
	ErrFoundingCurrencyCodeTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrFoundingCurrencyCodeTaken", "that currency code is taken")
	ErrFoundingCurrencyNameTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrFoundingCurrencyNameTaken", "that currency name is taken")

	// ErrFoundingDraftNotFound means no founding draft has that id.
	ErrFoundingDraftNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrFoundingDraftNotFound", "no such founding draft")
)

// Draft statuses (settlement_founding_drafts.status).
const (
	DraftOpen      = "open"
	DraftSubmitted = "submitted"
	DraftExpired   = "expired"
)

// FoundingDraft is a group's founding waiting for its founder to complete the
// village details (migration 0051): nothing is founded until it is submitted.
type FoundingDraft struct {
	ID              string
	ChatID          int64
	BotID           string
	FounderPlayerID string
	// FounderName is the founder's display name, for the group's message and
	// for whoever else opens the link.
	FounderName string
	// Language is the group's language.
	Language string
	// SuggestedName is the generated place name the form starts from, in
	// the group's language; SuggestedNameLatin is its Latin spelling.
	SuggestedName      string
	SuggestedNameLatin string
	Status             string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	SubmittedAt        time.Time
	// SettlementID is the village a submitted draft became.
	SettlementID string
}

// Expired reports a draft that is open but past its time.
func (d FoundingDraft) Expired(now time.Time) bool {
	return d.Status == DraftExpired || (d.Status == DraftOpen && !now.Before(d.ExpiresAt))
}

// VillageCurrency is the national currency a village reserved at its founding.
type VillageCurrency struct {
	Code, Name, Symbol string
}

// WorldRepository reads the world registry and hands out founding indices.
// Reached through Tx.Worlds so a read taken to plan a founding and the
// founding itself see a consistent world.
type WorldRepository interface {
	// Active returns the one active world, or ErrNoActiveWorld.
	Active(ctx context.Context) (World, error)
	// Create writes a new world row and marks it active. It refuses
	// ErrWorldAlreadyActive: the owner chooses the production seed once,
	// through `admin world create`, and a world is never replaced from
	// under a live game (ADR 0028 section 2).
	Create(ctx context.Context, w World) (World, error)
	// ReserveSpawnNumber hands out the next founding index N
	// (internal/domain/settlement.LatticePoint's own N, ADR 0028 section
	// 3.2 step 1): race-safe across as many replicas as are founding
	// settlements at once, by construction — migration 0044's identity
	// column needs no row lock shared between them, unlike an
	// increment-one-counter-row scheme would.
	ReserveSpawnNumber(ctx context.Context) (int64, error)
}

// ExistingSettlement is one already-founded settlement's world cell and
// tier, exactly what internal/domain/settlement.FindSpawn needs for its
// spacing check and threat score (which converts Tier to a weight itself —
// this port stays agnostic of what a tier is worth).
type ExistingSettlement struct {
	WorldCellID int32
	Tier        string
}

// SettlementBuilding is one founding-kit building's placement, mirroring
// internal/domain/settlement.BuildingPlacement (this file avoids importing
// the domain package purely for its type: application ports name their own
// shapes, the same boundary every other port in this package already
// draws).
type SettlementBuilding struct {
	TypeCode   string
	LotX, LotY int
}

// Founding is everything SettlementRepository.Found needs to write one
// settlement's rows: the spot internal/domain/settlement.FindSpawn chose,
// the founding-kit buildings internal/domain/settlement.PlaceFoundingKit
// laid out, and who is founding it.
type Founding struct {
	WorldID     string
	WorldCellID int32
	LatDeg      float64
	LonDeg      float64
	// GridShiftX/GridShiftY slide the village's lot grid from its cell's
	// centre, in whole lots (east/north positive; migration 0053).
	GridShiftX, GridShiftY int
	// Tier is always "village" today (ADR 0028 section 3.1); carried as a
	// field, not a constant, because expedition-founded settlements (a
	// later phase, ADR 0028 section 5.4) start at village tier the same
	// way, through this same port.
	Tier string
	// Code and Name are the settlement's own: Code stable and unique
	// (cities.code), Name the display text a group sees.
	Code, Name string
	// CountryCode is the content code of the country the new village's
	// jurisdiction sits under (governance.yml declares the level's allowed
	// parents; today every founded village sits under the one playable
	// country, content code default_country — DefaultFoundingCountryCode).
	CountryCode     string
	FounderPlayerID string
	// The founding form's choices (migration 0051): the emblem's four codes,
	// the motto, and the currency the village reserves. NameKey is
	// settlement.NameKey(Name), CurrencyNameKey that of the currency's name.
	Emblem          EmblemCodes
	Motto           string
	NameKey         string
	Currency        VillageCurrency
	CurrencyNameKey string
	// FoundedByGroupChatID is the Telegram chat that founded it (negative,
	// like every group chat id in this codebase).
	FoundedByGroupChatID int64
	FoundedByBotID       string
	GroupLanguage        string
	FoundedAt            time.Time
	ProtectedUntil       time.Time
	Buildings            []SettlementBuilding
}

// EmblemCodes are a village emblem's four catalogue codes.
type EmblemCodes struct {
	Shape, ColorA, ColorB, Icon string
}

// FoundedSettlement is what one founding wrote.
type FoundedSettlement struct {
	// Emblem, Motto and Currency are the founding form's choices; empty for
	// a village founded before the form existed.
	Emblem   EmblemCodes
	Motto    string
	Currency VillageCurrency

	CityID         string
	WorldID        string
	Code           string
	Name           string
	JurisdictionID string
	Tier           string
	WorldCellID    int32
	// GridShiftX/GridShiftY: see Founding.
	GridShiftX, GridShiftY int
	// GridGrowth is how many expansions the village bought while land was
	// sold by expansion (migration 0054, retired by ADR 0044: land now opens
	// by roads). The column is kept: the grid's side is the tier's base side
	// plus this, so an old village keeps the grid it had as the first block.
	GridGrowth     int
	FoundedAt      time.Time
	ProtectedUntil time.Time
	Buildings      []SettlementBuilding
}

// SettlementRepository is the transactional port behind founding a
// settlement (ADR 0028 section 3.1), reached through Tx.Settlements so its
// jurisdiction, cities row, vacant office seat, founding-kit buildings and
// Telegram group link commit together or not at all.
type SettlementRepository interface {
	// Found writes one settlement: a jurisdictions row (level "village",
	// under f.CountryJurisdictionID), a cities row (origin "founded"), a
	// vacant seat for every village-level office of the active content
	// version (village_head included — left for the caller to fill, with
	// application.FoundOffice, in the same transaction: seating an office
	// holder is governance's own concern, not a settlement write), the
	// founding-kit buildings and the city_group_links row linking the
	// founding chat to it.
	//
	// It refuses ErrSpawnCellTaken (the world cell is already claimed —
	// cities_world_cell_unique_idx, migration 0042) and
	// ErrGroupAlreadyFounded (the chat already founded one —
	// cities_founded_by_group_unique_idx). Both are for the caller to
	// treat as ordinary outcomes, not faults: the first means "try the
	// next candidate", the second means "nothing to do, the group already
	// has a village".
	Found(ctx context.Context, f Founding) (FoundedSettlement, error)

	// ExistingForWorld returns every founded settlement's cell and tier on
	// one world, for FindSpawn's spacing and threat score. Always read
	// fresh, never cached: a stale list could let two spawns land closer
	// than settlement.min_spawn_distance_km allows.
	ExistingForWorld(ctx context.Context, worldID string) ([]ExistingSettlement, error)

	// ByFoundingGroup returns the settlement this chat already founded, or
	// ErrCityNotFound — the idempotency check a founding command runs
	// before searching for a spot at all, so a redelivered command answers
	// from the existing village instead of running FindSpawn again.
	ByFoundingGroup(ctx context.Context, chatID int64) (FoundedSettlement, error)

	// ByID returns one founded settlement (buildings left empty), or
	// ErrCityNotFound.
	ByID(ctx context.Context, id string) (FoundedSettlement, error)

	// Founded returns every founded settlement, ordered by code (buildings
	// left empty): the places a traveller can go to besides the content
	// cities, with the cell each stands on (ADR 0034).
	Founded(ctx context.Context) ([]FoundedSettlement, error)

	// ByPlayer returns the settlement a player belongs to: the one whose
	// top office they hold (head), else the one they live in (their residence).
	// Buildings are left empty. ErrCityNotFound when they belong to none.
	// A game client has no Telegram group to name its settlement by, so it
	// is resolved from the player (docs/adr/0028 section 9.4).
	ByPlayer(ctx context.Context, playerID string) (PlayerSettlement, error)

	// CreateDraft opens a founding draft for d.ChatID, first marking any
	// open draft of that chat whose time is up as expired. When another
	// draft of the chat is still open (a concurrent «ساخت روستا», or a
	// redelivery) it returns that one and created is false: one open draft
	// per group.
	CreateDraft(ctx context.Context, d FoundingDraft, now time.Time) (draft FoundingDraft, created bool, err error)
	// OpenDraftOfChat is the chat's open, unexpired draft, or
	// ErrFoundingDraftNotFound.
	OpenDraftOfChat(ctx context.Context, chatID int64, now time.Time) (FoundingDraft, error)
	// DraftByID reads a draft, locking its row for the rest of the
	// transaction, or ErrFoundingDraftNotFound. An open draft past its time
	// is returned with status expired (and stored so).
	DraftByID(ctx context.Context, id string, now time.Time) (FoundingDraft, error)
	// OpenDraftOfPlayer is the player's open, unexpired draft, or
	// ErrFoundingDraftNotFound.
	OpenDraftOfPlayer(ctx context.Context, playerID string, now time.Time) (FoundingDraft, error)
	// MarkDraftSubmitted records that the draft became settlementID.
	MarkDraftSubmitted(ctx context.Context, draftID, settlementID string, at time.Time) error
	// FoundingNameTaken reports whether a settlement or city already has a
	// name with this key (settlement.NameKey).
	FoundingNameTaken(ctx context.Context, nameKey string) (bool, error)
	// CurrencyTaken reports whether the code is an existing or reserved
	// currency, and whether the name key is a reserved currency name.
	CurrencyTaken(ctx context.Context, code, nameKey string) (codeTaken, nameTaken bool, err error)

	// Promote moves a settlement one tier up (ADR 0028 section 4.1) when it
	// is still at p.From: cities.tier and its jurisdiction's kind both take
	// p.To, the new tier's office seats are created vacant (an existing seat
	// is kept), and the audit row is written - one compare-and-swap, so a
	// redelivered or concurrent promotion changes nothing and reports false.
	// The caller seats the office holder, in the same transaction.
	Promote(ctx context.Context, p SettlementPromotion) (changed bool, err error)

	// ResidentCount is how many active players have this settlement as
	// their home (players.residence_city_id): its population.
	ResidentCount(ctx context.Context, settlementID string) (int64, error)
}

// PlayerSettlement is a settlement as seen by one of its people.
type PlayerSettlement struct {
	FoundedSettlement
	// Offices are the office codes the player holds in the settlement's
	// jurisdiction.
	Offices []string
	// Resident reports that the player's home (residence) is this settlement.
	Resident bool
}

// SettlementPromotion is one step up the tier ladder.
type SettlementPromotion struct {
	SettlementID   string
	JurisdictionID string
	From, To       string
	// Actor is the player who promoted it, for the audit row.
	Actor string
	At    time.Time
}
