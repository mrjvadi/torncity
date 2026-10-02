package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// StateSync is the client state sync's log and held versions
// (migrations 0060 and 0061, docs/adr/0034-client-state-sync.md).
//
// # The one write path: Project
//
// A projection opens its own transaction (never inside a command's: a
// projection runs after the command committed, and refusing an ambient
// transaction is how that rule is enforced), takes the player's
// transaction-scoped advisory lock, reads the player's current state on that
// transaction, diffs it against entity_versions (statesync.Plan), and for
// each change appends a log row and bumps its entity_versions row, then
// commits. Every read of the projection, including the settlement layout
// versions read through other repositories, runs on the projection's own
// transaction (the context carries it, ambient.go), so a projection holds
// exactly one connection.
//
// # pts: no holes, crash-safe
//
// pts is allocated under the lock, in the appending transaction, as
// GREATEST(max(pts), min_pts - 1) + 1, and advanced only for a row actually
// inserted. So:
//   - two projections of one player never interleave (the lock), and the
//     second reads the first's committed rows when it computes max(pts):
//     numbers continue, never repeat;
//   - a crash or a rollback takes its numbers away with its rows: the next
//     projection computes the same base again, so no number is skipped;
//   - a change whose cause key is already in the log (a redelivered event)
//     inserts nothing and consumes no number;
//   - the trimmer never deletes a player's newest rows (it keeps at least
//     retention_min), and min_pts - 1 covers a log trimmed to nothing.
//
// Reading the state UNDER the lock is what keeps versions moving forward
// only: a projection that read before locking could append an older state
// after a newer one.
type StateSync struct {
	pool *pgxpool.Pool
	// Rules are the numbers vitals are projected with.
	Rules StateRules
	// Layouts gives a settlement's layout versions per kind of viewer; nil
	// leaves layout_version out of the settlement summary.
	Layouts LayoutVersioner
	// Overlays adds the per-viewer building overlay and the open election to
	// the settlement summary, and the player's next goal; nil leaves them out.
	Overlays SettlementOverlayer
	// LockTimeout bounds the wait for the player's lock (and every other
	// lock) inside a projection; zero waits for the context alone.
	LockTimeout time.Duration
}

// StateRules are the regen rules and limits a projection writes into the
// entities, so a client counts regen locally.
type StateRules struct {
	EnergyRegenAmount   int
	EnergyRegenInterval time.Duration
	NerveMax            int
	NerveRegenAmount    int
	NerveRegenInterval  time.Duration
	// NoticesKept is how many of the latest notices a client holds.
	NoticesKept int
}

// LayoutVersioner is the settlement layout's versions per kind of viewer and
// its grid's side, computed by the same code the layout endpoint uses
// (clientapi.VillageService). It reads through repositories built over the
// pool, which run on the projection's transaction (ambient.go).
type LayoutVersioner interface {
	LayoutVersions(ctx context.Context, settlementID string) (application.LayoutVersions, int, error)
}

// SettlementOverlayer is what a viewer may do with each building of a
// settlement and the player's next goal, computed by the code the client API
// owns (clientapi.VillageService). Like LayoutVersioner it reads through
// repositories built over the pool, which run on the projection's
// transaction (ambient.go). viewer is a statesync.Viewer* kind.
type SettlementOverlayer interface {
	SettlementOverlay(ctx context.Context, settlementID, playerID, viewer string, resident bool) ([]statesync.BuildingOverlay, *statesync.ElectionData, error)
	Goal(ctx context.Context, playerID string) (*statesync.GoalData, error)
}

var _ statesync.Store = (*StateSync)(nil)

// NewStateSync returns the store.
func NewStateSync(p *Pool, rules StateRules) *StateSync {
	if rules.NoticesKept <= 0 {
		rules.NoticesKept = 50
	}
	return &StateSync{pool: p.Raw(), Rules: rules}
}

// ErrProjectInTransaction refuses a projection asked for inside a unit of
// work: it must run after the command's transaction committed, on its own.
var ErrProjectInTransaction = errors.New("postgres: a state projection must not run inside a transaction")

// lockKey is the player's projection lock, hashed to the advisory lock's
// 64-bit key space.
const lockKeySQL = `SELECT pg_advisory_xact_lock(hashtextextended('statesync:' || $1::text, 0))`

