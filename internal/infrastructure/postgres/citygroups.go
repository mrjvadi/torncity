package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// CityGroupRepository implements application.CityGroupRepository over
// city_group_links (migrations/0016), and the operator's link and unlink.
type CityGroupRepository struct {
	q transactor
}

var _ application.CityGroupRepository = (*CityGroupRepository)(nil)

// NewCityGroupRepository returns the repository over the pool.
func NewCityGroupRepository(p *Pool) *CityGroupRepository {
	return &CityGroupRepository{q: p.Raw()}
}

const cityGroupColumns = `c.id::text, c.code, c.name, l.chat_id, l.bot_id::text, l.language, l.linked_by, l.linked_at`

func scanCityGroups(rows pgx.Rows) ([]application.CityGroup, error) {
	defer rows.Close()
	var out []application.CityGroup
	for rows.Next() {
		var g application.CityGroup
		if err := rows.Scan(&g.CityID, &g.CityCode, &g.CityName, &g.ChatID, &g.BotID, &g.Language,
			&g.LinkedBy, &g.LinkedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a city group: %w", err)
		}
		g.LinkedAt = g.LinkedAt.UTC()
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading city groups: %w", err)
	}
	return out, nil
}

// ForCity returns a city's groups, oldest link first.
func (r *CityGroupRepository) ForCity(ctx context.Context, cityID, cityCode string) ([]application.CityGroup, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+cityGroupColumns+`
		   FROM city_group_links l JOIN cities c ON c.id = l.city_id
		  WHERE ($1 <> '' AND c.id::text = $1) OR ($1 = '' AND c.code = $2)
		  ORDER BY l.linked_at, l.chat_id`, cityID, cityCode)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a city's groups: %w", err)
	}
	return scanCityGroups(rows)
}

// ByChat returns the link of one group, or nil.
func (r *CityGroupRepository) ByChat(ctx context.Context, chatID int64) (*application.CityGroup, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+cityGroupColumns+`
		   FROM city_group_links l JOIN cities c ON c.id = l.city_id
		  WHERE l.chat_id = $1`, chatID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a group's city: %w", err)
	}
	groups, err := scanCityGroups(rows)
	if err != nil || len(groups) == 0 {
		return nil, err
	}
	return &groups[0], nil
}

// CityWithGroup reports whether a city has a group, and the city; the
// gateway's hint for a group-only command in the private chat reads it.
func (r *CityGroupRepository) CityWithGroup(ctx context.Context, cityID string) (*application.City, bool, error) {
	groups, err := r.ForCity(ctx, cityID, "")
	if err != nil || len(groups) == 0 {
		return nil, false, err
	}
	return &application.City{ID: groups[0].CityID, Code: groups[0].CityCode, Name: groups[0].CityName}, true, nil
}

// All lists every link, by city then group.
func (r *CityGroupRepository) All(ctx context.Context) ([]application.CityGroup, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+cityGroupColumns+`
		   FROM city_group_links l JOIN cities c ON c.id = l.city_id
		  ORDER BY c.code, l.chat_id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing city groups: %w", err)
	}
	return scanCityGroups(rows)
}

// Errors an operator's link can meet.
var (
	ErrCityGroupCity  = errors.New("postgres: no city has that code")
	ErrCityGroupBot   = errors.New("postgres: no bot has that key")
	ErrCityGroupTaken = errors.New("postgres: that group is already linked to another city")
	ErrCityGroupNone  = errors.New("postgres: that group is not linked to that city")
)

// CityGroupChange is one operator link or unlink, audited.
type CityGroupChange struct {
	CityCode string
	ChatID   int64
	// BotKey names the bot that serves the group (telegram_bots.bot_key).
	BotKey   string
	Language string
	Actor    string
	Reason   string
	At       time.Time
}

// Link ties a group to a city, or updates the bot and language of the link
// it already has, and writes the audit row in the same transaction.
func (r *CityGroupRepository) Link(ctx context.Context, ch CityGroupChange) (application.CityGroup, error) {
	var out application.CityGroup
	err := inTx(ctx, r.q, func(ctx context.Context, tx pgx.Tx) error {
		var cityID, botID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM cities WHERE code = $1`, ch.CityCode).Scan(&cityID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCityGroupCity
			}
			return fmt.Errorf("postgres: reading the city: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT id::text FROM telegram_bots WHERE bot_key = $1`, ch.BotKey).Scan(&botID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCityGroupBot
			}
			return fmt.Errorf("postgres: reading the bot: %w", err)
		}
		var other string
		err := tx.QueryRow(ctx, `SELECT city_id::text FROM city_group_links WHERE chat_id = $1`, ch.ChatID).Scan(&other)
		switch {
		case err == nil && other != cityID:
			return ErrCityGroupTaken
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("postgres: reading the group's link: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO city_group_links (city_id, chat_id, bot_id, language, linked_by, linked_at)
			 VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6)
			 ON CONFLICT ON CONSTRAINT city_group_links_pkey
			 DO UPDATE SET bot_id = EXCLUDED.bot_id, language = EXCLUDED.language,
			               linked_by = EXCLUDED.linked_by, linked_at = EXCLUDED.linked_at`,
			cityID, ch.ChatID, botID, strings.ToLower(ch.Language), ch.Actor, ch.At.UTC()); err != nil {
			return fmt.Errorf("postgres: linking the group: %w", err)
		}
		if err := cityGroupAudit(ctx, tx, "city.link_group", ch); err != nil {
			return err
		}
		out = application.CityGroup{CityID: cityID, CityCode: ch.CityCode, ChatID: ch.ChatID, BotID: botID,
			Language: strings.ToLower(ch.Language), LinkedBy: ch.Actor, LinkedAt: ch.At.UTC()}
		return nil
	})
	return out, err
}

// Unlink removes a group from a city, audited in the same transaction.
func (r *CityGroupRepository) Unlink(ctx context.Context, ch CityGroupChange) error {
	return inTx(ctx, r.q, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM city_group_links l USING cities c
			  WHERE c.id = l.city_id AND c.code = $1 AND l.chat_id = $2`, ch.CityCode, ch.ChatID)
		if err != nil {
			return fmt.Errorf("postgres: unlinking the group: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrCityGroupNone
		}
		return cityGroupAudit(ctx, tx, "city.unlink_group", ch)
	})
}

func cityGroupAudit(ctx context.Context, tx pgx.Tx, action string, ch CityGroupChange) error {
	value, err := json.Marshal(map[string]any{
		"city": ch.CityCode, "chat_id": ch.ChatID, "bot": ch.BotKey, "language": ch.Language,
	})
	if err != nil {
		return fmt.Errorf("postgres: encoding audit value: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		 VALUES ($1, $2, 'city_group_link', NULL, NULL, $3, $4, $5)`,
		ch.Actor, action, value, ch.Reason, ch.At.UTC()); err != nil {
		return fmt.Errorf("postgres: writing audit row: %w", err)
	}
	return nil
}
