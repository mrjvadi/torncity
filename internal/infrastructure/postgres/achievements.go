package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists achievements (migrations/0026): progress, the
// achievements earned and the consumer's inbox.

// AchievementRepository implements application.AchievementRepository.
type AchievementRepository struct {
	q querier
}

var _ application.AchievementRepository = (*AchievementRepository)(nil)

// achievementLockClass and achievementDayLock key the advisory locks (0026).
const (
	achievementLockClass = 26
	achievementDayLock   = 2600026
)

// Lock serialises one player's achievements.
func (r *AchievementRepository) Lock(ctx context.Context, playerID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, achievementLockClass, playerID); err != nil {
		return fmt.Errorf("postgres: locking achievements: %w", err)
	}
	return nil
}

// LockDay serialises achievement payouts.
func (r *AchievementRepository) LockDay(ctx context.Context) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, achievementDayLock); err != nil {
		return fmt.Errorf("postgres: locking achievement payouts: %w", err)
	}
	return nil
}

// MarkEvent records an event in the inbox once.
func (r *AchievementRepository) MarkEvent(ctx context.Context, eventID, playerID, subject string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO achievement_events (event_id, player_id, subject, processed_at)
	   VALUES ($1, $2::uuid, $3, $4) ON CONFLICT DO NOTHING`, eventID, playerID, subject, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: marking an achievement event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Progress returns the player's counts.
func (r *AchievementRepository) Progress(ctx context.Context, playerID string) (map[string]int64, error) {
	rows, err := r.q.Query(ctx, `SELECT code, count FROM achievement_progress WHERE player_id = $1::uuid`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading achievement progress: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var code string
		var n int64
		if err := rows.Scan(&code, &n); err != nil {
			return nil, fmt.Errorf("postgres: scanning achievement progress: %w", err)
		}
		out[code] = n
	}
	return out, rows.Err()
}

// SaveProgress writes one count.
func (r *AchievementRepository) SaveProgress(ctx context.Context, playerID, code string, count int64, at time.Time) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO achievement_progress (player_id, code, count, updated_at)
	   VALUES ($1::uuid, $2, $3, $4)
	   ON CONFLICT (player_id, code) DO UPDATE SET count = EXCLUDED.count, updated_at = EXCLUDED.updated_at`,
		playerID, code, count, at.UTC()); err != nil {
		return fmt.Errorf("postgres: saving achievement progress: %w", err)
	}
	return nil
}

// Earned returns what the player has earned.
func (r *AchievementRepository) Earned(ctx context.Context, playerID string) ([]application.PlayerAchievement, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `SELECT player_id::text, code, awarded_at, cash, withheld FROM player_achievements
	   WHERE player_id = $1::uuid ORDER BY awarded_at DESC, code`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading achievements: %w", err)
	}
	defer rows.Close()
	var out []application.PlayerAchievement
	for rows.Next() {
		var a application.PlayerAchievement
		if err := rows.Scan(&a.PlayerID, &a.Code, &a.AwardedAt, &a.Cash, &a.Withheld); err != nil {
			return nil, fmt.Errorf("postgres: scanning an achievement: %w", err)
		}
		a.AwardedAt = a.AwardedAt.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// Award records an achievement once.
func (r *AchievementRepository) Award(ctx context.Context, a application.PlayerAchievement) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO player_achievements (player_id, code, awarded_at, cash, withheld)
	   VALUES ($1::uuid, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, a.PlayerID, a.Code, a.AwardedAt.UTC(), a.Cash, a.Withheld)
	if err != nil {
		return false, fmt.Errorf("postgres: awarding an achievement: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PaidSince is achievement cash paid since a time.
func (r *AchievementRepository) PaidSince(ctx context.Context, playerID string, since time.Time) (int64, error) {
	var paid int64
	var err error
	if playerID == "" {
		err = r.q.QueryRow(ctx, `SELECT COALESCE(SUM(cash), 0)::bigint FROM player_achievements WHERE awarded_at >= $1`,
			since.UTC()).Scan(&paid)
	} else {
		err = r.q.QueryRow(ctx, `SELECT COALESCE(SUM(cash), 0)::bigint FROM player_achievements
		   WHERE player_id = $1::uuid AND awarded_at >= $2`, playerID, since.UTC()).Scan(&paid)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: reading achievement payouts: %w", err)
	}
	return paid, nil
}
