package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/life"
)

// This file persists a character's life (migrations/0028): the life row, the
// history and its consumer's inbox, the nights slept, the photos kept per
// bot and the leaderboards.

// LifeRepository implements application.LifeRepository.
type LifeRepository struct {
	q querier
}

var _ application.LifeRepository = (*LifeRepository)(nil)

// NewLifeRepository returns a repository over the pool, for readers outside
// a unit of work (the gateway's photo cache).
func NewLifeRepository(p *Pool) *LifeRepository { return &LifeRepository{q: p.Raw()} }

const lifeColumns = `player_id::text, born_at, hunger, sleep, stress, needs_at, happiness_at, intelligence,
	COALESCE(rank, ''), rank_since, net_worth, net_worth_at, equity, COALESCE(bio, ''), COALESCE(avatar, ''),
	last_sleep_at, created_at, updated_at`

func scanLife(row pgx.Row) (*application.PlayerLife, error) {
	var l application.PlayerLife
	var hunger, sleep, stress int
	if err := row.Scan(&l.PlayerID, &l.BornAt, &hunger, &sleep, &stress, &l.NeedsAt, &l.HappinessAt, &l.Intelligence,
		&l.Rank, &l.RankSince, &l.NetWorth, &l.NetWorthAt, &l.Equity, &l.Bio, &l.Avatar, &l.LastSleepAt, &l.CreatedAt,
		&l.UpdatedAt); err != nil {
		return nil, err
	}
	l.Hunger, l.Sleep, l.Stress = int64(hunger), int64(sleep), int64(stress)
	l.BornAt, l.NeedsAt, l.HappinessAt = l.BornAt.UTC(), l.NeedsAt.UTC(), l.HappinessAt.UTC()
	l.CreatedAt, l.UpdatedAt = l.CreatedAt.UTC(), l.UpdatedAt.UTC()
	for _, t := range []**time.Time{&l.RankSince, &l.NetWorthAt, &l.LastSleepAt} {
		if *t != nil {
			v := (**t).UTC()
			*t = &v
		}
	}
	return &l, nil
}

// Ensure creates the life on first sight and returns it locked.
func (r *LifeRepository) Ensure(ctx context.Context, d application.PlayerLife) (*application.PlayerLife, error) {
	if !validUUID(d.PlayerID) {
		return nil, application.ErrPlayerNotFound
	}
	l, err := scanLife(r.q.QueryRow(ctx, `
		INSERT INTO player_life (player_id, born_at, hunger, sleep, stress, needs_at, happiness_at, intelligence,
		                         created_at, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (player_id) DO UPDATE SET updated_at = player_life.updated_at
		RETURNING `+lifeColumns,
		d.PlayerID, d.BornAt.UTC(), clampNeed(d.Hunger), clampNeed(d.Sleep), clampNeed(d.Stress), d.NeedsAt.UTC(),
		d.HappinessAt.UTC(), d.Intelligence, d.CreatedAt.UTC()))
	if err != nil {
		return nil, fmt.Errorf("postgres: ensuring a life: %w", err)
	}
	return l, nil
}

func clampNeed(v int64) int { return int(min(max(v, 0), life.MaxPoints*life.Milli)) }

