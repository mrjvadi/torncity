package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/notices"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
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
	view := notices.AchievementView{Achievement: notices.Named{Code: ev.Code, Name: ev.Name}, Cash: ev.Cash,
		Withheld: ev.Withheld}
	return &Draft{PlayerID: ev.PlayerID, Notice: func(c presentation.Ctx) *presentation.Response {
		return notices.AchievementNotice(c, view)
	}}, nil
}
