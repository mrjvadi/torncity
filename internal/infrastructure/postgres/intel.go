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

// IntelRepository is the agents, operations, reports, exposures, expulsions, acts of sabotage and the vetting policy (migration 0155;
// docs/adr/0077 5 and 6), bound to one transaction.
type IntelRepository struct{ q querier }

var _ application.IntelRepository = (*IntelRepository)(nil)

const intelAgentColumns = `id::text, player_id::text, sponsor_id::text, target_id::text, status, offered_by::text, cover_since, ended_at, created_at`

func scanIntelAgent(row pgx.Row) (*application.IntelAgent, error) {
	var a application.IntelAgent
	if err := row.Scan(&a.ID, &a.PlayerID, &a.SponsorID, &a.TargetID, &a.Status, &a.OfferedBy, &a.CoverSince, &a.EndedAt, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.CreatedAt, a.CoverSince, a.EndedAt = a.CreatedAt.UTC(), utcPtr(a.CoverSince), utcPtr(a.EndedAt)
	return &a, nil
}

func (r *IntelRepository) agents(ctx context.Context, sql string, args ...any) ([]application.IntelAgent, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing agents: %w", err)
	}
	defer rows.Close()
	var out []application.IntelAgent
	for rows.Next() {
		a, err := scanIntelAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an agent: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// InsertAgent records an offer; false when the player has an open one on that target (the partial unique index is the fence).
func (r *IntelRepository) InsertAgent(ctx context.Context, a application.IntelAgent) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO intel_agents (id, player_id, sponsor_id, target_id, status, offered_by, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'offered', $5::uuid, $6) ON CONFLICT DO NOTHING`, a.ID, a.PlayerID, a.SponsorID, a.TargetID, a.OfferedBy, a.CreatedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: engaging an agent: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AgentByID reads an agent, nil for none.
func (r *IntelRepository) AgentByID(ctx context.Context, id string, lock bool) (*application.IntelAgent, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + intelAgentColumns + ` FROM intel_agents WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	a, err := scanIntelAgent(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an agent: %w", err)
	}
	return a, nil
}

// OpenAgent is the player's offered or active assignment on the target, nil for none.
func (r *IntelRepository) OpenAgent(ctx context.Context, playerID, targetID string) (*application.IntelAgent, error) {
	if !validUUID(playerID) || !validUUID(targetID) {
		return nil, nil
	}
	a, err := scanIntelAgent(r.q.QueryRow(ctx, `SELECT `+intelAgentColumns+` FROM intel_agents WHERE player_id = $1::uuid AND target_id = $2::uuid AND status IN ('offered', 'active')`, playerID, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an agent: %w", err)
	}
	return a, nil
}

// AgentsOfSponsor lists the agents of a bureau's settlement, newest first.
func (r *IntelRepository) AgentsOfSponsor(ctx context.Context, sponsorID string, limit int) ([]application.IntelAgent, error) {
	return r.agents(ctx, `SELECT `+intelAgentColumns+` FROM intel_agents WHERE sponsor_id = $1::uuid ORDER BY created_at DESC, id LIMIT $2`, sponsorID, limit)
}

// AgentsOfPlayer lists a player's engagements, newest first.
func (r *IntelRepository) AgentsOfPlayer(ctx context.Context, playerID string, limit int) ([]application.IntelAgent, error) {
	return r.agents(ctx, `SELECT `+intelAgentColumns+` FROM intel_agents WHERE player_id = $1::uuid ORDER BY created_at DESC, id LIMIT $2`, playerID, limit)
}

// Activate moves an offered agent to active with its cover begun, once.
func (r *IntelRepository) Activate(ctx context.Context, id string, coverSince time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE intel_agents SET status = 'active', cover_since = $2 WHERE id = $1::uuid AND status = 'offered'`, id, coverSince.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: activating an agent: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Close ends an offered or active assignment (declined, expelled, recalled), once.
func (r *IntelRepository) Close(ctx context.Context, id, status string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE intel_agents SET status = $2, ended_at = $3 WHERE id = $1::uuid AND status IN ('offered', 'active')`, id, status, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: closing an agent: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// InsertOp records an operation; false when the agent has done that kind on that target today already (the unique key is the fence).
func (r *IntelRepository) InsertOp(ctx context.Context, o application.IntelOp) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO intel_ops (id, agent_id, sponsor_id, target_id, kind, day, chance_bps, roll, outcome, fee, wage, ledger_tx, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12::uuid, $13) ON CONFLICT DO NOTHING`,
		o.ID, o.AgentID, o.SponsorID, o.TargetID, o.Kind, o.Day, o.ChanceBPS, o.Roll, o.Outcome, o.Fee, o.Wage, nullText(o.LedgerTx), o.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording an operation: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// OpsToday is how many operations the agent did on the day.
func (r *IntelRepository) OpsToday(ctx context.Context, agentID string, day int64) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM intel_ops WHERE agent_id = $1::uuid AND day = $2`, agentID, day).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting operations: %w", err)
	}
	return n, nil
}

