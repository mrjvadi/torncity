//go:build integration

// The consistency harness of client state sync (docs/adr/0034, "Tests"):
// random command sequences against a live PostgreSQL, projected the ways
// production projects them (the event projector, a command's own wait, the
// poke after a Telegram command, a snapshot), with the chaos the ADR names,
// and after every step and at the end:
//
//	snapshot(t) == fold(snapshot(0), updates(0..t))   per kind, per device
//	the log has no holes and no repeats, versions only move forward
//	the snapshot says what the tables say (spot checks per kind)
//
// Devices are the Go model of a client (statesync.ClientState, the rules
// src/state/store.ts follows): one on the socket, one polling with its own
// cursor; the socket drops, publications get lost, a replica dies between
// commit and publish, two replicas race on one player, a device falls so far
// behind it is told to reset.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// syncPub stands in for the realtime server: it hands each publication to
// the devices subscribed to the channel, unless the "replica" dies before
// publishing (dropNext) or the device's socket is down.
type syncPub struct {
	mu       sync.Mutex
	devices  map[string][]*syncDevice
	dropNext bool
	sent     int
	dropped  int
}

func (p *syncPub) Publish(_ context.Context, channel string, data any, _ string) error {
	p.mu.Lock()
	if p.dropNext {
		p.dropNext = false
		p.dropped++
		p.mu.Unlock()
		return fmt.Errorf("the replica died before publishing")
	}
	p.sent++
	devs := append([]*syncDevice(nil), p.devices[channel]...)
	p.mu.Unlock()
	pub := data.(statesync.Publication)
	for _, d := range devs {
		d.deliver(pub)
	}
	return nil
}

// syncDevice is one client: its copy, its cursor, its socket.
type syncDevice struct {
	t        *testing.T
	name     string
	svc      *statesync.Service
	playerID string
	mu       sync.Mutex
	state    *statesync.ClientState
	online   bool
	inbox    []statesync.Publication // delivered while busy; applied by drain
	gaps     int
	resets   int
	pulls    int
}

func (d *syncDevice) deliver(p statesync.Publication) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.online {
		return // the socket is down: the publication is lost for this device
	}
	d.inbox = append(d.inbox, p)
}

// boot is GET /state.
func (d *syncDevice) boot(ctx context.Context) {
	snap, err := d.svc.State(ctx, d.playerID, nil)
	if err != nil {
		d.t.Fatalf("%s: state: %v", d.name, err)
	}
	d.mu.Lock()
	d.state = statesync.NewClientState(snap)
	d.mu.Unlock()
}

// pull is GET /updates?since= until caught up, or a reset and a new boot.
func (d *syncDevice) pull(ctx context.Context) {
	for {
		d.mu.Lock()
		since, epoch := d.state.PTS, d.state.Epoch
		d.mu.Unlock()
		diff, err := d.svc.Updates(ctx, d.playerID, since, epoch, 0)
		if err != nil {
			d.t.Fatalf("%s: updates: %v", d.name, err)
		}
		d.pulls++
		if diff.Reset {
			d.resets++
			d.boot(ctx)
			return
		}
		d.mu.Lock()
		res := d.state.Apply(diff.Updates)
		d.mu.Unlock()
		if res.Gap {
			d.t.Fatalf("%s: a pull from %d returned a hole: %+v", d.name, since, diff.Updates)
		}
		if !diff.More {
			return
		}
	}
}

// drain applies what the socket delivered, by the client's rules: a too-long
// poke or a gap means pull.
func (d *syncDevice) drain(ctx context.Context) {
	d.mu.Lock()
	inbox := d.inbox
	d.inbox = nil
	d.mu.Unlock()
	for _, p := range inbox {
		if p.Type == statesync.PublicationTooLong {
			d.pull(ctx)
			continue
		}
		d.mu.Lock()
		res := d.state.Apply(p.Updates)
		d.mu.Unlock()
		if res.Gap {
			d.gaps++
			d.pull(ctx)
			d.mu.Lock()
			d.state.Apply(p.Updates) // what the pull already had is a duplicate now
			d.mu.Unlock()
		}
	}
}

// reconnect is the socket coming back: subscribe, then pull, then apply.
func (d *syncDevice) reconnect(ctx context.Context) {
	d.mu.Lock()
	d.online = true
	d.mu.Unlock()
	d.pull(ctx)
	d.drain(ctx)
}

