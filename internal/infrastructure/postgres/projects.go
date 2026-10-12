package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// ProjectRepository is the special projects, their stock, pledges, shifts and contracts (migration 0155; docs/adr/0077 4), bound to one transaction.
type ProjectRepository struct{ q querier }

var _ application.ProjectRepository = (*ProjectRepository)(nil)

const projColumns = `id::text, kind, settlement_id::text, started_by::text, status, stages, stage, stage_hours, stage_started_at, delayed_until, started_at, finished_at, cancelled_at`

func scanProj(row pgx.Row) (*application.SpecialProject, error) {
	var p application.SpecialProject
	if err := row.Scan(&p.ID, &p.Kind, &p.SettlementID, &p.StartedBy, &p.Status, &p.Stages, &p.Stage, &p.StageHours, &p.StageStartedAt, &p.DelayedUntil,
		&p.StartedAt, &p.FinishedAt, &p.CancelledAt); err != nil {
		return nil, err
	}
	p.StartedAt = p.StartedAt.UTC()
	p.StageStartedAt, p.DelayedUntil, p.FinishedAt, p.CancelledAt = utcPtr(p.StageStartedAt), utcPtr(p.DelayedUntil), utcPtr(p.FinishedAt), utcPtr(p.CancelledAt)
	return &p, nil
}

func (r *ProjectRepository) projects(ctx context.Context, sql string, args ...any) ([]application.SpecialProject, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing projects: %w", err)
	}
	defer rows.Close()
	var out []application.SpecialProject
	for rows.Next() {
		p, err := scanProj(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a project: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Insert starts a project; false when the id exists.
func (r *ProjectRepository) Insert(ctx context.Context, p application.SpecialProject) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO special_projects (id, kind, settlement_id, started_by, stages, stage, stage_hours, status, started_at)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, 1, 0, 'active', $6) ON CONFLICT DO NOTHING`,
		p.ID, p.Kind, p.SettlementID, p.StartedBy, p.Stages, p.StartedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: starting a project: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ByID reads a project, nil for none.
func (r *ProjectRepository) ByID(ctx context.Context, id string, lock bool) (*application.SpecialProject, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + projColumns + ` FROM special_projects WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	p, err := scanProj(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a project: %w", err)
	}
	return p, nil
}

// OfSettlement lists a settlement's projects, newest first.
func (r *ProjectRepository) OfSettlement(ctx context.Context, settlementID string, limit int) ([]application.SpecialProject, error) {
	return r.projects(ctx, `SELECT `+projColumns+` FROM special_projects WHERE settlement_id = $1::uuid ORDER BY started_at DESC, id LIMIT $2`, settlementID, limit)
}

// CountActive is how many projects of the settlement are still being built.
func (r *ProjectRepository) CountActive(ctx context.Context, settlementID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM special_projects WHERE settlement_id = $1::uuid AND status = 'active'`, settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting projects: %w", err)
	}
	return n, nil
}

// Finished lists the finished works of a settlement, oldest first.
func (r *ProjectRepository) Finished(ctx context.Context, settlementID string) ([]application.SpecialProject, error) {
	return r.projects(ctx, `SELECT `+projColumns+` FROM special_projects WHERE settlement_id = $1::uuid AND status = 'finished' ORDER BY finished_at, id`, settlementID)
}

// AddHours adds crew-hours to the current stage.
func (r *ProjectRepository) AddHours(ctx context.Context, id string, hours int64) error {
	_, err := r.q.Exec(ctx, `UPDATE special_projects SET stage_hours = stage_hours + $2 WHERE id = $1::uuid AND status = 'active'`, id, hours)
	if err != nil {
		return fmt.Errorf("postgres: adding hours: %w", err)
	}
	return nil
}

// StartStage marks the current stage begun, once.
func (r *ProjectRepository) StartStage(ctx context.Context, id string, at time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE special_projects SET stage_started_at = $2 WHERE id = $1::uuid AND status = 'active' AND stage_started_at IS NULL`, id, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: starting a stage: %w", err)
	}
	return nil
}

