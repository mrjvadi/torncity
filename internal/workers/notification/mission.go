package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// missionCompleted is the payload of mission.completed
// (docs/adr/0023-health-missions-factions.md).
type missionCompleted struct {
	PlayerID    string `json:"player_id"`
	Mission     string `json:"mission"`
	MissionName string `json:"mission_name"`
	Cash        int64  `json:"cash"`
	Withheld    int64  `json:"withheld"`
	XP          int64  `json:"xp"`
	Items       []struct {
		Item     string `json:"item"`
		ItemName string `json:"item_name"`
		Qty      int64  `json:"qty"`
	} `json:"items"`
}

// renderMissionCompleted tells a player a mission is complete, and what it
// paid — and what the day's caps kept back.
func renderMissionCompleted(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev missionCompleted
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("mission.completed payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Mission == "" {
		return nil, apperrors.InvalidInput("mission.completed names no player or no mission")
	}
	view := screens.MissionCompletedView{Mission: screens.Named{Code: ev.Mission, Name: ev.MissionName},
		Cash: ev.Cash, Withheld: ev.Withheld, XP: ev.XP}
	for _, it := range ev.Items {
		view.Items = append(view.Items, screens.LootLine{Item: screens.Named{Code: it.Item, Name: it.ItemName}, Qty: it.Qty})
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.MissionCompletedNotice(c, view)
	}}, nil
}
