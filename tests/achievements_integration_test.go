//go:build integration

// Integration test of achievements (migration 0026,
// docs/adr/0024-property-and-politics.md): the consumer counts a player's
// shifts, awards "first day at work" at the first one exactly once — however
// often the event is delivered — pays its cash inside the player's daily cap
// and withholds the rest, and counts nothing twice.
package tests

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

func TestAchievementAwardedOnce(t *testing.T) {
	w := newFWorld(t, "brennhaven")
	ctx := testCtx(t)
	def, ok := w.snap.AchievementDef("first_shift")
	if !ok || def.Reward < 2 {
		t.Skip("the active content has no paying first_shift achievement; run `admin content load`")
	}
	worker := w.resident(0)
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM achievement_events WHERE player_id = $1::uuid`,
			`DELETE FROM achievement_progress WHERE player_id = $1::uuid`,
			`DELETE FROM player_achievements WHERE player_id = $1::uuid`,
			`DELETE FROM outbox WHERE subject LIKE 'game.event.achievement.%' AND payload->>'player_id' = $1`,
		} {
			if _, err := w.pool.Raw().Exec(context.Background(), stmt, worker.ID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	// A cap below the reward: part is paid, the rest withheld.
	cap := def.Reward / 2
	ach := handlers.NewAchievementsHandler(w.uow, nil, w.registry, handlers.AchievementRules{PlayerDailyCap: cap,
		EconomyDailyCap: 1_000_000_000}, w.now)
	subject := subjects.Event("job", "shift_worked")
	shift := func(requestID string) *envelope.Envelope {
		m := validMeta(t)
		m.RequestID, m.EventID = requestID, requestID
		payload, _ := json.Marshal(map[string]any{"player_id": worker.ID, "career": "cashier"})
		return &envelope.Envelope{Metadata: m, Payload: payload}
	}
	first := shift("evt-" + randomToken(t, 12))
	for range 3 {
		if err := ach.OnEvent(ctx, first, subject); err != nil {
			t.Fatalf("OnEvent: %v", err)
		}
	}
	if n := w.count(`SELECT count(*) FROM player_achievements WHERE player_id = $1::uuid AND code = 'first_shift'`, worker.ID); n != 1 {
		t.Fatalf("first_shift awarded %d times, want once", n)
	}
	var progress int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT count FROM achievement_progress WHERE player_id = $1::uuid AND code = 'hard_worker'`,
		worker.ID).Scan(&progress); err != nil {
		t.Fatal(err)
	}
	if progress != 1 {
		t.Fatalf("a shift delivered three times counted %d toward hard_worker, want 1", progress)
	}
	if got := cashBalance(t, w.pool, application.AccountPlayerCash, worker.ID); got != cap {
		t.Fatalf("the reward paid %d, want the cap %d", got, cap)
	}
	var withheld int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT withheld FROM player_achievements WHERE player_id = $1::uuid AND code = 'first_shift'`,
		worker.ID).Scan(&withheld); err != nil || withheld != def.Reward-cap {
		t.Fatalf("withheld %d (%v), want %d", withheld, err, def.Reward-cap)
	}
	// A second shift counts on, and awards first_shift no more.
	if err := ach.OnEvent(ctx, shift("evt-"+randomToken(t, 12)), subject); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM player_achievements WHERE player_id = $1::uuid`, worker.ID); n != 1 {
		t.Fatalf("%d achievements after a second shift, want 1", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox WHERE subject LIKE 'game.event.achievement.awarded.%' AND payload->>'player_id' = $1`,
		worker.ID); n != 1 {
		t.Fatalf("%d award notices, want 1", n)
	}
	if f := w.verify(); f.AchievementLedger == 0 {
		t.Fatalf("the invariants saw no achievement cash: %+v", f)
	}
}
