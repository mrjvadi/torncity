package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/faction"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/society"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
)

// FriendActRequest is the payload of social.friend.view and
// social.friend.remove. Confirm is "yes" on the second step of a removal.
type FriendActRequest struct {
	Player  string `json:"player"`
	Confirm string `json:"confirm,omitempty"`
}

// WithFactions gives the handler the content that says which faction rank may
// invite, so a friend's screen can offer "invite to my faction" only to
// someone holding that right. Without it the offer is never made.
func (h *SocialHandler) WithFactions(src ContentSource) *SocialHandler {
	h.content = src
	return h
}

// acceptedFriend loads the edge to a friend and refuses anyone who is not an
// accepted friend of this player.
func (h *SocialHandler) acceptedFriend(ctx context.Context, tx application.Tx, selfID, other string) error {
	edges, err := tx.Friendships().List(ctx, selfID)
	if err != nil {
		return err
	}
	for _, e := range edges {
		if e.FriendPlayerID == other && !e.Incoming && e.Status == friendAccepted {
			return nil
		}
	}
	return application.ErrNotFriends
}

// FriendView handles social.friend.view: a friend's profile card with the
// actions that go with it (pay, invite, remove). The viewer may invite only
// when their own faction rank holds the invite right and the friend is in no
// faction; the invite itself is faction.invite, which checks all of it again.
func (h *SocialHandler) FriendView(ctx context.Context, meta envelope.Metadata, req FriendActRequest) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	if req.Player == "" {
		return nil, errors.InvalidInput("social.friend.view names no player")
	}
	lang := meta.Language
	var view society.FriendDetailView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, self)
		if err := h.acceptedFriend(ctx, tx, self.ID, req.Player); err != nil {
			return err
		}
		other, err := tx.Players().GetByID(ctx, req.Player)
		if err != nil {
			return err
		}
		view = society.FriendDetailView{ID: other.ID, Name: shownName(other), Code: other.PublicCode}
		if m, err := tx.Factions().Membership(ctx, other.ID); err == nil {
			if f, err := tx.Factions().ByID(ctx, m.FactionID); err == nil {
				view.Faction = f.Name
			}
		} else if !isSentinel(err, application.ErrNotInFaction) {
			return err
		}
		if view.Faction == "" && h.content != nil {
			mine, err := tx.Factions().Membership(ctx, self.ID)
			switch {
			case err == nil:
				if def, ok := h.content.Current().Faction(); ok {
					view.CanInvite = def.Charter().Can(faction.Rank(mine.Rank), faction.Invite)
				}
			case !isSentinel(err, application.ErrNotInFaction):
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return society.FriendDetail(presentation.Ctx{Lang: lang}, view), nil
}

// FriendRemove handles social.friend.remove: the first call asks, the second
// (confirm "yes") removes the player's own side of the friendship. Removing
// again is not an error: it reports the same result and writes nothing.
func (h *SocialHandler) FriendRemove(ctx context.Context, meta envelope.Metadata, req FriendActRequest) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	if req.Player == "" {
		return nil, errors.InvalidInput("social.friend.remove names no player")
	}
	lang := meta.Language
	var (
		name    string
		removed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		self, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, self)
		if name, err = h.nameOf(ctx, tx, req.Player); err != nil {
			return err
		}
		if req.Confirm != "yes" {
			return h.acceptedFriend(ctx, tx, self.ID, req.Player)
		}
		key := idempotency.Derive(self.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), self.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		removed = true
		if !fresh {
			return nil
		}
		if err := tx.Friendships().Remove(ctx, self.ID, req.Player); err != nil && !isSentinel(err, application.ErrNotFriends) {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !removed {
		return society.FriendRemoveAsk(presentation.Ctx{Lang: lang}, society.FriendRemoveAskView{ID: req.Player, Name: name}), nil
	}
	return society.FriendRemoved(presentation.Ctx{Lang: lang}, society.FriendRemovedView{Name: name}), nil
}
