package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
)

// This file implements the policy resolver and the governance repository of
// docs/adr/0015-player-held-offices.md.
//
// Both load the same inputs through loadPolicyInputs and hand them to
// application.ResolvePolicy. Nothing here decides which value wins: SQL
// gathers the facts, the one precedence function in the application layer
// judges them.

// policyLockClass is the first key of the two-key advisory lock that
// serialises changes of one lever in one jurisdiction. Two-key locks do not
// share a key space with the one-key locks elsewhere (contentLoadLockKey), and
// 15 reads as ADR 0015.
const policyLockClass = 15

// Constraint names raised by the policy_values trigger in migration 0008.
const (
	policyValuesWithinBounds     = "policy_values_within_bounds"
	policyValuesWithinAllocation = "policy_values_within_allocation"
	policyValuesCooldown         = "policy_values_cooldown"
	officesOneSeatPerHolder      = "offices_one_seat_per_holder_key"
)

// leverColumns is what every lever read selects, in scanLever's order.
const leverColumns = `ld.code, ld.jurisdiction_kind, ld.value_type, ld.value_kind,
       COALESCE(ld.default_value, 0), COALESCE(ld.min_value, 0), COALESCE(ld.max_value, 0),
       ld.held_by, ld.decision_rule, ld.change_cooldown_seconds, ld.notice_seconds,
       COALESCE(ld.city_default, ''), COALESCE(ld.threshold, ''), COALESCE(ld.quorum, ''),
       ld.categories, ld.default_json, COALESCE(ld.requires_confirmation_by, ''),
       COALESCE(ld.confirmation_rule, ''), COALESCE(ld.confirmation_threshold, ''),
       COALESCE(ld.confirmation_quorum, ''), ld.confirm_above`

// selectActiveLever reads one lever of the active content version.
const selectActiveLever = `
SELECT ` + leverColumns + `
  FROM lever_definitions ld
  JOIN content_versions cv ON cv.id = ld.content_version_id
 WHERE cv.status = 'active'
   AND ld.code = $1`

// scanLever reads one lever selected with leverColumns.
func scanLever(row pgx.Row) (application.LeverDefinition, error) {
	var (
		l                  application.LeverDefinition
		cooldown, noticeSe int64
		defJSON            []byte
	)
	if err := row.Scan(&l.Code, &l.Jurisdiction, &l.Type, &l.ValueKind,
		&l.Default, &l.Min, &l.Max, &l.HeldBy, &l.DecisionRule, &cooldown, &noticeSe, &l.CityDefault,
		&l.Threshold, &l.Quorum, &l.Categories, &defJSON, &l.RequiresConfirmationBy,
		&l.ConfirmationRule, &l.ConfirmationThreshold, &l.ConfirmationQuorum, &l.ConfirmAbove); err != nil {
		return l, err
	}
	l.ChangeCooldown = time.Duration(cooldown) * time.Second
	l.Notice = time.Duration(noticeSe) * time.Second
	if l.IsAllocation() {
		shares, err := decodeAllocation(defJSON)
		if err != nil {
			return l, fmt.Errorf("postgres: lever %q default: %w", l.Code, err)
		}
		l.DefaultAllocation = shares
	}
	return l, nil
}

