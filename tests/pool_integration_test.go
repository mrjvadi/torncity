//go:build integration

// Integration tests of the connection discipline behind the freeze of the
// live bot: a command holds exactly ONE connection — its unit of work's —
// and every read a handler makes through a repository built over the pool
// (a city, a policy, a player search) runs on that transaction instead of
// waiting for a second connection. Before, a pool of N connections held by N
// transactions each waiting for one more froze every command for good.
package tests

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// tinyPool opens a pool of at most n connections.
func tinyPool(t *testing.T, n int) *postgres.Pool {
	t.Helper()
	dsn := os.Getenv(envDSN)
	if dsn == "" {
		t.Skipf("%s is not set; skipping (this test needs a live PostgreSQL)", envDSN)
	}
	pool, err := postgres.Open(testCtx(t), dsn, postgres.Options{MaxConns: n, IdleInTransactionTimeout: time.Minute})
	if err != nil {
		t.Fatalf("connecting to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// poolHandlers are the profile and «زندگی من» over a pool, built the way
// cmd/game builds them: cities and policies read through pool-level
// repositories.
type poolHandlers struct {
	profile *handlers.ProfileHandler
	life    *handlers.LifeHandler
}

func newPoolHandlers(t *testing.T, w *goodsWorld, pool *postgres.Pool) poolHandlers {
	t.Helper()
	if _, ok := w.registry.Current().Life(); !ok {
		t.Skip("the active content has no life; run `admin content load`")
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	cities := postgres.NewCityRepository(pool)
	scale := gametime.Scale(gameScale)
	return poolHandlers{
		profile: handlers.NewProfileHandler(uow, workIDs{t}, nil, cities, testDefaultLanguage, time.Hour, nil).
			WithWork(w.registry, postgres.NewPolicyReader(pool, nil)).WithHealth(scale),
		life: handlers.NewLifeHandler(uow, workIDs{t}, nil, w.registry, cities, postgres.NewPlayerSearchRepository(pool),
			scale, time.Hour, nil),
	}
}

// profileMeta is a fresh press of a command by p, through bot.
func profileMeta(t *testing.T, w *goodsWorld, p *application.Player, bot, command string) envelope.Metadata {
	t.Helper()
	m := w.meta(t, p, command)
	m.BotID = bot
	return m
}

// TestACommandNeedsOneConnection: with a pool of ONE connection, the profile
// and «زندگی من» — which read cities, policies and prices inside their
// transaction — still complete. Any read that wanted a second connection
// would wait forever here.
func TestACommandNeedsOneConnection(t *testing.T) {
	w := newGoodsWorld(t)
	pool := tinyPool(t, 1)
	h := newPoolHandlers(t, w, pool)
	// The bot first: cleanups run last-in first-out, and the player's link
	// to it must go before it does.
	bot := insertBot(t, w.pool)
	p := w.shopper(t, "bazaar", 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		if _, err := h.profile.Handle(ctx, profileMeta(t, w, p, bot, "player.profile.get")); err != nil {
			t.Fatalf("profile on one connection: %v", err)
		}
		if _, err := h.life.Me(ctx, profileMeta(t, w, p, bot, "life.me")); err != nil {
			t.Fatalf("life on one connection: %v", err)
		}
	}
}

// TestManyCommandsOnATinyPoolNeverHang: many profiles and lives at once, for
// the same players and different ones, on a pool of three connections, all
// finish within a bound.
func TestManyCommandsOnATinyPoolNeverHang(t *testing.T) {
	w := newGoodsWorld(t)
	pool := tinyPool(t, 3)
	h := newPoolHandlers(t, w, pool)
	bot := insertBot(t, w.pool)
	players := []*application.Player{w.shopper(t, "bazaar", 1000), w.shopper(t, "bazaar", 1000),
		w.shopper(t, "bazaar", 1000)}

	// Metadata is made up front: t.Helper-bound helpers are not for
	// goroutines.
	type press struct {
		meta    envelope.Metadata
		profile bool
	}
	var presses []press
	for round := 0; round < 8; round++ {
		for _, p := range players {
			presses = append(presses, press{meta: profileMeta(t, w, p, bot, "player.profile.get"), profile: true},
				press{meta: profileMeta(t, w, p, bot, "life.me")})
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	start := time.Now()
	errs := make(chan error, len(presses))
	var wg sync.WaitGroup
	for _, pr := range presses {
		wg.Add(1)
		go func(pr press) {
			defer wg.Done()
			var err error
			if pr.profile {
				_, err = h.profile.Handle(ctx, pr.meta)
			} else {
				_, err = h.life.Me(ctx, pr.meta)
			}
			errs <- err
		}(pr)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("%d commands on a pool of 3 did not finish in %s: the pool deadlocked", len(presses), time.Since(start))
	}
	close(errs)
	failed := 0
	for err := range errs {
		if err != nil {
			failed++
			if failed <= 3 {
				t.Errorf("a command failed: %v", err)
			}
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d commands failed", failed, len(presses))
	}
	t.Logf("%d commands on a pool of 3 in %s", len(presses), time.Since(start))
}
