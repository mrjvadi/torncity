package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
)

// CharterRepository is a settlement's offices, seats and audit (migration 0121).
type CharterRepository struct{ q querier }

// Lock takes a transaction-scoped advisory lock keyed by the settlement.
func (r *CharterRepository) Lock(ctx context.Context, settlementID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('charter:' || $1, 0))`, settlementID); err != nil {
		return fmt.Errorf("postgres: locking a charter: %w", err)
	}
	return nil
}

type grantJSON struct {
	P string `json:"p"`
	L int64  `json:"l,omitempty"`
}

func grantsToJSON(gs []charter.Grant) ([]byte, error) {
	out := make([]grantJSON, 0, len(gs))
	for _, g := range gs {
		out = append(out, grantJSON{P: string(g.Permission), L: g.Limit})
	}
	return json.Marshal(out)
}

func grantsFromJSON(raw []byte) ([]charter.Grant, error) {
	var in []grantJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("postgres: reading charter grants: %w", err)
	}
	out := make([]charter.Grant, 0, len(in))
	for _, g := range in {
		out = append(out, charter.Grant{Permission: charter.Permission(g.P), Limit: g.L})
	}
	return out, nil
}

const charterOfficeColumns = `o.id::text, o.title, o.seats, o.grants, o.acquisition, COALESCE(o.appointer_office_id::text, ''), o.term_days`

func scanOffices(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]charter.Office, error) {
	defer rows.Close()
	var out []charter.Office
	for rows.Next() {
		var o charter.Office
		var raw []byte
		var acq string
		if err := rows.Scan(&o.ID, &o.Title, &o.Seats, &raw, &acq, &o.AppointerID, &o.TermDays); err != nil {
			return nil, fmt.Errorf("postgres: reading a charter office: %w", err)
		}
		o.Acquisition = charter.Acquisition(acq)
		var err error
		if o.Grants, err = grantsFromJSON(raw); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Offices lists the open offices, oldest first.
func (r *CharterRepository) Offices(ctx context.Context, settlementID string) ([]charter.Office, error) {
	rows, err := r.q.Query(ctx, `SELECT `+charterOfficeColumns+` FROM charter_offices o
		WHERE o.settlement_id = $1::uuid AND o.closed_at IS NULL ORDER BY o.created_at, o.id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing charter offices: %w", err)
	}
	return scanOffices(rows)
}

// SaveOffice inserts or updates an office.
func (r *CharterRepository) SaveOffice(ctx context.Context, settlementID string, o charter.Office, createdBy string, at time.Time) error {
	raw, err := grantsToJSON(o.Grants)
	if err != nil {
		return err
	}
	var by any
	if createdBy != "" {
		by = createdBy
	}
	var appointer any
	if o.AppointerID != "" {
		appointer = o.AppointerID
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO charter_offices (id, settlement_id, title, seats, grants, acquisition, appointer_office_id, term_days, created_by, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7::uuid, $8, $9::uuid, $10)
		ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, seats = EXCLUDED.seats, grants = EXCLUDED.grants,
		    appointer_office_id = EXCLUDED.appointer_office_id, term_days = EXCLUDED.term_days`,
		o.ID, settlementID, o.Title, o.Seats, raw, string(o.Acquisition), appointer, o.TermDays, by, at); err != nil {
		return fmt.Errorf("postgres: saving a charter office: %w", err)
	}
	return nil
}

// CloseOffice closes an office.
func (r *CharterRepository) CloseOffice(ctx context.Context, officeID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE charter_offices SET closed_at = $2 WHERE id = $1::uuid AND closed_at IS NULL`, officeID, at); err != nil {
		return fmt.Errorf("postgres: closing a charter office: %w", err)
	}
	return nil
}

