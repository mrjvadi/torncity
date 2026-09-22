package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Friendship statuses, as friendships_status_check accepts them.
const (
	FriendshipPending  = "pending"
	FriendshipAccepted = "accepted"
	FriendshipBlocked  = "blocked"
)

// FriendshipRepository persists the social graph.
//
// Every edge is directed, and the schema says so: the unique key is the
// ORDERED pair (player_id, friend_player_id), so (a -> b) and (b -> a) are two
// independent rows. A mutual friendship is therefore two accepted rows, and a
// block by a says nothing about b's edge to a. Only Accept writes both
// directions, because only "we are friends" is a claim about both people.
//
// It holds a transactor rather than a plain querier because Accept changes two
// rows and must do so atomically; see Accept.
type FriendshipRepository struct {
	q transactor
}

var _ application.FriendshipRepository = (*FriendshipRepository)(nil)

// NewFriendshipRepository returns a repository over the pool.
func NewFriendshipRepository(p *Pool) *FriendshipRepository {
	return &FriendshipRepository{q: p.Raw()}
}

const selectFriendships = `
SELECT id, player_id, friend_player_id, status, created_at
FROM friendships
WHERE player_id = $1::uuid
ORDER BY created_at, id`

// List returns the player's own outgoing edges, in every status.
//
// Blocked and pending rows are included rather than filtered: the caller is
// the only party that knows which screen it is drawing — an outgoing-requests
// list, a friends list or a block list — and a repository that decided for it
// would need a separate method per screen. Incoming requests are a different
// query entirely (friend_player_id = me), which friendships_friend_player_id_idx
// exists to answer and which this port does not expose.
//
// The tie-break on id makes the order total: created_at is supplied by the
// application and two rows written in one transaction can share it exactly, so
// without it paging over this list could repeat or skip a row.
func (r *FriendshipRepository) List(ctx context.Context, playerID string) ([]application.Friendship, error) {
	rows, err := r.q.Query(ctx, selectFriendships, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing friendships for player %s: %w", playerID, err)
	}
	defer rows.Close()

	var out []application.Friendship
	for rows.Next() {
		var f application.Friendship
		if err := rows.Scan(&f.ID, &f.PlayerID, &f.FriendPlayerID, &f.Status, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning friendship row: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading friendship rows: %w", err)
	}

	return out, nil
}

// insertFriendRequest creates the pending edge, or reports the one already
// there.
//
// The conflict action preserves the existing status — it assigns the row's own
// value back to itself — so a repeated request can never downgrade an accepted
// friendship back to pending, which is what a plain EXCLUDED upsert would do
// and is a genuine way to lose a friendship by tapping a stale button.
//
// DO UPDATE rather than DO NOTHING, for the reason set out on insertPlayer:
// DO NOTHING does not block on the conflicting row, so a caller that lost a
// race would read back nothing and could not tell "already friends" from "the
// insert did not happen". DO UPDATE takes the row lock, waits, and RETURNING
// then yields the surviving row.
//
// Whether the row is new is decided by comparing the returned id with the one
// this statement proposed, rather than by inspecting system columns: if the id
// that came back is the generated one, this call inserted it.
const insertFriendRequest = `
INSERT INTO friendships (id, player_id, friend_player_id, status, created_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5)
ON CONFLICT (player_id, friend_player_id) DO UPDATE SET status = friendships.status
RETURNING id, status`

// Request creates one pending edge from playerID to friendPlayerID.
//
// Only one row is written. The other player's edge is theirs to create by
// accepting, which is the whole reason the request state exists.
//
// Re-sending a request that is still pending succeeds silently: pressing the
// button twice is ordinary, and the edge is already exactly what the caller
// asked for.
func (r *FriendshipRepository) Request(ctx context.Context, playerID, friendPlayerID string) error {
	id, err := newUUID()
	if err != nil {
		return err
	}

	var (
		survivingID string
		status      string
	)

	err = r.q.QueryRow(ctx, insertFriendRequest,
		id,
		playerID,
		friendPlayerID,
		FriendshipPending,
		time.Now().UTC(),
	).Scan(&survivingID, &status)
	if err != nil {
		if violates(err, sqlstateCheckViolation, friendshipsNoSelfCheck) {
			return ErrSelfFriendship
		}
		return fmt.Errorf("postgres: requesting friendship from player %s: %w", playerID, err)
	}

	if survivingID == id {
		return nil
	}

	// An edge was already there. What it means depends on its status, and the
	// caller needs to tell those apart to draw anything sensible.
	switch status {
	case FriendshipAccepted:
		return application.ErrAlreadyFriends
	case FriendshipBlocked:
		return ErrFriendshipBlocked
	default:
		return nil
	}
}

// acceptIncoming turns the other player's pending request into an accepted
// edge. The status predicate is what makes this mean "accept a request": it
// matches nothing when there is no request, and it cannot reanimate an edge
// the requester has since blocked.
const acceptIncoming = `
UPDATE friendships
SET status = $3
WHERE player_id = $1::uuid AND friend_player_id = $2::uuid AND status = 'pending'
RETURNING id`

// insertReverseEdge writes the accepting player's own side.
//
// EXCLUDED is correct here and only here among the friendship statements: the
// caller has just agreed to the friendship, so an existing pending row of
// theirs — the two of them sent requests to each other — must become accepted
// rather than stay pending forever.
const insertReverseEdge = `
INSERT INTO friendships (id, player_id, friend_player_id, status, created_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5)
ON CONFLICT (player_id, friend_player_id) DO UPDATE SET status = EXCLUDED.status`

// Accept records that playerID accepts friendPlayerID's request.
//
// The incoming row is (friendPlayerID -> playerID): the other player asked
// first, so their edge is the one that exists. Accepting sets it to accepted
// and creates the reverse edge (playerID -> friendPlayerID), also accepted.
//
// Both writes are in ONE transaction. Split apart, a crash between them leaves
// a friendship that only one of the two people has: the requester's list shows
// an accepted friend while the accepter's shows nothing, the accepter's screen
// still offers the "accept" button for a request that is no longer pending, and
// nothing in the schema marks the pair as half-finished for a repair pass to
// find.
//
// It returns application.ErrNotFriends when there is no pending request to
// accept, which is the honest answer: without a request there is nothing to
// become friends over.
func (r *FriendshipRepository) Accept(ctx context.Context, playerID, friendPlayerID string) error {
	return inTx(ctx, r.q, func(ctx context.Context, tx pgx.Tx) error {
		var incomingID string

		err := tx.QueryRow(ctx, acceptIncoming, friendPlayerID, playerID, FriendshipAccepted).Scan(&incomingID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return application.ErrNotFriends
			}
			return fmt.Errorf("postgres: accepting friendship for player %s: %w", playerID, err)
		}

		id, err := newUUID()
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, insertReverseEdge,
			id,
			playerID,
			friendPlayerID,
			FriendshipAccepted,
			time.Now().UTC(),
		); err != nil {
			if violates(err, sqlstateCheckViolation, friendshipsNoSelfCheck) {
				return ErrSelfFriendship
			}
			return fmt.Errorf("postgres: writing reverse friendship edge for player %s: %w", playerID, err)
		}

		return nil
	})
}