// InsertReport records what an operation brought back.
func (r *IntelRepository) InsertReport(ctx context.Context, rep application.IntelReport) error {
	raw, err := json.Marshal(rep.Fields)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `INSERT INTO intel_reports (id, op_id, sponsor_id, target_id, kind, fields, confidence, taken_at, expires_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::jsonb, $7, $8, $9)`,
		rep.ID, rep.OpID, rep.SponsorID, rep.TargetID, rep.Kind, string(raw), rep.Confidence, rep.TakenAt.UTC(), rep.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: recording a report: %w", err)
	}
	return nil
}

const intelReportColumns = `id::text, op_id::text, sponsor_id::text, target_id::text, kind, fields, confidence, taken_at, expires_at`

func scanIntelReport(row pgx.Row) (*application.IntelReport, error) {
	var rep application.IntelReport
	var raw []byte
	if err := row.Scan(&rep.ID, &rep.OpID, &rep.SponsorID, &rep.TargetID, &rep.Kind, &raw, &rep.Confidence, &rep.TakenAt, &rep.ExpiresAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &rep.Fields); err != nil {
		return nil, fmt.Errorf("postgres: decoding a report: %w", err)
	}
	rep.TakenAt, rep.ExpiresAt = rep.TakenAt.UTC(), rep.ExpiresAt.UTC()
	return &rep, nil
}

func (r *IntelRepository) reports(ctx context.Context, sql string, args ...any) ([]application.IntelReport, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing reports: %w", err)
	}
	defer rows.Close()
	var out []application.IntelReport
	for rows.Next() {
		rep, err := scanIntelReport(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a report: %w", err)
		}
		out = append(out, *rep)
	}
	return out, rows.Err()
}

// ReportsOfSponsor lists a bureau's reports, newest first.
func (r *IntelRepository) ReportsOfSponsor(ctx context.Context, sponsorID string, limit int) ([]application.IntelReport, error) {
	return r.reports(ctx, `SELECT `+intelReportColumns+` FROM intel_reports WHERE sponsor_id = $1::uuid ORDER BY taken_at DESC, id LIMIT $2`, sponsorID, limit)
}

