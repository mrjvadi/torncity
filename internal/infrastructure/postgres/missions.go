package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists missions (migrations/0023): the missions players took,
// their progress and rewards, and the inbox that moves them once per event.

const missionAssignmentsOneActiveIdx = "mission_assignments_one_active_idx"

// missionDayLock is the advisory lock key every mission payout takes, so the
// economy's daily cap is read and spent by one completion at a time. It is
// a fixed number of this file's, named, never input.
const missionDayLock int64 = 0x6d697373_696f6e73 // "missions"

const assignmentColumns = `id::text, no, player_id::text, mission_code, city_id::text, board, status, progress,
	accepted_at, expires_at, ended_at, reward_cash, reward_withheld, reward_xp, COALESCE(reward_grant_id::text, ''),
	content_version`

// MissionRepository implements application.MissionRepository.
type MissionRepository struct {
	q querier
}

var _ application.MissionRepository = (*MissionRepository)(nil)

func scanAssignment(row pgx.Row) (*application.MissionAssignment, error) {
	var (
		a       application.MissionAssignment
		expires *time.Time
	)
	if err := row.Scan(&a.ID, &a.No, &a.PlayerID, &a.Mission, &a.CityID, &a.Board, &a.Status, &a.Progress,
		&a.AcceptedAt, &expires, &a.EndedAt, &a.RewardCash, &a.RewardWithheld, &a.RewardXP, &a.RewardGrantID,
		&a.ContentVersion); err != nil {
		return nil, err
	}
	a.AcceptedAt, a.EndedAt = a.AcceptedAt.UTC(), utcPtr(a.EndedAt)
	if expires != nil {
		a.ExpiresAt = expires.UTC()
	}
	return &a, nil
}

func (r *MissionRepository) list(ctx context.Context, sql string, args ...any) ([]application.MissionAssignment, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing missions: %w", err)
	}
	defer rows.Close()
	var out []application.MissionAssignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a mission: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// Lock takes a transaction-scoped advisory lock on the player's missions.
func (r *MissionRepository) Lock(ctx context.Context, playerID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('missions:' || $1, 0))`, playerID); err != nil {
		return fmt.Errorf("postgres: locking missions: %w", err)
	}
	return nil
}

// LockDay takes the transaction-scoped advisory lock of mission payouts.
func (r *MissionRepository) LockDay(ctx context.Context) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, missionDayLock); err != nil {
		return fmt.Errorf("postgres: locking mission payouts: %w", err)
	}
	return nil
}

// Active lists the player's active missions.
func (r *MissionRepository) Active(ctx context.Context, playerID string) ([]application.MissionAssignment, error) {
	return r.list(ctx, `SELECT `+assignmentColumns+` FROM mission_assignments
	                     WHERE player_id = $1::uuid AND status = $2 ORDER BY accepted_at, no`,
		playerID, application.MissionActive)
}

// Recent lists the player's missions.
func (r *MissionRepository) Recent(ctx context.Context, playerID string, limit int) ([]application.MissionAssignment, error) {
	return r.list(ctx, `SELECT `+assignmentColumns+` FROM mission_assignments
	                     WHERE player_id = $1::uuid ORDER BY accepted_at DESC, no DESC LIMIT $2`, playerID, limit)
}

// ByNo reads one of the player's missions by number.
func (r *MissionRepository) ByNo(ctx context.Context, playerID string, no int64) (*application.MissionAssignment, error) {
	a, err := scanAssignment(r.q.QueryRow(ctx,
		`SELECT `+assignmentColumns+` FROM mission_assignments WHERE player_id = $1::uuid AND no = $2`, playerID, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrMissionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a mission: %w", err)
	}
	return a, nil
}

// Accept inserts an active mission.
func (r *MissionRepository) Accept(ctx context.Context, a application.MissionAssignment) (application.MissionAssignment, error) {
	id, err := ensureID(a.ID)
	if err != nil {
		return a, err
	}
	a.ID, a.Status = id, application.MissionActive
	var expires *time.Time
	if !a.ExpiresAt.IsZero() {
		e := a.ExpiresAt.UTC()
		expires = &e
	}
	if a.Progress == nil {
		a.Progress = []int64{}
	}
	err = r.q.QueryRow(ctx,
		`INSERT INTO mission_assignments (id, player_id, mission_code, city_id, board, status, progress, accepted_at,
		                                  expires_at, reward_cash, reward_withheld, reward_xp, content_version)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, 0, 0, 0, $10) RETURNING no`,
		a.ID, a.PlayerID, a.Mission, a.CityID, a.Board, a.Status, a.Progress, a.AcceptedAt.UTC(), expires,
		a.ContentVersion).Scan(&a.No)
	if violates(err, sqlstateUniqueViolation, missionAssignmentsOneActiveIdx) {
		return a, application.ErrMissionActive
	}
	if err != nil {
		return a, fmt.Errorf("postgres: taking a mission: %w", err)
	}
	return a, nil
}

// SaveProgress writes an active mission's progress.
func (r *MissionRepository) SaveProgress(ctx context.Context, id string, progress []int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE mission_assignments SET progress = $2 WHERE id = $1::uuid AND status = $3`,
		id, progress, application.MissionActive); err != nil {
		return fmt.Errorf("postgres: saving mission progress: %w", err)
	}
	return nil
}

