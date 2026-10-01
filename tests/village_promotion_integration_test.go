//go:build integration

// Integration test of tier promotion (docs/adr/0028 section 4.1): the way
// forward shows progress and no button while a goal is unmet; when every goal
// is met the head promotes once, and the tier, the offices, the event and the
// audit row all follow; a repeat or a race changes nothing.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func TestVillagePromotion(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool

	meta, head := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID, jurisdictionID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, jurisdiction_id::text FROM cities WHERE founded_by_group_id = $1`,
		meta.TelegramChatID).Scan(&cityID, &jurisdictionID); err != nil {
		t.Fatal(err)
	}

	village := handlers.NewVillageHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), workIDs{t}, nil,
		staticContentSource{snap: loadTestContent(t)}, e.cache, postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now)

	as := func(p *application.Player, command, action string) envelope.Metadata {
		m := asPlayer(meta, p)
		m.Command, m.Action = command, action
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	keyboard := func(r *presenter.Response) string {
		b, _ := json.Marshal(r.Keyboard)
		return string(b)
	}
	overview := func(p *application.Player) *presenter.Response {
		m := as(p, "settlement.overview", "overview")
		r, err := village.Overview(ctx, m)
		if err != nil {
			t.Fatalf("overview: %v", err)
		}
		return renderedIn(t, m, r)
	}
	promote := func(p *application.Player, confirm string) *presenter.Response {
		m := as(p, "settlement.promote", "promote")
		r, err := village.Promote(ctx, m, handlers.VillagePromoteRequest{Confirm: confirm})
		if err != nil {
			t.Fatalf("promote: %v", err)
		}
		return renderedIn(t, m, r)
	}
	tierOf := func() (tier, kind string) {
		if err := pool.Raw().QueryRow(ctx,
			`SELECT c.tier, j.kind FROM cities c JOIN jurisdictions j ON j.id = c.jurisdiction_id WHERE c.id = $1::uuid`, cityID).Scan(&tier, &kind); err != nil {
			t.Fatal(err)
		}
		return tier, kind
	}
	events := func() int {
		return e.count(t, `SELECT count(*) FROM outbox WHERE payload->>'settlement_id' = $1 AND subject LIKE '%promoted%'`, cityID)
	}
	holder := func(office string) (id, by string) {
		var h, a *string
		if err := pool.Raw().QueryRow(ctx,
			`SELECT holder_player_id::text, acquired_by FROM offices WHERE office_code = $1 AND jurisdiction_id = $2::uuid AND seat = 1`,
			office, jurisdictionID).Scan(&h, &a); err != nil {
			t.Fatalf("seat %s: %v", office, err)
		}
		if h != nil {
			id = *h
		}
		if a != nil {
			by = *a
		}
		return id, by
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Raw().Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	// The structured view is what a game client receives, so it is read
	// through the client's own path.
	promotionOf := func(pl *application.Player) map[string]any {
		m := clientMeta(as(pl, "settlement.overview", "overview"), "settlement.overview", "overview")
		r, err := village.Overview(ctx, m)
		if err != nil {
			t.Fatalf("client overview: %v", err)
		}
		p, _ := viewOf(t, r)["promotion"].(map[string]any)
		return p
	}
	current := func(p map[string]any, kind, role string) float64 {
		for _, c := range p["criteria"].([]any) {
			m := c.(map[string]any)
			if m["kind"] == kind && (role == "" || m["role"] == role) {
				return m["current"].(float64)
			}
		}
		t.Fatalf("no %s/%s goal in %v", kind, role, p["criteria"])
		return 0
	}

	// 1. A young village: the way to a town, with progress, and no button.
	// Nothing about the city beyond is shown.
	r := overview(head)
	p := promotionOf(head)
	if p == nil || p["to"] != "town" || p["met"] != false || p["can_promote"] != true {
		t.Fatalf("the way forward of a young village: %v", p)
	}
	if current(p, "residents", "") != 1 || current(p, "role", "food") != 0 {
		t.Errorf("progress of a young village: %v", p["criteria"])
	}
	if strings.Contains(keyboard(r), "settlement:promote\"") || strings.Contains(fmt.Sprint(p), "city") {
		t.Errorf("an unmet village shows the act or the city: %s", keyboard(r))
	}
	if !strings.Contains(keyboard(r), "settlement:promotion.view") {
		t.Errorf("no way-forward button on the overview: %s", keyboard(r))
	}

	// 2. Asking to promote with goals unmet shows the goals: nothing moves.
	if r := promote(head, screens.VillagePromoteConfirm); !strings.Contains(r.Text, "village.promotion.title") {
		t.Errorf("an unmet promotion did not show the way forward: %s", r.Text)
	}
	if tier, _ := tierOf(); tier != "village" || events() != 0 {
		t.Fatalf("an unmet promotion moved the village to %s (%d events)", tier, events())
	}

	// 3. Meet the goals one kind at a time; the progress follows.
	var residents []*application.Player
	for i := 0; i < 7; i++ {
		rp := insertPlayer(t, pool)
		exec(`UPDATE players SET residence_city_id = $1::uuid, residence_since = now() WHERE id = $2::uuid`, cityID, rp.ID)
		residents = append(residents, rp)
	}
	if p := promotionOf(head); current(p, "residents", "") != 8 || p["met"] != false {
		t.Errorf("after the residents came: %v", p)
	}
	exec(`INSERT INTO settlement_literacy (settlement_id, literacy_share_bps, updated_at) VALUES ($1::uuid, 2500, now())
	      ON CONFLICT (settlement_id) DO UPDATE SET literacy_share_bps = 2500`, cityID)
	for i, code := range []string{"farm_canal", "teaching_circle", "health_house", "granary"} {
		exec(`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		      VALUES (gen_random_uuid(), $1::uuid, $2, $3, 20, 'complete', now(), now())`, cityID, code, i*3)
	}
	// A demolished or unfinished building does not count.
	exec(`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at) VALUES (gen_random_uuid(), $1::uuid, 'market', 30, 30, 'building', now())`, cityID)
	// The founding kit's civic hall, market and granary, and the four above
	// (roads do not count).
	if p := promotionOf(head); current(p, "buildings", "") != 7 {
		t.Errorf("buildings counted wrong: %v", p["criteria"])
	}
	for _, code := range []string{"carpentry", "basic_literacy"} {
		exec(`INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, $2, 'researched', now())`, cityID, code)
	}
	// Not yet: the treasury is empty.
	if p := promotionOf(head); p["met"] != false || current(p, "treasury", "") != 0 {
		t.Fatalf("an empty treasury still promotes: %v", p)
	}
	err := postgres.NewUnitOfWork(pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, err := application.GrantSettlementTreasury(ctx, tx, cityID, 5_000, newUUID(t), application.SettlementGrantFounding, "system:founding", time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// 4. All met: the head is offered the act, a member is not.
	r = overview(head)
	if p := promotionOf(head); p["met"] != true {
		t.Fatalf("every goal is met, the way forward says not: %v", p)
	}
	if !strings.Contains(keyboard(r), "settlement:promote\"") {
		t.Errorf("no promotion button for the head: %s", keyboard(r))
	}
	if kb := keyboard(overview(residents[0])); strings.Contains(kb, "settlement:promote\"") {
		t.Errorf("a plain resident was offered the promotion: %s", kb)
	}
	if r := promote(residents[0], screens.VillagePromoteConfirm); !strings.Contains(r.Text, "village.refusal.not_office_holder") {
		t.Errorf("a resident promoted the village: %s", r.Text)
	}

	// 5. The head's first press asks; nothing moves.
	if r := promote(head, ""); !strings.Contains(r.Text, "village.promotion.ask_title") {
		t.Errorf("the promotion did not ask first: %s", r.Text)
	}
	if tier, _ := tierOf(); tier != "village" {
		t.Fatalf("asking promoted the village to %s", tier)
	}

	// 6. Two heads' presses racing under different keys promote once.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := village.Promote(ctx, as(head, "settlement.promote", "promote"), handlers.VillagePromoteRequest{Confirm: screens.VillagePromoteConfirm}); err != nil {
				t.Errorf("racing promote: %v", err)
			}
		}()
	}
	wg.Wait()
	if tier, kind := tierOf(); tier != "town" || kind != "town" {
		t.Fatalf("after promotion the tier is %s and the jurisdiction %s", tier, kind)
	}
	if n := events(); n != 1 {
		t.Fatalf("%d promoted events, want exactly 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'settlement.promote' AND target_id = $1::uuid`, cityID); n != 1 {
		t.Errorf("%d audit rows, want 1", n)
	}

	// 7. The offices changed: the sitting head succeeds into the town head.
	if id, _ := holder("village_head"); id != "" {
		t.Errorf("the village head seat is still held by %s", id)
	}
	if id, by := holder("town_head"); id != head.ID || by != "founding" {
		t.Errorf("town_head is held by %q (%s), want the sitting head by founding", id, by)
	}

	// 8. A repeat changes nothing. The head still heads the town: the way
	// forward is now the city step, unmet.
	if r := promote(head, screens.VillagePromoteConfirm); !strings.Contains(r.Text, "village.promotion.title") {
		t.Errorf("a repeat did not show the next way forward: %s", r.Text)
	}
	if tier, _ := tierOf(); tier != "town" || events() != 1 {
		t.Errorf("a repeat changed things: tier %s, %d events", tier, events())
	}
	if p := promotionOf(head); p == nil || p["to"] != "city" || p["from"] != "town" || p["met"] != false || p["can_promote"] != true {
		t.Errorf("the way forward of a town: %v", p)
	}

	// 9. A game client asks for the same way forward.
	cm := clientMeta(as(head, "settlement.promotion.view", "promotion.view"), "settlement.promotion.view", "promotion.view")
	cr, err := village.PromotionView(ctx, cm)
	if err != nil {
		t.Fatal(err)
	}
	if v := viewOf(t, cr); v["to"] != "city" || v["can_promote"] != true {
		t.Errorf("client view: %v", v)
	}
}
