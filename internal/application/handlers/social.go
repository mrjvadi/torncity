package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
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

// playerActive is the stored status of an account in good standing
// (players_status_check). Only such an account is ever a search result.
const playerActive = "active"

// SearchRequest is the payload of social.search.
//
// Query is everything the player typed after the command, as one string
// (internal/gateway/routing joins the words). It is untrusted: the gateway
// only spells, and ClassifyPlayerQuery decides what it means.
type SearchRequest struct {
	Query string `json:"query"`
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
	uow    application.UnitOfWork
	ids    IDGenerator
	msgs   Translator
	search application.PlayerSearch

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewSocialHandler wires the handler.
//
// pageSize is rejected at zero: a page of no rows is an empty screen with a
// next button, which is a list a player can never read to the end of.
//
// Friendships are not a constructor argument: the edges are written alongside
// an idempotency reservation and an outbox record, so they are reached through
// the unit of work's Tx. search reads other players' public records and stays
// injected; see application.Tx.
func NewSocialHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	search application.PlayerSearch,
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
		pageSize:       pageSize,
		idempotencyTTL: idempotencyTTL,
		now:            now,
	}
}

// screen builds the rendering context for a reply to the player who sent
// meta, in lang (see RenderLanguage).
func (h *SocialHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta)}
}

// Search handles social.search: finding one player by an exact identifier.
//
// The query is classified first (ClassifyPlayerQuery). One that is none of
// the three forms is not searched at all — there is no display-name fallback,
// because a name is not unique and a fuzzy match answers "who is Ali?" with a
// list of strangers — and the player is shown the three forms instead. An
// empty query, which is what a bare /find sends, gets the same answer.
//
// The player found is shown by display name and public code only. The
// Telegram id and the record id never reach the screen, even when the search
// was made with the Telegram id. Finding yourself says so and offers no
// add-friend button.
func (h *SocialHandler) Search(ctx context.Context, meta envelope.Metadata, req SearchRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	query, ok := ClassifyPlayerQuery(req.Query)

	var view screens.SearchView
	lang := meta.Language

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, self)

		if !ok {
			view = screens.SearchView{Help: true}
			return nil
		}
		view = searchView(query)

		found, err := h.search.Find(ctx, query)
		if isSentinel(err, application.ErrPlayerNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if found.Status != playerActive {
			// The port promises active players only. Checked again here
			// because the cost of trusting it wrongly — confirming a ban to
			// anyone who asks — is paid by a player, not by a test.
			return nil
		}

		view.Found = &screens.SearchResult{
			ID:   found.ID,
			Name: shownName(found),
			Code: found.PublicCode,
			Self: found.ID == self.ID,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.Search(h.screen(meta, lang), view), nil
}

// searchView starts the view for a classified query: which form it took and
// what of it may be echoed back. A Telegram id is never echoed.
func searchView(q application.PlayerQuery) screens.SearchView {
	switch q.Kind {
	case application.PlayerQueryUsername:
		return screens.SearchView{By: screens.SearchByUsername, Query: "@" + q.Username}
	case application.PlayerQueryTelegramUserID:
		return screens.SearchView{By: screens.SearchByTelegramID}
	case application.PlayerQueryPublicCode:
		return screens.SearchView{By: screens.SearchByCode, Query: q.PublicCode}
	}
	return screens.SearchView{Help: true}
}

// shownName is the display name a search result may show. The placeholder a
// record gets when no real name was on hand is derived from the Telegram
// account number (fallbackDisplayName), so showing it would print the
// Telegram id dressed up as a name; the screen says "a player" instead.
func shownName(p *application.Player) string {
	if p.DisplayName == fallbackDisplayName(p.TelegramUserID) {
		return ""
	}
	return p.DisplayName
}

// ClassifyPlayerQuery decides which of the three identifiers a search query
// is, and normalises it. It is pure: no I/O, no clock, the same answer for
// the same string.
//
//   - "@name"    -> a Telegram username, lower-cased, without the @. The @ is
//     required: it is what tells a username from a code, since a seven-letter
//     username such as "mrjvadi" could otherwise be either.
//   - all digits -> a Telegram user id. Persian (۰-۹) and Arabic-Indic (٠-٩)
//     digits count, because that is what a Persian keyboard types.
//   - a code     -> a public player code, upper-cased: seven characters from
//     playercode.Alphabet, in either case.
//   - anything else, including an empty query and anything with a space in
//     it, is none of them, and ok is false.
//
// # Why digits always mean a Telegram id
//
// A seven-digit number is the one input that could look like both an id and
// a code. It is settled at the source rather than by precedence: a code is
// never all digits (playercode refuses to issue one, and the schema's CHECK
// refuses to store one), so an all-digit query cannot be a code and there is
// nothing to decide.
//
// # Why this lives here and not in the gateway
//
// internal/gateway/routing only spells commands: it joins the words after
// /social into one query and knows nothing about what a code looks like.
// Deciding that is game knowledge — the code alphabet is the game's — and the
// payload arrives from outside, so the core has to judge it anyway. Doing it
// once, here, means there is one classifier and it sits next to the code that
// trusts its answer.
func ClassifyPlayerQuery(raw string) (application.PlayerQuery, bool) {
	q := asciiDigits(strings.TrimSpace(raw))
	if q == "" || strings.ContainsFunc(q, unicode.IsSpace) {
		return application.PlayerQuery{}, false
	}

	switch {
	case strings.HasPrefix(q, "@"):
		name := q[1:]
		if !telegramUsername(name) {
			return application.PlayerQuery{}, false
		}
		return application.PlayerQuery{
			Kind:     application.PlayerQueryUsername,
			Username: strings.ToLower(name),
		}, true

	case allDigits(q):
		id, err := strconv.ParseInt(q, 10, 64)
		if err != nil || id <= 0 {
			// Too long to be an id, or zero: no account has it.
			return application.PlayerQuery{}, false
		}
		return application.PlayerQuery{
			Kind:           application.PlayerQueryTelegramUserID,
			TelegramUserID: id,
		}, true
	}

	if code := playercode.Normalize(q); playercode.Valid(code) {
		return application.PlayerQuery{
			Kind:       application.PlayerQueryPublicCode,
			PublicCode: code,
		}, true
	}
	return application.PlayerQuery{}, false
}

// telegramUsername reports whether s can be a Telegram username: 4 to 32
// characters of ASCII letters, digits and underscores, starting with a
// letter. (Telegram asks 5 of a new username; 4-character ones exist as
// collectibles.) Anything else cannot be one, so it is not searched.
func telegramUsername(s string) bool {
	if len(s) < 4 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_'):
		default:
			return false
		}
	}
	return true
}

