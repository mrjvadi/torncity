package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// The console's changes that are rows of their own, each in one transaction
// with its audit row (target_id naming the player or the action): a player's
// moderation imposed or lifted, a sentence or a hospital stay brought to an
// end now, and a stuck timed action handed back to the scheduler.
//
// Ending a sentence or a stay early does not end it here. It moves the end
// to now and the action that ends it to now; the scheduler then runs the
// game's own release or discharge — the same statements, events and
// resumed studies a sentence served in full gets.

// ErrPanelConflict is a console change the state does not allow: a
// moderation already standing, nothing to release, an action not stuck.
var ErrPanelConflict = errors.New("postgres: the change does not fit the current state")

// PanelOps carries out the console's row changes.
type PanelOps struct{ q routed }

// NewPanelOps returns them over the pool.
func NewPanelOps(p *Pool) *PanelOps { return &PanelOps{q: p.shared()} }

// PanelActor is who makes a change, why and when.
type PanelActor struct {
	Name, Reason string
	At           time.Time
}

func (a PanelActor) valid() error {
	if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Reason) == "" || a.At.IsZero() {
		return errors.New("postgres: a console change needs who makes it, why and when")
	}
	return nil
}

// inTx runs fn in one transaction with the audit row it asks for.
func (o *PanelOps) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if InTransaction(ctx) {
		return ErrPanelReadInCommand
	}
	tx, err := o.q.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: console change: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: console change: commit: %w", err)
	}
	return nil
}

