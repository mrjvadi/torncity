package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// SkillRepository persists trained abilities.
//
// skill_code is config-driven text rather than an enum, so adding a skill is a
// configuration change and not a migration. Nothing in this file validates the
// code against a list for that reason.
type SkillRepository struct {
	q querier
}

var _ application.SkillRepository = (*SkillRepository)(nil)

// NewSkillRepository returns a repository over the pool.
func NewSkillRepository(p *Pool) *SkillRepository { return &SkillRepository{q: p.shared()} }

// The id column is not selected: application.Skill is keyed by (player_id,
// code), which is also the unique constraint, so the surrogate id is of no use
// to a caller and returning it would invite someone to address a skill by it.

const selectSkills = `
SELECT player_id, skill_code, level, xp, updated_at
FROM player_skills
WHERE player_id = $1::uuid
ORDER BY skill_code`

// List returns every skill the player has, ordered by code.
//
// An empty result is not an error: a new player has trained nothing, which is
// a legitimate answer and not a missing row.
func (r *SkillRepository) List(ctx context.Context, playerID string) ([]application.Skill, error) {
	rows, err := r.q.Query(ctx, selectSkills, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing skills for player %s: %w", playerID, err)
	}
	defer rows.Close()

	var out []application.Skill
	for rows.Next() {
		var s application.Skill
		if err := rows.Scan(&s.PlayerID, &s.Code, &s.Level, &s.XP, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning skill row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading skill rows: %w", err)
	}

	return out, nil
}

const selectSkill = `
SELECT player_id, skill_code, level, xp, updated_at
FROM player_skills
WHERE player_id = $1::uuid AND skill_code = $2`

// Get returns one skill, or application.ErrSkillNotFound.
func (r *SkillRepository) Get(ctx context.Context, playerID, code string) (*application.Skill, error) {
	var s application.Skill

	err := r.q.QueryRow(ctx, selectSkill, playerID, code).Scan(
		&s.PlayerID, &s.Code, &s.Level, &s.XP, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrSkillNotFound
		}
		return nil, fmt.Errorf("postgres: loading skill %s for player %s: %w", code, playerID, err)
	}

	return &s, nil
}

// upsertSkill adds the skill or overwrites its progress.
//
// player_skills_player_skill_key is what makes this one statement instead of a
// read-then-branch: a player holds each skill exactly once, and two concurrent
// training commands that each checked first would both find nothing and split
// one skill into two half-progressed rows.
//
// EXCLUDED is correct here, unlike in insertStatsDefaults: the caller is
// stating what the skill now is, so the new level, experience and timestamp
// must win. Only the surrogate id is left alone, so a repeated upsert does not
// hand the same skill a new identity.
const upsertSkill = `
INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
ON CONFLICT (player_id, skill_code) DO UPDATE SET
    level      = EXCLUDED.level,
    xp         = EXCLUDED.xp,
    updated_at = EXCLUDED.updated_at`

// Upsert adds or updates one skill, keyed on (player_id, skill_code).
func (r *SkillRepository) Upsert(ctx context.Context, s application.Skill) error {
	if s.Code == "" {
		// skill_code is NOT NULL and part of the unique key. An empty code
		// would occupy that slot and collide with the next skill written
		// without one, merging two unrelated abilities into one row.
		return fmt.Errorf("postgres: upsert skill: skill code is required")
	}

	id, err := newUUID()
	if err != nil {
		return err
	}

	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	if _, err := r.q.Exec(ctx, upsertSkill,
		id,
		s.PlayerID,
		s.Code,
		s.Level,
		s.XP,
		updatedAt,
	); err != nil {
		return fmt.Errorf("postgres: upserting skill %s for player %s: %w", s.Code, s.PlayerID, err)
	}

	return nil
}
