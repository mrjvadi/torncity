package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists the watch (migrations/0023): its flags and the
// payments it holds for review, and the reads of the ledger its rules count
// transfers from.

const (
	flagColumns = `id::text, no, rule, player_id::text, COALESCE(other_player_id::text, ''), score, hits, evidence,
	status, created_at, updated_at, cleared_at, COALESCE(cleared_by, ''), COALESCE(note, '')`
	holdColumns = `id::text, no, payer_id::text, payee_id::text, method, amount, COALESCE(flag_id::text, ''), status,
	hold_transaction_id::text, COALESCE(settle_transaction_id::text, ''), created_at, settled_at,
	COALESCE(settled_by, ''), COALESCE(note, '')`
)

// pairRules are the rules a flag of which links two accounts.
var pairRules = []string{"one_way_transfers", "off_market_trade", "single_partner"}

// WatchRepository implements application.WatchRepository and
// application.WatchRecorder.
type WatchRepository struct {
	q querier
}

var (
	_ application.WatchRepository = (*WatchRepository)(nil)
	_ application.WatchRecorder   = (*WatchRepository)(nil)
)

// NewWatchRepository returns the watch over the pool: the game service's
// command-rate recorder and the operator's tool.
func NewWatchRepository(p *Pool) *WatchRepository { return &WatchRepository{q: p.shared()} }

func scanFlag(row pgx.Row) (*application.WatchFlag, error) {
	var (
		f   application.WatchFlag
		raw []byte
	)
	if err := row.Scan(&f.ID, &f.No, &f.Rule, &f.PlayerID, &f.OtherPlayerID, &f.Score, &f.Hits, &raw, &f.Status,
		&f.CreatedAt, &f.UpdatedAt, &f.ClearedAt, &f.ClearedBy, &f.Note); err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &f.Evidence); err != nil {
			return nil, fmt.Errorf("postgres: reading a flag's evidence: %w", err)
		}
	}
	f.CreatedAt, f.UpdatedAt, f.ClearedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC(), utcPtr(f.ClearedAt)
	return &f, nil
}

// Raise records a finding on the open flag of its rule and pair.
func (r *WatchRepository) Raise(ctx context.Context, f application.WatchFlag) (application.WatchFlag, error) {
	id, err := ensureID(f.ID)
	if err != nil {
		return f, err
	}
	evidence, err := json.Marshal(f.Evidence)
	if err != nil {
		return f, err
	}
	at := f.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}
	out, err := scanFlag(r.q.QueryRow(ctx,
		`INSERT INTO watch_flags (id, rule, player_id, other_player_id, score, hits, evidence, status, created_at, updated_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, 1, $6::jsonb, 'open', $7, $7)
		 ON CONFLICT (rule, player_id, COALESCE(other_player_id, '00000000-0000-0000-0000-000000000000'::uuid))
		   WHERE status = 'open'
		 DO UPDATE SET hits = watch_flags.hits + 1, score = GREATEST(watch_flags.score, EXCLUDED.score),
		               evidence = EXCLUDED.evidence, updated_at = EXCLUDED.updated_at
		 RETURNING `+flagColumns,
		id, f.Rule, f.PlayerID, nullableUUID(f.OtherPlayerID), f.Score, string(evidence), at.UTC()))
	if err != nil {
		return f, fmt.Errorf("postgres: raising a flag: %w", err)
	}
	return *out, nil
}

// Linking reads an open flag linking two players.
func (r *WatchRepository) Linking(ctx context.Context, a, b string) (*application.WatchFlag, error) {
	f, err := scanFlag(r.q.QueryRow(ctx,
		`SELECT `+flagColumns+` FROM watch_flags
		  WHERE status = 'open' AND rule = ANY($3)
		    AND ((player_id = $1::uuid AND other_player_id = $2::uuid) OR (player_id = $2::uuid AND other_player_id = $1::uuid))
		  ORDER BY score DESC, no LIMIT 1`, a, b, pairRules))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrFlagNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a linking flag: %w", err)
	}
	return f, nil
}

// paymentsFrom is the payments (cash or card) out of one player's purses
// since, with each payee: the legs of one transaction, a debit of the payer
// and a credit of another player. A held payment counts too, by its row.
const paymentsFrom = `
	WITH out_legs AS (
	    SELECT d.transaction_id, c.amount, ca.owner_id AS payee
	      FROM accounts da
	      JOIN ledger_entries d ON d.account_id = da.id AND d.amount < 0 AND d.created_at >= $2
	                           AND d.reason IN ('cash_payment', 'card_payment')
	      JOIN ledger_entries c ON c.transaction_id = d.transaction_id AND c.amount > 0
	      JOIN accounts ca ON ca.id = c.account_id AND ca.kind IN ('player_cash', 'player_bank')
	     WHERE da.owner_id = $1::uuid AND da.kind IN ('player_cash', 'player_bank')
	    UNION ALL
	    SELECT h.hold_transaction_id, h.amount, h.payee_id
	      FROM payment_holds h WHERE h.payer_id = $1::uuid AND h.created_at >= $2
	)`

// Flow sums what moved from one player to another since.
func (r *WatchRepository) Flow(ctx context.Context, from, to string, since time.Time) (application.TransferFlow, error) {
	var f application.TransferFlow
	err := r.q.QueryRow(ctx, paymentsFrom+`
		SELECT count(DISTINCT transaction_id), COALESCE(SUM(amount), 0)::bigint FROM out_legs WHERE payee = $3::uuid`,
		from, since.UTC(), to).Scan(&f.Count, &f.Total)
	if err != nil {
		if isInvalidUUIDText(err) {
			return f, nil
		}
		return f, fmt.Errorf("postgres: summing transfers: %w", err)
	}
	return f, nil
}

