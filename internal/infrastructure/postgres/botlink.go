package postgres

import (
	"context"
	"fmt"

	"github.com/mrjvadi/torncity/internal/application"
)

// The two statements below are the notification side of player_bot_links.
// LinkBot (player.go) writes a link when the player speaks to a bot; these
// read the links a notice may travel through and retire one Telegram refuses.

// selectReachableBotLinks reads through player_bot_links_reachable_idx, the
// partial index that exists for exactly this question. Most recently seen
// first: that is the chat the player is most likely looking at. id breaks a
// tie, so two links seen in the same instant are tried in the same order on
// every attempt rather than in whatever order the planner returns them.
const selectReachableBotLinks = `
SELECT player_id::text, bot_id::text, telegram_chat_id, is_reachable
FROM player_bot_links
WHERE player_id = $1::uuid AND is_reachable
ORDER BY last_seen_at DESC, id`

// ReachableBotLinks returns the player's links that are still reachable, most
// recently seen first. A player with none gets an empty result, not an error:
// there is nobody to tell, which is a normal outcome.
func (r *PlayerRepository) ReachableBotLinks(ctx context.Context, playerID string) ([]application.BotLink, error) {
	rows, err := r.q.Query(ctx, selectReachableBotLinks, playerID)
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
// speaking. LinkBot sets is_reachable back to true the next time they do.
const markBotUnreachable = `
UPDATE player_bot_links SET is_reachable = false
WHERE player_id = $1::uuid AND bot_id = $2::uuid`

// MarkBotUnreachable records that the player can no longer be reached through
// this bot: they blocked it, or the chat is gone. A link that does not exist
// is not an error; there is nothing to retire.
func (r *PlayerRepository) MarkBotUnreachable(ctx context.Context, playerID, botID string) error {
	if _, err := r.q.Exec(ctx, markBotUnreachable, playerID, botID); err != nil {
		return fmt.Errorf("postgres: marking bot %s unreachable for player %s: %w", botID, playerID, err)
	}
	return nil
}