func liveJSON(m map[statesync.Key]statesync.HeldEntity) map[string]string {
	out := map[string]string{}
	for k, e := range m {
		var v any
		_ = json.Unmarshal(e.Data, &v)
		raw, _ := json.Marshal(v)
		out[k.Kind+"/"+k.ID] = "v" + strconv.FormatInt(e.V, 10) + " " + string(raw)
	}
	return out
}

// sameState compares a device's copy with the snapshot, kind by kind.
func sameState(t *testing.T, label string, got, want map[statesync.Key]statesync.HeldEntity) {
	t.Helper()
	g, w := liveJSON(got), liveJSON(want)
	keys := map[string]bool{}
	for k := range g {
		keys[k] = true
	}
	for k := range w {
		keys[k] = true
	}
	var bad []string
	for k := range keys {
		if g[k] != w[k] {
			bad = append(bad, fmt.Sprintf("  %s: device %q, snapshot %q", k, g[k], w[k]))
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("%s: the device's copy is not the snapshot:\n%s", label, joinLines(bad))
	}
}

func joinLines(l []string) string {
	out := ""
	for _, s := range l {
		out += s + "\n"
	}
	return out
}

// logInvariants: pts runs min..max with no hole and no repeat, and every
// entity's version only grows along pts.
func logInvariants(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()
	ctx := testCtx(t)
	var n, lo, hi int64
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*), COALESCE(min(pts), 0), COALESCE(max(pts), 0)
  FROM player_updates WHERE player_id = $1::uuid`, playerID).Scan(&n, &lo, &hi); err != nil {
		t.Fatal(err)
	}
	if n > 0 && hi-lo+1 != n {
		t.Fatalf("the log of %s has holes: %d rows over pts %d..%d", playerID, n, lo, hi)
	}
	var bad int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM (
  SELECT v, lag(v) OVER (PARTITION BY kind, entity_id ORDER BY pts) AS prev
    FROM player_updates WHERE player_id = $1::uuid) x WHERE prev IS NOT NULL AND v <= prev`, playerID).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad > 0 {
		t.Fatalf("%d records of %s went back in version", bad, playerID)
	}
}

// foldFromZero folds the whole log into an empty copy: snapshot(0) is
// nothing at pts 0.
func foldFromZero(t *testing.T, svc *statesync.Service, playerID string) *statesync.ClientState {
	t.Helper()
	c := statesync.NewClientState(statesync.Snapshot{Epoch: svc.Cfg.Epoch})
	whole := &statesync.Service{Store: svc.Store, Cfg: svc.Cfg}
	whole.Cfg.ResetThreshold = 0 // the whole log, however long
	for {
		d, err := whole.Updates(testCtx(t), playerID, c.PTS, "", 0)
		if err != nil || d.Reset {
			t.Fatalf("folding from zero: %v %+v", err, d)
		}
		if res := c.Apply(d.Updates); res.Gap {
			t.Fatal("a hole while folding from zero")
		}
		if !d.More {
			return c
		}
	}
}

type syncWorld struct {
	t     *testing.T
	pool  *postgres.Pool
	store *postgres.StateSync
	svc   *statesync.Service
	pub   *syncPub
	bank  *handlers.BankHandler
	a, b  *application.Player
}

