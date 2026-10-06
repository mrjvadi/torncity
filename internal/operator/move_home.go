package operator

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// HomeMove is the result of an operator moving a player's home.
type HomeMove struct {
	Label string
	// From and To are the settlement ids (From empty: the player had none).
	From, To string
	// FromName and ToName are their names, for the summary.
	FromName, ToName string
	NoOp             bool
	// Placed says the player's physical place moved to the settlement too.
	Placed bool
}

// MoveHome moves a player's home to a founded settlement, skipping only the residence cool-down
// (handlers.OperatorMoveHome: the same move a player's own join makes). The audit row (operator,
// reason, before and after) is written once the move has happened; a repeat that finds the
// player already at home there writes nothing and says so.
func (o Ops) MoveHome(ctx context.Context, playerCode, toSettlementID string, actor Actor) (HomeMove, error) {
	actor, err := actor.check()
	if err != nil {
		return HomeMove{}, err
	}
	admin := postgres.NewEconomyAdmin(o.Pool)
	playerID, label, err := admin.PlayerByCode(ctx, playerCode)
	if err != nil {
		return HomeMove{}, err
	}
	before, err := admin.PlayerPlace(ctx, playerID)
	if err != nil {
		return HomeMove{}, err
	}
	now := actor.At.UTC()
	meta := envelope.Metadata{RequestID: newID(), TraceID: newID(), Command: "admin.player.move_home",
		Language: o.Language, SchemaVersion: envelope.SchemaVersion}
	res := HomeMove{Label: label}
	var m handlers.OperatorHomeMove
	if err := postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		if m, err = handlers.OperatorMoveHome(ctx, tx, meta, playerID, toSettlementID, now); err != nil {
			return err
		}
		res.From, res.To, res.NoOp, res.Placed = m.From, m.To, m.NoOp, m.Placed
		res.ToName = nameOf(ctx, tx, m.To)
		res.FromName = nameOf(ctx, tx, m.From)
		return nil
	}); err != nil {
		return res, err
	}
	if m.NoOp {
		return res, nil
	}
	after, err := admin.PlayerPlace(ctx, playerID)
	if err != nil {
		return res, err
	}
	var since any
	if m.SinceBefore != nil {
		since = m.SinceBefore.UTC().Format(time.RFC3339)
	}
	err = admin.AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "player.move_home", TargetType: "player",
		NewValue: map[string]any{
			"player": playerID, "code": playerCode,
			"before":  map[string]any{"residence": m.From, "residence_since": since, "city": before.CityID, "place": before.PlaceCode},
			"after":   map[string]any{"residence": m.To, "residence_since": after.ResidenceSince, "city": after.CityID, "place": after.PlaceCode},
			"skipped": "residence cool-down", "placed": m.Placed,
		}, Reason: actor.Reason, At: now})
	return res, err
}

// nameOf is a settlement's name, or its id when it cannot be read.
func nameOf(ctx context.Context, tx application.Tx, id string) string {
	if id == "" {
		return ""
	}
	s, err := tx.Settlements().ByID(ctx, id)
	if err != nil {
		return id // a content city is not a founded settlement: its id stands for it
	}
	return s.Name
}
