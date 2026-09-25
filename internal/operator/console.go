package operator

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// The console's actions on players, timed actions and companies. Each
// refuses a missing actor or reason before it touches anything, and leaves
// its audit row.

func (a Actor) panel() postgres.PanelActor {
	return postgres.PanelActor{Name: a.Name, Reason: a.Reason, At: a.At}
}

// Moderate mutes or bans a player, for a time (for > 0) or until lifted.
func (o Ops) Moderate(ctx context.Context, player, kind string, dur time.Duration, actor Actor) (postgres.Moderated, error) {
	actor, err := actor.check()
	if err != nil {
		return postgres.Moderated{}, err
	}
	return postgres.NewPanelOps(o.Pool).Moderate(ctx, postgres.ModerationChange{Player: player, Kind: kind, For: dur,
		Actor: actor.panel()})
}

// LiftModeration lifts a player's standing mute or ban.
func (o Ops) LiftModeration(ctx context.Context, player, kind string, actor Actor) (postgres.Moderated, error) {
	actor, err := actor.check()
	if err != nil {
		return postgres.Moderated{}, err
	}
	return postgres.NewPanelOps(o.Pool).LiftModeration(ctx, postgres.ModerationChange{Player: player, Kind: kind,
		Actor: actor.panel()})
}

// Release ends a player's jail sentence now; the scheduler then releases
// them the way a served sentence is.
func (o Ops) Release(ctx context.Context, player string, actor Actor) (postgres.Ended, error) {
	actor, err := actor.check()
	if err != nil {
		return postgres.Ended{}, err
	}
	return postgres.NewPanelOps(o.Pool).ReleaseFromJail(ctx, actor.panel(), player)
}

// Discharge ends a player's hospital stay now; the scheduler then
// discharges them the way a finished stay is.
func (o Ops) Discharge(ctx context.Context, player string, actor Actor) (postgres.Ended, error) {
	actor, err := actor.check()
	if err != nil {
		return postgres.Ended{}, err
	}
	return postgres.NewPanelOps(o.Pool).DischargeFromHospital(ctx, actor.panel(), player)
}

// Requeue hands a failed or stuck timed action back to the scheduler.
func (o Ops) Requeue(ctx context.Context, actionID string, actor Actor) (postgres.Requeued, error) {
	actor, err := actor.check()
	if err != nil {
		return postgres.Requeued{}, err
	}
	return postgres.NewPanelOps(o.Pool).RequeueAction(ctx, actor.panel(), actionID)
}

// Dissolve closes a company on the operator's authority, as its owner's
// closing would: the audit row first, so the intent is on record, then the
// dissolution in one unit of work with its events.
func (o Ops) Dissolve(ctx context.Context, company string, actor Actor) (handlers.OperatorClosing, error) {
	var out handlers.OperatorClosing
	actor, err := actor.check()
	if err != nil {
		return out, err
	}
	pack, err := postgres.NewContentStore(o.Pool).LoadActive(ctx)
	if err != nil {
		return out, err
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return out, err
	}
	if err := postgres.NewEconomyAdmin(o.Pool).AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name,
		Action: "company.dissolve", TargetType: "companies", NewValue: map[string]any{"company": company},
		Reason: actor.Reason, At: actor.At}); err != nil {
		return out, err
	}
	now := actor.At.UTC()
	meta := envelope.Metadata{RequestID: newID(), TraceID: newID(), Command: "company.dissolve",
		Language: o.Language, SchemaVersion: envelope.SchemaVersion}
	cities := postgres.NewCityRepository(o.Pool)
	policy := postgres.NewPolicyReader(o.Pool, nil)
	err = postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		out, err = handlers.DissolveCompany(ctx, tx, cities, policy, snap, meta, company, now)
		return err
	})
	return out, err
}
