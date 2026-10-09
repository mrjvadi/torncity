package operator

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// RefundNationalLevy gives every founded settlement back what the national levy took from its treasury (ADR 0009
// section 2, reason levy_refund; migration 0135). The audit row is written first, then all refunds in one
// transaction; a settlement already refunded is skipped, so a second run pays nothing.
func (o Ops) RefundNationalLevy(ctx context.Context, actor Actor) ([]application.LevyRefund, error) {
	actor, err := actor.check()
	if err != nil {
		return nil, err
	}
	if err := postgres.NewEconomyAdmin(o.Pool).AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "settlement.refund_national_levy",
		TargetType: "levy_refunds", NewValue: map[string]any{}, Reason: actor.Reason, At: actor.At}); err != nil {
		return nil, err
	}
	var out []application.LevyRefund
	err = postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		out, err = application.RefundNationalLevy(ctx, tx, newID, "admin:"+actor.Name, actor.Reason, actor.At)
		return err
	})
	return out, err
}
