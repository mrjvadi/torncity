package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// ItemRepository implements application.ItemRepository over item_stacks,
// item_pieces, item_movements and item_cooldowns
// (migrations/0017_items_and_trade.up.sql).
type ItemRepository struct {
	q querier
}

var _ application.ItemRepository = (*ItemRepository)(nil)

const pieceColumns = `id::text, serial, item_code, archetype, quality, uses_left, COALESCE(owner_id::text, ''),
       holding, origin, origin_ref::text, created_at`

func scanPiece(row pgx.Row) (*application.Piece, error) {
	var p application.Piece
	if err := row.Scan(&p.ID, &p.Serial, &p.Item, &p.Archetype, &p.Quality, &p.UsesLeft, &p.OwnerID,
		&p.Holding, &p.Origin, &p.OriginRef, &p.CreatedAt); err != nil {
		return nil, err
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return &p, nil
}

// LockOwner takes a transaction-scoped advisory lock on the player's goods.
// A lock on the player row would queue a departure and a payment behind
// every trade; the goods need their own.
func (r *ItemRepository) LockOwner(ctx context.Context, playerID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('items:' || $1))`, playerID); err != nil {
		return fmt.Errorf("postgres: locking a player's goods: %w", err)
	}
	return nil
}

// Holdings lists a player's stacks and pieces in one holding.
func (r *ItemRepository) Holdings(ctx context.Context, playerID, holding string) ([]application.Stack, []application.Piece, error) {
	rows, err := r.q.Query(ctx,
		`SELECT item_code, quantity FROM item_stacks
		  WHERE player_id = $1::uuid AND holding = $2 ORDER BY item_code`, playerID, holding)
	if isInvalidUUIDText(err) {
		return nil, nil, application.ErrPlayerNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: reading stacks: %w", err)
	}
	var stacks []application.Stack
	for rows.Next() {
		s := application.Stack{PlayerID: playerID, Holding: holding}
		if err := rows.Scan(&s.Item, &s.Qty); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("postgres: scanning a stack: %w", err)
		}
		stacks = append(stacks, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("postgres: reading stacks: %w", err)
	}
	rows, err = r.q.Query(ctx,
		`SELECT `+pieceColumns+` FROM item_pieces
		  WHERE owner_id = $1::uuid AND holding = $2 ORDER BY item_code, serial`, playerID, holding)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: reading pieces: %w", err)
	}
	defer rows.Close()
	var pieces []application.Piece
	for rows.Next() {
		p, err := scanPiece(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("postgres: scanning a piece: %w", err)
		}
		pieces = append(pieces, *p)
	}
	return stacks, pieces, rows.Err()
}

// Piece returns one piece, locked.
func (r *ItemRepository) Piece(ctx context.Context, id string) (*application.Piece, error) {
	p, err := scanPiece(r.q.QueryRow(ctx, `SELECT `+pieceColumns+` FROM item_pieces WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrPieceNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a piece: %w", err)
	}
	return p, nil
}

// PieceBySerial returns one piece by its serial, locked.
func (r *ItemRepository) PieceBySerial(ctx context.Context, serial string) (*application.Piece, error) {
	p, err := scanPiece(r.q.QueryRow(ctx, `SELECT `+pieceColumns+` FROM item_pieces WHERE serial = $1 FOR UPDATE`, serial))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrPieceNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a piece: %w", err)
	}
	return p, nil
}

