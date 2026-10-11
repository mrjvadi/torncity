package postgres

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// SurvivalRepository is thirst, warmth, garments and sickness (migration 0147), bound to one transaction.
type SurvivalRepository struct{ q querier }

var _ application.SurvivalRepository = (*SurvivalRepository)(nil)

func (r *SurvivalRepository) State(ctx context.Context, playerID string) (*application.SurvivalState, error) {
	var s application.SurvivalState
	var shelter, notified *time.Time
	err := r.q.QueryRow(ctx, `SELECT player_id::text, thirst_milli, thirst_at, warmth_milli, warmth_at, shelter_until, shelter_bonus,
		cold_armed, thirst_armed, notified_at FROM player_survival WHERE player_id = $1::uuid`, playerID).Scan(
		&s.PlayerID, &s.ThirstMilli, &s.ThirstAt, &s.WarmthMilli, &s.WarmthAt, &shelter, &s.ShelterBonus, &s.ColdArmed, &s.ThirstArmed, &notified)
	if stderrors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading survival: %w", err)
	}
	if shelter != nil {
		s.ShelterUntil = *shelter
	}
	if notified != nil {
		s.NotifiedAt = *notified
	}
	return &s, nil
}

func nilTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func (r *SurvivalRepository) SaveState(ctx context.Context, s application.SurvivalState) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO player_survival (player_id, thirst_milli, thirst_at, warmth_milli, warmth_at, shelter_until,
		shelter_bonus, cold_armed, thirst_armed, notified_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (player_id) DO UPDATE SET thirst_milli = EXCLUDED.thirst_milli, thirst_at = EXCLUDED.thirst_at,
		warmth_milli = EXCLUDED.warmth_milli, warmth_at = EXCLUDED.warmth_at, shelter_until = EXCLUDED.shelter_until,
		shelter_bonus = EXCLUDED.shelter_bonus, cold_armed = EXCLUDED.cold_armed, thirst_armed = EXCLUDED.thirst_armed,
		notified_at = EXCLUDED.notified_at`,
		s.PlayerID, s.ThirstMilli, s.ThirstAt.UTC(), s.WarmthMilli, s.WarmthAt.UTC(), nilTime(s.ShelterUntil), s.ShelterBonus,
		s.ColdArmed, s.ThirstArmed, nilTime(s.NotifiedAt)); err != nil {
		return fmt.Errorf("postgres: saving survival: %w", err)
	}
	return nil
}

func (r *SurvivalRepository) Garments(ctx context.Context, playerID string) ([]application.Garment, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, player_id::text, item, slot, uses_left, worn, status, counted_at, created_at
		FROM player_garments WHERE player_id = $1::uuid AND status = 'kept' ORDER BY created_at, id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing garments: %w", err)
	}
	defer rows.Close()
	var out []application.Garment
	for rows.Next() {
		var g application.Garment
		if err := rows.Scan(&g.ID, &g.PlayerID, &g.Item, &g.Slot, &g.UsesLeft, &g.Worn, &g.Status, &g.CountedAt, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *SurvivalRepository) AddGarment(ctx context.Context, g application.Garment) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO player_garments (id, player_id, item, slot, uses_left, worn, status, counted_at, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, 'kept', $7, $8)`,
		g.ID, g.PlayerID, g.Item, g.Slot, g.UsesLeft, g.Worn, g.CountedAt.UTC(), g.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: adding a garment: %w", err)
	}
	return nil
}

func (r *SurvivalRepository) SetWorn(ctx context.Context, id string, worn bool, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE player_garments SET worn = $2, counted_at = $3 WHERE id = $1::uuid`, id, worn, at.UTC()); err != nil {
		return fmt.Errorf("postgres: dressing: %w", err)
	}
	return nil
}

func (r *SurvivalRepository) SetUses(ctx context.Context, id string, uses int, counted time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE player_garments SET uses_left = $2, counted_at = $3,
		status = CASE WHEN $2 <= 0 THEN 'worn_out' ELSE status END, worn = CASE WHEN $2 <= 0 THEN false ELSE worn END WHERE id = $1::uuid`,
		id, max(uses, 0), counted.UTC()); err != nil {
		return fmt.Errorf("postgres: wearing a garment: %w", err)
	}
	return nil
}

func (r *SurvivalRepository) Illnesses(ctx context.Context, playerID string) ([]application.Illness, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, player_id::text, kind, onset_at, ends_at, status, COALESCE(settlement_id::text, ''),
		COALESCE(building_id::text, ''), COALESCE(medicine_item, ''), medicine_units, treated_at
		FROM player_illness WHERE player_id = $1::uuid AND status = 'active' ORDER BY onset_at, id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing illness: %w", err)
	}
	defer rows.Close()
	var out []application.Illness
	for rows.Next() {
		var i application.Illness
		var treated *time.Time
		if err := rows.Scan(&i.ID, &i.PlayerID, &i.Kind, &i.OnsetAt, &i.EndsAt, &i.Status, &i.SettlementID, &i.BuildingID,
			&i.MedicineItem, &i.MedicineUnits, &treated); err != nil {
			return nil, err
		}
		if treated != nil {
			i.TreatedAt = *treated
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *SurvivalRepository) Fall(ctx context.Context, i application.Illness) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO player_illness (id, player_id, kind, onset_at, ends_at) VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT DO NOTHING`, i.ID, i.PlayerID, i.Kind, i.OnsetAt.UTC(), i.EndsAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: falling ill: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *SurvivalRepository) End(ctx context.Context, id string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE player_illness SET status = 'ended', ends_at = LEAST(ends_at, $2) WHERE id = $1::uuid AND status = 'active'`,
		id, at.UTC()); err != nil {
		return fmt.Errorf("postgres: ending an illness: %w", err)
	}
	return nil
}

func (r *SurvivalRepository) Treat(ctx context.Context, id string, endsAt time.Time, item string, units int, settlementID, buildingID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE player_illness SET ends_at = $2, medicine_item = $3, medicine_units = $4, settlement_id = $5::uuid,
		building_id = $6::uuid, treated_at = $7 WHERE id = $1::uuid AND status = 'active' AND medicine_units = 0`,
		id, endsAt.UTC(), item, units, settlementID, buildingID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: treating an illness: %w", err)
	}
	return nil
}
