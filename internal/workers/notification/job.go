package notification

import (
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation"
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
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
	Injury *injuryPayload `json:"injury"`
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
	view := plife.ShiftWorkedView{
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
		Injury:           ev.Injury.view(),
	}
	for _, s := range ev.Skills {
		view.Skills = append(view.Skills, plife.SkillGain{Skill: s.Skill, XP: s.XP, Level: s.Level})
	}
	return &Draft{
		PlayerID: ev.PlayerID,
		Notice:   func(c presentation.Ctx) *presentation.Response { return plife.ShiftWorked(c, view) },
	}, nil
}
