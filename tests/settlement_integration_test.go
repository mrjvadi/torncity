//go:build integration

// Integration test of group founding (docs/adr/0028-world-and-settlements.md
// section 3): the one guarantee no unit test can make, because it lives in
// PostgreSQL — the unique indexes that make founding idempotent and
// race-safe (migration 0042) — and in the real transactional wiring between
// SettlementsHandler, the world cache and the postgres repositories.
package tests

import (
	"context"
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
)

// TestSettlementFounding runs ADR 0028 section 3.1 end to end against a real
// database: a group's founding command creates a village (jurisdiction,
// city row, a seated village_head, the founding kit, the group link, an
// outbox event), and a second founding command from the SAME chat is
// answered idempotently — never a second village.
func TestSettlementFounding(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	var exists bool
	if err := pool.Raw().QueryRow(ctx, `SELECT to_regclass('public.worlds') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("worlds does not exist; apply migration 0042 first")
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
	// Small on purpose: this test generates the planet for real (no fake),
	// and a few thousand cells is enough land to found on in well under a
	// second.
	params := worldgen.DefaultParams()
	params.CellCount = 4000

	w, err := worlds.Create(ctx, application.World{
		Seed: 20280928001, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: "integration-test", CreatedBy: "integration-test",
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
	h := handlers.NewSettlementsHandler(uow, workIDs{t}, nil, worldCache,
		staticContentSource{snap: snap}, gametime.Scale(1),
		wsettle.Params{MinSpawnDistanceKm: 30, ThreatRadiusKm: 150, SearchMaxCells: 2000, SearchMaxAttempts: 50},
		168*time.Hour, 5, time.Hour, func() time.Time { return time.Now().UTC() })

	meta := validMeta(t)
	meta.BotID = bot
	meta.TelegramUserID = founder.TelegramUserID
	meta.TelegramChatID = -newTelegramUserID(t) // a group chat id is negative
	meta.ChatType = "group"
	meta.Command, meta.Action = "settlement.found", "found"

	var cityID string
	t.Cleanup(func() {
		if cityID == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		var jurisdictionID string
		_ = pool.Raw().QueryRow(ctx, `SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, cityID).Scan(&jurisdictionID)
		for _, stmt := range []string{
			`DELETE FROM outbox WHERE subject = 'game.event.settlement.founded.v1' AND payload->>'settlement_id' = $1`,
			`DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid`,
			`DELETE FROM city_group_links WHERE city_id = $1::uuid`,
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

	resp, err := h.Found(ctx, meta)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if resp == nil {
		t.Fatal("Found returned a nil response")
	}
	// messages is nil, so every text key renders as itself (screens.Context.T's
	// documented nil-Msgs behaviour) — a simple, exact way to tell which
	// screen was rendered without needing a loaded catalogue.
	if !strings.Contains(resp.Text, "settlement.found.title") {
		t.Fatalf("Found's response does not look like the founded screen: %q", resp.Text)
	}

	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatalf("reading the founded city: %v", err)
	}

	var origin, tier string
	var worldID string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT origin, tier, world_id::text FROM cities WHERE id = $1::uuid`, cityID).
		Scan(&origin, &tier, &worldID); err != nil {
		t.Fatalf("reading the founded city's shape: %v", err)
	}
	if origin != "founded" || tier != "village" || worldID != w.ID {
		t.Fatalf("founded city = origin %q tier %q world %q, want founded/village/%s", origin, tier, worldID, w.ID)
	}

	var jurisdictionID, holder, acquiredBy string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, cityID).Scan(&jurisdictionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx,
		`SELECT COALESCE(holder_player_id::text, ''), COALESCE(acquired_by, '')
		   FROM offices WHERE office_code = 'village_head' AND jurisdiction_id = $1::uuid AND seat = 1`,
		jurisdictionID).Scan(&holder, &acquiredBy); err != nil {
		t.Fatalf("reading the village_head seat: %v", err)
	}
	if holder != founder.ID || acquiredBy != "founding" {
		t.Fatalf("village_head = holder %q acquired_by %q, want %s/founding", holder, acquiredBy, founder.ID)
	}

	var buildingCount int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND status = 'complete'`,
		cityID).Scan(&buildingCount); err != nil {
		t.Fatal(err)
	}
	if buildingCount == 0 {
		t.Error("no founding-kit buildings were placed")
	}

	var links int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM city_group_links WHERE city_id = $1::uuid AND chat_id = $2 AND bot_id = $3::uuid`,
		cityID, meta.TelegramChatID, bot).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Errorf("city_group_links has %d rows for this founding, want 1", links)
	}

	var events int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE subject = 'game.event.settlement.founded.v1' AND payload->>'settlement_id' = $1`,
		cityID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Errorf("outbox has %d settlement.founded events, want 1", events)
	}

	// A second founding command from the SAME chat must not found a second
	// village: ADR 0028 section 3.1's "no group founds more than one", and
	// the idempotency at-least-once delivery needs.
	meta2 := meta
	meta2.RequestID = "req_" + randomToken(t, 24)
	resp2, err := h.Found(ctx, meta2)
	if err != nil {
		t.Fatalf("second Found: %v", err)
	}
	if resp2 == nil || !strings.Contains(resp2.Text, "settlement.found.already") {
		text := ""
		if resp2 != nil {
			text = resp2.Text
		}
		t.Fatalf("second Found's response does not look like the already-founded refusal: %q", text)
	}

	var settlements int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&settlements); err != nil {
		t.Fatal(err)
	}
	if settlements != 1 {
		t.Fatalf("chat %d founded %d settlements, want exactly 1", meta.TelegramChatID, settlements)
	}
}
