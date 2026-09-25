package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists health and hospitals (migrations/0023): when a
// player's health last recovered at rest, their stays, the treatments that
// shortened them, and the clinics' prices.

// Index and constraint names from migrations/0023 this file maps to
// sentinels.
const (
	hospitalStaysOneAdmittedIdx = "hospital_stays_one_admitted_idx"
	hospitalTreatmentsStayKey   = "hospital_treatments_stay_key"
)

const stayColumns = `id::text, player_id::text, city_id::text, cause, COALESCE(cause_ref::text, ''), status,
	health_in, health_out, admitted_at, ends_at, game_action_id::text, discharged_at`

// HealthRepository implements application.HealthRepository.
type HealthRepository struct {
	q querier
}

var _ application.HealthRepository = (*HealthRepository)(nil)

// NewHealthRepository returns the repository over the pool, for reads
// outside a unit of work.
func NewHealthRepository(p *Pool) *HealthRepository { return &HealthRepository{q: p.Raw()} }

func scanStay(row pgx.Row) (*application.HospitalStay, error) {
	var s application.HospitalStay
	if err := row.Scan(&s.ID, &s.PlayerID, &s.CityID, &s.Cause, &s.CauseRef, &s.Status, &s.HealthIn, &s.HealthOut,
		&s.AdmittedAt, &s.EndsAt, &s.GameActionID, &s.DischargedAt); err != nil {
		return nil, err
	}
	s.AdmittedAt, s.EndsAt, s.DischargedAt = s.AdmittedAt.UTC(), s.EndsAt.UTC(), utcPtr(s.DischargedAt)
	return &s, nil
}

// RestSince reads player_health.
func (r *HealthRepository) RestSince(ctx context.Context, playerID string) (time.Time, error) {
	var at time.Time
	err := r.q.QueryRow(ctx, `SELECT rest_since FROM player_health WHERE player_id = $1::uuid`, playerID).Scan(&at)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("postgres: reading rest: %w", err)
	}
	return at.UTC(), nil
}

// SetRestSince upserts player_health.
func (r *HealthRepository) SetRestSince(ctx context.Context, playerID string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO player_health (player_id, rest_since, updated_at) VALUES ($1::uuid, $2, $3)
		 ON CONFLICT (player_id) DO UPDATE SET rest_since = EXCLUDED.rest_since, updated_at = EXCLUDED.updated_at`,
		playerID, at.UTC(), time.Now().UTC()); err != nil {
		return fmt.Errorf("postgres: recording rest: %w", err)
	}
	return nil
}

// ActiveStay reads the player's admitted stay without a lock.
func (r *HealthRepository) ActiveStay(ctx context.Context, playerID string) (*application.HospitalStay, error) {
	s, err := scanStay(r.q.QueryRow(ctx,
		`SELECT `+stayColumns+` FROM hospital_stays WHERE player_id = $1::uuid AND status = $2`,
		playerID, application.StayAdmitted))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNotHospitalised
	case err != nil:
		return nil, fmt.Errorf("postgres: active stay: %w", err)
	}
	return s, nil
}

// Stay reads one stay, locked.
func (r *HealthRepository) Stay(ctx context.Context, id string) (*application.HospitalStay, error) {
	s, err := scanStay(r.q.QueryRow(ctx, `SELECT `+stayColumns+` FROM hospital_stays WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrStayNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading stay: %w", err)
	}
	return s, nil
}

// Admit inserts an admitted stay; the partial unique index refuses a second.
func (r *HealthRepository) Admit(ctx context.Context, s application.HospitalStay) error {
	id, err := ensureID(s.ID)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO hospital_stays (id, player_id, city_id, cause, cause_ref, status, health_in, health_out,
		                             admitted_at, ends_at, game_action_id)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, $10, $11::uuid)`,
		id, s.PlayerID, s.CityID, s.Cause, nullableUUID(s.CauseRef), application.StayAdmitted, s.HealthIn, s.HealthOut,
		s.AdmittedAt.UTC(), s.EndsAt.UTC(), s.GameActionID)
	if violates(err, sqlstateUniqueViolation, hospitalStaysOneAdmittedIdx) {
		return application.ErrAlreadyHospitalised
	}
	if err != nil {
		return fmt.Errorf("postgres: admitting: %w", err)
	}
	return nil
}

// Reschedule moves an admitted stay's end.
func (r *HealthRepository) Reschedule(ctx context.Context, id string, endsAt time.Time, gameActionID string) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE hospital_stays SET ends_at = GREATEST($2, admitted_at), game_action_id = $3::uuid
		  WHERE id = $1::uuid AND status = $4`,
		id, endsAt.UTC(), gameActionID, application.StayAdmitted)
	if err != nil {
		return fmt.Errorf("postgres: rescheduling a stay: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotHospitalised
	}
	return nil
}

