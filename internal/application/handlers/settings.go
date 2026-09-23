package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// LanguageRequest is the payload of player.language.set.
type LanguageRequest struct {
	Lang string `json:"lang"`
}

// LanguageSource lists the languages the game can render. It is the message
// catalogue in production — the one authority on which languages exist — and
// is read per request, so a reloaded catalogue with a new locale is offered
// without a restart.
type LanguageSource interface {
	Languages() []string
}

// SettingsHandler serves the settings screen and the changes made from it.
type SettingsHandler struct {
	uow            application.UnitOfWork
	msgs           Translator
	languages      LanguageSource
	idempotencyTTL time.Duration
}

// NewSettingsHandler wires the handler.
//
// languages is required: without it no language could be validated, and a
// settings screen that accepts any string would store codes nothing can
// render. idempotencyTTL is rejected at zero for the reason NewProfileHandler
// gives.
func NewSettingsHandler(
	uow application.UnitOfWork,
	msgs Translator,
	languages LanguageSource,
	idempotencyTTL time.Duration,
) *SettingsHandler {
	if languages == nil {
		panic("handlers: NewSettingsHandler requires a language source")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewSettingsHandler requires a positive idempotency ttl")
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &SettingsHandler{uow: uow, msgs: msgs, languages: languages, idempotencyTTL: idempotencyTTL}
}

// Show handles player.settings. It reads and writes nothing but the player.
func (h *SettingsHandler) Show(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, err
	}

	var lang string
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return h.render(meta, lang, false), nil
}

// SetLanguage handles player.language.set.
//
// The language is checked against the catalogue BEFORE the unit of work
// opens, so a refused code writes nothing at all — not even an idempotency
// reservation. The reply is the settings screen rendered in the NEW language
// and edited over the old one, which is how the player sees the change took.
//
// A redelivered press is recognised by its idempotency key and writes nothing
// the second time. That matters even though the write is "the same value
// again": a stale redelivery of an earlier choice, arriving after a later
// one, would otherwise quietly switch the player back.
func (h *SettingsHandler) SetLanguage(ctx context.Context, meta envelope.Metadata, req LanguageRequest) (*presenter.Response, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, err
	}

	lang := strings.ToLower(strings.TrimSpace(req.Lang))
	if !h.supported(lang) {
		return nil, application.ErrUnsupportedLanguage
	}

	rendered := lang
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}

		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			// Already applied, or overtaken by a later choice: show what is
			// stored now rather than what this old press asked for.
			rendered = RenderLanguage(meta, p)
			return nil
		}
		return tx.Players().SetLanguage(ctx, p.ID, lang)
	})
	if err != nil {
		return nil, err
	}

	return h.render(meta, rendered, true), nil
}

// supported reports whether the catalogue has lang.
func (h *SettingsHandler) supported(lang string) bool {
	if lang == "" {
		return false
	}
	for _, have := range h.languages.Languages() {
		if have == lang {
			return true
		}
	}
	return false
}

// render lays out the settings screen in lang.
//
// A stored language the catalogue does not have — a record stamped from a
// Telegram client in a language the game has never shipped — has no name to
// show, and the screen is really being read in the fallback language. So no
// language is claimed as current and every one is offered, which is the
// accurate thing to say and one press away from fixing.
func (h *SettingsHandler) render(meta envelope.Metadata, lang string, changed bool) *presenter.Response {
	c := screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta)}
	current := lang
	if !h.supported(current) {
		current, changed = "", false
	}
	return screens.Settings(c, screens.SettingsView{
		Language:        current,
		Languages:       h.languages.Languages(),
		LanguageChanged: changed,
	})
}

// validPlayerRequest is the check every player-sent command starts with.
func validPlayerRequest(meta envelope.Metadata) error {
	if err := meta.Validate(); err != nil {
		return errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return errors.InvalidInput("request carries no telegram user")
	}
	return nil
}
