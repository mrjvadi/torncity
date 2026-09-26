package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// A character's life (docs/adr/0025-life-and-legacy.md): a player hears,
// privately, that their rank by net worth rose or fell.

// renderRankChanged tells a player their rank moved.
func renderRankChanged(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev struct {
		PlayerID  string `json:"player_id"`
		Rank      string `json:"rank"`
		RankName  string `json:"rank_name"`
		RankEmoji string `json:"rank_emoji"`
		From      string `json:"from"`
		FromName  string `json:"from_name"`
		Up        bool   `json:"up"`
		NetWorth  int64  `json:"net_worth"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("life.rank_changed payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Rank == "" {
		return nil, apperrors.InvalidInput("life.rank_changed names nobody")
	}
	view := screens.RankNoticeView{Rank: screens.RankRef{Code: ev.Rank, Name: ev.RankName, Emoji: ev.RankEmoji},
		From: screens.RankRef{Code: ev.From, Name: ev.FromName}, Up: ev.Up, Worth: ev.NetWorth}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.RankNotice(c, view)
	}}, nil
}

// renderHungerLow tells a player they are hungry, the one urgent need alert
// this feature adds (life_common.go's alertHunger). It is content-driven
// only in WHEN it fires (life.yml needs.high, notifications.hunger_alert_
// cooldown); the wording itself is fixed, warm Persian and English text
// rather than a literal translation of "you are hungry, go eat" — see
// configs/locales/*.yml life.hunger_low.
func renderHungerLow(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev struct {
		PlayerID string `json:"player_id"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("life.hunger_low payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("life.hunger_low names nobody")
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.HungerNotice(c)
	}}, nil
}