// Project is statesync.Store.Project.
func (s *StateSync) Project(ctx context.Context, req statesync.ProjectRequest) (statesync.Projection, error) {
	var out statesync.Projection
	if ambient(ctx) != nil {
		return out, ErrProjectInTransaction
	}
	if !isUUID(req.PlayerID) {
		return out, nil
	}
	if req.At.IsZero() {
		req.At = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("postgres: statesync: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if s.LockTimeout > 0 {
		if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)",
			strconv.FormatInt(s.LockTimeout.Milliseconds(), 10)+"ms"); err != nil {
			return out, fmt.Errorf("postgres: statesync: lock timeout: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, lockKeySQL, req.PlayerID); err != nil {
		return out, fmt.Errorf("postgres: statesync: locking player %s: %w", req.PlayerID, err)
	}
	actx := withAmbient(ctx, tx)

	held, heldData, err := s.held(actx, tx, req.PlayerID)
	if err != nil {
		return out, err
	}
	current, err := s.read(actx, tx, req.PlayerID, req.Kinds, heldData)
	if err != nil {
		return out, err
	}
	changes := statesync.Plan(current, held, req.Kinds)
	base, err := maxPTS(actx, tx, req.PlayerID)
	if err != nil {
		return out, err
	}
	next := base
	for _, c := range changes {
		var data any
		if c.Op != statesync.OpDel {
			data = string(c.Data)
		}
		key := statesync.CauseKey(req.Source, c.Kind, c.ID)
		var pts int64
		err := tx.QueryRow(actx, `
INSERT INTO player_updates (player_id, pts, kind, entity_id, v, op, data, cause, cause_key, at)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)
ON CONFLICT (player_id, cause_key) DO NOTHING
RETURNING pts`, req.PlayerID, next+1, c.Kind, c.ID, c.V, c.Op, data, req.Cause, key, req.At).Scan(&pts)
		if errors.Is(err, pgx.ErrNoRows) {
			// This source already appended this entity (a redelivery that
			// meets a change made since): leave it for the projection of
			// whatever made the change, with its own source.
			continue
		}
		if err != nil {
			return out, fmt.Errorf("postgres: statesync: appending %s/%s: %w", c.Kind, c.ID, err)
		}
		next = pts
		if _, err := tx.Exec(actx, `
INSERT INTO entity_versions (player_id, kind, entity_id, v, hash, data, deleted, updated_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6::jsonb, $7, $8)
ON CONFLICT (player_id, kind, entity_id) DO UPDATE
   SET v = EXCLUDED.v, hash = EXCLUDED.hash, data = EXCLUDED.data, deleted = EXCLUDED.deleted, updated_at = EXCLUDED.updated_at`,
			req.PlayerID, c.Kind, c.ID, c.V, c.Hash, data, c.Op == statesync.OpDel, req.At); err != nil {
			return out, fmt.Errorf("postgres: statesync: holding %s/%s: %w", c.Kind, c.ID, err)
		}
		out.Records = append(out.Records, statesync.Record{PTS: pts, Type: statesync.TypeOf(c.Kind, c.Op), Entity: c.Kind,
			ID: c.ID, V: c.V, Op: c.Op, Data: c.Data, At: req.At, Cause: req.Cause})
	}
	if err := tx.Commit(ctx); err != nil {
		return statesync.Projection{}, fmt.Errorf("postgres: statesync: commit: %w", err)
	}
	out.MaxPTS = next
	return out, nil
}

// maxPTS is the player's highest pts: the log's, or the trimmed floor.
func maxPTS(ctx context.Context, q querier, playerID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `
SELECT GREATEST(
         COALESCE((SELECT max(pts) FROM player_updates WHERE player_id = $1::uuid), 0),
         COALESCE((SELECT min_pts - 1 FROM player_update_state WHERE player_id = $1::uuid), 0))`, playerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: statesync: reading the pts of %s: %w", playerID, err)
	}
	return n, nil
}

// held reads what the player's client was told: every entity's version and
// hash, and the data of the notices (which are re-used rather than read
// again: a notice's view never changes, only whether it was read).
func (s *StateSync) held(ctx context.Context, q querier, playerID string) (map[statesync.Key]statesync.Held, map[string]json.RawMessage, error) {
	rows, err := q.Query(ctx, `
SELECT kind, entity_id, v, hash, deleted, CASE WHEN kind = 'notice' THEN data END
  FROM entity_versions WHERE player_id = $1::uuid`, playerID)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: statesync: reading held versions of %s: %w", playerID, err)
	}
	defer rows.Close()
	held := map[statesync.Key]statesync.Held{}
	notices := map[string]json.RawMessage{}
	for rows.Next() {
		var (
			k    statesync.Key
			h    statesync.Held
			data []byte
		)
		if err := rows.Scan(&k.Kind, &k.ID, &h.V, &h.Hash, &h.Deleted, &data); err != nil {
			return nil, nil, fmt.Errorf("postgres: statesync: scanning held versions: %w", err)
		}
		held[k] = h
		if len(data) > 0 && !h.Deleted {
			notices[k.ID] = json.RawMessage(data)
		}
	}
	return held, notices, rows.Err()
}

// recordColumns are a log row as statesync.Record.
const recordColumns = `pts, kind, entity_id, v, op, data, at, cause`