// End closes an active mission.
func (r *MissionRepository) End(ctx context.Context, a application.MissionAssignment) error {
	ended := time.Now().UTC()
	if a.EndedAt != nil {
		ended = a.EndedAt.UTC()
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE mission_assignments SET status = $2, progress = $3, ended_at = $4, reward_cash = $5,
		        reward_withheld = $6, reward_xp = $7, reward_grant_id = $8::uuid
		  WHERE id = $1::uuid AND status = $9`,
		a.ID, a.Status, a.Progress, ended, a.RewardCash, a.RewardWithheld, a.RewardXP, nullableUUID(a.RewardGrantID),
		application.MissionActive)
	if err != nil {
		return fmt.Errorf("postgres: ending a mission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrMissionNotFound
	}
	return nil
}

// Completions reads when the player last completed each mission.
func (r *MissionRepository) Completions(ctx context.Context, playerID string) (map[string]time.Time, error) {
	rows, err := r.q.Query(ctx,
		`SELECT mission_code, max(ended_at) FROM mission_assignments
		  WHERE player_id = $1::uuid AND status = $2 GROUP BY mission_code`, playerID, application.MissionCompleted)
	if err != nil {
		if isInvalidUUIDText(err) {
			return map[string]time.Time{}, nil
		}
		return nil, fmt.Errorf("postgres: reading completions: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var (
			code string
			at   time.Time
		)
		if err := rows.Scan(&code, &at); err != nil {
			return nil, err
		}
		out[code] = at.UTC()
	}
	return out, rows.Err()
}

// PaidSince sums mission cash paid since, to one player or to all.
func (r *MissionRepository) PaidSince(ctx context.Context, playerID string, since time.Time) (int64, error) {
	var paid int64
	var err error
	if playerID == "" {
		err = r.q.QueryRow(ctx,
			`SELECT COALESCE(SUM(reward_cash), 0)::bigint FROM mission_assignments
			  WHERE status = $1 AND ended_at >= $2`, application.MissionCompleted, since.UTC()).Scan(&paid)
	} else {
		err = r.q.QueryRow(ctx,
			`SELECT COALESCE(SUM(reward_cash), 0)::bigint FROM mission_assignments
			  WHERE player_id = $1::uuid AND status = $2 AND ended_at >= $3`,
			playerID, application.MissionCompleted, since.UTC()).Scan(&paid)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: summing mission cash: %w", err)
	}
	return paid, nil
}

// MarkEvent records an event in the missions' inbox.
func (r *MissionRepository) MarkEvent(ctx context.Context, eventID, playerID, subject string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`INSERT INTO mission_progress_events (event_id, player_id, subject, processed_at)
		 VALUES ($1, $2::uuid, $3, $4) ON CONFLICT DO NOTHING`, eventID, playerID, subject, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a mission event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
