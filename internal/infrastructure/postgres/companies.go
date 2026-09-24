package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Constraint names from migrations/0019_companies.up.sql mapped to
// sentinels here.
const (
	companiesCityNameIdx             = "companies_city_name_idx"
	companiesCodeKey                 = "companies_code_key"
	companyApplicationsOnePendingIdx = "company_applications_one_pending_idx"
	companyMarketPeriodsPkey         = "company_market_periods_pkey"
)

// CompanyRepository persists player companies (migrations/0019).
type CompanyRepository struct {
	q querier
}

var _ application.CompanyRepository = (*CompanyRepository)(nil)

// NewCompanyRepository returns the companies over the pool, for the admin
// tool's reads. Inside a unit of work, use Tx.Companies.
func NewCompanyRepository(p *Pool) *CompanyRepository { return &CompanyRepository{q: p.Raw()} }

// Periods lists a company's latest settled periods, newest first, for the
// admin tool.
func (r *CompanyRepository) Periods(ctx context.Context, companyID string, limit int) ([]application.CompanyPeriod, error) {
	rows, err := r.q.Query(ctx,
		`SELECT company_id::text, period_no, city_id::text, started_at, ended_at, presence_bps, price_bps,
		        quality_bps, shifts, wanted_units, capacity_units, sold_units, revenue, sales_tax, wages,
		        upkeep_due, upkeep_paid, debt, balance_after, insolvent, settled_at
		   FROM company_periods WHERE company_id = $1::uuid ORDER BY period_no DESC LIMIT $2`, companyID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a company's periods: %w", err)
	}
	defer rows.Close()
	var out []application.CompanyPeriod
	for rows.Next() {
		var p application.CompanyPeriod
		if err := rows.Scan(&p.CompanyID, &p.PeriodNo, &p.CityID, &p.StartedAt, &p.EndedAt, &p.PresenceBPS, &p.PriceBPS,
			&p.QualityBPS, &p.Shifts, &p.WantedUnits, &p.CapacityUnits, &p.SoldUnits, &p.Revenue, &p.SalesTax,
			&p.Wages, &p.UpkeepDue, &p.UpkeepPaid, &p.Debt, &p.BalanceAfter, &p.Insolvent, &p.SettledAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const companyColumns = `id::text, code, name, name_key, type_code, city_id::text, owner_player_id::text,
	COALESCE(manager_player_id::text, ''), status, price_bps, auto_accept, total_shares, debt, arrears,
	rating_bps, registration_fee, COALESCE(registration_transaction_id::text, ''), content_version,
	founded_at, updated_at, closed_at, COALESCE(close_reason, '')`

// companyColumnsC is companyColumns of a companies table aliased c.
const companyColumnsC = `c.id::text, c.code, c.name, c.name_key, c.type_code, c.city_id::text, c.owner_player_id::text,
	COALESCE(c.manager_player_id::text, ''), c.status, c.price_bps, c.auto_accept, c.total_shares, c.debt, c.arrears,
	c.rating_bps, c.registration_fee, COALESCE(c.registration_transaction_id::text, ''), c.content_version,
	c.founded_at, c.updated_at, c.closed_at, COALESCE(c.close_reason, '')`

func scanCompany(row pgx.Row) (*application.Company, error) {
	var c application.Company
	if err := row.Scan(&c.ID, &c.Code, &c.Name, &c.NameKey, &c.TypeCode, &c.CityID, &c.OwnerID,
		&c.ManagerID, &c.Status, &c.PriceBPS, &c.AutoAccept, &c.TotalShares, &c.Debt, &c.Arrears,
		&c.RatingBPS, &c.RegistrationFee, &c.RegistrationTransactionID, &c.ContentVersion,
		&c.FoundedAt, &c.UpdatedAt, &c.ClosedAt, &c.CloseReason); err != nil {
		return nil, err
	}
	c.FoundedAt, c.UpdatedAt = c.FoundedAt.UTC(), c.UpdatedAt.UTC()
	if c.ClosedAt != nil {
		t := c.ClosedAt.UTC()
		c.ClosedAt = &t
	}
	return &c, nil
}

func (r *CompanyRepository) companies(ctx context.Context, sql string, args ...any) ([]application.Company, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing companies: %w", err)
	}
	defer rows.Close()
	var out []application.Company
	for rows.Next() {
		c, err := scanCompany(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a company: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r *CompanyRepository) one(ctx context.Context, sql string, args ...any) (*application.Company, error) {
	c, err := scanCompany(r.q.QueryRow(ctx, sql, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrCompanyNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a company: %w", err)
	}
	return c, nil
}

// Create inserts a company and its founder's shares.
func (r *CompanyRepository) Create(ctx context.Context, c application.Company, founder application.CompanyShareholder) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO companies (id, code, name, name_key, type_code, city_id, owner_player_id, status, price_bps,
		                        auto_accept, total_shares, registration_fee, registration_transaction_id,
		                        content_version, founded_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7::uuid, $8, $9, $10, $11, $12, $13::uuid, $14, $15, $15)`,
		c.ID, c.Code, c.Name, c.NameKey, c.TypeCode, c.CityID, c.OwnerID, application.CompanyActive, c.PriceBPS,
		c.AutoAccept, c.TotalShares, c.RegistrationFee, nullableUUID(c.RegistrationTransactionID),
		c.ContentVersion, c.FoundedAt.UTC())
	switch {
	case violates(err, sqlstateUniqueViolation, companiesCityNameIdx):
		return application.ErrCompanyNameTaken
	case violates(err, sqlstateUniqueViolation, companiesCodeKey):
		return application.ErrCompanyCodeTaken
	case err != nil:
		return fmt.Errorf("postgres: founding a company: %w", err)
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO company_shareholders (company_id, player_id, shares, acquired_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4)`,
		c.ID, founder.PlayerID, founder.Shares, founder.AcquiredAt.UTC()); err != nil {
		return fmt.Errorf("postgres: issuing a company's shares: %w", err)
	}
	return nil
}

// ByID reads a company.
func (r *CompanyRepository) ByID(ctx context.Context, id string) (*application.Company, error) {
	return r.one(ctx, `SELECT `+companyColumns+` FROM companies WHERE id = $1::uuid`, id)
}

// ByCode reads a company by its public code.
func (r *CompanyRepository) ByCode(ctx context.Context, code string) (*application.Company, error) {
	return r.one(ctx, `SELECT `+companyColumns+` FROM companies WHERE code = $1`, code)
}

// Lock reads a company locked for the rest of the transaction.
func (r *CompanyRepository) Lock(ctx context.Context, id string) (*application.Company, error) {
	return r.one(ctx, `SELECT `+companyColumns+` FROM companies WHERE id = $1::uuid FOR UPDATE`, id)
}

// InCity lists a city's active companies, oldest first.
func (r *CompanyRepository) InCity(ctx context.Context, cityID string) ([]application.Company, error) {
	return r.companies(ctx, `SELECT `+companyColumns+` FROM companies
		WHERE city_id = $1::uuid AND status = 'active' ORDER BY founded_at, id`, cityID)
}

// LockActiveInCity locks a city's active companies in id order.
func (r *CompanyRepository) LockActiveInCity(ctx context.Context, cityID string) ([]application.Company, error) {
	return r.companies(ctx, `SELECT `+companyColumns+` FROM companies
		WHERE city_id = $1::uuid AND status = 'active' ORDER BY id FOR UPDATE`, cityID)
}

// Of lists the active companies a player owns or manages.
func (r *CompanyRepository) Of(ctx context.Context, playerID string) ([]application.Company, error) {
	return r.companies(ctx, `SELECT `+companyColumns+` FROM companies
		WHERE status = 'active' AND (owner_player_id = $1::uuid OR manager_player_id = $1::uuid)
		ORDER BY founded_at, id`, playerID)
}

// OwnedCount is how many active companies a player owns.
func (r *CompanyRepository) OwnedCount(ctx context.Context, playerID string) (int, error) {
	var n int
	err := r.q.QueryRow(ctx, `SELECT count(*) FROM companies WHERE status = 'active' AND owner_player_id = $1::uuid`,
		playerID).Scan(&n)
	if err != nil && !isInvalidUUIDText(err) {
		return 0, fmt.Errorf("postgres: counting companies: %w", err)
	}
	return n, nil
}

// All lists companies, newest first.
func (r *CompanyRepository) All(ctx context.Context, limit int) ([]application.Company, error) {
	return r.companies(ctx, `SELECT `+companyColumns+` FROM companies ORDER BY founded_at DESC, id LIMIT $1`, limit)
}

// Save writes a company's mutable state.
func (r *CompanyRepository) Save(ctx context.Context, c application.Company) error {
	var closed *time.Time
	if c.ClosedAt != nil {
		t := c.ClosedAt.UTC()
		closed = &t
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE companies
		    SET manager_player_id = $2::uuid, price_bps = $3, auto_accept = $4, debt = $5, arrears = $6,
		        rating_bps = $7, status = $8, closed_at = $9, close_reason = $10, updated_at = $11,
		        registration_transaction_id = COALESCE(registration_transaction_id, $12::uuid)
		  WHERE id = $1::uuid`,
		c.ID, nullableUUID(c.ManagerID), c.PriceBPS, c.AutoAccept, c.Debt, c.Arrears, c.RatingBPS,
		c.Status, closed, nullableText(c.CloseReason), c.UpdatedAt.UTC(), nullableUUID(c.RegistrationTransactionID))
	if err != nil {
		return fmt.Errorf("postgres: saving a company: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrCompanyNotFound
	}
	return nil
}

// Shareholders lists who holds a company's shares, the largest first.
func (r *CompanyRepository) Shareholders(ctx context.Context, companyID string) ([]application.CompanyShareholder, error) {
	rows, err := r.q.Query(ctx,
		`SELECT company_id::text, player_id::text, shares, acquired_at FROM company_shareholders
		  WHERE company_id = $1::uuid ORDER BY shares DESC, acquired_at`, companyID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing shareholders: %w", err)
	}
	defer rows.Close()
	var out []application.CompanyShareholder
	for rows.Next() {
		var s application.CompanyShareholder
		if err := rows.Scan(&s.CompanyID, &s.PlayerID, &s.Shares, &s.AcquiredAt); err != nil {
			return nil, err
		}
		s.AcquiredAt = s.AcquiredAt.UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// Reserved sums the wages reserved by shifts worked for the company now.
func (r *CompanyRepository) Reserved(ctx context.Context, companyID string) (int64, error) {
	var v int64
	if err := r.q.QueryRow(ctx,
		`SELECT COALESCE(SUM(wage_reserved), 0)::bigint FROM shift_sessions
		  WHERE company_id = $1::uuid AND status = 'working'`, companyID).Scan(&v); err != nil {
		return 0, fmt.Errorf("postgres: reading reserved wages: %w", err)
	}
	return v, nil
}

// Staff lists a company's current employees.
func (r *CompanyRepository) Staff(ctx context.Context, companyID string) ([]application.CompanyEmployee, error) {
	rows, err := r.q.Query(ctx,
		`SELECT e.id::text, e.player_id::text, e.career_code, e.tier, e.rate, COALESCE(e.opening_id::text, ''),
		        e.hired_at, e.total_shifts,
		        EXISTS (SELECT 1 FROM shift_sessions s WHERE s.employment_id = e.id AND s.status = 'working')
		   FROM employments e
		  WHERE e.company_id = $1::uuid AND e.ended_at IS NULL
		  ORDER BY e.hired_at, e.id`, companyID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing staff: %w", err)
	}
	defer rows.Close()
	var out []application.CompanyEmployee
	for rows.Next() {
		var e application.CompanyEmployee
		if err := rows.Scan(&e.EmploymentID, &e.PlayerID, &e.CareerCode, &e.Tier, &e.Rate, &e.OpeningID,
			&e.HiredAt, &e.TotalShifts, &e.Working); err != nil {
			return nil, err
		}
		e.HiredAt = e.HiredAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Activity counts the shifts worked for a company in [from, to) and the
// gross wages they paid.
func (r *CompanyRepository) Activity(ctx context.Context, companyID string, from, to time.Time) (int, int64, error) {
	var (
		n     int
		wages int64
	)
	if err := r.q.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(gross), 0)::bigint FROM work_shifts
		  WHERE company_id = $1::uuid AND worked_at >= $2 AND worked_at < $3`,
		companyID, from.UTC(), to.UTC()).Scan(&n, &wages); err != nil {
		return 0, 0, fmt.Errorf("postgres: counting a company's shifts: %w", err)
	}
	return n, wages, nil
}

const openingColumns = `o.id::text, o.no, o.company_id::text, o.career_code, o.wage, o.positions, o.status,
	o.created_at, o.updated_at,
	(SELECT count(*) FROM employments e WHERE e.opening_id = o.id AND e.ended_at IS NULL)::int`

func scanOpening(row pgx.Row) (*application.CompanyOpening, error) {
	var o application.CompanyOpening
	if err := row.Scan(&o.ID, &o.No, &o.CompanyID, &o.CareerCode, &o.Wage, &o.Positions, &o.Status,
		&o.CreatedAt, &o.UpdatedAt, &o.Filled); err != nil {
		return nil, err
	}
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	return &o, nil
}

// PostOpening inserts an opening.
func (r *CompanyRepository) PostOpening(ctx context.Context, o application.CompanyOpening) (application.CompanyOpening, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO company_openings (id, company_id, career_code, wage, positions, status, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'open', $6, $6) RETURNING no`,
		o.ID, o.CompanyID, o.CareerCode, o.Wage, o.Positions, o.CreatedAt.UTC()).Scan(&o.No)
	if err != nil {
		return o, fmt.Errorf("postgres: posting an opening: %w", err)
	}
	o.Status, o.UpdatedAt = application.OpeningOpen, o.CreatedAt
	return o, nil
}

func (r *CompanyRepository) opening(ctx context.Context, sql string, args ...any) (*application.CompanyOpening, error) {
	o, err := scanOpening(r.q.QueryRow(ctx, sql, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrOpeningNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an opening: %w", err)
	}
	return o, nil
}

// Opening reads an opening by number, locked when lock is set.
func (r *CompanyRepository) Opening(ctx context.Context, no int64, lock bool) (*application.CompanyOpening, error) {
	if lock {
		return r.opening(ctx, `SELECT `+openingColumns+` FROM company_openings o WHERE o.no = $1 FOR UPDATE`, no)
	}
	return r.opening(ctx, `SELECT `+openingColumns+` FROM company_openings o WHERE o.no = $1`, no)
}

// OpeningByID reads an opening by id.
func (r *CompanyRepository) OpeningByID(ctx context.Context, id string) (*application.CompanyOpening, error) {
	return r.opening(ctx, `SELECT `+openingColumns+` FROM company_openings o WHERE o.id = $1::uuid`, id)
}

// Openings lists a company's open openings.
func (r *CompanyRepository) Openings(ctx context.Context, companyID string) ([]application.CompanyOpening, error) {
	rows, err := r.q.Query(ctx, `SELECT `+openingColumns+` FROM company_openings o
		WHERE o.company_id = $1::uuid AND o.status = 'open' ORDER BY o.no`, companyID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing openings: %w", err)
	}
	defer rows.Close()
	var out []application.CompanyOpening
	for rows.Next() {
		o, err := scanOpening(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// CityOpenings lists the open openings with a free position of a city's
// active companies.
func (r *CompanyRepository) CityOpenings(ctx context.Context, cityID string) ([]application.CityOpening, error) {
	rows, err := r.q.Query(ctx, `SELECT `+openingColumns+`, `+companyColumnsC+`
		  FROM company_openings o JOIN companies c ON c.id = o.company_id
		 WHERE c.city_id = $1::uuid AND c.status = 'active' AND o.status = 'open'
		 ORDER BY o.wage DESC, o.no`, cityID)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing a city's openings: %w", err)
	}
	defer rows.Close()
	var out []application.CityOpening
	for rows.Next() {
		var (
			o application.CompanyOpening
			c application.Company
		)
		if err := rows.Scan(&o.ID, &o.No, &o.CompanyID, &o.CareerCode, &o.Wage, &o.Positions, &o.Status,
			&o.CreatedAt, &o.UpdatedAt, &o.Filled,
			&c.ID, &c.Code, &c.Name, &c.NameKey, &c.TypeCode, &c.CityID, &c.OwnerID,
			&c.ManagerID, &c.Status, &c.PriceBPS, &c.AutoAccept, &c.TotalShares, &c.Debt, &c.Arrears,
			&c.RatingBPS, &c.RegistrationFee, &c.RegistrationTransactionID, &c.ContentVersion,
			&c.FoundedAt, &c.UpdatedAt, &c.ClosedAt, &c.CloseReason); err != nil {
			return nil, err
		}
		if o.Free() == 0 {
			continue
		}
		out = append(out, application.CityOpening{Opening: o, Company: c})
	}
	return out, rows.Err()
}

// SaveOpening writes an opening's positions and status.
func (r *CompanyRepository) SaveOpening(ctx context.Context, o application.CompanyOpening) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE company_openings SET positions = $2, status = $3, updated_at = $4 WHERE id = $1::uuid`,
		o.ID, o.Positions, o.Status, o.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving an opening: %w", err)
	}
	return nil
}

const applicationColumns = `id::text, no, opening_id::text, company_id::text, player_id::text, status,
	applied_at, decided_at, COALESCE(decided_by::text, '')`

func scanApplication(row pgx.Row) (*application.CompanyApplication, error) {
	var a application.CompanyApplication
	if err := row.Scan(&a.ID, &a.No, &a.OpeningID, &a.CompanyID, &a.PlayerID, &a.Status,
		&a.AppliedAt, &a.DecidedAt, &a.DecidedBy); err != nil {
		return nil, err
	}
	a.AppliedAt = a.AppliedAt.UTC()
	return &a, nil
}

// Apply records a pending application.
func (r *CompanyRepository) Apply(ctx context.Context, a application.CompanyApplication) (application.CompanyApplication, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO company_applications (id, opening_id, company_id, player_id, status, applied_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'pending', $5) RETURNING no`,
		a.ID, a.OpeningID, a.CompanyID, a.PlayerID, a.AppliedAt.UTC()).Scan(&a.No)
	if violates(err, sqlstateUniqueViolation, companyApplicationsOnePendingIdx) {
		return a, application.ErrAlreadyApplied
	}
	if err != nil {
		return a, fmt.Errorf("postgres: applying: %w", err)
	}
	a.Status = application.ApplicationPending
	return a, nil
}

// Application reads an application by number, locked.
func (r *CompanyRepository) Application(ctx context.Context, no int64) (*application.CompanyApplication, error) {
	a, err := scanApplication(r.q.QueryRow(ctx,
		`SELECT `+applicationColumns+` FROM company_applications WHERE no = $1 FOR UPDATE`, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrApplicationNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an application: %w", err)
	}
	return a, nil
}

// Pending lists a company's pending applications.
func (r *CompanyRepository) Pending(ctx context.Context, companyID string) ([]application.CompanyApplication, error) {
	rows, err := r.q.Query(ctx, `SELECT `+applicationColumns+` FROM company_applications
		WHERE company_id = $1::uuid AND status = 'pending' ORDER BY applied_at, no`, companyID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing applications: %w", err)
	}
	defer rows.Close()
	var out []application.CompanyApplication
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// Decide moves a pending application to status.
func (r *CompanyRepository) Decide(ctx context.Context, id, status, by string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE company_applications SET status = $2, decided_at = $3, decided_by = $4::uuid
		  WHERE id = $1::uuid AND status = 'pending'`, id, status, at.UTC(), nullableUUID(by))
	if err != nil {
		return false, fmt.Errorf("postgres: deciding an application: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// WithdrawPlayerApplications withdraws a player's pending applications.
func (r *CompanyRepository) WithdrawPlayerApplications(ctx context.Context, playerID string, at time.Time) (int, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE company_applications SET status = 'withdrawn', decided_at = $2
		  WHERE player_id = $1::uuid AND status = 'pending'`, playerID, at.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: withdrawing applications: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// WithdrawCompanyApplications withdraws a company's pending applications.
func (r *CompanyRepository) WithdrawCompanyApplications(ctx context.Context, companyID string, at time.Time) (int, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE company_applications SET status = 'withdrawn', decided_at = $2
		  WHERE company_id = $1::uuid AND status = 'pending'`, companyID, at.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: withdrawing applications: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// CloseOpenings closes a company's open openings.
func (r *CompanyRepository) CloseOpenings(ctx context.Context, companyID string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE company_openings SET status = 'closed', updated_at = $2
		  WHERE company_id = $1::uuid AND status = 'open'`, companyID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: closing openings: %w", err)
	}
	return nil
}

// MarketClock reads a city's settlement clock, locked, creating an idle one
// first when the city has none: two founders racing in a new city both wait
// on the one row.
func (r *CompanyRepository) MarketClock(ctx context.Context, cityID string, now time.Time) (*application.CompanyMarketClock, error) {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO company_markets (city_id, period_no, period_started_at, updated_at)
		 VALUES ($1::uuid, 1, $2, $2) ON CONFLICT (city_id) DO NOTHING`, cityID, now.UTC()); err != nil {
		if isInvalidUUIDText(err) {
			return nil, application.ErrNoMarketClock
		}
		return nil, fmt.Errorf("postgres: opening a city's company clock: %w", err)
	}
	var c application.CompanyMarketClock
	err := r.q.QueryRow(ctx,
		`SELECT city_id::text, period_no, period_started_at, next_at, COALESCE(action_id::text, ''), updated_at
		   FROM company_markets WHERE city_id = $1::uuid FOR UPDATE`, cityID).Scan(
		&c.CityID, &c.PeriodNo, &c.PeriodStartedAt, &c.NextAt, &c.ActionID, &c.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNoMarketClock
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a city's company clock: %w", err)
	}
	c.PeriodStartedAt = c.PeriodStartedAt.UTC()
	c.UpdatedAt = c.UpdatedAt.UTC()
	if c.NextAt != nil {
		t := c.NextAt.UTC()
		c.NextAt = &t
	}
	return &c, nil
}

// NextSettlement reads when a city's companies are next settled, without a
// lock; nil when none is scheduled.
func (r *CompanyRepository) NextSettlement(ctx context.Context, cityID string) (*time.Time, error) {
	var next *time.Time
	err := r.q.QueryRow(ctx, `SELECT next_at FROM company_markets WHERE city_id = $1::uuid`, cityID).Scan(&next)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a city's next settlement: %w", err)
	}
	if next != nil {
		t := next.UTC()
		next = &t
	}
	return next, nil
}

// SaveMarketClock inserts or replaces a city's settlement clock.
func (r *CompanyRepository) SaveMarketClock(ctx context.Context, c application.CompanyMarketClock) error {
	var next *time.Time
	if c.NextAt != nil {
		t := c.NextAt.UTC()
		next = &t
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO company_markets (city_id, period_no, period_started_at, next_at, action_id, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6)
		 ON CONFLICT (city_id) DO UPDATE
		    SET period_no = EXCLUDED.period_no, period_started_at = EXCLUDED.period_started_at,
		        next_at = EXCLUDED.next_at, action_id = EXCLUDED.action_id, updated_at = EXCLUDED.updated_at`,
		c.CityID, c.PeriodNo, c.PeriodStartedAt.UTC(), next, nullableUUID(c.ActionID), c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a city's company clock: %w", err)
	}
	return nil
}

// RecordMarketPeriod appends a city's settled period.
func (r *CompanyRepository) RecordMarketPeriod(ctx context.Context, p application.CompanyMarketPeriod) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`INSERT INTO company_market_periods (city_id, period_no, started_at, ended_at, population, budget, asked,
		                                     paid, companies, settled_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT ON CONSTRAINT `+companyMarketPeriodsPkey+` DO NOTHING`,
		p.CityID, p.PeriodNo, p.StartedAt.UTC(), p.EndedAt.UTC(), p.Population, p.Budget, p.Asked, p.Paid,
		p.Companies, p.SettledAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a settled period: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordPeriod appends a company's settled period.
func (r *CompanyRepository) RecordPeriod(ctx context.Context, p application.CompanyPeriod) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO company_periods (company_id, period_no, city_id, started_at, ended_at, presence_bps, price_bps,
		                              quality_bps, shifts, wanted_units, capacity_units, sold_units, revenue,
		                              sales_tax, wages, upkeep_due, upkeep_paid, debt, balance_after, insolvent,
		                              settled_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
		         $19, $20, $21)`,
		p.CompanyID, p.PeriodNo, p.CityID, p.StartedAt.UTC(), p.EndedAt.UTC(), p.PresenceBPS, p.PriceBPS,
		p.QualityBPS, p.Shifts, p.WantedUnits, p.CapacityUnits, p.SoldUnits, p.Revenue, p.SalesTax, p.Wages,
		p.UpkeepDue, p.UpkeepPaid, p.Debt, p.BalanceAfter, p.Insolvent, p.SettledAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a company's period: %w", err)
	}
	return nil
}

// LastPeriod reads a company's latest settled period.
func (r *CompanyRepository) LastPeriod(ctx context.Context, companyID string) (*application.CompanyPeriod, error) {
	var p application.CompanyPeriod
	err := r.q.QueryRow(ctx,
		`SELECT company_id::text, period_no, city_id::text, started_at, ended_at, presence_bps, price_bps,
		        quality_bps, shifts, wanted_units, capacity_units, sold_units, revenue, sales_tax, wages,
		        upkeep_due, upkeep_paid, debt, balance_after, insolvent, settled_at
		   FROM company_periods WHERE company_id = $1::uuid ORDER BY period_no DESC LIMIT 1`, companyID).Scan(
		&p.CompanyID, &p.PeriodNo, &p.CityID, &p.StartedAt, &p.EndedAt, &p.PresenceBPS, &p.PriceBPS,
		&p.QualityBPS, &p.Shifts, &p.WantedUnits, &p.CapacityUnits, &p.SoldUnits, &p.Revenue, &p.SalesTax,
		&p.Wages, &p.UpkeepDue, &p.UpkeepPaid, &p.Debt, &p.BalanceAfter, &p.Insolvent, &p.SettledAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNoCompanyPeriod
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a company's last period: %w", err)
	}
	p.StartedAt, p.EndedAt, p.SettledAt = p.StartedAt.UTC(), p.EndedAt.UTC(), p.SettledAt.UTC()
	return &p, nil
}
