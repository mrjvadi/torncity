package operator

import (
	"context"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// OrphanShiftReport is what a start that was refused after it wrote left behind (the refusal that committed), and what the repair did
// with it. In a dry run Applied is false and nothing was written.
type OrphanShiftReport struct {
	Applied bool
	// Kitchens: per settlement, the meal openings with no shift, the points the kitchen ate above its shifts (given back to the pot).
	Kitchens []OrphanKitchen
	// Refunds: the input movements with no shift, given back as refused_shift_refund.
	Refunds []postgres.OrphanMove
	// Actions: the pending settlement.worked actions with no shift, cancelled.
	Actions []string
}

// OrphanKitchen is one settlement's share of the meals.
type OrphanKitchen struct {
	SettlementID string
	Meals        []postgres.OrphanMeal
	// OpenedPoints is the points those rows opened into the pot (they stay there); GivenBack the points eaten that go back to the pot.
	OpenedPoints, GivenBack int64
}

// RepairOrphanShifts finds, and with apply repairs, what refused shift starts left behind: meal openings whose shift does not exist
// (the points the kitchen ate for them go back to the pot, the rows stay and are detached from the missing shift), production inputs
// and board food moved for a shift that does not exist (given back to the side they left, reason refused_shift_refund, referencing
// the original movement), and pending finish actions of such shifts (cancelled). The audit row is written first, in the same
// transaction as the repair; a second run finds nothing, because every repaired thing stops matching.
func (o Ops) RepairOrphanShifts(ctx context.Context, actor Actor, apply bool) (OrphanShiftReport, error) {
	actor, err := actor.check()
	if err != nil {
		return OrphanShiftReport{}, err
	}
	rep := OrphanShiftReport{Applied: apply}
	admin := postgres.NewEconomyAdmin(o.Pool)
	err = postgres.NewUnitOfWork(o.Pool, o.Language).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		meals, err := admin.OrphanMeals(ctx)
		if err != nil {
			return err
		}
		moves, err := admin.OrphanMoves(ctx)
		if err != nil {
			return err
		}
		actions, err := admin.OrphanActions(ctx)
		if err != nil {
			return err
		}
		bySettlement := map[string]*OrphanKitchen{}
		var order []string
		for _, m := range meals {
			k := bySettlement[m.SettlementID]
			if k == nil {
				k = &OrphanKitchen{SettlementID: m.SettlementID}
				bySettlement[m.SettlementID] = k
				order = append(order, m.SettlementID)
			}
			k.Meals = append(k.Meals, m)
			k.OpenedPoints += m.Points
		}
		sort.Strings(order)
		for _, id := range order {
			k := bySettlement[id]
			// the kitchen's own books say what the refused shifts ate: eaten above what the shifts that exist record
			excess, err := admin.KitchenExcess(ctx, id)
			if err != nil {
				return err
			}
			k.GivenBack = excess
			rep.Kitchens = append(rep.Kitchens, *k)
		}
		rep.Refunds, rep.Actions = moves, actions
		if !apply {
			return nil
		}
		if len(meals)+len(moves)+len(actions) == 0 {
			return nil
		}
		if err := admin.AppendAudit(ctx, postgres.AuditEntry{Actor: actor.Name, Action: "settlement.repair_orphan_shifts", TargetType: "settlement_shifts",
			NewValue: map[string]any{"meals": len(meals), "refunds": len(moves), "actions": len(actions), "kitchens": len(rep.Kitchens)},
			Reason:   actor.Reason, At: actor.At}); err != nil {
			return err
		}
		for _, k := range rep.Kitchens {
			ids := make([]string, 0, len(k.Meals))
			for _, m := range k.Meals {
				ids = append(ids, m.ID)
			}
			if err := admin.ReleaseKitchen(ctx, k.SettlementID, k.GivenBack, ids); err != nil {
				return err
			}
		}
		for _, m := range moves {
			mv := application.ItemMove{
				ID: newID(), Item: m.Item, Qty: m.Qty, Reason: application.ItemRefusedShiftRefund,
				ReferenceType: postgres.RefusedShiftRefundReference, ReferenceID: m.ID, At: actor.At,
			}
			if m.FromPlayer != "" {
				if err := tx.Items().LockOwner(ctx, m.FromPlayer); err != nil {
					return err
				}
				mv.To, mv.ToHolding = m.FromPlayer, m.FromHolding
			} else {
				mv.ToOrg, mv.ToHolding = application.Org{Kind: m.FromOrgKind, ID: m.FromOrg}, m.FromHolding
				if err := tx.Items().LockOrg(ctx, mv.ToOrg); err != nil {
					return err
				}
			}
			if err := tx.Items().Move(ctx, mv); err != nil {
				return err
			}
		}
		if len(actions) > 0 {
			return admin.CancelActions(ctx, actions, actor.At)
		}
		return nil
	})
	return rep, err
}
