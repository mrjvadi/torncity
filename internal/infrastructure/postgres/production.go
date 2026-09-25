package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Constraint names from migrations/0020_production.up.sql mapped to
// sentinels here.
const (
	productDesignsNameIdx        = "product_designs_name_idx"
	companyResearchOneRunningIdx = "company_research_one_running_idx"
	companyResearchOnceIdx       = "company_research_once_idx"
	technologyLicensesOnceKey    = "technology_licenses_once_key"
	companyListingsOneOpenIdx    = "company_listings_one_open_idx"
)

// ProductionRepository implements application.ProductionRepository over the
// tables of migration 0020.
type ProductionRepository struct {
	q querier
}

var _ application.ProductionRepository = (*ProductionRepository)(nil)

// NewProductionRepository returns the production economy over the pool, for
// the admin tool's reads. Inside a unit of work, use Tx.Production.
func NewProductionRepository(p *Pool) *ProductionRepository { return &ProductionRepository{q: p.shared()} }

// ---------------------------------------------------------------------------
// Designs.

const designColumns = `id::text, no, company_id::text, item_code, archetype, COALESCE(name, ''), COALESCE(name_key, ''),
	origin, status, fills, quality_loss_bps, overhead_bps, COALESCE(source_design_id::text, ''),
	COALESCE(created_by::text, ''), created_at, updated_at, finalized_at`

func scanDesign(row pgx.Row) (*application.Design, error) {
	var (
		d     application.Design
		fills []byte
	)
	if err := row.Scan(&d.ID, &d.No, &d.CompanyID, &d.Item, &d.Archetype, &d.Name, &d.NameKey, &d.Origin, &d.Status,
		&fills, &d.QualityLossBPS, &d.OverheadBPS, &d.SourceDesignID, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt,
		&d.FinalizedAt); err != nil {
		return nil, err
	}
	d.Fills = map[string]application.DesignFill{}
	if len(fills) > 0 {
		if err := json.Unmarshal(fills, &d.Fills); err != nil {
			return nil, fmt.Errorf("design %s fills: %w", d.ID, err)
		}
	}
	d.CreatedAt, d.UpdatedAt, d.FinalizedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC(), utcPtr(d.FinalizedAt)
	return &d, nil
}

