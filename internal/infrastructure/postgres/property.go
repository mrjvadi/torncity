package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists property (migrations/0025_property): the units a city
// sold, their listings and leases, each period's charges and rent, a
// player's residence and their last rest at home.

// PropertyRepository implements application.PropertyRepository.
type PropertyRepository struct {
	q querier
}

var _ application.PropertyRepository = (*PropertyRepository)(nil)

// propertyLockClass is the first key of the advisory lock that serialises a
// city's sales of one type (0025).
const propertyLockClass = 25

const propertyColumns = `id::text, no, city_id::text, type_code, COALESCE(owner_player_id::text, ''), status, value,
       tax_debt, upkeep_debt, unpaid_periods, acquired_at, repossessed_at, content_version`

func scanProperty(row pgx.Row) (*application.Property, error) {
	var p application.Property
	if err := row.Scan(&p.ID, &p.No, &p.CityID, &p.TypeCode, &p.OwnerID, &p.Status, &p.Value, &p.TaxDebt,
		&p.UpkeepDebt, &p.UnpaidPeriods, &p.AcquiredAt, &p.RepossessedAt, &p.ContentVersion); err != nil {
		return nil, err
	}
	p.AcquiredAt = p.AcquiredAt.UTC()
	return &p, nil
}

func (r *PropertyRepository) properties(ctx context.Context, query string, args ...any) ([]application.Property, error) {
	rows, err := r.q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading properties: %w", err)
	}
	defer rows.Close()
	var out []application.Property
	for rows.Next() {
		p, err := scanProperty(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a property: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading properties: %w", err)
	}
	return out, nil
}

// LockStock takes the advisory lock of a city's sales of one type.
func (r *PropertyRepository) LockStock(ctx context.Context, cityID, typeCode string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, propertyLockClass,
		cityID+"/"+typeCode); err != nil {
		return fmt.Errorf("postgres: locking a city's property stock: %w", err)
	}
	return nil
}

// Owned counts a type's units sold and owned in a city.
func (r *PropertyRepository) Owned(ctx context.Context, cityID, typeCode string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM properties WHERE city_id = $1::uuid AND type_code = $2
	   AND status = 'owned'`, cityID, typeCode).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting properties: %w", err)
	}
	return n, nil
}

// OwnedBy counts a player's properties.
func (r *PropertyRepository) OwnedBy(ctx context.Context, playerID string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM properties WHERE owner_player_id = $1::uuid AND status = 'owned'`,
		playerID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting a player's properties: %w", err)
	}
	return n, nil
}