// Seats lists the active seats of the settlement's open offices.
func (r *CharterRepository) Seats(ctx context.Context, settlementID string) ([]application.CharterSeat, error) {
	rows, err := r.q.Query(ctx, `SELECT s.id::text, s.office_id::text, s.holder_id::text, COALESCE(s.appointed_by::text, ''), s.since
		FROM charter_seats s JOIN charter_offices o ON o.id = s.office_id
		WHERE o.settlement_id = $1::uuid AND o.closed_at IS NULL AND s.until IS NULL ORDER BY s.since, s.id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing charter seats: %w", err)
	}
	defer rows.Close()
	var out []application.CharterSeat
	for rows.Next() {
		var s application.CharterSeat
		if err := rows.Scan(&s.ID, &s.OfficeID, &s.HolderID, &s.AppointedBy, &s.Since); err != nil {
			return nil, fmt.Errorf("postgres: reading a charter seat: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OfficesOf lists the open offices the player sits in.
func (r *CharterRepository) OfficesOf(ctx context.Context, settlementID, playerID string) ([]charter.Office, error) {
	rows, err := r.q.Query(ctx, `SELECT `+charterOfficeColumns+` FROM charter_offices o
		JOIN charter_seats s ON s.office_id = o.id AND s.until IS NULL AND s.holder_id = $2::uuid
		WHERE o.settlement_id = $1::uuid AND o.closed_at IS NULL ORDER BY o.created_at, o.id`, settlementID, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a player's charter offices: %w", err)
	}
	return scanOffices(rows)
}

// Seat puts a player in an office.
func (r *CharterRepository) Seat(ctx context.Context, s application.CharterSeat) (bool, error) {
	var by any
	if s.AppointedBy != "" {
		by = s.AppointedBy
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO charter_seats (id, office_id, holder_id, appointed_by, since)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5) ON CONFLICT DO NOTHING`, s.ID, s.OfficeID, s.HolderID, by, s.Since)
	if err != nil {
		return false, fmt.Errorf("postgres: seating a player: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndSeat takes a player out of an office.
func (r *CharterRepository) EndSeat(ctx context.Context, officeID, holderID, reason string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE charter_seats SET until = $4, end_reason = $3
		WHERE office_id = $1::uuid AND holder_id = $2::uuid AND until IS NULL`, officeID, holderID, reason, at)
	if err != nil {
		return false, fmt.Errorf("postgres: ending a seat: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndSeatsOf ends every seat of an office.
func (r *CharterRepository) EndSeatsOf(ctx context.Context, officeID, reason string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE charter_seats SET until = $3, end_reason = $2 WHERE office_id = $1::uuid AND until IS NULL`,
		officeID, reason, at); err != nil {
		return fmt.Errorf("postgres: ending the seats of an office: %w", err)
	}
	return nil
}

// Audit appends one line to the charter log.
func (r *CharterRepository) Audit(ctx context.Context, a application.CharterAuditRow) error {
	detail, err := json.Marshal(a.Detail)
	if err != nil {
		return err
	}
	var actor, office any
	if a.ActorID != "" {
		actor = a.ActorID
	}
	if a.OfficeID != "" {
		office = a.OfficeID
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO charter_audit (id, settlement_id, actor_id, action, office_id, detail, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::jsonb, $7)`, a.ID, a.SettlementID, actor, a.Action, office, detail, a.At); err != nil {
		return fmt.Errorf("postgres: writing the charter log: %w", err)
	}
	return nil
}

// AuditList reads the newest lines of the charter log.
func (r *CharterRepository) AuditList(ctx context.Context, settlementID string, limit int) ([]application.CharterAuditRow, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, settlement_id::text, COALESCE(actor_id::text, ''), action, COALESCE(office_id::text, ''), detail, at
		FROM charter_audit WHERE settlement_id = $1::uuid ORDER BY at DESC, id DESC LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the charter log: %w", err)
	}
	defer rows.Close()
	var out []application.CharterAuditRow
	for rows.Next() {
		var a application.CharterAuditRow
		var raw []byte
		if err := rows.Scan(&a.ID, &a.SettlementID, &a.ActorID, &a.Action, &a.OfficeID, &raw, &a.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a charter log line: %w", err)
		}
		_ = json.Unmarshal(raw, &a.Detail)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResidentByCode finds an active resident of the settlement by public code.
func (r *CharterRepository) ResidentByCode(ctx context.Context, settlementID, code string) (*application.CharterPerson, error) {
	var p application.CharterPerson
	err := r.q.QueryRow(ctx, `SELECT id::text, display_name, public_code FROM players
		WHERE residence_city_id = $1::uuid AND upper(public_code) = upper($2) AND status = 'active'`, settlementID, code).Scan(&p.ID, &p.Name, &p.Code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: finding a resident by code: %w", err)
	}
	return &p, nil
}
