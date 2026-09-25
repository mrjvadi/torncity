//go:build integration

// Integration test of missions (migration 0023,
// docs/adr/0023-health-missions-factions.md): a mission moved on by the
// game's own events, through the real handlers, the outbox and the unit of
// work.
//
//	A player buys bandages at the city's pharmacy, walks to the police
//	station and takes "first aid training" (use two medicines). They use
//	two bandages; the two inventory.used events the outbox holds are
//	delivered to the missions' consumer — each twice, as a broker may.
//	The first moves the mission once, its duplicate nothing; the second
//	completes it: the cash reward paid once as a mission grant, held
//	inside the player's daily cap, the reward's goods given, and one
//	completion event.
package tests

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

func TestMissionMovedByEventsOnceAndPaidInsideCap(t *testing.T) {
	w := newGoodsWorld(t)
	requireStageE(t, w.pool)
	ctx := testCtx(t)
	snap := w.registry.Current()
	def, ok := snap.MissionDef("first_aid_course")
	if !ok || def.Reward.Cash <= 0 {
		t.Skip("the active content has no first aid mission; run `admin content load`")
	}
	scale := gametime.Scale(gameScale)
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, w.clock)
	bag := handlers.NewInventoryHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, scale, crimeRules().Nerve, 10, time.Hour, w.clock)
	// The player's daily cap is below the mission's reward: the rest is
	// withheld, never paid later.
	cap := def.Reward.Cash - 50
	missions := handlers.NewMissionsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, scale,
		handlers.MissionRules{MaxActive: 3, PlayerDailyCap: cap, EconomyDailyCap: 1_000_000_000}, time.Hour, w.clock)

	p := w.shopper(t, "hospital", 5_000)
	t.Cleanup(func() { purgeStageE(t, w.pool, p.ID) })
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE player_stats SET health = 40 WHERE player_id = $1::uuid`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := shops.Buy(ctx, w.meta(t, p, "shop.buy"),
		handlers.ShopRequest{Shop: "pharmacy", Item: "bandage", Qty: "2", Method: "cash", Nonce: w.nonce(t)}); err != nil {
		t.Fatalf("buy bandages: %v", err)
	}
	if got := w.held(t, p.ID, "bandage", application.HoldCarried); got != 2 {
		t.Fatalf("bandages carried = %d, want 2", got)
	}

	// Taken at its board, the police station.
	resp, err := missions.Accept(ctx, w.meta(t, p, "mission.accept"), handlers.MissionRequest{Mission: def.Code})
	if err != nil {
		t.Fatalf("accept away from the board: %v", err)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM mission_assignments WHERE player_id = $1::uuid`, p.ID); n != 0 {
		t.Fatalf("a mission was taken away from its board (%s)", resp.Text)
	}
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'police_station', place_since = $2 WHERE id = $1::uuid`,
		p.ID, w.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := missions.Accept(ctx, w.meta(t, p, "mission.accept"), handlers.MissionRequest{Mission: def.Code}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	var assignmentID string
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text FROM mission_assignments WHERE player_id = $1::uuid AND status = 'active'`,
		p.ID).Scan(&assignmentID); err != nil {
		t.Fatalf("the mission was not taken: %v", err)
	}

	// Two bandages used, the second after the medicine cooldown.
	for i := range 2 {
		if i > 0 {
			w.now = w.now.Add(time.Hour)
		}
		if _, err := bag.Use(ctx, w.meta(t, p, "inventory.use"), handlers.ItemRequest{Item: "bandage", Nonce: w.nonce(t)}); err != nil {
			t.Fatalf("use bandage #%d: %v", i+1, err)
		}
	}
	type outboxEvent struct {
		id       string
		subject  string
		metadata envelope.Metadata
		payload  json.RawMessage
	}
	var used []outboxEvent
	rows, err := w.pool.Raw().Query(ctx, `SELECT event_id::text, subject, metadata, payload FROM outbox
	  WHERE subject = $1 AND payload->>'player_id' = $2 ORDER BY id`, subjects.Event("inventory", "used"), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var (
			e    outboxEvent
			meta []byte
		)
		if err := rows.Scan(&e.id, &e.subject, &meta, &e.payload); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(meta, &e.metadata); err != nil {
			t.Fatal(err)
		}
		e.metadata.EventID = e.id
		used = append(used, e)
	}
	rows.Close()
	if len(used) != 2 {
		t.Fatalf("inventory.used events = %d, want 2", len(used))
	}
	t.Cleanup(func() {
		for _, e := range used {
			_, _ = w.pool.Raw().Exec(testCtx(t), `DELETE FROM outbox WHERE event_id = $1::uuid`, e.id)
		}
	})
	progress := func() int64 {
		var n int64
		if err := w.pool.Raw().QueryRow(ctx, `SELECT progress[1] FROM mission_assignments WHERE id = $1::uuid`, assignmentID).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	deliver := func(e outboxEvent) {
		t.Helper()
		env := &envelope.Envelope{Metadata: e.metadata, Payload: e.payload}
		if err := missions.OnEvent(ctx, env, e.subject); err != nil {
			t.Fatalf("OnEvent: %v", err)
		}
	}
	cash := w.purse(t, application.AccountPlayerCash, p.ID)
	deliver(used[0])
	deliver(used[0])
	if got := progress(); got != 1 {
		t.Fatalf("after one event and its duplicate the progress is %d, want 1", got)
	}
	deliver(used[1])
	deliver(used[1])
	var (
		status string
		paid   int64
	)
	if err := w.pool.Raw().QueryRow(ctx, `SELECT status, reward_cash FROM mission_assignments WHERE id = $1::uuid`, assignmentID).
		Scan(&status, &paid); err != nil {
		t.Fatal(err)
	}
	if status != application.MissionCompleted || paid != cap {
		t.Fatalf("the mission is %s and paid %d; want completed and %d (the cap)", status, paid, cap)
	}
	if got := w.purse(t, application.AccountPlayerCash, p.ID) - cash; got != cap {
		t.Fatalf("the player got %d, want %d once", got, cap)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM reward_grants WHERE player_id = $1::uuid AND source = 'mission'
	  AND amount = $2`, p.ID, cap); n != 1 {
		t.Fatalf("mission grants = %d, want 1", n)
	}
	for _, it := range def.Reward.Items {
		if got := w.held(t, p.ID, it.Item, application.HoldCarried); got != int64(it.Qty) {
			t.Errorf("the reward's %s: %d carried, want %d", it.Item, got, it.Qty)
		}
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'player_id' = $2`,
		subjects.Event("mission", "completed"), p.ID); n != 1 {
		t.Errorf("mission.completed events = %d, want 1", n)
	}
	// A completed mission taken once is not on offer again.
	if _, err := missions.Accept(ctx, w.meta(t, p, "mission.accept"), handlers.MissionRequest{Mission: def.Code}); err != nil {
		t.Fatalf("accept again: %v", err)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM mission_assignments WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Errorf("a one-time mission was taken %d times", n)
	}
	verifyLedger(t, w.pool)
}