// SetDelay sets the instant before which the stage cannot be accepted (never earlier than one standing).
func (r *ProjectRepository) SetDelay(ctx context.Context, id string, until time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE special_projects SET delayed_until = GREATEST(COALESCE(delayed_until, $2), $2) WHERE id = $1::uuid AND status = 'active'`, id, until.UTC())
	if err != nil {
		return fmt.Errorf("postgres: delaying a stage: %w", err)
	}
	return nil
}

// CompleteStage finishes the named stage in one conditional update (the stage number is the fence) and writes the stage row.
func (r *ProjectRepository) CompleteStage(ctx context.Context, id string, stage int, hours int64, finish bool, at time.Time) (bool, error) {
	var tag int64
	if finish {
		t, err := r.q.Exec(ctx, `UPDATE special_projects SET status = 'finished', finished_at = $3, stage_hours = 0, stage_started_at = NULL, delayed_until = NULL
			WHERE id = $1::uuid AND status = 'active' AND stage = $2 AND stage = stages`, id, stage, at.UTC())
		if err != nil {
			return false, fmt.Errorf("postgres: finishing a project: %w", err)
		}
		tag = t.RowsAffected()
	} else {
		t, err := r.q.Exec(ctx, `UPDATE special_projects SET stage = stage + 1, stage_hours = 0, stage_started_at = NULL, delayed_until = NULL
			WHERE id = $1::uuid AND status = 'active' AND stage = $2 AND stage < stages`, id, stage)
		if err != nil {
			return false, fmt.Errorf("postgres: finishing a stage: %w", err)
		}
		tag = t.RowsAffected()
	}
	if tag != 1 {
		return false, nil
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO project_stages (project_id, stage, hours, completed_at) VALUES ($1::uuid, $2, $3, $4)`, id, stage, hours, at.UTC()); err != nil {
		return false, fmt.Errorf("postgres: writing a stage: %w", err)
	}
	return true, nil
}

