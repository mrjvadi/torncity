package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/workers/notification"
)

// store holds the three statements the notifier needs that the postgres
// adapter does not have yet.
//
// TODO: this file moves into internal/infrastructure/postgres as it stands —
// ReachableBotLinks and MarkBotUnreachable onto PlayerRepository, Processed
// onto InboxStore — and main.go then passes those instead. It was written here
// only because that package was being changed by other work at the time. The
// ports it satisfies are declared in internal/workers/notification, so the
// move changes no caller.
type store struct {
	pool  *pgxpool.Pool
	inbox *postgres.InboxStore
}

var (
	_ notification.Links = (*store)(nil)
	_ notification.Inbox = (*store)(nil)
)

func newStore(p *postgres.Pool) *store {
	return &store{pool: p.Raw(), inbox: postgres.NewInboxStore(p)}
}

// selectReachableBotLinks reads through player_bot_links_reachable_idx, the
// partial index that exists for exactly this question. Most recently seen
// first: that is the chat the player is most likely looking at.
const selectReachableBotLinks = `
SELECT player_id::text, bot_id::text, telegram_chat_id, is_reachable
FROM player_bot_links
WHERE player_id = $1::uuid AND is_reachable
ORDER BY last_seen_at DESC`

// ReachableBotLinks returns the player's reachable links, most recently seen
// first.
func (s *store) ReachableBotLinks(ctx context.Context, playerID string) ([]application.BotLink, error) {
	rows, err := s.pool.Query(ctx, selectReachableBotLinks, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading bot links of player %s: %w", playerID, err)
	}
	defer rows.Close()

	var links []application.BotLink
	for rows.Next() {
		var l application.BotLink
		if err := rows.Scan(&l.PlayerID, &l.BotID, &l.TelegramChatID, &l.IsReachable); err != nil {
			return nil, fmt.Errorf("postgres: reading bot links of player %s: %w", playerID, err)
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading bot links of player %s: %w", playerID, err)
	}
	return links, nil
}

// markBotUnreachable leaves last_seen_at alone: it records when the player
// last spoke to the bot, and a refusal from Telegram is not the player
// speaking. PlayerRepository.LinkBot sets is_reachable back to true the next
// time they do.
const markBotUnreachable = `
UPDATE player_bot_links SET is_reachable = false
WHERE player_id = $1::uuid AND bot_id = $2::uuid`

// MarkBotUnreachable records that the player blocked this bot.
func (s *store) MarkBotUnreachable(ctx context.Context, playerID, botID string) error {
	if _, err := s.pool.Exec(ctx, markBotUnreachable, playerID, botID); err != nil {
		return fmt.Errorf("postgres: marking bot %s unreachable for player %s: %w", botID, playerID, err)
	}
	return nil
}

const selectProcessed = `
SELECT EXISTS (SELECT 1 FROM inbox_messages WHERE message_id = $1 AND consumer = $2)`

// Processed reports whether consumer already recorded messageID.
func (s *store) Processed(ctx context.Context, messageID, consumer string) (bool, error) {
	if messageID == "" || consumer == "" {
		return false, fmt.Errorf("postgres: inbox processed: message id and consumer are both required")
	}
	var done bool
	if err := s.pool.QueryRow(ctx, selectProcessed, messageID, consumer).Scan(&done); err != nil {
		return false, fmt.Errorf("postgres: reading the inbox for message %s and %s: %w", messageID, consumer, err)
	}
	return done, nil
}

// MarkProcessed is the existing InboxStore method.
func (s *store) MarkProcessed(ctx context.Context, messageID, consumer string) (bool, error) {
	return s.inbox.MarkProcessed(ctx, messageID, consumer)
}
