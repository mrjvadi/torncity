package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Travel statuses, as travels_status_check accepts them.
const (
	TravelInTransit = "in_transit"
	TravelArrived   = "arrived"
	TravelCancelled = "cancelled"
)

// TravelRepository persists journeys.
//
// It holds a transactor rather than a plain querier because Complete has to
// change two tables at once; see Complete.
type TravelRepository struct {
	q transactor
}

var _ application.TravelRepository = (*TravelRepository)(nil)

// NewTravelRepository returns a repository over the pool.
func NewTravelRepository(p *Pool) *TravelRepository { return &TravelRepository{q: p.shared()} }

// vehicle_id is absent from the insert below: application.Travel has no field
// for it and migrations/0002_phase1.up.sql creates the column NULL-able with
// no foreign key until the vehicles table exists, so every phase 1 journey is
// on foot and the column stays NULL.

const selectActiveTravel = `
SELECT id, player_id, from_city_id, to_city_id, cost, game_action_id, status, departed_at, arrives_at,
       COALESCE(mode, ''), COALESCE(ledger_transaction_id::text, ''), COALESCE(content_version, 0)
FROM travels
WHERE player_id = $1::uuid AND status = 'in_transit'`

// Active returns the player's journey in progress, or
// application.ErrNoActiveTravel.
//
// At most one row can match: travels_one_active_per_player_idx is a unique
// index over player_id restricted to in_transit rows, which is also what lets
// this be a single-row query rather than a scan with a "pick the newest"
// tie-break that would quietly hide a double-start bug.
func (r *TravelRepository) Active(ctx context.Context, playerID string) (*application.Travel, error) {
	var t application.Travel

	err := r.q.QueryRow(ctx, selectActiveTravel, playerID).Scan(
		&t.ID, &t.PlayerID, &t.FromCityID, &t.ToCityID, &t.Cost,
		&t.GameActionID, &t.Status, &t.DepartedAt, &t.ArrivesAt,
		&t.Mode, &t.LedgerTransactionID, &t.ContentVersion,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrNoActiveTravel
		}
		return nil, fmt.Errorf("postgres: loading active travel for player %s: %w", playerID, err)
	}

	return &t, nil
}

const insertTravel = `
INSERT INTO travels (id, player_id, from_city_id, to_city_id, cost, game_action_id, status, departed_at, arrives_at,
                     mode, ledger_transaction_id, content_version, vehicle_id)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9,
        NULLIF($10, ''), NULLIF($11, '')::uuid, NULLIF($12, 0), NULLIF($13, '')::uuid)`

// Start inserts a journey.
//
// No "does this player already travel?" check is made first, and that is the
// point: two taps on the same button arrive as two concurrent requests, each
// would read "no active travel" under READ COMMITTED, and both would insert.
// travels_one_active_per_player_idx is the only thing that can serialise them,
// so the insert is simply attempted and the loser's violation is translated.
//
// The translation is deliberately narrow. It requires a *pgconn.PgError whose
// SQLSTATE is 23505 AND whose constraint name is that index, because this
// statement can violate other unique constraints — a retried command reusing
// the same travel id violates the primary key with the same 23505 — and
// reporting that as "you are already travelling" would send a player to a
// journey screen instead of telling an operator that a command is being
// replayed. Matching the message text is not an option either: the server
// translates it under a non-English lc_messages.
func (r *TravelRepository) Start(ctx context.Context, t application.Travel) error {
	id, err := ensureID(t.ID)
	if err != nil {
		return err
	}

	if t.GameActionID == "" {
		// game_action_id is NOT NULL with a foreign key to game_actions:
		// travel finishes because the scheduler fires, so a journey with no
		// action behind it would never arrive.
		return fmt.Errorf("postgres: start travel: game action id is required")
	}

	status := t.Status
	if status == "" {
		// A journey being started is in transit by definition, and only that
		// status is covered by the one-active-per-player index.
		status = TravelInTransit
	}

	departedAt := t.DepartedAt
	if departedAt.IsZero() {
		departedAt = time.Now().UTC()
	}

	if _, err := r.q.Exec(ctx, insertTravel,
		id,
		t.PlayerID,
		t.FromCityID,
		t.ToCityID,
		t.Cost,
		t.GameActionID,
		status,
		departedAt,
		t.ArrivesAt,
		t.Mode,
		t.LedgerTransactionID,
		t.ContentVersion,
		t.VehicleID,
	); err != nil {
		if violates(err, sqlstateUniqueViolation, travelsOneActivePerPlayerIdx) {
			// Returned unwrapped, like ErrPlayerNotFound: a player pressing a
			// button twice is ordinary, and attaching the driver error would
			// classify it as an internal fault and put a generic failure in
			// front of someone who is simply already on their way.
			return application.ErrAlreadyTravelling
		}
		return fmt.Errorf("postgres: starting travel for player %s: %w", t.PlayerID, err)
	}

	return nil
}

