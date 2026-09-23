//go:build integration

// Integration tests for the phase 0 infrastructure.
//
// These are the checks a unit test cannot make, because each one is about a
// guarantee that lives in PostgreSQL, Redis or JetStream rather than in Go: a
// row lock that is genuinely held, an upsert that genuinely serialises two
// writers, a Lua script that genuinely compares before it deletes, a broker
// that genuinely redelivers. A fake would pass all of them while proving
// nothing at all.
//
// They are excluded twice over, and deliberately:
//
//   - the build tag keeps them out of `go test ./...`, so the default test run
//     stays fast and needs no services;
//   - every test still skips when the service it needs is not configured, so
//     `go test -tags=integration ./...` on a laptop with nothing running
//     reports skips rather than a wall of failures.
//
// Required environment:
//
//	INTEGRATION_DSN        postgres://user:pass@host:port/db?sslmode=disable
//	INTEGRATION_REDIS_URL  redis://host:port/0
//	INTEGRATION_NATS_URL   nats://host:port
package tests

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/dedup"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

const (
	envDSN   = "INTEGRATION_DSN"
	envRedis = "INTEGRATION_REDIS_URL"
	envNATS  = "INTEGRATION_NATS_URL"
)

// testTimeout bounds any single operation. A test that hangs on a service is
// less useful than one that fails.
const testTimeout = 30 * time.Second

// testDefaultLanguage is the fallback the player repository is built with
// here. It is fixed rather than read from configs/config.yml so an assertion
// on a written row cannot move when an operator changes
// player.default_language.
const testDefaultLanguage = "fa"

