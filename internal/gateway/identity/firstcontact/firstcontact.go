// Package firstcontact is how a Telegram user becomes a player: the one
// transaction that finds the player by their Telegram user id or creates
// them, grants the starting cash, announces player.created through the
// outbox and records the bot they reached us through.
//
// It is shared by every edge that meets a Telegram user: the gateway, for an
// update, and the client API (cmd/clientapi), for a Telegram Mini App
// sign-in. Both must create the same player the same way, so the code is
// here once.
package firstcontact

import (
	"context"
	"errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// metaKey carries the request context down to Store.EnsurePlayer.
type metaKey struct{}

// WithMeta carries the request context into EnsurePlayer: a creation writes
// an outbox row whose metadata must be a valid request context, and
// identity.PlayerStore's signature is about identity, not tracing.
func WithMeta(ctx context.Context, meta envelope.Metadata) context.Context {
	return context.WithValue(ctx, metaKey{}, meta)
}

func metaFrom(ctx context.Context) envelope.Metadata {
	meta, _ := ctx.Value(metaKey{}).(envelope.Metadata)
	return meta
}

// DefaultSource is the reason the starting cash is granted under when a
// Store names none.
const DefaultSource = "gateway.first_contact"

func (s *Store) source() string {
	if s.Source != "" {
		return s.Source
	}
	return DefaultSource
}

// Store adapts the application's unit of work to identity.PlayerStore.
//
// identity.Resolver asks one question — "who is this Telegram user, globally"
// — and needs an answer before the command is published, because the envelope
// carries player_id and the idempotency key is scoped to it. So first contact
// happens here, at the edge, in one transaction:
//
//	read, create if absent, announce the creation, record the bot link.
//
// The player.created event is appended to the outbox in that same transaction
// rather than published directly, for the usual reason: an event published
// after a rollback describes a player who does not exist, and an event
// published before a commit can be lost. The outbox worker drains it.
type Store struct {
	UOW application.UnitOfWork

	// StartingCash is granted once, in the transaction that creates the
	// player, so a person never exists in the world without the money they
	// were promised, and a retried first contact never pays twice: the grant
	// is idempotent on the player.
	StartingCash money.Amount

	// Source is the reason the grant is recorded under; empty is
	// DefaultSource.
	Source string
}

func (s *Store) EnsurePlayer(
	ctx context.Context,
	telegramUserID int64,
	username, displayName, language string,
	botID string,
	chatID int64,
) (*application.Player, error) {
	var player *application.Player

	err := s.UOW.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, telegramUserID)
		switch {
		case err == nil:
			// Usernames change and move between people, and a player can be
			// found by theirs. So what Telegram reports on this update is
			// written back whenever it differs from the record — including
			// "none", which clears it. Left stale, a search for the old
			// @username would find this player after they gave it up, or
			// after somebody else took it. Nothing is written when nothing
			// changed, which is almost every update.
			if p.Username != username {
				if err := tx.Players().SetUsername(ctx, p.ID, username); err != nil {
					return err
				}
				p.Username = username
			}
			player = p

		case errors.Is(err, application.ErrPlayerNotFound):
			p = &application.Player{
				TelegramUserID: telegramUserID,
				Username:       username,
				DisplayName:    displayName,
				Language:       language,
				Status:         "active",
			}
			// Create is an upsert that returns the surviving row, so two
			// concurrent first contacts for the same person both come out of
			// here holding the same id rather than one of them failing.
			if err := tx.Players().Create(ctx, p); err != nil {
				return err
			}
			player = p

			if _, err := application.GrantStartingCash(ctx, tx.Ledger(), p.ID,
				s.StartingCash, s.source(), time.Now().UTC()); err != nil {
				return err
			}

			ev, err := events.New("player.created", "player", p.ID, map[string]any{
				"player_id":        p.ID,
				"telegram_user_id": p.TelegramUserID,
				"language":         p.Language,
			})
			if err != nil {
				return err
			}
			meta := metaFrom(ctx)
			meta.PlayerID = p.ID
			if err := tx.Outbox().Append(ctx, application.OutboxRecord{
				EventID:  ev.ID,
				Subject:  subjects.Event("player", "created"),
				Metadata: meta,
				Payload:  ev.Payload,
			}); err != nil {
				return err
			}

		default:
			return err
		}

		if chatID == 0 {
			// The update came from a group (identity.Identity.ChatID): the
			// player has no private chat with this bot to record, and the
			// group must never become the chat their notices go to.
			return nil
		}
		return tx.Players().LinkBot(ctx, application.BotLink{
			PlayerID:       player.ID,
			BotID:          botID,
			TelegramChatID: chatID,
			IsReachable:    true,
		})
	})
	if err != nil {
		return nil, err
	}

	return player, nil
}
