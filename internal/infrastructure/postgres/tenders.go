package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// TenderRepository is the public tenders, their bids, stages and debarments (migration 0154; docs/adr/0076 10), bound to one transaction.
type TenderRepository struct{ q querier }

var _ application.TenderRepository = (*TenderRepository)(nil)

const tenderRowColumns = `id::text, payer_kind, payer_id::text, settlement_id::text, building_id::text, posted_by::text, COALESCE(posted_office::text, ''), rule, status,
	COALESCE(bid_id::text, ''), COALESCE(company_id::text, ''), budget_cap, bond_bps, retention_bps, COALESCE(price, 0), retention_held, defects_days, due_days, stages_paid,
	opens_at, closes_at, awarded_at, due_at, last_stage_at, closed_at`

func scanTenderRow(row pgx.Row) (*application.Tender, error) {
	var t application.Tender
	if err := row.Scan(&t.ID, &t.PayerKind, &t.PayerID, &t.SettlementID, &t.BuildingID, &t.PostedBy, &t.PostedOffice, &t.Rule, &t.Status,
		&t.BidID, &t.CompanyID, &t.BudgetCap, &t.BondBPS, &t.RetentionBPS, &t.Price, &t.RetentionHeld, &t.DefectsDays, &t.DueDays, &t.StagesPaid,
		&t.OpensAt, &t.ClosesAt, &t.AwardedAt, &t.DueAt, &t.LastStageAt, &t.ClosedAt); err != nil {
		return nil, err
	}
	t.OpensAt, t.ClosesAt = t.OpensAt.UTC(), t.ClosesAt.UTC()
	t.AwardedAt, t.DueAt, t.LastStageAt, t.ClosedAt = utcPtr(t.AwardedAt), utcPtr(t.DueAt), utcPtr(t.LastStageAt), utcPtr(t.ClosedAt)
	return &t, nil
}

func (r *TenderRepository) many(ctx context.Context, sql string, args ...any) ([]application.Tender, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing tenders: %w", err)
	}
	defer rows.Close()
	var out []application.Tender
	for rows.Next() {
		t, err := scanTenderRow(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a tender: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Insert posts a tender; false when the building has an open or running one.
func (r *TenderRepository) Insert(ctx context.Context, t application.Tender) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO tenders (id, payer_kind, payer_id, settlement_id, building_id, posted_by, posted_office, budget_cap, rule, bond_bps, retention_bps, defects_days,
		opens_at, closes_at, due_days, status) VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8, $9, $10, $11, $12, $13, $14, $15, 'open') ON CONFLICT DO NOTHING`,
		t.ID, t.PayerKind, t.PayerID, t.SettlementID, t.BuildingID, t.PostedBy, nullText(t.PostedOffice), t.BudgetCap, t.Rule, t.BondBPS, t.RetentionBPS, t.DefectsDays,
		t.OpensAt.UTC(), t.ClosesAt.UTC(), t.DueDays)
	if err != nil {
		return false, fmt.Errorf("postgres: posting a tender: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ByID reads a tender, nil for none.
func (r *TenderRepository) ByID(ctx context.Context, id string, lock bool) (*application.Tender, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + tenderRowColumns + ` FROM tenders WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	t, err := scanTenderRow(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a tender: %w", err)
	}
	return t, nil
}

