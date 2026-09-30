package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// CitizenRepository is the citizen loop's port (migration 0058_citizen_loop),
// bound to one transaction.
type CitizenRepository struct{ q querier }

var _ application.CitizenRepository = (*CitizenRepository)(nil)

const settlementLotsLotUnique = "settlement_lots_lot_unique"

// Lots lists every owned lot of a settlement.
func (r *CitizenRepository) Lots(ctx context.Context, settlementID string) ([]application.SettlementLot, error) {
	rows, err := r.q.Query(ctx, `
		SELECT id::text, settlement_id::text, lot_x, lot_y, tenure, owner_id::text, price,
		       ledger_transaction_id::text, acquired_at
		  FROM settlement_lots WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing settlement lots: %w", err)
	}
	defer rows.Close()
	var out []application.SettlementLot
	for rows.Next() {
		var l application.SettlementLot
		if err := rows.Scan(&l.ID, &l.SettlementID, &l.X, &l.Y, &l.Tenure, &l.OwnerID, &l.Price,
			&l.LedgerTransactionID, &l.AcquiredAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning settlement lot: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// InsertLot records a bought lot.
func (r *CitizenRepository) InsertLot(ctx context.Context, l application.SettlementLot) error {
	_, err := r.q.Exec(ctx, `
		INSERT INTO settlement_lots (id, settlement_id, lot_x, lot_y, tenure, owner_id, price, ledger_transaction_id, acquired_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8::uuid, $9)`,
		l.ID, l.SettlementID, l.X, l.Y, l.Tenure, l.OwnerID, l.Price, l.LedgerTransactionID, l.AcquiredAt.UTC())
	if violates(err, sqlstateUniqueViolation, settlementLotsLotUnique) {
		return application.ErrLotTaken
	}
	if err != nil {
		return fmt.Errorf("postgres: recording the settlement lot: %w", err)
	}
	return nil
}

// Terms reads the head's levers.
func (r *CitizenRepository) Terms(ctx context.Context, settlementID string) (application.LotTerms, error) {
	var price, permit sql.NullInt64
	var tax sql.NullInt32
	err := r.q.QueryRow(ctx, `SELECT lot_price, permit_fee, tax_bps FROM settlement_lot_terms WHERE settlement_id = $1::uuid`,
		settlementID).Scan(&price, &permit, &tax)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.LotTerms{}, nil
	}
	if err != nil {
		return application.LotTerms{}, fmt.Errorf("postgres: reading the lot terms: %w", err)
	}
	return application.LotTerms{
		LotPrice: price.Int64, PermitFee: permit.Int64, HasPermit: permit.Valid,
		TaxBPS: int(tax.Int32), HasTax: tax.Valid,
	}, nil
}

// SetTerms writes the head's levers.
func (r *CitizenRepository) SetTerms(ctx context.Context, settlementID string, t application.LotTerms, by string, at time.Time) error {
	var price, permit, tax any
	if t.LotPrice > 0 {
		price = t.LotPrice
	}
	if t.HasPermit {
		permit = t.PermitFee
	}
	if t.HasTax {
		tax = t.TaxBPS
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO settlement_lot_terms (settlement_id, lot_price, permit_fee, tax_bps, updated_by, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6)
		ON CONFLICT (settlement_id) DO UPDATE
		   SET lot_price = EXCLUDED.lot_price, permit_fee = EXCLUDED.permit_fee, tax_bps = EXCLUDED.tax_bps,
		       updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at`,
		settlementID, price, permit, tax, by, at.UTC()); err != nil {
		return fmt.Errorf("postgres: writing the lot terms: %w", err)
	}
	return nil
}

const privateBuildingColumns = `building_id::text, settlement_id::text, owner_id::text, permit_fee, construction_paid,
	materials_paid, assessed_value, COALESCE(ledger_transaction_id::text, ''), last_rest_at, created_at`

func scanPrivateBuilding(row pgx.Row) (application.PrivateBuilding, error) {
	var b application.PrivateBuilding
	err := row.Scan(&b.BuildingID, &b.SettlementID, &b.OwnerID, &b.PermitFee, &b.ConstructionPaid,
		&b.MaterialsPaid, &b.AssessedValue, &b.LedgerTransactionID, &b.LastRestAt, &b.CreatedAt)
	return b, err
}