// Create writes a sold unit.
func (r *PropertyRepository) Create(ctx context.Context, p application.Property) (application.Property, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO properties (id, city_id, type_code, owner_player_id, status, value, tax_debt, upkeep_debt,
		        unpaid_periods, acquired_at, content_version)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, 'owned', $5, 0, 0, 0, $6, $7) RETURNING no`,
		p.ID, p.CityID, p.TypeCode, p.OwnerID, p.Value, p.AcquiredAt.UTC(), p.ContentVersion).Scan(&p.No)
	if err != nil {
		return p, fmt.Errorf("postgres: writing a property: %w", err)
	}
	p.Status = application.PropertyOwned
	return p, nil
}

func (r *PropertyRepository) one(ctx context.Context, where string, arg any, lock bool) (*application.Property, error) {
	query := `SELECT ` + propertyColumns + ` FROM properties WHERE ` + where
	if lock {
		query += ` FOR UPDATE`
	}
	p, err := scanProperty(r.q.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrPropertyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a property: %w", err)
	}
	return p, nil
}

// ByNo reads one by its public number.
func (r *PropertyRepository) ByNo(ctx context.Context, no int64, lock bool) (*application.Property, error) {
	return r.one(ctx, `no = $1`, no, lock)
}

// ByID reads one by id.
func (r *PropertyRepository) ByID(ctx context.Context, id string, lock bool) (*application.Property, error) {
	if !validUUID(id) {
		return nil, application.ErrPropertyNotFound
	}
	return r.one(ctx, `id = $1::uuid`, id, lock)
}

// OfOwner lists what a player owns.
func (r *PropertyRepository) OfOwner(ctx context.Context, playerID string) ([]application.Property, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	return r.properties(ctx, `SELECT `+propertyColumns+` FROM properties
	  WHERE owner_player_id = $1::uuid AND status = 'owned' ORDER BY acquired_at, no`, playerID)
}

// InCity lists a city's owned properties, locked.
func (r *PropertyRepository) InCity(ctx context.Context, cityID string) ([]application.Property, error) {
	return r.properties(ctx, `SELECT `+propertyColumns+` FROM properties
	  WHERE city_id = $1::uuid AND status = 'owned' ORDER BY no FOR UPDATE`, cityID)
}

// Save writes a property.
func (r *PropertyRepository) Save(ctx context.Context, p application.Property) error {
	var owner any
	if p.OwnerID != "" {
		owner = p.OwnerID
	}
	if _, err := r.q.Exec(ctx,
		`UPDATE properties SET owner_player_id = $2::uuid, status = $3, value = $4, tax_debt = $5, upkeep_debt = $6,
		        unpaid_periods = $7, acquired_at = $8, repossessed_at = $9
		  WHERE id = $1::uuid`,
		p.ID, owner, p.Status, p.Value, p.TaxDebt, p.UpkeepDebt, p.UnpaidPeriods, p.AcquiredAt.UTC(), p.RepossessedAt); err != nil {
		return fmt.Errorf("postgres: saving a property: %w", err)
	}
	return nil
}

const offerColumns = `id::text, no, property_id::text, seller_player_id::text, kind, price, status,
       COALESCE(buyer_player_id::text, ''), created_at, closed_at`

func scanOffer(row pgx.Row) (*application.PropertyListing, error) {
	var l application.PropertyListing
	if err := row.Scan(&l.ID, &l.No, &l.PropertyID, &l.SellerID, &l.Kind, &l.Price, &l.Status, &l.BuyerID,
		&l.CreatedAt, &l.ClosedAt); err != nil {
		return nil, err
	}
	l.CreatedAt = l.CreatedAt.UTC()
	return &l, nil
}

// OpenListing writes a new listing.
func (r *PropertyRepository) OpenListing(ctx context.Context, l application.PropertyListing) (application.PropertyListing, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO property_listings (id, property_id, seller_player_id, kind, price, status, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'open', $6) RETURNING no`,
		l.ID, l.PropertyID, l.SellerID, l.Kind, l.Price, l.CreatedAt.UTC()).Scan(&l.No)
	if violates(err, sqlstateUniqueViolation, "property_listings_one_open_idx") {
		return l, application.ErrOfferExists
	}
	if err != nil {
		return l, fmt.Errorf("postgres: writing a listing: %w", err)
	}
	l.Status = application.OfferOpen
	return l, nil
}

