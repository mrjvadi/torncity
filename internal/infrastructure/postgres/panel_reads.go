package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// Reads the web panel needs beyond the operator's command-line cards. They
// only read; every change goes through the audited paths the command line
// uses.

// PanelCounts is the world at a glance.
type PanelCounts struct {
	Players      int `json:"players"`
	Active15m    int `json:"active_15m"`
	Active24h    int `json:"active_24h"`
	Companies    int `json:"companies"`
	Cities       int `json:"cities"`
	OpenFlags    int `json:"open_flags"`
	HeldPayments int `json:"held_payments"`
}

// PanelCounts counts players, activity, companies, cities and the watch.
func (a *EconomyAdmin) PanelCounts(ctx context.Context, now time.Time) (PanelCounts, error) {
	var c PanelCounts
	err := a.q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM players WHERE status = 'active'),
		(SELECT count(*) FROM players WHERE last_active_at > $1::timestamptz - interval '15 minutes'),
		(SELECT count(*) FROM players WHERE last_active_at > $1::timestamptz - interval '24 hours'),
		(SELECT count(*) FROM companies WHERE status = 'active'),
		(SELECT count(*) FROM cities),
		(SELECT count(*) FROM watch_flags WHERE status = 'open'),
		(SELECT count(*) FROM payment_holds WHERE status = 'held')`, now.UTC()).Scan(
		&c.Players, &c.Active15m, &c.Active24h, &c.Companies, &c.Cities, &c.OpenFlags, &c.HeldPayments)
	if err != nil {
		return c, fmt.Errorf("postgres: panel counts: %w", err)
	}
	return c, nil
}

// PlayerHit is one player found by a search.
type PlayerHit struct {
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Username   string     `json:"username"`
	TelegramID int64      `json:"telegram_id"`
	Status     string     `json:"status"`
	City       string     `json:"city"`
	LastActive *time.Time `json:"last_active"`
}

// SearchPlayers finds players by public code, @username, Telegram id or
// display name, most recently active first.
func (a *EconomyAdmin) SearchPlayers(ctx context.Context, query string, limit int) ([]PlayerHit, error) {
	q := strings.TrimSpace(query)
	tgID, _ := strconv.ParseInt(q, 10, 64)
	handle := strings.TrimPrefix(q, "@")
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(handle) + "%"
	rows, err := a.q.Query(ctx, `SELECT p.public_code, p.display_name, COALESCE(p.username, ''), p.telegram_user_id,
		p.status, COALESCE(c.code, ''), p.last_active_at
		FROM players p LEFT JOIN cities c ON c.id = p.city_id
		WHERE $1 = '' OR p.public_code = $2 OR p.telegram_user_id = $3
		   OR p.username ILIKE $4 OR p.display_name ILIKE $4
		ORDER BY p.last_active_at DESC NULLS LAST, p.created_at DESC LIMIT $5`,
		q, playercode.Normalize(q), tgID, like, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: searching players: %w", err)
	}
	defer rows.Close()
	var out []PlayerHit
	for rows.Next() {
		var h PlayerHit
		if err := rows.Scan(&h.Code, &h.Name, &h.Username, &h.TelegramID, &h.Status, &h.City, &h.LastActive); err != nil {
			return nil, fmt.Errorf("postgres: searching players: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// PlayerExtra is what the panel shows of a player beyond their card.
type PlayerExtra struct {
	TelegramID int64       `json:"telegram_id"`
	Username   string      `json:"username"`
	Status     string      `json:"status"`
	Language   string      `json:"language"`
	LastActive *time.Time  `json:"last_active"`
	History    []LifeEntry `json:"history"`
	Flags      []FlagLine  `json:"flags"`
	Licences   []Licence   `json:"licences"`
}

// LifeEntry is one moment of a player's life history.
type LifeEntry struct {
	Kind       string          `json:"kind"`
	At         time.Time       `json:"at"`
	Public     bool            `json:"public"`
	Backfilled bool            `json:"backfilled"`
	Data       json.RawMessage `json:"data"`
}

// FlagLine is a watch flag in a list.
type FlagLine struct {
	No        int64      `json:"no"`
	Rule      string     `json:"rule"`
	Player    string     `json:"player"`
	Other     string     `json:"other"`
	Score     int        `json:"score"`
	Hits      int        `json:"hits"`
	Status    string     `json:"status"`
	Evidence  any        `json:"evidence,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClearedAt *time.Time `json:"cleared_at,omitempty"`
	ClearedBy string     `json:"cleared_by,omitempty"`
	Note      string     `json:"note,omitempty"`
}

// Licence is a defence licence of a company.
type Licence struct {
	No        int64      `json:"no"`
	Company   string     `json:"company"`
	Kind      string     `json:"kind"`
	Basis     string     `json:"basis"`
	Status    string     `json:"status"`
	Office    string     `json:"decided_office"`
	Decided   *time.Time `json:"decided_at"`
	Revoked   *time.Time `json:"revoked_at"`
	Effective *time.Time `json:"effective_at"`
}

