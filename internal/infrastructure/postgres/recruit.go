package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists specialist recruitment
// (migrations/0031_specialist_recruitment): the cities' pools of
// specialists, the companies' campaigns and their advertising fees. The
// candidates and the specialists are recruit_staff.go.

// recruitCampaignsOneDraftIdx is the partial unique index of one draft per
// company.
const recruitCampaignsOneDraftIdx = "recruit_campaigns_one_draft_idx"

// RecruitRepository implements application.RecruitRepository over the
// tables of migration 0031.
type RecruitRepository struct {
	q querier
}

var _ application.RecruitRepository = (*RecruitRepository)(nil)

// NewRecruitRepository returns recruitment over the pool, for the admin
// tool's reads. Inside a unit of work, use Tx.Recruitment.
func NewRecruitRepository(p *Pool) *RecruitRepository { return &RecruitRepository{q: p.shared()} }

// ---------------------------------------------------------------------------
// Pools.

// Pool reads a pool.
func (r *RecruitRepository) Pool(ctx context.Context, cityID, skill string, level int, lock bool) (*application.SpecialistPool, error) {
	sql := `SELECT city_id::text, skill, level, available, refilled_at FROM specialist_pools
		WHERE city_id = $1::uuid AND skill = $2 AND level = $3`
	if lock {
		sql += ` FOR UPDATE`
	}
	var p application.SpecialistPool
	err := r.q.QueryRow(ctx, sql, cityID, skill, level).Scan(&p.CityID, &p.Skill, &p.Level, &p.Available, &p.RefilledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a specialist pool: %w", err)
	}
	p.RefilledAt = p.RefilledAt.UTC()
	return &p, nil
}

// SavePool writes a pool.
func (r *RecruitRepository) SavePool(ctx context.Context, p application.SpecialistPool) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO specialist_pools (city_id, skill, level, available, refilled_at)
		VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (city_id, skill, level) DO UPDATE SET available = EXCLUDED.available, refilled_at = EXCLUDED.refilled_at`,
		p.CityID, p.Skill, p.Level, p.Available, p.RefilledAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a specialist pool: %w", err)
	}
	return nil
}

// EnsurePool inserts a pool when it is absent.
func (r *RecruitRepository) EnsurePool(ctx context.Context, p application.SpecialistPool) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO specialist_pools (city_id, skill, level, available, refilled_at)
		VALUES ($1::uuid, $2, $3, $4, $5) ON CONFLICT (city_id, skill, level) DO NOTHING`,
		p.CityID, p.Skill, p.Level, p.Available, p.RefilledAt.UTC()); err != nil {
		return fmt.Errorf("postgres: creating a specialist pool: %w", err)
	}
	return nil
}