// Get returns the life, nil when none.
func (r *LifeRepository) Get(ctx context.Context, playerID string) (*application.PlayerLife, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	l, err := scanLife(r.q.QueryRow(ctx, `SELECT `+lifeColumns+` FROM player_life WHERE player_id = $1::uuid`, playerID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a life: %w", err)
	}
	return l, nil
}

// Save writes the row.
func (r *LifeRepository) Save(ctx context.Context, l application.PlayerLife) error {
	var rank, bio, avatar any
	if l.Rank != "" {
		rank = l.Rank
	}
	if l.Bio != "" {
		bio = l.Bio
	}
	if l.Avatar != "" {
		avatar = l.Avatar
	}
	if _, err := r.q.Exec(ctx, `
		UPDATE player_life SET hunger = $2, sleep = $3, stress = $4, needs_at = $5, happiness_at = $6,
		       intelligence = $7, rank = $8, rank_since = $9, net_worth = $10, net_worth_at = $11, equity = $12,
		       bio = $13, avatar = $14, last_sleep_at = $15, updated_at = $16
		 WHERE player_id = $1::uuid`,
		l.PlayerID, clampNeed(l.Hunger), clampNeed(l.Sleep), clampNeed(l.Stress), l.NeedsAt.UTC(), l.HappinessAt.UTC(),
		l.Intelligence, rank, l.RankSince, l.NetWorth, l.NetWorthAt, l.Equity, bio, avatar, l.LastSleepAt,
		l.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a life: %w", err)
	}
	return nil
}

// SetRegen stores how fast energy comes back.
func (r *LifeRepository) SetRegen(ctx context.Context, playerID string, bps int) error {
	if _, err := r.q.Exec(ctx, `UPDATE player_stats SET regen_bps = $2 WHERE player_id = $1::uuid AND regen_bps <> $2`,
		playerID, min(max(bps, 1), 10000)); err != nil {
		return fmt.Errorf("postgres: setting energy regeneration: %w", err)
	}
	return nil
}

// Factors reads what a life has going for it.
func (r *LifeRepository) Factors(ctx context.Context, playerID string, homeTypes []string) (application.LifeFactors, error) {
	var f application.LifeFactors
	if homeTypes == nil {
		homeTypes = []string{}
	}
	err := r.q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM properties p WHERE p.owner_player_id = $1::uuid AND p.status = 'owned'
		                  AND p.type_code = ANY($2::text[])
		                  AND NOT EXISTS (SELECT 1 FROM property_leases l WHERE l.property_id = p.id AND l.status = 'active'))
		    OR EXISTS (SELECT 1 FROM property_leases l JOIN properties p ON p.id = l.property_id
		                WHERE l.tenant_player_id = $1::uuid AND l.status = 'active' AND p.type_code = ANY($2::text[])),
		       (SELECT count(*) FROM friendships WHERE player_id = $1::uuid AND status = 'accepted'),
		       EXISTS (SELECT 1 FROM faction_members WHERE player_id = $1::uuid),
		       (SELECT count(*) FROM player_achievements WHERE player_id = $1::uuid)`,
		playerID, homeTypes).Scan(&f.Home, &f.Friends, &f.Faction, &f.Achievements)
	if err != nil {
		return f, fmt.Errorf("postgres: reading a life's factors: %w", err)
	}
	return f, nil
}

// MarkEvent records an event in the inbox once.
func (r *LifeRepository) MarkEvent(ctx context.Context, eventID, playerID, subject string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO life_events (event_id, player_id, subject, processed_at)
	   VALUES ($1, $2::uuid, $3, $4) ON CONFLICT DO NOTHING`, eventID, playerID, subject, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: marking a life event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AddHistory appends an entry once per source.
func (r *LifeRepository) AddHistory(ctx context.Context, e application.HistoryEntry) (bool, error) {
	id := e.ID
	if id == "" {
		var err error
		if id, err = newUUID(); err != nil {
			return false, err
		}
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return false, fmt.Errorf("postgres: encoding a history entry: %w", err)
	}
	tag, err := r.q.Exec(ctx, `
		INSERT INTO life_history (id, player_id, kind, at, public, backfilled, source, data, recorded_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (player_id, source) DO NOTHING`,
		id, e.PlayerID, e.Kind, e.At.UTC(), e.Public, e.Backfilled, e.Source, data, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: appending to a life history: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// History pages a timeline, newest first.
func (r *LifeRepository) History(ctx context.Context, playerID string, publicOnly bool, offset, limit int) (
	[]application.HistoryEntry, int, error,
) {
	var total int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM life_history WHERE player_id = $1::uuid AND (public OR NOT $2)`,
		playerID, publicOnly).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: counting a life history: %w", err)
	}
	rows, err := r.q.Query(ctx, `
		SELECT id::text, player_id::text, kind, at, public, backfilled, source, data
		  FROM life_history WHERE player_id = $1::uuid AND (public OR NOT $2)
		 ORDER BY at DESC, recorded_at DESC, id OFFSET $3 LIMIT $4`, playerID, publicOnly, max(offset, 0), max(limit, 1))
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: reading a life history: %w", err)
	}
	defer rows.Close()
	var out []application.HistoryEntry
	for rows.Next() {
		var e application.HistoryEntry
		var data []byte
		if err := rows.Scan(&e.ID, &e.PlayerID, &e.Kind, &e.At, &e.Public, &e.Backfilled, &e.Source, &data); err != nil {
			return nil, 0, fmt.Errorf("postgres: scanning a life history: %w", err)
		}
		_ = json.Unmarshal(data, &e.Data)
		e.At = e.At.UTC()
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// CountHistory counts entries of a kind.
func (r *LifeRepository) CountHistory(ctx context.Context, playerID, kind string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM life_history WHERE player_id = $1::uuid AND kind = $2`,
		playerID, kind).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting a life history: %w", err)
	}
	return n, nil
}

// netWorthQuery values what players hold. $1 one player or ” for everyone;
// $2..$4 the property prices by city and kind, $5..$6 the goods' reference
// prices. Money is summed as numeric and brought back to bigint.
const netWorthQuery = `
WITH p AS (
    SELECT id, public_code, display_name, telegram_user_id, created_at FROM players
     WHERE status = 'active' AND ($1 = '' OR id = NULLIF($1, '')::uuid)
), acct AS (
    SELECT owner_id,
           SUM(balance) FILTER (WHERE kind = 'player_cash')   AS cash,
           SUM(balance) FILTER (WHERE kind = 'player_bank')   AS bank,
           SUM(balance) FILTER (WHERE kind = 'player_escrow') AS escrow
      FROM accounts WHERE kind IN ('player_cash', 'player_bank', 'player_escrow') AND owner_id IN (SELECT id FROM p)
     GROUP BY owner_id
), eq AS (
    SELECT s.player_id,
           SUM(GREATEST(COALESCE(a.balance, 0) - c.debt, 0)::numeric * s.shares / c.total_shares) AS equity
      FROM company_shareholders s
      JOIN companies c ON c.id = s.company_id AND c.status = 'active'
      LEFT JOIN accounts a ON a.kind = 'company_treasury' AND a.owner_id = c.id
     WHERE s.player_id IN (SELECT id FROM p)
     GROUP BY s.player_id
), prop AS (
    SELECT pr.owner_player_id AS pid, SUM(COALESCE(pp.price, pr.value)::numeric) AS value,
           SUM((pr.tax_debt + pr.upkeep_debt)::numeric) AS debt
      FROM properties pr
      LEFT JOIN unnest($2::uuid[], $3::text[], $4::bigint[]) AS pp(city_id, type_code, price)
             ON pp.city_id = pr.city_id AND pp.type_code = pr.type_code
     WHERE pr.status = 'owned' AND pr.owner_player_id IN (SELECT id FROM p)
     GROUP BY pr.owner_player_id
), held AS (
    SELECT player_id AS pid, item_code, quantity AS qty FROM item_stacks WHERE player_id IN (SELECT id FROM p)
    UNION ALL
    SELECT owner_id, item_code, 1 FROM item_pieces WHERE holding <> 'gone' AND owner_id IN (SELECT id FROM p)
), goods AS (
    SELECT h.pid, SUM(h.qty::numeric * COALESCE(ip.price, 0)) AS value
      FROM held h LEFT JOIN unnest($5::text[], $6::bigint[]) AS ip(code, price) ON ip.code = h.item_code
     GROUP BY h.pid
)
SELECT p.id::text, p.public_code, p.display_name, p.telegram_user_id, p.created_at,
       COALESCE(acct.cash, 0)::bigint, COALESCE(acct.bank, 0)::bigint, COALESCE(acct.escrow, 0)::bigint,
       COALESCE(eq.equity, 0)::bigint, COALESCE(prop.value, 0)::bigint, COALESCE(goods.value, 0)::bigint,
       COALESCE(prop.debt, 0)::bigint
  FROM p
  LEFT JOIN acct ON acct.owner_id = p.id
  LEFT JOIN eq ON eq.player_id = p.id
  LEFT JOIN prop ON prop.pid = p.id
  LEFT JOIN goods ON goods.pid = p.id`

// NetWorth values one player, or every active player.
func (r *LifeRepository) NetWorth(ctx context.Context, prices application.NetWorthPrices, playerID string) (
	[]application.NetWorth, error,
) {
	if playerID != "" && !validUUID(playerID) {
		return nil, nil
	}
	cities, types, pprices := []string{}, []string{}, []int64{}
	for k, v := range prices.PropertyPrices {
		if validUUID(k.CityID) {
			cities, types, pprices = append(cities, k.CityID), append(types, k.Type), append(pprices, v)
		}
	}
	codes, iprices := []string{}, []int64{}
	for k, v := range prices.Items {
		codes, iprices = append(codes, k), append(iprices, v)
	}
	rows, err := r.q.Query(ctx, netWorthQuery, playerID, cities, types, pprices, codes, iprices)
	if err != nil {
		return nil, fmt.Errorf("postgres: valuing players: %w", err)
	}
	defer rows.Close()
	var out []application.NetWorth
	for rows.Next() {
		var n application.NetWorth
		w := &n.Worth
		if err := rows.Scan(&n.PlayerID, &n.Code, &n.Name, &n.TelegramUserID, &n.JoinedAt, &w.Cash, &w.Bank, &w.Escrow, &w.Equity,
			&w.Property, &w.Goods, &w.Debts); err != nil {
			return nil, fmt.Errorf("postgres: scanning a worth: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RecordSleep writes a night at a spot.
func (r *LifeRepository) RecordSleep(ctx context.Context, s application.LifeSleep) error {
	var method, tx any
	if s.Method != "" {
		method = s.Method
	}
	if s.LedgerTx != "" {
		tx = s.LedgerTx
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO life_sleeps (id, player_id, spot, city_id, price, method, ledger_transaction_id, slept_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7::uuid, $8)`,
		s.ID, s.PlayerID, s.Spot, s.CityID, s.Price, method, tx, s.SleptAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a night: %w", err)
	}
	return nil
}

// Photo is a player's photo as one bot knows it.
func (r *LifeRepository) Photo(ctx context.Context, playerID, botID string) (string, time.Time, error) {
	if !validUUID(playerID) || !validUUID(botID) {
		return "", time.Time{}, nil
	}
	var file string
	var at time.Time
	err := r.q.QueryRow(ctx, `SELECT file_id, fetched_at FROM player_photos WHERE player_id = $1::uuid AND bot_id = $2::uuid`,
		playerID, botID).Scan(&file, &at)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", time.Time{}, nil
	case err != nil:
		return "", time.Time{}, fmt.Errorf("postgres: reading a photo: %w", err)
	}
	return file, at.UTC(), nil
}

// KeepPhoto records what one bot knows of a player's photo. It is the
// gateway's: the only process that talks to Telegram.
func (r *LifeRepository) KeepPhoto(ctx context.Context, playerID, botID, fileID string, at time.Time) error {
	if !validUUID(playerID) || !validUUID(botID) || fileID == "" || len(fileID) > 512 {
		return nil
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO player_photos (player_id, bot_id, file_id, fetched_at) VALUES ($1::uuid, $2::uuid, $3, $4)
		ON CONFLICT (player_id, bot_id) DO UPDATE SET file_id = EXCLUDED.file_id, fetched_at = EXCLUDED.fetched_at`,
		playerID, botID, fileID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: keeping a photo: %w", err)
	}
	return nil
}

// ForgetPhotos drops every bot's photo of a player.
func (r *LifeRepository) ForgetPhotos(ctx context.Context, playerID string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM player_photos WHERE player_id = $1::uuid`, playerID); err != nil {
		return fmt.Errorf("postgres: forgetting photos: %w", err)
	}
	return nil
}

// Clock returns the leaderboard clock, locked.
func (r *LifeRepository) Clock(ctx context.Context, now time.Time) (*application.LeaderboardClock, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO leaderboard_clock (id, period_no, period_started_at, updated_at)
		VALUES (1, 1, $1, $1) ON CONFLICT (id) DO NOTHING`, now.UTC()); err != nil {
		return nil, fmt.Errorf("postgres: opening the leaderboard clock: %w", err)
	}
	var c application.LeaderboardClock
	if err := r.q.QueryRow(ctx, `SELECT period_no, period_started_at, next_at, COALESCE(action_id::text, ''), updated_at
		  FROM leaderboard_clock WHERE id = 1 FOR UPDATE`).Scan(&c.PeriodNo, &c.PeriodStartedAt, &c.NextAt, &c.ActionID,
		&c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: reading the leaderboard clock: %w", err)
	}
	c.PeriodStartedAt, c.UpdatedAt = c.PeriodStartedAt.UTC(), c.UpdatedAt.UTC()
	if c.NextAt != nil {
		t := c.NextAt.UTC()
		c.NextAt = &t
	}
	return &c, nil
}

// SaveClock writes the clock.
func (r *LifeRepository) SaveClock(ctx context.Context, c application.LeaderboardClock) error {
	var action any
	if c.ActionID != "" {
		action = c.ActionID
	}
	if _, err := r.q.Exec(ctx, `UPDATE leaderboard_clock SET period_no = $1, period_started_at = $2, next_at = $3,
		action_id = $4::uuid, updated_at = $5 WHERE id = 1`,
		c.PeriodNo, c.PeriodStartedAt.UTC(), c.NextAt, action, c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving the leaderboard clock: %w", err)
	}
	return nil
}

// RecordPeriod records a period's refresh once.
func (r *LifeRepository) RecordPeriod(ctx context.Context, periodNo int64, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO leaderboard_periods (period_no, refreshed_at) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, periodNo, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a leaderboard period: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SaveBoard writes a period's lines.
func (r *LifeRepository) SaveBoard(ctx context.Context, periodNo int64, lines []application.LeaderLine) error {
	for _, l := range lines {
		if _, err := r.q.Exec(ctx, `
			INSERT INTO leaderboard_lines (period_no, board, position, code, name, tag, tag_name, value, extra, extra2)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			periodNo, l.Board, l.Position, l.Code, l.Name, l.Tag, l.TagName, l.Value, l.Extra, l.Extra2); err != nil {
			return fmt.Errorf("postgres: writing a leaderboard line: %w", err)
		}
	}
	return nil
}

// Board is the latest refreshed lines of a board.
func (r *LifeRepository) Board(ctx context.Context, board string) ([]application.LeaderLine, int64, time.Time, error) {
	var period int64
	var at time.Time
	err := r.q.QueryRow(ctx, `SELECT period_no, refreshed_at FROM leaderboard_periods
		ORDER BY period_no DESC LIMIT 1`).Scan(&period, &at)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, 0, time.Time{}, nil
	case err != nil:
		return nil, 0, time.Time{}, fmt.Errorf("postgres: finding the latest leaderboard: %w", err)
	}
	rows, err := r.q.Query(ctx, `SELECT board, position, code, name, tag, tag_name, value, extra, extra2
		FROM leaderboard_lines WHERE period_no = $1 AND board = $2 ORDER BY position`, period, board)
	if err != nil {
		return nil, 0, time.Time{}, fmt.Errorf("postgres: reading a leaderboard: %w", err)
	}
	defer rows.Close()
	var out []application.LeaderLine
	for rows.Next() {
		var l application.LeaderLine
		if err := rows.Scan(&l.Board, &l.Position, &l.Code, &l.Name, &l.Tag, &l.TagName, &l.Value, &l.Extra,
			&l.Extra2); err != nil {
			return nil, 0, time.Time{}, fmt.Errorf("postgres: scanning a leaderboard: %w", err)
		}
		out = append(out, l)
	}
	return out, period, at.UTC(), rows.Err()
}

// Prune drops boards of periods before one.
func (r *LifeRepository) Prune(ctx context.Context, before int64) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM leaderboard_lines WHERE period_no < $1`, before); err != nil {
		return fmt.Errorf("postgres: pruning leaderboards: %w", err)
	}
	return nil
}

// TopCompanies are active companies by book value.
func (r *LifeRepository) TopCompanies(ctx context.Context, limit int) ([]application.LeaderLine, error) {
	rows, err := r.q.Query(ctx, `
		SELECT c.code, c.name, c.type_code, ci.code, ci.name,
		       (COALESCE(a.balance, 0) - c.debt)::bigint AS book, c.rating_bps,
		       (SELECT count(*) FROM employments e WHERE e.company_id = c.id AND e.ended_at IS NULL)
		  FROM companies c
		  JOIN cities ci ON ci.id = c.city_id
		  LEFT JOIN accounts a ON a.kind = 'company_treasury' AND a.owner_id = c.id
		 WHERE c.status = 'active'
		 ORDER BY book DESC, c.founded_at, c.code
		 LIMIT $1`, max(limit, 1))
	if err != nil {
		return nil, fmt.Errorf("postgres: ranking companies: %w", err)
	}
	defer rows.Close()
	var out []application.LeaderLine
	for rows.Next() {
		l := application.LeaderLine{Board: application.BoardCompanies}
		var typeCode string
		if err := rows.Scan(&l.Code, &l.Name, &typeCode, &l.Tag, &l.TagName, &l.Value, &l.Extra, &l.Extra2); err != nil {
			return nil, fmt.Errorf("postgres: scanning a company's rank: %w", err)
		}
		l.Tag = l.Tag + "|" + typeCode
		out = append(out, l)
	}
	return out, rows.Err()
}

// Cities weighs every city.
func (r *LifeRepository) Cities(ctx context.Context) ([]application.CityStanding, error) {
	rows, err := r.q.Query(ctx, `
		SELECT ci.id::text, ci.code, ci.name,
		       (SELECT count(*) FROM players p WHERE p.residence_city_id = ci.id AND p.status = 'active'),
		       (SELECT count(*) FROM companies c WHERE c.city_id = ci.id AND c.status = 'active'),
		       COALESCE((SELECT a.balance FROM accounts a WHERE a.kind = 'city_treasury' AND a.owner_id = ci.id), 0),
		       COALESCE((SELECT d.damage_bps FROM city_war_damage d WHERE d.city_id = ci.id), 0)
		  FROM cities ci ORDER BY ci.code`)
	if err != nil {
		return nil, fmt.Errorf("postgres: weighing cities: %w", err)
	}
	defer rows.Close()
	var out []application.CityStanding
	for rows.Next() {
		var c application.CityStanding
		if err := rows.Scan(&c.CityID, &c.Code, &c.Name, &c.Residents, &c.Companies, &c.Treasury, &c.DamageBPS); err != nil {
			return nil, fmt.Errorf("postgres: scanning a city's standing: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TopWorkers are players by shifts worked and performance.
func (r *LifeRepository) TopWorkers(ctx context.Context, limit int) ([]application.LeaderLine, error) {
	rows, err := r.q.Query(ctx, `
		SELECT p.public_code, p.display_name, p.telegram_user_id, COALESCE(cur.career_code, ''),
		       SUM(e.total_shifts)::bigint AS shifts, COALESCE(MAX(cur.performance), 0)::bigint,
		       SUM(e.total_earned)::bigint
		  FROM employments e
		  JOIN players p ON p.id = e.player_id AND p.status = 'active'
		  LEFT JOIN employments cur ON cur.player_id = p.id AND cur.ended_at IS NULL
		 GROUP BY p.id, p.public_code, p.display_name, p.telegram_user_id, cur.career_code
		HAVING SUM(e.total_shifts) > 0
		 ORDER BY shifts DESC, 6 DESC, p.public_code
		 LIMIT $1`, max(limit, 1))
	if err != nil {
		return nil, fmt.Errorf("postgres: ranking workers: %w", err)
	}
	defer rows.Close()
	var out []application.LeaderLine
	for rows.Next() {
		l := application.LeaderLine{Board: application.BoardWorkers}
		var tg int64
		if err := rows.Scan(&l.Code, &l.Name, &tg, &l.Tag, &l.Value, &l.Extra, &l.Extra2); err != nil {
			return nil, fmt.Errorf("postgres: scanning a worker's rank: %w", err)
		}
		if l.Name == fmt.Sprintf("player-%d", tg) {
			l.Name = ""
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
