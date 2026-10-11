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

// RaidRepository is raids, falls, the sight of detectors and the settlements' insurers (migration 0152; docs/adr/0074), bound to one transaction.
type RaidRepository struct{ q querier }

var _ application.RaidRepository = (*RaidRepository)(nil)

const raidColumns = `id::text, attacker_id::text, defender_id::text, filed_by::text, filed_at, start_at, end_at, status, seed, fee,
	COALESCE(fee_tx::text, ''), tribute, looted, resolved_at, outcome`

func scanRaid(row pgx.Row) (*application.SettlementRaid, error) {
	var r application.SettlementRaid
	if err := row.Scan(&r.ID, &r.AttackerID, &r.DefenderID, &r.FiledBy, &r.FiledAt, &r.StartAt, &r.EndAt, &r.Status, &r.Seed, &r.Fee,
		&r.FeeTx, &r.Tribute, &r.Looted, &r.ResolvedAt, &r.Outcome); err != nil {
		return nil, err
	}
	r.FiledAt, r.StartAt, r.EndAt, r.ResolvedAt = r.FiledAt.UTC(), r.StartAt.UTC(), r.EndAt.UTC(), utcPtr(r.ResolvedAt)
	return &r, nil
}

func (r *RaidRepository) raids(ctx context.Context, sql string, args ...any) ([]application.SettlementRaid, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing raids: %w", err)
	}
	defer rows.Close()
	var out []application.SettlementRaid
	for rows.Next() {
		x, err := scanRaid(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a raid: %w", err)
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// Insert writes a filed raid.
func (r *RaidRepository) Insert(ctx context.Context, x application.SettlementRaid) error {
	outcome := string(x.Outcome)
	if outcome == "" {
		outcome = "{}"
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO settlement_raids (id, attacker_id, defender_id, filed_by, filed_at, start_at, end_at, status, seed, fee, fee_tx, outcome)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, 'filed', $8, $9, $10::uuid, $11::jsonb)`,
		x.ID, x.AttackerID, x.DefenderID, x.FiledBy, x.FiledAt.UTC(), x.StartAt.UTC(), x.EndAt.UTC(), x.Seed, x.Fee, nullText(x.FeeTx), outcome); err != nil {
		return fmt.Errorf("postgres: filing a raid: %w", err)
	}
	return nil
}

// ByID reads a raid.
func (r *RaidRepository) ByID(ctx context.Context, id string, lock bool) (*application.SettlementRaid, error) {
	if !validUUID(id) {
		return nil, application.ErrRaidNotFound
	}
	sql := `SELECT ` + raidColumns + ` FROM settlement_raids WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	x, err := scanRaid(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrRaidNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a raid: %w", err)
	}
	return x, nil
}

// Move changes the status once.
func (r *RaidRepository) Move(ctx context.Context, id, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE settlement_raids SET status = $3 WHERE id = $1::uuid AND status = $2`, id, from, to)
	if err != nil {
		return false, fmt.Errorf("postgres: moving a raid: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Finish closes a raid once.
func (r *RaidRepository) Finish(ctx context.Context, id, from, to string, looted bool, tribute *int64, outcome []byte, at time.Time) (bool, error) {
	if outcome == nil {
		outcome = []byte("{}")
	}
	tag, err := r.q.Exec(ctx, `UPDATE settlement_raids SET status = $3, looted = $4, tribute = $5, outcome = $6::jsonb, resolved_at = $7
		WHERE id = $1::uuid AND status = $2`, id, from, to, looted, tribute, string(outcome), at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: finishing a raid: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AgainstSince lists the raids filed against a settlement since a time, newest first.
func (r *RaidRepository) AgainstSince(ctx context.Context, defenderID string, since time.Time) ([]application.SettlementRaid, error) {
	return r.raids(ctx, `SELECT `+raidColumns+` FROM settlement_raids WHERE defender_id = $1::uuid AND filed_at >= $2 ORDER BY filed_at DESC`,
		defenderID, since.UTC())
}

// Of lists a settlement's unfinished raids, then its latest finished ones.
func (r *RaidRepository) Of(ctx context.Context, settlementID string, limit int) ([]application.SettlementRaid, error) {
	return r.raids(ctx, `SELECT `+raidColumns+` FROM settlement_raids WHERE attacker_id = $1::uuid OR defender_id = $1::uuid
		ORDER BY (status IN ('filed', 'open')) DESC, start_at DESC LIMIT $2`, settlementID, limit)
}

// AddParticipant joins a player to a raid on a side, once.
func (r *RaidRepository) AddParticipant(ctx context.Context, p application.RaidParticipant) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO settlement_raid_participants (raid_id, player_id, side, joined_at) VALUES ($1::uuid, $2::uuid, $3, $4)
		ON CONFLICT DO NOTHING`, p.RaidID, p.PlayerID, p.Side, p.JoinedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: joining a raid: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Participants lists a raid's players in the order they joined.
func (r *RaidRepository) Participants(ctx context.Context, raidID string) ([]application.RaidParticipant, error) {
	rows, err := r.q.Query(ctx, `SELECT raid_id::text, player_id::text, side, joined_at FROM settlement_raid_participants WHERE raid_id = $1::uuid
		ORDER BY joined_at, player_id`, raidID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing raid participants: %w", err)
	}
	defer rows.Close()
	var out []application.RaidParticipant
	for rows.Next() {
		var p application.RaidParticipant
		if err := rows.Scan(&p.RaidID, &p.PlayerID, &p.Side, &p.JoinedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a raid participant: %w", err)
		}
		p.JoinedAt = p.JoinedAt.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// OpenFor lists the unfinished raids a player joined.
func (r *RaidRepository) OpenFor(ctx context.Context, playerID string) ([]application.RaidParticipant, error) {
	rows, err := r.q.Query(ctx, `SELECT p.raid_id::text, p.player_id::text, p.side, p.joined_at FROM settlement_raid_participants p
		JOIN settlement_raids x ON x.id = p.raid_id WHERE p.player_id = $1::uuid AND x.status IN ('filed', 'open') ORDER BY p.joined_at`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a player's raids: %w", err)
	}
	defer rows.Close()
	var out []application.RaidParticipant
	for rows.Next() {
		var p application.RaidParticipant
		if err := rows.Scan(&p.RaidID, &p.PlayerID, &p.Side, &p.JoinedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a raid participant: %w", err)
		}
		p.JoinedAt = p.JoinedAt.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecordDetection records a detector's roll once per raid.
func (r *RaidRepository) RecordDetection(ctx context.Context, d application.RaidDetection) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO settlement_raid_detections (raid_id, building_id, code, chance_bps, roll, detected, sight_m, hops, lead_secs, at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT DO NOTHING`,
		d.RaidID, d.BuildingID, d.Code, d.ChanceBPS, d.Roll, d.Detected, d.SightM, d.Hops, d.LeadSecs, d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a detection: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Detections lists a raid's detections.
func (r *RaidRepository) Detections(ctx context.Context, raidID string) ([]application.RaidDetection, error) {
	rows, err := r.q.Query(ctx, `SELECT raid_id::text, building_id::text, code, chance_bps, roll, detected, sight_m, hops, lead_secs, at
		FROM settlement_raid_detections WHERE raid_id = $1::uuid ORDER BY at, building_id`, raidID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing detections: %w", err)
	}
	defer rows.Close()
	var out []application.RaidDetection
	for rows.Next() {
		var d application.RaidDetection
		if err := rows.Scan(&d.RaidID, &d.BuildingID, &d.Code, &d.ChanceBPS, &d.Roll, &d.Detected, &d.SightM, &d.Hops, &d.LeadSecs, &d.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a detection: %w", err)
		}
		d.At = d.At.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddLoot writes one line of what a raid took or broke.
func (r *RaidRepository) AddLoot(ctx context.Context, l application.RaidLoot) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO settlement_raid_loot (id, raid_id, kind, item, qty, building_id, ledger_tx, at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7::uuid, $8)`,
		l.ID, l.RaidID, l.Kind, nullText(l.Item), l.Qty, nullText(l.BuildingID), nullText(l.LedgerTx), l.At.UTC()); err != nil {
		return fmt.Errorf("postgres: writing raid loot: %w", err)
	}
	return nil
}

// Loot lists what a raid took.
func (r *RaidRepository) Loot(ctx context.Context, raidID string) ([]application.RaidLoot, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, raid_id::text, kind, COALESCE(item, ''), qty, COALESCE(building_id::text, ''), COALESCE(ledger_tx::text, ''), at
		FROM settlement_raid_loot WHERE raid_id = $1::uuid ORDER BY at, id`, raidID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing raid loot: %w", err)
	}
	defer rows.Close()
	var out []application.RaidLoot
	for rows.Next() {
		var l application.RaidLoot
		if err := rows.Scan(&l.ID, &l.RaidID, &l.Kind, &l.Item, &l.Qty, &l.BuildingID, &l.LedgerTx, &l.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning raid loot: %w", err)
		}
		l.At = l.At.UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// RecordFall records a fall once per player and cause reference.
func (r *RaidRepository) RecordFall(ctx context.Context, f application.Fall) (bool, error) {
	goods, err := json.Marshal(f.LostGoods)
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO falls (id, player_id, cause, cause_ref, lost_cash, lost_goods, capped, cooling, net_assets, lost_worth, stay_id, cash_tx, at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6::jsonb, $7, $8, $9, $10, $11::uuid, $12::uuid, $13) ON CONFLICT (player_id, cause_ref) DO NOTHING`,
		f.ID, f.PlayerID, f.Cause, f.CauseRef, f.LostCash, string(goods), f.Capped, f.Cooling, f.NetAssets, f.LostWorth, nullText(f.StayID), nullText(f.CashTx), f.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a fall: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// LastFall is when a player last fell, zero for never.
func (r *RaidRepository) LastFall(ctx context.Context, playerID string) (time.Time, error) {
	var at *time.Time
	if err := r.q.QueryRow(ctx, `SELECT max(at) FROM falls WHERE player_id = $1::uuid AND NOT cooling`, playerID).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("postgres: reading a player's last fall: %w", err)
	}
	if at == nil {
		return time.Time{}, nil
	}
	return at.UTC(), nil
}

// Sight reads a stored sight at a revision.
func (r *RaidRepository) Sight(ctx context.Context, buildingID, revision string) ([]int32, error) {
	var s []int32
	err := r.q.QueryRow(ctx, `SELECT sight FROM building_sight WHERE building_id = $1::uuid AND revision = $2`, buildingID, revision).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a building's sight: %w", err)
	}
	return s, nil
}

// SaveSight stores a sight (a racing replica writes the same numbers).
func (r *RaidRepository) SaveSight(ctx context.Context, buildingID, revision string, sight []int32, at time.Time) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO building_sight (building_id, revision, sight, computed_at) VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (building_id) DO UPDATE SET revision = EXCLUDED.revision, sight = EXCLUDED.sight, computed_at = EXCLUDED.computed_at`,
		buildingID, revision, sight, at.UTC()); err != nil {
		return fmt.Errorf("postgres: storing a building's sight: %w", err)
	}
	return nil
}

// Insurer reads a settlement's insurance fund.
func (r *RaidRepository) Insurer(ctx context.Context, settlementID string) (*application.SettlementInsurer, error) {
	var i application.SettlementInsurer
	err := r.q.QueryRow(ctx, `SELECT settlement_id::text, opened_by::text, opened_at, scale_bps FROM settlement_insurers WHERE settlement_id = $1::uuid`,
		settlementID).Scan(&i.SettlementID, &i.OpenedBy, &i.OpenedAt, &i.ScaleBPS)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a settlement's insurer: %w", err)
	}
	i.OpenedAt = i.OpenedAt.UTC()
	return &i, nil
}

// OpenInsurer opens the fund once.
func (r *RaidRepository) OpenInsurer(ctx context.Context, i application.SettlementInsurer) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO settlement_insurers (settlement_id, opened_by, opened_at, scale_bps) VALUES ($1::uuid, $2::uuid, $3, $4)
		ON CONFLICT DO NOTHING`, i.SettlementID, i.OpenedBy, i.OpenedAt.UTC(), i.ScaleBPS)
	if err != nil {
		return false, fmt.Errorf("postgres: opening a settlement insurer: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// OfferTribute puts a tribute on a filed raid.
func (r *RaidRepository) OfferTribute(ctx context.Context, id string, amount int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE settlement_raids SET tribute = $2 WHERE id = $1::uuid AND status = 'filed'`, id, amount); err != nil {
		return fmt.Errorf("postgres: offering a tribute: %w", err)
	}
	return nil
}

// SetInsurerScale moves the price level of a settlement's fund.
func (r *RaidRepository) SetInsurerScale(ctx context.Context, settlementID string, scaleBPS int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE settlement_insurers SET scale_bps = $2 WHERE settlement_id = $1::uuid`, settlementID, scaleBPS); err != nil {
		return fmt.Errorf("postgres: scaling a settlement insurer: %w", err)
	}
	return nil
}