// Outgoing counts a player's payments since, and those to their most
// frequent payee.
func (r *WatchRepository) Outgoing(ctx context.Context, from string, since time.Time) (int, string, int, error) {
	rows, err := r.q.Query(ctx, paymentsFrom+`
		SELECT payee::text, count(DISTINCT transaction_id) FROM out_legs GROUP BY payee ORDER BY 2 DESC, 1`,
		from, since.UTC())
	if err != nil {
		if isInvalidUUIDText(err) {
			return 0, "", 0, nil
		}
		return 0, "", 0, fmt.Errorf("postgres: counting transfers: %w", err)
	}
	defer rows.Close()
	var (
		all, top int
		partner  string
	)
	for rows.Next() {
		var (
			payee string
			n     int
		)
		if err := rows.Scan(&payee, &n); err != nil {
			return 0, "", 0, err
		}
		if partner == "" {
			partner, top = payee, n
		}
		all += n
	}
	return all, partner, top, rows.Err()
}

func scanHold(row pgx.Row) (*application.PaymentHold, error) {
	var h application.PaymentHold
	if err := row.Scan(&h.ID, &h.No, &h.PayerID, &h.PayeeID, &h.Method, &h.Amount, &h.FlagID, &h.Status,
		&h.HoldTransactionID, &h.SettleTransactionID, &h.CreatedAt, &h.SettledAt, &h.SettledBy, &h.Note); err != nil {
		return nil, err
	}
	h.CreatedAt, h.SettledAt = h.CreatedAt.UTC(), utcPtr(h.SettledAt)
	return &h, nil
}

// Hold inserts a held payment.
func (r *WatchRepository) Hold(ctx context.Context, h application.PaymentHold) (application.PaymentHold, error) {
	id, err := ensureID(h.ID)
	if err != nil {
		return h, err
	}
	h.ID, h.Status = id, application.HoldHeld
	err = r.q.QueryRow(ctx,
		`INSERT INTO payment_holds (id, payer_id, payee_id, method, amount, flag_id, status, hold_transaction_id, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, $7, $8::uuid, $9) RETURNING no`,
		h.ID, h.PayerID, h.PayeeID, h.Method, h.Amount, nullableUUID(h.FlagID), h.Status, h.HoldTransactionID,
		h.CreatedAt.UTC()).Scan(&h.No)
	if err != nil {
		return h, fmt.Errorf("postgres: holding a payment: %w", err)
	}
	return h, nil
}

// HoldByNo reads a held payment, locked.
func (r *WatchRepository) HoldByNo(ctx context.Context, no int64) (*application.PaymentHold, error) {
	h, err := scanHold(r.q.QueryRow(ctx, `SELECT `+holdColumns+` FROM payment_holds WHERE no = $1 FOR UPDATE`, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrHoldNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a held payment: %w", err)
	}
	return h, nil
}

// SettleHold closes a held payment.
func (r *WatchRepository) SettleHold(ctx context.Context, h application.PaymentHold) error {
	at := time.Now().UTC()
	if h.SettledAt != nil {
		at = h.SettledAt.UTC()
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE payment_holds SET status = $2, settle_transaction_id = $3::uuid, settled_at = $4, settled_by = $5, note = $6
		  WHERE id = $1::uuid AND status = $7`,
		h.ID, h.Status, h.SettleTransactionID, at, nullableText(h.SettledBy), nullableText(h.Note), application.HoldHeld)
	if err != nil {
		return fmt.Errorf("postgres: settling a held payment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrHoldNotFound
	}
	return nil
}

// Flags lists flags of a status.
func (r *WatchRepository) Flags(ctx context.Context, status string, limit int) ([]application.WatchFlag, error) {
	rows, err := r.q.Query(ctx, `SELECT `+flagColumns+` FROM watch_flags WHERE status = $1
	                              ORDER BY updated_at DESC, no DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing flags: %w", err)
	}
	defer rows.Close()
	var out []application.WatchFlag
	for rows.Next() {
		f, err := scanFlag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// FlagByNo reads one flag.
func (r *WatchRepository) FlagByNo(ctx context.Context, no int64) (*application.WatchFlag, error) {
	f, err := scanFlag(r.q.QueryRow(ctx, `SELECT `+flagColumns+` FROM watch_flags WHERE no = $1`, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrFlagNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a flag: %w", err)
	}
	return f, nil
}

// Clear clears an open flag.
func (r *WatchRepository) Clear(ctx context.Context, no int64, by, note string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE watch_flags SET status = 'cleared', cleared_at = $2, cleared_by = $3, note = $4, updated_at = $2
		  WHERE no = $1 AND status = 'open'`, no, at.UTC(), by, nullableText(note))
	if err != nil {
		return fmt.Errorf("postgres: clearing a flag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrFlagNotFound
	}
	return nil
}

// Holds lists held payments of a status, oldest first.
func (r *WatchRepository) Holds(ctx context.Context, status string, limit int) ([]application.PaymentHold, error) {
	rows, err := r.q.Query(ctx, `SELECT `+holdColumns+` FROM payment_holds WHERE status = $1
	                              ORDER BY created_at, no LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing held payments: %w", err)
	}
	defer rows.Close()
	var out []application.PaymentHold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}
