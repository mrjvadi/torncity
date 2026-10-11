package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// Stage E (docs/adr/0023-health-missions-factions.md): health and
// hospitals, factions and their organised crimes, missions.
type stageEHandlers struct {
	health   *handlers.HealthHandler
	factions *handlers.FactionsHandler
	missions *handlers.MissionsHandler
}

// bindStageE maps stage E's commands to their handlers.
func (h phaseHandlers) bindStageE() map[string]commandFunc {
	hh, fh, mh := h.stageE.health, h.stageE.factions, h.stageE.missions
	return map[string]commandFunc{
		"health.hospital": bare(hh.Hospital),
		"health.treat":    decoded(hh.Treat),
		"health.survival": bare(h.survival.Needs),
		// The teaching network (docs/adr/0071).
		"settlement.apprentice.mine":    bare(h.apprentice.Mine),
		"settlement.apprentice.offer":   decoded(h.apprentice.Offer),
		"settlement.apprentice.accept":  decoded(h.apprentice.Accept),
		"settlement.apprentice.leave":   decoded(h.apprentice.Leave),
		"settlement.apprentice.dismiss": decoded(h.apprentice.Dismiss),
		"settlement.apprentice.exam":    decoded(h.apprentice.Exam),
		"settlement.apprentice.join":    decoded(h.apprentice.Join),
		"education.teachers":            decoded(h.apprentice.Teachers),
		"health.drink":                  decoded(h.survival.Drink),
		"health.wear":                   decoded(h.survival.Wear),
		"health.remove":                 decoded(h.survival.Remove),
		"health.warm":                   bare(h.survival.Warm),
		"health.cure":                   bare(h.survival.Cure),
		"health.clinic":                 decoded(hh.Clinic),
		"health.price":                  decoded(hh.Price),
		"health.open":                   decoded(hh.Open),
		// The scheduler's: a stay reaching its end.
		"health.discharge": decoded(hh.Discharge),

		"faction.list":     decoded(fh.List),
		"faction.view":     decoded(fh.View),
		"faction.found":    decoded(fh.Found),
		"faction.mine":     bare(fh.Mine),
		"faction.members":  bare(fh.Members),
		"faction.invite":   decoded(fh.Invite),
		"faction.apply":    decoded(fh.Apply),
		"faction.answer":   decoded(fh.Answer),
		"faction.kick":     decoded(fh.Kick),
		"faction.rank":     decoded(fh.Rank),
		"faction.leave":    decoded(fh.Leave),
		"faction.bank":     bare(fh.Bank),
		"faction.deposit":  decoded(fh.Deposit),
		"faction.withdraw": decoded(fh.Withdraw),
		"faction.link":     decoded(fh.Link),
		"faction.crime":    bare(fh.CrimeBoard),
		"faction.plan":     decoded(fh.Plan),
		"faction.join":     bare(fh.Join),
		"faction.launch":   decoded(fh.Launch),
		"faction.calloff":  decoded(fh.CallOff),
		// The scheduler's: an organised crime reaching its end.
		"faction.resolve": decoded(fh.Resolve),

		"mission.board":   decoded(mh.Board),
		"mission.view":    decoded(mh.View),
		"mission.accept":  decoded(mh.Accept),
		"mission.mine":    bare(mh.Mine),
		"mission.deliver": decoded(mh.Deliver),
		"mission.abandon": decoded(mh.Abandon),
	}
}