func scanRecords(rows pgx.Rows) ([]statesync.Record, error) {
	defer rows.Close()
	var out []statesync.Record
	for rows.Next() {
		var (
			r    statesync.Record
			data []byte
		)
		if err := rows.Scan(&r.PTS, &r.Entity, &r.ID, &r.V, &r.Op, &data, &r.At, &r.Cause); err != nil {
			return nil, fmt.Errorf("postgres: statesync: scanning a record: %w", err)
		}
		if len(data) > 0 {
			r.Data = json.RawMessage(data)
		}
		r.Type = statesync.TypeOf(r.Entity, r.Op)
		r.At = r.At.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// BySource is statesync.Store.BySource: the cause keys of one source share
// the prefix "<source>:", found by a range scan of the cause key index
// (COLLATE "C": ';' is the byte after ':').
func (s *StateSync) BySource(ctx context.Context, playerID, source string) ([]statesync.Record, error) {
	if !isUUID(playerID) || source == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+recordColumns+` FROM player_updates
 WHERE player_id = $1::uuid AND cause_key >= $2 AND cause_key < $3 ORDER BY pts`, playerID, source+":", source+";")
	if err != nil {
		return nil, fmt.Errorf("postgres: statesync: reading records of %s: %w", source, err)
	}
	return scanRecords(rows)
}

// ByCause is statesync.Store.ByCause.
func (s *StateSync) ByCause(ctx context.Context, playerID, cause string, window int) ([]statesync.Record, error) {
	if !isUUID(playerID) || cause == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+recordColumns+` FROM player_updates
 WHERE player_id = $1::uuid AND cause = $2
   AND pts > (SELECT COALESCE(max(pts), 0) FROM player_updates WHERE player_id = $1::uuid) - $3
 ORDER BY pts`, playerID, cause, window)
	if err != nil {
		return nil, fmt.Errorf("postgres: statesync: reading records caused by %s: %w", cause, err)
	}
	return scanRecords(rows)
}

