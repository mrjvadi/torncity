package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
)

// militaryRules is the armed forces' tuning from the configuration.
func militaryRules(c config.Military) handlers.MilitaryRules {
	return handlers.MilitaryRules{Period: c.Period, ReadinessLossBPS: int64(c.ReadinessLossBPS),
		ReadinessRecoveryBPS: int64(c.ReadinessRecoveryBPS), ReferenceRadarKM: int64(c.ReferenceRadarKM)}
}

// diplomacyRules is diplomacy's tuning from the configuration.
func diplomacyRules(c config.Diplomacy) handlers.DiplomacyRules {
	return handlers.DiplomacyRules{SanctionNotice: c.SanctionNotice, SanctionMinDuration: c.SanctionMinDuration,
		TreatyOfferTTL: c.TreatyOfferTTL, EndedShownFor: c.EndedShownFor, HistoryPageSize: c.HistoryPageSize}
}

// bindMilitary maps the armed forces', diplomacy's and appointments'
// commands to their handlers (docs/adr/0022-military-and-diplomacy.md).
func (h phaseHandlers) bindMilitary() map[string]commandFunc {
	m, d, a := h.military, h.diplomacy, h.appointments
	return map[string]commandFunc{
		"military.ministry": decoded(m.Ministry),
		"military.forces":   decoded(m.Forces),
		"military.branch":   decoded(m.Branch),
		"military.station":  decoded(m.Station),
		"military.procure":  decoded(m.Procure),
		"military.buy":      decoded(m.ArmsBuy),
		// The scheduler's: a defence period ending, equipment landing.
		"military.settle": decoded(m.Settle),
		"military.arrive": decoded(m.Arrive),

		"diplomacy.sanctions": decoded(d.Sanctions),
		"diplomacy.impose":    decoded(d.Impose),
		"diplomacy.lift":      decoded(d.Lift),
		"diplomacy.treaties":  decoded(d.Treaties),
		"diplomacy.propose":   decoded(d.Propose),
		"diplomacy.answer":    decoded(d.Answer),
		"diplomacy.end":       decoded(d.End),
		"diplomacy.history":   decoded(d.History),

		"gov.appoint": decoded(a.Appoint),
		"gov.seat":    decoded(a.Seat),
		"gov.dismiss": decoded(a.Dismiss),
		"gov.unseat":  decoded(a.Unseat),
	}
}
