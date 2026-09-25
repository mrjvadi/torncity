package notification

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// operatorAnnounced is the payload `admin announce` writes: the text, in the
// operator's words, optionally per language, and every city whose groups
// read it.
type operatorAnnounced struct {
	Text    string            `json:"text"`
	Texts   map[string]string `json:"texts"`
	CityIDs []string          `json:"city_ids"`
}

// operatorAnnouncement: an operator's words in every linked group
// (docs/adr/0024-property-and-politics.md, admin panel). A group reads the
// text written for its language when there is one, else the main text.
func operatorAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	var ev operatorAnnounced
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("admin.announced payload is unreadable").WithCause(err)
	}
	if strings.TrimSpace(ev.Text) == "" || len(ev.CityIDs) == 0 {
		return nil, nil
	}
	return &Announcement{CityIDs: ev.CityIDs, Line: func(c screens.Context, _ string) string {
		text := ev.Text
		if t := strings.TrimSpace(ev.Texts[c.Lang]); t != "" {
			text = t
		}
		return screens.OperatorAnnouncement(c, text)
	}}, nil
}

// operatorBroadcast is the payload `admin broadcast` writes, one per player.
type operatorBroadcast struct {
	PlayerID string            `json:"player_id"`
	Text     string            `json:"text"`
	Texts    map[string]string `json:"texts"`
}

// renderBroadcast sends an operator's message to one player's private chat,
// in the player's language when a text was written for it.
func renderBroadcast(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev operatorBroadcast
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("admin.broadcast payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || strings.TrimSpace(ev.Text) == "" {
		return nil, apperrors.InvalidInput("admin.broadcast names no player or no text")
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		text := ev.Text
		if t := strings.TrimSpace(ev.Texts[c.Lang]); t != "" {
			text = t
		}
		return presenter.Message(screens.OperatorAnnouncement(c, text), nil)
	}}, nil
}
