package postgres

import (
	"context"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
)

// BankRepository implements application.BankRepository. Reach it through
// Tx.Bank: its locks are only worth anything inside the unit of work that
// moves the money they guard.
type BankRepository struct {
	q querier
}

var _ application.BankRepository = (*BankRepository)(nil)

// lockPlayersSQL locks the player rows, in id order, and reads each one's
// city. An arrival updates players.city_id, so it waits behind this lock.
const lockPlayersSQL = `
SELECT id::text, COALESCE(city_id::text, '')
  FROM players
 WHERE id = ANY($1::uuid[])
 ORDER BY id
   FOR UPDATE`

// lockConditionSQL locks the players' condition rows, in id order. A departure
// spends energy on this row before it inserts the journey, so it waits behind
// this lock — or, if it got there first, this waits for it and the journey
// read below then sees the departure.
const lockConditionSQL = `
SELECT player_id FROM player_stats
 WHERE player_id = ANY($1::uuid[])
 ORDER BY player_id
   FOR UPDATE`

// travellingSQL names which of the players have a journey in progress. It
// runs after both locks, as its own statement, so under READ COMMITTED it sees
// every departure that committed while this transaction waited.
const travellingSQL = `
SELECT player_id::text FROM travels
 WHERE player_id = ANY($1::uuid[]) AND status = 'in_transit'`

// LockPresence locks the players against moving and reports where each one
// is. See application.BankRepository.
//
// Player rows are locked before condition rows, and each set in id order, so
// two payments between the same two players — in either direction — take the
// locks in the same order and cannot deadlock.
func (r *BankRepository) LockPresence(ctx context.Context, playerIDs ...string) ([]application.Presence, error) {
	if len(playerIDs) == 0 {
		return nil, nil
	}
	for _, id := range playerIDs {
		if !validUUID(id) {
			return nil, application.ErrPlayerNotFound
		}
	}
	ids := append([]string(nil), playerIDs...)
	sort.Strings(ids)

	cities := make(map[string]string, len(ids))
	rows, err := r.q.Query(ctx, lockPlayersSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: locking players: %w", err)
	}
	for rows.Next() {
		var id, city string
		if err := rows.Scan(&id, &city); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: locking players: %w", err)
		}
		cities[id] = city
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: locking players: %w", err)
	}

	if _, err := r.q.Exec(ctx, lockConditionSQL, ids); err != nil {
		return nil, fmt.Errorf("postgres: locking player condition: %w", err)
	}

	travelling := map[string]bool{}
	rows, err = r.q.Query(ctx, travellingSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading journeys: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: reading journeys: %w", err)
		}
		travelling[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading journeys: %w", err)
	}

	out := make([]application.Presence, 0, len(playerIDs))
	for _, id := range playerIDs {
		city, ok := cities[id]
		if !ok {
			return nil, application.ErrPlayerNotFound
		}
		out = append(out, application.Presence{PlayerID: id, CityID: city, Travelling: travelling[id]})
	}
	return out, nil
}