func (r *ProductionRepository) design(ctx context.Context, sql string, args ...any) (*application.Design, error) {
	d, err := scanDesign(r.q.QueryRow(ctx, sql, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrDesignNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a design: %w", err)
	}
	return d, nil
}

func fillsJSON(f map[string]application.DesignFill) (string, error) {
	if f == nil {
		f = map[string]application.DesignFill{}
	}
	raw, err := json.Marshal(f)
	return string(raw), err
}

// CreateDesign inserts a design.
func (r *ProductionRepository) CreateDesign(ctx context.Context, d application.Design) (application.Design, error) {
	fills, err := fillsJSON(d.Fills)
	if err != nil {
		return d, err
	}
	err = r.q.QueryRow(ctx,
		`INSERT INTO product_designs (id, company_id, item_code, archetype, name, name_key, origin, status, fills,
		                              quality_loss_bps, overhead_bps, source_design_id, created_by, created_at,
		                              updated_at, finalized_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12::uuid, $13::uuid, $14, $14, $15)
		 RETURNING no`,
		d.ID, d.CompanyID, d.Item, d.Archetype, nullableText(d.Name), nullableText(d.NameKey), d.Origin, d.Status, fills,
		d.QualityLossBPS, d.OverheadBPS, nullableUUID(d.SourceDesignID), nullableUUID(d.CreatedBy), d.CreatedAt.UTC(),
		utcPtr(d.FinalizedAt)).Scan(&d.No)
	switch {
	case violates(err, sqlstateUniqueViolation, productDesignsNameIdx):
		return d, application.ErrDesignNameTaken
	case err != nil:
		return d, fmt.Errorf("postgres: creating a design: %w", err)
	}
	d.UpdatedAt = d.CreatedAt
	return d, nil
}

// Design reads a design by number.
func (r *ProductionRepository) Design(ctx context.Context, no int64, lock bool) (*application.Design, error) {
	sql := `SELECT ` + designColumns + ` FROM product_designs WHERE no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	return r.design(ctx, sql, no)
}

// DesignByID reads a design by id.
func (r *ProductionRepository) DesignByID(ctx context.Context, id string) (*application.Design, error) {
	return r.design(ctx, `SELECT `+designColumns+` FROM product_designs WHERE id = $1::uuid`, id)
}

// Designs lists a company's live designs, newest first.
func (r *ProductionRepository) Designs(ctx context.Context, companyID string) ([]application.Design, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+designColumns+` FROM product_designs
		  WHERE company_id = $1::uuid AND status <> 'retired' ORDER BY no DESC`, companyID)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: listing designs: %w", err)
	}
	defer rows.Close()
	var out []application.Design
	for rows.Next() {
		d, err := scanDesign(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a design: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// SaveDesign writes a design's mutable state.
func (r *ProductionRepository) SaveDesign(ctx context.Context, d application.Design) error {
	fills, err := fillsJSON(d.Fills)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx,
		`UPDATE product_designs SET fills = $2::jsonb, name = $3, name_key = $4, status = $5, quality_loss_bps = $6,
		        overhead_bps = $7, updated_at = $8, finalized_at = $9
		  WHERE id = $1::uuid`,
		d.ID, fills, nullableText(d.Name), nullableText(d.NameKey), d.Status, d.QualityLossBPS, d.OverheadBPS,
		d.UpdatedAt.UTC(), utcPtr(d.FinalizedAt))
	switch {
	case violates(err, sqlstateUniqueViolation, productDesignsNameIdx):
		return application.ErrDesignNameTaken
	case err != nil:
		return fmt.Errorf("postgres: saving a design: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Research and technologies.

const researchColumns = `id::text, company_id::text, tech_code, status, cost, COALESCE(ledger_transaction_id::text, ''),
	game_action_id::text, COALESCE(started_by::text, ''), started_at, finish_at, completed_at`

func scanResearch(row pgx.Row) (*application.Research, error) {
	var r application.Research
	if err := row.Scan(&r.ID, &r.CompanyID, &r.Tech, &r.Status, &r.Cost, &r.LedgerTransactionID, &r.GameActionID,
		&r.StartedBy, &r.StartedAt, &r.FinishAt, &r.CompletedAt); err != nil {
		return nil, err
	}
	r.StartedAt, r.FinishAt, r.CompletedAt = r.StartedAt.UTC(), r.FinishAt.UTC(), utcPtr(r.CompletedAt)
	return &r, nil
}

// StartResearch records a running research.
func (r *ProductionRepository) StartResearch(ctx context.Context, rs application.Research) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO company_research (id, company_id, tech_code, status, cost, ledger_transaction_id, game_action_id,
		                               started_by, started_at, finish_at)
		 VALUES ($1::uuid, $2::uuid, $3, 'running', $4, $5::uuid, $6::uuid, $7::uuid, $8, $9)`,
		rs.ID, rs.CompanyID, rs.Tech, rs.Cost, nullableUUID(rs.LedgerTransactionID), rs.GameActionID,
		nullableUUID(rs.StartedBy), rs.StartedAt.UTC(), rs.FinishAt.UTC())
	switch {
	case violates(err, sqlstateUniqueViolation, companyResearchOneRunningIdx):
		return application.ErrResearchBusy
	case violates(err, sqlstateUniqueViolation, companyResearchOnceIdx):
		return application.ErrAlreadyResearched
	case err != nil:
		return fmt.Errorf("postgres: starting research: %w", err)
	}
	return nil
}