func newSyncWorld(t *testing.T) *syncWorld {
	t.Helper()
	pool := requirePostgres(t)
	requireLedger(t, pool)
	var ok bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.player_updates') IS NOT NULL`).Scan(&ok); err != nil || !ok {
		t.Skip("player_updates does not exist; apply migration 0060_player_updates first")
	}
	city := seedCity(t, pool, "state sync")
	a := bankPlayer(t, pool, city.ID)
	b := bankPlayer(t, pool, city.ID)
	t.Cleanup(func() { purgeLedgerFor(t, pool, city.ID) })
	for _, p := range []*application.Player{a, b} {
		id := p.ID
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			for _, stmt := range []string{
				`DELETE FROM player_updates WHERE player_id = $1::uuid`,
				`DELETE FROM entity_versions WHERE player_id = $1::uuid`,
				`DELETE FROM player_update_state WHERE player_id = $1::uuid`,
				`DELETE FROM item_stacks WHERE player_id = $1::uuid`,
				`DELETE FROM player_notifications WHERE player_id = $1::uuid`,
				`DELETE FROM game_actions WHERE actor_id = $1::uuid`,
			} {
				if _, err := pool.Raw().Exec(ctx, stmt, id); err != nil {
					t.Errorf("cleanup %q: %v", stmt, err)
				}
			}
		})
	}
	ctx := testCtx(t)
	ledger := postgres.NewLedgerRepository(pool)
	if _, err := application.GrantStartingCash(ctx, ledger, a.ID, money.FromMinor(1_000_000), "integration-test", time.Now()); err != nil {
		t.Fatal(err)
	}
	stats := postgres.NewStatsRepository(pool)
	for _, id := range []string{a.ID, b.ID} {
		if _, err := stats.EnsureDefaults(ctx, id, application.Stats{PlayerID: id, Level: 1, Health: 100, MaxHealth: 100,
			Energy: 100, MaxEnergy: 100, RegenBPS: 10000, UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	store := postgres.NewStateSync(pool, postgres.StateRules{EnergyRegenAmount: player.EnergyRegenAmount,
		EnergyRegenInterval: player.EnergyRegenInterval, NerveMax: 20, NerveRegenAmount: 1, NerveRegenInterval: 5 * time.Minute,
		NoticesKept: 5})
	store.LockTimeout = 5 * time.Second
	pub := &syncPub{devices: map[string][]*syncDevice{}}
	svc := &statesync.Service{Store: store, Pub: pub, Metrics: statesync.NewMetrics(), Cfg: statesync.Config{
		Enabled: true, Epoch: "1", ResetThreshold: 40, PullLimit: 7, CommandWait: 2 * time.Second,
		PushMaxRecords: 4, PushMaxBytes: 1 << 20, CauseWindow: 200}}
	limits, err := bank.NewLimits(1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewBankHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), seqUUID{t}, nil,
		placedCities{postgres.NewCityRepository(pool)},
		bankStubPolicy{bps: map[string]int64{application.LeverBankWithdrawalFee: 100, application.LeverCardTransferFee: 100}},
		postgres.NewPlayerSearchRepository(pool), limits, time.Hour, nil)
	return &syncWorld{t: t, pool: pool, store: store, svc: svc, pub: pub, bank: h, a: a, b: b}
}

func (w *syncWorld) device(name string, p *application.Player, online bool) *syncDevice {
	d := &syncDevice{t: w.t, name: name, svc: w.svc, playerID: p.ID, online: online}
	w.pub.mu.Lock()
	ch := statesync.PlayerChannel(p.ID)
	w.pub.devices[ch] = append(w.pub.devices[ch], d)
	w.pub.mu.Unlock()
	d.boot(testCtx(w.t))
	return d
}

// exec runs one SQL change as a command would (its own transaction).
func (w *syncWorld) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.pool.Raw().Exec(testCtx(w.t), sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

// command is one random command for player p; it returns a description.
func (w *syncWorld) command(r *rand.Rand, p, other *application.Player) string {
	ctx := testCtx(w.t)
	switch r.IntN(11) {
	case 0:
		amt := strconv.Itoa(1 + r.IntN(500))
		_, _ = w.bank.Deposit(ctx, bankMeta(w.t, p, "bank.deposit"), handlers.BankAmountRequest{Amount: amt})
		return "deposit " + amt
	case 1:
		amt := strconv.Itoa(1 + r.IntN(300))
		_, _ = w.bank.Withdraw(ctx, bankMeta(w.t, p, "bank.withdraw"), handlers.BankAmountRequest{Amount: amt})
		return "withdraw " + amt
	case 2:
		amt := strconv.Itoa(1 + r.IntN(200))
		_, _ = w.bank.PaySend(ctx, bankMeta(w.t, p, "bank.pay.send"),
			handlers.PayRequest{To: other.PublicCode, Amount: amt, Method: "card", Nonce: randomToken(w.t, 8)})
		return "card pay " + amt
	case 3:
		w.exec(`UPDATE player_stats SET energy = GREATEST(energy - $2, 0), updated_at = now() WHERE player_id = $1::uuid`, p.ID, 1+r.IntN(10))
		return "spend energy"
	case 4:
		w.exec(`INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at) VALUES ($1::uuid, $2::uuid, $3, 1, $4, now())
ON CONFLICT (player_id, skill_code) DO UPDATE SET xp = player_skills.xp + EXCLUDED.xp, level = player_skills.level + 1`,
			newUUID(w.t), p.ID, []string{"driving", "trading", "farming"}[r.IntN(3)], 1+r.IntN(50))
		return "train a skill"
	case 5:
		w.exec(`INSERT INTO item_stacks (player_id, item_code, holding, quantity) VALUES ($1::uuid, $2, 'carried', $3)
ON CONFLICT (player_id, item_code, holding) DO UPDATE SET quantity = item_stacks.quantity + EXCLUDED.quantity`,
			p.ID, []string{"bread", "knife", "rope"}[r.IntN(3)], 1+r.IntN(3))
		return "gain an item"
	case 6:
		w.exec(`DELETE FROM item_stacks WHERE player_id = $1::uuid AND item_code = $2`, p.ID, []string{"bread", "knife", "rope"}[r.IntN(3)])
		return "lose an item"
	case 7:
		w.exec(`INSERT INTO player_notifications (id, player_id, category, kind, link_addr, source_message_id, created_at, screen, view)
VALUES ($1::uuid, $2::uuid, 'economy', 'bank.payment_received', '', $3, clock_timestamp(), 'payment_notice', '{"amount": 5}'::jsonb)`,
			newUUID(w.t), p.ID, randomToken(w.t, 10))
		return "a notice"
	case 8:
		w.exec(`UPDATE player_notifications SET read_at = now() WHERE player_id = $1::uuid AND read_at IS NULL`, p.ID)
		return "mark read"
	case 9:
		w.exec(`INSERT INTO game_actions (id, action_type, actor_type, actor_id, status, started_at, finish_at)
VALUES ($1::uuid, 'education', 'player', $2::uuid, 'scheduled', now(), now() + interval '1 hour')`, newUUID(w.t), p.ID)
		return "start a course"
	default:
		w.exec(`UPDATE game_actions SET status = 'completed', completed_at = now() WHERE actor_id = $1::uuid AND status = 'scheduled'`, p.ID)
		return "finish the timed actions"
	}
}

// project triggers a projection the way production does, picked at random.
func (w *syncWorld) project(r *rand.Rand, p *application.Player, other *application.Player) (string, *envelope.Envelope) {
	ctx := testCtx(w.t)
	switch r.IntN(4) {
	case 0: // the event projector: an outbox event naming the actor and the other player
		env := &envelope.Envelope{Metadata: envelope.Metadata{RequestID: newUUID(w.t), EventID: newUUID(w.t), PlayerID: p.ID,
			ReceivedAt: time.Now().UTC()}, Payload: json.RawMessage(`{"payee_id":"` + other.ID + `"}`)}
		if err := w.svc.HandleEvent(ctx, env); err != nil {
			w.t.Fatalf("HandleEvent: %v", err)
		}
		return "event", env
	case 1: // a client command's own wait
		w.svc.ForCommand(ctx, p.ID, newUUID(w.t))
		return "command", nil
	case 2: // the poke after a Telegram command
		w.svc.HandleResponse(ctx, envelope.Metadata{RequestID: newUUID(w.t), PlayerID: p.ID, ChatType: "group"})
		return "poke", nil
	default: // not yet: a later projection catches it up
		return "later", nil
	}
}

// TestStateSyncConsistencyHarness is the harness. STATESYNC_SEED replays one
// run; STATESYNC_STEPS makes it longer.
func TestStateSyncConsistencyHarness(t *testing.T) {
	w := newSyncWorld(t)
	seed := uint64(time.Now().UnixNano())
	if s := os.Getenv("STATESYNC_SEED"); s != "" {
		seed, _ = strconv.ParseUint(s, 10, 64)
	}
	steps := 150
	if s := os.Getenv("STATESYNC_STEPS"); s != "" {
		steps, _ = strconv.Atoi(s)
	}
	t.Logf("seed %d, %d steps", seed, steps)
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	live := w.device("phone (socket)", w.a, true)   // device 1: on the socket
	tablet := w.device("tablet (poll)", w.a, false) // device 2: polls with its own cursor
	other := w.device("b's phone", w.b, true)
	chaos := map[string]int{}

	var lastEvent *envelope.Envelope
	for step := 1; step <= steps; step++ {
		p, q := w.a, w.b
		if r.IntN(4) == 0 {
			p, q = w.b, w.a
		}
		what := w.command(r, p, q)
		how, env := w.project(r, p, q)
		if env != nil {
			lastEvent = env
		}

		switch c := r.IntN(12); {
		case c == 0 && lastEvent != nil:
			// the same outbox event delivered again: nothing new is appended
			// for it, and what it appended is re-sent with the same pts
			chaos["redelivery"]++
			before := w.maxPTS(lastEvent.Metadata.PlayerID)
			if err := w.svc.HandleEvent(ctx, lastEvent); err != nil {
				t.Fatal(err)
			}
			if again := w.maxPTS(lastEvent.Metadata.PlayerID); again != before {
				// allowed only when the state moved since: then the records are
				// the new change's, never a second copy of the old ones
				w.noDuplicateCause(lastEvent)
			}
		case c == 1:
			// the replica dies after the commit, before the publish
			chaos["crash before publish"]++
			w.pub.mu.Lock()
			w.pub.dropNext = true
			w.pub.mu.Unlock()
			w.command(r, w.a, w.b)
			_, _ = w.svc.Project(ctx, w.a.ID, statesync.EventSource(newUUID(t)), "", nil)
		case c == 2:
			// two replicas race on one player
			chaos["replica race"]++
			w.command(r, w.a, w.b)
			w.command(r, w.a, w.b)
			var wg sync.WaitGroup
			for range 3 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := w.svc.Project(context.Background(), w.a.ID, statesync.EventSource(newUUID(t)), "", nil); err != nil {
						t.Errorf("a racing projection failed: %v", err)
					}
				}()
			}
			wg.Wait()
		case c == 3:
			// the socket drops; changes happen while it is down; it comes back
			chaos["socket drop"]++
			live.mu.Lock()
			live.online = false
			live.mu.Unlock()
			for range 1 + r.IntN(3) {
				w.command(r, w.a, w.b)
				_, _ = w.svc.Project(ctx, w.a.ID, statesync.EventSource(newUUID(t)), "", nil)
			}
			live.reconnect(ctx)
		}

		live.drain(ctx)
		other.drain(ctx)
		if r.IntN(10) == 0 {
			tablet.pull(ctx) // the polling device, now and then
		}
		_ = what
		_ = how

		if step%25 == 0 || step == steps {
			w.checkpoint(fmt.Sprintf("step %d", step), live, tablet, other)
		}
	}

	// the reset path: a device so far behind it may not replay
	t.Run("reset", func(t *testing.T) {
		stale := statesync.NewClientState(statesync.Snapshot{PTS: 0, Epoch: "1"})
		far := &syncDevice{t: t, name: "stale", svc: w.svc, playerID: w.a.ID, state: stale}
		if w.maxPTS(w.a.ID) <= int64(w.svc.Cfg.ResetThreshold) {
			for range w.svc.Cfg.ResetThreshold + 1 {
				w.exec(`UPDATE player_stats SET energy = energy % 50 + 1 WHERE player_id = $1::uuid`, w.a.ID)
				_, _ = w.svc.Project(ctx, w.a.ID, statesync.EventSource(newUUID(t)), "", nil)
			}
		}
		far.pull(ctx)
		if far.resets != 1 {
			t.Fatalf("a device %d records behind was not reset", w.maxPTS(w.a.ID))
		}
		snap, _ := w.svc.State(ctx, w.a.ID, nil)
		sameState(t, "after reset", far.state.Live(), statesync.SnapshotLive(snap))
		d, _ := w.svc.Updates(ctx, w.a.ID, 0, "other-epoch", 0)
		if !d.Reset || d.Reason != statesync.ResetEpoch {
			t.Fatalf("a client of another epoch was not reset: %+v", d)
		}
	})
	live.drain(ctx)
	other.drain(ctx)
	tablet.pull(ctx)
	w.checkpoint("end", live, tablet, other)
	w.truth(w.a)
	w.truth(w.b)

	t.Logf("chaos: %v; device gaps phone=%d tablet=%d; resets tablet=%d; publications sent=%d dropped=%d",
		chaos, live.gaps, tablet.gaps, tablet.resets, w.pub.sent, w.pub.dropped)
	t.Logf("metrics: %v", w.svc.Metrics.Snapshot())
}

