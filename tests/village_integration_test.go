//go:build integration

// Integration test of K2/W5 end to end (docs/adr/0031-knowledge-and-
// village-progression.md; docs/adr/0028-world-and-settlements.md section
// 6): found a village, its founding grant appears, research a knowledge
// item to completion, buy one from Support at the scarcity price, build a
// building to completion, and let the literacy tick raise literacy once a
// school stands. Every guarantee here lives in PostgreSQL — the unique
// partial indexes that make research and construction idempotent and
// race-safe (migrations 0045, 0046), and the real transactional wiring
// between VillageHandler and the postgres repositories — the same reason
// TestSettlementFounding exists for founding itself.
package tests

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// testClock (military_integration_test.go) already lets a test move the
// game clock forward without really waiting, exactly the seam production
// code provides via now func() time.Time; reused verbatim here.

// seedTreasury grants a settlement's own treasury starting money, the same
// shape GrantStartingCash gives a new player, so the test's own research,
// purchase and construction have something to spend without touching a
// live economy.
func seedTreasury(t *testing.T, pool *postgres.Pool, cityID string, amount int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, cityID)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
			Reason:    application.ReasonAdminGrant,
			CreatedAt: time.Now().UTC(),
			Entries: []application.LedgerEntry{
				{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-amount)},
				{AccountID: acct.ID, Amount: money.FromMinor(amount)},
			},
		})
		return err
	})
	if err != nil {
		t.Fatalf("seeding the settlement's treasury: %v", err)
	}
}

// ownedKnowledge reads a settlement's own settlement_knowledge_owned codes.
func ownedKnowledge(t *testing.T, pool *postgres.Pool, settlementID string) map[string]bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	rows, err := pool.Raw().Query(ctx, `SELECT code FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`, settlementID)
	if err != nil {
		t.Fatalf("reading owned knowledge: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatalf("scanning owned knowledge: %v", err)
		}
		out[code] = true
	}
	return out
}

