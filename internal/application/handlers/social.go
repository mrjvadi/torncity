package handlers

import (
	"context"
	"strings"
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

// Friendship statuses as they are stored. An edge is directed, so these
// describe one side of it: see FriendshipRepository.
const (
	friendPending  = "pending"
	friendAccepted = "accepted"
	friendBlocked  = "blocked"
)

// SearchRequest is the payload of social.search.
type SearchRequest struct {
	Query string `json:"query"`
	Page  string `json:"page,omitempty"`
}

// FriendRequest is the payload of social.friend.add and
// social.friend.accept.
//
// Player is the other player's identifier as it came off a button. It is an
// address and grants nothing: sending a request is not becoming a friend, and
// the repository re-checks the edge either way.
type FriendRequest struct {
	Player string `json:"player"`
}

// PageRequest is the payload of a screen whose only argument is which page to
// show.
type PageRequest struct {
	Page string `json:"page,omitempty"`
}

// SocialHandler serves the social graph: finding players, asking to be
// friends, accepting, and listing.
type SocialHandler struct {
	uow         application.UnitOfWork
	ids         IDGenerator
	msgs        Translator
	search      application.PlayerSearch
	friendships application.FriendshipRepository

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewSocialHandler wires the handler.
//
// pageSize is rejected at zero: a page of no rows is an empty screen with a
// next button, which is a list a player can never read to the end of.
func NewSocialHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	search application.PlayerSearch,
	friendships application.FriendshipRepository,
	pageSize int,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *SocialHandler {
	if pageSize <= 0 {
		panic("handlers: NewSocialHandler requires a positive page size")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewSocialHandler requires a positive idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &SocialHandler{
		uow:            uow,
		ids:            ids,
		msgs:           msgs,
		search:         search,
		friendships:    friendships,
		pageSize:       pageSize,
		idempotencyTTL: idempotencyTTL,
		now:            now,
	}
}

func (h *SocialHandler) screen(meta envelope.Metadata) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: meta.Language, MessageID: editableMessageID(meta)}
}

// Search handles social.search.
//
// # How it knows there is another page
//
// PlayerSearch takes a limit and an offset and returns rows; it does not
// report a total, and asking for one would mean a second count query on every
// keystroke of a search screen. So this asks for ONE row more than the page
// holds: if it comes back, a next page exists, and the extra row is dropped
// before rendering. The page count is therefore "at least this many", which
// is exactly what a next button needs to know and all a player can act on.
func (h *SocialHandler) Search(ctx context.Context, meta envelope.Metadata, req SearchRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, errors.InvalidInput("social.search requires a query")
	}
	page := parsePage(req.Page)

	var view screens.SearchView

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}

		offset := (page - 1) * h.pageSize
		found, err := h.search.Search(ctx, query, h.pageSize+1, offset)
		if err != nil {
			return err
		}

		hasNext := len(found) > h.pageSize
		if hasNext {
			found = found[:h.pageSize]
		}

		results := make([]screens.SearchResult, 0, len(found))
		for _, p := range found {
			if p.ID == self.ID {
				// Offering to befriend yourself is not a feature.
				continue
			}
			results = append(results, screens.SearchResult{ID: p.ID, Name: p.DisplayName})
		}

		pages := page
		if hasNext {
			pages = page + 1
		}
		view = screens.SearchView{Query: query, Results: results, Page: page, Pages: pages}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.Search(h.screen(meta), view), nil
}

// FriendAdd handles social.friend.add: asking another player to be friends.
//
// An edge is directed, so this writes the requesting side only. The other
// player accepts, which is what FriendAccept is for; nobody acquires a friend
// without agreeing to it.
func (h *SocialHandler) FriendAdd(ctx context.Context, meta envelope.Metadata, req FriendRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	if req.Player == "" {
		return nil, errors.InvalidInput("social.friend.add names no player")
	}

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		if self.ID == req.Player {
			return errors.InvalidInput("a player cannot befriend themselves")
		}

		key := idempotency.Derive(self.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), self.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}

		edges, err := h.friendships.List(ctx, self.ID)
		if err != nil {
			return err
		}
		for _, e := range edges {
			if e.FriendPlayerID != req.Player {
				continue
			}
			switch e.Status {
			case friendAccepted:
				return application.ErrAlreadyFriends
			case friendBlocked:
				return errors.Unauthorized("this player cannot be added")
			case friendPending:
				// The request is already out. Asking again is not an error
				// and writes nothing; the player gets the same answer they
				// got the first time.
				return nil
			}
		}

		if err := h.friendships.Request(ctx, self.ID, req.Player); err != nil {
			return err
		}

		ev, err := events.New("social.friend.requested", "player", self.ID, map[string]any{
			"player_id": self.ID,
			"friend_id": req.Player,
		})
		if err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, application.OutboxRecord{
			EventID:  ev.ID,
			Subject:  subjects.Event("social", "friend_requested"),
			Metadata: meta,
			Payload:  ev.Payload,
		})
	})
	if err != nil {
		return nil, err
	}

	// The other player's NAME is not resolvable here: no port maps a player
	// id back to a record. The screen says so in the player's language
	// rather than printing an identifier at them.
	return screens.FriendRequested(h.screen(meta), ""), nil
}

// FriendAccept handles social.friend.accept.
func (h *SocialHandler) FriendAccept(ctx context.Context, meta envelope.Metadata, req FriendRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	if req.Player == "" {
		return nil, errors.InvalidInput("social.friend.accept names no player")
	}

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		if self.ID == req.Player {
			return errors.InvalidInput("a player cannot befriend themselves")
		}

		key := idempotency.Derive(self.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), self.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}

		if err := h.friendships.Accept(ctx, self.ID, req.Player); err != nil {
			return err
		}

		ev, err := events.New("social.friend.accepted", "player", self.ID, map[string]any{
			"player_id": self.ID,
			"friend_id": req.Player,
		})
		if err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, application.OutboxRecord{
			EventID:  ev.ID,
			Subject:  subjects.Event("social", "friend_accepted"),
			Metadata: meta,
			Payload:  ev.Payload,
		})
	})
	if err != nil {
		return nil, err
	}

	return screens.FriendAccepted(h.screen(meta), ""), nil
}

// FriendList handles social.friend.list.
//
// The whole list is read and then paged in memory. That is the right trade
// while a friend list is the size a person can maintain; when it is not, the
// fix is a limit and an offset on the port, not a bigger message.
func (h *SocialHandler) FriendList(ctx context.Context, meta envelope.Metadata, req PageRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	page := parsePage(req.Page)

	var view screens.FriendsView

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}

		edges, err := h.friendships.List(ctx, self.ID)
		if err != nil {
			return err
		}

		start, end, pages := pageWindow(len(edges), page, h.pageSize)
		lines := make([]screens.FriendLine, 0, end-start)
		for _, e := range edges[start:end] {
			lines = append(lines, screens.FriendLine{
				ID:     e.FriendPlayerID,
				Status: e.Status,
				// A pending edge gets the accept button. The port gives one
				// direction of the graph, so it cannot say whether this
				// request was sent or received; the repository re-checks and
				// answers ErrNotFriends when it was ours to send.
				Incoming: e.Status == friendPending,
			})
		}
		view = screens.FriendsView{Friends: lines, Page: page, Pages: pages}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.Friends(h.screen(meta), view), nil
}
