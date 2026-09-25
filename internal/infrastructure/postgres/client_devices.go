package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
)

// ClientDevices keeps the game clients players have linked and their
// refresh tokens (migrations/0032_client_devices). Only a token's SHA-256
// is ever written.
type ClientDevices struct {
	pool *pgxpool.Pool
}

var _ application.ClientDevices = (*ClientDevices)(nil)

// NewClientDevices returns the repository over the pool.
func NewClientDevices(p *Pool) *ClientDevices { return &ClientDevices{pool: p.Raw()} }

const clientDeviceColumns = `d.id::text, d.player_id::text, COALESCE(d.bot_id::text, ''), d.name, d.via,
	d.created_at, d.last_seen_at, d.revoked_at`

func scanClientDevice(row pgx.Row) (application.ClientDevice, error) {
	var d application.ClientDevice
	err := row.Scan(&d.ID, &d.PlayerID, &d.BotID, &d.Name, &d.Via, &d.CreatedAt, &d.LastSeenAt, &d.RevokedAt)
	return d, err
}

// Create records the device and its first refresh token, then signs out the
// oldest of the player's devices beyond maxDevices, in one transaction.
func (r *ClientDevices) Create(ctx context.Context, d application.ClientDevice, tokenHash string, expiresAt time.Time, maxDevices int) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		// The player's row is locked so two sign-ins at once cannot both
		// count themselves under the limit.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM players WHERE id = $1::uuid FOR UPDATE`, d.PlayerID); err != nil {
			return err
		}
		var bot any
		if d.BotID != "" {
			bot = d.BotID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO client_devices (id, player_id, bot_id, name, via, created_at, last_seen_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $6)`,
			d.ID, d.PlayerID, bot, d.Name, d.Via, d.CreatedAt.UTC()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO client_refresh_tokens (token_hash, device_id, issued_at, expires_at)
			VALUES ($1, $2::uuid, $3, $4)`, tokenHash, d.ID, d.CreatedAt.UTC(), expiresAt.UTC()); err != nil {
			return err
		}
		if maxDevices > 0 {
			if _, err := tx.Exec(ctx, `UPDATE client_devices SET revoked_at = $3, revoked_reason = $4
				WHERE id IN (
				  SELECT id FROM client_devices WHERE player_id = $1::uuid AND revoked_at IS NULL
				  ORDER BY created_at DESC, id DESC OFFSET $2)`,
				d.PlayerID, maxDevices, d.CreatedAt.UTC(), application.ClientRevokedLimit); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("postgres: linking a client device: %w", err)
	}
	return nil
}

// Rotate replaces a refresh token. See application.ClientDevices.
func (r *ClientDevices) Rotate(ctx context.Context, oldHash, newHash string, now, expiresAt time.Time) (application.ClientDevice, error) {
	var out application.ClientDevice
	var refusal error
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var deviceID string
		var tokenExpires time.Time
		var used *time.Time
		err := tx.QueryRow(ctx, `SELECT device_id::text, expires_at, used_at FROM client_refresh_tokens
			WHERE token_hash = $1 FOR UPDATE`, oldHash).Scan(&deviceID, &tokenExpires, &used)
		if errors.Is(err, pgx.ErrNoRows) {
			refusal = application.ErrClientTokenInvalid
			return nil
		}
		if err != nil {
			return err
		}
		d, err := scanClientDevice(tx.QueryRow(ctx, `SELECT `+clientDeviceColumns+`
			FROM client_devices d WHERE d.id = $1::uuid FOR UPDATE`, deviceID))
		if err != nil {
			return err
		}
		switch {
		case d.RevokedAt != nil:
			refusal = application.ErrClientTokenInvalid
			return nil
		case used != nil:
			// A used token presented again: one of the two holders is not
			// the player. The device is signed out; both must sign in again.
			if _, err := tx.Exec(ctx, `UPDATE client_devices SET revoked_at = $2, revoked_reason = $3
				WHERE id = $1::uuid`, d.ID, now.UTC(), application.ClientRevokedReuse); err != nil {
				return err
			}
			refusal = application.ErrClientTokenReused
			return nil
		case !now.Before(tokenExpires):
			refusal = application.ErrClientTokenInvalid
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE client_refresh_tokens SET used_at = $2 WHERE token_hash = $1`, oldHash, now.UTC()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO client_refresh_tokens (token_hash, device_id, issued_at, expires_at)
			VALUES ($1, $2::uuid, $3, $4)`, newHash, d.ID, now.UTC(), expiresAt.UTC()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE client_devices SET last_seen_at = $2 WHERE id = $1::uuid`, d.ID, now.UTC()); err != nil {
			return err
		}
		// Tokens used long ago only prove theft for as long as they could
		// have been valid; past that they are dead weight.
		if _, err := tx.Exec(ctx, `DELETE FROM client_refresh_tokens WHERE device_id = $1::uuid AND expires_at < $2`,
			d.ID, now.UTC()); err != nil {
			return err
		}
		d.LastSeenAt = now.UTC()
		out = d
		return nil
	})
	if err != nil {
		return application.ClientDevice{}, fmt.Errorf("postgres: renewing a client token: %w", err)
	}
	if refusal != nil {
		return application.ClientDevice{}, refusal
	}
	return out, nil
}

// Get returns the device.
func (r *ClientDevices) Get(ctx context.Context, id string) (application.ClientDevice, error) {
	d, err := scanClientDevice(r.pool.QueryRow(ctx, `SELECT `+clientDeviceColumns+`
		FROM client_devices d WHERE d.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ClientDevice{}, application.ErrClientDeviceNotFound
	}
	if err != nil {
		return application.ClientDevice{}, fmt.Errorf("postgres: reading a client device: %w", err)
	}
	return d, nil
}

// Active lists the player's signed-in devices, oldest first.
func (r *ClientDevices) Active(ctx context.Context, playerID string) ([]application.ClientDevice, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+clientDeviceColumns+` FROM client_devices d
		WHERE d.player_id = $1::uuid AND d.revoked_at IS NULL ORDER BY d.created_at, d.id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing client devices: %w", err)
	}
	defer rows.Close()
	var out []application.ClientDevice
	for rows.Next() {
		d, err := scanClientDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: listing client devices: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: listing client devices: %w", err)
	}
	return out, nil
}

// Revoke signs one of the player's devices out.
func (r *ClientDevices) Revoke(ctx context.Context, playerID, deviceID, reason string, now time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE client_devices SET revoked_at = $3, revoked_reason = $4
		WHERE id = $1::uuid AND player_id = $2::uuid AND revoked_at IS NULL`, deviceID, playerID, now.UTC(), reason)
	if err != nil {
		return false, fmt.Errorf("postgres: signing a client device out: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
