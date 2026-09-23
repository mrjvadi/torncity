package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// GovernanceAdmin is the operator's tooling for offices (ADR 0015): appoint and
// vacate a seat — the only way a player holds an office until elections exist —
// and the reads `admin policy show` and `admin office list` print.
//
// An appointment and its audit row commit together, in one transaction this
// type opens, like a content load: an office change with no audit row is the
// one an investigator would most want to find.
type GovernanceAdmin struct {
	pool *pgxpool.Pool
}

// NewGovernanceAdmin returns the tooling backed by p.
func NewGovernanceAdmin(p *Pool) *GovernanceAdmin { return &GovernanceAdmin{pool: p.Raw()} }

// SeatRef names one seat the way an operator types it.
type SeatRef struct {
	OfficeCode string
	// JurisdictionKind and JurisdictionCode name the place: ("city",
	// "ostmarch").
	JurisdictionKind string
	JurisdictionCode string
	Seat             int
}

// SeatChangeRequest is an appointment (PlayerCode set) or a vacancy.
type SeatChangeRequest struct {
	SeatRef
	// PlayerCode is the appointee's public code; empty for a vacate.
	PlayerCode string
	Actor      string
	Reason     string
	At         time.Time
}

// SeatChange is what an appointment or a vacancy did.
type SeatChange struct {
	Jurisdiction  application.Jurisdiction
	Before, After application.Office
	// PlayerLabel names the appointee (or, for a vacate, the holder who left).
	PlayerLabel string
}

// Appoint seats the player whose public code is given, through
// application.AppointToOffice, and records the audit row in the same
// transaction.
func (a *GovernanceAdmin) Appoint(ctx context.Context, req SeatChangeRequest) (SeatChange, error) {
	return a.changeSeat(ctx, req, true)
}

// Vacate empties the seat, through application.VacateOffice, and records the
// audit row in the same transaction. The values its holder set stay in force.
func (a *GovernanceAdmin) Vacate(ctx context.Context, req SeatChangeRequest) (SeatChange, error) {
	return a.changeSeat(ctx, req, false)
}

func (a *GovernanceAdmin) changeSeat(ctx context.Context, req SeatChangeRequest, appoint bool) (SeatChange, error) {
	var out SeatChange
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return out, ErrNoReason
	}
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		actor = "unknown"
	}
	if req.At.IsZero() {
		req.At = time.Now()
	}

	pgtx, err := a.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("postgres: office change: begin: %w", err)
	}
	defer func() { _ = pgtx.Rollback(context.WithoutCancel(ctx)) }()

	out.Jurisdiction, err = jurisdictionByCode(ctx, pgtx, req.JurisdictionKind, req.JurisdictionCode)
	if err != nil {
		return out, err
	}

	unit := &tx{q: pgtx}
	action := "office.vacate"
	if appoint {
		action = "office.appoint"
		playerID, label, err := activePlayerByCode(ctx, pgtx, req.PlayerCode)
		if err != nil {
			return out, err
		}
		out.PlayerLabel = label
		out.Before, out.After, err = application.AppointToOffice(ctx, unit,
			req.OfficeCode, out.Jurisdiction.ID, req.Seat, playerID, req.At)
		if err != nil {
			return out, err
		}
	} else {
		out.Before, out.After, err = application.VacateOffice(ctx, unit,
			req.OfficeCode, out.Jurisdiction.ID, req.Seat, req.At)
		if err != nil {
			return out, err
		}
		labels, err := playerLabels(ctx, pgtx, []string{out.Before.HolderPlayerID})
		if err != nil {
			return out, err
		}
		out.PlayerLabel = labels[out.Before.HolderPlayerID]
	}

	if err := appendSeatAudit(ctx, pgtx, action, actor, reason, out, req.At); err != nil {
		return out, err
	}
	if err := pgtx.Commit(ctx); err != nil {
		return out, fmt.Errorf("postgres: office change: commit: %w", err)
	}
	return out, nil
}

// seatAuditValue is one side of the audit row.
func seatAuditValue(j application.Jurisdiction, o application.Office) map[string]any {
	v := map[string]any{
		"office":       o.OfficeCode,
		"jurisdiction": j.Kind + ":" + j.Code,
		"seat":         o.Seat,
		"holder":       nil,
		"acquired_by":  nil,
		"term_ends_at": nil,
		"since":        o.Since.UTC(),
	}
	if !o.Vacant() {
		v["holder"] = o.HolderPlayerID
		v["acquired_by"] = o.AcquiredBy
	}
	if o.TermEndsAt != nil {
		v["term_ends_at"] = o.TermEndsAt.UTC()
	}
	return v
}

