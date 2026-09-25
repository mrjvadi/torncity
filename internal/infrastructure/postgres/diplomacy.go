package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
)

// This file persists diplomacy (migrations/0021_military): sanctions,
// treaties and their public record, and answers which country a city or a
// player belongs to — a player to the country of the city they live in.

const (
	sanctionsOneStandingIdx = "sanctions_one_standing_idx"
	treatiesOneOpenIdx      = "treaties_one_open_idx"
)

// DiplomacyRepository implements application.DiplomacyRepository.
type DiplomacyRepository struct {
	q querier
}

var _ application.DiplomacyRepository = (*DiplomacyRepository)(nil)

// NewDiplomacyRepository returns the repository over the pool, for reads
// outside a unit of work.
func NewDiplomacyRepository(p *Pool) *DiplomacyRepository { return &DiplomacyRepository{q: p.Raw()} }

// countryOfJurisdiction is the recursive walk from a jurisdiction up to the
// country above it (or itself).
const countryOfJurisdiction = `
WITH RECURSIVE up AS (
    SELECT id, kind, parent_id, 0 AS depth FROM jurisdictions WHERE id = $1::uuid
    UNION ALL
    SELECT j.id, j.kind, j.parent_id, up.depth + 1 FROM jurisdictions j JOIN up ON j.id = up.parent_id WHERE up.depth < 16
)
SELECT id::text FROM up WHERE kind = 'country' ORDER BY depth LIMIT 1`

// LockCountry takes a row lock on the country's jurisdiction.
func (r *DiplomacyRepository) LockCountry(ctx context.Context, countryID string) error {
	if !validUUID(countryID) {
		return application.ErrJurisdictionNotFound
	}
	var id string
	err := r.q.QueryRow(ctx, `SELECT id::text FROM jurisdictions WHERE id = $1::uuid AND kind = 'country' FOR UPDATE`, countryID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrJurisdictionNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: locking a country: %w", err)
	}
	return nil
}

// Countries lists every country by code.
func (r *DiplomacyRepository) Countries(ctx context.Context) ([]application.Jurisdiction, error) {
	rows, err := r.q.Query(ctx,
		`SELECT id::text, kind, code, name, COALESCE(parent_id::text, '') FROM jurisdictions WHERE kind = 'country' ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing countries: %w", err)
	}
	defer rows.Close()
	var out []application.Jurisdiction
	for rows.Next() {
		var j application.Jurisdiction
		if err := rows.Scan(&j.ID, &j.Kind, &j.Code, &j.Name, &j.ParentID); err != nil {
			return nil, fmt.Errorf("postgres: scanning a country: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountryByCode finds a country.
func (r *DiplomacyRepository) CountryByCode(ctx context.Context, code string) (application.Jurisdiction, error) {
	var j application.Jurisdiction
	err := r.q.QueryRow(ctx,
		`SELECT id::text, kind, code, name, COALESCE(parent_id::text, '') FROM jurisdictions WHERE kind = 'country' AND code = $1`,
		code).Scan(&j.ID, &j.Kind, &j.Code, &j.Name, &j.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, application.ErrJurisdictionNotFound
	}
	if err != nil {
		return j, fmt.Errorf("postgres: finding a country: %w", err)
	}
	return j, nil
}

// CountryOfCity is the country above a city.
func (r *DiplomacyRepository) CountryOfCity(ctx context.Context, cityID string) (string, error) {
	if !validUUID(cityID) {
		return "", nil
	}
	var jid *string
	err := r.q.QueryRow(ctx, `SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, cityID).Scan(&jid)
	if errors.Is(err, pgx.ErrNoRows) || jid == nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading a city's jurisdiction: %w", err)
	}
	var country string
	err = r.q.QueryRow(ctx, countryOfJurisdiction, *jid).Scan(&country)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: finding a city's country: %w", err)
	}
	return country, nil
}

