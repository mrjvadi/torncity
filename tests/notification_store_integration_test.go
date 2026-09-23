//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/workers/notification"
)

// The notifier's reads and writes against a real schema: the partial index
// predicate, the ordering, the flag LinkBot restores, and the inbox read that
// never claims. The worker's ports are satisfied by the repositories
// themselves, so cmd/notifier wires no adapter of its own.
var (
	_ notification.Links = (*postgres.PlayerRepository)(nil)
	_ notification.Inbox = (*postgres.InboxStore)(nil)
)

// insertBot creates a telegram_bots row a gateway will never poll (paused and
// disabled), for links to point at, and removes it when the test ends. It
// must be registered before the player so its cleanup runs after the
// player's has removed the links that reference it.
func insertBot(t *testing.T, pool *postgres.Pool) string {
	t.Helper()

	id := newUUID(t)
	tgID := newTelegramUserID(t)
	now := time.Now().UTC()
	if _, err := pool.Raw().Exec(testCtx(t), `
INSERT INTO telegram_bots (id, bot_key, telegram_bot_id, username, token_secret_ref, status, enabled, rate_limit, created_at, updated_at)
VALUES ($1::uuid, $2, $3, $4, $5, 'paused', false, 1, $6, $6)`,
		id, "itest_"+randomToken(t, 12), tgID, "itest_"+randomToken(t, 8)+"_bot", "TORN_ITEST_UNUSED_TOKEN", now); err != nil {
		t.Fatalf("inserting a test bot: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(ctx, `DELETE FROM telegram_bots WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("cleaning up test bot %s: %v", id, err)
		}
	})
	return id
}

func TestNotificationBotLinks(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	older, newer := insertBot(t, pool), insertBot(t, pool)
	player := insertPlayer(t, pool)
	repo := postgres.NewPlayerRepository(pool, testDefaultLanguage)

	link := func(botID string, chat int64) {
		t.Helper()
		if err := repo.LinkBot(ctx, application.BotLink{
			PlayerID: player.ID, BotID: botID, TelegramChatID: chat, IsReachable: true,
		}); err != nil {
			t.Fatalf("linking bot %s: %v", botID, err)
		}
	}
	reachable := func() []string {
		t.Helper()
		links, err := repo.ReachableBotLinks(ctx, player.ID)
		if err != nil {
			t.Fatalf("ReachableBotLinks: %v", err)
		}
		var bots []string
		for _, l := range links {
			if l.PlayerID != player.ID || !l.IsReachable || l.TelegramChatID == 0 {
				t.Errorf("link %+v is not a reachable link of the player", l)
			}
			bots = append(bots, l.BotID)
		}
		return bots
	}

	if got := reachable(); len(got) != 0 {
		t.Fatalf("a player with no links has reachable links %v", got)
	}

	link(older, 1001)
	// last_seen_at is a timestamp; make sure the two links cannot share one.
	time.Sleep(5 * time.Millisecond)
	link(newer, 2002)

	if got := reachable(); len(got) != 2 || got[0] != newer || got[1] != older {
		t.Fatalf("reachable = %v, want the most recently seen first: [%s %s]", got, newer, older)
	}

	if err := repo.MarkBotUnreachable(ctx, player.ID, newer); err != nil {
		t.Fatalf("MarkBotUnreachable: %v", err)
	}
	if got := reachable(); len(got) != 1 || got[0] != older {
		t.Fatalf("after blocking %s, reachable = %v, want only %s", newer, got, older)
	}
	// Marking it again, or a link that does not exist, is not an error.
	if err := repo.MarkBotUnreachable(ctx, player.ID, newer); err != nil {
		t.Fatalf("MarkBotUnreachable a second time: %v", err)
	}
	if err := repo.MarkBotUnreachable(ctx, player.ID, newUUID(t)); err != nil {
		t.Fatalf("MarkBotUnreachable on no link: %v", err)
	}

	// The player speaking to the bot again is what makes it reachable.
	link(newer, 2002)
	if got := reachable(); len(got) != 2 || got[0] != newer {
		t.Fatalf("after the player came back, reachable = %v, want %s first", got, newer)
	}
}

func TestInboxProcessedReadsWithoutClaiming(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	store := postgres.NewInboxStore(pool)
	messageID := "req_" + randomToken(t, 24)
	consumer := "itest-" + randomToken(t, 8)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(cleanupCtx, `DELETE FROM inbox_messages WHERE message_id = $1`, messageID); err != nil {
			t.Errorf("cleaning up inbox rows: %v", err)
		}
	})

	for i := 0; i < 2; i++ {
		done, err := store.Processed(ctx, messageID, consumer)
		if err != nil {
			t.Fatalf("Processed: %v", err)
		}
		if done {
			t.Fatalf("read %d: a message nobody recorded is reported processed", i+1)
		}
	}

	// Reading twice claimed nothing, so the first claim is still fresh.
	fresh, err := store.MarkProcessed(ctx, messageID, consumer)
	if err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if !fresh {
		t.Fatal("Processed claimed the message: the claim after it was not fresh")
	}

	done, err := store.Processed(ctx, messageID, consumer)
	if err != nil {
		t.Fatalf("Processed after the claim: %v", err)
	}
	if !done {
		t.Fatal("a recorded message is not reported processed")
	}
	// The key is (message_id, consumer): another consumer has not seen it.
	if other, err := store.Processed(ctx, messageID, consumer+"-other"); err != nil || other {
		t.Fatalf("Processed for another consumer = %v, %v; want false", other, err)
	}
}