func TestVillageLifecycle(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	for _, table := range []string{"settlement_knowledge_owned", "settlement_knowledge_holder_counts"} {
		var exists bool
		if err := pool.Raw().QueryRow(ctx, `SELECT to_regclass('public.`+table+`') IS NOT NULL`).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Skipf("%s does not exist; apply migrations 0045-0046 first", table)
		}
	}

	worlds := postgres.NewWorldRepository(pool)
	if _, err := worlds.Active(ctx); err == nil {
		t.Skip("a world is already active; this test only runs against a database with none (it cannot safely replace a live world)")
	}

	pack, err := content.LoadWorldGen("../configs/content")
	if err != nil {
		t.Fatalf("LoadWorldGen: %v", err)
	}
	wgContent, err := pack.ToContent()
	if err != nil {
		t.Fatalf("ToContent: %v", err)
	}
	params := worldgen.DefaultParams()
	params.CellCount = 4000

	w, err := worlds.Create(ctx, application.World{
		Seed: 20280928002, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: "integration-test-village", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("creating the test world: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(ctx, `DELETE FROM worlds WHERE id = $1::uuid`, w.ID); err != nil {
			t.Errorf("cleaning up the test world: %v", err)
		}
	})

	worldCache := application.NewWorldCache(worlds, params, wgContent)
	bot := insertBot(t, pool)
	founder := insertPlayer(t, pool)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	snap := loadTestContent(t)
	source := staticContentSource{snap: snap}
	cities := postgres.NewCityRepository(pool)

	clk := &testClock{now: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}

	settlements := handlers.NewSettlementsHandler(uow, workIDs{t}, nil, worldCache, source, gametime.Scale(1),
		wsettle.Params{MinSpawnDistanceKm: 30, ThreatRadiusKm: 150, SearchMaxCells: 2000, SearchMaxAttempts: 50,
			ExcludedBiomes: []string{"polar_ice"}, MaxAbsLatitudeDeg: 70},
		168*time.Hour, 5, time.Second, testFoundingConfig(), clk.Now)

	village := handlers.NewVillageHandler(uow, workIDs{t}, nil, source, worldCache, cities, gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
		}, time.Hour, clk.Now)

	groupChatID := -newTelegramUserID(t)

	newMeta := func(command, action string) envelope.Metadata {
		m := validMeta(t)
		m.BotID = bot
		m.TelegramUserID = founder.TelegramUserID
		m.TelegramChatID = groupChatID
		m.ChatType = "group"
		m.Command, m.Action = command, action
		return m
	}

	var cityID string
	t.Cleanup(func() {
		if cityID == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		var jurisdictionID string
		_ = pool.Raw().QueryRow(ctx, `SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, cityID).Scan(&jurisdictionID)
		// ledger_entries/accounts, and org_stacks/item_movements the same
		// way, are deliberately left alone: both are append-only by design
		// (docs/database.md section 6) precisely so an audit trail can
		// never be edited, a test's own small history included — the same
		// reason no other integration test in this package tries to
		// delete one either, and why seedTimber below is a real
		// tx.Items().Move rather than a raw INSERT into org_stacks (a raw
		// insert would leave EconomyAdmin.verifyGoods's own journal check
		// unable to reconcile the stack it never saw arrive).
		// settlement_research is
		// deleted before game_actions, which it references (its own
		// research/build game_actions rows are left as harmless orphans,
		// the same as any completed action's row normally outlives what it
		// finished).
		for _, stmt := range []string{
			`UPDATE players SET residence_city_id = NULL, residence_since = NULL WHERE residence_city_id = $1::uuid`,
			`DELETE FROM outbox WHERE payload->>'to_city_id' = $1 OR payload->>'from_city_id' = $1`,
			`DELETE FROM outbox WHERE payload->>'settlement_id' = $1`,
			`DELETE FROM settlement_research WHERE settlement_id = $1::uuid`,
			`DELETE FROM game_actions WHERE reference_type = 'settlement' AND reference_id = $1::uuid`,
			`DELETE FROM settlement_literacy WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid`,
			`DELETE FROM city_group_links WHERE city_id = $1::uuid`,
			`DELETE FROM village_currency_reservations WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_founding_drafts WHERE settlement_id = $1::uuid OR chat_id IN (SELECT founded_by_group_id FROM cities WHERE id = $1::uuid)`,
		} {
			if _, err := pool.Raw().Exec(ctx, stmt, cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if jurisdictionID != "" {
			if _, err := pool.Raw().Exec(ctx, `DELETE FROM offices WHERE jurisdiction_id = $1::uuid`, jurisdictionID); err != nil {
				t.Errorf("cleaning up offices: %v", err)
			}
		}
		if _, err := pool.Raw().Exec(ctx, `DELETE FROM cities WHERE id = $1::uuid`, cityID); err != nil {
			t.Errorf("cleaning up the founded city: %v", err)
		}
		if jurisdictionID != "" {
			if _, err := pool.Raw().Exec(ctx, `DELETE FROM jurisdictions WHERE id = $1::uuid`, jurisdictionID); err != nil {
				t.Errorf("cleaning up the founded jurisdiction: %v", err)
			}
		}
	})

	// ------------------------------------------------------------------
	// 1. Found.
	// ------------------------------------------------------------------
	foundVillage(t, pool, settlements, newMeta("settlement.found", "found"))
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, groupChatID).Scan(&cityID); err != nil {
		t.Fatalf("reading the founded city: %v", err)
	}

	// The group's «who is around» screen (docs/adr/0030, R2 settlement.who):
	// a group screen, named for the village, listing who is online.
	if os.Getenv(envRedis) != "" {
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid WHERE id = $1::uuid`, founder.ID, cityID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			// The founder lived in the village for this check; let go of it
			// before the village's own cleanup deletes the city.
			_, _ = pool.Raw().Exec(context.Background(), `UPDATE players SET city_id = NULL, residence_city_id = NULL WHERE id = $1::uuid`, founder.ID)
		})
		catalog, err := i18n.Load("../configs/locales")
		if err != nil {
			t.Fatal(err)
		}
		presenceSvc := &application.PresenceService{
			Store: infraredis.NewPresence(requireRedis(t), time.Minute), Repo: postgres.NewPresenceRepository(pool), RosterLimit: 50,
		}
		if err := presenceSvc.Beat(ctx, founder.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		who, err := handlers.NewPresenceHandler(uow, catalog, presenceSvc).Who(ctx, newMeta("settlement.who", "who"))
		if err != nil {
			t.Fatalf("settlement.who: %v", err)
		}
		if who.Screen != "settlement_who" || !strings.Contains(who.Text, "integration") {
			t.Errorf("the who screen does not list the online founder: %q (%s)", who.Text, who.Screen)
		}
		// In a private chat there is no village to speak of.
		private := newMeta("settlement.who", "who")
		private.ChatType, private.TelegramChatID = "private", founder.TelegramUserID
		if refused, err := handlers.NewPresenceHandler(uow, catalog, presenceSvc).Who(ctx, private); err != nil || refused.Screen == "settlement_who" {
			t.Errorf("settlement.who answered outside a group: %v %+v", err, refused)
		}
	}

	// ------------------------------------------------------------------
	// 2. The founding grant appears: the four universal items, plus a
	// terrain-matched one, plus a scheduled literacy tick.
	// ------------------------------------------------------------------
	owned := ownedKnowledge(t, pool, cityID)
	for _, want := range []string{"oral_tradition", "communal_watch", "kin_apprenticeship", "barter_ring"} {
		if !owned[want] {
			t.Errorf("founding grant missing %q; owned = %v", want, owned)
		}
	}
	if len(owned) < 5 {
		t.Errorf("expected a terrain-matched item on top of the four universal ones; owned = %v", owned)
	}

	var literacyBPS int
	var pendingTeachAction *string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT literacy_share_bps, pending_action_id::text FROM settlement_literacy WHERE settlement_id = $1::uuid`,
		cityID).Scan(&literacyBPS, &pendingTeachAction); err != nil {
		t.Fatalf("reading literacy: %v", err)
	}
	if pendingTeachAction == nil || *pendingTeachAction == "" {
		t.Fatal("no literacy tick was scheduled at founding")
	}
	if literacyBPS != 0 {
		t.Errorf("literacy_share_bps = %d at founding, want 0", literacyBPS)
	}

	seedTreasury(t, pool, cityID, 200_000)

	// ------------------------------------------------------------------
	// 3. Research completes: record_keeping (requires only the
	// literacy_spread capability, which oral_tradition already provides).
	// ------------------------------------------------------------------
	if _, err := village.Research(ctx, newMeta("settlement.knowledge.research", "knowledge.research"),
		handlers.VillageKnowledgeRequest{Code: "record_keeping"}); err != nil {
		t.Fatalf("Research: %v", err)
	}
	var researchID, researchActionID string
	var researchFinish time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text, game_action_id::text, finish_at FROM settlement_research WHERE settlement_id = $1::uuid AND status = 'running'`,
		cityID).Scan(&researchID, &researchActionID, &researchFinish); err != nil {
		t.Fatalf("reading the running research: %v", err)
	}
	clk.Advance(researchFinish.Sub(clk.Now()) + time.Second)
	if _, err := village.Researched(ctx, newMeta("settlement.researched", "researched"),
		handlers.CrimeScheduledRequest{ActionID: researchActionID, ReferenceID: researchID}); err != nil {
		t.Fatalf("Researched: %v", err)
	}
	owned = ownedKnowledge(t, pool, cityID)
	if !owned["record_keeping"] {
		t.Errorf("research did not grant record_keeping; owned = %v", owned)
	}
	// Idempotency: a redelivered completion changes nothing and errors on
	// neither call.
	if _, err := village.Researched(ctx, newMeta("settlement.researched", "researched"),
		handlers.CrimeScheduledRequest{ActionID: researchActionID, ReferenceID: researchID}); err != nil {
		t.Errorf("a redelivered Researched should be a no-op, got: %v", err)
	}

	// ------------------------------------------------------------------
	// 4. Buy from Support at the scarcity price.
	// ------------------------------------------------------------------
	if _, err := village.Buy(ctx, newMeta("settlement.knowledge.buy", "knowledge.buy"),
		handlers.VillageKnowledgeRequest{Code: "basic_literacy"}); err != nil {
		t.Fatalf("Buy: %v", err)
	}
	owned = ownedKnowledge(t, pool, cityID)
	if !owned["basic_literacy"] {
		t.Errorf("buying did not grant basic_literacy; owned = %v", owned)
	}

	// ------------------------------------------------------------------
	// 5. Build completes: the civic hall, then a teaching circle (role
	// education) so the literacy tick below has a school to run.
	// ------------------------------------------------------------------
	buildAndComplete := func(code string) {
		t.Helper()
		lotsResp, err := village.Lots(ctx, newMeta("settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: code})
		if err != nil {
			t.Fatalf("Lots(%s): %v", code, err)
		}
		x, y, ok := firstFittingFreeLot(parseLotGrid(t, lotsResp.View))
		if !ok {
			t.Fatalf("no free lot on %s's own grid fits %s's own footprint", cityID, code)
		}
		if _, err := village.Place(ctx, newMeta("settlement.build.place", "build.place"),
			handlers.VillageBuildRequest{Code: code, Lot: screens.LotToken(x, y, false), Confirm: screens.VillageBuildConfirm}); err != nil {
			t.Fatalf("Place(%s): %v", code, err)
		}
		var buildingID string
		var queuedAt time.Time
		if err := pool.Raw().QueryRow(ctx,
			`SELECT id::text, queued_at FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = $2 AND status = 'building'`,
			cityID, code).Scan(&buildingID, &queuedAt); err != nil {
			t.Fatalf("reading the placed building %s: %v", code, err)
		}
		def, ok := snap.SettlementBuildingDef(code)
		if !ok {
			t.Fatalf("shipped content has no building %q", code)
		}
		clk.Advance(queuedAt.Add(def.Def().BuildTime).Sub(clk.Now()) + time.Second)
		if _, err := village.Built(ctx, newMeta("settlement.built", "built"),
			handlers.CrimeScheduledRequest{ReferenceID: buildingID}); err != nil {
			t.Fatalf("Built(%s): %v", code, err)
		}
		var status string
		if err := pool.Raw().QueryRow(ctx, `SELECT status FROM settlement_buildings WHERE id = $1::uuid`, buildingID).Scan(&status); err != nil {
			t.Fatalf("reading building status: %v", err)
		}
		if status != "complete" {
			t.Errorf("%s status = %q, want complete", code, status)
		}
	}
	seedTimber(t, uow, cityID, 5) // civic_hall's own cost_materials: {timber: 5}
	buildAndComplete("civic_hall")
	buildAndComplete("teaching_circle")

	// ------------------------------------------------------------------
	// 6. Teach raises literacy, now that a school (teaching_circle,
	// role: education) stands complete.
	// ------------------------------------------------------------------
	var teachFinish time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT finish_at FROM game_actions WHERE id = $1::uuid`, *pendingTeachAction).Scan(&teachFinish); err != nil {
		t.Fatalf("reading the teach action: %v", err)
	}
	if clk.Now().Before(teachFinish) {
		clk.Advance(teachFinish.Sub(clk.Now()) + time.Second)
	}
	if _, err := village.Taught(ctx, newMeta("settlement.taught", "taught"),
		handlers.CrimeScheduledRequest{ActionID: *pendingTeachAction, ReferenceID: cityID}); err != nil {
		t.Fatalf("Taught: %v", err)
	}
	var newLiteracyBPS int
	var nextPending string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT literacy_share_bps, pending_action_id::text FROM settlement_literacy WHERE settlement_id = $1::uuid`,
		cityID).Scan(&newLiteracyBPS, &nextPending); err != nil {
		t.Fatalf("reading literacy after teaching: %v", err)
	}
	if newLiteracyBPS <= literacyBPS {
		t.Errorf("literacy_share_bps did not rise: was %d, now %d", literacyBPS, newLiteracyBPS)
	}
	if nextPending == "" || nextPending == *pendingTeachAction {
		t.Error("the teach tick did not schedule its own successor")
	}
	t.Logf("village lifecycle: literacy_share_bps %d -> %d after one teach tick with a school standing", literacyBPS, newLiteracyBPS)

	// ------------------------------------------------------------------
	// 7. What the village wrote to the outbox reaches its settlement
	// channel, versioned and once, and its group as one merged post
	// (docs/adr/0030, R2); needs Redis.
	// ------------------------------------------------------------------
	if os.Getenv(envRedis) != "" {
		verifySettlementChannel(t, pool, cityID)
	} else {
		t.Logf("%s is not set; the settlement channel and village news were not checked", envRedis)
	}
}
