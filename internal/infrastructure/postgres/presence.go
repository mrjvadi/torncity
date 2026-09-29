package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/presence"
)

// PresenceRepository is the database half of presence (ADR 0030 section 3,
// migration 0050): a player's "last seen" setting, and the read-only facts
// the visibility rules are computed from. The heartbeat itself is Redis.
type PresenceRepository struct{ q querier }

var _ application.PresenceRepository = (*PresenceRepository)(nil)

// NewPresenceRepository returns a repository over the pool, for reads made
// outside a unit of work (the client API, the gateway).
func NewPresenceRepository(p *Pool) *PresenceRepository { return &PresenceRepository{q: p.shared()} }

// Visibility is the player's setting.
func (r *PresenceRepository) Visibility(ctx context.Context, playerID string) (presence.Visibility, error) {
	var v string
	err := r.q.QueryRow(ctx, `SELECT presence_visibility FROM players WHERE id = $1::uuid`, playerID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", application.ErrPlayerNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading a presence setting: %w", err)
	}
	return presence.Visibility(v), nil
}

// SetVisibility stores the setting.
func (r *PresenceRepository) SetVisibility(ctx context.Context, playerID string, v presence.Visibility) error {
	if !v.Valid() {
		return application.ErrUnsupportedPresenceVisibility
	}
	tag, err := r.q.Exec(ctx, `UPDATE players SET presence_visibility = $2 WHERE id = $1::uuid`, playerID, string(v))
	if err != nil {
		return fmt.Errorf("postgres: storing a presence setting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrPlayerNotFound
	}
	return nil
}

// Facts reads what presence needs about each player: the setting, where they
// are and live, and their open, visible game actions. One query for the
// players, one for their actions (game_actions_actor_idx).
func (r *PresenceRepository) Facts(ctx context.Context, playerIDs []string) (map[string]application.PresenceFacts, error) {
	out := map[string]application.PresenceFacts{}
	if len(playerIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT id::text, display_name, public_code, presence_visibility,
		        COALESCE(city_id::text, ''), COALESCE(residence_city_id::text, ''), COALESCE(place_code, '')
		   FROM players WHERE id = ANY($1::uuid[])`, playerIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading presence facts: %w", err)
	}
	for rows.Next() {
		var f application.PresenceFacts
		var vis string
		if err := rows.Scan(&f.PlayerID, &f.DisplayName, &f.PublicCode, &vis, &f.CityID, &f.ResidenceCityID, &f.PlaceCode); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning presence facts: %w", err)
		}
		f.Visibility = presence.Visibility(vis)
		out[f.PlayerID] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	acts, err := r.q.Query(ctx,
		`SELECT actor_id::text, action_type FROM game_actions
		  WHERE actor_type = 'player' AND actor_id = ANY($1::uuid[])
		    AND status IN ('scheduled', 'running') AND action_type = ANY($2::text[])`,
		playerIDs, presence.ActionTypes())
	if err != nil {
		return nil, fmt.Errorf("postgres: reading presence activities: %w", err)
	}
	defer acts.Close()
	for acts.Next() {
		var id, typ string
		if err := acts.Scan(&id, &typ); err != nil {
			return nil, fmt.Errorf("postgres: scanning presence activities: %w", err)
		}
		if f, ok := out[id]; ok {
			f.OpenActions = append(f.OpenActions, typ)
			out[id] = f
		}
	}
	return out, acts.Err()
}

// ContactsAmong says which of playerIDs are the viewer's accepted friends
// (either direction) or share the viewer's faction.
func (r *PresenceRepository) ContactsAmong(ctx context.Context, viewerID string, playerIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(playerIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx,
		`SELECT p.id::text FROM unnest($2::uuid[]) AS p(id)
		  WHERE EXISTS (SELECT 1 FROM friendships f WHERE f.status = 'accepted'
		                  AND ((f.player_id = $1::uuid AND f.friend_player_id = p.id)
		                    OR (f.friend_player_id = $1::uuid AND f.player_id = p.id)))
		     OR EXISTS (SELECT 1 FROM faction_members a JOIN faction_members b ON a.faction_id = b.faction_id
		                 WHERE a.player_id = $1::uuid AND b.player_id = p.id)`,
		viewerID, playerIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading contacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scanning contacts: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CountryOf maps each city to the country jurisdiction above it.
func (r *PresenceRepository) CountryOf(ctx context.Context, cityIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(cityIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx,
		`WITH RECURSIVE up(city_id, jid, kind, parent_id, depth) AS (
		     SELECT c.id, j.id, j.kind, j.parent_id, 0
		       FROM cities c JOIN jurisdictions j ON j.id = c.jurisdiction_id
		      WHERE c.id = ANY($1::uuid[])
		     UNION ALL
		     SELECT up.city_id, j.id, j.kind, j.parent_id, up.depth + 1
		       FROM up JOIN jurisdictions j ON j.id = up.parent_id WHERE up.depth < 8
		 )
		 SELECT city_id::text, jid::text FROM up WHERE kind = 'country'`, cityIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading countries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var city, country string
		if err := rows.Scan(&city, &country); err != nil {
			return nil, fmt.Errorf("postgres: scanning countries: %w", err)
		}
		out[city] = country
	}
	return out, rows.Err()
}

// Roster lists the players who live in or stand in a settlement.
func (r *PresenceRepository) Roster(ctx context.Context, settlementID string, limit int) ([]string, error) {
	rows, err := r.q.Query(ctx,
		`SELECT id::text FROM players
		  WHERE city_id = $1::uuid OR residence_city_id = $1::uuid
		  ORDER BY display_name, id LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a settlement roster: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scanning a settlement roster: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Member says whether the player lives in or stands in the settlement.
func (r *PresenceRepository) Member(ctx context.Context, playerID, settlementID string) (bool, error) {
	var ok bool
	err := r.q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM players WHERE id = $1::uuid AND (city_id = $2::uuid OR residence_city_id = $2::uuid))`,
		playerID, settlementID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("postgres: checking settlement membership: %w", err)
	}
	return ok, nil
}
