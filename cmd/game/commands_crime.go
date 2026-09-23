package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// bindCrime maps the crime commands to their handler. bind merges it into
// the one table bindAll checks against the subscriptions.
func (h phaseHandlers) bindCrime() map[string]commandFunc {
	return map[string]commandFunc{
		"crime.hub": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.crime.Hub(ctx, env.Metadata)
		},
		"crime.list": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeCategoryRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.List(ctx, env.Metadata, req)
		},
		"crime.view": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.View(ctx, env.Metadata, req)
		},
		"crime.commit": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeCommitRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.Commit(ctx, env.Metadata, req)
		},
		"crime.record": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.crime.Record(ctx, env.Metadata)
		},
		"crime.jail": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.crime.Jail(ctx, env.Metadata)
		},
		"crime.bail": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.BailRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.Bail(ctx, env.Metadata, req)
		},
		"crime.report": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeReportRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.Report(ctx, env.Metadata, req)
		},
		"crime.cases": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.crime.Cases(ctx, env.Metadata)
		},
		// The scheduler's dispatch payloads, as for job.finish_shift.
		"crime.resolve": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.Resolve(ctx, env.Metadata, req)
		},
		"crime.conclude": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.Conclude(ctx, env.Metadata, req)
		},
		"crime.release": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.crime.Release(ctx, env.Metadata, req)
		},
	}
}

// crimeRules gathers the crime engine's tuning from configuration.
func crimeRules(c config.Crime) handlers.CrimeRules {
	return handlers.CrimeRules{
		Nerve: crime.NerveRules{Max: c.NerveMax, RegenAmount: c.NerveRegenAmount, RegenInterval: c.NerveRegenInterval},
		Heat:  crime.HeatRules{Max: c.HeatMax, DecayPerHour: c.HeatDecayPerHour},
		Victims: crime.VictimRules{
			Protection:     crime.Protection{MinLevel: c.ProtectMinLevel, MinAge: c.ProtectMinAge},
			ActiveWindow:   c.ActiveWindow,
			VictimCooldown: c.VictimCooldown,
			ThiefCooldown:  c.ThiefCooldown,
		},
		ArrivalLinger: c.ArrivalLinger,
		ReportWindow:  c.ReportWindow,
		Investigation: crime.InvestigationModel{
			BaseBPS:         c.InvestigationBaseBPS,
			PerHeatBPS:      c.InvestigationPerHeatBPS,
			WitnessBonusBPS: c.InvestigationWitnessBonusBPS,
			EffortWeightBPS: c.InvestigationEffortWeightBPS,
		},
		InvestigationDuration: c.InvestigationDuration,
		NPCDailyCap:           money.FromMinor(c.NPCDailyCap),
	}
}

// newCrimeHandler builds the crime handler. It reads crimes from the live
// registry, a city's justice policy only through the resolver (ADR 0015), and
// runs on the game clock.
func newCrimeHandler(
	uow application.UnitOfWork,
	msgs handlers.Translator,
	registry *content.Registry,
	cities application.CityRepository,
	policy application.PolicyReader,
	scale gametime.Scale,
	cfg config.Crime,
	idempotencyTTL time.Duration,
) *handlers.CrimeHandler {
	return handlers.NewCrimeHandler(uow, uuidGenerator{}, msgs, registry, cities, policy, scale,
		cryptoDice{}, crimeRules(cfg), idempotencyTTL, nil)
}

// cryptoDice rolls with crypto/rand. A thief who could predict math/rand's
// sequence could time an attempt to a certain success.
type cryptoDice struct{}

// Roll returns a uniform number in [0, n), by rejection so no value is
// likelier than another.
func (cryptoDice) Roll(n int64) int64 {
	if n <= 1 {
		return 0
	}
	limit := uint64(1<<63) - uint64(1<<63)%uint64(n)
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic("game: no randomness available for the crime dice: " + err.Error())
		}
		v := binary.BigEndian.Uint64(b[:]) >> 1
		if v < limit {
			return int64(v % uint64(n))
		}
	}
}
