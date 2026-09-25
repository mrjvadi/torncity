package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists the defence licences (migrations/0027_defence_licences,
// docs/adr/0022-military-and-diplomacy.md section 2.14): a defence company's
// licence and a civilian contractor's, from application to revocation.

const defenceLicencesOpenKey = "defence_licences_open_key"

const licenceColumns = `l.id::text, l.no, l.company_id::text, l.kind, l.basis, l.status, COALESCE(l.applied_by::text, ''),
	l.applied_at, COALESCE(l.decided_by::text, ''), COALESCE(l.decided_office, ''), l.decided_at,
	COALESCE(l.revoked_by::text, ''), COALESCE(l.revoked_office, ''), l.revoked_at, l.effective_at, l.updated_at`

func scanLicence(row pgx.Row, extra ...any) (*application.DefenceLicence, error) {
	var l application.DefenceLicence
	dest := append([]any{&l.ID, &l.No, &l.CompanyID, &l.Kind, &l.Basis, &l.Status, &l.AppliedBy, &l.AppliedAt,
		&l.DecidedBy, &l.DecidedOffice, &l.DecidedAt, &l.RevokedBy, &l.RevokedOffice, &l.RevokedAt, &l.EffectiveAt,
		&l.UpdatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	l.AppliedAt, l.UpdatedAt = l.AppliedAt.UTC(), l.UpdatedAt.UTC()
	for _, t := range []**time.Time{&l.DecidedAt, &l.RevokedAt, &l.EffectiveAt} {
		if *t != nil {
			u := (*t).UTC()
			*t = &u
		}
	}
	return &l, nil
}

func (r *MilitaryRepository) licence(ctx context.Context, sql string, args ...any) (*application.DefenceLicence, error) {
	l, err := scanLicence(r.q.QueryRow(ctx, sql, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrDefenceLicenceNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a defence licence: %w", err)
	}
	return l, nil
}

// CompanyLicence reads a company's latest licence or application.
func (r *MilitaryRepository) CompanyLicence(ctx context.Context, companyID string, lock bool) (*application.DefenceLicence, error) {
	if !validUUID(companyID) {
		return nil, application.ErrDefenceLicenceNotFound
	}
	sql := `SELECT ` + licenceColumns + ` FROM defence_licences l WHERE l.company_id = $1::uuid
	         ORDER BY l.applied_at DESC, l.no DESC LIMIT 1`
	if lock {
		sql += ` FOR UPDATE`
	}
	return r.licence(ctx, sql, companyID)
}

// LicenceByNo reads a licence by its public number.
func (r *MilitaryRepository) LicenceByNo(ctx context.Context, no int64, lock bool) (*application.DefenceLicence, error) {
	sql := `SELECT ` + licenceColumns + ` FROM defence_licences l WHERE l.no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	return r.licence(ctx, sql, no)
}

// CreateLicence records a licence or an application.
func (r *MilitaryRepository) CreateLicence(ctx context.Context, l application.DefenceLicence) (application.DefenceLicence, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO defence_licences (id, company_id, kind, basis, status, applied_by, applied_at, decided_by,
		        decided_office, decided_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8::uuid, $9, $10, $11)
		 RETURNING no`,
		l.ID, l.CompanyID, l.Kind, l.Basis, l.Status, nullableUUID(l.AppliedBy), l.AppliedAt.UTC(),
		nullableUUID(l.DecidedBy), nullableText(l.DecidedOffice), utcOrNil(l.DecidedAt), l.UpdatedAt.UTC()).Scan(&l.No)
	switch {
	case violates(err, sqlstateUniqueViolation, defenceLicencesOpenKey):
		return l, application.ErrDefenceLicenceOpen
	case err != nil:
		return l, fmt.Errorf("postgres: recording a defence licence: %w", err)
	}
	return l, nil
}

// SaveLicence writes a licence's status, decision and revocation.
func (r *MilitaryRepository) SaveLicence(ctx context.Context, l application.DefenceLicence) error {
	_, err := r.q.Exec(ctx,
		`UPDATE defence_licences SET status = $2, decided_by = $3::uuid, decided_office = $4, decided_at = $5,
		        revoked_by = $6::uuid, revoked_office = $7, revoked_at = $8, effective_at = $9, updated_at = $10
		  WHERE id = $1::uuid`,
		l.ID, l.Status, nullableUUID(l.DecidedBy), nullableText(l.DecidedOffice), utcOrNil(l.DecidedAt),
		nullableUUID(l.RevokedBy), nullableText(l.RevokedOffice), utcOrNil(l.RevokedAt), utcOrNil(l.EffectiveAt),
		l.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: saving a defence licence: %w", err)
	}
	return nil
}

// Licences lists the licences of these cities' companies: every open one,
// and the latest ended ones.
func (r *MilitaryRepository) Licences(ctx context.Context, cityIDs []string, ended int) ([]application.LicenceLine, error) {
	if len(cityIDs) == 0 {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `WITH mine AS (
		    SELECT l.id, l.status, l.updated_at, l.no FROM defence_licences l JOIN companies c ON c.id = l.company_id
		     WHERE c.city_id = ANY($1::uuid[])),
		  shown AS (
		    SELECT id FROM mine WHERE status IN ('pending', 'active', 'revoking')
		    UNION ALL
		    (SELECT id FROM mine WHERE status NOT IN ('pending', 'active', 'revoking')
		      ORDER BY updated_at DESC, no DESC LIMIT $2))
		SELECT `+licenceColumns+`, `+companyColumnsC+`
		  FROM defence_licences l JOIN companies c ON c.id = l.company_id
		 WHERE l.id IN (SELECT id FROM shown)
		 ORDER BY (l.status = 'pending') DESC, l.updated_at DESC, l.no DESC`, cityIDs, max(ended, 0))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing defence licences: %w", err)
	}
	defer rows.Close()
	var out []application.LicenceLine
	for rows.Next() {
		var c application.Company
		l, err := scanLicence(rows, &c.ID, &c.Code, &c.Name, &c.NameKey, &c.TypeCode, &c.CityID, &c.OwnerID,
			&c.ManagerID, &c.Status, &c.PriceBPS, &c.AutoAccept, &c.TotalShares, &c.Debt, &c.Arrears,
			&c.RatingBPS, &c.RegistrationFee, &c.RegistrationTransactionID, &c.ContentVersion,
			&c.FoundedAt, &c.UpdatedAt, &c.ClosedAt, &c.CloseReason)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a defence licence: %w", err)
		}
		c.FoundedAt, c.UpdatedAt = c.FoundedAt.UTC(), c.UpdatedAt.UTC()
		out = append(out, application.LicenceLine{Licence: *l, Company: c})
	}
	return out, rows.Err()
}

// utcOrNil is an optional instant for a nullable column.
func utcOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