// arriveTravel marks the journey arrived and yields what the second statement
// needs. The status predicate keeps it replay-safe: a second Complete for the
// same travel matches nothing and cannot move a player who has since started
// another journey.
const arriveTravel = `
UPDATE travels
SET status = $2
WHERE id = $1::uuid AND status = 'in_transit'
RETURNING player_id, to_city_id`

// movePlayerToCity is the other half of arrival.
const movePlayerToCity = `
UPDATE players
SET city_id = $2::uuid, updated_at = $3
WHERE id = $1::uuid`

// Complete marks the journey arrived and moves the player's city.
//
// Both statements run in ONE transaction. Run separately, a process that dies
// between them strands the player: the travel row says arrived while
// players.city_id still names the city they left, so they are in neither place
// — Active returns ErrNoActiveTravel, so nothing will ever finish the move,
// and every screen keeps showing the origin city for a journey the player was
// told had ended. There is no reconciliation pass that could repair it either,
// because an arrived travel is indistinguishable from one that completed
// correctly. Committing the two writes together removes the state entirely.
func (r *TravelRepository) Complete(ctx context.Context, travelID string) error {
	return inTx(ctx, r.q, func(ctx context.Context, tx pgx.Tx) error {
		var playerID, toCityID string

		err := tx.QueryRow(ctx, arriveTravel, travelID, TravelArrived).Scan(&playerID, &toCityID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// No in-transit row with that id: either the id is unknown or
				// the journey was already completed or cancelled.
				return application.ErrNoActiveTravel
			}
			return fmt.Errorf("postgres: completing travel %s: %w", travelID, err)
		}

		if _, err := tx.Exec(ctx, movePlayerToCity, playerID, toCityID, time.Now().UTC()); err != nil {
			return fmt.Errorf("postgres: moving player %s to city %s: %w", playerID, toCityID, err)
		}

		return nil
	})
}

const cancelTravel = `
UPDATE travels
SET status = $2
WHERE id = $1::uuid AND status = 'in_transit'`

// Cancel abandons a journey in progress, or returns
// application.ErrNoActiveTravel.
//
// The player's city is deliberately left alone: a cancelled journey never
// happened, so they are still in the city they departed from, which
// players.city_id already says. Cancelling is a single statement for that
// reason and needs no transaction.
func (r *TravelRepository) Cancel(ctx context.Context, travelID string) error {
	tag, err := r.q.Exec(ctx, cancelTravel, travelID, TravelCancelled)
	if err != nil {
		return fmt.Errorf("postgres: cancelling travel %s: %w", travelID, err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNoActiveTravel
	}

	return nil
}

const countRecentDepartures = `
SELECT count(*)
FROM travels
WHERE from_city_id = $1::uuid AND to_city_id = $2::uuid AND mode = $3 AND departed_at >= $4`

// RecentDepartures counts journeys on one route by one mode since a moment,
// whatever their status; see application.TravelRepository. It is answered
// from travels_demand_idx.
func (r *TravelRepository) RecentDepartures(ctx context.Context, fromCityID, toCityID, mode string, since time.Time) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, countRecentDepartures, fromCityID, toCityID, mode, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting departures %s -> %s by %s: %w", fromCityID, toCityID, mode, err)
	}
	return n, nil
}
