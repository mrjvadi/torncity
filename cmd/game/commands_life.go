package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// Stage G1 (docs/adr/0025-life-and-legacy.md): a character's life.
type lifeHandlers struct {
	life *handlers.LifeHandler
}

// bindLife maps the life commands to their handlers.
func (h phaseHandlers) bindLife() map[string]commandFunc {
	lh := h.stageG1.life
	return map[string]commandFunc{
		"life.me":      bare(lh.Me),
		"life.card":    decoded(lh.Card),
		"life.history": decoded(lh.History),
		"life.bio":     decoded(lh.Bio),
		"life.avatar":  decoded(lh.Avatar),
		"life.sleep":   decoded(lh.Sleep),
		"life.top":     decoded(lh.Top),
		// The scheduler's: a leaderboard period ending.
		"life.refresh": decoded(lh.Refresh),
	}
}
