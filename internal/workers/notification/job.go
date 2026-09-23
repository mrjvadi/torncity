package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// shiftWorked is the payload JobsHandler.FinishShift writes when a shift
// ends. Only the fields the notice needs are read.
type shiftWorked struct {
	PlayerID         string `json:"player_id"`
	Gross            int64  `json:"gross"`
	Tax              int64  `json:"tax"`
	Net              int64  `json:"net"`
	XP               int64  `json:"xp"`
	Performance      int    `json:"performance"`
	PerformanceDelta int    `json:"performance_delta"`
	FatigueBPS       int    `json:"fatigue_bps"`
	Level            int    `json:"level"`
	Energy           int    `json:"energy"`
	MaxEnergy        int    `json:"max_energy"`
	Skills           []struct {
		Skill string `json:"skill"`
		XP    int64  `json:"xp"`
		Level int    `json:"level"`
	} `json:"skills"`
}

// renderShiftWorked tells a player their shift has ended and what it paid.
// A shift ends on the schedule, while the player may be doing anything else,
// which is why it is a notice and not a reply.
func renderShiftWorked(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev shiftWorked
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("job.shift_worked payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("job.shift_worked names no player")
	}
	view := screens.ShiftWorkedView{
		Gross:            ev.Gross,
		Tax:              ev.Tax,
		Net:              ev.Net,
		XP:               ev.XP,
		Performance:      ev.Performance,
		PerformanceDelta: ev.PerformanceDelta,
		FatigueBPS:       ev.FatigueBPS,
		Level:            ev.Level,
		Energy:           ev.Energy,
		MaxEnergy:        ev.MaxEnergy,
	}
	for _, s := range ev.Skills {
		view.Skills = append(view.Skills, screens.SkillGain{Skill: s.Skill, XP: s.XP, Level: s.Level})
	}
	return &Draft{
		PlayerID: ev.PlayerID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.ShiftWorked(c, view) },
	}, nil
}