// CreatePiece inserts a new piece and its first journal row.
func (r *ItemRepository) CreatePiece(ctx context.Context, p application.Piece, m application.ItemMove) error {
	if p.Origin == "" || p.OriginRef == "" {
		return fmt.Errorf("postgres: a piece without an origin: %w", application.ErrUnknownItemReason)
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO item_pieces (id, serial, item_code, archetype, quality, uses_left, owner_id, holding,
		                          origin, origin_ref, created_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::uuid, $8, $9, $10::uuid, $11)`,
		p.ID, p.Serial, p.Item, p.Archetype, p.Quality, p.UsesLeft, p.OwnerID, p.Holding,
		p.Origin, p.OriginRef, p.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: creating a piece: %w", err)
	}
	m.PieceID, m.Qty, m.From, m.FromHolding = p.ID, 1, "", ""
	return r.journal(ctx, m)
}

// Move applies one movement and journals it.
func (r *ItemRepository) Move(ctx context.Context, m application.ItemMove) error {
	if !m.Reason.Known() {
		return application.ErrUnknownItemReason.WithDetail("reason", string(m.Reason))
	}
	if m.Qty <= 0 {
		return application.ErrNotEnoughItems
	}
	if m.PieceID != "" {
		if err := r.movePiece(ctx, m); err != nil {
			return err
		}
		m.Qty = 1
		return r.journal(ctx, m)
	}
	if m.From != "" {
		// Taking the whole stack deletes it (an empty stack is never kept,
		// and the table's check refuses a zero); taking part of it lowers it.
		tag, err := r.q.Exec(ctx,
			`DELETE FROM item_stacks WHERE player_id = $1::uuid AND item_code = $2 AND holding = $3 AND quantity = $4`,
			m.From, m.Item, m.FromHolding, m.Qty)
		if err != nil {
			return fmt.Errorf("postgres: taking goods: %w", err)
		}
		if tag.RowsAffected() == 0 {
			if tag, err = r.q.Exec(ctx,
				`UPDATE item_stacks SET quantity = quantity - $4
				  WHERE player_id = $1::uuid AND item_code = $2 AND holding = $3 AND quantity > $4`,
				m.From, m.Item, m.FromHolding, m.Qty); err != nil {
				return fmt.Errorf("postgres: taking goods: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return application.ErrNotEnoughItems
			}
		}
	}
	if m.To != "" {
		if _, err := r.q.Exec(ctx,
			`INSERT INTO item_stacks (player_id, item_code, holding, quantity) VALUES ($1::uuid, $2, $3, $4)
			 ON CONFLICT (player_id, item_code, holding) DO UPDATE SET quantity = item_stacks.quantity + EXCLUDED.quantity`,
			m.To, m.Item, m.ToHolding, m.Qty); err != nil {
			return fmt.Errorf("postgres: giving goods: %w", err)
		}
	}
	return r.journal(ctx, m)
}

// movePiece changes a piece's hands, only if the From side still holds it.
func (r *ItemRepository) movePiece(ctx context.Context, m application.ItemMove) error {
	var (
		tag interface{ RowsAffected() int64 }
		err error
	)
	switch {
	case m.To == "":
		tag, err = r.q.Exec(ctx,
			`UPDATE item_pieces SET owner_id = NULL, holding = 'gone', gone_at = $4
			  WHERE id = $1::uuid AND owner_id = $2::uuid AND holding = $3`,
			m.PieceID, m.From, m.FromHolding, m.At.UTC())
	default:
		tag, err = r.q.Exec(ctx,
			`UPDATE item_pieces SET owner_id = $4::uuid, holding = $5
			  WHERE id = $1::uuid AND owner_id = $2::uuid AND holding = $3`,
			m.PieceID, m.From, m.FromHolding, m.To, m.ToHolding)
	}
	if isInvalidUUIDText(err) {
		return application.ErrPieceNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: moving a piece: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrPieceNotFound
	}
	return nil
}

// journal appends one movement.
func (r *ItemRepository) journal(ctx context.Context, m application.ItemMove) error {
	id, err := ensureID(m.ID)
	if err != nil {
		return err
	}
	at := m.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var fromHolding, toHolding *string
	if m.From != "" {
		fromHolding = &m.FromHolding
	}
	if m.To != "" {
		toHolding = &m.ToHolding
	}
	var refType *string
	if m.ReferenceType != "" {
		refType = &m.ReferenceType
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO item_movements (id, item_code, piece_id, quantity, from_player, from_holding, to_player,
		                             to_holding, reason, reference_type, reference_id, created_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4, $5::uuid, $6, $7::uuid, $8, $9, $10, $11::uuid, $12)`,
		id, m.Item, nullableUUID(m.PieceID), m.Qty, nullableUUID(m.From), fromHolding, nullableUUID(m.To), toHolding,
		string(m.Reason), refType, nullableUUID(m.ReferenceID), at.UTC()); err != nil {
		return fmt.Errorf("postgres: journalling goods: %w", err)
	}
	return nil
}

// SetUses records what is left of a piece.
func (r *ItemRepository) SetUses(ctx context.Context, pieceID string, uses int) error {
	if _, err := r.q.Exec(ctx, `UPDATE item_pieces SET uses_left = $2 WHERE id = $1::uuid`, pieceID, max(uses, 0)); err != nil {
		return fmt.Errorf("postgres: wearing a piece: %w", err)
	}
	return nil
}

// LastUsed reads a cooldown group's last use.
func (r *ItemRepository) LastUsed(ctx context.Context, playerID, group string) (time.Time, error) {
	var at time.Time
	err := r.q.QueryRow(ctx,
		`SELECT used_at FROM item_cooldowns WHERE player_id = $1::uuid AND cool_group = $2`, playerID, group).Scan(&at)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("postgres: reading a cooldown: %w", err)
	}
	return at.UTC(), nil
}

// MarkUsed records a use of a cooldown group.
func (r *ItemRepository) MarkUsed(ctx context.Context, playerID, group string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO item_cooldowns (player_id, cool_group, used_at) VALUES ($1::uuid, $2, $3)
		 ON CONFLICT (player_id, cool_group) DO UPDATE SET used_at = EXCLUDED.used_at`,
		playerID, group, at.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a use: %w", err)
	}
	return nil
}
