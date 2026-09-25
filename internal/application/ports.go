// Package application holds use cases and the ports they depend on.
//
// Every dependency an application handler needs is declared here as an
// interface. Infrastructure and gateway packages implement these; nothing in
// this package imports a driver, a broker or an HTTP library. That direction
// is what tests/architecture_test.go protects for the domain layer and what
// keeps a use case testable with nothing but fakes.
package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// Player is the identity record shared across the whole bot fleet.
//
// A player is one person, identified by TelegramUserID, regardless of which
// bot they happened to talk to. Nothing here is scoped to a bot.
type Player struct {
	ID             string
	TelegramUserID int64
	// Username is the Telegram username as Telegram last reported it,
	// without the @, or empty when the account has none. It is refreshed on
	// every contact (SetUsername), because usernames move between people.
	Username    string
	DisplayName string
	// PublicCode is the short code the player sees on their profile and
	// gives to friends (internal/shared/playercode). It is assigned once, by
	// Create, and never changes. Unlike ID it is meant to be shown.
	PublicCode string
	Language   string
	CityID     *string
	Status     string
	CreatedAt  time.Time
}

// BotLink records that a player has an open chat with one specific bot.
//
// Telegram only lets a bot message users who started that bot, so a
// notification must be sent through a bot the player is actually linked to.
type BotLink struct {
	PlayerID       string
	BotID          string
	TelegramChatID int64
	IsReachable    bool
}

// Bot is one row of the bot registry.
//
// TokenSecretRef is the NAME of a secret, never the secret itself.
type Bot struct {
	ID             string
	BotKey         string
	TelegramBotID  int64
	Username       string
	TokenSecretRef string
	Status         string
	GatewayGroup   string
	Enabled        bool
	RateLimit      int
}

// OutboxRecord is a domain event queued for publication inside the same
// transaction that changed state. See MASTER_PROMPT section 21.
type OutboxRecord struct {
	EventID  string
	Subject  string
	Metadata envelope.Metadata
	Payload  []byte
}

// Tx is a unit of work. Everything a command changes, including its outbox
// record, commits or rolls back together.
//
// Every repository a handler WRITES through is reachable here, because a write
// made on any other connection commits on its own: a later step that fails
// then rolls back the idempotency reservation and the outbox record but not
// that write, and the retry finds a world that no longer needs the step it is
// retrying. Landing a journey is the concrete case — the arrival committed, the
// XP award rolled back, and the redelivery saw no journey to land.
//
// CityRepository and PlayerSearch are deliberately NOT here. Both are
// read-only: cities are content that only the loader writes, and search reads
// other players' public records. A read that no write in the same command
// depends on for correctness gains nothing from the transaction and would
// only lengthen it, so those two stay injected where they are used.
type Tx interface {
	Players() PlayerRepository
	Outbox() OutboxRepository
	Idempotency() IdempotencyRepository

	Stats() StatsRepository
	Skills() SkillRepository
	Travels() TravelRepository
	GameActions() GameActionRepository
	Friendships() FriendshipRepository

	// Ledger moves money in the same transaction as the change that
	// caused it; see ports_ledger.go.
	Ledger() LedgerRepository

	// Governance changes levers and seats office holders, in the same
	// transaction as the public record of the change; see governance.go.
	Governance() GovernanceRepository

	// Bank locks where players are while money moves between them; see
	// bank.go.
	Bank() BankRepository

	// Employment and Education hold a player's job and their study, so a
	// shift commits with its wage and a course with its fee; see
	// ports_jobs.go.
	Employment() EmploymentRepository
	Education() EducationRepository

	// Crime holds the crime engine's state, so an attempt commits with its
	// nerve, its money and its sentence; see ports_crime.go.
	Crime() CrimeRepository

	// Places holds where players stand inside their city and their walks
	// between places, so a walk commits with its energy and its schedule;
	// see ports_places.go.
	Places() PlaceRepository

	// Items, Shops, Market and Auctions hold goods and their trade, so an
	// item moves with the money that paid for it; see ports_items.go.
	Items() ItemRepository
	Shops() ShopRepository
	Market() MarketRepository
	Auctions() AuctionRepository
	Elections() ElectionRepository

	// Companies holds player companies, so a company changes with the
	// money that moved for it; see ports_companies.go.
	Companies() CompanyRepository

	// Production holds the production economy — designs, research and
	// licenses, orders, reverse engineering, listings — so each changes
	// with the goods and money that moved for it; see ports_production.go.
	Production() ProductionRepository

	// Military and Diplomacy hold the armed forces and the relations
	// between countries — defence periods, arms bought, garrisons,
	// sanctions, treaties — so each changes with the money and goods that
	// moved for it; see ports_military.go.
	Military() MilitaryRepository
	Diplomacy() DiplomacyRepository

	// War holds wars, their operations, the damage and occupation of
	// cities, so each changes with the equipment and money that moved for
	// it; see ports_war.go.
	War() WarRepository

	// Health, Missions, Factions and Watch are stage E
	// (docs/adr/0023-health-missions-factions.md): hospital stays and
	// treatments, missions and their inbox, factions and their organised
	// crimes, and the watch's flags and held payments — each changing with
	// the money, goods and state that moved for it; see ports_health.go,
	// ports_missions.go, ports_factions.go and ports_watch.go.
	Health() HealthRepository
	Missions() MissionRepository
	Factions() FactionRepository
	Watch() WatchRepository

	// Legislature and CityPeriods are stage F
	// (docs/adr/0024-property-and-politics.md): proposals put to a body's
	// vote, and each city's period with its budget — each changing with the
	// policy, action and money that moved for it; see ports_legislature.go
	// and ports_city.go.
	Legislature() LegislatureRepository
	CityPeriods() CityPeriodRepository
	// Property holds property, its listings, leases and period charges; see
	// ports_property.go.
	Property() PropertyRepository
	// Achievements holds progress toward achievements and what was earned;
	// see ports_achievements.go.
	Achievements() AchievementRepository
}

