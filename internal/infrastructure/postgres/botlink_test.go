package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
)

const testBotID = "22222222-2222-4222-8222-222222222222"

// Notification routing asks one question of player_bot_links, and the partial
// index player_bot_links_reachable_idx exists to answer it: the predicate must
// be the index's own, or the planner cannot use it. Most recently seen first
// is the routing rule; the id tie-break keeps the order stable across retries.
func TestSelectReachableBotLinksUsesThePartialIndexInRoutingOrder(t *testing.T) {
	sql := normalize(selectReachableBotLinks)

	if !strings.Contains(sql, "FROM player_bot_links WHERE player_id = $1::uuid AND is_reachable") {
		t.Errorf("reachable links are not read through the partial index predicate:\n%s", sql)
	}
	if !strings.HasSuffix(sql, "ORDER BY last_seen_at DESC, id") {
		t.Errorf("reachable links are not most recently seen first with a stable tie-break:\n%s", sql)
	}
	if !strings.Contains(sql, "SELECT player_id::text, bot_id::text, telegram_chat_id, is_reachable") {
		t.Errorf("reachable links do not select the columns ReachableBotLinks scans:\n%s", sql)
	}
}

// Retiring a link touches is_reachable on one (player, bot) row and nothing
// else. last_seen_at in particular records the player speaking, and Telegram
// refusing a message is not that.
func TestMarkBotUnreachableTouchesOneFlagOfOneLink(t *testing.T) {
	sql := normalize(markBotUnreachable)

	if sql != "UPDATE player_bot_links SET is_reachable = false WHERE player_id = $1::uuid AND bot_id = $2::uuid" {
		t.Errorf("unexpected unreachable update:\n%s", sql)
	}
	if strings.Contains(sql, "last_seen_at") {
		t.Errorf("marking a link unreachable rewrites last_seen_at:\n%s", sql)
	}
}

// The link LinkBot writes and the one ReachableBotLinks reads are the same
// row: the upsert must set is_reachable, or a player who blocked a bot and
// came back would never be routed to it again.
func TestLinkBotRestoresReachability(t *testing.T) {
	if !strings.Contains(normalize(upsertBotLink), "is_reachable = EXCLUDED.is_reachable") {
		t.Errorf("LinkBot does not refresh is_reachable, so MarkBotUnreachable would be permanent")
	}
}

func TestReachableBotLinksScansEveryRowInOrder(t *testing.T) {
	rows := &fakeRows{vals: [][]any{
		{testPlayerID, testBotID, int64(2002), true},
		{testPlayerID, "33333333-3333-4333-8333-333333333333", int64(1001), true},
	}}
	q := &fakeQuerier{rows: rows}
	repo := &PlayerRepository{q: q}

	links, err := repo.ReachableBotLinks(context.Background(), testPlayerID)
	if err != nil {
		t.Fatalf("ReachableBotLinks: %v", err)
	}
	want := []application.BotLink{
		{PlayerID: testPlayerID, BotID: testBotID, TelegramChatID: 2002, IsReachable: true},
		{PlayerID: testPlayerID, BotID: "33333333-3333-4333-8333-333333333333", TelegramChatID: 1001, IsReachable: true},
	}
	if len(links) != len(want) {
		t.Fatalf("got %d links, want %d", len(links), len(want))
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, links[i], want[i])
		}
	}
	if got := q.last(); got.sql != selectReachableBotLinks || len(got.args) != 1 || got.args[0] != testPlayerID {
		t.Errorf("ReachableBotLinks sent %q with %v", got.sql, got.args)
	}
	if !rows.closed {
		t.Error("the result set was not closed")
	}
}

// No links is an empty answer, not an error: the worker then has nobody to
// tell and acknowledges the event.
func TestReachableBotLinksWithNoneIsEmpty(t *testing.T) {
	links, err := (&PlayerRepository{q: &fakeQuerier{}}).ReachableBotLinks(context.Background(), testPlayerID)
	if err != nil {
		t.Fatalf("ReachableBotLinks: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("got %d links, want none", len(links))
	}
}

// Every failure keeps its cause, so the worker's log says what went wrong and
// the event is retried rather than treated as "nobody to tell".
func TestReachableBotLinksReportsFailures(t *testing.T) {
	boom := errors.New("boom")
	for name, q := range map[string]*fakeQuerier{
		"query": {queryErr: boom},
		"scan":  {rows: &fakeRows{vals: [][]any{{testPlayerID}}, scanErr: boom}},
		"rows":  {rows: &fakeRows{err: boom}},
	} {
		t.Run(name, func(t *testing.T) {
			links, err := (&PlayerRepository{q: q}).ReachableBotLinks(context.Background(), testPlayerID)
			if !errors.Is(err, boom) {
				t.Fatalf("error = %v, want one wrapping the driver's", err)
			}
			if links != nil {
				t.Errorf("a failed read returned links %v", links)
			}
		})
	}
}

func TestMarkBotUnreachableSendsBothKeys(t *testing.T) {
	q := &fakeQuerier{execTag: okTag()}
	if err := (&PlayerRepository{q: q}).MarkBotUnreachable(context.Background(), testPlayerID, testBotID); err != nil {
		t.Fatalf("MarkBotUnreachable: %v", err)
	}
	got := q.last()
	if got.sql != markBotUnreachable || len(got.args) != 2 || got.args[0] != testPlayerID || got.args[1] != testBotID {
		t.Errorf("MarkBotUnreachable sent %q with %v", got.sql, got.args)
	}
}

// A link that is already gone is not an error: there is nothing to retire.
func TestMarkBotUnreachableOnNoLinkIsNotAnError(t *testing.T) {
	q := &fakeQuerier{execTag: noRowsTag()}
	if err := (&PlayerRepository{q: q}).MarkBotUnreachable(context.Background(), testPlayerID, testBotID); err != nil {
		t.Errorf("MarkBotUnreachable on a missing link: %v", err)
	}
}

func TestMarkBotUnreachableReportsFailures(t *testing.T) {
	boom := errors.New("boom")
	err := (&PlayerRepository{q: &fakeQuerier{execErr: boom}}).MarkBotUnreachable(context.Background(), testPlayerID, testBotID)
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want one wrapping the driver's", err)
	}
}