// decodeAllocation reads an allocation document: category to bps.
func decodeAllocation(raw []byte) (map[string]int64, error) {
	out := map[string]int64{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// selectJurisdiction reads one jurisdiction and, for a city, the stored
// values a per-city default may come from.
const selectJurisdiction = `
SELECT j.id::text, j.kind, j.code, j.name, COALESCE(j.parent_id::text, ''), c.tax_rate_bps
  FROM jurisdictions j
  LEFT JOIN cities c ON c.jurisdiction_id = j.id
 WHERE j.id = $1::uuid`

// selectActiveOffices reads every office of the active content version.
const selectActiveOffices = `
SELECT od.code, od.jurisdiction_kind, od.seats, COALESCE(od.deputy, ''),
       COALESCE(od.term_seconds, 0), od.incompatible_with, od.acquired_by, COALESCE(od.appointed_by, ''),
       od.can_be_removed_by
  FROM office_definitions od
  JOIN content_versions cv ON cv.id = od.content_version_id
 WHERE cv.status = 'active'
 ORDER BY od.code`

// officeColumns is what every seat read selects, in scanOffice's order.
const officeColumns = `id::text, office_code, jurisdiction_id::text, seat, COALESCE(holder_player_id::text, ''),
       term_ends_at, COALESCE(acquired_by, ''), since`

// selectChainSeats reads the seats of a set of offices in one jurisdiction.
const selectChainSeats = `
SELECT ` + officeColumns + `
  FROM offices
 WHERE jurisdiction_id = $1::uuid
   AND office_code = ANY($2::text[])
 ORDER BY office_code, seat`

// selectPolicySettings reads the latest setting in effect at $3 and every
// setting not yet in effect. Nothing older can win, so nothing older is read.
const selectPolicySettings = `
(SELECT id::text, jurisdiction_id::text, lever_code, COALESCE(value, 0), value_json, set_by_player_id::text,
        office_id::text, set_at, effective_at
   FROM policy_values
  WHERE jurisdiction_id = $1::uuid AND lever_code = $2 AND effective_at <= $3
  ORDER BY effective_at DESC, set_at DESC, id DESC
  LIMIT 1)
UNION ALL
(SELECT id::text, jurisdiction_id::text, lever_code, COALESCE(value, 0), value_json, set_by_player_id::text,
        office_id::text, set_at, effective_at
   FROM policy_values
  WHERE jurisdiction_id = $1::uuid AND lever_code = $2 AND effective_at > $3)`

// selectLastPolicyChange is when the lever last changed here, in effect or
// not: the cooldown runs from the announcement.
const selectLastPolicyChange = `
SELECT max(set_at) FROM policy_values WHERE jurisdiction_id = $1::uuid AND lever_code = $2`

// loadPolicyInputs gathers everything application.ResolvePolicy needs.
//
// lockSeats locks the acting chain's seats FOR SHARE, so that inside a
// transaction an appointment or a vacancy cannot land between the holder
// check and the write. The reader passes false: it writes nothing.
func loadPolicyInputs(ctx context.Context, q querier, jurisdictionID, leverCode string,
	now time.Time, lockSeats bool,
) (application.PolicyInputs, error) {
	var in application.PolicyInputs

	lever, err := activeLever(ctx, q, leverCode)
	if err != nil {
		return in, err
	}
	in.Lever = lever

	j, taxRate, err := jurisdictionRow(ctx, q, jurisdictionID)
	if err != nil {
		return in, err
	}
	in.Jurisdiction = j

	// A structured lever has no integer to read; refuse it here, before the
	// scalar-only queries below, for the reader and SetPolicy alike.
	if err := application.CheckLeverSupported(lever); err != nil {
		return in, err
	}

	// The one place a per-city default source becomes a value. The set of
	// sources is closed (content.CityDefaultTaxRate); a lever naming one
	// reads it off the stored city.
	if lever.CityDefault == content.CityDefaultTaxRate && taxRate != nil {
		v := int64(*taxRate)
		in.CityDefault = &v
	}

	if in.Chain, err = actingChain(ctx, q, lever.HeldBy, jurisdictionID, lockSeats); err != nil {
		return in, err
	}

	rows, err := q.Query(ctx, selectPolicySettings, jurisdictionID, leverCode, now.UTC())
	if err != nil {
		return in, fmt.Errorf("postgres: policy settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			s   application.PolicySetting
			doc []byte
		)
		if err := rows.Scan(&s.ID, &s.JurisdictionID, &s.LeverCode, &s.Value, &doc, &s.SetByPlayerID, &s.OfficeID,
			&s.SetAt, &s.EffectiveAt); err != nil {
			return in, fmt.Errorf("postgres: scanning policy setting: %w", err)
		}
		if lever.IsAllocation() {
			if s.Allocation, err = decodeAllocation(doc); err != nil {
				return in, fmt.Errorf("postgres: policy setting %s: %w", s.ID, err)
			}
		}
		in.Settings = append(in.Settings, s)
	}
	if err := rows.Err(); err != nil {
		return in, fmt.Errorf("postgres: reading policy settings: %w", err)
	}
	rows.Close()

	if err := q.QueryRow(ctx, selectLastPolicyChange, jurisdictionID, leverCode).Scan(&in.LastChangeAt); err != nil {
		return in, fmt.Errorf("postgres: last policy change: %w", err)
	}
	return in, nil
}

// activeLever reads one lever of the active content, or ErrUnknownLever.
func activeLever(ctx context.Context, q querier, code string) (application.LeverDefinition, error) {
	l, err := scanLever(q.QueryRow(ctx, selectActiveLever, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return l, application.ErrUnknownLever.WithCause(fmt.Errorf("no lever %q in the active content", code))
	}
	if err != nil {
		return l, fmt.Errorf("postgres: reading lever %q: %w", code, err)
	}
	return l, nil
}

// jurisdictionRow reads one jurisdiction and its city's tax rate, if it is a
// city. A value that is not a uuid names nothing, and is answered as such
// before it reaches the server: inside a transaction a malformed parameter
// would abort everything after it.
func jurisdictionRow(ctx context.Context, q querier, id string) (application.Jurisdiction, *int, error) {
	var (
		j       application.Jurisdiction
		taxRate *int
	)
	if !validUUID(id) {
		return j, nil, application.ErrJurisdictionNotFound
	}
	err := q.QueryRow(ctx, selectJurisdiction, id).Scan(&j.ID, &j.Kind, &j.Code, &j.Name, &j.ParentID, &taxRate)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, nil, application.ErrJurisdictionNotFound
	}
	if err != nil {
		return j, nil, fmt.Errorf("postgres: reading jurisdiction: %w", err)
	}
	return j, taxRate, nil
}

// activeOffices reads every office of the active content.
func activeOffices(ctx context.Context, q querier) ([]application.OfficeDefinition, error) {
	rows, err := q.Query(ctx, selectActiveOffices)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading offices: %w", err)
	}
	defer rows.Close()
	var out []application.OfficeDefinition
	for rows.Next() {
		var (
			o    application.OfficeDefinition
			term int64
		)
		if err := rows.Scan(&o.Code, &o.Jurisdiction, &o.Seats, &o.Deputy, &term, &o.IncompatibleWith,
			&o.AcquiredBy, &o.AppointedBy, &o.CanBeRemovedBy); err != nil {
			return nil, fmt.Errorf("postgres: scanning office: %w", err)
		}
		o.Term = time.Duration(term) * time.Second
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading offices: %w", err)
	}
	return out, nil
}

// actingChain is the lever's office followed by its deputies, each with its
// seats in this jurisdiction.
//
// The walk stops at the first office already in the chain. The loader refuses
// a deputy cycle, so that never happens for loaded content; the guard is here
// because an unbounded loop is the one failure a reader must not have.
func actingChain(ctx context.Context, q querier, heldBy, jurisdictionID string, lock bool) ([]application.OfficeLink, error) {
	defs, err := activeOffices(ctx, q)
	if err != nil {
		return nil, err
	}
	deputy := make(map[string]string, len(defs))
	for _, d := range defs {
		deputy[d.Code] = d.Deputy
	}

	var codes []string
	seen := map[string]struct{}{}
	for code := heldBy; code != ""; code = deputy[code] {
		if _, loop := seen[code]; loop {
			break
		}
		if _, declared := deputy[code]; !declared {
			break
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return nil, nil
	}

	query := selectChainSeats
	if lock {
		query += "\n   FOR SHARE"
	}
	rows, err := q.Query(ctx, query, jurisdictionID, codes)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading office seats: %w", err)
	}
	defer rows.Close()
	seats := map[string][]application.Office{}
	for rows.Next() {
		o, err := scanOffice(rows)
		if err != nil {
			return nil, err
		}
		seats[o.OfficeCode] = append(seats[o.OfficeCode], o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading office seats: %w", err)
	}

	chain := make([]application.OfficeLink, 0, len(codes))
	for _, code := range codes {
		chain = append(chain, application.OfficeLink{OfficeCode: code, Seats: seats[code]})
	}
	return chain, nil
}

// scanOffice reads one seat selected with officeColumns.
func scanOffice(row pgx.Row) (application.Office, error) {
	var o application.Office
	if err := row.Scan(&o.ID, &o.OfficeCode, &o.JurisdictionID, &o.Seat, &o.HolderPlayerID,
		&o.TermEndsAt, &o.AcquiredBy, &o.Since); err != nil {
		return o, fmt.Errorf("postgres: scanning office seat: %w", err)
	}
	return o, nil
}

// validUUID reports whether s is canonical uuid text.
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}

// PolicyStore is the postgres application.PolicyReader.
//
// Each Get reads in one read-only REPEATABLE READ transaction, so the lever,
// the seats and the settings it judges are one consistent moment of the
// world, not four.
type PolicyStore struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

var _ application.PolicyReader = (*PolicyStore)(nil)

// NewPolicyReader returns the resolver backed by p. now is the clock "in
// effect" is judged against; nil means the system clock.
func NewPolicyReader(p *Pool, now func() time.Time) *PolicyStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PolicyStore{pool: p.Raw(), now: now}
}

