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
)

// Player is the identity record shared across the whole bot fleet.
//
// A player is one person, identified by TelegramUserID, regardless of which
// bot they happened to talk to. Nothing here is scoped to a bot.
type Player struct {
	ID             string
	TelegramUserID int64
	Username       string
	DisplayName    string
	Language       string
	CityID         *string
	Status         string
	CreatedAt      time.Time
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
type Tx interface {
	Players() PlayerRepository
	Outbox() OutboxRepository
	Idempotency() IdempotencyRepository
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
}

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
