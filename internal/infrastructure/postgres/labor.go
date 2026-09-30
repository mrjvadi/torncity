package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// The labour market's rows (migration 0059_labor_market): jobs on the hiring
// board, construction shifts, and a player's experience. Part of the same
// repository as the village economy.

const laborJobsOneOpenIdx = "labor_jobs_one_open_idx"

var _ application.LaborRepository = (*SettlementTreasuryRepository)(nil)

const laborJobColumns = `id::text, settlement_id::text, building_id::text, kind, employer_kind, employer_id::text, wage,
	shifts_total, shifts_started, npc_crew, status, created_by::text, created_at, closed_at`

func scanLaborJob(row pgx.Row) (application.LaborJob, error) {
	var j application.LaborJob
	err := row.Scan(&j.ID, &j.SettlementID, &j.BuildingID, &j.Kind, &j.EmployerKind, &j.EmployerID, &j.Wage,
		&j.ShiftsTotal, &j.ShiftsStarted, &j.NPCCrew, &j.Status, &j.CreatedBy, &j.CreatedAt, &j.ClosedAt)
	return j, err
}

// PostJob writes an open job.
func (r *SettlementTreasuryRepository) PostJob(ctx context.Context, j application.LaborJob) error {
	_, err := r.q.Exec(ctx, `
		INSERT INTO labor_jobs (id, settlement_id, building_id, kind, employer_kind, employer_id, wage, shifts_total,
		       shifts_started, npc_crew, status, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9, 'open', $10, $11)`,
		j.ID, j.SettlementID, j.BuildingID, j.Kind, j.EmployerKind, j.EmployerID, j.Wage, j.ShiftsTotal, j.NPCCrew, j.CreatedBy, j.CreatedAt.UTC())
	if violates(err, sqlstateUniqueViolation, laborJobsOneOpenIdx) {
		return application.ErrJobExists
	}
	if err != nil {
		return fmt.Errorf("postgres: posting a job: %w", err)
	}
	return nil
}

func (r *SettlementTreasuryRepository) readJob(ctx context.Context, sql, id string) (*application.LaborJob, error) {
	j, err := scanLaborJob(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a job: %w", err)
	}
	return &j, nil
}

// LockJob reads a job under a row lock.
func (r *SettlementTreasuryRepository) LockJob(ctx context.Context, id string) (*application.LaborJob, error) {
	return r.readJob(ctx, `SELECT `+laborJobColumns+` FROM labor_jobs WHERE id = $1::uuid FOR UPDATE`, id)
}

// Job reads a job.
func (r *SettlementTreasuryRepository) Job(ctx context.Context, id string) (*application.LaborJob, error) {
	return r.readJob(ctx, `SELECT `+laborJobColumns+` FROM labor_jobs WHERE id = $1::uuid`, id)
}

// OpenJobs lists a settlement's open jobs.
func (r *SettlementTreasuryRepository) OpenJobs(ctx context.Context, settlementID string) ([]application.LaborJob, error) {
	rows, err := r.q.Query(ctx, `SELECT `+laborJobColumns+`
		FROM labor_jobs WHERE settlement_id = $1::uuid AND status = 'open' ORDER BY created_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing jobs: %w", err)
	}
	defer rows.Close()
	var out []application.LaborJob
	for rows.Next() {
		j, err := scanLaborJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// JobOfBuilding is the building's open job, or nil.
func (r *SettlementTreasuryRepository) JobOfBuilding(ctx context.Context, buildingID string) (*application.LaborJob, error) {
	j, err := scanLaborJob(r.q.QueryRow(ctx, `SELECT `+laborJobColumns+`
		FROM labor_jobs WHERE building_id = $1::uuid AND status = 'open'`, buildingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the building's job: %w", err)
	}
	return &j, nil
}

// CountStarted adds one started shift to a job.
func (r *SettlementTreasuryRepository) CountStarted(ctx context.Context, jobID string) error {
	if _, err := r.q.Exec(ctx, `UPDATE labor_jobs SET shifts_started = shifts_started + 1 WHERE id = $1::uuid`, jobID); err != nil {
		return fmt.Errorf("postgres: counting a job's shift: %w", err)
	}
	return nil
}

// UpdateJob sets an open job's wage, budget and crew.
func (r *SettlementTreasuryRepository) UpdateJob(ctx context.Context, id string, wage int64, shiftsTotal, npcCrew int) error {
	if _, err := r.q.Exec(ctx, `
		UPDATE labor_jobs SET wage = $2, shifts_total = GREATEST($3, shifts_started), npc_crew = $4
		 WHERE id = $1::uuid AND status = 'open'`, id, wage, shiftsTotal, npcCrew); err != nil {
		return fmt.Errorf("postgres: updating a job: %w", err)
	}
	return nil
}

// CloseJobOfBuilding closes the open job of a building.
func (r *SettlementTreasuryRepository) CloseJobOfBuilding(ctx context.Context, buildingID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `
		UPDATE labor_jobs SET status = 'closed', closed_at = $2 WHERE building_id = $1::uuid AND status = 'open'`,
		buildingID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: closing the building's job: %w", err)
	}
	return nil
}

// CloseJob closes one job.
func (r *SettlementTreasuryRepository) CloseJob(ctx context.Context, id string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE labor_jobs SET status = 'closed', closed_at = $2 WHERE id = $1::uuid AND status = 'open'`,
		id, at.UTC()); err != nil {
		return fmt.Errorf("postgres: closing a job: %w", err)
	}
	return nil
}