// Pending counts a pool's candidates waiting for an answer.
func (r *RecruitRepository) Pending(ctx context.Context, cityID, skill string, level int, at time.Time) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM recruit_candidates
		WHERE home_city_id = $1::uuid AND skill = $2 AND level = $3 AND status = 'pending' AND expires_at > $4`,
		cityID, skill, level, at.UTC()).
		Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting a pool's candidates: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Campaigns.

const campaignColumns = `id::text, no, company_id::text, status, skill, min_level, cities, positions, hired, salary,
	housing, signing, relocation, term_periods, shares, auto_accept, ad_fee, checks_total, checks_done,
	COALESCE(action_id::text, ''), next_check_at, created_by::text, created_at, posted_at, ended_at, updated_at`

func scanCampaign(row pgx.Row) (*application.RecruitCampaign, error) {
	var c application.RecruitCampaign
	if err := row.Scan(&c.ID, &c.No, &c.CompanyID, &c.Status, &c.Skill, &c.MinLevel, &c.Cities, &c.Positions, &c.Hired,
		&c.Salary, &c.Housing, &c.Signing, &c.Relocation, &c.TermPeriods, &c.Shares, &c.AutoAccept, &c.AdFee,
		&c.ChecksTotal, &c.ChecksDone, &c.ActionID, &c.NextCheckAt, &c.CreatedBy, &c.CreatedAt, &c.PostedAt, &c.EndedAt,
		&c.UpdatedAt); err != nil {
		return nil, err
	}
	c.NextCheckAt, c.PostedAt, c.EndedAt = utcPtr(c.NextCheckAt), utcPtr(c.PostedAt), utcPtr(c.EndedAt)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return &c, nil
}

// CreateCampaign inserts a campaign.
func (r *RecruitRepository) CreateCampaign(ctx context.Context, c application.RecruitCampaign) (application.RecruitCampaign, error) {
	err := r.q.QueryRow(ctx, `INSERT INTO recruit_campaigns (id, company_id, status, skill, min_level, cities, positions,
		hired, salary, housing, signing, relocation, term_periods, shares, auto_accept, ad_fee, checks_total, checks_done,
		action_id, next_check_at, created_by, created_at, posted_at, ended_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19::uuid, $20,
		        $21::uuid, $22, $23, $24, $25)
		RETURNING no`,
		c.ID, c.CompanyID, c.Status, c.Skill, c.MinLevel, c.Cities, c.Positions, c.Hired, c.Salary, c.Housing, c.Signing,
		c.Relocation, c.TermPeriods, c.Shares, c.AutoAccept, c.AdFee, c.ChecksTotal, c.ChecksDone, nullableUUID(c.ActionID),
		c.NextCheckAt, c.CreatedBy, c.CreatedAt.UTC(), c.PostedAt, c.EndedAt, c.UpdatedAt.UTC()).Scan(&c.No)
	if violates(err, sqlstateUniqueViolation, recruitCampaignsOneDraftIdx) {
		return c, application.ErrCampaignDraftExists
	}
	if err != nil {
		return c, fmt.Errorf("postgres: creating a recruitment campaign: %w", err)
	}
	return c, nil
}

// Draft reads a company's draft.
func (r *RecruitRepository) Draft(ctx context.Context, companyID string) (*application.RecruitCampaign, error) {
	c, err := scanCampaign(r.q.QueryRow(ctx, `SELECT `+campaignColumns+` FROM recruit_campaigns
		WHERE company_id = $1::uuid AND status = 'draft'`, companyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a campaign draft: %w", err)
	}
	return c, nil
}

func (r *RecruitRepository) campaign(ctx context.Context, where string, arg any, lock bool) (*application.RecruitCampaign, error) {
	sql := `SELECT ` + campaignColumns + ` FROM recruit_campaigns WHERE ` + where
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := scanCampaign(r.q.QueryRow(ctx, sql, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrCampaignNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a recruitment campaign: %w", err)
	}
	return c, nil
}

// Campaign reads a campaign by number.
func (r *RecruitRepository) Campaign(ctx context.Context, no int64, lock bool) (*application.RecruitCampaign, error) {
	return r.campaign(ctx, `no = $1`, no, lock)
}

// CampaignByID reads a campaign by id.
func (r *RecruitRepository) CampaignByID(ctx context.Context, id string, lock bool) (*application.RecruitCampaign, error) {
	if !validUUID(id) {
		return nil, application.ErrCampaignNotFound
	}
	return r.campaign(ctx, `id = $1::uuid`, id, lock)
}

// SaveCampaign writes a campaign's mutable state.
func (r *RecruitRepository) SaveCampaign(ctx context.Context, c application.RecruitCampaign) error {
	if _, err := r.q.Exec(ctx, `UPDATE recruit_campaigns SET status = $2, skill = $3, min_level = $4, cities = $5,
		positions = $6, hired = $7, salary = $8, housing = $9, signing = $10, relocation = $11, term_periods = $12,
		shares = $13, auto_accept = $14, ad_fee = $15, checks_total = $16, checks_done = $17, action_id = $18::uuid,
		next_check_at = $19, posted_at = $20, ended_at = $21, updated_at = $22
		WHERE id = $1::uuid`,
		c.ID, c.Status, c.Skill, c.MinLevel, c.Cities, c.Positions, c.Hired, c.Salary, c.Housing, c.Signing,
		c.Relocation, c.TermPeriods, c.Shares, c.AutoAccept, c.AdFee, c.ChecksTotal, c.ChecksDone,
		nullableUUID(c.ActionID), c.NextCheckAt, c.PostedAt, c.EndedAt, c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a recruitment campaign: %w", err)
	}
	return nil
}

// Campaigns lists a company's campaigns.
func (r *RecruitRepository) Campaigns(ctx context.Context, companyID string, limit int) ([]application.RecruitCampaign, error) {
	rows, err := r.q.Query(ctx, `SELECT `+campaignColumns+` FROM recruit_campaigns WHERE company_id = $1::uuid
		ORDER BY (status IN ('draft', 'running')) DESC, created_at DESC, no DESC LIMIT $2`, companyID, max(limit, 1))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing recruitment campaigns: %w", err)
	}
	defer rows.Close()
	var out []application.RecruitCampaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a recruitment campaign: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Running counts a company's running campaigns.
func (r *RecruitRepository) Running(ctx context.Context, companyID string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM recruit_campaigns WHERE company_id = $1::uuid AND status = 'running'`,
		companyID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting running campaigns: %w", err)
	}
	return n, nil
}

// RecordAdFee appends what a campaign paid a city.
func (r *RecruitRepository) RecordAdFee(ctx context.Context, f application.RecruitAdFee) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO recruit_ad_fees (campaign_id, city_id, amount, ledger_transaction_id, paid_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5)`,
		f.CampaignID, f.CityID, f.Amount, nullableUUID(f.LedgerTransactionID), f.PaidAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording an advertising fee: %w", err)
	}
	return nil
}
