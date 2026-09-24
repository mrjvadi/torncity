package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// placeMovesOneMovingIdx is the partial unique index of
// migrations/0015_places.up.sql that keeps one walk in progress per player.
const placeMovesOneMovingIdx = "place_moves_one_moving_idx"

const placeMoveColumns = `id::text, player_id::text, city_id::text, from_place, to_place, status, energy,
       game_action_id::text, started_at, arrives_at, arrived_at`

// PlaceRepository implements application.PlaceRepository.
type PlaceRepository struct {
	q querier
}

var _ application.PlaceRepository = (*PlaceRepository)(nil)

func scanPlaceMove(row pgx.Row) (*application.PlaceMove, error) {
	var m application.PlaceMove
	if err := row.Scan(&m.ID, &m.PlayerID, &m.CityID, &m.From, &m.To, &m.Status, &m.Energy,
		&m.GameActionID, &m.StartedAt, &m.ArrivesAt, &m.ArrivedAt); err != nil {
		return nil, err
	}
	m.StartedAt, m.ArrivesAt, m.ArrivedAt = m.StartedAt.UTC(), m.ArrivesAt.UTC(), utcPtr(m.ArrivedAt)
	return &m, nil
}

// Where reads players.place_code; NULL is the default place.
func (r *PlaceRepository) Where(ctx context.Context, playerID string) (string, error) {
	var code *string
	err := r.q.QueryRow(ctx, `SELECT place_code FROM players WHERE id = $1::uuid`, playerID).Scan(&code)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return "", application.ErrPlayerNotFound
	case err != nil:
		return "", fmt.Errorf("postgres: reading a player's place: %w", err)
	}
	if code == nil {
		return "", nil
	}
	return *code, nil
}

// Put records where the player stands.
func (r *PlaceRepository) Put(ctx context.Context, playerID, code string, at time.Time) error {
	var place *string
	if code != "" {
		place = &code
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE players SET place_code = $2, place_since = $3 WHERE id = $1::uuid`, playerID, place, at.UTC())
	if isInvalidUUIDText(err) {
		return application.ErrPlayerNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: putting a player at a place: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrPlayerNotFound
	}
	return nil
}

// ActiveMove returns the walk in progress.
func (r *PlaceRepository) ActiveMove(ctx context.Context, playerID string) (*application.PlaceMove, error) {
	m, err := scanPlaceMove(r.q.QueryRow(ctx,
		`SELECT `+placeMoveColumns+` FROM place_moves WHERE player_id = $1::uuid AND status = 'moving'`, playerID))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNotMoving
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a walk: %w", err)
	}
	return m, nil
}

// StartMove records a walk; the partial unique index refuses a second one.
func (r *PlaceRepository) StartMove(ctx context.Context, m application.PlaceMove) error {
	id, err := ensureID(m.ID)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO place_moves (id, player_id, city_id, from_place, to_place, status, energy, game_action_id,
		                          started_at, arrives_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'moving', $6, $7::uuid, $8, $9)`,
		id, m.PlayerID, m.CityID, m.From, m.To, m.Energy, m.GameActionID, m.StartedAt.UTC(), m.ArrivesAt.UTC())
	if violates(err, sqlstateUniqueViolation, placeMovesOneMovingIdx) {
		return application.ErrAlreadyMoving
	}
	if err != nil {
		return fmt.Errorf("postgres: starting a walk: %w", err)
	}
	return nil
}

// FinishMove ends a walk in progress and puts the player at its destination
// in one statement, so a walk cannot end without the player arriving.
func (r *PlaceRepository) FinishMove(ctx context.Context, id string, at time.Time) (*application.PlaceMove, error) {
	m, err := scanPlaceMove(r.q.QueryRow(ctx,
		`WITH done AS (
		     UPDATE place_moves SET status = 'arrived', arrived_at = $2
		      WHERE id = $1::uuid AND status = 'moving'
		  RETURNING `+placeMoveColumns+`
		 ), moved AS (
		     UPDATE players p SET place_code = d.to_place, place_since = $2
		       FROM done d WHERE p.id = d.player_id::uuid
		 )
		 SELECT * FROM done`, id, at.UTC()))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNotMoving
	case err != nil:
		return nil, fmt.Errorf("postgres: finishing a walk: %w", err)
	}
	return m, nil
}

// CancelMove ends a walk without arriving.
func (r *PlaceRepository) CancelMove(ctx context.Context, id string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE place_moves SET status = 'cancelled' WHERE id = $1::uuid AND status = 'moving'`, id)
	if isInvalidUUIDText(err) {
		return application.ErrNotMoving
	}
	if err != nil {
		return fmt.Errorf("postgres: cancelling a walk: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotMoving
	}
	return nil
}

// Headcount counts who stands where in a city: present in the city, not on
// a journey, not walking, not serving a sentence.
func (r *PlaceRepository) Headcount(ctx context.Context, cityID string) (map[string]int, error) {
	rows, err := r.q.Query(ctx,
		`SELECT COALESCE(p.place_code, ''), count(*)
		   FROM players p
		  WHERE p.city_id = $1::uuid AND p.status = 'active'
		    AND NOT EXISTS (SELECT 1 FROM travels t WHERE t.player_id = p.id AND t.status = 'in_transit')
		    AND NOT EXISTS (SELECT 1 FROM place_moves m WHERE m.player_id = p.id AND m.status = 'moving')
		    AND NOT EXISTS (SELECT 1 FROM jail_sentences j WHERE j.player_id = p.id AND j.status = 'serving')
		  GROUP BY 1`, cityID)
	if isInvalidUUIDText(err) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: counting who stands where: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			code string
			n    int
		)
		if err := rows.Scan(&code, &n); err != nil {
			return nil, fmt.Errorf("postgres: counting who stands where: %w", err)
		}
		out[code] = n
	}
	return out, rows.Err()
}