// Research reads one research, locked.
func (r *ProductionRepository) Research(ctx context.Context, id string) (*application.Research, error) {
	rs, err := scanResearch(r.q.QueryRow(ctx, `SELECT `+researchColumns+` FROM company_research WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrResearchNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading research: %w", err)
	}
	return rs, nil
}

// RunningResearch reads a company's running research, or nil.
func (r *ProductionRepository) RunningResearch(ctx context.Context, companyID string) (*application.Research, error) {
	rs, err := scanResearch(r.q.QueryRow(ctx,
		`SELECT `+researchColumns+` FROM company_research WHERE company_id = $1::uuid AND status = 'running'`, companyID))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres: reading running research: %w", err)
	}
	return rs, nil
}

// FinishResearch marks a research done.
func (r *ProductionRepository) FinishResearch(ctx context.Context, id string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE company_research SET status = 'done', completed_at = $2 WHERE id = $1::uuid AND status = 'running'`,
		id, at.UTC()); err != nil {
		return fmt.Errorf("postgres: finishing research: %w", err)
	}
	return nil
}

const techColumns = `company_id::text, tech_code, mode, COALESCE(license_price, 0), research_id::text, acquired_at,
	updated_at, published_at`

func scanTech(row pgx.Row) (*application.CompanyTech, error) {
	var t application.CompanyTech
	if err := row.Scan(&t.CompanyID, &t.Tech, &t.Mode, &t.LicensePrice, &t.ResearchID, &t.AcquiredAt, &t.UpdatedAt,
		&t.PublishedAt); err != nil {
		return nil, err
	}
	t.AcquiredAt, t.UpdatedAt, t.PublishedAt = t.AcquiredAt.UTC(), t.UpdatedAt.UTC(), utcPtr(t.PublishedAt)
	return &t, nil
}

func licensePrice(t application.CompanyTech) *int64 {
	if t.Mode != "license" {
		return nil
	}
	return &t.LicensePrice
}

// AddTechnology records an owned technology.
func (r *ProductionRepository) AddTechnology(ctx context.Context, t application.CompanyTech) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO company_technologies (company_id, tech_code, mode, license_price, research_id, acquired_at,
		                                   updated_at, published_at)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6, $6, $7)`,
		t.CompanyID, t.Tech, t.Mode, licensePrice(t), t.ResearchID, t.AcquiredAt.UTC(), utcPtr(t.PublishedAt)); err != nil {
		return fmt.Errorf("postgres: recording a technology: %w", err)
	}
	return nil
}

// Technologies lists what a company owns.
func (r *ProductionRepository) Technologies(ctx context.Context, companyID string) ([]application.CompanyTech, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+techColumns+` FROM company_technologies WHERE company_id = $1::uuid ORDER BY acquired_at, tech_code`, companyID)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: listing technologies: %w", err)
	}
	defer rows.Close()
	var out []application.CompanyTech
	for rows.Next() {
		t, err := scanTech(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a technology: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Technology reads one owned technology, locked.
func (r *ProductionRepository) Technology(ctx context.Context, companyID, tech string) (*application.CompanyTech, error) {
	t, err := scanTech(r.q.QueryRow(ctx,
		`SELECT `+techColumns+` FROM company_technologies WHERE company_id = $1::uuid AND tech_code = $2 FOR UPDATE`,
		companyID, tech))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrTechnologyNotOwned
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a technology: %w", err)
	}
	return t, nil
}

// SaveTechnology writes how a technology is shared. The trigger of
// migration 0020 refuses to unpublish, whatever the caller did.
func (r *ProductionRepository) SaveTechnology(ctx context.Context, t application.CompanyTech) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE company_technologies SET mode = $3, license_price = $4, updated_at = $5, published_at = $6
		  WHERE company_id = $1::uuid AND tech_code = $2`,
		t.CompanyID, t.Tech, t.Mode, licensePrice(t), t.UpdatedAt.UTC(), utcPtr(t.PublishedAt)); err != nil {
		return fmt.Errorf("postgres: saving a technology: %w", err)
	}
	return nil
}

