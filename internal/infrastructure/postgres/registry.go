package postgres

import (
	"context"
	"fmt"

	"github.com/mrjvadi/torncity/internal/application"
)

// BotRegistry reads the bot fleet from telegram_bots.
type BotRegistry struct {
	q querier
}

var _ application.BotRegistry = (*BotRegistry)(nil)

// NewBotRegistry returns a registry over the pool.
func NewBotRegistry(p *Pool) *BotRegistry { return &BotRegistry{q: p.shared()} }

// selectEnabledBots lists the bots a gateway may actually poll.
//
// Both predicates are needed and they mean different things. `enabled` is the
// operator's switch: a bot turned off for capacity or for a staged rollout.
// `status` is the token's own condition: a revoked token cannot be used at all
// and a paused bot is mid-maintenance. Filtering on only one of them would put
// a gateway on a bot whose token Telegram has already invalidated.
//
// token_secret_ref is selected, the token is not: the column holds the NAME of
// an environment variable (ADR 0002), and the value is resolved at the edge by
// a SecretResolver. Nothing about a token is ever read from this table because
// nothing about a token is ever written to it.
//
// Ordered by bot_key so a fleet-wide log or a lease sweep reads the same way
// every run.
const selectEnabledBots = `
SELECT id, bot_key, telegram_bot_id, username, token_secret_ref, status, gateway_group, enabled, rate_limit
FROM telegram_bots
WHERE enabled AND status = 'active'
ORDER BY bot_key`

// ListEnabled returns every bot a gateway may poll.
func (r *BotRegistry) ListEnabled(ctx context.Context) ([]application.Bot, error) {
	rows, err := r.q.Query(ctx, selectEnabledBots)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing enabled bots: %w", err)
	}
	defer rows.Close()

	var bots []application.Bot
	for rows.Next() {
		var (
			b            application.Bot
			gatewayGroup *string
		)
		if err := rows.Scan(
			&b.ID,
			&b.BotKey,
			&b.TelegramBotID,
			&b.Username,
			&b.TokenSecretRef,
			&b.Status,
			&gatewayGroup,
			&b.Enabled,
			&b.RateLimit,
		); err != nil {
			return nil, fmt.Errorf("postgres: scanning bot row: %w", err)
		}
		// gateway_group is NULL-able: a bot that belongs to no particular
		// gateway group is served by any of them.
		if gatewayGroup != nil {
			b.GatewayGroup = *gatewayGroup
		}
		bots = append(bots, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading bot rows: %w", err)
	}

	return bots, nil
}
