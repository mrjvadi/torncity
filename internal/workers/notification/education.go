package notification

import (
	"context"
	"encoding/json"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// courseCompleted is the payload EducationHandler.Complete writes. The course
// is named by its code, which keys its name in the catalogue, and by its
// authored name, the fallback; no id is shown.
type courseCompleted struct {
	PlayerID   string `json:"player_id"`
	Course     string `json:"course"`
	CourseName string `json:"course_name"`
	Certified  bool   `json:"certified"`
	Skills     []struct {
		Skill string `json:"skill"`
		XP    int64  `json:"xp"`
		Level int    `json:"level"`
	} `json:"skills"`
}

// renderCourseCompleted tells a player their course has finished: the
// certificate it issued, and the skills it trained. A course finishes on the
// schedule, while the player may be doing anything else, which is why it is a
// notice and not a reply.
func renderCourseCompleted(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev courseCompleted
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("education.completed payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Course == "" {
		return nil, apperrors.InvalidInput("education.completed names no player or no course")
	}
	view := plife.CourseCompletedView{
		Course:    plife.CourseRef{Code: ev.Course, Name: ev.CourseName},
		Certified: ev.Certified,
	}
	for _, s := range ev.Skills {
		view.Skills = append(view.Skills, plife.SkillGain{Skill: s.Skill, XP: s.XP, Level: s.Level})
	}
	return &Draft{
		PlayerID: ev.PlayerID,
		Notice:   func(c presentation.Ctx) *presentation.Response { return plife.CourseCompleted(c, view) },
	}, nil
}