// Published lists every published technology.
func (r *ProductionRepository) Published(ctx context.Context) ([]string, error) {
	rows, err := r.q.Query(ctx, `SELECT DISTINCT tech_code FROM company_technologies WHERE mode = 'published' ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing published technologies: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Offers lists a technology's owners among active companies.
func (r *ProductionRepository) Offers(ctx context.Context, tech string) ([]application.TechOffer, error) {
	rows, err := r.q.Query(ctx,
		`SELECT t.company_id::text, t.tech_code, t.mode, COALESCE(t.license_price, 0), t.research_id::text, t.acquired_at,
		        t.updated_at, t.published_at, `+companyColumnsC+`
		   FROM company_technologies t JOIN companies c ON c.id = t.company_id
		  WHERE t.tech_code = $1 AND c.status = 'active'
		  ORDER BY (t.mode = 'license') DESC, t.license_price NULLS LAST, c.founded_at`, tech)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a technology's owners: %w", err)
	}
	defer rows.Close()
	var out []application.TechOffer
	for rows.Next() {
		var (
			o application.TechOffer
			c = &o.Company
		)
		if err := rows.Scan(&o.Tech.CompanyID, &o.Tech.Tech, &o.Tech.Mode, &o.Tech.LicensePrice, &o.Tech.ResearchID,
			&o.Tech.AcquiredAt, &o.Tech.UpdatedAt, &o.Tech.PublishedAt,
			&c.ID, &c.Code, &c.Name, &c.NameKey, &c.TypeCode, &c.CityID, &c.OwnerID,
			&c.ManagerID, &c.Status, &c.PriceBPS, &c.AutoAccept, &c.TotalShares, &c.Debt, &c.Arrears,
			&c.RatingBPS, &c.RegistrationFee, &c.RegistrationTransactionID, &c.ContentVersion,
			&c.FoundedAt, &c.UpdatedAt, &c.ClosedAt, &c.CloseReason); err != nil {
			return nil, fmt.Errorf("postgres: scanning an offer: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GrantLicense records a license.
func (r *ProductionRepository) GrantLicense(ctx context.Context, l application.License) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO technology_licenses (id, tech_code, licensor_company_id, licensee_company_id, price,
		                                  ledger_transaction_id, bought_by, granted_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6::uuid, $7::uuid, $8)`,
		l.ID, l.Tech, l.LicensorID, l.LicenseeID, l.Price, l.LedgerTransactionID, nullableUUID(l.BoughtBy), l.GrantedAt.UTC())
	switch {
	case violates(err, sqlstateUniqueViolation, technologyLicensesOnceKey):
		return application.ErrAlreadyLicensed
	case err != nil:
		return fmt.Errorf("postgres: granting a license: %w", err)
	}
	return nil
}

