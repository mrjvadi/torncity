// Package handlers holds the command use cases.
//
// A handler orchestrates: it validates idempotency, opens one unit of work,
// asks repositories for state, applies domain rules, appends the outbox record
// and returns a presentation model. It never renders Telegram output and never
// publishes to the broker directly — the outbox worker does that, so an event
// cannot survive a rolled-back transaction or be lost after a committed one.
package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// IdempotencyTTL is how long a command key blocks a replay.
//
// It must comfortably exceed the broker's maximum redelivery window: a
// redelivery arriving after the key expired would be processed twice.
const IdempotencyTTL = 24 * time.Hour

// IDGenerator produces identifiers. It is a port so tests get deterministic ids.
type IDGenerator interface {
	NewID() string
}

// ProfileHandler serves player.profile.get, the phase 0 command.
//
// It also performs first contact: a Telegram user who has never played gets a
// player record here. That record is keyed on telegram_user_id alone, so the
// same person reaching the game through any bot in the fleet is the same
// player. See docs/adr/0001-telegram-bot-fleet.md.
type ProfileHandler struct {
	uow application.UnitOfWork
	ids IDGenerator
	now func() time.Time
}

// NewProfileHandler wires the handler. now may be nil, in which case UTC wall
// clock is used; tests inject a fixed clock.
func NewProfileHandler(uow application.UnitOfWork, ids IDGenerator, now func() time.Time) *ProfileHandler {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ProfileHandler{uow: uow, ids: ids, now: now}
}

// Handle processes one player.profile.get command.
func (h *ProfileHandler) Handle(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	var player *application.Player

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.ensurePlayer(ctx, tx, meta)
		if err != nil {
			return err
		}
		player = p

		// Reserve after the player exists, because the key is scoped to the
		// player id. A replay of the same request finds the key taken and
		// skips the side effect, but still returns the profile below.
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, IdempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}

		return tx.Players().LinkBot(ctx, application.BotLink{
			PlayerID:       p.ID,
			BotID:          meta.BotID,
			TelegramChatID: meta.TelegramChatID,
			IsReachable:    true,
		})
	})
	if err != nil {
		return nil, err
	}

	return renderProfile(player), nil
}

// ensurePlayer loads the player, creating one on first contact and emitting
// player.created through the outbox in the same transaction.
func (h *ProfileHandler) ensurePlayer(ctx context.Context, tx application.Tx, meta envelope.Metadata) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err == nil {
		return p, nil
	}
	if !stderrors.Is(err, application.ErrPlayerNotFound) {
		return nil, err
	}

	lang := meta.Language
	if lang == "" {
		lang = "fa"
	}
	p = &application.Player{
		ID:             h.ids.NewID(),
		TelegramUserID: meta.TelegramUserID,
		DisplayName:    displayName(meta),
		Language:       lang,
		Status:         "active",
		CreatedAt:      h.now(),
	}
	if err := tx.Players().Create(ctx, p); err != nil {
		return nil, err
	}

	ev, err := events.New("player.created", "player", p.ID, map[string]any{
		"player_id":        p.ID,
		"telegram_user_id": p.TelegramUserID,
		"language":         p.Language,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID:  ev.ID,
		Subject:  subjects.Event("player", "created"),
		Metadata: meta,
		Payload:  ev.Payload,
	}); err != nil {
		return nil, err
	}
	return p, nil
}

func displayName(meta envelope.Metadata) string {
	if meta.Command != "" && meta.TelegramUserID != 0 {
		// A display name is cosmetic; the identity that matters is the
		// Telegram user id. Anything better arrives with a later command.
		return "player"
	}
	return "player"
}

func renderProfile(p *application.Player) *presenter.Response {
	if p == nil {
		return presenter.Message("پروفایل در دسترس نیست.", nil)
	}
	return presenter.Message(
		"پروفایل\n\nشناسه: "+p.ID+"\nزبان: "+p.Language+"\nوضعیت: "+p.Status,
		&presenter.Keyboard{Rows: [][]presenter.Button{{
			{Text: "بروزرسانی", CallbackData: "player:profile.get"},
		}}},
	)
}
