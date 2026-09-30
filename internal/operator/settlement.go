package operator

import (
	"context"
	"errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// SettlementTopup is one operator top-up of a village's treasury made.
type SettlementTopup struct {
	SettlementID, Name, ID string
	Amount                 int64
	GrantedBy              string
}

// GrantSettlement pays amount (minor units) into one founded settlement's
// treasury from system_source (ledger reason settlement_topup, one
// settlement_topups row): the audit row first, then the top-up.
func (o Ops) GrantSettlement(ctx context.Context, settlementID string, amount int64, actor Actor) (SettlementTopup, error) {
	actor, err := actor.check()
	if err != nil {
		return SettlementTopup{}, err
	}
	if amount <= 0 {
		return SettlementTopup{}, errors.New("operator: a top-up must be above zero")
	}
	var s application.FoundedSettlement
	if err := postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		s, err = tx.Settlements().ByID(ctx, settlementID)
		return err
	}); err != nil {
		return SettlementTopup{}, err
	}
	g := SettlementTopup{SettlementID: s.CityID, Name: s.Name, ID: newID(), Amount: amount, GrantedBy: "admin:" + actor.Name}
	if err := postgres.NewEconomyAdmin(o.Pool).AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "settlement.grant",
		TargetType: "settlement_topups", NewValue: map[string]any{"settlement": s.CityID, "amount": amount},
		Reason: actor.Reason, At: actor.At}); err != nil {
		return SettlementTopup{}, err
	}
	err = postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return application.TopupSettlementTreasury(ctx, tx, s.CityID, amount, g.ID, newID(), g.GrantedBy, actor.Reason, actor.At)
	})
	return g, err
}

// BackfillSettlementGrants gives every founded settlement that has no
// founding grant yet its one grant of amount (source "backfill"). Each
// village is its own transaction and the grant row's primary key lets only
// one run or replica win, so it is safe to repeat and to run twice at once.
// It returns the settlements it granted.
func (o Ops) BackfillSettlementGrants(ctx context.Context, amount int64, actor Actor) ([]string, error) {
	actor, err := actor.check()
	if err != nil {
		return nil, err
	}
	if amount <= 0 {
		return nil, errors.New("operator: settlement.founding_grant is not above zero; there is nothing to backfill")
	}
	uow := postgres.NewUnitOfWork(o.Pool, o.Language)
	var ids []string
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		ids, err = tx.SettlementTreasury().FoundedWithoutGrant(ctx)
		return err
	}); err != nil {
		return nil, err
	}
	if err := postgres.NewEconomyAdmin(o.Pool).AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "settlement.backfill_grants",
		TargetType: "settlement_grants", NewValue: map[string]any{"amount": amount, "candidates": len(ids)},
		Reason: actor.Reason, At: actor.At}); err != nil {
		return nil, err
	}
	var granted []string
	for _, id := range ids {
		var fresh bool
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			var err error
			fresh, err = application.GrantSettlementTreasury(ctx, tx, id, amount, newID(),
				application.SettlementGrantBackfill, "admin:"+actor.Name, actor.At)
			return err
		}); err != nil {
			return granted, err
		}
		if fresh {
			granted = append(granted, id)
		}
	}
	return granted, nil
}
