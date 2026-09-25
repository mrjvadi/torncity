package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// GovernanceDirectory is the postgres application.GovernanceDirectory: the
// facts the city and office screens show around a policy value. It reads
// committed rows on the pool and writes nothing. It never reads a lever's
// value; that is PolicyStore.Get's alone.
type GovernanceDirectory struct {
	q querier
}

var _ application.GovernanceDirectory = (*GovernanceDirectory)(nil)

// NewGovernanceDirectory returns the directory backed by p.
func NewGovernanceDirectory(p *Pool) *GovernanceDirectory { return &GovernanceDirectory{q: p.Raw()} }

// selectActiveLevers reads every lever of the active content version, in
// scanLever's column order.
const selectActiveLevers = `
SELECT ` + leverColumns + `
  FROM lever_definitions ld
  JOIN content_versions cv ON cv.id = ld.content_version_id
 WHERE cv.status = 'active'
 ORDER BY ld.code`

// JurisdictionByCode finds a jurisdiction by level and code.
func (d *GovernanceDirectory) JurisdictionByCode(ctx context.Context, kind, code string) (application.Jurisdiction, error) {
	var j application.Jurisdiction
	err := d.q.QueryRow(ctx,
		`SELECT id::text, kind, code, name, COALESCE(parent_id::text, '')
		   FROM jurisdictions WHERE kind = $1 AND code = $2`, kind, code).
		Scan(&j.ID, &j.Kind, &j.Code, &j.Name, &j.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, application.ErrJurisdictionNotFound.WithCause(fmt.Errorf("no %s with code %q", kind, code))
	}
	if err != nil {
		return j, fmt.Errorf("postgres: reading jurisdiction %s %q: %w", kind, code, err)
	}
	return j, nil
}