// Discharge ends an admitted stay.
func (r *HealthRepository) Discharge(ctx context.Context, id string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE hospital_stays SET status = $2, discharged_at = $3 WHERE id = $1::uuid AND status = $4`,
		id, application.StayDischarged, at.UTC(), application.StayAdmitted)
	if err != nil {
		if isInvalidUUIDText(err) {
			return application.ErrNotHospitalised
		}
		return fmt.Errorf("postgres: discharging: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotHospitalised
	}
	return nil
}

// RecentStays lists a player's stays, most recent first.
func (r *HealthRepository) RecentStays(ctx context.Context, playerID string, limit int) ([]application.HospitalStay, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+stayColumns+` FROM hospital_stays WHERE player_id = $1::uuid ORDER BY admitted_at DESC LIMIT $2`,
		playerID, limit)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing stays: %w", err)
	}
	defer rows.Close()
	var out []application.HospitalStay
	for rows.Next() {
		s, err := scanStay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// RecordTreatment appends a treatment; one per stay.
func (r *HealthRepository) RecordTreatment(ctx context.Context, t application.Treatment) error {
	id, err := ensureID(t.ID)
	if err != nil {
		return err
	}
	var item *string
	if t.MedicineUnits > 0 {
		item = &t.MedicineItem
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO hospital_treatments (id, stay_id, player_id, city_id, provider, company_id, price, method,
		                                  ledger_transaction_id, medicine_item, medicine_units, doctor_level,
		                                  reduction_bps, saved_seconds, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9::uuid, $10, $11, $12, $13, $14, $15)`,
		id, t.StayID, t.PlayerID, t.CityID, t.Provider, nullableUUID(t.CompanyID), t.Price, t.Method,
		nullableUUID(t.LedgerTxID), item, t.MedicineUnits, t.DoctorLevel, t.ReductionBPS,
		int64(t.Saved/time.Second), t.CreatedAt.UTC())
	if violates(err, sqlstateUniqueViolation, hospitalTreatmentsStayKey) {
		return application.ErrAlreadyTreated
	}
	if err != nil {
		return fmt.Errorf("postgres: recording a treatment: %w", err)
	}
	return nil
}

// TreatmentOf reads a stay's treatment.
func (r *HealthRepository) TreatmentOf(ctx context.Context, stayID string) (*application.Treatment, error) {
	var (
		t     application.Treatment
		saved int64
	)
	err := r.q.QueryRow(ctx,
		`SELECT id::text, stay_id::text, player_id::text, city_id::text, provider, COALESCE(company_id::text, ''),
		        price, method, COALESCE(ledger_transaction_id::text, ''), COALESCE(medicine_item, ''), medicine_units,
		        doctor_level, reduction_bps, saved_seconds, created_at
		   FROM hospital_treatments WHERE stay_id = $1::uuid`, stayID).Scan(
		&t.ID, &t.StayID, &t.PlayerID, &t.CityID, &t.Provider, &t.CompanyID, &t.Price, &t.Method, &t.LedgerTxID,
		&t.MedicineItem, &t.MedicineUnits, &t.DoctorLevel, &t.ReductionBPS, &saved, &t.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrTreatmentNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a treatment: %w", err)
	}
	t.Saved, t.CreatedAt = time.Duration(saved)*time.Second, t.CreatedAt.UTC()
	return &t, nil
}

// Clinic reads a clinic's service; none reads closed.
func (r *HealthRepository) Clinic(ctx context.Context, companyID string) (application.ClinicService, error) {
	c := application.ClinicService{CompanyID: companyID}
	err := r.q.QueryRow(ctx, `SELECT price, open, updated_at FROM clinic_services WHERE company_id = $1::uuid`,
		companyID).Scan(&c.Price, &c.Open, &c.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return c, nil
	case err != nil:
		return c, fmt.Errorf("postgres: reading a clinic: %w", err)
	}
	c.UpdatedAt = c.UpdatedAt.UTC()
	return c, nil
}

// SaveClinic upserts a clinic's service.
func (r *HealthRepository) SaveClinic(ctx context.Context, c application.ClinicService) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO clinic_services (company_id, price, open, updated_at) VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (company_id) DO UPDATE SET price = EXCLUDED.price, open = EXCLUDED.open, updated_at = EXCLUDED.updated_at`,
		c.CompanyID, c.Price, c.Open, c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a clinic: %w", err)
	}
	return nil
}

// Clinics lists a city's active companies of the kinds, with their service.
func (r *HealthRepository) Clinics(ctx context.Context, cityID string, kinds []string) ([]application.ClinicListing, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT `+companyColumnsC+`, COALESCE(s.price, 0), COALESCE(s.open, false), COALESCE(s.updated_at, c.founded_at),
		        (SELECT count(*) FROM hospital_treatments t WHERE t.company_id = c.id)
		   FROM companies c LEFT JOIN clinic_services s ON s.company_id = c.id
		  WHERE c.city_id = $1::uuid AND c.status = 'active' AND c.type_code = ANY($2)
		  ORDER BY c.name, c.code`, cityID, kinds)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing clinics: %w", err)
	}
	defer rows.Close()
	var out []application.ClinicListing
	for rows.Next() {
		var (
			c application.Company
			l application.ClinicListing
		)
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.NameKey, &c.TypeCode, &c.CityID, &c.OwnerID,
			&c.ManagerID, &c.Status, &c.PriceBPS, &c.AutoAccept, &c.TotalShares, &c.Debt, &c.Arrears,
			&c.RatingBPS, &c.RegistrationFee, &c.RegistrationTransactionID, &c.ContentVersion,
			&c.FoundedAt, &c.UpdatedAt, &c.ClosedAt, &c.CloseReason,
			&l.Service.Price, &l.Service.Open, &l.Service.UpdatedAt, &l.Treated); err != nil {
			return nil, fmt.Errorf("postgres: scanning a clinic: %w", err)
		}
		l.Company, l.Service.CompanyID = c, c.ID
		out = append(out, l)
	}
	return out, rows.Err()
}

// ClinicEarnings sums a clinic's treatments.
func (r *HealthRepository) ClinicEarnings(ctx context.Context, companyID string) (int64, int, error) {
	var (
		paid    int64
		treated int
	)
	if err := r.q.QueryRow(ctx,
		`SELECT COALESCE(SUM(price), 0)::bigint, count(*) FROM hospital_treatments WHERE company_id = $1::uuid`,
		companyID).Scan(&paid, &treated); err != nil {
		if isInvalidUUIDText(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("postgres: summing a clinic's treatments: %w", err)
	}
	return paid, treated, nil
}