// ByBuilding is the open or running tender on a building, nil for none.
func (r *TenderRepository) ByBuilding(ctx context.Context, buildingID string) (*application.Tender, error) {
	if !validUUID(buildingID) {
		return nil, nil
	}
	t, err := scanTenderRow(r.q.QueryRow(ctx, `SELECT `+tenderRowColumns+` FROM tenders WHERE building_id = $1::uuid AND status IN ('open', 'awarded')`, buildingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a tender: %w", err)
	}
	return t, nil
}

// OfSettlement lists a settlement's tenders, newest first.
func (r *TenderRepository) OfSettlement(ctx context.Context, settlementID string, limit int) ([]application.Tender, error) {
	return r.many(ctx, `SELECT `+tenderRowColumns+` FROM tenders WHERE settlement_id = $1::uuid OR payer_id = $1::uuid ORDER BY opens_at DESC, id LIMIT $2`, settlementID, limit)
}

// OfCompany lists the tenders a company won, newest first.
func (r *TenderRepository) OfCompany(ctx context.Context, companyID string, limit int) ([]application.Tender, error) {
	return r.many(ctx, `SELECT `+tenderRowColumns+` FROM tenders WHERE company_id = $1::uuid ORDER BY opens_at DESC, id LIMIT $2`, companyID, limit)
}

// Open lists the tenders still taking bids, newest first.
func (r *TenderRepository) Open(ctx context.Context, limit int) ([]application.Tender, error) {
	return r.many(ctx, `SELECT `+tenderRowColumns+` FROM tenders WHERE status = 'open' ORDER BY opens_at DESC, id LIMIT $1`, limit)
}

// AddBid writes a bid; false when the company bid on this tender already.
func (r *TenderRepository) AddBid(ctx context.Context, b application.TenderBid) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO tender_bids (id, tender_id, company_id, bid_by, price, days, record, bond, at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9) ON CONFLICT DO NOTHING`,
		b.ID, b.TenderID, b.CompanyID, b.BidBy, b.Price, b.Days, b.Record, b.Bond, b.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: placing a bid: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Bids lists the bids of a tender, oldest first.
func (r *TenderRepository) Bids(ctx context.Context, tenderID string) ([]application.TenderBid, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, tender_id::text, company_id::text, bid_by::text, status, price, bond, days, record, at FROM tender_bids WHERE tender_id = $1::uuid ORDER BY at, id`, tenderID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing bids: %w", err)
	}
	defer rows.Close()
	var out []application.TenderBid
	for rows.Next() {
		var b application.TenderBid
		if err := rows.Scan(&b.ID, &b.TenderID, &b.CompanyID, &b.BidBy, &b.Status, &b.Price, &b.Bond, &b.Days, &b.Record, &b.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a bid: %w", err)
		}
		b.At = b.At.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBids moves every bid of a tender from one status to another.
func (r *TenderRepository) SetBids(ctx context.Context, tenderID, from, to string) error {
	if _, err := r.q.Exec(ctx, `UPDATE tender_bids SET status = $3 WHERE tender_id = $1::uuid AND status = $2`, tenderID, from, to); err != nil {
		return fmt.Errorf("postgres: settling bids: %w", err)
	}
	return nil
}

// SetBid sets one bid's status.
func (r *TenderRepository) SetBid(ctx context.Context, bidID, status string) error {
	if _, err := r.q.Exec(ctx, `UPDATE tender_bids SET status = $2 WHERE id = $1::uuid`, bidID, status); err != nil {
		return fmt.Errorf("postgres: setting a bid: %w", err)
	}
	return nil
}

// Award moves an open tender to awarded in one conditional update: true for exactly one caller.
func (r *TenderRepository) Award(ctx context.Context, tenderID, bidID, companyID string, price int64, at, dueAt time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE tenders SET status = 'awarded', bid_id = $2::uuid, company_id = $3::uuid, price = $4, awarded_at = $5, due_at = $6 WHERE id = $1::uuid AND status = 'open'`,
		tenderID, bidID, companyID, price, at.UTC(), dueAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: awarding a tender: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetStatus moves a tender from one status to another; false when it was not in `from`.
func (r *TenderRepository) SetStatus(ctx context.Context, tenderID, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE tenders SET status = $3, closed_at = CASE WHEN $3 IN ('done', 'failed', 'cancelled') THEN $4::timestamptz ELSE closed_at END WHERE id = $1::uuid AND status = $2`,
		tenderID, from, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: moving a tender: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Stages lists the stages paid.
func (r *TenderRepository) Stages(ctx context.Context, tenderID string) ([]application.TenderStage, error) {
	rows, err := r.q.Query(ctx, `SELECT tender_id::text, stage, paid, held, COALESCE(ledger_tx::text, ''), at FROM tender_stages WHERE tender_id = $1::uuid ORDER BY stage`, tenderID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing stages: %w", err)
	}
	defer rows.Close()
	var out []application.TenderStage
	for rows.Next() {
		var s application.TenderStage
		if err := rows.Scan(&s.TenderID, &s.Stage, &s.Paid, &s.Held, &s.LedgerTx, &s.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a stage: %w", err)
		}
		s.At = s.At.UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// PayStage writes a stage; false when it was written already. A written stage also moves the counters of the tender.
func (r *TenderRepository) PayStage(ctx context.Context, s application.TenderStage) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO tender_stages (tender_id, stage, paid, held, ledger_tx, at) VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6) ON CONFLICT DO NOTHING`,
		s.TenderID, s.Stage, s.Paid, s.Held, nullText(s.LedgerTx), s.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: paying a stage: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if _, err := r.q.Exec(ctx, `UPDATE tenders SET stages_paid = GREATEST(stages_paid, $2), retention_held = retention_held + $3, last_stage_at = $4 WHERE id = $1::uuid`,
		s.TenderID, s.Stage, s.Held, s.At.UTC()); err != nil {
		return false, fmt.Errorf("postgres: counting a stage: %w", err)
	}
	return true, nil
}

// AddRetention is unused by stages (PayStage counts them); it exists for corrections by the admin tool.
func (r *TenderRepository) AddRetention(ctx context.Context, tenderID string, held int64, at time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE tenders SET retention_held = retention_held + $2 WHERE id = $1::uuid`, tenderID, held)
	return err
}

// ClearRetention writes that the held retention was released.
func (r *TenderRepository) ClearRetention(ctx context.Context, tenderID string) error {
	if _, err := r.q.Exec(ctx, `UPDATE tenders SET retention_held = 0 WHERE id = $1::uuid`, tenderID); err != nil {
		return fmt.Errorf("postgres: clearing a retention: %w", err)
	}
	return nil
}

// DebarredUntil is when a company's debarment ends; zero for none.
func (r *TenderRepository) DebarredUntil(ctx context.Context, companyID string) (time.Time, error) {
	var until time.Time
	err := r.q.QueryRow(ctx, `SELECT until FROM tender_debarments WHERE company_id = $1::uuid`, companyID).Scan(&until)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading a debarment: %w", err)
	}
	return until.UTC(), nil
}

// Debar keeps a company from public tenders until a day; a longer standing debarment is kept.
func (r *TenderRepository) Debar(ctx context.Context, companyID string, until time.Time, reason, tenderID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO tender_debarments (company_id, until, reason, tender_id, at) VALUES ($1::uuid, $2, $3, $4::uuid, $5)
		ON CONFLICT (company_id) DO UPDATE SET until = GREATEST(tender_debarments.until, EXCLUDED.until), reason = EXCLUDED.reason, tender_id = EXCLUDED.tender_id, at = EXCLUDED.at`,
		companyID, until.UTC(), reason, nullText(tenderID), at.UTC()); err != nil {
		return fmt.Errorf("postgres: debarring a company: %w", err)
	}
	return nil
}

// Record is how many works the company finished (its tenders that ended 'done').
func (r *TenderRepository) Record(ctx context.Context, companyID string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM tenders WHERE company_id = $1::uuid AND status = 'done'`, companyID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting a company's works: %w", err)
	}
	return n, nil
}