// blockEdge sets the caller's own edge to blocked, creating it if the two have
// no history. EXCLUDED is correct: blocking overrides whatever the edge was.
const blockEdge = `
INSERT INTO friendships (id, player_id, friend_player_id, status, created_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5)
ON CONFLICT (player_id, friend_player_id) DO UPDATE SET status = EXCLUDED.status`

// Block marks friendPlayerID blocked from playerID's side.
//
// One direction only, and one statement, on purpose: blocking is a statement
// about what the blocker will accept, not about the other person's graph.
// Writing the reverse edge too would let anyone silently unfriend someone
// else's account and would make "did they block me?" readable from the victim's
// own list.
func (r *FriendshipRepository) Block(ctx context.Context, playerID, friendPlayerID string) error {
	id, err := newUUID()
	if err != nil {
		return err
	}

	if _, err := r.q.Exec(ctx, blockEdge,
		id,
		playerID,
		friendPlayerID,
		FriendshipBlocked,
		time.Now().UTC(),
	); err != nil {
		if violates(err, sqlstateCheckViolation, friendshipsNoSelfCheck) {
			return ErrSelfFriendship
		}
		return fmt.Errorf("postgres: blocking player %s for player %s: %w", friendPlayerID, playerID, err)
	}

	return nil
}

const deleteFriendship = `
DELETE FROM friendships
WHERE player_id = $1::uuid AND friend_player_id = $2::uuid`

// Remove deletes playerID's own edge, or returns application.ErrNotFriends.
//
// One direction, like Block: the port models a directed graph, and deleting
// the other player's row would let one account rewrite another's social graph
// through a normal command. The consequence is real and deliberate — after a
// one-sided removal the other player still lists the remover as a friend until
// they remove them too — and a mutual unfriend is therefore two calls, made by
// a use case that decides that policy, not by this adapter.
//
// A missing row is reported rather than swallowed: the caller asked to remove
// something that was not there, and for a screen that just offered an
// "unfriend" button that is stale state worth knowing about.
func (r *FriendshipRepository) Remove(ctx context.Context, playerID, friendPlayerID string) error {
	tag, err := r.q.Exec(ctx, deleteFriendship, playerID, friendPlayerID)
	if err != nil {
		return fmt.Errorf("postgres: removing friendship for player %s: %w", playerID, err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotFriends
	}

	return nil
}