func (w *syncWorld) maxPTS(playerID string) int64 {
	var n int64
	_ = w.pool.Raw().QueryRow(testCtx(w.t), `SELECT COALESCE(max(pts), 0) FROM player_updates WHERE player_id = $1::uuid`, playerID).Scan(&n)
	return n
}

func (w *syncWorld) noDuplicateCause(env *envelope.Envelope) {
	var dup int
	_ = w.pool.Raw().QueryRow(testCtx(w.t), `SELECT count(*) - count(DISTINCT cause_key) FROM player_updates
 WHERE player_id = $1::uuid`, env.Metadata.PlayerID).Scan(&dup)
	if dup != 0 {
		w.t.Fatalf("a redelivered event appended a second copy (%d)", dup)
	}
}

// checkpoint projects both players (so the snapshot is current), then holds
// every device to it and the log to its invariants.
func (w *syncWorld) checkpoint(label string, live, tablet, other *syncDevice) {
	w.t.Helper()
	ctx := testCtx(w.t)
	for _, d := range []*syncDevice{live, tablet, other} {
		snap, err := w.svc.State(ctx, d.playerID, nil) // projects first, then reads
		if err != nil {
			w.t.Fatal(err)
		}
		d.drain(ctx)
		d.pull(ctx)
		sameState(w.t, label+" "+d.name, d.state.Live(), statesync.SnapshotLive(snap))
		if d.state.PTS != snap.PTS {
			w.t.Fatalf("%s %s: device at pts %d, snapshot at %d", label, d.name, d.state.PTS, snap.PTS)
		}
		// snapshot(t) == fold(snapshot(0), updates(0..t)), from the very start
		sameState(w.t, label+" fold from zero", foldFromZero(w.t, w.svc, d.playerID).Live(), statesync.SnapshotLive(snap))
		logInvariants(w.t, w.pool, d.playerID)
		// projecting again finds nothing: the held state is the current state
		p, err := w.svc.Project(ctx, d.playerID, statesync.RefreshSource(newUUID(w.t)), "", nil)
		if err != nil || len(p.Records) != 0 {
			w.t.Fatalf("%s: a second projection appended %d records (err %v)", label, len(p.Records), err)
		}
	}
}

