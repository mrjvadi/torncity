package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// The console's changes. The wrapper (mutation) has already required the
// session, the CSRF token, the idempotency key and the reason; each change
// checks its own fields before anything is tried. A destructive change also
// requires the thing it acts on to be typed again (confirm), so a slip of
// the hand in a dialog acts on nothing.

// maxModerationHours bounds a timed moderation: longer is "until lifted".
const maxModerationHours = 24 * 365

func (s *Server) moderate(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	code, err := pathPlayer(r)
	if err != nil {
		return 0, nil, err
	}
	var m struct {
		Kind    string `json:"kind"`
		Hours   int    `json:"hours"`
		Confirm string `json:"confirm"`
	}
	if err := strict(body, &m); err != nil {
		return 0, nil, err
	}
	switch {
	case m.Kind != postgres.ModerationMute && m.Kind != postgres.ModerationBan:
		return 0, nil, bad("a moderation is a mute or a ban")
	case m.Hours < 0 || m.Hours > maxModerationHours:
		return 0, nil, bad("hours is from 0 (until lifted) to %d", maxModerationHours)
	case m.Kind == postgres.ModerationBan && !strings.EqualFold(strings.TrimSpace(m.Confirm), code):
		return 0, nil, bad("type the player's code to confirm a ban")
	}
	out, err := s.console.Moderate(ctx, code, m.Kind, time.Duration(m.Hours)*time.Hour, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, out, nil
}

func (s *Server) liftModeration(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	code, err := pathPlayer(r)
	if err != nil {
		return 0, nil, err
	}
	var m struct {
		Kind string `json:"kind"`
	}
	if err := strict(body, &m); err != nil {
		return 0, nil, err
	}
	if m.Kind != postgres.ModerationMute && m.Kind != postgres.ModerationBan {
		return 0, nil, bad("a moderation is a mute or a ban")
	}
	out, err := s.console.LiftModeration(ctx, code, m.Kind, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, out, nil
}

func (s *Server) release(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	return s.endNow(ctx, r, body, a, false)
}

func (s *Server) discharge(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	return s.endNow(ctx, r, body, a, true)
}

func (s *Server) endNow(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor, hospital bool) (int, any, error) {
	code, err := pathPlayer(r)
	if err != nil {
		return 0, nil, err
	}
	if err := strict(body, &struct{}{}); err != nil {
		return 0, nil, err
	}
	var out postgres.Ended
	if hospital {
		out, err = s.console.Discharge(ctx, code, a)
	} else {
		out, err = s.console.Release(ctx, code, a)
	}
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, out, nil
}

func (s *Server) requeue(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if !uuidText.MatchString(id) {
		return 0, nil, bad("that is not an action's id")
	}
	if err := strict(body, &struct{}{}); err != nil {
		return 0, nil, err
	}
	out, err := s.console.Requeue(ctx, id, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, out, nil
}

func (s *Server) dissolve(ctx context.Context, r *http.Request, body json.RawMessage, a operator.Actor) (int, any, error) {
	code, err := pathCompany(r)
	if err != nil {
		return 0, nil, err
	}
	var c struct {
		Confirm string `json:"confirm"`
	}
	if err := strict(body, &c); err != nil {
		return 0, nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(c.Confirm), code) {
		return 0, nil, bad("type the company's code to confirm")
	}
	out, err := s.console.Dissolve(ctx, code, a)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, out, nil
}