// Get answers the value of a lever in a jurisdiction, now. A vacant office is
// never an error; see application.ResolvePolicy.
func (s *PolicyStore) Get(ctx context.Context, jurisdictionID, leverCode string) (application.PolicyValue, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return application.PolicyValue{}, fmt.Errorf("postgres: policy read: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	now := s.now()
	in, err := loadPolicyInputs(ctx, tx, jurisdictionID, leverCode, now, false)
	if err != nil {
		return application.PolicyValue{}, err
	}
	if err := application.CheckLeverApplies(in); err != nil {
		return application.PolicyValue{}, err
	}
	return application.ResolvePolicy(in, now), nil
}

// GovernanceRepository is application.GovernanceRepository bound to one
// transaction; reach it through Tx.Governance.
type GovernanceRepository struct {
	q querier
}

var _ application.GovernanceRepository = (*GovernanceRepository)(nil)

// LockLever takes a transaction-scoped advisory lock on one lever in one
// jurisdiction. Released by commit or rollback.
func (r *GovernanceRepository) LockLever(ctx context.Context, jurisdictionID, leverCode string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`,
		policyLockClass, jurisdictionID+"/"+leverCode); err != nil {
		return fmt.Errorf("postgres: locking lever %s: %w", leverCode, err)
	}
	return nil
}

// PolicyInputs loads the resolver's inputs with the acting chain's seats
// locked FOR SHARE.
func (r *GovernanceRepository) PolicyInputs(ctx context.Context, jurisdictionID, leverCode string,
	now time.Time,
) (application.PolicyInputs, error) {
	return loadPolicyInputs(ctx, r.q, jurisdictionID, leverCode, now, true)
}

// RecordPolicy writes the setting and its public record.
//
// The policy_values trigger re-checks the bounds, the notice and the cooldown
// against the active lever; a violation it finds means a writer raced past
// SetPolicy's checks and is mapped to the same sentinel SetPolicy would have
// returned.
func (r *GovernanceRepository) RecordPolicy(ctx context.Context, c application.PolicyChange) (application.PolicyChange, error) {
	var err error
	if c.Setting.ID, err = ensureID(c.Setting.ID); err != nil {
		return c, err
	}
	if c.ID, err = ensureID(c.ID); err != nil {
		return c, err
	}
	s := c.Setting
	if s.Allocation != nil {
		return c, r.recordAllocation(ctx, c)
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO policy_values (id, jurisdiction_id, lever_code, value_kind, value, set_by_player_id, office_id,
		        set_at, effective_at)
		 VALUES ($1::uuid, $2::uuid, $3, 'scalar', $4, $5::uuid, $6::uuid, $7, $8)`,
		s.ID, s.JurisdictionID, s.LeverCode, s.Value, s.SetByPlayerID, s.OfficeID, s.SetAt.UTC(), s.EffectiveAt.UTC())
	switch {
	case violates(err, sqlstateCheckViolation, policyValuesWithinAllocation):
		return c, application.ErrInvalidAllocation.WithCause(err)
	case violates(err, sqlstateCheckViolation, policyValuesWithinBounds):
		return c, application.ErrPolicyOutOfBounds.WithCause(err)
	case violates(err, sqlstateCheckViolation, policyValuesCooldown):
		return c, application.ErrPolicyCooldown.WithCause(err)
	case err != nil:
		return c, fmt.Errorf("postgres: writing policy value: %w", err)
	}

	if _, err := r.q.Exec(ctx,
		`INSERT INTO policy_changes (id, policy_value_id, jurisdiction_id, lever_code, office_id, office_code,
		        set_by_player_id, value_kind, old_value, new_value, set_at, effective_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7::uuid, 'scalar', $8, $9, $10, $11)`,
		c.ID, s.ID, s.JurisdictionID, s.LeverCode, s.OfficeID, c.OfficeCode,
		s.SetByPlayerID, c.OldValue, s.Value, s.SetAt.UTC(), s.EffectiveAt.UTC()); err != nil {
		return c, fmt.Errorf("postgres: writing policy change: %w", err)
	}
	return c, nil
}

