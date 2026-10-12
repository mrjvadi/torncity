package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// LawRepository is the law of the settlements (migration 0157; docs/adr/0078), bound to one transaction.
type LawRepository struct{ q querier }

var _ application.LawRepository = (*LawRepository)(nil)

// one runs a statement that must change exactly one row; false when it changed none (the conditional update that fences a step).
func (r *LawRepository) one(ctx context.Context, what, sql string, args ...any) (bool, error) {
	tag, err := r.q.Exec(ctx, sql, args...)
	if err != nil {
		return false, fmt.Errorf("postgres: %s: %w", what, err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *LawRepository) exec(ctx context.Context, what, sql string, args ...any) error {
	if _, err := r.q.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("postgres: %s: %w", what, err)
	}
	return nil
}

func utcT(t time.Time) time.Time { return t.UTC() }

func tp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

// ---------------------------------------------------------------------------
// the legal model and the offences
// ---------------------------------------------------------------------------

// Model is the settlement's legal model, adversarial until its charter says otherwise.
func (r *LawRepository) Model(ctx context.Context, settlementID string) (string, error) {
	var m string
	err := r.q.QueryRow(ctx, `SELECT model FROM law_settings WHERE settlement_id = $1::uuid`, settlementID).Scan(&m)
	if errors.Is(err, pgx.ErrNoRows) {
		return "adversarial", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading the legal model: %w", err)
	}
	return m, nil
}

// SetModel writes the settlement's legal model.
func (r *LawRepository) SetModel(ctx context.Context, settlementID, model, by string, at time.Time) error {
	return r.exec(ctx, "setting the legal model", `INSERT INTO law_settings (settlement_id, model, updated_by, updated_at) VALUES ($1::uuid, $2, $3::uuid, $4)
		ON CONFLICT (settlement_id) DO UPDATE SET model = EXCLUDED.model, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at`,
		settlementID, model, by, utcT(at))
}

const lawOffenceColumns = `settlement_id::text, offence, listed_by::text, max_fine, max_jail_hours, max_ban_days, listed_at, effective_at, withdrawn_at`

func scanLawOffence(row pgx.Row) (*application.LawOffence, error) {
	var o application.LawOffence
	if err := row.Scan(&o.SettlementID, &o.Offence, &o.ListedBy, &o.MaxFine, &o.MaxJailHours, &o.MaxBanDays, &o.ListedAt, &o.EffectiveAt, &o.WithdrawnAt); err != nil {
		return nil, err
	}
	o.ListedAt, o.EffectiveAt, o.WithdrawnAt = o.ListedAt.UTC(), o.EffectiveAt.UTC(), utcPtr(o.WithdrawnAt)
	return &o, nil
}

// Offences lists the offences a settlement has listed and not withdrawn.
func (r *LawRepository) Offences(ctx context.Context, settlementID string) ([]application.LawOffence, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawOffenceColumns+` FROM law_offences WHERE settlement_id = $1::uuid AND withdrawn_at IS NULL ORDER BY offence`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing offences: %w", err)
	}
	defer rows.Close()
	var out []application.LawOffence
	for rows.Next() {
		o, err := scanLawOffence(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an offence: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// OffenceIn is the offence if the settlement lists it, it is not withdrawn and its notice period is over.
func (r *LawRepository) OffenceIn(ctx context.Context, settlementID, code string, now time.Time) (*application.LawOffence, error) {
	o, err := scanLawOffence(r.q.QueryRow(ctx, `SELECT `+lawOffenceColumns+` FROM law_offences WHERE settlement_id = $1::uuid AND offence = $2
		AND withdrawn_at IS NULL AND effective_at <= $3`, settlementID, code, utcT(now)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an offence: %w", err)
	}
	return o, nil
}

// UpsertOffence lists an offence, or re-lists a withdrawn one with new bounds.
func (r *LawRepository) UpsertOffence(ctx context.Context, o application.LawOffence) error {
	return r.exec(ctx, "listing an offence", `INSERT INTO law_offences (settlement_id, offence, max_fine, max_jail_hours, max_ban_days, listed_by, listed_at, effective_at, withdrawn_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $8, NULL)
		ON CONFLICT (settlement_id, offence) DO UPDATE SET max_fine = EXCLUDED.max_fine, max_jail_hours = EXCLUDED.max_jail_hours,
			max_ban_days = EXCLUDED.max_ban_days, listed_by = EXCLUDED.listed_by, withdrawn_at = NULL,
			listed_at = CASE WHEN law_offences.withdrawn_at IS NULL THEN law_offences.listed_at ELSE EXCLUDED.listed_at END,
			effective_at = CASE WHEN law_offences.withdrawn_at IS NULL THEN law_offences.effective_at ELSE EXCLUDED.effective_at END`,
		o.SettlementID, o.Offence, o.MaxFine, o.MaxJailHours, o.MaxBanDays, o.ListedBy, utcT(o.ListedAt), utcT(o.EffectiveAt))
}

// WithdrawOffence takes an offence off the list; false when it was not listed.
func (r *LawRepository) WithdrawOffence(ctx context.Context, settlementID, code string, at time.Time) (bool, error) {
	return r.one(ctx, "withdrawing an offence", `UPDATE law_offences SET withdrawn_at = $3 WHERE settlement_id = $1::uuid AND offence = $2 AND withdrawn_at IS NULL`, settlementID, code, utcT(at))
}

// ---------------------------------------------------------------------------
// the points of an open post
// ---------------------------------------------------------------------------

// GrantPoints writes the day's points once: the first replica to write the row wins, the others find it.
func (r *LawRepository) GrantPoints(ctx context.Context, settlementID string, day, points int64) (bool, error) {
	return r.one(ctx, "granting the day's points", `INSERT INTO law_capacity (settlement_id, day, points) VALUES ($1::uuid, $2, $3) ON CONFLICT DO NOTHING`, settlementID, day, points)
}

// Points reads the day's points.
func (r *LawRepository) Points(ctx context.Context, settlementID string, day int64) (int64, int64, int64, bool, error) {
	var p, s, pt int64
	err := r.q.QueryRow(ctx, `SELECT points, spent, patrol FROM law_capacity WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day).Scan(&p, &s, &pt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("postgres: reading the day's points: %w", err)
	}
	return p, s, pt, true, nil
}

// Spend takes cost points of the day when that many are left.
func (r *LawRepository) Spend(ctx context.Context, settlementID string, day, cost int64, patrol bool) (bool, error) {
	n := int64(0)
	if patrol {
		n = 1
	}
	return r.one(ctx, "spending points", `UPDATE law_capacity SET spent = spent + $3, patrol = patrol + $4 WHERE settlement_id = $1::uuid AND day = $2 AND spent + $3 <= points`,
		settlementID, day, cost, n)
}

// ---------------------------------------------------------------------------
// cases
// ---------------------------------------------------------------------------

const lawCaseColumns = `id::text, no, settlement_id::text, offence, accused_id::text, COALESCE(opened_by::text, ''), source, state, model,
	COALESCE(custody_building::text, ''), custody_until, COALESCE(charged_by::text, ''), charged_at, hearing_at, COALESCE(defence_id::text, ''), defence_npc,
	complaint_fee, created_at, updated_at, decided_at`

func scanLawCase(row pgx.Row) (*application.LawCase, error) {
	var c application.LawCase
	if err := row.Scan(&c.ID, &c.No, &c.SettlementID, &c.Offence, &c.AccusedID, &c.OpenedBy, &c.Source, &c.State, &c.Model, &c.CustodyBuilding, &c.CustodyUntil,
		&c.ChargedBy, &c.ChargedAt, &c.HearingAt, &c.DefenceID, &c.DefenceNPC, &c.ComplaintFee, &c.CreatedAt, &c.UpdatedAt, &c.DecidedAt); err != nil {
		return nil, err
	}
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	c.CustodyUntil, c.ChargedAt, c.HearingAt, c.DecidedAt = utcPtr(c.CustodyUntil), utcPtr(c.ChargedAt), utcPtr(c.HearingAt), utcPtr(c.DecidedAt)
	return &c, nil
}

func (r *LawRepository) cases(ctx context.Context, sql string, args ...any) ([]application.LawCase, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing cases: %w", err)
	}
	defer rows.Close()
	var out []application.LawCase
	for rows.Next() {
		c, err := scanLawCase(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a case: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// InsertCase opens a case and writes its first stage.
func (r *LawRepository) InsertCase(ctx context.Context, c application.LawCase) error {
	if err := r.exec(ctx, "opening a case", `INSERT INTO law_cases (id, settlement_id, offence, accused_id, opened_by, source, state, model, complaint_fee, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6, 'open', $7, $8, $9, $9)`,
		c.ID, c.SettlementID, c.Offence, c.AccusedID, nullIfEmpty(c.OpenedBy), c.Source, c.Model, c.ComplaintFee, utcT(c.CreatedAt)); err != nil {
		return err
	}
	_, err := r.Stage(ctx, c.ID, "opened", c.OpenedBy, c.CreatedAt)
	return err
}

// CaseByID reads a case, nil for none.
func (r *LawRepository) CaseByID(ctx context.Context, id string, lock bool) (*application.LawCase, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawCaseColumns + ` FROM law_cases WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := scanLawCase(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a case: %w", err)
	}
	return c, nil
}

// CasesOf lists the settlement's cases, newest first.
func (r *LawRepository) CasesOf(ctx context.Context, settlementID string, limit int) ([]application.LawCase, error) {
	return r.cases(ctx, `SELECT `+lawCaseColumns+` FROM law_cases WHERE settlement_id = $1::uuid ORDER BY created_at DESC, id LIMIT $2`, settlementID, limit)
}

// CasesAgainst lists the cases against a person, newest first.
func (r *LawRepository) CasesAgainst(ctx context.Context, accusedID string, limit int) ([]application.LawCase, error) {
	return r.cases(ctx, `SELECT `+lawCaseColumns+` FROM law_cases WHERE accused_id = $1::uuid ORDER BY created_at DESC, id LIMIT $2`, accusedID, limit)
}

// OpenCaseFor is the case still running against a person for an offence in a settlement, nil for none.
func (r *LawRepository) OpenCaseFor(ctx context.Context, settlementID, accusedID, offence string) (*application.LawCase, error) {
	c, err := scanLawCase(r.q.QueryRow(ctx, `SELECT `+lawCaseColumns+` FROM law_cases WHERE settlement_id = $1::uuid AND accused_id = $2::uuid AND offence = $3
		AND state IN ('open', 'charged', 'hearing') ORDER BY created_at DESC LIMIT 1`, settlementID, accusedID, offence))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a case: %w", err)
	}
	return c, nil
}

// Stage writes the row of a stage. False when the case has passed it already: the primary key is the fence replicas race on.
func (r *LawRepository) Stage(ctx context.Context, caseID, stage, by string, at time.Time) (bool, error) {
	return r.one(ctx, "recording a stage", `INSERT INTO law_case_stages (case_id, stage, by_id, at) VALUES ($1::uuid, $2, $3::uuid, $4) ON CONFLICT DO NOTHING`,
		caseID, stage, nullIfEmpty(by), utcT(at))
}

// HasStage says whether the case has passed a stage.
func (r *LawRepository) HasStage(ctx context.Context, caseID, stage string) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM law_case_stages WHERE case_id = $1::uuid AND stage = $2)`, caseID, stage).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: reading a stage: %w", err)
	}
	return ok, nil
}

// Move changes a case's state when it is one of from.
func (r *LawRepository) Move(ctx context.Context, caseID string, from []string, to string, at time.Time) (bool, error) {
	return r.one(ctx, "moving a case", `UPDATE law_cases SET state = $3, updated_at = $4 WHERE id = $1::uuid AND state = ANY($2::text[])`, caseID, from, to, utcT(at))
}

// Arrest holds the accused in a building until an instant.
func (r *LawRepository) Arrest(ctx context.Context, caseID, building string, until, at time.Time) (bool, error) {
	return r.one(ctx, "arresting", `UPDATE law_cases SET custody_building = $2::uuid, custody_until = $3, updated_at = $4 WHERE id = $1::uuid
		AND state IN ('open', 'charged') AND custody_until IS NULL`, caseID, building, utcT(until), utcT(at))
}

// ReleaseCustody ends a custody.
func (r *LawRepository) ReleaseCustody(ctx context.Context, caseID string, at time.Time) (bool, error) {
	return r.one(ctx, "releasing from custody", `UPDATE law_cases SET custody_building = NULL, custody_until = NULL, updated_at = $2 WHERE id = $1::uuid AND custody_until IS NOT NULL`, caseID, utcT(at))
}

// Charge moves an open case to charged, once.
func (r *LawRepository) Charge(ctx context.Context, caseID, by string, hearingAt, at time.Time) (bool, error) {
	return r.one(ctx, "charging", `UPDATE law_cases SET state = 'charged', charged_by = $2::uuid, charged_at = $4, hearing_at = $3, updated_at = $4 WHERE id = $1::uuid AND state = 'open'`,
		caseID, by, utcT(hearingAt), utcT(at))
}

// SetDefence sets the accused's defence before the hearing, once.
func (r *LawRepository) SetDefence(ctx context.Context, caseID, defenceID string, npc bool, at time.Time) (bool, error) {
	return r.one(ctx, "setting the defence", `UPDATE law_cases SET defence_id = $2::uuid, defence_npc = $3, updated_at = $4 WHERE id = $1::uuid
		AND state IN ('open', 'charged') AND defence_id IS NULL AND NOT defence_npc`, caseID, nullIfEmpty(defenceID), npc, utcT(at))
}

// Decided marks the instant a case was decided.
func (r *LawRepository) Decided(ctx context.Context, caseID string, at time.Time) error {
	return r.exec(ctx, "marking a case decided", `UPDATE law_cases SET decided_at = $2, updated_at = $2 WHERE id = $1::uuid`, caseID, utcT(at))
}

// ---------------------------------------------------------------------------
// evidence and warrants
// ---------------------------------------------------------------------------

const lawEvidenceColumns = `id::text, case_id::text, kind, strength, proper, excluded, reliability, COALESCE(informant_id::text, ''), COALESCE(obtained_by::text, ''), ref, at`

func scanLawEvidence(row pgx.Row) (*application.LawEvidence, error) {
	var e application.LawEvidence
	if err := row.Scan(&e.ID, &e.CaseID, &e.Kind, &e.Strength, &e.Proper, &e.Excluded, &e.Reliability, &e.InformantID, &e.ObtainedBy, &e.Ref, &e.At); err != nil {
		return nil, err
	}
	e.At = e.At.UTC()
	return &e, nil
}

// AddEvidence stores an item; false when the case already holds an item about the same act (ref).
func (r *LawRepository) AddEvidence(ctx context.Context, e application.LawEvidence) (bool, error) {
	return r.one(ctx, "storing evidence", `INSERT INTO law_evidence (id, case_id, kind, strength, proper, reliability, informant_id, obtained_by, ref, at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8::uuid, $9, $10) ON CONFLICT DO NOTHING`,
		e.ID, e.CaseID, e.Kind, e.Strength, e.Proper, e.Reliability, nullIfEmpty(e.InformantID), nullIfEmpty(e.ObtainedBy), e.Ref, utcT(e.At))
}

// Evidence lists a case's items, oldest first.
func (r *LawRepository) Evidence(ctx context.Context, caseID string) ([]application.LawEvidence, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawEvidenceColumns+` FROM law_evidence WHERE case_id = $1::uuid ORDER BY at, id`, caseID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing evidence: %w", err)
	}
	defer rows.Close()
	var out []application.LawEvidence
	for rows.Next() {
		e, err := scanLawEvidence(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading evidence: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// EvidenceByID reads one item, nil for none.
func (r *LawRepository) EvidenceByID(ctx context.Context, id string) (*application.LawEvidence, error) {
	if !validUUID(id) {
		return nil, nil
	}
	e, err := scanLawEvidence(r.q.QueryRow(ctx, `SELECT `+lawEvidenceColumns+` FROM law_evidence WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading evidence: %w", err)
	}
	return e, nil
}

// Exclude has an improper item excluded, once.
func (r *LawRepository) Exclude(ctx context.Context, evidenceID string) (bool, error) {
	return r.one(ctx, "excluding evidence", `UPDATE law_evidence SET excluded = true WHERE id = $1::uuid AND NOT proper AND NOT excluded`, evidenceID)
}

// InsertWarrant records a request for a warrant.
func (r *LawRepository) InsertWarrant(ctx context.Context, w application.LawWarrant) error {
	return r.exec(ctx, "asking for a warrant", `INSERT INTO law_warrants (id, case_id, asked_by, status, at) VALUES ($1::uuid, $2::uuid, $3::uuid, 'asked', $4)`,
		w.ID, w.CaseID, w.AskedBy, utcT(w.At))
}

const lawWarrantColumns = `id::text, case_id::text, asked_by::text, COALESCE(signed_by::text, ''), status, expires_at, at`

func scanLawWarrant(row pgx.Row) (*application.LawWarrant, error) {
	var w application.LawWarrant
	if err := row.Scan(&w.ID, &w.CaseID, &w.AskedBy, &w.SignedBy, &w.Status, &w.ExpiresAt, &w.At); err != nil {
		return nil, err
	}
	w.ExpiresAt, w.At = utcPtr(w.ExpiresAt), w.At.UTC()
	return &w, nil
}

// WarrantByID reads a warrant, nil for none.
func (r *LawRepository) WarrantByID(ctx context.Context, id string, lock bool) (*application.LawWarrant, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawWarrantColumns + ` FROM law_warrants WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	w, err := scanLawWarrant(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a warrant: %w", err)
	}
	return w, nil
}

// WarrantsOf lists a case's warrants.
func (r *LawRepository) WarrantsOf(ctx context.Context, caseID string) ([]application.LawWarrant, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawWarrantColumns+` FROM law_warrants WHERE case_id = $1::uuid ORDER BY at, id`, caseID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing warrants: %w", err)
	}
	defer rows.Close()
	var out []application.LawWarrant
	for rows.Next() {
		w, err := scanLawWarrant(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a warrant: %w", err)
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// Sign signs an asked warrant, once.
func (r *LawRepository) Sign(ctx context.Context, id, by string, expires, at time.Time) (bool, error) {
	return r.one(ctx, "signing a warrant", `UPDATE law_warrants SET status = 'signed', signed_by = $2::uuid, expires_at = $3 WHERE id = $1::uuid AND status = 'asked'`, id, by, utcT(expires))
}

// RefuseWarrant refuses an asked warrant, once.
func (r *LawRepository) RefuseWarrant(ctx context.Context, id string) (bool, error) {
	return r.one(ctx, "refusing a warrant", `UPDATE law_warrants SET status = 'refused' WHERE id = $1::uuid AND status = 'asked'`, id)
}

// UseWarrant marks a signed warrant used.
func (r *LawRepository) UseWarrant(ctx context.Context, id string) (bool, error) {
	return r.one(ctx, "using a warrant", `UPDATE law_warrants SET status = 'used' WHERE id = $1::uuid AND status = 'signed'`, id)
}

// LiveWarrant is the case's signed warrant that has not run out, nil for none.
func (r *LawRepository) LiveWarrant(ctx context.Context, caseID string, now time.Time) (*application.LawWarrant, error) {
	w, err := scanLawWarrant(r.q.QueryRow(ctx, `SELECT `+lawWarrantColumns+` FROM law_warrants WHERE case_id = $1::uuid AND status = 'signed' AND expires_at > $2
		ORDER BY at DESC LIMIT 1`, caseID, utcT(now)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a warrant: %w", err)
	}
	return w, nil
}