// StartLaborShift inserts a working shift of the labour market.
func (r *SettlementTreasuryRepository) StartLaborShift(ctx context.Context, s application.SettlementShift) error {
	var player, job, payer any
	if s.PlayerID != "" {
		player = s.PlayerID
	}
	if s.JobID != "" {
		job = s.JobID
	}
	if s.PayerID != "" {
		payer = s.PayerID
	}
	_, err := r.q.Exec(ctx, `
		INSERT INTO settlement_shifts
		       (id, settlement_id, building_id, player_id, status, wage, wage_paid, produced, consumed, game_action_id,
		        started_at, finish_at, kind, job_id, worker_kind, work_points, payer_kind, payer_id, fee)
		VALUES ($1, $2, $3, $4::uuid, 'working', $5, 0, '{}'::jsonb, '{}'::jsonb, $6, $7, $8, $9, $10::uuid, $11, $12, $13, $14::uuid, 0)`,
		s.ID, s.SettlementID, s.BuildingID, player, s.Wage, s.GameActionID, s.StartedAt.UTC(), s.FinishAt.UTC(),
		s.Kind, job, s.WorkerKind, s.WorkPoints, s.PayerKind, payer)
	if violates(err, sqlstateUniqueViolation, settlementShiftsOneWorkingIdx) {
		return application.ErrAlreadyWorking
	}
	if err != nil {
		return fmt.Errorf("postgres: starting a labour shift: %w", err)
	}
	return nil
}

// FinishLaborShift ends a working shift, exactly once.
func (r *SettlementTreasuryRepository) FinishLaborShift(ctx context.Context, id string, wagePaid, fee int64, ledgerTransactionID string, at time.Time) (bool, error) {
	var ledgerTx any
	if ledgerTransactionID != "" {
		ledgerTx = ledgerTransactionID
	}
	tag, err := r.q.Exec(ctx, `
		UPDATE settlement_shifts
		   SET status = 'done', wage_paid = $2, fee = $3, ledger_transaction_id = $4::uuid, finished_at = $5
		 WHERE id = $1::uuid AND status = 'working'`, id, wagePaid, fee, ledgerTx, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: finishing a labour shift: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SiteShifts lists a building's construction shifts in progress.
func (r *SettlementTreasuryRepository) SiteShifts(ctx context.Context, buildingID string) ([]application.SettlementShift, error) {
	rows, err := r.q.Query(ctx, `SELECT `+shiftColumns+`
		FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'working' AND kind = 'construction'
		ORDER BY started_at, id`, buildingID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing site shifts: %w", err)
	}
	defer rows.Close()
	var out []application.SettlementShift
	for rows.Next() {
		s, err := scanShift(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// WorkingCount is how many shifts of a settlement are in progress, and how many
// are NPC labourers.
func (r *SettlementTreasuryRepository) WorkingCount(ctx context.Context, settlementID string) (int64, int64, error) {
	var all, npc int64
	if err := r.q.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE worker_kind = 'npc')
		  FROM settlement_shifts WHERE settlement_id = $1::uuid AND status = 'working'`, settlementID).Scan(&all, &npc); err != nil {
		return 0, 0, fmt.Errorf("postgres: counting working shifts: %w", err)
	}
	return all, npc, nil
}

// Vacancies is how many shifts open jobs of a settlement still pay for.
func (r *SettlementTreasuryRepository) Vacancies(ctx context.Context, settlementID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `
		SELECT COALESCE(SUM(shifts_total - shifts_started), 0)::bigint
		  FROM labor_jobs WHERE settlement_id = $1::uuid AND status = 'open'`, settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting vacancies: %w", err)
	}
	return n, nil
}

// AddWork adds points to a building under construction.
func (r *SettlementTreasuryRepository) AddWork(ctx context.Context, buildingID string, points int64) (int64, int64, bool, error) {
	var done, required int64
	err := r.q.QueryRow(ctx, `
		UPDATE settlement_buildings SET work_done = LEAST(work_required, work_done + $2)
		 WHERE id = $1::uuid AND status = 'building' AND work_required > 0
		RETURNING work_done, work_required`, buildingID, points).Scan(&done, &required)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("postgres: adding work: %w", err)
	}
	return done, required, true, nil
}

// Worker reads a player's experience.
func (r *SettlementTreasuryRepository) Worker(ctx context.Context, playerID string) (application.LaborWorker, error) {
	w := application.LaborWorker{PlayerID: playerID}
	err := r.q.QueryRow(ctx, `SELECT shifts, earned FROM labor_workers WHERE player_id = $1::uuid`, playerID).Scan(&w.Shifts, &w.Earned)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, nil
	}
	if err != nil {
		return w, fmt.Errorf("postgres: reading a worker: %w", err)
	}
	return w, nil
}

// RecordWorked adds a finished shift to a player's experience.
func (r *SettlementTreasuryRepository) RecordWorked(ctx context.Context, playerID string, earned int64, at time.Time) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO labor_workers (player_id, shifts, earned, updated_at) VALUES ($1, 1, $2, $3)
		ON CONFLICT (player_id) DO UPDATE SET shifts = labor_workers.shifts + 1, earned = labor_workers.earned + $2, updated_at = $3`,
		playerID, earned, at.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a worked shift: %w", err)
	}
	return nil
}
