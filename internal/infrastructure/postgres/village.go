package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file implements K2/W5 (docs/adr/0031-knowledge-and-village-
// progression.md sections 4, 5, 6; migrations 0045, 0046): a settlement's
// own knowledge, literacy, research and buildings under construction.

const (
	settlementResearchOneRunningIdx = "settlement_research_one_running_idx"
	settlementResearchOnceIdx       = "settlement_research_once_idx"
	settlementBuildingsLotUnique    = "settlement_buildings_lot_unique"
)

// SettlementKnowledgeRepository is bound to one transaction.
type SettlementKnowledgeRepository struct{ q querier }

var _ application.SettlementKnowledgeRepository = (*SettlementKnowledgeRepository)(nil)

// Owned lists everything the settlement holds.
func (r *SettlementKnowledgeRepository) Owned(ctx context.Context, settlementID string) ([]application.SettlementKnowledgeOwned, error) {
	rows, err := r.q.Query(ctx,
		`SELECT settlement_id::text, code, acquired_via, acquired_at
		   FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading owned knowledge: %w", err)
	}
	defer rows.Close()
	var out []application.SettlementKnowledgeOwned
	for rows.Next() {
		var o application.SettlementKnowledgeOwned
		if err := rows.Scan(&o.SettlementID, &o.Code, &o.AcquiredVia, &o.AcquiredAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning owned knowledge: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Grant records the settlement as holding g.Code, idempotently.
func (r *SettlementKnowledgeRepository) Grant(ctx context.Context, g application.SettlementKnowledgeOwned) (bool, error) {
	id, err := newUUID()
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		 ON CONFLICT (settlement_id, code) DO NOTHING`,
		id, g.SettlementID, g.Code, g.AcquiredVia, g.AcquiredAt)
	if err != nil {
		return false, fmt.Errorf("postgres: granting knowledge %s: %w", g.Code, err)
	}
	return tag.RowsAffected() > 0, nil
}

func scanSettlementResearch(row pgx.Row) (application.SettlementResearch, error) {
	var (
		rs          application.SettlementResearch
		ledgerTxID  *string
		completedAt *time.Time
	)
	err := row.Scan(&rs.ID, &rs.SettlementID, &rs.Code, &rs.Status, &rs.Cost, &ledgerTxID, &rs.GameActionID,
		&rs.StartedBy, &rs.StartedAt, &rs.FinishAt, &completedAt)
	if ledgerTxID != nil {
		rs.LedgerTransactionID = *ledgerTxID
	}
	rs.CompletedAt = completedAt
	return rs, err
}

const selectSettlementResearchColumns = `id::text, settlement_id::text, code, status, cost, ledger_transaction_id::text,
	game_action_id::text, started_by::text, started_at, finish_at, completed_at`

// RunningResearch returns the settlement's one running project, or nil.
func (r *SettlementKnowledgeRepository) RunningResearch(ctx context.Context, settlementID string) (*application.SettlementResearch, error) {
	rs, err := scanSettlementResearch(r.q.QueryRow(ctx,
		`SELECT `+selectSettlementResearchColumns+` FROM settlement_research
		  WHERE settlement_id = $1::uuid AND status = 'running'`, settlementID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading running research: %w", err)
	}
	return &rs, nil
}

// StartResearch writes a new running research row.
func (r *SettlementKnowledgeRepository) StartResearch(ctx context.Context, in application.SettlementResearch) error {
	id, err := ensureID(in.ID)
	if err != nil {
		return err
	}
	var ledgerTxID any
	if in.LedgerTransactionID != "" {
		ledgerTxID = in.LedgerTransactionID
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO settlement_research (id, settlement_id, code, status, cost, ledger_transaction_id,
		        game_action_id, started_by, started_at, finish_at)
		 VALUES ($1::uuid, $2::uuid, $3, 'running', $4, $5::uuid, $6::uuid, $7::uuid, $8, $9)`,
		id, in.SettlementID, in.Code, in.Cost, ledgerTxID, in.GameActionID, in.StartedBy, in.StartedAt, in.FinishAt)
	switch {
	case violates(err, sqlstateUniqueViolation, settlementResearchOneRunningIdx):
		return application.ErrSettlementResearchBusy
	case violates(err, sqlstateUniqueViolation, settlementResearchOnceIdx):
		return application.ErrSettlementAlreadyResearched
	case err != nil:
		return fmt.Errorf("postgres: starting research %s: %w", in.Code, err)
	}
	return nil
}

// Research returns one research row by id.
func (r *SettlementKnowledgeRepository) Research(ctx context.Context, id string) (*application.SettlementResearch, error) {
	rs, err := scanSettlementResearch(r.q.QueryRow(ctx,
		`SELECT `+selectSettlementResearchColumns+` FROM settlement_research WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrSettlementResearchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading research %s: %w", id, err)
	}
	return &rs, nil
}

// FinishResearch marks a running research done, idempotently.
func (r *SettlementKnowledgeRepository) FinishResearch(ctx context.Context, id string, at time.Time) error {
	_, err := r.q.Exec(ctx,
		`UPDATE settlement_research SET status = 'done', completed_at = $2
		  WHERE id = $1::uuid AND status = 'running'`, id, at)
	if err != nil {
		return fmt.Errorf("postgres: finishing research %s: %w", id, err)
	}
	return nil
}

// EnsureLiteracy creates the settlement's literacy row at 0 if it does not
// exist yet.
func (r *SettlementKnowledgeRepository) EnsureLiteracy(ctx context.Context, settlementID string, at time.Time) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO settlement_literacy (settlement_id, literacy_share_bps, updated_at)
		 VALUES ($1::uuid, 0, $2)
		 ON CONFLICT (settlement_id) DO NOTHING`, settlementID, at)
	if err != nil {
		return fmt.Errorf("postgres: creating literacy row: %w", err)
	}
	return nil
}

// Literacy returns the settlement's own literacy_share_bps and its pending
// teach action id.
func (r *SettlementKnowledgeRepository) Literacy(ctx context.Context, settlementID string) (int, string, error) {
	var (
		shareBPS int
		pending  *string
	)
	err := r.q.QueryRow(ctx,
		`SELECT literacy_share_bps, pending_action_id::text FROM settlement_literacy WHERE settlement_id = $1::uuid`,
		settlementID).Scan(&shareBPS, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("postgres: reading literacy: %w", err)
	}
	if pending != nil {
		return shareBPS, *pending, nil
	}
	return shareBPS, "", nil
}

// AdvanceLiteracy applies one diffusion step's result and stamps the next
// teach tick's fence.
func (r *SettlementKnowledgeRepository) AdvanceLiteracy(ctx context.Context, settlementID string, newShareBPS int,
	nextActionID string, at time.Time,
) error {
	var next any
	if nextActionID != "" {
		next = nextActionID
	}
	_, err := r.q.Exec(ctx,
		`UPDATE settlement_literacy SET literacy_share_bps = $2, pending_action_id = $3::uuid, updated_at = $4
		  WHERE settlement_id = $1::uuid`, settlementID, newShareBPS, next, at)
	if err != nil {
		return fmt.Errorf("postgres: advancing literacy: %w", err)
	}
	return nil
}

// HoldersShareBPS reads code's holders/total_settlements from the
// periodically refreshed aggregate.
func (r *SettlementKnowledgeRepository) HoldersShareBPS(ctx context.Context, code string) (int64, error) {
	var holders, total int64
	err := r.q.QueryRow(ctx,
		`SELECT holders, total_settlements FROM settlement_knowledge_holder_counts WHERE code = $1`, code).
		Scan(&holders, &total)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: reading holder counts for %s: %w", code, err)
	}
	if total <= 0 {
		return 0, nil
	}
	return holders * 10_000 / total, nil
}

// RefreshHolderCounts recomputes the whole aggregate from
// settlement_knowledge_owned in one statement. Idempotent, safe from any
// number of replicas at once: each run is a plain upsert of a derived
// value, never a read-modify-write of a counter.
func (r *SettlementKnowledgeRepository) RefreshHolderCounts(ctx context.Context, at time.Time) error {
	_, err := r.q.Exec(ctx, `
		INSERT INTO settlement_knowledge_holder_counts (code, holders, total_settlements, refreshed_at)
		SELECT sko.code, COUNT(DISTINCT sko.settlement_id),
		       (SELECT COUNT(*) FROM cities WHERE origin = 'founded'),
		       $1
		  FROM settlement_knowledge_owned sko
		 GROUP BY sko.code
		ON CONFLICT (code) DO UPDATE
		   SET holders = EXCLUDED.holders, total_settlements = EXCLUDED.total_settlements, refreshed_at = EXCLUDED.refreshed_at`,
		at)
	if err != nil {
		return fmt.Errorf("postgres: refreshing knowledge holder counts: %w", err)
	}
	return nil
}

// SettlementBuildingRepository is bound to one transaction.
type SettlementBuildingRepository struct{ q querier }

var _ application.SettlementBuildingRepository = (*SettlementBuildingRepository)(nil)

const selectSettlementBuildingColumns = `id::text, settlement_id::text, type_code, lot_x, lot_y, status,
	queued_at, completed_at, demolished_at, cancelled_at, rotated, finish_at, damage_bps, work_required, work_done, COALESCE(employer_player_id::text, '')`

func scanSettlementBuilding(row pgx.Row) (application.SettlementBuildingInstance, error) {
	var b application.SettlementBuildingInstance
	err := row.Scan(&b.ID, &b.SettlementID, &b.TypeCode, &b.LotX, &b.LotY, &b.Status,
		&b.QueuedAt, &b.CompletedAt, &b.DemolishedAt, &b.CancelledAt, &b.Rotated, &b.FinishAt, &b.DamageBPS, &b.WorkRequired, &b.WorkDone, &b.EmployerPlayerID)
	return b, err
}

// List returns every building a settlement has, any status.
func (r *SettlementBuildingRepository) List(ctx context.Context, settlementID string) ([]application.SettlementBuildingInstance, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+selectSettlementBuildingColumns+` FROM settlement_buildings WHERE settlement_id = $1::uuid`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing settlement buildings: %w", err)
	}
	defer rows.Close()
	var out []application.SettlementBuildingInstance
	for rows.Next() {
		b, err := scanSettlementBuilding(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning settlement building: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Get returns one building by id.
func (r *SettlementBuildingRepository) Get(ctx context.Context, id string) (*application.SettlementBuildingInstance, error) {
	b, err := scanSettlementBuilding(r.q.QueryRow(ctx,
		`SELECT `+selectSettlementBuildingColumns+` FROM settlement_buildings WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrBuildingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading building %s: %w", id, err)
	}
	return &b, nil
}