// ListingOf returns the property's open listing.
func (r *PropertyRepository) ListingOf(ctx context.Context, propertyID string) (*application.PropertyListing, error) {
	l, err := scanOffer(r.q.QueryRow(ctx, `SELECT `+offerColumns+` FROM property_listings
	  WHERE property_id = $1::uuid AND status = 'open'`, propertyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a listing: %w", err)
	}
	return l, nil
}

// ListingByNo reads one listing by number.
func (r *PropertyRepository) ListingByNo(ctx context.Context, no int64, lock bool) (*application.PropertyListing, error) {
	query := `SELECT ` + offerColumns + ` FROM property_listings WHERE no = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	l, err := scanOffer(r.q.QueryRow(ctx, query, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrOfferNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a listing: %w", err)
	}
	return l, nil
}

// Listings returns a city's open listings.
func (r *PropertyRepository) Listings(ctx context.Context, cityID string, limit int) ([]application.PropertyListing, error) {
	if !validUUID(cityID) || limit < 1 {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+offerColumns+` FROM property_listings l
	  WHERE l.status = 'open' AND l.property_id IN (SELECT id FROM properties WHERE city_id = $1::uuid)
	  ORDER BY l.created_at DESC, l.no DESC LIMIT $2`, cityID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing listings: %w", err)
	}
	defer rows.Close()
	var out []application.PropertyListing
	for rows.Next() {
		l, err := scanOffer(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a listing: %w", err)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing listings: %w", err)
	}
	return out, nil
}

// CloseListing closes an open listing.
func (r *PropertyRepository) CloseListing(ctx context.Context, id, status, buyerID string, at time.Time) (bool, error) {
	var buyer any
	if buyerID != "" {
		buyer = buyerID
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE property_listings SET status = $2, buyer_player_id = $3::uuid, closed_at = $4
		  WHERE id = $1::uuid AND status = 'open'`, id, status, buyer, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: closing a listing: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const leaseColumns = `id::text, no, property_id::text, landlord_player_id::text, tenant_player_id::text, rent, status,
       arrears, started_at, ended_at, COALESCE(end_reason, '')`

func scanLease(row pgx.Row) (*application.PropertyLease, error) {
	var l application.PropertyLease
	if err := row.Scan(&l.ID, &l.No, &l.PropertyID, &l.LandlordID, &l.TenantID, &l.Rent, &l.Status, &l.Arrears,
		&l.StartedAt, &l.EndedAt, &l.EndReason); err != nil {
		return nil, err
	}
	l.StartedAt = l.StartedAt.UTC()
	return &l, nil
}

func (r *PropertyRepository) lease(ctx context.Context, query string, arg any) (*application.PropertyLease, error) {
	l, err := scanLease(r.q.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a lease: %w", err)
	}
	return l, nil
}

// StartLease writes a new lease.
func (r *PropertyRepository) StartLease(ctx context.Context, l application.PropertyLease) (application.PropertyLease, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO property_leases (id, property_id, landlord_player_id, tenant_player_id, rent, status, arrears, started_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, 'active', 0, $6) RETURNING no`,
		l.ID, l.PropertyID, l.LandlordID, l.TenantID, l.Rent, l.StartedAt.UTC()).Scan(&l.No)
	if violates(err, sqlstateUniqueViolation, "property_leases_one_active_idx") ||
		violates(err, sqlstateUniqueViolation, "property_leases_one_home_idx") {
		return l, application.ErrLeaseExists
	}
	if err != nil {
		return l, fmt.Errorf("postgres: writing a lease: %w", err)
	}
	l.Status = application.LeaseActive
	return l, nil
}

// LeaseOf returns the property's active lease.
func (r *PropertyRepository) LeaseOf(ctx context.Context, propertyID string) (*application.PropertyLease, error) {
	return r.lease(ctx, `SELECT `+leaseColumns+` FROM property_leases WHERE property_id = $1::uuid AND status = 'active'`,
		propertyID)
}

// TenantLease returns the player's active lease as a tenant.
func (r *PropertyRepository) TenantLease(ctx context.Context, playerID string) (*application.PropertyLease, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	return r.lease(ctx, `SELECT `+leaseColumns+` FROM property_leases WHERE tenant_player_id = $1::uuid AND status = 'active'`,
		playerID)
}

// LeaseByNo reads one lease by number.
func (r *PropertyRepository) LeaseByNo(ctx context.Context, no int64, lock bool) (*application.PropertyLease, error) {
	query := `SELECT ` + leaseColumns + ` FROM property_leases WHERE no = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	return r.lease(ctx, query, no)
}

// LeasesInCity lists a city's active leases, locked.
func (r *PropertyRepository) LeasesInCity(ctx context.Context, cityID string) ([]application.PropertyLease, error) {
	rows, err := r.q.Query(ctx, `SELECT `+leaseColumns+` FROM property_leases
	  WHERE status = 'active' AND property_id IN (SELECT id FROM properties WHERE city_id = $1::uuid)
	  ORDER BY no FOR UPDATE`, cityID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading leases: %w", err)
	}
	defer rows.Close()
	var out []application.PropertyLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a lease: %w", err)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading leases: %w", err)
	}
	return out, nil
}