// Licenses lists the licenses a company holds.
func (r *ProductionRepository) Licenses(ctx context.Context, companyID string) ([]application.License, error) {
	rows, err := r.q.Query(ctx,
		`SELECT id::text, tech_code, licensor_company_id::text, licensee_company_id::text, price,
		        ledger_transaction_id::text, COALESCE(bought_by::text, ''), granted_at
		   FROM technology_licenses WHERE licensee_company_id = $1::uuid ORDER BY granted_at`, companyID)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: listing licenses: %w", err)
	}
	defer rows.Close()
	var out []application.License
	for rows.Next() {
		var l application.License
		if err := rows.Scan(&l.ID, &l.Tech, &l.LicensorID, &l.LicenseeID, &l.Price, &l.LedgerTransactionID,
			&l.BoughtBy, &l.GrantedAt); err != nil {
			return nil, err
		}
		l.GrantedAt = l.GrantedAt.UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// LicensesSold counts a technology's licenses a company sold.
func (r *ProductionRepository) LicensesSold(ctx context.Context, companyID, tech string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM technology_licenses WHERE licensor_company_id = $1::uuid AND tech_code = $2`,
		companyID, tech).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting licenses: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Production orders.

const productionOrderColumns = `id::text, no, company_id::text, kind, COALESCE(design_id::text, ''), output_code, quantity,
	output_qty, workers, input_quality, skill_level, consumed, status, COALESCE(quality, 0), game_action_id::text,
	COALESCE(placed_by::text, ''), started_at, finish_at, completed_at`

func scanProductionOrder(row pgx.Row) (*application.ProductionOrder, error) {
	var (
		o        application.ProductionOrder
		consumed []byte
	)
	if err := row.Scan(&o.ID, &o.No, &o.CompanyID, &o.Kind, &o.DesignID, &o.Output, &o.Quantity, &o.OutputQty,
		&o.Workers, &o.InputQuality, &o.SkillLevel, &consumed, &o.Status, &o.Quality, &o.GameActionID, &o.PlacedBy,
		&o.StartedAt, &o.FinishAt, &o.CompletedAt); err != nil {
		return nil, err
	}
	o.Consumed = map[string]int64{}
	if len(consumed) > 0 {
		if err := json.Unmarshal(consumed, &o.Consumed); err != nil {
			return nil, fmt.Errorf("order %s consumed: %w", o.ID, err)
		}
	}
	o.StartedAt, o.FinishAt, o.CompletedAt = o.StartedAt.UTC(), o.FinishAt.UTC(), utcPtr(o.CompletedAt)
	return &o, nil
}

// PlaceOrder records a running order.
func (r *ProductionRepository) PlaceOrder(ctx context.Context, o application.ProductionOrder) (application.ProductionOrder, error) {
	consumed, err := json.Marshal(o.Consumed)
	if err != nil {
		return o, err
	}
	if err := r.q.QueryRow(ctx,
		`INSERT INTO production_orders (id, company_id, kind, design_id, output_code, quantity, output_qty, workers,
		                                input_quality, skill_level, consumed, status, game_action_id, placed_by,
		                                started_at, finish_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, $11::jsonb, 'running', $12::uuid, $13::uuid,
		         $14, $15)
		 RETURNING no`,
		o.ID, o.CompanyID, o.Kind, nullableUUID(o.DesignID), o.Output, o.Quantity, o.OutputQty, o.Workers,
		o.InputQuality, o.SkillLevel, string(consumed), o.GameActionID, nullableUUID(o.PlacedBy), o.StartedAt.UTC(),
		o.FinishAt.UTC()).Scan(&o.No); err != nil {
		return o, fmt.Errorf("postgres: placing a production order: %w", err)
	}
	o.Status = application.OrderRunning
	return o, nil
}

// Order reads one order, locked.
func (r *ProductionRepository) Order(ctx context.Context, id string) (*application.ProductionOrder, error) {
	o, err := scanProductionOrder(r.q.QueryRow(ctx, `SELECT `+productionOrderColumns+` FROM production_orders WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrProductionOrderNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a production order: %w", err)
	}
	return o, nil
}

// Orders lists a company's orders.
func (r *ProductionRepository) Orders(ctx context.Context, companyID string, limit int) ([]application.ProductionOrder, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+productionOrderColumns+` FROM production_orders WHERE company_id = $1::uuid
		  ORDER BY (status = 'running') DESC, started_at DESC LIMIT $2`, companyID, limit)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: listing production orders: %w", err)
	}
	defer rows.Close()
	var out []application.ProductionOrder
	for rows.Next() {
		o, err := scanProductionOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a production order: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// RunningOrders counts a company's running orders.
func (r *ProductionRepository) RunningOrders(ctx context.Context, companyID string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM production_orders WHERE company_id = $1::uuid AND status = 'running'`, companyID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting running orders: %w", err)
	}
	return n, nil
}

// FinishOrder marks an order done.
func (r *ProductionRepository) FinishOrder(ctx context.Context, id string, quality int, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE production_orders SET status = 'done', quality = $2, completed_at = $3 WHERE id = $1::uuid AND status = 'running'`,
		id, quality, at.UTC()); err != nil {
		return fmt.Errorf("postgres: finishing a production order: %w", err)
	}
	return nil
}