// recordAllocation writes an allocation's setting and public record: the
// shares as a document, the old and the new.
func (r *GovernanceRepository) recordAllocation(ctx context.Context, c application.PolicyChange) error {
	s := c.Setting
	newDoc, err := json.Marshal(s.Allocation)
	if err != nil {
		return fmt.Errorf("postgres: encoding allocation: %w", err)
	}
	old := c.OldAllocation
	if old == nil {
		old = map[string]int64{}
	}
	oldDoc, err := json.Marshal(old)
	if err != nil {
		return fmt.Errorf("postgres: encoding allocation: %w", err)
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO policy_values (id, jurisdiction_id, lever_code, value_kind, value_json, set_by_player_id, office_id,
		        set_at, effective_at)
		 VALUES ($1::uuid, $2::uuid, $3, 'structured', $4::jsonb, $5::uuid, $6::uuid, $7, $8)`,
		s.ID, s.JurisdictionID, s.LeverCode, newDoc, s.SetByPlayerID, s.OfficeID, s.SetAt.UTC(), s.EffectiveAt.UTC())
	switch {
	case violates(err, sqlstateCheckViolation, policyValuesWithinAllocation):
		return application.ErrInvalidAllocation.WithCause(err)
	case violates(err, sqlstateCheckViolation, policyValuesCooldown):
		return application.ErrPolicyCooldown.WithCause(err)
	case err != nil:
		return fmt.Errorf("postgres: writing policy value: %w", err)
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO policy_changes (id, policy_value_id, jurisdiction_id, lever_code, office_id, office_code,
		        set_by_player_id, value_kind, old_value_json, new_value_json, set_at, effective_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7::uuid, 'structured', $8::jsonb, $9::jsonb, $10, $11)`,
		c.ID, s.ID, s.JurisdictionID, s.LeverCode, s.OfficeID, c.OfficeCode,
		s.SetByPlayerID, oldDoc, newDoc, s.SetAt.UTC(), s.EffectiveAt.UTC()); err != nil {
		return fmt.Errorf("postgres: writing policy change: %w", err)
	}
	return nil
}