// ReportByID reads a report, nil for none.
func (r *IntelRepository) ReportByID(ctx context.Context, id string) (*application.IntelReport, error) {
	if !validUUID(id) {
		return nil, nil
	}
	rep, err := scanIntelReport(r.q.QueryRow(ctx, `SELECT `+intelReportColumns+` FROM intel_reports WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a report: %w", err)
	}
	return rep, nil
}

// AddShare records that a report was shared; false when it was shared with that party already.
func (r *IntelRepository) AddShare(ctx context.Context, reportID, toKind, toID, by string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO intel_shares (report_id, to_kind, to_id, shared_by, at) VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5) ON CONFLICT DO NOTHING`,
		reportID, toKind, toID, by, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: sharing a report: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SharedTo lists the reports shared with a party, newest first.
func (r *IntelRepository) SharedTo(ctx context.Context, toKind, toID string, limit int) ([]application.IntelReport, error) {
	return r.reports(ctx, `SELECT `+intelReportColumns+` FROM intel_reports WHERE id IN (SELECT report_id FROM intel_shares WHERE to_kind = $1 AND to_id = $2::uuid) ORDER BY taken_at DESC, id LIMIT $3`, toKind, toID, limit)
}

// InsertExposure records a detector's roll; false when it was rolled already.
func (r *IntelRepository) InsertExposure(ctx context.Context, e application.IntelExposure) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO intel_exposures (op_id, detector_id, chance_bps, roll, detected, at) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
		e.OpID, e.DetectorID, e.ChanceBPS, e.Roll, e.Detected, e.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording an exposure: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// InsertExpulsion records an expulsion; false when the operation has one already.
func (r *IntelRepository) InsertExpulsion(ctx context.Context, e application.IntelExpulsion) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO intel_expulsions (id, op_id, agent_id, player_id, target_id, sponsor_id, attributed, until, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7, $8, $9) ON CONFLICT DO NOTHING`,
		e.ID, e.OpID, e.AgentID, e.PlayerID, e.TargetID, e.SponsorID, e.Attributed, e.Until.UTC(), e.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording an expulsion: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ExpelledUntil is the end of the latest expulsion of the player from the settlement, zero for none.
func (r *IntelRepository) ExpelledUntil(ctx context.Context, playerID, targetID string) (time.Time, error) {
	if !validUUID(playerID) || !validUUID(targetID) {
		return time.Time{}, nil
	}
	var t *time.Time
	if err := r.q.QueryRow(ctx, `SELECT max(until) FROM intel_expulsions WHERE player_id = $1::uuid AND target_id = $2::uuid`, playerID, targetID).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading expulsions: %w", err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return t.UTC(), nil
}

// ExpulsionsOf lists the expulsions from a settlement, newest first.
func (r *IntelRepository) ExpulsionsOf(ctx context.Context, targetID string, limit int) ([]application.IntelExpulsion, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, op_id::text, agent_id::text, player_id::text, target_id::text, sponsor_id::text, attributed, until, at
		FROM intel_expulsions WHERE target_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, targetID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing expulsions: %w", err)
	}
	defer rows.Close()
	var out []application.IntelExpulsion
	for rows.Next() {
		var e application.IntelExpulsion
		if err := rows.Scan(&e.ID, &e.OpID, &e.AgentID, &e.PlayerID, &e.TargetID, &e.SponsorID, &e.Attributed, &e.Until, &e.At); err != nil {
			return nil, fmt.Errorf("postgres: reading an expulsion: %w", err)
		}
		e.Until, e.At = e.Until.UTC(), e.At.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// ActsSince is how many acts of sabotage the sponsor did since the instant.
func (r *IntelRepository) ActsSince(ctx context.Context, sponsorID string, since time.Time) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM sabotage_acts WHERE sponsor_id = $1::uuid AND at >= $2`, sponsorID, since.UTC()).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting acts: %w", err)
	}
	return n, nil
}

// LastAct is the instant of the last act of the kind on the very target, zero for none.
func (r *IntelRepository) LastAct(ctx context.Context, targetID, kind, refID string) (time.Time, error) {
	var t *time.Time
	if err := r.q.QueryRow(ctx, `SELECT max(at) FROM sabotage_acts WHERE target_id = $1::uuid AND kind = $2 AND ref_id = $3`, targetID, kind, refID).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading acts: %w", err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return t.UTC(), nil
}

