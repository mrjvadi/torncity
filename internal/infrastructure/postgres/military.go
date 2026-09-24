package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists the armed forces (migrations/0021_military): each
// country's defence clock and settled periods, the arms its state bought,
// the pieces it holds with their garrisons, and equipment on the move.

// MilitaryRepository implements application.MilitaryRepository.
type MilitaryRepository struct {
	q querier
}

var _ application.MilitaryRepository = (*MilitaryRepository)(nil)

// Clock returns a country's clock, locked, opening it on first use.
func (r *MilitaryRepository) Clock(ctx context.Context, countryID string, now time.Time) (*application.MilitaryClock, error) {
	if !validUUID(countryID) {
		return nil, application.ErrJurisdictionNotFound
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO military_clocks (country_id, period_no, period_started_at, readiness_bps, updated_at)
		 SELECT $1::uuid, 1, $2, 10000, $2 WHERE EXISTS (SELECT 1 FROM jurisdictions WHERE id = $1::uuid AND kind = 'country')
		 ON CONFLICT (country_id) DO NOTHING`, countryID, now.UTC()); err != nil {
		return nil, fmt.Errorf("postgres: opening a country's defence clock: %w", err)
	}
	var c application.MilitaryClock
	err := r.q.QueryRow(ctx,
		`SELECT country_id::text, period_no, period_started_at, next_at, COALESCE(action_id::text, ''), readiness_bps, updated_at
		   FROM military_clocks WHERE country_id = $1::uuid FOR UPDATE`, countryID).Scan(
		&c.CountryID, &c.PeriodNo, &c.PeriodStartedAt, &c.NextAt, &c.ActionID, &c.ReadinessBPS, &c.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrJurisdictionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a country's defence clock: %w", err)
	}
	c.PeriodStartedAt, c.UpdatedAt = c.PeriodStartedAt.UTC(), c.UpdatedAt.UTC()
	if c.NextAt != nil {
		t := c.NextAt.UTC()
		c.NextAt = &t
	}
	return &c, nil
}

// SaveClock writes a clock.
func (r *MilitaryRepository) SaveClock(ctx context.Context, c application.MilitaryClock) error {
	var action any
	if c.ActionID != "" {
		action = c.ActionID
	}
	if _, err := r.q.Exec(ctx,
		`UPDATE military_clocks SET period_no = $2, period_started_at = $3, next_at = $4, action_id = $5::uuid,
		        readiness_bps = $6, updated_at = $7
		  WHERE country_id = $1::uuid`,
		c.CountryID, c.PeriodNo, c.PeriodStartedAt.UTC(), c.NextAt, action, c.ReadinessBPS, c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a country's defence clock: %w", err)
	}
	return nil
}

// RecordPeriod appends a settled period.
func (r *MilitaryRepository) RecordPeriod(ctx context.Context, p application.MilitaryPeriod) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO military_periods (country_id, period_no, started_at, ended_at, revenue, levy, appropriation,
		        upkeep_due, upkeep_paid, pieces, readiness_bps)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		p.CountryID, p.PeriodNo, p.StartedAt.UTC(), p.EndedAt.UTC(), p.Revenue, p.Levy, p.Appropriation,
		p.UpkeepDue, p.UpkeepPaid, p.Pieces, p.ReadinessBPS)
	if violates(err, sqlstateUniqueViolation, "military_periods_pkey") {
		return application.ErrPeriodSettled
	}
	if err != nil {
		return fmt.Errorf("postgres: recording a defence period: %w", err)
	}
	return nil
}

// Periods lists a country's latest periods, newest first.
func (r *MilitaryRepository) Periods(ctx context.Context, countryID string, limit int) ([]application.MilitaryPeriod, error) {
	if !validUUID(countryID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT country_id::text, period_no, started_at, ended_at, revenue, levy, appropriation, upkeep_due,
		        upkeep_paid, pieces, readiness_bps
		   FROM military_periods WHERE country_id = $1::uuid ORDER BY period_no DESC LIMIT $2`, countryID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading defence periods: %w", err)
	}
	defer rows.Close()
	var out []application.MilitaryPeriod
	for rows.Next() {
		var p application.MilitaryPeriod
		if err := rows.Scan(&p.CountryID, &p.PeriodNo, &p.StartedAt, &p.EndedAt, &p.Revenue, &p.Levy, &p.Appropriation,
			&p.UpkeepDue, &p.UpkeepPaid, &p.Pieces, &p.ReadinessBPS); err != nil {
			return nil, fmt.Errorf("postgres: scanning a defence period: %w", err)
		}
		p.StartedAt, p.EndedAt = p.StartedAt.UTC(), p.EndedAt.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// Revenue sums an account's positive entries in [from, to).
func (r *MilitaryRepository) Revenue(ctx context.Context, accountID string, from, to time.Time) (int64, error) {
	var sum int64
	if err := r.q.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
		  WHERE account_id = $1::uuid AND amount > 0 AND created_at >= $2 AND created_at < $3`,
		accountID, from.UTC(), to.UTC()).Scan(&sum); err != nil {
		return 0, fmt.Errorf("postgres: summing an account's revenue: %w", err)
	}
	return sum, nil
}

// RecordProcurement appends a purchase.
func (r *MilitaryRepository) RecordProcurement(ctx context.Context, p application.Procurement) (application.Procurement, error) {
	var design any
	if p.DesignID != "" {
		design = p.DesignID
	}
	err := r.q.QueryRow(ctx,
		`INSERT INTO procurements (id, country_id, listing_id, company_id, item_code, design_id, quantity, unit_price,
		        total, ledger_transaction_id, bought_by, office_code, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9, $10::uuid, $11::uuid, $12, $13)
		 RETURNING no`,
		p.ID, p.CountryID, p.ListingID, p.CompanyID, p.Item, design, p.Qty, p.UnitPrice, p.Total,
		p.LedgerTransactionID, p.BoughtBy, p.OfficeCode, p.At.UTC()).Scan(&p.No)
	if err != nil {
		return p, fmt.Errorf("postgres: recording a procurement: %w", err)
	}
	return p, nil
}

// Procurements lists a country's latest purchases.
func (r *MilitaryRepository) Procurements(ctx context.Context, countryID string, limit int) ([]application.Procurement, error) {
	if !validUUID(countryID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT id::text, no, country_id::text, listing_id::text, company_id::text, item_code, COALESCE(design_id::text, ''),
		        quantity, unit_price, total, ledger_transaction_id::text, bought_by::text, office_code, created_at
		   FROM procurements WHERE country_id = $1::uuid ORDER BY created_at DESC, no DESC LIMIT $2`, countryID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading procurements: %w", err)
	}
	defer rows.Close()
	var out []application.Procurement
	for rows.Next() {
		var p application.Procurement
		if err := rows.Scan(&p.ID, &p.No, &p.CountryID, &p.ListingID, &p.CompanyID, &p.Item, &p.DesignID, &p.Qty,
			&p.UnitPrice, &p.Total, &p.LedgerTransactionID, &p.BoughtBy, &p.OfficeCode, &p.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a procurement: %w", err)
		}
		p.At = p.At.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// ArmsListings lists the open listings of these goods.
func (r *MilitaryRepository) ArmsListings(ctx context.Context, items []string) ([]application.Listing, error) {
	if len(items) == 0 {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT l.id::text, l.no, l.company_id::text, l.city_id::text, l.item_code,
		        COALESCE(l.design_id::text, ''), l.quantity, l.sold, l.unit_price, l.status, l.created_at, l.updated_at,
		        l.closed_at
		   FROM company_listings l JOIN companies c ON c.id = l.company_id
		  WHERE l.item_code = ANY($1) AND l.status = 'open' AND c.status = 'active'
		  ORDER BY l.item_code, l.unit_price, l.no`, items)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading arms listings: %w", err)
	}
	defer rows.Close()
	var out []application.Listing
	for rows.Next() {
		var l application.Listing
		if err := rows.Scan(&l.ID, &l.No, &l.CompanyID, &l.CityID, &l.Item, &l.DesignID, &l.Qty, &l.Sold, &l.UnitPrice,
			&l.Status, &l.CreatedAt, &l.UpdatedAt, &l.ClosedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning an arms listing: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// AddAsset records a piece the state holds.
func (r *MilitaryRepository) AddAsset(ctx context.Context, a application.MilitaryAsset) error {
	var garrison any
	if a.GarrisonCityID != "" {
		garrison = a.GarrisonCityID
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO military_assets (piece_id, country_id, branch, class_code, status, garrison_city_id, procurement_id,
		        acquired_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, 'stationed', $5::uuid, $6::uuid, $7, $7)`,
		a.PieceID, a.CountryID, a.Branch, a.ClassCode, garrison, a.ProcurementID, a.AcquiredAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a military asset: %w", err)
	}
	return nil
}

// Assets lists a country's pieces with what each is.
func (r *MilitaryRepository) Assets(ctx context.Context, countryID string) ([]application.MilitaryAsset, error) {
	if !validUUID(countryID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT a.piece_id::text, a.country_id::text, a.branch, a.class_code, a.status,
		        COALESCE(a.garrison_city_id::text, ''), COALESCE(a.move_id::text, ''), a.procurement_id::text,
		        a.acquired_at, a.updated_at, p.item_code, COALESCE(p.design_id::text, ''), p.serial, p.quality
		   FROM military_assets a JOIN item_pieces p ON p.id = a.piece_id
		  WHERE a.country_id = $1::uuid
		  ORDER BY a.class_code, p.item_code, p.design_id NULLS FIRST, p.serial`, countryID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading military assets: %w", err)
	}
	defer rows.Close()
	var out []application.MilitaryAsset
	for rows.Next() {
		var a application.MilitaryAsset
		if err := rows.Scan(&a.PieceID, &a.CountryID, &a.Branch, &a.ClassCode, &a.Status, &a.GarrisonCityID, &a.MoveID,
			&a.ProcurementID, &a.AcquiredAt, &a.UpdatedAt, &a.Item, &a.DesignID, &a.Serial, &a.Quality); err != nil {
			return nil, fmt.Errorf("postgres: scanning a military asset: %w", err)
		}
		a.AcquiredAt, a.UpdatedAt = a.AcquiredAt.UTC(), a.UpdatedAt.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountByClass counts a country's pieces by class.
func (r *MilitaryRepository) CountByClass(ctx context.Context, countryID string) (map[string]int64, error) {
	out := map[string]int64{}
	if !validUUID(countryID) {
		return out, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT class_code, count(*) FROM military_assets WHERE country_id = $1::uuid GROUP BY class_code`, countryID)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting military assets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			code string
			n    int64
		)
		if err := rows.Scan(&code, &n); err != nil {
			return nil, fmt.Errorf("postgres: scanning a class count: %w", err)
		}
		out[code] = n
	}
	return out, rows.Err()
}

const moveColumns = `id::text, no, country_id::text, branch, to_city_id::text, item_code, COALESCE(design_id::text, ''),
       quantity, status, game_action_id::text, ordered_by::text, office_code, started_at, arrives_at, arrived_at`

func scanMove(row pgx.Row) (*application.MilitaryMove, error) {
	var m application.MilitaryMove
	if err := row.Scan(&m.ID, &m.No, &m.CountryID, &m.Branch, &m.ToCityID, &m.Item, &m.DesignID, &m.Qty, &m.Status,
		&m.GameActionID, &m.OrderedBy, &m.OfficeCode, &m.StartedAt, &m.ArrivesAt, &m.ArrivedAt); err != nil {
		return nil, err
	}
	m.StartedAt, m.ArrivesAt = m.StartedAt.UTC(), m.ArrivesAt.UTC()
	if m.ArrivedAt != nil {
		t := m.ArrivedAt.UTC()
		m.ArrivedAt = &t
	}
	return &m, nil
}

// StartMove records a move.
func (r *MilitaryRepository) StartMove(ctx context.Context, m application.MilitaryMove) (application.MilitaryMove, error) {
	var design any
	if m.DesignID != "" {
		design = m.DesignID
	}
	err := r.q.QueryRow(ctx,
		`INSERT INTO military_moves (id, country_id, branch, to_city_id, item_code, design_id, quantity, status,
		        game_action_id, ordered_by, office_code, started_at, arrives_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6::uuid, $7, 'moving', $8::uuid, $9::uuid, $10, $11, $12)
		 RETURNING no`,
		m.ID, m.CountryID, m.Branch, m.ToCityID, m.Item, design, m.Qty, m.GameActionID, m.OrderedBy, m.OfficeCode,
		m.StartedAt.UTC(), m.ArrivesAt.UTC()).Scan(&m.No)
	if err != nil {
		return m, fmt.Errorf("postgres: recording a move of forces: %w", err)
	}
	m.Status = application.MoveMoving
	return m, nil
}

// MarkMoving sets pieces on the way.
func (r *MilitaryRepository) MarkMoving(ctx context.Context, pieceIDs []string, moveID string, now time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE military_assets SET status = 'moving', move_id = $2::uuid, updated_at = $3
		  WHERE piece_id = ANY($1::uuid[])`, pieceIDs, moveID, now.UTC()); err != nil {
		return fmt.Errorf("postgres: setting forces on the move: %w", err)
	}
	return nil
}

// Move reads one move, locked.
func (r *MilitaryRepository) Move(ctx context.Context, id string) (*application.MilitaryMove, error) {
	if !validUUID(id) {
		return nil, application.ErrMoveNotFound
	}
	m, err := scanMove(r.q.QueryRow(ctx, `SELECT `+moveColumns+` FROM military_moves WHERE id = $1::uuid FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrMoveNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a move of forces: %w", err)
	}
	return m, nil
}

// FinishMove lands a move's pieces at its city.
func (r *MilitaryRepository) FinishMove(ctx context.Context, moveID string, now time.Time) (int64, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE military_moves SET status = 'arrived', arrived_at = $2 WHERE id = $1::uuid AND status = 'moving'`,
		moveID, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: landing a move of forces: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, nil
	}
	tag, err = r.q.Exec(ctx,
		`UPDATE military_assets a SET status = 'stationed', move_id = NULL, garrison_city_id = m.to_city_id, updated_at = $2
		   FROM military_moves m
		  WHERE m.id = $1::uuid AND a.move_id = m.id`, moveID, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: stationing forces: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Moves lists a country's moves under way.
func (r *MilitaryRepository) Moves(ctx context.Context, countryID string) ([]application.MilitaryMove, error) {
	if !validUUID(countryID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT `+moveColumns+` FROM military_moves WHERE country_id = $1::uuid AND status = 'moving' ORDER BY arrives_at, no`,
		countryID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading moves of forces: %w", err)
	}
	defer rows.Close()
	var out []application.MilitaryMove
	for rows.Next() {
		m, err := scanMove(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a move of forces: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}
