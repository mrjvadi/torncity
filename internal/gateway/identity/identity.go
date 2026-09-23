// Package identity maps a Telegram user to the one global player behind them.
//
// # The global identity rule
//
// A player is identified by telegram_user_id and by nothing else. It is never
// combined with a bot id, not in a key, not in a lookup, not in a cache entry.
// ADR 0001 constraint 1 states it as a requirement and MASTER_PROMPT section 8
// draws the same path: Telegram user, identity resolver, player id, domain.
//
// The reason is the entire premise of the fleet. Ten bots exist for fault
// isolation and outbound capacity, not to run ten games. A player who started
// with bot01 and a player who started with bot07 are in the same world: they
// share one economy, one market, one set of factions, and they can attack,
// trade and work together. Key identity by (telegram_user_id, bot_id) and that
// stops being true in the most expensive possible way — quietly. Nothing
// fails, no test goes red; there are simply two of everyone, each with their
// own money, and the game has silently split into parallel worlds that cannot
// be merged afterwards without inventing money out of thin air.
//
// The bot id is transport metadata. It answers "which bot can reach this
// player", which is what application.BotLink records, and it never answers
// "who is this player".
//
// # Why the port is declared here
//
// EnsurePlayer is a database operation, and MASTER_PROMPT section 1 sends
// dependencies inward: the gateway declares the capability it needs and
// internal/infrastructure implements it. No SQL lives in this package, and the
// resolver is testable with nothing but a fake.
package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	gwcontext "github.com/mrjvadi/torncity/internal/gateway/context"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// PlayerStore is the persistence capability the resolver needs.
//
// EnsurePlayer is one call on purpose. "Look up, and create if missing" has to
// be atomic: two updates from the same person arriving at two gateway
// instances at the same moment must produce one player, not two. Splitting it
// into Get and Create would move that race into this package, where it cannot
// be won, instead of leaving it in the database, where an upsert settles it.
//
// The implementation is expected to:
//
//   - find or create the player keyed on telegramUserID ALONE,
//   - refresh the profile fields it was given, and
//   - record the player's link to botID and chatID (application.BotLink), so
//     a later notification knows which bot can reach them.
//
// botID and chatID are arguments to the link, never to the identity lookup.
type PlayerStore interface {
	EnsurePlayer(
		ctx context.Context,
		telegramUserID int64,
		username, displayName, language string,
		botID string,
		chatID int64,
	) (*application.Player, error)
}

// Failures.
var (
	ErrNoStore = errors.New("identity: a player store is required")

	// ErrNoTelegramUser means the update had no usable sender, so there is
	// nobody to resolve.
	ErrNoTelegramUser = errors.New("identity: update has no telegram user")

	// ErrNoBotID means the caller did not say which bot received the update.
	// It is required for the bot link, not for the identity.
	ErrNoBotID = errors.New("identity: bot id is required")

	// ErrNoDefaultLanguage means the caller did not say what language a
	// player gets when Telegram sends none. It comes from
	// player.default_language and is required rather than defaulted, so this
	// package cannot become a second place the value is written down.
	ErrNoDefaultLanguage = errors.New("identity: default language is required")

	// ErrUnsupportedUpdate means the update is of a type this package does
	// not read a user from.
	ErrUnsupportedUpdate = errors.New("identity: update carries neither a message nor a callback query")

	// ErrNoPlayer means the store returned neither a player nor an error.
	ErrNoPlayer = errors.New("identity: the player store returned no player")
)

// Identity is the Telegram-side view of one person: what Telegram told us
// about them plus where we were talking to them.
//
// TelegramUserID is the identity. Everything else is either a profile detail
// that may change at any time, or transport metadata.
type Identity struct {
	TelegramUserID int64

	Username    string
	DisplayName string
	Language    string

	// BotID and ChatID describe the conversation, not the person. They feed
	// the bot link; they must never reach a player lookup key.
	//
	// ChatID is the player's PRIVATE chat with the bot, the one notices are
	// sent to. It is 0 for an update from a group: recording the group as
	// the player's chat would deliver their notices to the whole room, and a
	// player who has only ever played in a group has no private chat the
	// bot may write to.
	BotID  string
	ChatID int64
}