// InsertAct records an act of sabotage and what it does while it lasts.
func (r *IntelRepository) InsertAct(ctx context.Context, a application.SabotageAct, e *application.SabotageEffect) error {
	_, err := r.q.Exec(ctx, `INSERT INTO sabotage_acts (id, sponsor_id, target_id, agent_id, kind, ref_type, ref_id, amount, unit, cost, ledger_tx, repair, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11::uuid, $12, $13)`,
		a.ID, a.SponsorID, a.TargetID, a.AgentID, a.Kind, a.RefType, a.RefID, a.Amount, a.Unit, a.Cost, nullText(a.LedgerTx), a.Repair, a.At.UTC())
	if err != nil {
		return fmt.Errorf("postgres: recording an act: %w", err)
	}
	if e == nil {
		return nil
	}
	var until any
	if e.Until != nil {
		until = e.Until.UTC()
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO sabotage_effects (act_id, kind, building_id, day, until, bps) VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6)`,
		e.ActID, e.Kind, e.BuildingID, e.Day, until, e.BPS); err != nil {
		return fmt.Errorf("postgres: recording an effect: %w", err)
	}
	return nil
}

// ActsOf lists the acts done to or by a settlement, newest first.
func (r *IntelRepository) ActsOf(ctx context.Context, settlementID string, limit int) ([]application.SabotageAct, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, sponsor_id::text, target_id::text, agent_id::text, kind, ref_type, ref_id, amount, unit, cost, COALESCE(ledger_tx::text, ''), repair, repaired_at, at
		FROM sabotage_acts WHERE sponsor_id = $1::uuid OR target_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing acts: %w", err)
	}
	defer rows.Close()
	var out []application.SabotageAct
	for rows.Next() {
		var a application.SabotageAct
		if err := rows.Scan(&a.ID, &a.SponsorID, &a.TargetID, &a.AgentID, &a.Kind, &a.RefType, &a.RefID, &a.Amount, &a.Unit, &a.Cost, &a.LedgerTx, &a.Repair, &a.RepairedAt, &a.At); err != nil {
			return nil, fmt.Errorf("postgres: reading an act: %w", err)
		}
		a.At, a.RepairedAt = a.At.UTC(), utcPtr(a.RepairedAt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// BlindBPS is the share a post's chance is cut by at now.
func (r *IntelRepository) BlindBPS(ctx context.Context, buildingID string, now time.Time) (int, error) {
	if !validUUID(buildingID) {
		return 0, nil
	}
	var bps int
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(max(bps), 0) FROM sabotage_effects WHERE building_id = $1::uuid AND kind = 'blind_post' AND until > $2`, buildingID, now.UTC()).Scan(&bps); err != nil {
		return 0, fmt.Errorf("postgres: reading a blinding: %w", err)
	}
	return bps, nil
}

// SkippedDay reports a skipped service day of a building.
func (r *IntelRepository) SkippedDay(ctx context.Context, buildingID string, day int64) (bool, error) {
	if !validUUID(buildingID) {
		return false, nil
	}
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM sabotage_effects WHERE building_id = $1::uuid AND kind = 'skip_service_day' AND day = $2`, buildingID, day).Scan(&n); err != nil {
		return false, fmt.Errorf("postgres: reading a skipped day: %w", err)
	}
	return n > 0, nil
}

// Vetting is the probation days of the settlement's vetting policy, 0 for none.
func (r *IntelRepository) Vetting(ctx context.Context, settlementID string) (int, error) {
	if !validUUID(settlementID) {
		return 0, nil
	}
	var d int
	if err := r.q.QueryRow(ctx, `SELECT COALESCE((SELECT probation_days FROM intel_vetting WHERE settlement_id = $1::uuid), 0)`, settlementID).Scan(&d); err != nil {
		return 0, fmt.Errorf("postgres: reading the vetting policy: %w", err)
	}
	return d, nil
}

// SetVetting sets the settlement's vetting policy.
func (r *IntelRepository) SetVetting(ctx context.Context, settlementID string, days int, by string, at time.Time) error {
	_, err := r.q.Exec(ctx, `INSERT INTO intel_vetting (settlement_id, probation_days, set_by, at) VALUES ($1::uuid, $2, $3::uuid, $4)
		ON CONFLICT (settlement_id) DO UPDATE SET probation_days = EXCLUDED.probation_days, set_by = EXCLUDED.set_by, at = EXCLUDED.at`, settlementID, days, by, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: setting the vetting policy: %w", err)
	}
	return nil
}