// truth spot-checks the snapshot against the tables.
func (w *syncWorld) truth(p *application.Player) {
	w.t.Helper()
	ctx := testCtx(w.t)
	snap, err := w.svc.State(ctx, p.ID, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	ledger := postgres.NewLedgerRepository(w.pool)
	cash, _ := ledger.AccountFor(ctx, application.AccountPlayerCash, p.ID)
	bankAcct, _ := ledger.AccountFor(ctx, application.AccountPlayerBank, p.ID)
	var wallet statesync.WalletData
	if e, ok := snap.Entities[statesync.KindWallet][cash.Currency]; ok {
		_ = json.Unmarshal(e.D, &wallet)
	}
	if wallet.Cash != cash.Balance.Minor() || wallet.Bank != bankAcct.Balance.Minor() {
		w.t.Fatalf("wallet %+v, ledger cash %d bank %d", wallet, cash.Balance.Minor(), bankAcct.Balance.Minor())
	}
	stats, _ := postgres.NewStatsRepository(w.pool).Get(ctx, p.ID)
	var vit statesync.VitalsData
	_ = json.Unmarshal(snap.Entities[statesync.KindVitals][p.ID].D, &vit)
	if vit.Energy.Value != stats.Energy || vit.Energy.Max != stats.MaxEnergy {
		w.t.Fatalf("vitals %+v, stats energy %d/%d", vit.Energy, stats.Energy, stats.MaxEnergy)
	}
	var items int
	_ = w.pool.Raw().QueryRow(ctx, `SELECT count(DISTINCT item_code) FROM item_stacks WHERE player_id = $1::uuid`, p.ID).Scan(&items)
	if len(snap.Entities[statesync.KindInventory]) != items {
		w.t.Fatalf("inventory holds %d items, the table %d", len(snap.Entities[statesync.KindInventory]), items)
	}
	var unread int
	_ = w.pool.Raw().QueryRow(ctx, `SELECT count(*) FROM player_notifications WHERE player_id = $1::uuid AND read_at IS NULL`, p.ID).Scan(&unread)
	var inbox statesync.InboxData
	_ = json.Unmarshal(snap.Entities[statesync.KindInbox][statesync.SelfID].D, &inbox)
	if inbox.Unread != unread || len(snap.Entities[statesync.KindNotice]) != len(inbox.Latest) {
		w.t.Fatalf("inbox %+v with %d notices, table unread %d", inbox, len(snap.Entities[statesync.KindNotice]), unread)
	}
}

// TestStateSyncPTSUnderConcurrency hammers one player from many goroutines,
// each changing the state and projecting it: the log must come out as
// 1..N with every number once.
func TestStateSyncPTSUnderConcurrency(t *testing.T) {
	w := newSyncWorld(t)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 15 {
				if _, err := w.pool.Raw().Exec(context.Background(),
					`UPDATE player_stats SET energy = $2 WHERE player_id = $1::uuid`, w.a.ID, (g*31+i)%100+1); err != nil {
					t.Error(err)
					return
				}
				if _, err := w.svc.Project(context.Background(), w.a.ID, statesync.EventSource(newUUID(t)), "", nil); err != nil {
					t.Errorf("projection: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	logInvariants(t, w.pool, w.a.ID)
	var n, hi int64
	_ = w.pool.Raw().QueryRow(testCtx(t), `SELECT count(*), max(pts) FROM player_updates WHERE player_id = $1::uuid`, w.a.ID).Scan(&n, &hi)
	if n != hi {
		t.Fatalf("%d rows but max pts %d", n, hi)
	}
}

// TestStateSyncProjectionRefusesATransaction: a projection inside a command's
// unit of work would read uncommitted state and hold a second connection.
func TestStateSyncProjectionRefusesATransaction(t *testing.T) {
	w := newSyncWorld(t)
	uow := postgres.NewUnitOfWork(w.pool, testDefaultLanguage)
	err := uow.Do(testCtx(t), func(ctx context.Context, _ application.Tx) error {
		_, err := w.store.Project(ctx, statesync.ProjectRequest{PlayerID: w.a.ID, Source: "x"})
		return err
	})
	if err == nil {
		t.Fatal("a projection ran inside a unit of work")
	}
}

// TestStateSyncTrimKeepsTheTail trims a log and checks the floor, the
// reset below it, and that numbering continues after it.
func TestStateSyncTrimKeepsTheTail(t *testing.T) {
	w := newSyncWorld(t)
	ctx := testCtx(t)
	for i := range 12 {
		w.exec(`UPDATE player_stats SET energy = $2 WHERE player_id = $1::uuid`, w.a.ID, i+1)
		if _, err := w.svc.Project(ctx, w.a.ID, statesync.EventSource(newUUID(t)), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	w.exec(`UPDATE player_updates SET at = at - interval '30 days' WHERE player_id = $1::uuid`, w.a.ID)
	hi := w.maxPTS(w.a.ID)
	var res statesync.TrimResult
	cursor := ""
	for range 1000 {
		var err error
		res, err = w.store.Trim(ctx, statesync.TrimPolicy{Age: 24 * time.Hour, Records: 4, Min: 2, Batch: 50}, cursor, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !res.Ran {
			t.Fatal("the trim did not run (lock held?)")
		}
		cursor = res.Cursor
		if cursor == "" {
			break
		}
	}
	var lo, n int64
	_ = w.pool.Raw().QueryRow(ctx, `SELECT min(pts), count(*) FROM player_updates WHERE player_id = $1::uuid`, w.a.ID).Scan(&lo, &n)
	if n != 4 || lo != hi-3 {
		t.Fatalf("after the trim: %d records from %d (max %d)", n, lo, hi)
	}
	if d, _ := w.svc.Updates(ctx, w.a.ID, lo-2, "", 0); !d.Reset || d.Reason != statesync.ResetTooLong {
		t.Fatalf("a cursor below the floor = %+v", d)
	}
	if d, _ := w.svc.Updates(ctx, w.a.ID, lo-1, "", 0); d.Reset || len(d.Updates) != 4 {
		t.Fatalf("the floor = %+v", d)
	}
	w.exec(`UPDATE player_stats SET energy = 77 WHERE player_id = $1::uuid`, w.a.ID)
	p, _ := w.svc.Project(ctx, w.a.ID, statesync.EventSource(newUUID(t)), "", nil)
	if len(p.Records) != 1 || p.Records[0].PTS != hi+1 {
		t.Fatalf("numbering after the trim: %+v (max was %d)", p.Records, hi)
	}
}