// RunningCount is how many of the settlement's buildings are status
// "building" right now.
func (r *SettlementBuildingRepository) RunningCount(ctx context.Context, settlementID string) (int, error) {
	var n int
	err := r.q.QueryRow(ctx,
		`SELECT COUNT(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND status = 'building'`,
		settlementID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting running builds: %w", err)
	}
	return n, nil
}

// Place writes a new building row, status "building" - or "complete" (with
// its completion time) for one the game lays itself, an automatic road, which
// has no construction to wait for.
func (r *SettlementBuildingRepository) Place(ctx context.Context, b application.SettlementBuildingInstance) error {
	id, err := ensureID(b.ID)
	if err != nil {
		return err
	}
	status, completedAt := "building", (*time.Time)(nil)
	if b.Status == "complete" {
		status, completedAt = "complete", b.CompletedAt
		if completedAt == nil {
			at := b.QueuedAt
			completedAt = &at
		}
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at,
		        rotated, finish_at, work_required, employer_player_id)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $9, $6, $10, $7, $8, $11, NULLIF($12, '')::uuid)`,
		id, b.SettlementID, b.TypeCode, b.LotX, b.LotY, b.QueuedAt, b.Rotated, b.FinishAt, status, completedAt, b.WorkRequired, b.EmployerPlayerID)
	if violates(err, sqlstateUniqueViolation, settlementBuildingsLotUnique) {
		return application.ErrLotOccupied
	}
	if err != nil {
		return fmt.Errorf("postgres: placing %s: %w", b.TypeCode, err)
	}
	return nil
}

// Complete marks a building complete, idempotently.
func (r *SettlementBuildingRepository) Complete(ctx context.Context, id string, at time.Time) error {
	_, err := r.q.Exec(ctx,
		`UPDATE settlement_buildings SET status = 'complete', completed_at = $2
		  WHERE id = $1::uuid AND status = 'building'`, id, at)
	if err != nil {
		return fmt.Errorf("postgres: completing building %s: %w", id, err)
	}
	return nil
}

// Demolish marks a complete building demolished.
func (r *SettlementBuildingRepository) Demolish(ctx context.Context, id string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE settlement_buildings SET status = 'demolished', demolished_at = $2
		  WHERE id = $1::uuid AND status = 'complete'`, id, at)
	if err != nil {
		return fmt.Errorf("postgres: demolishing building %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrBuildingNotDemolishable
	}
	return nil
}

// Cancel marks a building under construction cancelled.
func (r *SettlementBuildingRepository) Cancel(ctx context.Context, id string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE settlement_buildings SET status = 'cancelled', cancelled_at = $2
		  WHERE id = $1::uuid AND status IN ('queued', 'building')`, id, at)
	if err != nil {
		return fmt.Errorf("postgres: cancelling building %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrBuildingNotCancellable
	}
	return nil
}