// Cancel ends an active project.
func (r *ProjectRepository) Cancel(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE special_projects SET status = 'cancelled', cancelled_at = $2 WHERE id = $1::uuid AND status = 'active'`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: cancelling a project: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Stages lists the stages done.
func (r *ProjectRepository) Stages(ctx context.Context, id string) ([]application.ProjectStageRow, error) {
	rows, err := r.q.Query(ctx, `SELECT project_id::text, stage, hours, completed_at FROM project_stages WHERE project_id = $1::uuid ORDER BY stage`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing stages: %w", err)
	}
	defer rows.Close()
	var out []application.ProjectStageRow
	for rows.Next() {
		var s application.ProjectStageRow
		if err := rows.Scan(&s.ProjectID, &s.Stage, &s.Hours, &s.CompletedAt); err != nil {
			return nil, fmt.Errorf("postgres: reading a stage: %w", err)
		}
		s.CompletedAt = s.CompletedAt.UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// Stock is the materials delivered and not yet used.
func (r *ProjectRepository) Stock(ctx context.Context, id string) (map[string]int64, error) {
	rows, err := r.q.Query(ctx, `SELECT item, qty FROM project_stock WHERE project_id = $1::uuid`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a project's stock: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var it string
		var q int64
		if err := rows.Scan(&it, &q); err != nil {
			return nil, fmt.Errorf("postgres: reading a stack: %w", err)
		}
		out[it] = q
	}
	return out, rows.Err()
}

// AddStock puts materials on the site.
func (r *ProjectRepository) AddStock(ctx context.Context, id, item string, qty int64) error {
	_, err := r.q.Exec(ctx, `INSERT INTO project_stock (project_id, item, qty) VALUES ($1::uuid, $2, $3)
		ON CONFLICT (project_id, item) DO UPDATE SET qty = project_stock.qty + EXCLUDED.qty`, id, item, qty)
	if err != nil {
		return fmt.Errorf("postgres: delivering to a site: %w", err)
	}
	return nil
}

// TakeStock uses up what a stage asks, all or nothing.
func (r *ProjectRepository) TakeStock(ctx context.Context, id string, items map[string]int64) (bool, error) {
	for it, q := range items {
		tag, err := r.q.Exec(ctx, `UPDATE project_stock SET qty = qty - $3 WHERE project_id = $1::uuid AND item = $2 AND qty >= $3`, id, it, q)
		if err != nil {
			return false, fmt.Errorf("postgres: using up a stack: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return false, errStockShort
		}
	}
	return true, nil
}

// errStockShort aborts the caller's transaction path: a stage was judged ready and the stock did not hold it (the caller holds the project's lock, so this is a bug).
var errStockShort = errors.New("postgres: the project's stock does not hold the stage")

// AddPledge records a gift to the fund.
func (r *ProjectRepository) AddPledge(ctx context.Context, p application.ProjectPledge) error {
	_, err := r.q.Exec(ctx, `INSERT INTO project_pledges (id, project_id, player_id, kind, amount, anonymous, status, ledger_tx, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, 'held', $7::uuid, $8)`,
		p.ID, p.ProjectID, nullText(p.PlayerID), p.Kind, p.Amount, p.Anonymous, nullText(p.LedgerTx), p.At.UTC())
	if err != nil {
		return fmt.Errorf("postgres: recording a pledge: %w", err)
	}
	return nil
}

const projPledgeColumns = `id::text, project_id::text, COALESCE(player_id::text, ''), kind, amount, anonymous, status, COALESCE(ledger_tx::text, ''), at, refunded_at`

func scanProjPledge(row pgx.Row) (*application.ProjectPledge, error) {
	var p application.ProjectPledge
	if err := row.Scan(&p.ID, &p.ProjectID, &p.PlayerID, &p.Kind, &p.Amount, &p.Anonymous, &p.Status, &p.LedgerTx, &p.At, &p.RefundedAt); err != nil {
		return nil, err
	}
	p.At, p.RefundedAt = p.At.UTC(), utcPtr(p.RefundedAt)
	return &p, nil
}

// Pledges lists a project's gifts, oldest first.
func (r *ProjectRepository) Pledges(ctx context.Context, projectID string) ([]application.ProjectPledge, error) {
	rows, err := r.q.Query(ctx, `SELECT `+projPledgeColumns+` FROM project_pledges WHERE project_id = $1::uuid ORDER BY at, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing pledges: %w", err)
	}
	defer rows.Close()
	var out []application.ProjectPledge
	for rows.Next() {
		p, err := scanProjPledge(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a pledge: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// PledgeByID reads a pledge, nil for none.
func (r *ProjectRepository) PledgeByID(ctx context.Context, id string, lock bool) (*application.ProjectPledge, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + projPledgeColumns + ` FROM project_pledges WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	p, err := scanProjPledge(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a pledge: %w", err)
	}
	return p, nil
}

// RefundPledge moves a held pledge to refunded, once.
func (r *ProjectRepository) RefundPledge(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE project_pledges SET status = 'refunded', refunded_at = $2 WHERE id = $1::uuid AND status = 'held'`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: refunding a pledge: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const projShiftColumns = `id::text, project_id::text, stage, player_id::text, hours, wage, payer_kind, COALESCE(payer_id::text, ''), status, COALESCE(ledger_tx::text, ''), started_at, ends_at`

func scanProjShift(row pgx.Row) (*application.ProjectShift, error) {
	var s application.ProjectShift
	if err := row.Scan(&s.ID, &s.ProjectID, &s.Stage, &s.PlayerID, &s.Hours, &s.Wage, &s.PayerKind, &s.PayerID, &s.Status, &s.LedgerTx, &s.StartedAt, &s.EndsAt); err != nil {
		return nil, err
	}
	s.StartedAt, s.EndsAt = s.StartedAt.UTC(), s.EndsAt.UTC()
	return &s, nil
}

// StartShift opens a shift; false when the player has a running one (the partial unique index is the fence).
func (r *ProjectRepository) StartShift(ctx context.Context, s application.ProjectShift) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO project_shifts (id, project_id, stage, player_id, hours, wage, payer_kind, payer_id, status, started_at, ends_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8::uuid, 'running', $9, $10) ON CONFLICT DO NOTHING`,
		s.ID, s.ProjectID, s.Stage, s.PlayerID, s.Hours, s.Wage, s.PayerKind, nullText(s.PayerID), s.StartedAt.UTC(), s.EndsAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: starting a shift: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Shift reads a shift, nil for none.
func (r *ProjectRepository) Shift(ctx context.Context, id string, lock bool) (*application.ProjectShift, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + projShiftColumns + ` FROM project_shifts WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	s, err := scanProjShift(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a shift: %w", err)
	}
	return s, nil
}

// RunningShift is the player's shift in progress, nil for none.
func (r *ProjectRepository) RunningShift(ctx context.Context, playerID string) (*application.ProjectShift, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	s, err := scanProjShift(r.q.QueryRow(ctx, `SELECT `+projShiftColumns+` FROM project_shifts WHERE player_id = $1::uuid AND status = 'running'`, playerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a running shift: %w", err)
	}
	return s, nil
}

// EndShift settles a running shift once.
func (r *ProjectRepository) EndShift(ctx context.Context, id, ledgerTx string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE project_shifts SET status = 'done', ledger_tx = $2::uuid WHERE id = $1::uuid AND status = 'running'`, id, nullText(ledgerTx))
	if err != nil {
		return false, fmt.Errorf("postgres: ending a shift: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ShiftsOf lists the latest shifts of a project.
func (r *ProjectRepository) ShiftsOf(ctx context.Context, projectID string, limit int) ([]application.ProjectShift, error) {
	rows, err := r.q.Query(ctx, `SELECT `+projShiftColumns+` FROM project_shifts WHERE project_id = $1::uuid ORDER BY started_at DESC, id LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing shifts: %w", err)
	}
	defer rows.Close()
	var out []application.ProjectShift
	for rows.Next() {
		s, err := scanProjShift(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a shift: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// NPCHoursOn is how many hours of hired labour the project took on the day.
func (r *ProjectRepository) NPCHoursOn(ctx context.Context, projectID string, day int64) (int64, error) {
	var h int64
	err := r.q.QueryRow(ctx, `SELECT COALESCE((SELECT hours FROM project_npc_days WHERE project_id = $1::uuid AND day = $2), 0)`, projectID, day).Scan(&h)
	if err != nil {
		return 0, fmt.Errorf("postgres: reading hired hours: %w", err)
	}
	return h, nil
}

// AddNPCHours adds hired labour to the day's row.
func (r *ProjectRepository) AddNPCHours(ctx context.Context, projectID string, day int64, stage int, hours, wage int64) error {
	_, err := r.q.Exec(ctx, `INSERT INTO project_npc_days (project_id, day, stage, hours, wage) VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (project_id, day) DO UPDATE SET hours = project_npc_days.hours + EXCLUDED.hours, wage = project_npc_days.wage + EXCLUDED.wage`, projectID, day, stage, hours, wage)
	if err != nil {
		return fmt.Errorf("postgres: recording hired hours: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// contracts
// ---------------------------------------------------------------------------

const projContractColumns = `id::text, project_id::text, posted_by::text, rule, status, COALESCE(bid_id::text, ''), COALESCE(company_id::text, ''), budget_cap, bond_bps, retention_bps,
	COALESCE(price, 0), retention_held, defects_days, due_days, stages_paid, defect_raised, opens_at, closes_at, awarded_at, due_at, last_stage_at, closed_at`

func scanProjContract(row pgx.Row) (*application.ProjectContract, error) {
	var c application.ProjectContract
	if err := row.Scan(&c.ID, &c.ProjectID, &c.PostedBy, &c.Rule, &c.Status, &c.BidID, &c.CompanyID, &c.BudgetCap, &c.BondBPS, &c.RetentionBPS, &c.Price, &c.RetentionHeld,
		&c.DefectsDays, &c.DueDays, &c.StagesPaid, &c.DefectRaised, &c.OpensAt, &c.ClosesAt, &c.AwardedAt, &c.DueAt, &c.LastStageAt, &c.ClosedAt); err != nil {
		return nil, err
	}
	c.OpensAt, c.ClosesAt = c.OpensAt.UTC(), c.ClosesAt.UTC()
	c.AwardedAt, c.DueAt, c.LastStageAt, c.ClosedAt = utcPtr(c.AwardedAt), utcPtr(c.DueAt), utcPtr(c.LastStageAt), utcPtr(c.ClosedAt)
	return &c, nil
}

// InsertContract posts a contract; false when the project has an open or running one.
func (r *ProjectRepository) InsertContract(ctx context.Context, c application.ProjectContract) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO project_contracts (id, project_id, posted_by, budget_cap, rule, bond_bps, retention_bps, defects_days, opens_at, closes_at, due_days, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, 'open') ON CONFLICT DO NOTHING`,
		c.ID, c.ProjectID, c.PostedBy, c.BudgetCap, c.Rule, c.BondBPS, c.RetentionBPS, c.DefectsDays, c.OpensAt.UTC(), c.ClosesAt.UTC(), c.DueDays)
	if err != nil {
		return false, fmt.Errorf("postgres: posting a contract: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ContractByID reads a contract, nil for none.
func (r *ProjectRepository) ContractByID(ctx context.Context, id string, lock bool) (*application.ProjectContract, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + projContractColumns + ` FROM project_contracts WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := scanProjContract(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a contract: %w", err)
	}
	return c, nil
}

// ContractOf is the open or running contract of a project, nil for none.
func (r *ProjectRepository) ContractOf(ctx context.Context, projectID string) (*application.ProjectContract, error) {
	if !validUUID(projectID) {
		return nil, nil
	}
	c, err := scanProjContract(r.q.QueryRow(ctx, `SELECT `+projContractColumns+` FROM project_contracts WHERE project_id = $1::uuid AND status IN ('open', 'awarded')`, projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a contract: %w", err)
	}
	return c, nil
}

// OpenContracts lists the contracts still taking bids, newest first.
func (r *ProjectRepository) OpenContracts(ctx context.Context, limit int) ([]application.ProjectContract, error) {
	rows, err := r.q.Query(ctx, `SELECT `+projContractColumns+` FROM project_contracts WHERE status = 'open' ORDER BY opens_at DESC, id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing contracts: %w", err)
	}
	defer rows.Close()
	var out []application.ProjectContract
	for rows.Next() {
		c, err := scanProjContract(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a contract: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// AddContractBid records a bid; false when the company has one already.
func (r *ProjectRepository) AddContractBid(ctx context.Context, b application.ProjectContractBid) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO project_contract_bids (id, contract_id, company_id, bid_by, price, days, record, bond, status, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 'standing', $9) ON CONFLICT DO NOTHING`,
		b.ID, b.ContractID, b.CompanyID, b.BidBy, b.Price, b.Days, b.Record, b.Bond, b.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a bid: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ContractBids lists a contract's bids, oldest first.
func (r *ProjectRepository) ContractBids(ctx context.Context, contractID string) ([]application.ProjectContractBid, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, contract_id::text, company_id::text, bid_by::text, status, price, bond, days, record, at
		FROM project_contract_bids WHERE contract_id = $1::uuid ORDER BY at, id`, contractID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing bids: %w", err)
	}
	defer rows.Close()
	var out []application.ProjectContractBid
	for rows.Next() {
		var b application.ProjectContractBid
		if err := rows.Scan(&b.ID, &b.ContractID, &b.CompanyID, &b.BidBy, &b.Status, &b.Price, &b.Bond, &b.Days, &b.Record, &b.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a bid: %w", err)
		}
		b.At = b.At.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetContractBid sets one bid's state.
func (r *ProjectRepository) SetContractBid(ctx context.Context, bidID, status string) error {
	_, err := r.q.Exec(ctx, `UPDATE project_contract_bids SET status = $2 WHERE id = $1::uuid`, bidID, status)
	if err != nil {
		return fmt.Errorf("postgres: setting a bid: %w", err)
	}
	return nil
}

// AwardContract moves an open contract to awarded in one conditional update.
func (r *ProjectRepository) AwardContract(ctx context.Context, contractID, bidID, companyID string, price int64, at, dueAt time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE project_contracts SET status = 'awarded', bid_id = $2::uuid, company_id = $3::uuid, price = $4, awarded_at = $5, due_at = $6
		WHERE id = $1::uuid AND status = 'open'`, contractID, bidID, companyID, price, at.UTC(), dueAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: awarding a contract: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetContractStatus moves a contract from one state to another, once.
func (r *ProjectRepository) SetContractStatus(ctx context.Context, contractID, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE project_contracts SET status = $3, closed_at = CASE WHEN $3 IN ('done', 'failed', 'cancelled') THEN $4 ELSE closed_at END
		WHERE id = $1::uuid AND status = $2`, contractID, from, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: moving a contract: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PayContractStage writes a paid stage and counts it; false when it was written already.
func (r *ProjectRepository) PayContractStage(ctx context.Context, s application.ProjectContractStage) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO project_contract_stages (contract_id, stage, paid, held, ledger_tx, at) VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6) ON CONFLICT DO NOTHING`,
		s.ContractID, s.Stage, s.Paid, s.Held, nullText(s.LedgerTx), s.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: paying a stage: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if _, err := r.q.Exec(ctx, `UPDATE project_contracts SET stages_paid = stages_paid + 1, last_stage_at = $2 WHERE id = $1::uuid`, s.ContractID, s.At.UTC()); err != nil {
		return false, fmt.Errorf("postgres: counting a paid stage: %w", err)
	}
	return true, nil
}

// AddContractRetention adds to what is held back.
func (r *ProjectRepository) AddContractRetention(ctx context.Context, contractID string, held int64, at time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE project_contracts SET retention_held = retention_held + $2 WHERE id = $1::uuid`, contractID, held)
	if err != nil {
		return fmt.Errorf("postgres: holding a retention: %w", err)
	}
	return nil
}

// ClearContractRetention releases what is held back.
func (r *ProjectRepository) ClearContractRetention(ctx context.Context, contractID string) error {
	_, err := r.q.Exec(ctx, `UPDATE project_contracts SET retention_held = 0 WHERE id = $1::uuid`, contractID)
	if err != nil {
		return fmt.Errorf("postgres: releasing a retention: %w", err)
	}
	return nil
}

// RaiseContractDefect marks a defect raised, once.
func (r *ProjectRepository) RaiseContractDefect(ctx context.Context, contractID string) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE project_contracts SET defect_raised = true WHERE id = $1::uuid AND defect_raised = false`, contractID)
	if err != nil {
		return false, fmt.Errorf("postgres: raising a defect: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
