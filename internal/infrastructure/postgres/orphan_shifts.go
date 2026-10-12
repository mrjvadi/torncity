package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RefusedShiftID is the shift_id a repaired meal row carries: the shift it was opened for was refused and never existed, so the row
// is kept (the food really left the warehouse and sits in the pot) but no longer names a shift. The nil uuid.
const RefusedShiftID = "00000000-0000-0000-0000-000000000000"

// The item reason of the goods a refused shift took and the repair gave back (an origin; its reference is the original movement).
const (
	RefusedShiftRefundReason    = "refused_shift_refund"
	RefusedShiftRefundReference = "item_movement"
)

// The three shapes of what a start that was refused after it wrote could leave behind (the bug of the refusal that committed).
// They are the verifier's counts and the repair's work lists, so they are written once.
const (
	orphanMealsFrom = `FROM settlement_meals m WHERE m.shift_id <> '` + RefusedShiftID + `'::uuid
		AND NOT EXISTS (SELECT 1 FROM settlement_shifts s WHERE s.id = m.shift_id)`
	orphanMovesFrom = `FROM item_movements m WHERE m.reason IN ('production_input', 'board_eaten') AND m.reference_type = 'settlement_shift'
		AND NOT EXISTS (SELECT 1 FROM settlement_shifts s WHERE s.id = m.reference_id)
		AND NOT EXISTS (SELECT 1 FROM item_movements r WHERE r.reason = '` + RefusedShiftRefundReason + `'
		                AND r.reference_type = '` + RefusedShiftRefundReference + `' AND r.reference_id = m.id)`
	orphanActionsFrom = `FROM game_actions g WHERE g.action_type = 'settlement_work' AND g.reference_type = 'settlement_shift'
		AND g.status IN ('scheduled', 'running')
		AND NOT EXISTS (SELECT 1 FROM settlement_shifts s WHERE s.id = g.reference_id)`
)

// OrphanMeal is a meal opening whose shift does not exist.
type OrphanMeal struct {
	ID, SettlementID, Item string
	Units, Points          int64
}

// OrphanMove is a production_input (or board_eaten) movement whose shift does not exist: the goods to give back, to the side they
// left (a settlement's warehouse, or a player's home store for a citizen's workplace).
type OrphanMove struct {
	ID, Item, Reason        string
	Qty                     int64
	FromPlayer, FromHolding string
	FromOrgKind, FromOrg    string
}

// OrphanMeals lists the meal openings whose shift is missing.
func (a *EconomyAdmin) OrphanMeals(ctx context.Context) ([]OrphanMeal, error) {
	rows, err := a.q.Query(ctx, `SELECT m.id::text, m.settlement_id::text, m.item_code, m.units, m.units * m.points_each `+orphanMealsFrom+` ORDER BY m.created_at, m.id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing meals without a shift: %w", err)
	}
	defer rows.Close()
	var out []OrphanMeal
	for rows.Next() {
		var m OrphanMeal
		if err := rows.Scan(&m.ID, &m.SettlementID, &m.Item, &m.Units, &m.Points); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// OrphanMoves lists the input movements whose shift is missing and which were not given back yet.
func (a *EconomyAdmin) OrphanMoves(ctx context.Context) ([]OrphanMove, error) {
	rows, err := a.q.Query(ctx, `SELECT m.id::text, m.item_code, m.reason, m.quantity, COALESCE(m.from_player::text, ''), COALESCE(m.from_holding, ''),
		COALESCE(m.from_org_kind, ''), COALESCE(m.from_org::text, '') `+orphanMovesFrom+` ORDER BY m.created_at, m.id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing inputs without a shift: %w", err)
	}
	defer rows.Close()
	var out []OrphanMove
	for rows.Next() {
		var m OrphanMove
		if err := rows.Scan(&m.ID, &m.Item, &m.Reason, &m.Qty, &m.FromPlayer, &m.FromHolding, &m.FromOrgKind, &m.FromOrg); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// OrphanActions lists the pending settlement.worked actions whose shift is missing.
func (a *EconomyAdmin) OrphanActions(ctx context.Context) ([]string, error) {
	rows, err := a.q.Query(ctx, `SELECT g.id::text `+orphanActionsFrom+` ORDER BY g.finish_at, g.id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing finish actions without a shift: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// KitchenExcess is the points a settlement's kitchen says were eaten above what its shifts record: the points the refused shifts
// ate. The kitchen row is locked. Never negative.
func (a *EconomyAdmin) KitchenExcess(ctx context.Context, settlementID string) (int64, error) {
	var excess int64
	err := a.q.QueryRow(ctx, `SELECT GREATEST(k.eaten_points - COALESCE((SELECT SUM(s.meal_points) FROM settlement_shifts s
		WHERE s.settlement_id = k.settlement_id), 0), 0)::bigint FROM settlement_kitchen k WHERE k.settlement_id = $1::uuid FOR UPDATE`, settlementID).Scan(&excess)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: reading the kitchen's excess: %w", err)
	}
	return excess, nil
}

// ReleaseKitchen gives eaten points back to the pot and detaches the listed meal rows from their missing shift.
func (a *EconomyAdmin) ReleaseKitchen(ctx context.Context, settlementID string, points int64, mealIDs []string) error {
	if _, err := a.q.Exec(ctx, `UPDATE settlement_kitchen SET eaten_points = eaten_points - $2, pot = pot + $2 WHERE settlement_id = $1::uuid`, settlementID, points); err != nil {
		return fmt.Errorf("postgres: giving points back to the pot: %w", err)
	}
	if _, err := a.q.Exec(ctx, `UPDATE settlement_meals SET shift_id = '`+RefusedShiftID+`'::uuid WHERE id = ANY($1::uuid[])`, mealIDs); err != nil {
		return fmt.Errorf("postgres: marking the refused meals: %w", err)
	}
	return nil
}

// CancelActions cancels the listed pending actions.
func (a *EconomyAdmin) CancelActions(ctx context.Context, ids []string, at time.Time) error {
	if _, err := a.q.Exec(ctx, `UPDATE game_actions SET status = 'cancelled', completed_at = $2 WHERE id = ANY($1::uuid[]) AND status IN ('scheduled', 'running')`, ids, at.UTC()); err != nil {
		return fmt.Errorf("postgres: cancelling the actions of refused shifts: %w", err)
	}
	return nil
}