// DoneOrders counts a design's finished orders.
func (r *ProductionRepository) DoneOrders(ctx context.Context, designID string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM production_orders WHERE design_id = $1::uuid AND status = 'done'`, designID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting a design's orders: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Reverse engineering.

const reverseColumns = `id::text, no, company_id::text, piece_id::text, source_design_id::text, item_code,
	COALESCE(engineer_id::text, ''), skill, level, chance_bps, status, COALESCE(result_design_id::text, ''),
	game_action_id::text, COALESCE(started_by::text, ''), started_at, finish_at, completed_at`

func scanReverse(row pgx.Row) (*application.ReverseJob, error) {
	var j application.ReverseJob
	if err := row.Scan(&j.ID, &j.No, &j.CompanyID, &j.PieceID, &j.SourceDesignID, &j.Item, &j.EngineerID, &j.Skill,
		&j.Level, &j.ChanceBPS, &j.Status, &j.ResultDesignID, &j.GameActionID, &j.StartedBy, &j.StartedAt,
		&j.FinishAt, &j.CompletedAt); err != nil {
		return nil, err
	}
	j.StartedAt, j.FinishAt, j.CompletedAt = j.StartedAt.UTC(), j.FinishAt.UTC(), utcPtr(j.CompletedAt)
	return &j, nil
}

// StartReverse records a running reverse engineering.
func (r *ProductionRepository) StartReverse(ctx context.Context, j application.ReverseJob) (application.ReverseJob, error) {
	if err := r.q.QueryRow(ctx,
		`INSERT INTO reverse_jobs (id, company_id, piece_id, source_design_id, item_code, engineer_id, skill, level,
		                           chance_bps, status, game_action_id, started_by, started_at, finish_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9, 'running', $10::uuid, $11::uuid, $12, $13)
		 RETURNING no`,
		j.ID, j.CompanyID, j.PieceID, j.SourceDesignID, j.Item, nullableUUID(j.EngineerID), j.Skill, j.Level, j.ChanceBPS,
		j.GameActionID, nullableUUID(j.StartedBy), j.StartedAt.UTC(), j.FinishAt.UTC()).Scan(&j.No); err != nil {
		return j, fmt.Errorf("postgres: starting reverse engineering: %w", err)
	}
	j.Status = application.ReverseRunning
	return j, nil
}

// Reverse reads one reverse engineering, locked.
func (r *ProductionRepository) Reverse(ctx context.Context, id string) (*application.ReverseJob, error) {
	j, err := scanReverse(r.q.QueryRow(ctx, `SELECT `+reverseColumns+` FROM reverse_jobs WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrReverseNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading reverse engineering: %w", err)
	}
	return j, nil
}

