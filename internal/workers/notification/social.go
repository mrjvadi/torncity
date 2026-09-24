package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// friendEvent is the payload SocialHandler writes for a request and for an
// acceptance. player_* is the one who acted; friend_id is the one to tell.
type friendEvent struct {
	PlayerID   string `json:"player_id"`
	FriendID   string `json:"friend_id"`
	PlayerName string `json:"player_name"`
	PlayerCode string `json:"player_code"`
}

func readFriendEvent(env *envelope.Envelope, name string) (friendEvent, error) {
	var ev friendEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput(name + " payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.FriendID == "" {
		return ev, apperrors.InvalidInput(name + " names no player or no friend")
	}
	return ev, nil
}

// renderFriendRequested tells the other player about a friend request, with
// the button that accepts it.
func renderFriendRequested(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := readFriendEvent(env, "social.friend_requested")
	if err != nil {
		return nil, err
	}
	return &Draft{
		PlayerID: ev.FriendID,
		Screen: func(c screens.Context) *presenter.Response {
			return screens.FriendRequestNotice(c, ev.PlayerName, ev.PlayerCode, ev.PlayerID)
		},
	}, nil
}

// renderFriendAccepted tells the player who asked that their request was
// accepted.
func renderFriendAccepted(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := readFriendEvent(env, "social.friend_accepted")
	if err != nil {
		return nil, err
	}
	return &Draft{
		PlayerID: ev.FriendID,
		Screen: func(c screens.Context) *presenter.Response {
			return screens.FriendAcceptedNotice(c, ev.PlayerName)
		},
	}, nil
}