// requirePostgres opens the pool, or skips. The pool is closed by the test's
// cleanup, so a test never has to remember to.
func requirePostgres(t *testing.T) *postgres.Pool {
	t.Helper()

	dsn := os.Getenv(envDSN)
	if dsn == "" {
		t.Skipf("%s is not set; skipping (this test needs a live PostgreSQL)", envDSN)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	pool, err := postgres.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func requireRedis(t *testing.T) *infraredis.Client {
	t.Helper()

	url := os.Getenv(envRedis)
	if url == "" {
		t.Skipf("%s is not set; skipping (this test needs a live Redis)", envRedis)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	client, err := infraredis.New(ctx, url)
	if err != nil {
		t.Fatalf("connecting to Redis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func requireNATS(t *testing.T) *infranats.Conn {
	t.Helper()

	url := os.Getenv(envNATS)
	if url == "" {
		t.Skipf("%s is not set; skipping (this test needs a live NATS with JetStream)", envNATS)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	conn, err := infranats.New(ctx, url)
	if err != nil {
		t.Fatalf("connecting to NATS: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := infranats.EnsureStreams(ctx, conn, infranats.StreamOptions{}); err != nil {
		t.Fatalf("ensuring streams: %v", err)
	}
	return conn
}

// testCtx is a context bounded by testTimeout.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// randomInt64 returns a cryptographically random positive int64 below max.
func randomInt64(t *testing.T, max int64) int64 {
	t.Helper()
	n, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		t.Fatalf("randomness: %v", err)
	}
	return n.Int64()
}

// randomToken returns a random lowercase-letter token.
//
// Letters only, because subjects.Grammar admits [a-z_] and nothing else: a
// digit in a subject token would build a subject the system's own grammar
// rejects, which is not what any of these tests are about.
func randomToken(t *testing.T, n int) string {
	t.Helper()
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[randomInt64(t, int64(len(alphabet)))]
	}
	return string(out)
}

// newTelegramUserID returns a Telegram user id no other test will collide
// with. players.telegram_user_id is globally unique, so a fixed value would
// make two runs of the suite fight each other.
func newTelegramUserID(t *testing.T) int64 {
	t.Helper()
	return 9_000_000_000 + randomInt64(t, 900_000_000)
}

// newUUID returns a version 4 UUID in canonical form, for the tests that
// insert rows directly.
func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("randomness: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// insertPlayer creates a player row and removes it, and everything that
// references it, when the test ends.
func insertPlayer(t *testing.T, pool *postgres.Pool) *application.Player {
	t.Helper()

	ctx := testCtx(t)
	repo := postgres.NewPlayerRepository(pool, testDefaultLanguage)

	p := &application.Player{
		TelegramUserID: newTelegramUserID(t),
		DisplayName:    "integration",
		Language:       "fa",
		Status:         "active",
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("creating the test player: %v", err)
	}

	t.Cleanup(func() { deletePlayer(t, pool, p.ID) })
	return p
}

// deletePlayer removes a player and its dependants. Children first: both
// idempotency_keys and player_bot_links carry a foreign key to players.
func deletePlayer(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	raw := pool.Raw()
	for _, stmt := range []string{
		`DELETE FROM idempotency_keys WHERE player_id = $1::uuid`,
		`DELETE FROM player_bot_links WHERE player_id = $1::uuid`,
		`DELETE FROM players WHERE id = $1::uuid`,
	} {
		if _, err := raw.Exec(ctx, stmt, playerID); err != nil {
			t.Errorf("cleanup %q: %v", stmt, err)
		}
	}
}

// validMeta builds metadata that envelope.Validate and the publisher accept.
func validMeta(t *testing.T) envelope.Metadata {
	t.Helper()
	return envelope.Metadata{
		RequestID:         "req_" + randomToken(t, 24),
		TraceID:           "trc_" + randomToken(t, 24),
		BotID:             newUUID(t),
		GatewayInstanceID: "integration-test",
		TelegramUserID:    newTelegramUserID(t),
		TelegramChatID:    newTelegramUserID(t),
		ChatType:          "private",
		UpdateType:        "message",
		Command:           "player.profile.get",
		Action:            "profile.get",
		Language:          "fa",
		ReceivedAt:        time.Now().UTC(),
		SchemaVersion:     envelope.SchemaVersion,
	}
}

// ---------------------------------------------------------------------------
// 1. the outbox claim
// ---------------------------------------------------------------------------

// TestOutboxClaimIsDisjoint is the test this suite exists for.
//
// OutboxStore.FetchPending claims rows with FOR UPDATE SKIP LOCKED inside an
// UPDATE, and the whole multi-worker story rests on that being true: if the
// locks were not really held, or not really honoured, two publishers would
// claim the same rows and every event would go out twice. No unit test can
// check this — the behaviour belongs to PostgreSQL's lock manager.
//
// It is checked from both sides:
//
//   - honoured: rows locked by a transaction this test holds open are stepped
//     over, not waited on, and not returned;
//   - held: two concurrent claims come back with no id in common.
func TestOutboxClaimIsDisjoint(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	const batch = 150
	const total = batch * 2

	subject := subjects.Event("itest", randomToken(t, 12))
	ids := seedOutbox(t, pool, subject, total)

	store := postgres.NewOutboxStore(pool)

	t.Run("a held lock is skipped, not waited on", func(t *testing.T) {
		// A transaction of this test's own locks the oldest rows and keeps
		// them locked. FOR UPDATE without SKIP LOCKED is used here on
		// purpose: this side must genuinely hold the rows so that the
		// store's side has something real to skip.
		conn, err := pool.Raw().Acquire(ctx)
		if err != nil {
			t.Fatalf("acquiring a connection: %v", err)
		}
		defer conn.Release()

		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("beginning the blocking transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

		rows, err := tx.Query(ctx,
			`SELECT event_id FROM outbox WHERE subject = $1 AND status = 'pending' ORDER BY id LIMIT $2 FOR UPDATE`,
			subject, batch)
		if err != nil {
			t.Fatalf("locking the first batch: %v", err)
		}
		locked := make(map[string]bool, batch)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scanning a locked id: %v", err)
			}
			locked[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("reading locked ids: %v", err)
		}
		if len(locked) != batch {
			t.Fatalf("locked %d rows, want %d", len(locked), batch)
		}

		// The store must now step over every locked row and come back with
		// the next ones. If SKIP LOCKED were missing this call would block
		// until the deferred rollback, and the test would time out.
		claimed, err := store.FetchPending(ctx, batch)
		if err != nil {
			t.Fatalf("FetchPending while rows are locked: %v", err)
		}
		if len(claimed) != batch {
			t.Fatalf("claimed %d rows, want %d", len(claimed), batch)
		}
		for _, ev := range claimed {
			if locked[ev.EventID] {
				t.Fatalf("FetchPending returned event %s which another transaction holds locked", ev.EventID)
			}
			if !ids[ev.EventID] {
				t.Fatalf("FetchPending returned event %s which this test did not insert", ev.EventID)
			}
		}
	})

	t.Run("two concurrent claims are disjoint", func(t *testing.T) {
		// Both goroutines are released from the same barrier and each claims
		// a batch large enough that the two statements overlap in the server.
		// Whichever runs second finds the first one's rows locked and takes
		// the next ones instead, so the two batches cannot intersect.
		var (
			ready sync.WaitGroup
			done  sync.WaitGroup
			start = make(chan struct{})

			batches [2][]postgres.PendingEvent
			errs    [2]error
		)

		ready.Add(2)
		done.Add(2)
		for i := range batches {
			go func(i int) {
				defer done.Done()
				ready.Done()
				<-start
				batches[i], errs[i] = store.FetchPending(ctx, batch)
			}(i)
		}

		ready.Wait()
		close(start)
		done.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("claim %d failed: %v", i, err)
			}
		}

		first := map[string]bool{}
		for _, ev := range batches[0] {
			first[ev.EventID] = true
		}

		overlap := 0
		for _, ev := range batches[1] {
			if first[ev.EventID] {
				overlap++
			}
		}
		if overlap != 0 {
			t.Fatalf("the two concurrent claims share %d event(s); FOR UPDATE SKIP LOCKED is not holding its locks "+
				"(batch sizes %d and %d)", overlap, len(batches[0]), len(batches[1]))
		}
		if len(batches[0]) == 0 || len(batches[1]) == 0 {
			t.Fatalf("one claim came back empty (sizes %d and %d); the two statements did not overlap, "+
				"so this run proved nothing about the locks", len(batches[0]), len(batches[1]))
		}
	})
}

// seedOutbox inserts n pending rows on subject and returns their event ids.
func seedOutbox(t *testing.T, pool *postgres.Pool, subject string, n int) map[string]bool {
	t.Helper()

	ctx := testCtx(t)
	repo := postgres.NewOutboxRepository(pool)

	// A payload with some body to it, so that claiming a batch is a
	// measurable amount of work rather than an instant that two goroutines
	// could step over one another without ever overlapping.
	filler := randomToken(t, 2048)

	ids := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		payload, err := json.Marshal(map[string]any{"n": i, "filler": filler})
		if err != nil {
			t.Fatalf("encoding the seed payload: %v", err)
		}
		eventID := newUUID(t)
		if err := repo.Append(ctx, application.OutboxRecord{
			EventID:  eventID,
			Subject:  subject,
			Metadata: validMeta(t),
			Payload:  payload,
		}); err != nil {
			t.Fatalf("seeding outbox row %d: %v", i, err)
		}
		ids[eventID] = true
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(cleanupCtx, `DELETE FROM outbox WHERE subject = $1`, subject); err != nil {
			t.Errorf("cleaning up outbox rows for %s: %v", subject, err)
		}
	})

	return ids
}

// ---------------------------------------------------------------------------
// 2. player creation under a race
// ---------------------------------------------------------------------------

// TestPlayerCreateIsRaceSafe proves the contract PlayerRepository.Create
// declares: two first-contact requests for the same Telegram user produce one
// player, and both callers leave holding that player's id.
//
// This is the multi-bot promise from ADR 0001 at its most fragile moment — a
// person sending /start to two bots at once — and it is decided by the ON
// CONFLICT DO UPDATE taking a row lock, which only a real database does.
func TestPlayerCreateIsRaceSafe(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	repo := postgres.NewPlayerRepository(pool, testDefaultLanguage)
	telegramUserID := newTelegramUserID(t)

	var (
		ready sync.WaitGroup
		done  sync.WaitGroup
		start = make(chan struct{})

		players [2]*application.Player
		errs    [2]error
	)

	ready.Add(2)
	done.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer done.Done()
			p := &application.Player{
				TelegramUserID: telegramUserID,
				DisplayName:    fmt.Sprintf("caller-%d", i),
				Language:       "fa",
				Status:         "active",
			}
			ready.Done()
			<-start
			errs[i] = repo.Create(ctx, p)
			players[i] = p
		}(i)
	}

	ready.Wait()
	close(start)
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d failed to create the player: %v", i, err)
		}
	}

	t.Cleanup(func() { deletePlayer(t, pool, players[0].ID) })

	if players[0].ID != players[1].ID {
		t.Fatalf("the two callers hold different player ids (%s and %s); one of them lost its identity",
			players[0].ID, players[1].ID)
	}
	if players[0].ID == "" {
		t.Fatal("the surviving player has no id")
	}

	var count int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM players WHERE telegram_user_id = $1`, telegramUserID).Scan(&count); err != nil {
		t.Fatalf("counting players: %v", err)
	}
	if count != 1 {
		t.Fatalf("telegram user %d has %d player rows, want exactly 1", telegramUserID, count)
	}
}

// ---------------------------------------------------------------------------
// 3 and 4. atomic claims
// ---------------------------------------------------------------------------

// TestIdempotencyReserveIsAtomic proves that exactly one of two concurrent
// reservations of the same key wins.
//
// The whole point of the table is that the second copy of a command does not
// execute. That decision is made by the unique index on
// (player_id, idempotency_key) under ON CONFLICT DO NOTHING, and the answer
// comes from RowsAffected. A fake repository would happily report both fresh.
func TestIdempotencyReserveIsAtomic(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	player := insertPlayer(t, pool)
	repo := postgres.NewIdempotencyRepository(pool)

	key := "itest-" + randomToken(t, 24)

	fresh, errs := runConcurrently(t, 2, func(i int) (bool, error) {
		return repo.Reserve(ctx, key, player.ID, "req_"+randomToken(t, 12), "player.profile.get", time.Hour)
	})

	for i, err := range errs {
		if err != nil {
			t.Fatalf("reservation %d failed: %v", i, err)
		}
	}

	winners := 0
	for _, ok := range fresh {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of 2 concurrent reservations reported fresh, want exactly 1", winners)
	}

	// And a later, uncontended reservation of the same key is still refused:
	// the row outlives the race.
	again, err := repo.Reserve(ctx, key, player.ID, "req_"+randomToken(t, 12), "player.profile.get", time.Hour)
	if err != nil {
		t.Fatalf("re-reserving: %v", err)
	}
	if again {
		t.Fatal("a reservation of an already reserved key reported fresh")
	}
}

// TestInboxMarkProcessedIsAtomic proves the same shape for the consumer-side
// inbox: two concurrent deliveries of one message to one consumer, and exactly
// one of them is allowed to do the work.
//
// This is what turns JetStream's at-least-once delivery into once-per-consumer
// processing, so a redelivery cannot pay a player twice.
func TestInboxMarkProcessedIsAtomic(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	store := postgres.NewInboxStore(pool)
	messageID := "req_" + randomToken(t, 24)
	consumer := "itest-" + randomToken(t, 8)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(cleanupCtx,
			`DELETE FROM inbox_messages WHERE message_id = $1`, messageID); err != nil {
			t.Errorf("cleaning up inbox rows: %v", err)
		}
	})

	fresh, errs := runConcurrently(t, 2, func(i int) (bool, error) {
		return store.MarkProcessed(ctx, messageID, consumer)
	})

	for i, err := range errs {
		if err != nil {
			t.Fatalf("claim %d failed: %v", i, err)
		}
	}

	winners := 0
	for _, ok := range fresh {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of 2 concurrent inbox claims reported fresh, want exactly 1", winners)
	}

	// A different consumer must still get its own turn: the key is
	// (message_id, consumer), so one consumer cannot swallow a message on
	// behalf of the others.
	other, err := store.MarkProcessed(ctx, messageID, consumer+"-other")
	if err != nil {
		t.Fatalf("claiming for a second consumer: %v", err)
	}
	if !other {
		t.Fatal("a second consumer was refused a message the first had claimed")
	}
}

// runConcurrently releases n goroutines from one barrier and collects their
// (bool, error) results in order.
func runConcurrently(t *testing.T, n int, fn func(i int) (bool, error)) ([]bool, []error) {
	t.Helper()

	var (
		ready sync.WaitGroup
		done  sync.WaitGroup
		start = make(chan struct{})
	)
	oks := make([]bool, n)
	errs := make([]error, n)

	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			oks[i], errs[i] = fn(i)
		}(i)
	}

	ready.Wait()
	close(start)
	done.Wait()

	return oks, errs
}

// ---------------------------------------------------------------------------
// 5. the player lock
// ---------------------------------------------------------------------------

// TestPlayerLockIsExclusiveAndSafeToRelease covers the ordinary case and then
// the one the Lua script exists for.
//
// The ordinary case is mutual exclusion. The second case is what happens when
// a holder is slow: A's lock expires on its own, B legitimately takes it, and
// A — which has no idea any of that happened — then calls its release. If
// release were a plain DEL, A would delete B's lock and two goroutines would
// be inside the same critical section, which is worse than no lock at all
// because it looks like one. The compare-and-delete script is what makes A's
// release a no-op instead.
func TestPlayerLockIsExclusiveAndSafeToRelease(t *testing.T) {
	client := requireRedis(t)
	ctx := testCtx(t)

	locker := infraredis.NewPlayerLocker(client)
	playerID := newUUID(t)

	t.Run("exclusive while held, free after release", func(t *testing.T) {
		releaseA, err := locker.Lock(ctx, playerID, 30*time.Second)
		if err != nil {
			t.Fatalf("A could not take the lock: %v", err)
		}

		if _, err := locker.Lock(ctx, playerID, 30*time.Second); err == nil {
			t.Fatal("B took a lock A is holding")
		}

		releaseA()

		releaseB, err := locker.Lock(ctx, playerID, 30*time.Second)
		if err != nil {
			t.Fatalf("B could not take the lock after A released it: %v", err)
		}
		releaseB()
	})

	t.Run("a stale release does not free somebody else's lock", func(t *testing.T) {
		const ttl = 500 * time.Millisecond

		releaseA, err := locker.Lock(ctx, playerID, ttl)
		if err != nil {
			t.Fatalf("A could not take the lock: %v", err)
		}

		// A's lock expires by itself. A does not know.
		time.Sleep(ttl + 300*time.Millisecond)

		releaseB, err := locker.Lock(ctx, playerID, 30*time.Second)
		if err != nil {
			t.Fatalf("B could not take the expired lock: %v", err)
		}
		defer releaseB()

		// This is the assertion the whole compare-and-delete exists for.
		releaseA()

		if _, err := locker.Lock(ctx, playerID, 30*time.Second); err == nil {
			t.Fatal("A's stale release deleted B's lock: a third caller was let into the critical section " +
				"while B still believes it holds the lock")
		}
	})
}

// ---------------------------------------------------------------------------
// 6. the bot lease
// ---------------------------------------------------------------------------

// TestBotLeaseRenewAndRelease proves the same ownership check on the lease
// that gives one gateway instance the exclusive right to poll one bot.
//
// The stake is higher here than with an ordinary lock. Two gateways calling
// getUpdates for the same bot do not duplicate updates, they lose them: each
// call acknowledges the other's, and the loss is silent. So a gateway that has
// lost its lease must not be able to renew it, and must not be able to release
// the lease its successor now holds.
func TestBotLeaseRenewAndRelease(t *testing.T) {
	client := requireRedis(t)
	ctx := testCtx(t)

	lease := infraredis.NewBotLease(client)
	botKey := "itest-" + randomToken(t, 10)
	const (
		holderA = "gateway-a"
		holderB = "gateway-b"
		ttl     = 30 * time.Second
	)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_ = lease.Release(cleanupCtx, botKey, holderA)
		_ = lease.Release(cleanupCtx, botKey, holderB)
	})

	acquired, err := lease.Acquire(ctx, botKey, holderA, ttl)
	if err != nil {
		t.Fatalf("A could not acquire: %v", err)
	}
	if !acquired {
		t.Fatal("A was refused a free lease")
	}

	renewed, err := lease.Renew(ctx, botKey, holderA, ttl)
	if err != nil {
		t.Fatalf("A could not renew: %v", err)
	}
	if !renewed {
		t.Fatal("A could not renew a lease it holds")
	}

	// B holds nothing. Neither of its calls may touch A's lease.
	acquired, err = lease.Acquire(ctx, botKey, holderB, ttl)
	if err != nil {
		t.Fatalf("B's acquire errored: %v", err)
	}
	if acquired {
		t.Fatal("B acquired a lease A holds; two gateways would now poll the same bot")
	}

	renewed, err = lease.Renew(ctx, botKey, holderB, ttl)
	if err != nil {
		t.Fatalf("B's renew errored: %v", err)
	}
	if renewed {
		t.Fatal("B renewed a lease it does not hold")
	}

	if err := lease.Release(ctx, botKey, holderB); err != nil {
		// Releasing something you do not hold is not an error: it has to be
		// safe to call on a shutdown path that cannot check first.
		t.Fatalf("B's release errored instead of being a no-op: %v", err)
	}

	renewed, err = lease.Renew(ctx, botKey, holderA, ttl)
	if err != nil {
		t.Fatalf("A could not renew after B's release: %v", err)
	}
	if !renewed {
		t.Fatal("B's release deleted A's lease")
	}

	// A hands the bot back cleanly, and only then may B have it.
	if err := lease.Release(ctx, botKey, holderA); err != nil {
		t.Fatalf("A could not release: %v", err)
	}

	acquired, err = lease.Acquire(ctx, botKey, holderB, ttl)
	if err != nil {
		t.Fatalf("B could not acquire after A released: %v", err)
	}
	if !acquired {
		t.Fatal("B was refused a lease A had released")
	}
}

// ---------------------------------------------------------------------------
// 7. update deduplication
// ---------------------------------------------------------------------------

// TestDedupSuppressesRepeatUpdate proves the edge filter against a real Redis.
//
// Telegram redelivers an update whose offset was not acknowledged, and the
// gateway sees the same update_id again. Without this, the player's /start is
// processed twice and they get two profiles. SETNX is what decides it, and the
// decision has to survive being made from a different process.
func TestDedupSuppressesRepeatUpdate(t *testing.T) {
	client := requireRedis(t)
	ctx := testCtx(t)

	// A short TTL so the test leaves nothing behind for long; the production
	// window is a day.
	filter, err := dedup.New(infraredis.NewDeduplicator(client, time.Minute))
	if err != nil {
		t.Fatalf("building the filter: %v", err)
	}

	botID := newUUID(t)
	updateID := randomInt64(t, 1_000_000_000)

	allowed, err := filter.Allow(ctx, botID, updateID)
	if err != nil {
		t.Fatalf("first Allow: %v", err)
	}
	if !allowed {
		t.Fatal("the first sighting of an update was suppressed")
	}

	allowed, err = filter.Allow(ctx, botID, updateID)
	if err != nil {
		t.Fatalf("second Allow: %v", err)
	}
	if allowed {
		t.Fatal("the same (bot, update) pair was allowed twice; a replay would reach the game core")
	}

	// A different bot seeing the same update id is a different event. Update
	// ids are per bot, so a shared namespace would make one bot silence
	// another's traffic.
	allowed, err = filter.Allow(ctx, newUUID(t), updateID)
	if err != nil {
		t.Fatalf("Allow for a second bot: %v", err)
	}
	if !allowed {
		t.Fatal("one bot's update id suppressed another bot's")
	}
}

// ---------------------------------------------------------------------------
// 8. JetStream redelivery
// ---------------------------------------------------------------------------

// TestJetStreamRedeliversOnNak proves the delivery guarantee the inbox exists
// to complement: a handler that fails does not lose the message, and a handler
// that keeps failing does not retry forever.
//
// Both halves matter. Without redelivery, a transient database error would
// silently drop a player's command. Without MaxDeliver, one undeliverable
// message would be retried until the end of time, and in a work-queue stream
// it would sit at the head of the queue doing it.
func TestJetStreamRedeliversOnNak(t *testing.T) {
	conn := requireNATS(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subject := subjects.Command("itest", randomToken(t, 12))
	durable := "itest-redeliver-" + randomToken(t, 8)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), testTimeout)
		defer cleanupCancel()
		stream, err := conn.JetStream().Stream(cleanupCtx, infranats.CommandStreamName)
		if err != nil {
			return
		}
		_ = stream.DeleteConsumer(cleanupCtx, durable)
		_ = stream.Purge(cleanupCtx, jetstream.WithPurgeSubject(subject))
	})

	var (
		mu         sync.Mutex
		deliveries int
	)

	// A handler that always fails. infranats.Consumer turns that into a NAK
	// with a delay, which is the path under test.
	err := infranats.NewConsumer(conn, infranats.ConsumerOptions{}).Subscribe(ctx, subject, durable, func(context.Context, *envelope.Envelope) error {
		mu.Lock()
		deliveries++
		mu.Unlock()
		return fmt.Errorf("itest: deliberate handler failure")
	})
	if err != nil {
		t.Fatalf("subscribing: %v", err)
	}

	meta := validMeta(t)
	env, err := envelope.New(meta, map[string]any{"probe": true})
	if err != nil {
		t.Fatalf("building the envelope: %v", err)
	}

	publishCtx, publishCancel := context.WithTimeout(context.Background(), testTimeout)
	defer publishCancel()
	if err := infranats.NewPublisher(conn).Publish(publishCtx, subject, env); err != nil {
		t.Fatalf("publishing: %v", err)
	}

	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return deliveries
	}

	// Redelivery is spaced by the consumer's NAK delay and JetStream's own
	// backoff, which widens with each attempt: on a local broker the five
	// deliveries land at roughly 0s, 5s, 14s, 33s and 97s. The budget is
	// well past that last one, because the assertion is about the count, not
	// about the schedule, and a slow box must not turn this into a flake.
	deadline := time.Now().Add(180 * time.Second)
	for count() < infranats.MaxDeliver && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}

	got := count()
	if got < 2 {
		t.Fatalf("the message was delivered %d time(s); a NAKed message was not redelivered", got)
	}
	if got != infranats.MaxDeliver {
		t.Fatalf("the message was delivered %d time(s), want %d (MaxDeliver)", got, infranats.MaxDeliver)
	}

	// And then it stops. A message that exceeded MaxDeliver must not come
	// back; JetStream is done with it.
	time.Sleep(20 * time.Second)
	if after := count(); after != got {
		t.Fatalf("delivery count went from %d to %d after MaxDeliver was reached; retries are unbounded",
			got, after)
	}
}

// ---------------------------------------------------------------------------
// 9. outbox to NATS
// ---------------------------------------------------------------------------

// TestOutboxToNatsRoundTrip walks the publisher's whole cycle over live
// services: a row written by a transaction reaches the broker on the subject
// it named, carrying the deduplication header, and the row ends up marked
// published exactly once.
//
// The publish step is the one cmd/worker runs. It is reproduced here rather
// than imported because cmd/worker is a main package; the parts that carry the
// guarantees — OutboxStore.FetchPending, nats.Publisher.Publish and
// OutboxStore.MarkPublished — are the real ones.
func TestOutboxToNatsRoundTrip(t *testing.T) {
	pool := requirePostgres(t)
	conn := requireNATS(t)
	ctx := testCtx(t)

	subject := subjects.Event("itest", randomToken(t, 12))

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(cleanupCtx, `DELETE FROM outbox WHERE subject = $1`, subject); err != nil {
			t.Errorf("cleaning up outbox rows: %v", err)
		}
		stream, err := conn.JetStream().Stream(cleanupCtx, infranats.EventStreamName)
		if err != nil {
			return
		}
		_ = stream.Purge(cleanupCtx, jetstream.WithPurgeSubject(subject))
	})

	// Subscribe before publishing. A core subscription sees the same message
	// the stream stores, headers included, which is how the deduplication
	// header is asserted without reaching into JetStream internals.
	sub, err := conn.Raw().SubscribeSync(subject)
	if err != nil {
		t.Fatalf("subscribing to %s: %v", subject, err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := conn.Raw().Flush(); err != nil {
		t.Fatalf("flushing the subscription: %v", err)
	}

	meta := validMeta(t)
	eventID := newUUID(t)
	payload := []byte(`{"player_id":"probe","kind":"itest"}`)

	if err := postgres.NewOutboxRepository(pool).Append(ctx, application.OutboxRecord{
		EventID:  eventID,
		Subject:  subject,
		Metadata: meta,
		Payload:  payload,
	}); err != nil {
		t.Fatalf("appending the outbox row: %v", err)
	}

	// --- the worker's publish step -----------------------------------------

	store := postgres.NewOutboxStore(pool)

	pending, err := store.FetchPending(ctx, 100)
	if err != nil {
		t.Fatalf("FetchPending: %v", err)
	}

	var claimed *postgres.PendingEvent
	for i := range pending {
		if pending[i].EventID == eventID {
			claimed = &pending[i]
			break
		}
	}
	if claimed == nil {
		t.Fatalf("the row just written was not returned by FetchPending (%d rows claimed)", len(pending))
	}
	if claimed.Subject != subject {
		t.Fatalf("claimed subject is %q, want %q", claimed.Subject, subject)
	}
	if claimed.Metadata.RequestID != meta.RequestID {
		t.Fatalf("claimed request id is %q, want %q", claimed.Metadata.RequestID, meta.RequestID)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("attempts is %d after one claim, want 1", claimed.Attempts)
	}

	// The claim is an attempts bump and nothing else: the row is still
	// pending, which is what makes a worker that dies right here harmless.
	if status := outboxStatus(t, pool, eventID); status != postgres.StatusPending {
		t.Fatalf("status after claiming is %q, want %q; the claim invented a status transition",
			status, postgres.StatusPending)
	}

	if err := infranats.NewPublisher(conn).Publish(ctx, claimed.Subject, &envelope.Envelope{
		Metadata: claimed.Metadata,
		Payload:  claimed.Payload,
	}); err != nil {
		t.Fatalf("publishing the claimed event: %v", err)
	}

	if err := store.MarkPublished(ctx, []string{eventID}); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}

	// --- what arrived -------------------------------------------------------

	msg, err := sub.NextMsg(testTimeout)
	if err != nil {
		t.Fatalf("no message arrived on %s: %v", subject, err)
	}
	if msg.Subject != subject {
		t.Fatalf("message arrived on %q, want %q", msg.Subject, subject)
	}

	// Nats-Msg-Id is what lets JetStream's duplicate window collapse the
	// republish that an at-least-once outbox will occasionally produce.
	if got := msg.Header.Get(jetstream.MsgIDHeader); got != meta.RequestID {
		t.Fatalf("%s header is %q, want the request id %q", jetstream.MsgIDHeader, got, meta.RequestID)
	}

	var delivered envelope.Envelope
	if err := json.Unmarshal(msg.Data, &delivered); err != nil {
		t.Fatalf("the delivered message is not an envelope: %v", err)
	}
	if delivered.Metadata.TraceID != meta.TraceID {
		t.Fatalf("delivered trace id is %q, want %q", delivered.Metadata.TraceID, meta.TraceID)
	}
	// Compared as JSON, not as bytes: the payload made a round trip through a
	// jsonb column, which is free to reorder keys and normalise whitespace.
	// The contract is the document, not its spelling.
	var sent, back map[string]any
	if err := json.Unmarshal(payload, &sent); err != nil {
		t.Fatalf("decoding the payload that was written: %v", err)
	}
	if err := json.Unmarshal(delivered.Payload, &back); err != nil {
		t.Fatalf("decoding the payload that arrived: %v", err)
	}
	if !reflect.DeepEqual(sent, back) {
		t.Fatalf("delivered payload is %v, want %v", back, sent)
	}

	if status := outboxStatus(t, pool, eventID); status != postgres.StatusPublished {
		t.Fatalf("status after MarkPublished is %q, want %q", status, postgres.StatusPublished)
	}

	// A second claim must find nothing: the row is closed out, so a restarted
	// worker does not republish it.
	again, err := store.FetchPending(ctx, 100)
	if err != nil {
		t.Fatalf("second FetchPending: %v", err)
	}
	for _, ev := range again {
		if ev.EventID == eventID {
			t.Fatal("a published row was claimed again")
		}
	}
}

// outboxStatus reads one row's status.
func outboxStatus(t *testing.T, pool *postgres.Pool, eventID string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	var status string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT status FROM outbox WHERE event_id = $1::uuid`, eventID).Scan(&status); err != nil {
		t.Fatalf("reading the status of outbox event %s: %v", eventID, err)
	}
	return status
}

// Compile-time reminders that these tests speak to the real drivers rather
// than to a shim: if either of these types disappears from the dependency
// graph, this file stops building and somebody looks at why.
var (
	_ *pgxpool.Pool = (*pgxpool.Pool)(nil)
	_ *natsgo.Conn  = (*natsgo.Conn)(nil)
)