func auditTx(ctx context.Context, tx pgx.Tx, a PanelActor, action, targetType, targetID string, value map[string]any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("postgres: encoding audit value: %w", err)
	}
	var target any
	if targetID != "" {
		target = targetID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		VALUES ($1, $2, $3, $4::uuid, NULL, $5, $6, $7)`, a.Name, action, targetType, target, b, a.Reason, a.At.UTC()); err != nil {
		return fmt.Errorf("postgres: writing audit row: %w", err)
	}
	return nil
}

// lockPlayer reads a player by public code and locks the row.
func lockPlayer(ctx context.Context, tx pgx.Tx, code string) (id string, telegramID int64, err error) {
	err = tx.QueryRow(ctx, `SELECT id::text, telegram_user_id FROM players WHERE public_code = $1 FOR UPDATE`,
		playercode.Normalize(code)).Scan(&id, &telegramID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, ErrNoSuchPlayer
	}
	if err != nil {
		return "", 0, fmt.Errorf("postgres: reading a player: %w", err)
	}
	return id, telegramID, nil
}

// Moderation kinds.
const (
	ModerationMute = "mute"
	ModerationBan  = "ban"
)

// ModerationChange imposes (For > 0 for a time, 0 until lifted) or lifts a
// player's mute or ban.
type ModerationChange struct {
	Player string
	Kind   string
	For    time.Duration
	Actor  PanelActor
}

// Moderated is a moderation imposed or lifted.
type Moderated struct {
	No         int64      `json:"no"`
	Player     string     `json:"player"`
	PlayerID   string     `json:"-"`
	TelegramID int64      `json:"-"`
	Kind       string     `json:"kind"`
	EndsAt     *time.Time `json:"ends_at"`
}

// Moderate imposes a mute or a ban. One of the kind already standing is a
// conflict: it is lifted first, deliberately, not silently replaced.
func (o *PanelOps) Moderate(ctx context.Context, m ModerationChange) (Moderated, error) {
	var out Moderated
	if err := m.Actor.valid(); err != nil {
		return out, err
	}
	if m.Kind != ModerationMute && m.Kind != ModerationBan {
		return out, fmt.Errorf("postgres: %q is not a moderation", m.Kind)
	}
	if m.For < 0 {
		return out, errors.New("postgres: a moderation's length is not negative")
	}
	at := m.Actor.At.UTC()
	var ends *time.Time
	if m.For > 0 {
		e := at.Add(m.For)
		ends = &e
	}
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		id, tg, err := lockPlayer(ctx, tx, m.Player)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE player_moderation SET lifted_at = GREATEST(ends_at, imposed_at + interval '1 microsecond'),
			lifted_by = 'expired', lift_reason = 'expired'
			WHERE player_id = $1::uuid AND kind = $2 AND lifted_at IS NULL AND ends_at IS NOT NULL AND ends_at <= $3`,
			id, m.Kind, at); err != nil {
			return fmt.Errorf("postgres: closing an expired moderation: %w", err)
		}
		mid, err := newUUID()
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO player_moderation (id, player_id, kind, reason, imposed_by, imposed_at, ends_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING RETURNING no`,
			mid, id, m.Kind, m.Actor.Reason, m.Actor.Name, at, ends).Scan(&out.No)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: a %s already stands; lift it first", ErrPanelConflict, m.Kind)
		}
		if err != nil {
			return fmt.Errorf("postgres: imposing a moderation: %w", err)
		}
		out = Moderated{No: out.No, Player: playercode.Normalize(m.Player), PlayerID: id, TelegramID: tg, Kind: m.Kind, EndsAt: ends}
		return auditTx(ctx, tx, m.Actor, "player."+m.Kind, "players", id, map[string]any{"player": id,
			"code": out.Player, "no": out.No, "ends_at": ends})
	})
	return out, err
}

// LiftModeration lifts a standing mute or ban.
func (o *PanelOps) LiftModeration(ctx context.Context, m ModerationChange) (Moderated, error) {
	var out Moderated
	if err := m.Actor.valid(); err != nil {
		return out, err
	}
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		id, tg, err := lockPlayer(ctx, tx, m.Player)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `UPDATE player_moderation SET lifted_at = GREATEST($3, imposed_at + interval '1 microsecond'),
			lifted_by = $4, lift_reason = $5
			WHERE player_id = $1::uuid AND kind = $2 AND lifted_at IS NULL RETURNING no`,
			id, m.Kind, m.Actor.At.UTC(), m.Actor.Name, m.Actor.Reason).Scan(&out.No)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no %s stands", ErrPanelConflict, m.Kind)
		}
		if err != nil {
			return fmt.Errorf("postgres: lifting a moderation: %w", err)
		}
		out.Player, out.PlayerID, out.TelegramID, out.Kind = playercode.Normalize(m.Player), id, tg, m.Kind
		return auditTx(ctx, tx, m.Actor, "player.un"+m.Kind, "players", id, map[string]any{"player": id,
			"code": out.Player, "no": out.No})
	})
	return out, err
}

// Standing is what moderation stands against a Telegram user now.
type Standing struct {
	Muted, Banned bool
	// Until is when the earliest of them ends; zero while one stands until
	// lifted, or when none stands.
	Until time.Time
}

// ModerationReader answers the gateway's one question.
type ModerationReader struct{ q querier }

// NewModerationReader returns it over the pool.
func NewModerationReader(p *Pool) *ModerationReader { return &ModerationReader{q: p.shared()} }

// Standing reads the moderation standing against a Telegram user at now.
func (r *ModerationReader) Standing(ctx context.Context, telegramUserID int64, now time.Time) (Standing, error) {
	var s Standing
	rows, err := r.q.Query(ctx, `SELECT m.kind, m.ends_at FROM player_moderation m JOIN players p ON p.id = m.player_id
		WHERE p.telegram_user_id = $1 AND m.lifted_at IS NULL AND (m.ends_at IS NULL OR m.ends_at > $2)`,
		telegramUserID, now.UTC())
	if err != nil {
		return s, fmt.Errorf("postgres: reading moderation: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var ends *time.Time
		if err := rows.Scan(&kind, &ends); err != nil {
			return s, fmt.Errorf("postgres: reading moderation: %w", err)
		}
		switch kind {
		case ModerationMute:
			s.Muted = true
		case ModerationBan:
			s.Banned = true
		}
		if ends != nil && (s.Until.IsZero() || ends.Before(s.Until)) {
			s.Until = *ends
		}
	}
	return s, rows.Err()
}

// Ended is a sentence or a stay brought to its end now.
type Ended struct {
	ID       string    `json:"id"`
	Player   string    `json:"player"`
	PlayerID string    `json:"-"`
	WasEnd   time.Time `json:"was_ends_at"`
	EndsAt   time.Time `json:"ends_at"`
	Action   string    `json:"action"`
}

// endNow moves one row's end and its action's due time to now.
func (o *PanelOps) endNow(ctx context.Context, a PanelActor, player, what string) (Ended, error) {
	var out Ended
	if err := a.valid(); err != nil {
		return out, err
	}
	table, open, start, audit := "jail_sentences", "serving", "starts_at", "player.release"
	if what == "hospital" {
		table, open, start, audit = "hospital_stays", "admitted", "admitted_at", "player.discharge"
	}
	now := a.At.UTC()
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		id, _, err := lockPlayer(ctx, tx, player)
		if err != nil {
			return err
		}
		var action *string
		err = tx.QueryRow(ctx, `SELECT id::text, ends_at, game_action_id::text FROM `+table+`
			WHERE player_id = $1::uuid AND status = $2 FOR UPDATE`, id, open).Scan(&out.ID, &out.WasEnd, &action)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the player is not %s", ErrPanelConflict, map[string]string{"jail": "in jail", "hospital": "in hospital"}[what])
		}
		if err != nil {
			return fmt.Errorf("postgres: reading the %s: %w", what, err)
		}
		if err := tx.QueryRow(ctx, `UPDATE `+table+` SET ends_at = GREATEST($2, `+start+` + interval '1 second')
			WHERE id = $1::uuid AND ends_at > $2 RETURNING ends_at`, out.ID, now).Scan(&out.EndsAt); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("postgres: ending the %s: %w", what, err)
			}
			out.EndsAt = out.WasEnd
		}
		if action != nil {
			out.Action = *action
			if _, err := tx.Exec(ctx, `UPDATE game_actions SET finish_at = LEAST(finish_at, $2), status = 'scheduled',
				claimed_at = NULL, claimed_by = NULL, completed_at = NULL, payload = payload - 'last_error'
				WHERE id = $1::uuid AND status IN ('scheduled', 'failed')`, *action, out.EndsAt); err != nil {
				return fmt.Errorf("postgres: bringing the %s's action forward: %w", what, err)
			}
		}
		out.Player, out.PlayerID = playercode.Normalize(player), id
		return auditTx(ctx, tx, a, audit, table, id, map[string]any{"player": id, "code": out.Player, "row": out.ID,
			"was_ends_at": out.WasEnd, "ends_at": out.EndsAt, "action": out.Action})
	})
	return out, err
}

// ReleaseFromJail ends a player's sentence now.
func (o *PanelOps) ReleaseFromJail(ctx context.Context, a PanelActor, player string) (Ended, error) {
	return o.endNow(ctx, a, player, "jail")
}

// DischargeFromHospital ends a player's hospital stay now.
func (o *PanelOps) DischargeFromHospital(ctx context.Context, a PanelActor, player string) (Ended, error) {
	return o.endNow(ctx, a, player, "hospital")
}

// StuckAfter is how long a claim must be held before its action counts as
// stuck, the same order as the scheduler's lease.
const StuckAfter = 5 * time.Minute

// Requeued is a timed action handed back to the scheduler.
type Requeued struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	WasStatus string `json:"was_status"`
	LastError string `json:"last_error"`
}

// RequeueAction hands a failed action, or one whose claim has been held
// past StuckAfter, back to the scheduler: scheduled, unclaimed, due now at
// the latest. The game's handler then runs it as it would have.
func (o *PanelOps) RequeueAction(ctx context.Context, a PanelActor, actionID string) (Requeued, error) {
	var out Requeued
	if err := a.valid(); err != nil {
		return out, err
	}
	now := a.At.UTC()
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		var actor *string
		var claimed *time.Time
		err := tx.QueryRow(ctx, `SELECT id::text, action_type, status, COALESCE(payload->>'last_error', ''), actor_id::text, claimed_at
			FROM game_actions WHERE id = $1::uuid FOR UPDATE`, actionID).Scan(&out.ID, &out.Type, &out.WasStatus,
			&out.LastError, &actor, &claimed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("postgres: reading an action: %w", err)
		}
		stuck := out.WasStatus == "failed" || (out.WasStatus == "running" && claimed != nil && now.Sub(*claimed) >= StuckAfter)
		if !stuck {
			return fmt.Errorf("%w: the action is %s, not failed or stuck", ErrPanelConflict, out.WasStatus)
		}
		if _, err := tx.Exec(ctx, `UPDATE game_actions SET status = 'scheduled', claimed_at = NULL, claimed_by = NULL,
			completed_at = NULL, finish_at = LEAST(finish_at, $2), payload = payload - 'last_error'
			WHERE id = $1::uuid`, out.ID, now); err != nil {
			return fmt.Errorf("postgres: requeueing an action: %w", err)
		}
		value := map[string]any{"action": out.ID, "type": out.Type, "was_status": out.WasStatus, "last_error": out.LastError}
		if actor != nil {
			value["player"] = *actor
		}
		return auditTx(ctx, tx, a, "action.requeue", "game_actions", out.ID, value)
	})
	return out, err
}