const flagLineSQL = `SELECT f.no, f.rule, p.public_code, COALESCE(o.public_code, ''), f.score, f.hits, f.status,
	f.evidence, f.created_at, f.updated_at, f.cleared_at, COALESCE(f.cleared_by, ''), COALESCE(f.note, '')
	FROM watch_flags f JOIN players p ON p.id = f.player_id LEFT JOIN players o ON o.id = f.other_player_id`

func scanFlagLines(rows pgx.Rows) ([]FlagLine, error) {
	defer rows.Close()
	var out []FlagLine
	for rows.Next() {
		var f FlagLine
		var ev []byte
		if err := rows.Scan(&f.No, &f.Rule, &f.Player, &f.Other, &f.Score, &f.Hits, &f.Status, &ev,
			&f.CreatedAt, &f.UpdatedAt, &f.ClearedAt, &f.ClearedBy, &f.Note); err != nil {
			return nil, fmt.Errorf("postgres: reading flags: %w", err)
		}
		f.Evidence = json.RawMessage(ev)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FlagLines lists flags of a status, most recent first.
func (a *EconomyAdmin) FlagLines(ctx context.Context, status string, limit int) ([]FlagLine, error) {
	rows, err := a.q.Query(ctx, flagLineSQL+` WHERE f.status = $1 ORDER BY f.updated_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing flags: %w", err)
	}
	return scanFlagLines(rows)
}

// FlagLine reads one flag by its number.
func (a *EconomyAdmin) FlagLine(ctx context.Context, no int64) (FlagLine, error) {
	rows, err := a.q.Query(ctx, flagLineSQL+` WHERE f.no = $1`, no)
	if err != nil {
		return FlagLine{}, fmt.Errorf("postgres: reading a flag: %w", err)
	}
	list, err := scanFlagLines(rows)
	if err != nil {
		return FlagLine{}, err
	}
	if len(list) == 0 {
		return FlagLine{}, ErrNotFound
	}
	return list[0], nil
}

// ErrNotFound is a panel read of something that does not exist.
var ErrNotFound = errors.New("postgres: not found")

// PlayerExtra reads a player's identity, recent life history, the watch's
// flags about them and their companies' defence licences.
func (a *EconomyAdmin) PlayerExtra(ctx context.Context, playerID string, historyLimit int) (PlayerExtra, error) {
	var x PlayerExtra
	if err := a.q.QueryRow(ctx, `SELECT telegram_user_id, COALESCE(username, ''), status, language, last_active_at
		FROM players WHERE id = $1::uuid`, playerID).Scan(&x.TelegramID, &x.Username, &x.Status, &x.Language, &x.LastActive); err != nil {
		return x, fmt.Errorf("postgres: player extra: %w", err)
	}
	rows, err := a.q.Query(ctx, `SELECT kind, at, public, backfilled, data FROM life_history
		WHERE player_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, playerID, historyLimit)
	if err != nil {
		return x, fmt.Errorf("postgres: player history: %w", err)
	}
	for rows.Next() {
		var e LifeEntry
		var data []byte
		if err := rows.Scan(&e.Kind, &e.At, &e.Public, &e.Backfilled, &data); err != nil {
			rows.Close()
			return x, fmt.Errorf("postgres: player history: %w", err)
		}
		e.Data = data
		x.History = append(x.History, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return x, fmt.Errorf("postgres: player history: %w", err)
	}
	flagRows, err := a.q.Query(ctx, flagLineSQL+` WHERE f.player_id = $1::uuid OR f.other_player_id = $1::uuid
		ORDER BY f.updated_at DESC LIMIT 50`, playerID)
	if err != nil {
		return x, fmt.Errorf("postgres: player flags: %w", err)
	}
	if x.Flags, err = scanFlagLines(flagRows); err != nil {
		return x, err
	}
	x.Licences, err = a.licences(ctx, `c.owner_player_id = $1::uuid`, playerID)
	return x, err
}

func (a *EconomyAdmin) licences(ctx context.Context, where string, arg any) ([]Licence, error) {
	rows, err := a.q.Query(ctx, `SELECT l.no, c.code, l.kind, l.basis, l.status, COALESCE(l.decided_office, ''),
		l.decided_at, l.revoked_at, l.effective_at
		FROM defence_licences l JOIN companies c ON c.id = l.company_id WHERE `+where+` ORDER BY l.no DESC LIMIT 50`, arg)
	if err != nil {
		return nil, fmt.Errorf("postgres: licences: %w", err)
	}
	defer rows.Close()
	var out []Licence
	for rows.Next() {
		var l Licence
		if err := rows.Scan(&l.No, &l.Company, &l.Kind, &l.Basis, &l.Status, &l.Office, &l.Decided, &l.Revoked, &l.Effective); err != nil {
			return nil, fmt.Errorf("postgres: licences: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CompanyLicences lists a company's defence licences, newest first.
func (a *EconomyAdmin) CompanyLicences(ctx context.Context, companyCode string) ([]Licence, error) {
	return a.licences(ctx, `c.code = upper($1)`, companyCode)
}
