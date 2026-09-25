package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Achievements (docs/adr/0024-property-and-politics.md): a player hears,
// privately, that they earned one and what it paid.

// renderAchievement tells a player they earned an achievement.
func renderAchievement(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev struct {
		PlayerID string `json:"player_id"`
		Code     string `json:"code"`
		Name     string `json:"name"`
		Cash     int64  `json:"cash"`
		Withheld int64  `json:"withheld"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("achievement.awarded payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Code == "" {
		return nil, apperrors.InvalidInput("achievement.awarded names nobody")
	}
	view := screens.AchievementNoticeView{Achievement: screens.Named{Code: ev.Code, Name: ev.Name}, Cash: ev.Cash,
		Withheld: ev.Withheld}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.AchievementNotice(c, view)
	}}, nil
}