// UnitOfWork runs fn inside a single database transaction.
//
// The implementation must roll back when fn returns an error and must not
// swallow a rollback failure.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// PlayerRepository is the persistence port for player identity.
type PlayerRepository interface {
	// GetByTelegramUserID returns the player, or ErrPlayerNotFound.
	GetByTelegramUserID(ctx context.Context, telegramUserID int64) (*Player, error)
	// Create inserts a new player. It must be safe under a race: two
	// concurrent first-contact requests for the same Telegram user must
	// result in one player, not two.
	Create(ctx context.Context, p *Player) error
	// LinkBot records or refreshes the player's chat with one bot.
	LinkBot(ctx context.Context, link BotLink) error
	// GetByID returns the player with this id, or ErrPlayerNotFound. It is
	// for commands that name their player directly instead of arriving from
	// a Telegram user, such as a journey the scheduler lands.
	GetByID(ctx context.Context, id string) (*Player, error)
	// SetLanguage stores the language the player chose to play in, or
	// returns ErrPlayerNotFound when no such player exists. The repository
	// stores what it is given: whether lang is a language the game speaks is
	// the caller's question to answer first (see ErrUnsupportedLanguage),
	// because only the message catalogue knows.
	SetLanguage(ctx context.Context, playerID, lang string) error
	// SetUsername records the Telegram username Telegram reports for the
	// player now; an empty username clears it. Whoever is seen holding a
	// username takes it: the same write clears it from any other player
	// whose record still claims it, because Telegram lets one account hold a
	// username at a time and that other record is simply out of date. It
	// returns ErrPlayerNotFound when no such player exists.
	SetUsername(ctx context.Context, playerID, username string) error
}

// ErrUnsupportedLanguage refuses a language the message catalogue does not
// have. A player's language is chosen from the languages the game ships, never
// written as a free string: a stored code nothing can render would silently
// put the player back on the fallback language with no way to tell why.
var ErrUnsupportedLanguage = errors.Sentinel(errors.CodeInvalidInput,
	"application.ErrUnsupportedLanguage", "language not supported")

// OutboxRepository appends events for the outbox worker to publish.
type OutboxRepository interface {
	Append(ctx context.Context, rec OutboxRecord) error
}

// IdempotencyRepository records which command keys have already been handled.
type IdempotencyRepository interface {
	// Reserve stores the key and reports whether it was newly created.
	// A false return means this command was already processed.
	Reserve(ctx context.Context, key, playerID, requestID, command string, ttl time.Duration) (fresh bool, err error)
}

// BotRegistry reads the bot fleet. The game core never knows how many bots
// exist; only the gateway asks this.
type BotRegistry interface {
	ListEnabled(ctx context.Context) ([]Bot, error)
}

// SecretResolver turns a Bot.TokenSecretRef into the secret it names.
//
// Implementations must never log, wrap or return the resolved value inside an
// error. See docs/adr/0002-secret-management.md.
type SecretResolver interface {
	Resolve(ref string) (string, error)
}

// Deduplicator suppresses Telegram updates that arrive more than once.
type Deduplicator interface {
	// Seen reports whether this (botID, updateID) pair was already handled,
	// and records it if not.
	Seen(ctx context.Context, botID string, updateID int64) (bool, error)
}

// PlayerLocker serialises critical operations for one player.
type PlayerLocker interface {
	// Lock acquires the player's lock or returns an error. The returned
	// release function must be safe to call exactly once.
	Lock(ctx context.Context, playerID string, ttl time.Duration) (release func(), err error)
}

// Publisher sends a message to the messaging backbone.
type Publisher interface {
	Publish(ctx context.Context, subject string, env *envelope.Envelope) error
}

// EventPublisher publishes with an explicit broker deduplication id.
//
// Plain Publish identifies a message by its request id, which is correct for a
// command: one command, one message. It is wrong for events, because one
// command may append several outbox rows and they would then share a single
// deduplication id — the broker would drop all but the first, silently. The
// outbox worker passes the outbox event id instead, which is unique per row.
type EventPublisher interface {
	Publisher
	PublishWithID(ctx context.Context, subject string, env *envelope.Envelope, dedupID string) error
}