// RecordPrivateBuilding writes the ownership row of a new building.
func (r *CitizenRepository) RecordPrivateBuilding(ctx context.Context, b application.PrivateBuilding) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid,
		       materials_paid, assessed_value, ledger_transaction_id, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::uuid, $9)`,
		b.BuildingID, b.SettlementID, b.OwnerID, b.PermitFee, b.ConstructionPaid, b.MaterialsPaid,
		b.AssessedValue, nullText(b.LedgerTransactionID), b.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording the private building: %w", err)
	}
	return nil
}

// PrivateBuildings lists a settlement's private buildings.
func (r *CitizenRepository) PrivateBuildings(ctx context.Context, settlementID string) ([]application.PrivateBuilding, error) {
	rows, err := r.q.Query(ctx, `SELECT `+privateBuildingColumns+` FROM settlement_private_buildings
		WHERE settlement_id = $1::uuid ORDER BY created_at, building_id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing private buildings: %w", err)
	}
	defer rows.Close()
	var out []application.PrivateBuilding
	for rows.Next() {
		b, err := scanPrivateBuilding(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning private building: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PrivateBuilding returns one by building id.
func (r *CitizenRepository) PrivateBuilding(ctx context.Context, buildingID string) (*application.PrivateBuilding, error) {
	b, err := scanPrivateBuilding(r.q.QueryRow(ctx, `SELECT `+privateBuildingColumns+`
		FROM settlement_private_buildings WHERE building_id = $1::uuid`, buildingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrPrivateBuildingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading private building %s: %w", buildingID, err)
	}
	return &b, nil
}

// MarkRested records when a house's owner last rested at home.
func (r *CitizenRepository) MarkRested(ctx context.Context, buildingID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE settlement_private_buildings SET last_rest_at = $2 WHERE building_id = $1::uuid`,
		buildingID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: marking a rest at home: %w", err)
	}
	return nil
}

const propertyTaxColumns = `id::text, settlement_id::text, player_id::text, period_no, assessed_value, tax_bps, due,
	paid_at, COALESCE(ledger_transaction_id::text, ''), created_at`

func scanPropertyTax(row pgx.Row) (application.PropertyTax, error) {
	var t application.PropertyTax
	err := row.Scan(&t.ID, &t.SettlementID, &t.PlayerID, &t.PeriodNo, &t.AssessedValue, &t.TaxBPS, &t.Due,
		&t.PaidAt, &t.LedgerTransactionID, &t.CreatedAt)
	return t, err
}

// InsertTax writes one period's tax row; the unique key fences a second charge.
func (r *CitizenRepository) InsertTax(ctx context.Context, t application.PropertyTax) (bool, error) {
	tag, err := r.q.Exec(ctx, `
		INSERT INTO settlement_property_tax (id, settlement_id, player_id, period_no, assessed_value, tax_bps, due, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
		ON CONFLICT (settlement_id, player_id, period_no) DO NOTHING`,
		t.ID, t.SettlementID, t.PlayerID, t.PeriodNo, t.AssessedValue, t.TaxBPS, t.Due, t.CreatedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording the property tax: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *CitizenRepository) taxRows(ctx context.Context, where string, args ...any) ([]application.PropertyTax, error) {
	rows, err := r.q.Query(ctx, `SELECT `+propertyTaxColumns+` FROM settlement_property_tax WHERE `+where+
		` ORDER BY period_no, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the property tax: %w", err)
	}
	defer rows.Close()
	var out []application.PropertyTax
	for rows.Next() {
		t, err := scanPropertyTax(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning the property tax: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UnpaidTax lists a player's unpaid rows in a settlement, oldest first.
func (r *CitizenRepository) UnpaidTax(ctx context.Context, settlementID, playerID string) ([]application.PropertyTax, error) {
	return r.taxRows(ctx, `settlement_id = $1::uuid AND player_id = $2::uuid AND paid_at IS NULL`, settlementID, playerID)
}

// SettlementDebt lists every unpaid row of a settlement.
func (r *CitizenRepository) SettlementDebt(ctx context.Context, settlementID string) ([]application.PropertyTax, error) {
	return r.taxRows(ctx, `settlement_id = $1::uuid AND paid_at IS NULL`, settlementID)
}

// MarkTaxPaid records that a period's tax was paid.
func (r *CitizenRepository) MarkTaxPaid(ctx context.Context, id, ledgerTransactionID string, at time.Time) error {
	tag, err := r.q.Exec(ctx, `UPDATE settlement_property_tax SET paid_at = $3, ledger_transaction_id = $2::uuid
		WHERE id = $1::uuid AND paid_at IS NULL`, id, ledgerTransactionID, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: marking the property tax paid: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: property tax %s was not open", id)
	}
	return nil
}

// CitizenReader is the citizen loop's read side for the client API, over the pool.
type CitizenReader struct{ CitizenRepository }

// NewCitizenReader reads private property outside a transaction.
func NewCitizenReader(p *Pool) *CitizenReader {
	return &CitizenReader{CitizenRepository{q: p.shared()}}
}

// Names maps player ids to their display names.
func (r *CitizenReader) Names(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `SELECT id::text, display_name FROM players WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading player names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("postgres: scanning a player name: %w", err)
		}
		out[id] = name
	}
	return out, rows.Err()
}
