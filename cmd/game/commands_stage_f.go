package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// Stage F (docs/adr/0024-property-and-politics.md): votes of a body, a
// city's budget and period, allocation levers.
type stageFHandlers struct {
	legislature *handlers.LegislatureHandler
	city        *handlers.CityHandler
	property    *handlers.PropertyHandler
	achievement *handlers.AchievementsHandler
}

// bindStageF maps stage F's commands to their handlers.
func (h phaseHandlers) bindStageF() map[string]commandFunc {
	lh, ch, gh, ph := h.stageF.legislature, h.stageF.city, h.gov, h.stageF.property
	return map[string]commandFunc{
		"gov.alloc":    decoded(gh.Alloc),
		"gov.allocok":  decoded(gh.AllocConfirm),
		"gov.allocset": decoded(gh.AllocSet),

		"law.list": bare(lh.List),
		"law.view": decoded(lh.View),
		"law.vote": decoded(lh.Vote),
		// The scheduler's: a proposal's vote window ending.
		"law.close": decoded(lh.Close),

		"city.budget": decoded(ch.Budget),
		// The scheduler's: a city period ending.
		"city.settle": decoded(ch.Settle),

		"property.list":     bare(ph.Market),
		"property.type":     decoded(ph.Type),
		"property.purchase": decoded(ph.Purchase),
		"property.mine":     bare(ph.Mine),
		"property.view":     decoded(ph.View),
		"property.sell":     decoded(ph.Sell),
		"property.let":      decoded(ph.Let),
		"property.cancel":   decoded(ph.Cancel),
		"property.offer":    decoded(ph.Offer),
		"property.buy":      decoded(ph.Buy),
		"property.rent":     decoded(ph.Rent),
		"property.leave":    decoded(ph.Leave),
		"property.rest":     bare(ph.Rest),

		"achievement.list": bare(h.stageF.achievement.List),
	}
}
