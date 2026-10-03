package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/notices"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// InboxHandler serves /inbox (migrations/0037_notification_inbox): what
// cmd/notifier stored for a player instead of flooding them with separate
// messages (internal/workers/notification/badge.go). The hub (inbox.show)
// lists what is unread per category and changes nothing; a category's own
// list (inbox.category) shows the player's whole history there, read or not,
// newest first; opening one notice (inbox.read) marks only that one read;
// "read all" (inbox.read_all) is the one explicit clear-everything action.
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

// Show handles inbox.show: the hub, grouped by category, with what is still
// unread. Looking at the hub changes nothing: a notice is read when the
// player opens it (Read) or presses "read all" (ReadAll), never because the
// hub, a category or a refresh was shown. (Marking everything read on show
// emptied the hub the moment the player stepped back from one item, so the
// next one could not be opened.)
func (h *InboxHandler) Show(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.hub(ctx, meta, false)
}

// ReadAll handles inbox.read_all, the hub's own "mark all read" button: the
// one place everything is marked read at once, and the badge cleared with it.
func (h *InboxHandler) ReadAll(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.hub(ctx, meta, true)
}

func (h *InboxHandler) hub(ctx context.Context, meta envelope.Metadata, markAll bool) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	var view notices.InboxHubView
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
		if markAll {
			if _, err := tx.Notifications().MarkAllRead(ctx, p.ID); err != nil {
				return err
			}
			if err := tx.Notifications().ClearBadge(ctx, p.ID); err != nil {
				return err
			}
		}
		view.Total = before.Unread
		for _, c := range before.Categories {
			view.Categories = append(view.Categories, notices.InboxHubCategory{Category: c.Category, Count: c.Count})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return notices.InboxHub(presentation.Ctx{Lang: lang}, view), nil
}

// InboxReadRequest is inbox.read's payload: which notice, and the page of
// its category to come back to.
type InboxReadRequest struct {
	ID   string `json:"id"`
	Page string `json:"page,omitempty"`
}

// Read handles inbox.read: the player opened one notice. Only that notice is
// marked read (idempotent: reading it again changes nothing), the badge is
// set to what is still unread, and the notice's category list comes back
// with every other notice where it was, so the next one opens normally.
func (h *InboxHandler) Read(ctx context.Context, meta envelope.Metadata, req InboxReadRequest) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	if req.ID == "" {
		return nil, errors.InvalidInput("inbox.read names no notice")
	}
	var category string
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		item, err := tx.Notifications().Get(ctx, p.ID, req.ID)
		if err != nil {
			return err
		}
		if item == nil {
			return errors.NotFound("no such notice")
		}
		category = item.Category
		changed, err := tx.Notifications().MarkRead(ctx, p.ID, req.ID)
		if err != nil || !changed {
			return err
		}
		left, err := tx.Notifications().Summary(ctx, p.ID, 0)
		if err != nil {
			return err
		}
		if left.Unread == 0 {
			return tx.Notifications().ClearBadge(ctx, p.ID)
		}
		return tx.Notifications().SetBadgeUnread(ctx, p.ID, left.Unread)
	})
	if err != nil {
		return nil, err
	}
	return h.Category(ctx, meta, InboxCategoryRequest{Category: category, Page: req.Page})
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
func (h *InboxHandler) Category(ctx context.Context, meta envelope.Metadata, req InboxCategoryRequest) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if req.Category == "" {
		return nil, errors.InvalidInput("inbox.category names no category")
	}
	page := parsePage(req.Page)

	var view notices.InboxCategoryView
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
			view.Items = append(view.Items, notices.InboxItemLine{
				ID: it.ID, Read: it.ReadAt != nil, Kind: it.Kind, Notice: storedNotice(it, lang), Ago: now.Sub(it.CreatedAt),
				Link: presentation.RefOfAddress(it.LinkAddr),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return notices.InboxCategory(presentation.Ctx{Lang: lang}, view), nil
}

// storedNotice is an inbox item as the screens carry it: the notice as data
// when it was stored as data, else the text its producer wrote in the
// reader's language.
func storedNotice(it application.NotificationItem, lang string) notices.StoredNotice {
	if it.Screen != "" && len(it.View) > 0 {
		return notices.StoredNotice{Screen: it.Screen, View: it.View}
	}
	return notices.StoredNotice{Text: it.Text(lang)}
}