// FromUpdate reads the identity out of a message or a callback query.
//
// defaultLanguage is what Identity.Language becomes when Telegram sent no
// usable language_code; it is player.default_language from the configuration.
func FromUpdate(update client.Update, botID, defaultLanguage string) (Identity, error) {
	botID = strings.TrimSpace(botID)
	if botID == "" {
		return Identity{}, ErrNoBotID
	}
	if strings.TrimSpace(defaultLanguage) == "" {
		return Identity{}, ErrNoDefaultLanguage
	}

	var (
		from     *client.User
		chatID   int64
		chatType string
	)
	switch {
	case update.CallbackQuery != nil:
		from = &update.CallbackQuery.From
		if update.CallbackQuery.Message != nil {
			chatID = update.CallbackQuery.Message.Chat.ID
			chatType = update.CallbackQuery.Message.Chat.Type
		}
	case update.Message != nil:
		from = update.Message.From
		chatID = update.Message.Chat.ID
		chatType = update.Message.Chat.Type
	case update.EditedMessage != nil:
		from = update.EditedMessage.From
		chatID = update.EditedMessage.Chat.ID
		chatType = update.EditedMessage.Chat.Type
	default:
		return Identity{}, ErrUnsupportedUpdate
	}

	if from == nil || from.ID == 0 {
		return Identity{}, ErrNoTelegramUser
	}
	if chatType != "private" {
		chatID = 0
	}

	return Identity{
		TelegramUserID: from.ID,
		Username:       from.Username,
		DisplayName:    displayName(from),
		Language:       gwcontext.NormalizeLanguage(from.LanguageCode, defaultLanguage),
		BotID:          botID,
		ChatID:         chatID,
	}, nil
}

// displayName is what the game shows before a player picks a name. Telegram
// guarantees a first name and nothing else.
func displayName(u *client.User) string {
	return strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
}

// Resolver turns an Identity into the global player.
type Resolver struct {
	store           PlayerStore
	defaultLanguage string
}

// NewResolver builds a Resolver.
//
// defaultLanguage is player.default_language from the configuration: the
// language stamped on a player record created for someone whose Telegram
// client told us nothing usable.
func NewResolver(store PlayerStore, defaultLanguage string) (*Resolver, error) {
	if store == nil {
		return nil, ErrNoStore
	}
	if strings.TrimSpace(defaultLanguage) == "" {
		return nil, ErrNoDefaultLanguage
	}
	return &Resolver{store: store, defaultLanguage: defaultLanguage}, nil
}

// Resolve returns the player for this Telegram user, creating them on first
// contact.
//
// Note what is not passed to the lookup: the bot id goes to the store as link
// information, and there is no code path in which it narrows the search for
// the player. See the package doc.
func (r *Resolver) Resolve(ctx context.Context, id Identity) (*application.Player, error) {
	if id.TelegramUserID == 0 {
		return nil, ErrNoTelegramUser
	}
	if strings.TrimSpace(id.BotID) == "" {
		return nil, ErrNoBotID
	}
	if id.Language == "" {
		id.Language = r.defaultLanguage
	}

	player, err := r.store.EnsurePlayer(
		ctx,
		id.TelegramUserID,
		id.Username,
		id.DisplayName,
		id.Language,
		id.BotID,
		id.ChatID,
	)
	if err != nil {
		return nil, err
	}
	if player == nil {
		return nil, ErrNoPlayer
	}
	return player, nil
}

// ResolveUpdate is FromUpdate followed by Resolve, which is what the update
// pipeline does on every inbound message.
func (r *Resolver) ResolveUpdate(ctx context.Context, update client.Update, botID string) (*application.Player, error) {
	id, err := FromUpdate(update, botID, r.defaultLanguage)
	if err != nil {
		return nil, err
	}
	return r.Resolve(ctx, id)
}

// WithPlayer stamps the resolved player onto the request context, which is the
// WHO of MASTER_PROMPT section 7 and the field every domain handler works
// with.
//
// The language already on the metadata wins: it came from this update, while
// the stored player language may be days old. A player who switched their
// Telegram language should see the change on the next message, not after a
// profile update.
func WithPlayer(meta envelope.Metadata, player *application.Player) envelope.Metadata {
	if player == nil {
		return meta
	}
	meta.PlayerID = player.ID
	if meta.Language == "" {
		meta.Language = player.Language
	}
	return meta
}
