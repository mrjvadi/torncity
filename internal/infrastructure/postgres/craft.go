package postgres

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// CraftRepository is the craft jobs (migration 0145), bound to one transaction.
type CraftRepository struct{ q querier }

var _ application.CraftRepository = (*CraftRepository)(nil)

const craftCols = `id::text, settlement_id::text, player_id::text, building_id::text, recipe, batches, consumed, planned, made, output_bps,
	tool_item, status, COALESCE(game_action_id::text, ''), started_at, finish_at, finished_at`

func scanCraft(r interface{ Scan(dest ...any) error }) (application.CraftJob, error) {
	var j application.CraftJob
	var consumed, planned, made []byte
	if err := r.Scan(&j.ID, &j.SettlementID, &j.PlayerID, &j.BuildingID, &j.Recipe, &j.Batches, &consumed, &planned, &made, &j.OutputBPS,
		&j.ToolItem, &j.Status, &j.GameActionID, &j.StartedAt, &j.FinishAt, &j.FinishedAt); err != nil {
		return j, err
	}
	for _, x := range []struct {
		raw []byte
		to  *map[string]int64
	}{{consumed, &j.Consumed}, {planned, &j.Planned}, {made, &j.Made}} {
		*x.to = map[string]int64{}
		if len(x.raw) > 0 {
			if err := json.Unmarshal(x.raw, x.to); err != nil {
				return j, fmt.Errorf("postgres: the goods of a craft job: %w", err)
			}
		}
	}
	return j, nil
}

func (r *CraftRepository) Start(ctx context.Context, j application.CraftJob) error {
	consumed, _ := json.Marshal(j.Consumed)
	planned, _ := json.Marshal(j.Planned)
	if _, err := r.q.Exec(ctx, `INSERT INTO craft_jobs (id, settlement_id, player_id, building_id, recipe, batches, consumed, planned, output_bps, tool_item,
		game_action_id, started_at, finish_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::jsonb, $8::jsonb, $9, $10, NULLIF($11, '')::uuid, $12, $13)`,
		j.ID, j.SettlementID, j.PlayerID, j.BuildingID, j.Recipe, j.Batches, string(consumed), string(planned), j.OutputBPS, j.ToolItem,
		j.GameActionID, j.StartedAt.UTC(), j.FinishAt.UTC()); err != nil {
		return fmt.Errorf("postgres: starting a craft job: %w", err)
	}
	return nil
}

func (r *CraftRepository) Job(ctx context.Context, id string) (*application.CraftJob, error) {
	j, err := scanCraft(r.q.QueryRow(ctx, `SELECT `+craftCols+` FROM craft_jobs WHERE id = $1::uuid`, id))
	if stderrors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a craft job: %w", err)
	}
	return &j, nil
}

func (r *CraftRepository) Finish(ctx context.Context, id string, made map[string]int64, at time.Time) (bool, error) {
	raw, _ := json.Marshal(made)
	tag, err := r.q.Exec(ctx, `UPDATE craft_jobs SET status = 'done', made = $2::jsonb, finished_at = $3 WHERE id = $1::uuid AND status = 'working'`, id, string(raw), at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: finishing a craft job: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *CraftRepository) Running(ctx context.Context, playerID string) ([]application.CraftJob, error) {
	rows, err := r.q.Query(ctx, `SELECT `+craftCols+` FROM craft_jobs WHERE player_id = $1::uuid AND status = 'working' ORDER BY started_at, id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing craft jobs: %w", err)
	}
	defer rows.Close()
	var out []application.CraftJob
	for rows.Next() {
		j, err := scanCraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *CraftRepository) Lock(ctx context.Context, playerID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('craft:' || $1::text, 0))`, playerID); err != nil {
		return fmt.Errorf("postgres: locking the player's crafts: %w", err)
	}
	return nil
}