// Reverses lists a company's reverse engineering.
func (r *ProductionRepository) Reverses(ctx context.Context, companyID string, limit int) ([]application.ReverseJob, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+reverseColumns+` FROM reverse_jobs WHERE company_id = $1::uuid
		  ORDER BY (status = 'running') DESC, started_at DESC LIMIT $2`, companyID, limit)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: listing reverse engineering: %w", err)
	}
	defer rows.Close()
	var out []application.ReverseJob
	for rows.Next() {
		j, err := scanReverse(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning reverse engineering: %w", err)
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// FinishReverse records how a reverse engineering ended.
func (r *ProductionRepository) FinishReverse(ctx context.Context, id, status, resultDesignID string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE reverse_jobs SET status = $2, result_design_id = $3::uuid, completed_at = $4
		  WHERE id = $1::uuid AND status = 'running'`,
		id, status, nullableUUID(resultDesignID), at.UTC()); err != nil {
		return fmt.Errorf("postgres: finishing reverse engineering: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Listings, sales and supplies.

const listingColumns = `id::text, no, company_id::text, city_id::text, item_code, COALESCE(design_id::text, ''),
	quantity, sold, unit_price, status, created_at, updated_at, closed_at`

func scanListing(row pgx.Row) (*application.Listing, error) {
	var l application.Listing
	if err := row.Scan(&l.ID, &l.No, &l.CompanyID, &l.CityID, &l.Item, &l.DesignID, &l.Qty, &l.Sold, &l.UnitPrice,
		&l.Status, &l.CreatedAt, &l.UpdatedAt, &l.ClosedAt); err != nil {
		return nil, err
	}
	l.CreatedAt, l.UpdatedAt, l.ClosedAt = l.CreatedAt.UTC(), l.UpdatedAt.UTC(), utcPtr(l.ClosedAt)
	return &l, nil
}

func (r *ProductionRepository) listings(ctx context.Context, sql string, args ...any) ([]application.Listing, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: listing listings: %w", err)
	}
	defer rows.Close()
	var out []application.Listing
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a listing: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// OpenListing records an open listing.
func (r *ProductionRepository) OpenListing(ctx context.Context, l application.Listing) (application.Listing, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO company_listings (id, company_id, city_id, item_code, design_id, quantity, sold, unit_price, status,
		                               created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, 0, $7, 'open', $8, $8) RETURNING no`,
		l.ID, l.CompanyID, l.CityID, l.Item, nullableUUID(l.DesignID), l.Qty, l.UnitPrice, l.CreatedAt.UTC()).Scan(&l.No)
	switch {
	case violates(err, sqlstateUniqueViolation, companyListingsOneOpenIdx):
		return l, application.ErrListingOpen
	case err != nil:
		return l, fmt.Errorf("postgres: opening a listing: %w", err)
	}
	l.Status, l.UpdatedAt = application.ListingOpen, l.CreatedAt
	return l, nil
}

// Listing reads a listing by number.
func (r *ProductionRepository) Listing(ctx context.Context, no int64, lock bool) (*application.Listing, error) {
	sql := `SELECT ` + listingColumns + ` FROM company_listings WHERE no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanListing(r.q.QueryRow(ctx, sql, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrListingNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a listing: %w", err)
	}
	return l, nil
}

// CompanyListings lists a company's open listings.
func (r *ProductionRepository) CompanyListings(ctx context.Context, companyID string) ([]application.Listing, error) {
	return r.listings(ctx, `SELECT `+listingColumns+` FROM company_listings
		  WHERE company_id = $1::uuid AND status = 'open' ORDER BY no`, companyID)
}

// CityListings lists the open listings of a city's active companies.
func (r *ProductionRepository) CityListings(ctx context.Context, cityID string) ([]application.Listing, error) {
	return r.listings(ctx, `SELECT l.id::text, l.no, l.company_id::text, l.city_id::text, l.item_code,
		        COALESCE(l.design_id::text, ''), l.quantity, l.sold, l.unit_price, l.status, l.created_at, l.updated_at,
		        l.closed_at
		   FROM company_listings l JOIN companies c ON c.id = l.company_id
		  WHERE l.city_id = $1::uuid AND l.status = 'open' AND c.status = 'active'
		  ORDER BY l.item_code, l.unit_price, l.no`, cityID)
}

// SaveListing writes a listing's sold count and status.
func (r *ProductionRepository) SaveListing(ctx context.Context, l application.Listing) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE company_listings SET sold = $2, status = $3, updated_at = $4, closed_at = $5 WHERE id = $1::uuid`,
		l.ID, l.Sold, l.Status, l.UpdatedAt.UTC(), utcPtr(l.ClosedAt)); err != nil {
		return fmt.Errorf("postgres: saving a listing: %w", err)
	}
	return nil
}

// RecordSale appends a sale.
func (r *ProductionRepository) RecordSale(ctx context.Context, s application.CompanySale) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO company_sales (id, listing_id, company_id, buyer_player_id, buyer_org_kind, buyer_org_id, item_code,
		                            quantity, unit_price, total, tax, method, ledger_transaction_id, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9, $10, $11, $12, $13::uuid, $14)`,
		s.ID, s.ListingID, s.CompanyID, nullableUUID(s.BuyerPlayerID), nullableText(s.BuyerOrg.Kind),
		nullableUUID(s.BuyerOrg.ID), s.Item, s.Qty, s.UnitPrice, s.Total, s.Tax, nullableText(s.Method),
		s.LedgerTransactionID, s.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a company sale: %w", err)
	}
	return nil
}

// RecordSupply appends a supplier purchase.
func (r *ProductionRepository) RecordSupply(ctx context.Context, s application.SupplyPurchase) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO supply_purchases (id, company_id, city_id, supplier_code, component, quantity, unit_price, total,
		                               ledger_transaction_id, bought_by, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9::uuid, $10::uuid, $11)`,
		s.ID, s.CompanyID, s.CityID, s.Supplier, s.Component, s.Qty, s.UnitPrice, s.Total, s.LedgerTransactionID,
		nullableUUID(s.BoughtBy), s.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a supply purchase: %w", err)
	}
	return nil
}