// Jurisdiction returns one jurisdiction, or ErrJurisdictionNotFound.
func (r *GovernanceRepository) Jurisdiction(ctx context.Context, id string) (application.Jurisdiction, error) {
	j, _, err := jurisdictionRow(ctx, r.q, id)
	return j, err
}

// OfficeDefinitions returns every office of the active content.
func (r *GovernanceRepository) OfficeDefinitions(ctx context.Context) ([]application.OfficeDefinition, error) {
	return activeOffices(ctx, r.q)
}

// Seat returns one seat locked FOR UPDATE, or ErrOfficeNotFound.
func (r *GovernanceRepository) Seat(ctx context.Context, officeCode, jurisdictionID string, seat int) (application.Office, error) {
	if !validUUID(jurisdictionID) {
		return application.Office{}, application.ErrOfficeNotFound
	}
	o, err := scanOffice(r.q.QueryRow(ctx,
		`SELECT `+officeColumns+`
		   FROM offices
		  WHERE office_code = $1 AND jurisdiction_id = $2::uuid AND seat = $3
		    FOR UPDATE`, officeCode, jurisdictionID, seat))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, application.ErrOfficeNotFound.WithCause(
			fmt.Errorf("no seat %d of %s in jurisdiction %s", seat, officeCode, jurisdictionID))
	}
	return o, err
}

// ActingChain returns an office and its deputies with their seats in the
// jurisdiction, locked FOR SHARE.
func (r *GovernanceRepository) ActingChain(ctx context.Context, officeCode, jurisdictionID string) ([]application.OfficeLink, error) {
	if !validUUID(jurisdictionID) {
		return nil, application.ErrJurisdictionNotFound
	}
	return actingChain(ctx, r.q, officeCode, jurisdictionID, true)
}

// SeatsHeldBy returns every seat the player holds, anywhere.
func (r *GovernanceRepository) SeatsHeldBy(ctx context.Context, playerID string) ([]application.Office, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT `+officeColumns+` FROM offices WHERE holder_player_id = $1::uuid ORDER BY office_code, seat`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading seats held: %w", err)
	}
	defer rows.Close()
	var out []application.Office
	for rows.Next() {
		o, err := scanOffice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading seats held: %w", err)
	}
	return out, nil
}

// AssignSeat writes a seat's holder, acquisition, term and since.
func (r *GovernanceRepository) AssignSeat(ctx context.Context, o application.Office) error {
	_, err := r.q.Exec(ctx,
		`UPDATE offices
		    SET holder_player_id = NULLIF($2, '')::uuid,
		        acquired_by      = NULLIF($3, ''),
		        term_ends_at     = $4,
		        since            = $5
		  WHERE id = $1::uuid`,
		o.ID, o.HolderPlayerID, o.AcquiredBy, o.TermEndsAt, o.Since.UTC())
	if violates(err, sqlstateUniqueViolation, officesOneSeatPerHolder) {
		return application.ErrAlreadyHoldsSeat.WithCause(err)
	}
	if err != nil {
		return fmt.Errorf("postgres: writing office seat: %w", err)
	}
	return nil
}