// appendSeatAudit records an appointment or a vacancy in audit_logs.
func appendSeatAudit(ctx context.Context, q querier, action, actor, reason string, c SeatChange, at time.Time) error {
	oldValue, err := json.Marshal(seatAuditValue(c.Jurisdiction, c.Before))
	if err != nil {
		return fmt.Errorf("postgres: encoding audit value: %w", err)
	}
	after := seatAuditValue(c.Jurisdiction, c.After)
	after["player"] = c.PlayerLabel
	newValue, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("postgres: encoding audit value: %w", err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		 VALUES ($1, $2, 'office', $3::uuid, $4::jsonb, $5::jsonb, $6, $7)`,
		actor, action, c.After.ID, string(oldValue), string(newValue), reason, at.UTC()); err != nil {
		return fmt.Errorf("postgres: writing office audit row: %w", err)
	}
	return nil
}

// ErrUnknownJurisdictionCode means no jurisdiction of that kind has that code.
var ErrUnknownJurisdictionCode = errors.New("postgres: no jurisdiction with that code")

// JurisdictionByCode finds a jurisdiction by level and code.
func (a *GovernanceAdmin) JurisdictionByCode(ctx context.Context, kind, code string) (application.Jurisdiction, error) {
	return jurisdictionByCode(ctx, a.pool, kind, code)
}

func jurisdictionByCode(ctx context.Context, q querier, kind, code string) (application.Jurisdiction, error) {
	var j application.Jurisdiction
	err := q.QueryRow(ctx,
		`SELECT id::text, kind, code, name, COALESCE(parent_id::text, '')
		   FROM jurisdictions WHERE kind = $1 AND code = $2`, kind, code).
		Scan(&j.ID, &j.Kind, &j.Code, &j.Name, &j.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, fmt.Errorf("%w: %s %q (has content with governance been loaded?)", ErrUnknownJurisdictionCode, kind, code)
	}
	if err != nil {
		return j, fmt.Errorf("postgres: reading jurisdiction %s %q: %w", kind, code, err)
	}
	return j, nil
}

// Ancestry returns the jurisdiction with this id and every ancestor below the
// world, nearest first.
func (a *GovernanceAdmin) Ancestry(ctx context.Context, id string) ([]application.Jurisdiction, error) {
	rows, err := a.pool.Query(ctx,
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
	return out, rows.Err()
}

// ActiveLevers returns every lever of the active content, ordered by code.
func (a *GovernanceAdmin) ActiveLevers(ctx context.Context) ([]application.LeverDefinition, error) {
	rows, err := a.pool.Query(ctx,
		`SELECT ld.code FROM lever_definitions ld
		   JOIN content_versions cv ON cv.id = ld.content_version_id
		  WHERE cv.status = 'active' ORDER BY ld.code`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing levers: %w", err)
	}
	var codes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning lever: %w", err)
		}
		codes = append(codes, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing levers: %w", err)
	}
	out := make([]application.LeverDefinition, 0, len(codes))
	for _, c := range codes {
		l, err := activeLever(ctx, a.pool, c)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// SeatView is one seat with its place and holder, for listing.
type SeatView struct {
	application.Office
	JurisdictionKind string
	JurisdictionCode string
	HolderLabel      string
}

// Seats lists the seats of the given jurisdictions, or of every jurisdiction
// when none is given, ordered by place, office and seat.
func (a *GovernanceAdmin) Seats(ctx context.Context, jurisdictionIDs []string) ([]SeatView, error) {
	rows, err := a.pool.Query(ctx,
		`SELECT o.id::text, o.office_code, o.jurisdiction_id::text, o.seat, COALESCE(o.holder_player_id::text, ''),
		        o.term_ends_at, COALESCE(o.acquired_by, ''), o.since, j.kind, j.code
		   FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
		  WHERE cardinality($1::uuid[]) = 0 OR o.jurisdiction_id = ANY($1::uuid[])
		  ORDER BY j.kind, j.code, o.office_code, o.seat`, nonNil(jurisdictionIDs))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing seats: %w", err)
	}
	var out []SeatView
	var holders []string
	for rows.Next() {
		var s SeatView
		if err := rows.Scan(&s.ID, &s.OfficeCode, &s.JurisdictionID, &s.Seat, &s.HolderPlayerID,
			&s.TermEndsAt, &s.AcquiredBy, &s.Since, &s.JurisdictionKind, &s.JurisdictionCode); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning seat: %w", err)
		}
		if !s.Vacant() {
			holders = append(holders, s.HolderPlayerID)
		}
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing seats: %w", err)
	}
	labels, err := playerLabels(ctx, a.pool, holders)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].HolderLabel = labels[out[i].HolderPlayerID]
	}
	return out, nil
}

// PlayerLabels names players for an operator: "CODE (display name)".
func (a *GovernanceAdmin) PlayerLabels(ctx context.Context, ids []string) (map[string]string, error) {
	return playerLabels(ctx, a.pool, ids)
}

func playerLabels(ctx context.Context, q querier, ids []string) (map[string]string, error) {
	out := map[string]string{}
	var valid []string
	for _, id := range ids {
		if validUUID(id) {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx,
		`SELECT id::text, public_code, display_name FROM players WHERE id = ANY($1::uuid[])`, valid)
	if err != nil {
		return nil, fmt.Errorf("postgres: naming players: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, code, name string
		if err := rows.Scan(&id, &code, &name); err != nil {
			return nil, fmt.Errorf("postgres: naming players: %w", err)
		}
		out[id] = fmt.Sprintf("%s (%s)", code, name)
	}
	return out, rows.Err()
}

// activePlayerByCode resolves a public code to an active player.
func activePlayerByCode(ctx context.Context, q querier, raw string) (id, label string, err error) {
	code := playercode.Normalize(raw)
	if !playercode.Valid(code) {
		return "", "", application.ErrPlayerNotFound.WithCause(fmt.Errorf("%q is not a player code", raw))
	}
	var name string
	err = q.QueryRow(ctx,
		`SELECT id::text, display_name FROM players WHERE public_code = $1 AND status = 'active'`, code).
		Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", application.ErrPlayerNotFound.WithCause(fmt.Errorf("no active player has code %s", code))
	}
	if err != nil {
		return "", "", fmt.Errorf("postgres: finding player %s: %w", code, err)
	}
	return id, fmt.Sprintf("%s (%s)", code, name), nil
}
