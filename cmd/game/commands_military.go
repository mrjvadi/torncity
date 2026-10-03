package main

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
)

// militaryRules is the armed forces' tuning from the configuration.
// retrofitTime (company.retrofit_time) is shared with the production
// economy's own retrofit handler: applying an upgrade kit takes the same
// time whether the unit is a company's good or a state's asset.
func militaryRules(c config.Military, retrofitTime time.Duration) handlers.MilitaryRules {
	return handlers.MilitaryRules{Period: c.Period, ReadinessLossBPS: int64(c.ReadinessLossBPS),
		ReadinessRecoveryBPS: int64(c.ReadinessRecoveryBPS), ReferenceRadarKM: int64(c.ReferenceRadarKM),
		LicenceRevokeNotice: c.LicenceRevokeNotice, EndedLicencesShown: c.EndedLicencesShown, RetrofitTime: retrofitTime}
}

// diplomacyRules is diplomacy's tuning from the configuration.
func diplomacyRules(c config.Diplomacy) handlers.DiplomacyRules {
	return handlers.DiplomacyRules{SanctionNotice: c.SanctionNotice, SanctionMinDuration: c.SanctionMinDuration,
		TreatyOfferTTL: c.TreatyOfferTTL, EndedShownFor: c.EndedShownFor, HistoryPageSize: c.HistoryPageSize}
}

// warRules is war's tuning from the configuration.
func warRules(c config.War) handlers.WarRules {
	return handlers.WarRules{DeclarationNotice: c.DeclarationNotice, ProposalTTL: c.ProposalTTL,
		EndedShownFor: c.EndedShownFor, BoardOperations: c.BoardOperations, NoticeCap: c.NoticeCap}
}

// bindMilitary maps the armed forces', diplomacy's and appointments'
// commands to their handlers (docs/adr/0022-military-and-diplomacy.md).
func (h phaseHandlers) bindMilitary() map[string]commandFunc {
	m, d, a, w := h.military, h.diplomacy, h.appointments, h.war
	table := map[string]commandFunc{
		"military.ministry": decoded(m.Ministry),
		"military.forces":   decoded(m.Forces),
		"military.branch":   decoded(m.Branch),
		"military.station":  decoded(m.Station),
		"military.procure":  decoded(m.Procure),
		"military.buy":      decoded(m.ArmsBuy),
		"military.licences": decoded(m.Licences),
		"military.licence":  decoded(m.Licence),
		"military.kitbuy":   decoded(m.ProcureKit),
		"military.retrofit": decoded(m.RetrofitState),
		// The scheduler's: a defence period ending, equipment landing. A
		// state's retrofit finishes through company.retrofitted, the same
		// production-economy handler a company's own retrofit does
		// (Retrofitted is already generic to which org kind it moved).
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

		"war.board":   decoded(w.Board),
		"war.declare": decoded(w.Declare),
		"war.join":    decoded(w.Join),
		"war.propose": decoded(w.Propose),
		"war.answer":  decoded(w.Answer),
		"war.resume":  decoded(w.Resume),
		"war.room":    decoded(w.Room),
		"war.target":  decoded(w.Target),
		"war.launch":  decoded(w.Launch),
		// The scheduler's: an operation reaching its target.
		"war.resolve": decoded(w.Resolve),
	}
	// The army and war are offered only where a barracks stands, and refuse
	// everywhere else as if they did not exist. The scheduler's own commands
	// (a period ending, equipment landing, an operation reaching its target)
	// are not a player's and are not gated.
	for name, f := range table {
		switch {
		case name == "military.settle", name == "military.arrive", name == "war.resolve":
		case strings.HasPrefix(name, "military."), strings.HasPrefix(name, "war."):
			table[name] = h.needBarracks(f)
		}
	}
	return table
}

// needBarracks refuses a military or war command from a player who is not in
// a settlement with a barracks standing.
func (h phaseHandlers) needBarracks(f commandFunc) commandFunc {
	return func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
		gate := h.militaryGate
		if gate == nil {
			gate = h.village.MilitaryOpen
		}
		if err := gate(ctx, env.Metadata); err != nil {
			return nil, err
		}
		return f(ctx, env)
	}
}