// readOnlySnapshot runs fn in one read-only REPEATABLE READ transaction, so
// every statement sees the same database.
func (s *StateSync) readOnlySnapshot(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("postgres: statesync: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// state reads the player's floor and epoch ("1" and 1 when never trimmed).
func state(ctx context.Context, q querier, playerID string) (minPTS int64, epoch string, err error) {
	err = q.QueryRow(ctx, `SELECT min_pts, epoch FROM player_update_state WHERE player_id = $1::uuid`, playerID).Scan(&minPTS, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, "1", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("postgres: statesync: reading the log state of %s: %w", playerID, err)
	}
	return minPTS, epoch, nil
}

// Snapshot is statesync.Store.Snapshot.
func (s *StateSync) Snapshot(ctx context.Context, playerID string, kinds statesync.KindSet) (statesync.Snapshot, error) {
	out := statesync.Snapshot{Entities: map[string]map[string]statesync.SnapshotEntity{}, Channels: map[string]int64{}}
	for _, k := range kinds.List() {
		out.Entities[k] = map[string]statesync.SnapshotEntity{}
	}
	if !isUUID(playerID) {
		out.Epoch = "1"
		return out, nil
	}
	err := s.readOnlySnapshot(ctx, func(tx pgx.Tx) error {
		var err error
		if out.PTS, err = maxPTS(ctx, tx, playerID); err != nil {
			return err
		}
		if _, out.Epoch, err = state(ctx, tx, playerID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT kind, entity_id, v, data FROM entity_versions
 WHERE player_id = $1::uuid AND NOT deleted AND kind = ANY($2::text[])`, playerID, kinds.List())
		if err != nil {
			return fmt.Errorf("postgres: statesync: reading the snapshot of %s: %w", playerID, err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				kind, id string
				v        int64
				data     []byte
			)
			if err := rows.Scan(&kind, &id, &v, &data); err != nil {
				return fmt.Errorf("postgres: statesync: scanning the snapshot: %w", err)
			}
			if out.Entities[kind] == nil {
				out.Entities[kind] = map[string]statesync.SnapshotEntity{}
			}
			out.Entities[kind][id] = statesync.SnapshotEntity{V: v, D: json.RawMessage(data)}
		}
		return rows.Err()
	})
	return out, err
}

// Since is statesync.Store.Since.
func (s *StateSync) Since(ctx context.Context, playerID string, since int64, limit int) (statesync.Log, error) {
	out := statesync.Log{MinPTS: 1, Epoch: "1"}
	if !isUUID(playerID) {
		return out, nil
	}
	err := s.readOnlySnapshot(ctx, func(tx pgx.Tx) error {
		var err error
		if out.MinPTS, out.Epoch, err = state(ctx, tx, playerID); err != nil {
			return err
		}
		if out.MaxPTS, err = maxPTS(ctx, tx, playerID); err != nil {
			return err
		}
		if since < 0 || since >= out.MaxPTS {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT `+recordColumns+` FROM player_updates
 WHERE player_id = $1::uuid AND pts > $2 ORDER BY pts LIMIT $3`, playerID, since, limit)
		if err != nil {
			return fmt.Errorf("postgres: statesync: reading the log of %s: %w", playerID, err)
		}
		out.Records, err = scanRecords(rows)
		return err
	})
	return out, err
}

// Audience is statesync.Store.Audience.
func (s *StateSync) Audience(ctx context.Context, settlementID string, limit int) ([]string, error) {
	if !isUUID(settlementID) {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM players
 WHERE (residence_city_id = $1::uuid OR city_id = $1::uuid) AND status = 'active'
 ORDER BY id LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: statesync: reading the audience of %s: %w", settlementID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Trim is statesync.Store.Trim. One run is one transaction holding a
// transaction-scoped advisory lock taken with try: a second replica's run
// in the same moment sees ok=false and does nothing (the per-run leader
// election of a singleton job). Per player it keeps every record younger
// than p.Age or among the last p.Records, whichever keeps more, never fewer
// than p.Min, and drops entity tombstones older than p.Age.
func (s *StateSync) Trim(ctx context.Context, p statesync.TrimPolicy, cursor string, now time.Time) (statesync.TrimResult, error) {
	var res statesync.TrimResult
	if p.Age <= 0 || p.Records <= 0 {
		return res, nil
	}
	if p.Batch <= 0 {
		p.Batch = 200
	}
	if p.Min <= 0 {
		p.Min = 1
	}
	if cursor == "" || !isUUID(cursor) {
		cursor = "00000000-0000-0000-0000-000000000000"
	}
	cutoff := now.Add(-p.Age)
	keepLast := max(p.Records, p.Min)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("postgres: statesync trim: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var mine bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('statesync:trim', 0))`).Scan(&mine); err != nil {
		return res, fmt.Errorf("postgres: statesync trim: lock: %w", err)
	}
	if !mine {
		return res, nil
	}
	res.Ran = true
	rows, err := tx.Query(ctx, `SELECT DISTINCT player_id::text FROM player_updates
 WHERE at < $1 AND player_id > $2::uuid ORDER BY 1 LIMIT $3`, cutoff, cursor, p.Batch)
	if err != nil {
		return res, fmt.Errorf("postgres: statesync trim: players: %w", err)
	}
	var players []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		players = append(players, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	for _, id := range players {
		// keep_from: the lower of (first record young enough) and (start of
		// the last keepLast records), so whichever rule keeps more wins.
		var keepFrom int64
		if err := tx.QueryRow(ctx, `
SELECT LEAST(
         COALESCE((SELECT min(pts) FROM player_updates WHERE player_id = $1::uuid AND at >= $2),
                  (SELECT COALESCE(max(pts), 0) + 1 FROM player_updates WHERE player_id = $1::uuid)),
         (SELECT COALESCE(max(pts), 0) FROM player_updates WHERE player_id = $1::uuid) - $3 + 1)`,
			id, cutoff, keepLast).Scan(&keepFrom); err != nil {
			return res, fmt.Errorf("postgres: statesync trim: bounds of %s: %w", id, err)
		}
		if keepFrom <= 1 {
			continue
		}
		tag, err := tx.Exec(ctx, `DELETE FROM player_updates WHERE player_id = $1::uuid AND pts < $2`, id, keepFrom)
		if err != nil {
			return res, fmt.Errorf("postgres: statesync trim: deleting for %s: %w", id, err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		res.Players++
		res.Deleted += tag.RowsAffected()
		if _, err := tx.Exec(ctx, `
INSERT INTO player_update_state (player_id, min_pts, epoch, trimmed_at) VALUES ($1::uuid, $2, '1', $3)
ON CONFLICT (player_id) DO UPDATE SET min_pts = GREATEST(player_update_state.min_pts, EXCLUDED.min_pts), trimmed_at = EXCLUDED.trimmed_at`,
			id, keepFrom, now); err != nil {
			return res, fmt.Errorf("postgres: statesync trim: floor of %s: %w", id, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM entity_versions WHERE player_id = $1::uuid AND deleted AND updated_at < $2`, id, cutoff); err != nil {
			return res, fmt.Errorf("postgres: statesync trim: tombstones of %s: %w", id, err)
		}
	}
	if len(players) == p.Batch {
		res.Cursor = players[len(players)-1]
	}
	if err := tx.Commit(ctx); err != nil {
		return statesync.TrimResult{}, fmt.Errorf("postgres: statesync trim: commit: %w", err)
	}
	return res, nil
}