// Ancestry returns the jurisdiction and its ancestors below the world,
// nearest first. The walk is bounded, like the admin's, so a corrupt parent
// cycle cannot make a player's screen hang.
func (d *GovernanceDirectory) Ancestry(ctx context.Context, id string) ([]application.Jurisdiction, error) {
	if !validUUID(id) {
		return nil, application.ErrJurisdictionNotFound
	}
	rows, err := d.q.Query(ctx,
		`WITH RECURSIVE up AS (
		     SELECT id, kind, code, name, parent_id, 0 AS depth FROM jurisdictions WHERE id = $1::uuid
		     UNION ALL
		     SELECT j.id, j.kind, j.code, j.name, j.parent_id, up.depth + 1
		       FROM jurisdictions j JOIN up ON j.id = up.parent_id
		      WHERE up.depth < 32)
		 SELECT id::text, kind, code, name, COALESCE(parent_id::text, '')
		   FROM up WHERE kind <> 'world' ORDER BY depth`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading jurisdiction ancestry: %w", err)
	}
	defer rows.Close()
	var out []application.Jurisdiction
	for rows.Next() {
		var j application.Jurisdiction
		if err := rows.Scan(&j.ID, &j.Kind, &j.Code, &j.Name, &j.ParentID); err != nil {
			return nil, fmt.Errorf("postgres: scanning jurisdiction: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading jurisdiction ancestry: %w", err)
	}
	if len(out) == 0 {
		return nil, application.ErrJurisdictionNotFound
	}
	return out, nil
}

// Levers returns every lever of the active content.
func (d *GovernanceDirectory) Levers(ctx context.Context) ([]application.LeverDefinition, error) {
	rows, err := d.q.Query(ctx, selectActiveLevers)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing levers: %w", err)
	}
	defer rows.Close()
	var out []application.LeverDefinition
	for rows.Next() {
		l, err := scanLever(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning lever: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing levers: %w", err)
	}
	return out, nil
}

// Offices returns every office of the active content.
func (d *GovernanceDirectory) Offices(ctx context.Context) ([]application.OfficeDefinition, error) {
	return activeOffices(ctx, d.q)
}

// Seats returns the seats of the given jurisdictions.
func (d *GovernanceDirectory) Seats(ctx context.Context, jurisdictionIDs []string) ([]application.Office, error) {
	ids := make([]string, 0, len(jurisdictionIDs))
	for _, id := range jurisdictionIDs {
		if validUUID(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return d.offices(ctx,
		`SELECT `+officeColumns+` FROM offices WHERE jurisdiction_id = ANY($1::uuid[]) ORDER BY office_code, seat`, ids)
}

// SeatsHeldBy returns every seat the player holds.
func (d *GovernanceDirectory) SeatsHeldBy(ctx context.Context, playerID string) ([]application.Office, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	return d.offices(ctx,
		`SELECT `+officeColumns+` FROM offices WHERE holder_player_id = $1::uuid ORDER BY office_code, seat`, playerID)
}

func (d *GovernanceDirectory) offices(ctx context.Context, query string, arg any) ([]application.Office, error) {
	rows, err := d.q.Query(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading seats: %w", err)
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
		return nil, fmt.Errorf("postgres: reading seats: %w", err)
	}
	return out, nil
}

// LastPolicyChange is when the lever last changed here, in effect or not.
func (d *GovernanceDirectory) LastPolicyChange(ctx context.Context, jurisdictionID, leverCode string) (*time.Time, error) {
	if !validUUID(jurisdictionID) {
		return nil, application.ErrJurisdictionNotFound
	}
	var at *time.Time
	if err := d.q.QueryRow(ctx, selectLastPolicyChange, jurisdictionID, leverCode).Scan(&at); err != nil {
		return nil, fmt.Errorf("postgres: last policy change: %w", err)
	}
	return at, nil
}

// PolicyHistory returns one page of the public record, newest first. Only
// scalar changes are read: a structured lever has no behaviour yet, so no
// such change can exist, and one that did would have no number to show.
func (d *GovernanceDirectory) PolicyHistory(ctx context.Context, jurisdictionIDs []string, limit, offset int,
) ([]application.PolicyChangeRecord, int, error) {
	ids := make([]string, 0, len(jurisdictionIDs))
	for _, id := range jurisdictionIDs {
		if validUUID(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, 0, nil
	}
	if limit < 1 {
		limit = 1
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := d.q.QueryRow(ctx,
		`SELECT count(*) FROM policy_changes
		  WHERE jurisdiction_id = ANY($1::uuid[]) AND value_kind = 'scalar'`, ids).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: counting policy history: %w", err)
	}

	rows, err := d.q.Query(ctx,
		`SELECT id::text, jurisdiction_id::text, lever_code, office_code, set_by_player_id::text,
		        old_value, new_value, set_at, effective_at
		   FROM policy_changes
		  WHERE jurisdiction_id = ANY($1::uuid[]) AND value_kind = 'scalar'
		  ORDER BY set_at DESC, id DESC
		  LIMIT $2 OFFSET $3`, ids, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: reading policy history: %w", err)
	}
	defer rows.Close()
	var out []application.PolicyChangeRecord
	for rows.Next() {
		var r application.PolicyChangeRecord
		if err := rows.Scan(&r.ID, &r.JurisdictionID, &r.LeverCode, &r.OfficeCode, &r.SetByPlayerID,
			&r.OldValue, &r.NewValue, &r.SetAt, &r.EffectiveAt); err != nil {
			return nil, 0, fmt.Errorf("postgres: scanning policy change: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: reading policy history: %w", err)
	}
	return out, total, nil
}

// PlayerNames names players by id: public code and display name, the two
// things one player may see of another.
func (d *GovernanceDirectory) PlayerNames(ctx context.Context, ids []string) (map[string]application.PlayerName, error) {
	out := map[string]application.PlayerName{}
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if validUUID(id) {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := d.q.Query(ctx,
		`SELECT id::text, public_code, display_name FROM players WHERE id = ANY($1::uuid[])`, valid)
	if err != nil {
		return nil, fmt.Errorf("postgres: naming players: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id string
			n  application.PlayerName
		)
		if err := rows.Scan(&id, &n.PublicCode, &n.DisplayName); err != nil {
			return nil, fmt.Errorf("postgres: naming players: %w", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: naming players: %w", err)
	}
	return out, nil
}