// allDigits reports whether s is non-empty and made of ASCII digits only.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// asciiDigits rewrites Persian and Arabic-Indic digits as ASCII ones and
// leaves every other character alone.
func asciiDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		}
		return r
	}, s)
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
	lang := meta.Language
	var name string

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, self)
		if self.ID == req.Player {
			return errors.InvalidInput("a player cannot befriend themselves")
		}
		if name, err = h.nameOf(ctx, tx, req.Player); err != nil {
			return err
		}

		key := idempotency.Derive(self.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), self.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}

		edges, err := tx.Friendships().List(ctx, self.ID)
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

		if err := tx.Friendships().Request(ctx, self.ID, req.Player); err != nil {
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

	// The other player is named by their display name; one with no name
	// worth showing is "a player", never an identifier.
	return screens.FriendRequested(h.screen(meta, lang), name), nil
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
	lang := meta.Language
	var name string

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, self)
		if self.ID == req.Player {
			return errors.InvalidInput("a player cannot befriend themselves")
		}
		if name, err = h.nameOf(ctx, tx, req.Player); err != nil {
			return err
		}

		key := idempotency.Derive(self.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), self.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}

		if err := tx.Friendships().Accept(ctx, self.ID, req.Player); err != nil {
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

	return screens.FriendAccepted(h.screen(meta, lang), name), nil
}

// nameOf is the display name of another player, or "" when they have none
// worth showing or no longer exist: the screen then says "a player", never
// an identifier.
func (h *SocialHandler) nameOf(ctx context.Context, tx application.Tx, playerID string) (string, error) {
	p, err := tx.Players().GetByID(ctx, playerID)
	if isSentinel(err, application.ErrPlayerNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return shownName(p), nil
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
	lang := meta.Language

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, self)

		edges, err := tx.Friendships().List(ctx, self.ID)
		if err != nil {
			return err
		}

		start, end, pages := pageWindow(len(edges), page, h.pageSize)
		lines := make([]screens.FriendLine, 0, end-start)
		for _, e := range edges[start:end] {
			name, err := h.nameOf(ctx, tx, e.FriendPlayerID)
			if err != nil {
				return err
			}
			lines = append(lines, screens.FriendLine{
				ID:     e.FriendPlayerID,
				Name:   name,
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

	return screens.Friends(h.screen(meta, lang), view), nil
}
