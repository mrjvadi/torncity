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
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// How long a command key blocks a replay is NOT declared here any more. It is
// game.idempotency_ttl in configs/config.yml, injected through
// NewProfileHandler, and internal/config refuses to load a value that does not
// outlast the broker's redelivery schedule — a check this package could not
// make, because it cannot see the broker's settings.

// IDGenerator produces identifiers. It is a port so tests get deterministic ids.
type IDGenerator interface {
	NewID() string
}

// Translator resolves a message key for a language.
//
// It is declared here, at the point of use, so the handler depends on the
// one method it needs rather than on a concrete catalogue. Both a loaded
// catalogue and a hot-reloadable store satisfy it, which is what lets the
// text change under a running process without this package knowing.
type Translator interface {
	T(lang, key string, args map[string]any) string
}

// ProfileHandler serves player.profile.get, the phase 0 command.
//
// It also performs first contact: a Telegram user who has never played gets a
// player record here. That record is keyed on telegram_user_id alone, so the
// same person reaching the game through any bot in the fleet is the same
// player. See docs/adr/0001-telegram-bot-fleet.md.
type ProfileHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	msgs           Translator
	stats          application.StatsRepository
	cities         application.CityRepository
	defaultL       string
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewProfileHandler wires the handler.
//
// msgs is injected rather than read from a package global so that a test, a
// second deployment or a future per-bot catalogue can supply its own text.
// It may be nil, in which case messages resolve to their keys: an unwired
// catalogue then shows "profile.title" in the chat, which is wrong in an
// obvious way instead of crashing a player's session.
//
// now may be nil, in which case UTC wall clock is used; tests inject a fixed
// clock.
// defaultLanguage is the language stamped on a player record when Telegram
// told us nothing. It is injected rather than fixed here because it is a
// product setting, and because a player's stored language is not the same
// thing as the message catalogue's fallback: the catalogue falls back so a
// screen still renders, while this decides what a new account IS. An empty
// value is rejected, so a caller cannot forget to make the choice.
//
// idempotencyTTL is how long a reserved key blocks a replay. It is injected
// for the same reason and rejected at zero for a sharper one: a zero TTL is
// read by the idempotency store as an expiry that has already passed, so every
// redelivery would be executed as if it were new and a player would be charged
// twice. It must outlast the broker's redelivery schedule, which
// internal/config validates.
//
// stats and cities are the phase 1 repositories the profile reads. See the
// note at the top of phase1.go for why they are injected here rather than
// reached through the unit of work.
func NewProfileHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	stats application.StatsRepository,
	cities application.CityRepository,
	defaultLanguage string,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *ProfileHandler {
	if defaultLanguage == "" {
		panic("handlers: NewProfileHandler requires a default language")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewProfileHandler requires a positive idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &ProfileHandler{
		uow:            uow,
		ids:            ids,
		msgs:           msgs,
		stats:          stats,
		cities:         cities,
		defaultL:       defaultLanguage,
		idempotencyTTL: idempotencyTTL,
		now:            now,
	}
}

// keyTranslator is the no-catalogue fallback described on NewProfileHandler.
type keyTranslator struct{}

func (keyTranslator) T(_, key string, _ map[string]any) string { return key }

// Handle processes one player.profile.get command.
func (h *ProfileHandler) Handle(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	var (
		record *application.Player
		view   screens.ProfileView
	)

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.ensurePlayer(ctx, tx, meta)
		if err != nil {
			return err
		}
		record = p

		// Reserve after the player exists, because the key is scoped to the
		// player id. A replay of the same request finds the key taken and
		// skips the side effect, but still returns the profile below.
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if fresh {
			if err := tx.Players().LinkBot(ctx, application.BotLink{
				PlayerID:       p.ID,
				BotID:          meta.BotID,
				TelegramChatID: meta.TelegramChatID,
				IsReachable:    true,
			}); err != nil {
				return err
			}
		}

		// The condition is read on EVERY delivery, replay or not. It is a
		// read, so repeating it changes nothing, and suppressing it would
		// answer a redelivered request with a blank profile.
		view, err = h.condition(ctx, p)
		return err
	})
	if err != nil {
		return nil, err
	}

	return h.renderProfile(meta, record, view), nil
}

// condition loads the player's live state, catching their energy up to now.
//
// # Reading your profile is how energy catches up
//
// There is no ticker anywhere that tops players up. Energy accrues as a pure
// function of elapsed time (player.RegenerateEnergy), and the elapsed time is
// measured from the row's own updated_at, so the amount a player has is fully
// determined by when they last looked — not by whether a background job was
// running, whether the process restarted, or how many players exist. A
// hundred thousand idle accounts cost nothing, because nobody pays to
// regenerate energy for a player who is not there to spend it.
//
// The price is that a read WRITES. That is deliberate and it is the whole
// mechanism: if the caught-up value were not persisted, the next read would
// measure from the same old timestamp and regenerate the same energy again,
// and a player refreshing the screen would watch their bar refill for free.
func (h *ProfileHandler) condition(ctx context.Context, p *application.Player) (screens.ProfileView, error) {
	view := screens.ProfileView{
		ID:       p.ID,
		Language: p.Language,
		Status:   p.Status,
	}

	row, err := h.stats.EnsureDefaults(ctx, p.ID, defaultStats(p.ID, h.now()))
	if err != nil {
		return view, err
	}

	regenerated, changed := regenerateEnergy(*row, h.now())
	if changed {
		if err := h.stats.Save(ctx, regenerated); err != nil {
			return view, err
		}
	}

	view.Level = regenerated.Level
	view.XP = regenerated.XP
	view.Energy = regenerated.Energy
	view.MaxEnergy = regenerated.MaxEnergy
	view.Health = regenerated.Health
	view.MaxHealth = regenerated.MaxHealth

	if p.CityID != nil && *p.CityID != "" {
		city, err := h.cities.ByID(ctx, *p.CityID)
		if err != nil {
			// A player whose city has gone missing from content still has a
			// profile. The screen shows that they are nowhere, which is
			// visibly odd and therefore gets noticed, instead of failing the
			// one screen a player opens to find out what is wrong.
			if !isSentinel(err, application.ErrCityNotFound) {
				return view, err
			}
		} else {
			view.City = city.Name
		}
	}
	return view, nil
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
		lang = h.defaultL
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

// displayName is the name written on a record this handler creates.
//
// The request envelope carries no Telegram first or last name, so there is
// nothing better available here. In practice the gateway resolves identity
// before publishing and creates the player with the real name, which makes
// this the fallback for a command that somehow reached the core first.
//
// It deliberately does NOT return a constant like "player": every such record
// would then be indistinguishable in an admin screen or a support request. The
// Telegram user id is the one identifying fact on hand, so the name is derived
// from it and is at least unique and traceable back to the account.
func displayName(meta envelope.Metadata) string {
	return "player-" + strconv.FormatInt(meta.TelegramUserID, 10)
}

// renderProfile hands the view to the screen that lays it out.
//
// The layout lives in internal/telegram/screens, not here: a use case decides
// what is true and a screen decides what it looks like. Every word of it
// still comes from the catalogue.
func (h *ProfileHandler) renderProfile(meta envelope.Metadata, p *application.Player, view screens.ProfileView) *presenter.Response {
	c := screens.Context{Msgs: h.msgs, Lang: meta.Language, MessageID: editableMessageID(meta)}
	if p == nil {
		return presenter.Message(c.T("profile.unavailable", nil), nil)
	}
	return screens.Profile(c, view)
}