// SaveLease writes a lease.
func (r *PropertyRepository) SaveLease(ctx context.Context, l application.PropertyLease) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE property_leases SET status = $2, arrears = $3, ended_at = $4, end_reason = NULLIF($5, '')
		  WHERE id = $1::uuid`, l.ID, l.Status, l.Arrears, l.EndedAt, l.EndReason); err != nil {
		return fmt.Errorf("postgres: saving a lease: %w", err)
	}
	return nil
}

// RecordCharge appends a period's charge of a property.
func (r *PropertyRepository) RecordCharge(ctx context.Context, c application.PropertyCharge) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO property_charges (property_id, period_no, city_id, owner_player_id, upkeep, tax, upkeep_paid, tax_paid,
		        upkeep_debt, tax_debt, foreclosed, charged_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12)`,
		c.PropertyID, c.PeriodNo, c.CityID, c.OwnerID, c.Upkeep, c.Tax, c.UpkeepPaid, c.TaxPaid, c.UpkeepDebt, c.TaxDebt,
		c.Foreclosed, c.ChargedAt.UTC())
	if violates(err, sqlstateUniqueViolation, "property_charges_pkey") {
		return application.ErrPeriodSettled
	}
	if err != nil {
		return fmt.Errorf("postgres: recording a property's charge: %w", err)
	}
	return nil
}

// RecordRent appends a period's rent of a lease.
func (r *PropertyRepository) RecordRent(ctx context.Context, p application.RentPayment) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO rent_payments (lease_id, period_no, rent, paid, evicted, at) VALUES ($1::uuid, $2, $3, $4, $5, $6)`,
		p.LeaseID, p.PeriodNo, p.Rent, p.Paid, p.Evicted, p.At.UTC())
	if violates(err, sqlstateUniqueViolation, "rent_payments_pkey") {
		return application.ErrPeriodSettled
	}
	if err != nil {
		return fmt.Errorf("postgres: recording rent: %w", err)
	}
	return nil
}

// RentRecorded reports whether a lease's rent of a period is recorded.
func (r *PropertyRepository) RentRecorded(ctx context.Context, leaseID string, periodNo int64) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rent_payments WHERE lease_id = $1::uuid AND period_no = $2)`,
		leaseID, periodNo).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: reading rent: %w", err)
	}
	return ok, nil
}

// SetResidence moves a player's residence.
func (r *PropertyRepository) SetResidence(ctx context.Context, playerID, cityID string, since time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE players SET residence_city_id = $2::uuid, residence_since = $3 WHERE id = $1::uuid`,
		playerID, cityID, since.UTC()); err != nil {
		return fmt.Errorf("postgres: moving a residence: %w", err)
	}
	return nil
}

// ResidenceSince is when the player began living where they live.
func (r *PropertyRepository) ResidenceSince(ctx context.Context, playerID string) (*time.Time, error) {
	var at *time.Time
	if err := r.q.QueryRow(ctx, `SELECT residence_since FROM players WHERE id = $1::uuid`, playerID).Scan(&at); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a residence: %w", err)
	}
	return at, nil
}

// RestedAt is when the player last rested at home.
func (r *PropertyRepository) RestedAt(ctx context.Context, playerID string) (*time.Time, error) {
	var at time.Time
	err := r.q.QueryRow(ctx, `SELECT rested_at FROM home_rests WHERE player_id = $1::uuid`, playerID).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a rest: %w", err)
	}
	at = at.UTC()
	return &at, nil
}

// SetRested records a rest.
func (r *PropertyRepository) SetRested(ctx context.Context, playerID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO home_rests (player_id, rested_at) VALUES ($1::uuid, $2)
	   ON CONFLICT (player_id) DO UPDATE SET rested_at = EXCLUDED.rested_at`, playerID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a rest: %w", err)
	}
	return nil
}