// CountriesOfPlayers maps players to the country they live in.
func (r *DiplomacyRepository) CountriesOfPlayers(ctx context.Context, playerIDs []string) (map[string]string, error) {
	out := map[string]string{}
	var ids []string
	for _, id := range playerIDs {
		if validUUID(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `
		WITH RECURSIVE up AS (
		    SELECT p.id AS player_id, j.id, j.kind, j.parent_id, 0 AS depth
		      FROM players p JOIN cities c ON c.id = p.residence_city_id JOIN jurisdictions j ON j.id = c.jurisdiction_id
		     WHERE p.id = ANY($1::uuid[])
		    UNION ALL
		    SELECT up.player_id, j.id, j.kind, j.parent_id, up.depth + 1
		      FROM jurisdictions j JOIN up ON j.id = up.parent_id WHERE up.kind <> 'country' AND up.depth < 16
		)
		SELECT player_id::text, id::text FROM up WHERE kind = 'country'`, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: finding players' countries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p, c string
		if err := rows.Scan(&p, &c); err != nil {
			return nil, fmt.Errorf("postgres: scanning a player's country: %w", err)
		}
		out[p] = c
	}
	return out, rows.Err()
}

// CitiesOf lists a country's cities.
func (r *DiplomacyRepository) CitiesOf(ctx context.Context, countryID string) ([]application.City, error) {
	if !validUUID(countryID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `
		WITH RECURSIVE down AS (
		    SELECT id, 0 AS depth FROM jurisdictions WHERE id = $1::uuid
		    UNION ALL
		    SELECT j.id, down.depth + 1 FROM jurisdictions j JOIN down ON j.parent_id = down.id WHERE down.depth < 16
		)
		SELECT c.id::text, c.code, c.name, COALESCE(c.jurisdiction_id::text, ''), c.cost_of_living, c.population
		  FROM cities c WHERE c.jurisdiction_id IN (SELECT id FROM down) ORDER BY c.code`, countryID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a country's cities: %w", err)
	}
	defer rows.Close()
	var out []application.City
	for rows.Next() {
		var c application.City
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.JurisdictionID, &c.CostOfLiving, &c.Population); err != nil {
			return nil, fmt.Errorf("postgres: scanning a city: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const sanctionColumns = `id::text, no, imposer_id::text, target_id::text, measures, ground, imposed_by::text, imposed_office,
       imposed_at, effective_at, COALESCE(lifted_by::text, ''), COALESCE(lifted_office, ''), lifted_at`

func scanSanction(row pgx.Row) (*application.Sanction, error) {
	var (
		s        application.Sanction
		measures []string
	)
	if err := row.Scan(&s.ID, &s.No, &s.ImposerID, &s.TargetID, &measures, &s.Ground, &s.ImposedBy, &s.ImposedOffice,
		&s.ImposedAt, &s.EffectiveAt, &s.LiftedBy, &s.LiftedOffice, &s.LiftedAt); err != nil {
		return nil, err
	}
	for _, m := range measures {
		s.Measures = append(s.Measures, diplomacy.Measure(m))
	}
	s.ImposedAt, s.EffectiveAt = s.ImposedAt.UTC(), s.EffectiveAt.UTC()
	if s.LiftedAt != nil {
		t := s.LiftedAt.UTC()
		s.LiftedAt = &t
	}
	return &s, nil
}

func (r *DiplomacyRepository) sanctions(ctx context.Context, sql string, args ...any) ([]application.Sanction, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading sanctions: %w", err)
	}
	defer rows.Close()
	var out []application.Sanction
	for rows.Next() {
		s, err := scanSanction(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a sanction: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// ImposeSanction records a sanction.
func (r *DiplomacyRepository) ImposeSanction(ctx context.Context, s application.Sanction) (application.Sanction, error) {
	measures := make([]string, 0, len(s.Measures))
	for _, m := range s.Measures {
		measures = append(measures, string(m))
	}
	err := r.q.QueryRow(ctx,
		`INSERT INTO sanctions (id, imposer_id, target_id, measures, ground, imposed_by, imposed_office, imposed_at, effective_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, $7, $8, $9) RETURNING no`,
		s.ID, s.ImposerID, s.TargetID, measures, s.Ground, s.ImposedBy, s.ImposedOffice, s.ImposedAt.UTC(),
		s.EffectiveAt.UTC()).Scan(&s.No)
	if violates(err, sqlstateUniqueViolation, sanctionsOneStandingIdx) {
		return s, application.ErrAlreadySanctioned
	}
	if err != nil {
		return s, fmt.Errorf("postgres: recording a sanction: %w", err)
	}
	return s, nil
}

// SanctionByNo reads a sanction.
func (r *DiplomacyRepository) SanctionByNo(ctx context.Context, no int64, lock bool) (*application.Sanction, error) {
	sql := `SELECT ` + sanctionColumns + ` FROM sanctions WHERE no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	s, err := scanSanction(r.q.QueryRow(ctx, sql, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrSanctionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a sanction: %w", err)
	}
	return s, nil
}

// LiftSanction records a lifting.
func (r *DiplomacyRepository) LiftSanction(ctx context.Context, id, by, office string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE sanctions SET lifted_by = $2::uuid, lifted_office = $3, lifted_at = $4 WHERE id = $1::uuid AND lifted_at IS NULL`,
		id, by, office, at.UTC()); err != nil {
		return fmt.Errorf("postgres: lifting a sanction: %w", err)
	}
	return nil
}

// StandingSanctions lists the sanctions not lifted.
func (r *DiplomacyRepository) StandingSanctions(ctx context.Context, countryID string) ([]application.Sanction, error) {
	if countryID == "" {
		return r.sanctions(ctx, `SELECT `+sanctionColumns+` FROM sanctions WHERE lifted_at IS NULL ORDER BY imposed_at DESC, no DESC`)
	}
	if !validUUID(countryID) {
		return nil, nil
	}
	return r.sanctions(ctx, `SELECT `+sanctionColumns+` FROM sanctions
		WHERE lifted_at IS NULL AND (imposer_id = $1::uuid OR target_id = $1::uuid) ORDER BY imposed_at DESC, no DESC`, countryID)
}

// SanctionsBetween lists the sanctions not lifted between two countries.
func (r *DiplomacyRepository) SanctionsBetween(ctx context.Context, a, b string) ([]application.Sanction, error) {
	if !validUUID(a) || !validUUID(b) {
		return nil, nil
	}
	return r.sanctions(ctx, `SELECT `+sanctionColumns+` FROM sanctions
		WHERE lifted_at IS NULL AND ((imposer_id = $1::uuid AND target_id = $2::uuid) OR (imposer_id = $2::uuid AND target_id = $1::uuid))
		ORDER BY no`, a, b)
}

const treatyColumns = `id::text, no, kind, proposer_id::text, partner_id::text, status, proposed_by::text, proposed_office,
       proposed_at, expires_at, COALESCE(decided_by::text, ''), COALESCE(decided_office, ''), decided_at,
       COALESCE(ended_by::text, ''), COALESCE(ended_office, ''), ended_at`

func scanTreaty(row pgx.Row) (*application.Treaty, error) {
	var (
		t      application.Treaty
		status string
	)
	if err := row.Scan(&t.ID, &t.No, &t.Kind, &t.ProposerID, &t.PartnerID, &status, &t.ProposedBy, &t.ProposedOffice,
		&t.ProposedAt, &t.ExpiresAt, &t.DecidedBy, &t.DecidedOffice, &t.DecidedAt, &t.EndedBy, &t.EndedOffice, &t.EndedAt); err != nil {
		return nil, err
	}
	t.Status = diplomacy.Status(status)
	t.ProposedAt, t.ExpiresAt = t.ProposedAt.UTC(), t.ExpiresAt.UTC()
	for _, p := range []**time.Time{&t.DecidedAt, &t.EndedAt} {
		if *p != nil {
			u := (*p).UTC()
			*p = &u
		}
	}
	return &t, nil
}

func (r *DiplomacyRepository) treaties(ctx context.Context, sql string, args ...any) ([]application.Treaty, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading treaties: %w", err)
	}
	defer rows.Close()
	var out []application.Treaty
	for rows.Next() {
		t, err := scanTreaty(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a treaty: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ExpireTreaties marks lapsed proposals between two countries.
func (r *DiplomacyRepository) ExpireTreaties(ctx context.Context, a, b string, now time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE treaties SET status = 'expired'
		  WHERE status = 'proposed' AND expires_at <= $3
		    AND ((proposer_id = $1::uuid AND partner_id = $2::uuid) OR (proposer_id = $2::uuid AND partner_id = $1::uuid))`,
		a, b, now.UTC()); err != nil {
		return fmt.Errorf("postgres: expiring treaty proposals: %w", err)
	}
	return nil
}

// ProposeTreaty records a proposal.
func (r *DiplomacyRepository) ProposeTreaty(ctx context.Context, t application.Treaty) (application.Treaty, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO treaties (id, kind, proposer_id, partner_id, status, proposed_by, proposed_office, proposed_at, expires_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'proposed', $5::uuid, $6, $7, $8) RETURNING no`,
		t.ID, t.Kind, t.ProposerID, t.PartnerID, t.ProposedBy, t.ProposedOffice, t.ProposedAt.UTC(), t.ExpiresAt.UTC()).Scan(&t.No)
	if violates(err, sqlstateUniqueViolation, treatiesOneOpenIdx) {
		return t, application.ErrTreatyOpen
	}
	if err != nil {
		return t, fmt.Errorf("postgres: recording a treaty: %w", err)
	}
	t.Status = diplomacy.Proposed
	return t, nil
}

// TreatyByNo reads a treaty.
func (r *DiplomacyRepository) TreatyByNo(ctx context.Context, no int64, lock bool) (*application.Treaty, error) {
	sql := `SELECT ` + treatyColumns + ` FROM treaties WHERE no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	t, err := scanTreaty(r.q.QueryRow(ctx, sql, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrTreatyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a treaty: %w", err)
	}
	return t, nil
}

// SaveTreaty writes a treaty's status, answer and end.
func (r *DiplomacyRepository) SaveTreaty(ctx context.Context, t application.Treaty) error {
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	_, err := r.q.Exec(ctx,
		`UPDATE treaties SET status = $2, decided_by = $3::uuid, decided_office = $4, decided_at = $5,
		        ended_by = $6::uuid, ended_office = $7, ended_at = $8
		  WHERE id = $1::uuid`,
		t.ID, string(t.Status), nullable(t.DecidedBy), nullable(t.DecidedOffice), t.DecidedAt,
		nullable(t.EndedBy), nullable(t.EndedOffice), t.EndedAt)
	if violates(err, sqlstateUniqueViolation, treatiesOneOpenIdx) {
		return application.ErrTreatyOpen
	}
	if err != nil {
		return fmt.Errorf("postgres: saving a treaty: %w", err)
	}
	return nil
}

// Treaties lists a country's open treaties and those ended since since.
func (r *DiplomacyRepository) Treaties(ctx context.Context, countryID string, since time.Time) ([]application.Treaty, error) {
	if !validUUID(countryID) {
		return nil, nil
	}
	return r.treaties(ctx, `SELECT `+treatyColumns+` FROM treaties
		WHERE (proposer_id = $1::uuid OR partner_id = $1::uuid)
		  AND (status IN ('proposed', 'active') OR COALESCE(ended_at, decided_at, expires_at) >= $2)
		ORDER BY CASE status WHEN 'proposed' THEN 0 WHEN 'active' THEN 1 ELSE 2 END, proposed_at DESC, no DESC`,
		countryID, since.UTC())
}

// TreatiesBetween lists the treaties between two countries.
func (r *DiplomacyRepository) TreatiesBetween(ctx context.Context, a, b string) ([]application.Treaty, error) {
	if !validUUID(a) || !validUUID(b) {
		return nil, nil
	}
	return r.treaties(ctx, `SELECT `+treatyColumns+` FROM treaties
		WHERE (proposer_id = $1::uuid AND partner_id = $2::uuid) OR (proposer_id = $2::uuid AND partner_id = $1::uuid)
		ORDER BY no`, a, b)
}

// RecordEvent appends to the public record.
func (r *DiplomacyRepository) RecordEvent(ctx context.Context, e application.DiplomacyEvent) error {
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO diplomacy_events (id, kind, country_id, other_country_id, sanction_id, treaty_id, player_id,
		        office_code, created_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8, $9)`,
		e.ID, e.Kind, e.CountryID, e.OtherCountryID, nullable(e.SanctionID), nullable(e.TreatyID), nullable(e.PlayerID),
		nullable(e.OfficeCode), e.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a diplomacy event: %w", err)
	}
	return nil
}

// Events lists one page of the public record.
func (r *DiplomacyRepository) Events(ctx context.Context, countryID string, limit, offset int) ([]application.DiplomacyEvent, int, error) {
	where, args := `TRUE`, []any{}
	if countryID != "" {
		if !validUUID(countryID) {
			return nil, 0, nil
		}
		where, args = `(e.country_id = $1::uuid OR e.other_country_id = $1::uuid)`, []any{countryID}
	}
	var total int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM diplomacy_events e WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: counting diplomacy events: %w", err)
	}
	n := len(args)
	rows, err := r.q.Query(ctx, fmt.Sprintf(`
		SELECT e.id::text, e.kind, e.country_id::text, e.other_country_id::text, COALESCE(e.sanction_id::text, ''),
		       COALESCE(e.treaty_id::text, ''), COALESCE(e.player_id::text, ''), COALESCE(e.office_code, ''), e.created_at,
		       COALESCE(s.no, 0), COALESCE(t.no, 0), COALESCE(s.measures, '{}'), COALESCE(s.ground, ''), COALESCE(t.kind, '')
		  FROM diplomacy_events e
		  LEFT JOIN sanctions s ON s.id = e.sanction_id
		  LEFT JOIN treaties t ON t.id = e.treaty_id
		 WHERE %s
		 ORDER BY e.created_at DESC, e.id
		 LIMIT $%d OFFSET $%d`, where, n+1, n+2), append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: reading diplomacy events: %w", err)
	}
	defer rows.Close()
	var out []application.DiplomacyEvent
	for rows.Next() {
		var (
			e        application.DiplomacyEvent
			measures []string
		)
		if err := rows.Scan(&e.ID, &e.Kind, &e.CountryID, &e.OtherCountryID, &e.SanctionID, &e.TreatyID, &e.PlayerID,
			&e.OfficeCode, &e.At, &e.SanctionNo, &e.TreatyNo, &measures, &e.Ground, &e.TreatyKind); err != nil {
			return nil, 0, fmt.Errorf("postgres: scanning a diplomacy event: %w", err)
		}
		for _, m := range measures {
			e.Measures = append(e.Measures, diplomacy.Measure(m))
		}
		e.At = e.At.UTC()
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// RecordTariff appends the tariff withheld from one sale.
func (r *DiplomacyRepository) RecordTariff(ctx context.Context, t application.BorderTariff) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO border_tariffs (id, reference_type, reference_id, importer_id, exporter_id, value, rate_bps, tariff, at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9)`,
		t.ID, t.ReferenceType, t.ReferenceID, t.ImporterID, t.ExporterID, t.Value, t.RateBPS, t.Tariff, t.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a border tariff: %w", err)
	}
	return nil
}
