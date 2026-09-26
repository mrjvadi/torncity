package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// InboxHandler serves /inbox (migrations/0037_notification_inbox): what
// cmd/notifier stored for a player instead of flooding them with separate
// messages (internal/workers/notification/badge.go). Opening it — inbox.show
// — marks every item read and clears the badge in the same transaction, so
// there is no window where the count shown here and the count on the badge
// disagree; a category's own list (inbox.category) shows the player's whole
// history there, read or not, newest first.
type InboxHandler struct {
	uow   application.UnitOfWork
	msgs  Translator
	rules InboxRules
	now   func() time.Time
}

// InboxRules is /inbox's tuning (config notifications.*).
type InboxRules struct {
	// PageSize is how many items one category page shows.
	PageSize int
}

// NewInboxHandler wires the handler. There is no idempotency TTL and no id
// generator: nothing here writes a new entity, only marks existing rows read
// and clears a count, both safe to repeat.
func NewInboxHandler(uow application.UnitOfWork, msgs Translator, rules InboxRules, now func() time.Time) *InboxHandler {
	if rules.PageSize <= 0 {
		rules.PageSize = 5
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &InboxHandler{uow: uow, msgs: msgs, rules: rules, now: now}
}

// Show handles inbox.show: the hub, grouped by category. Opening it is what
// "read" means here, so it marks everything read and clears the badge
// before rendering — the counts shown are exactly what just arrived, and
// pressing "open" a second time (or refreshing) always shows a settled inbox
// with nothing left unread.
func (h *InboxHandler) Show(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	var view screens.InboxHubView
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		before, err := tx.Notifications().Summary(ctx, p.ID, 0)
		if err != nil {
			return err
		}
		if _, err := tx.Notifications().MarkAllRead(ctx, p.ID); err != nil {
			return err
		}
		if err := tx.Notifications().ClearBadge(ctx, p.ID); err != nil {
			return err
		}

		view.Total = before.Unread
		for _, c := range before.Categories {
			view.Categories = append(view.Categories, screens.InboxHubCategory{Category: c.Category, Count: c.Count})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.InboxHub(screens.Context{Msgs: h.msgs, Lang: lang, MessageID: meta.TelegramMessageID}, view), nil
}

// ReadAll handles inbox.read_all, the hub's own "mark all read" button. It
// is Show under another name: opening the hub already marks everything
// read, so pressing the button again only catches up anything that arrived
// in between.
func (h *InboxHandler) ReadAll(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.Show(ctx, meta)
}

// InboxCategoryRequest is inbox.category's payload: which category, which
// page. Page arrives as a string, like every positional callback argument
// (parsePage): callback data is a string, and a stale or tampered one reads
// as page 1 rather than failing.
type InboxCategoryRequest struct {
	Category string `json:"category"`
	Page     string `json:"page,omitempty"`
}

// Category handles inbox.category: one category's compact, paginated list.
func (h *InboxHandler) Category(ctx context.Context, meta envelope.Metadata, req InboxCategoryRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if req.Category == "" {
		return nil, errors.InvalidInput("inbox.category names no category")
	}
	page := parsePage(req.Page)

	var view screens.InboxCategoryView
	lang := meta.Language
	now := h.now()
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		items, pages, err := tx.Notifications().List(ctx, p.ID, req.Category, page, h.rules.PageSize)
		if err != nil {
			return err
		}
		view.Category, view.Page, view.TotalPages = req.Category, page, pages
		for _, it := range items {
			view.Items = append(view.Items, screens.InboxItemLine{
				Text: it.Text(lang), Ago: now.Sub(it.CreatedAt), LinkAddr: it.LinkAddr,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.InboxCategory(screens.Context{Msgs: h.msgs, Lang: lang, MessageID: meta.TelegramMessageID}, view), nil
}
