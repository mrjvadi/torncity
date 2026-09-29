package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/application"
)

// NewsQueue is the shared queue behind the village news
// (internal/workers/notification/village_news.go): per village, a sorted set
// of the things that happened, oldest first; a set of the villages that have
// anything waiting; a min-gap lock per village; and a dedup marker per event.
//
// All coordination is in the scripts below, so any number of notifier
// replicas can push and claim at once and a batch is taken exactly once.
type NewsQueue struct {
	client *Client
	// SeenTTL is how long an event id is remembered after it was queued, so
	// a redelivery long after the post went out is still recognised.
	seenTTL time.Duration
}

var _ application.NewsQueue = (*NewsQueue)(nil)

// NewNewsQueue returns the queue. seenTTL should outlast the event stream's
// redelivery horizon.
func NewNewsQueue(c *Client, seenTTL time.Duration) *NewsQueue {
	return &NewsQueue{client: c, seenTTL: seenTTL}
}

const (
	newsItemsPrefix = "village:news:items:"
	newsSeenPrefix  = "village:news:seen:"
	newsLastPrefix  = "village:news:last:"
	newsPendingKey  = "village:news:pending"
)

// tag keeps one village's keys in one hash slot.
func newsTag(settlementID string) string { return "{" + settlementID + "}" }

// push adds a member once. KEYS: seen marker, items. ARGV: seen ttl seconds,
// score, member. Returns 1 when queued, 0 for a repeat.
var pushScript = goredis.NewScript(`
if redis.call('SET', KEYS[1], '1', 'NX', 'EX', ARGV[1]) == false then return 0 end
redis.call('ZADD', KEYS[2], ARGV[2], ARGV[3])
return 1
`)

// Push queues the item once.
func (q *NewsQueue) Push(ctx context.Context, settlementID string, item application.QueuedNews) error {
	member, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("redis: encoding village news: %w", err)
	}
	tag := newsTag(settlementID)
	_, err = pushScript.Run(ctx, q.client.Raw(),
		[]string{newsSeenPrefix + tag + ":" + item.EventID, newsItemsPrefix + tag},
		int64(q.seenTTL/time.Second), item.At.UnixMilli(), string(member)).Result()
	if err != nil {
		return fmt.Errorf("redis: queueing village news: %w", err)
	}
	if err := q.client.Raw().SAdd(ctx, newsPendingKey, settlementID).Err(); err != nil {
		return fmt.Errorf("redis: queueing village news: %w", err)
	}
	return nil
}

// claim takes one village's whole queue if it is due. KEYS: items, last.
// ARGV: now ms, window ms, gap seconds. Returns {"empty"}, {"wait"} or
// {"ok", member...}.
var claimScript = goredis.NewScript(`
local first = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
if #first == 0 then return {'empty'} end
if tonumber(ARGV[1]) - tonumber(first[2]) < tonumber(ARGV[2]) then return {'wait'} end
if redis.call('SET', KEYS[2], '1', 'NX', 'EX', ARGV[3]) == false then return {'wait'} end
local items = redis.call('ZRANGE', KEYS[1], 0, -1)
redis.call('DEL', KEYS[1])
local out = {'ok'}
for _, m in ipairs(items) do out[#out + 1] = m end
return out
`)

// ClaimDue takes the due villages' queues.
func (q *NewsQueue) ClaimDue(ctx context.Context, now time.Time, window, gap time.Duration, max int) ([]application.NewsBatch, error) {
	ids, err := q.client.Raw().SMembers(ctx, newsPendingKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: listing village news: %w", err)
	}
	var out []application.NewsBatch
	for _, id := range ids {
		if len(out) >= max {
			break
		}
		tag := newsTag(id)
		res, err := claimScript.Run(ctx, q.client.Raw(),
			[]string{newsItemsPrefix + tag, newsLastPrefix + tag},
			now.UnixMilli(), window.Milliseconds(), int64(gap/time.Second)).StringSlice()
		if err != nil {
			return out, fmt.Errorf("redis: taking village news: %w", err)
		}
		switch {
		case len(res) == 0, res[0] == "wait":
			continue
		case res[0] == "empty":
			// Nothing queued any more (a batch was taken since the id was
			// listed): the village leaves the pending set until its next
			// item. A push racing this removal re-adds it (Push adds to the
			// set after queueing).
			_ = q.client.Raw().SRem(ctx, newsPendingKey, id).Err()
			continue
		}
		batch := application.NewsBatch{SettlementID: id}
		for _, m := range res[1:] {
			var item application.QueuedNews
			if err := json.Unmarshal([]byte(m), &item); err == nil {
				batch.Items = append(batch.Items, item)
			}
		}
		out = append(out, batch)
	}
	return out, nil
}

// Requeue puts a batch back and releases the village's gap.
func (q *NewsQueue) Requeue(ctx context.Context, b application.NewsBatch) error {
	tag := newsTag(b.SettlementID)
	pipe := q.client.Raw().TxPipeline()
	for _, item := range b.Items {
		member, err := json.Marshal(item)
		if err != nil {
			continue
		}
		pipe.ZAdd(ctx, newsItemsPrefix+tag, goredis.Z{Score: float64(item.At.UnixMilli()), Member: string(member)})
	}
	pipe.Del(ctx, newsLastPrefix+tag)
	pipe.SAdd(ctx, newsPendingKey, b.SettlementID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: putting village news back: %w", err)
	}
	return nil
}
