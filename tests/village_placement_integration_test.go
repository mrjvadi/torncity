//go:build integration

// Integration test of W5's placement picker (docs/adr/0028-world-and-
// settlements.md section 6): the leader chooses a building's own lot from
// the grid Lots renders, Place lands it at exactly that chosen lot, and
// CanPlace's own real refusals come back through Place with a distinct
// Persian reason — an unbuildable (water) lot never reads as "already
// occupied" by another building, and a materials shortage refuses without
// moving anything. tests/village_integration_test.go already lifts K2/W5
// end to end (found, research, buy, build, teach); this file is its own
// narrower follow-up for the chosen-lot picker specifically, sharing every
// fixture that file already defines (seedTreasury, testClock, insertBot,
// insertPlayer, loadTestContent, staticContentSource, workIDs — one
// package, one set of helpers, no need to redeclare any of them).
//
// The settlement here is founded directly against
// application.SettlementRepository.Found — the same repository call
// SettlementsHandler.Found itself makes — rather than through that
// handler's own automatic spawn search, at a world cell chosen so its own
// grid holds a water lot. Two things rule out the obvious "just found
// settlements until one has a water lot" approach: SettlementsHandler's
// own search (internal/domain/settlement/spawn.go scoreCell) actively
// steers away from open water (oceanShareWeight), and even without that
// bias a settlement's own 5x5 lot grid spans only about 150m — far too
// small a window for a random candidate anywhere on the globe to land on
// a coastline in any practical number of tries (measured: 0 hits in 300
// tries through the real search, 0 in 135 land points sampled uniformly
// at random). VillageHandler.grid itself only ever samples a settlement's
// grid centred on its own world cell's centroid — never the settlement's
// founding LatDeg/LonDeg — so findWaterCell below scans world cells
// directly for one whose own centroid's grid already holds a water lot,
// which this test then founds at directly. Every handler actually
// exercised here — Lots, Place, Built, Demolish — still runs through the
// real handler and a real Postgres transaction exactly as production
// does; only the spawn SEARCH itself (a different feature, its own test
// elsewhere) is bypassed.
package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
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
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// lotGridCellJSON/lotGridJSON mirror screens.LotGridView's own JSON shape
// closely enough to read x, y, state and fits back out of a Lots
// response's structured view (internal/telegram/presenter/view.go: a
// struct field becomes a snake_case key) — the same view clientapi itself
// would read to draw a tap-on-map grid.
type lotGridCellJSON struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	State string `json:"state"`
	Fits  bool   `json:"fits"`
}

type lotGridJSON struct {
	Rows [][]lotGridCellJSON `json:"rows"`
}

func parseLotGrid(t *testing.T, view json.RawMessage) lotGridJSON {
	t.Helper()
	if len(view) == 0 {
		t.Fatal("the Lots response carried no structured view (withGroupView should attach one even for a group screen)")
	}
	var g lotGridJSON
	if err := json.Unmarshal(view, &g); err != nil {
		t.Fatalf("parsing the lot grid view: %v", err)
	}
	return g
}

// firstLotWithState returns the first cell of the given state, row-major.
func firstLotWithState(g lotGridJSON, state string) (x, y int, ok bool) {
	for _, row := range g.Rows {
		for _, cell := range row {
			if cell.State == state {
				return cell.X, cell.Y, true
			}
		}
	}
	return 0, 0, false
}

// firstFittingFreeLot returns the first free lot the grid itself reports
// as fitting the building Lots was asked for (the real CanPlace check the
// handler ran, screens.LotCell.Fits — never re-derived here).
func firstFittingFreeLot(g lotGridJSON) (x, y int, ok bool) {
	for _, row := range g.Rows {
		for _, cell := range row {
			if cell.State == screens.LotFree && cell.Fits {
				return cell.X, cell.Y, true
			}
		}
	}
	return 0, 0, false
}

// findWaterCell returns the id of a world cell — not merely a nearby point,
// the cell itself, since VillageHandler.grid samples a settlement's own 5x5
// lot grid centred on w.Cells[s.WorldCellID].Point, its own centroid, never
// the settlement's own founding LatDeg/LonDeg (internal/application/
// handlers/village.go's own grid method) — whose own centroid's 5x5 lot
// grid holds at least one water (unbuildable) lot. An ordinary land cell,
// preferred so a settlement founded there still reads as a real place, one
// whose immediate window happens to dip into the sea, a lake or a stream.
func findWaterCell(t *testing.T, w *worldgen.World) int32 {
	t.Helper()
	for id, cell := range w.Cells {
		if cell.IsOcean || cell.IsLake {
			continue
		}
		grid := wsettle.SampleGrid(w, cell.Point.LatDeg, cell.Point.LonDeg, 5, int32(id))
		for _, row := range grid {
			for _, lot := range row {
				if !lot.Buildable {
					return int32(id)
				}
			}
		}
	}
	t.Fatal("findWaterCell: no land cell's own 5x5 lot grid holds a water lot in this world")
	return 0
}

// seedTimber credits qty timber into a settlement's own public stock
// (org_stacks, OrgSettlement — internal/application/ports_items.go), the
// same shape CostMaterials draws down from at placement. Through
// tx.Items().Move, not a raw INSERT: a raw INSERT changes org_stacks
// without a matching item_movements row, which is exactly what
// EconomyAdmin.verifyGoods (internal/infrastructure/postgres/
// ledger_admin.go) exists to catch — the same reason seedTreasury posts a
// real ledger transaction instead of writing accounts.balance directly.
func seedTimber(t *testing.T, uow application.UnitOfWork, settlementID string, qty int64) {
	t.Helper()
	ctx := testCtx(t)
	err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{
			ID: newUUID(t), Item: "timber", Qty: qty,
			ToOrg: application.SettlementOrg(settlementID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("seeding timber: %v", err)
	}
}

// assertNoBuildingAt fails the test if a refused placement somehow still
// left a row behind.
func assertNoBuildingAt(t *testing.T, pool *postgres.Pool, settlementID string, x, y int) {
	t.Helper()
	var count int
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3 AND status <> 'demolished'`,
		settlementID, x, y).Scan(&count); err != nil {
		t.Fatalf("checking for a building at (%d,%d): %v", x, y, err)
	}
	if count != 0 {
		t.Errorf("a building exists at (%d,%d) after a placement that should have been refused", x, y)
	}
}

// cleanupPlacementSettlement removes a founded settlement and everything
// that references it, the same statements TestVillageLifecycle's own
// t.Cleanup uses.
func cleanupPlacementSettlement(t *testing.T, pool *postgres.Pool, cityID string) {
	t.Helper()
	if cityID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	var jurisdictionID string
	_ = pool.Raw().QueryRow(ctx, `SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, cityID).Scan(&jurisdictionID)
	for _, stmt := range []string{
		`UPDATE players SET residence_city_id = NULL, residence_since = NULL WHERE residence_city_id = $1::uuid`,
		`UPDATE players SET city_id = (SELECT id FROM cities WHERE code = 'support'), place_code = NULL WHERE city_id = $1::uuid`,
		`DELETE FROM outbox WHERE payload->>'to_city_id' = $1 OR payload->>'from_city_id' = $1`,
		`DELETE FROM outbox WHERE payload->>'settlement_id' = $1`,
		`DELETE FROM settlement_research WHERE settlement_id = $1::uuid`,
		`DELETE FROM game_actions WHERE reference_type = 'settlement' AND reference_id = $1::uuid`,
		`DELETE FROM settlement_literacy WHERE settlement_id = $1::uuid`,
		`DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`,
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
	// ledger_entries/accounts and org_stacks/item_movements are
	// deliberately left alone, the same reason TestVillageLifecycle's own
	// cleanup gives for the ledger: both are append-only by design
	// (docs/database.md section 6), precisely so an audit trail can never
	// be edited — deleting an org_stacks row whose own quantity is not
	// currently zero would leave EconomyAdmin.verifyGoods's own journal
	// check permanently unable to reconcile it (internal/infrastructure/
	// postgres/ledger_admin.go), exactly the drift this test's own
	// seedTimber (a real tx.Items().Move, not a raw INSERT) exists to
	// avoid causing in the first place.
	if _, err := pool.Raw().Exec(ctx, `DELETE FROM cities WHERE id = $1::uuid`, cityID); err != nil {
		t.Errorf("cleaning up the founded city: %v", err)
	}
	if jurisdictionID != "" {
		if _, err := pool.Raw().Exec(ctx, `DELETE FROM jurisdictions WHERE id = $1::uuid`, jurisdictionID); err != nil {
			t.Errorf("cleaning up the founded jurisdiction: %v", err)
		}
	}
}

func TestVillageBuildPlacement(t *testing.T) {
	pool := requirePostgres(t)

	worlds := postgres.NewWorldRepository(pool)
	if _, err := worlds.Active(testCtx(t)); err == nil {
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

	w, err := worlds.Create(testCtx(t), application.World{
		Seed: 20280929001, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: "integration-test-village-placement", CreatedBy: "integration-test",
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

	clk := &testClock{now: time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)}

	// A real translator, not nil like TestVillageLifecycle's own handler
	// (which never inspects a screen's own Persian text) — this test reads
	// resp.Text to tell CanPlace's own distinct refusals apart, so it needs
	// the real catalogue behind them, not a raw locale key echoed back.
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatalf("loading the locale catalogue: %v", err)
	}

	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, source, worldCache, cities, gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			DemolitionSalvageBPS: 2_000,
		}, time.Hour, clk.Now)

	newMetaFor := func(groupChatID int64, command, action string) envelope.Metadata {
		m := validMeta(t)
		m.BotID = bot
		m.TelegramUserID = founder.TelegramUserID
		m.TelegramChatID = groupChatID
		m.ChatType = "group"
		m.Command, m.Action = command, action
		return m
	}

	// ------------------------------------------------------------------
	// Found a settlement directly at a coastline the world's own terrain
	// reports (this file's own header comment explains why), with no
	// founding kit: this test places its own buildings on its own chosen
	// lots — the very feature under test — and has no use for the
	// founding kit's own first-fit civic_hall/road.
	// ------------------------------------------------------------------
	_, worldModel, err := worldCache.Active(testCtx(t))
	if err != nil {
		t.Fatalf("reading the active world back: %v", err)
	}
	waterCellID := findWaterCell(t, worldModel)
	waterCell := worldModel.Cells[waterCellID]

	groupChatID := -newTelegramUserID(t)
	var cityID, jurisdictionID string
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		now := clk.Now()
		f := application.Founding{
			WorldID: w.ID, WorldCellID: waterCellID,
			LatDeg: waterCell.Point.LatDeg, LonDeg: waterCell.Point.LonDeg, Tier: "village",
			Code: "v-place-" + randomToken(t, 10), Name: "Coastal Test Village",
			CountryCode: application.DefaultFoundingCountryCode, FounderPlayerID: founder.ID,
			FoundedByGroupChatID: groupChatID, FoundedByBotID: bot, GroupLanguage: "fa",
			FoundedAt: now, ProtectedUntil: now.Add(168 * time.Hour),
		}
		out, err := tx.Settlements().Found(ctx, f)
		if err != nil {
			return err
		}
		cityID, jurisdictionID = out.CityID, out.JurisdictionID
		_, _, err = application.FoundOffice(ctx, tx, "village_head", jurisdictionID, 1, founder.ID, now)
		return err
	}); err != nil {
		t.Fatalf("founding the coastal test settlement: %v", err)
	}
	t.Cleanup(func() { cleanupPlacementSettlement(t, pool, cityID) })
	seedTreasury(t, pool, cityID, 50_000)

	lotsResp0, err := rrcm(newMetaFor(groupChatID, "settlement.build.lots", "build.lots"))(village.Lots(testCtx(t), newMetaFor(groupChatID, "settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: "road"}))
	if err != nil {
		t.Fatalf("Lots(road): %v", err)
	}
	waterX, waterY, ok := firstLotWithState(parseLotGrid(t, lotsResp0.View), screens.LotWater)
	if !ok {
		t.Fatalf("the coastal settlement's own grid has no water lot after all (world_cell_id=%d)", waterCellID)
	}

	// ------------------------------------------------------------------
	// 1. Refused on water: CanPlace's own ErrUnbuildableLot comes back as
	// its own distinct Persian reason, never "already occupied" (a real
	// occupied lot's own text) — nothing is placed.
	// ------------------------------------------------------------------
	waterResp, err := rrcm(newMetaFor(groupChatID, "settlement.build.place", "build.place"))(village.Place(testCtx(t), newMetaFor(groupChatID, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "road", Lot: screens.LotToken(waterX, waterY, false)}))
	if err != nil {
		t.Fatalf("Place on a water lot: %v", err)
	}
	if !strings.Contains(waterResp.Text, "نمی‌توان ساخت") {
		t.Errorf("Place on a water lot did not refuse with its own unbuildable reason; text = %q", waterResp.Text)
	}
	if strings.Contains(waterResp.Text, "اشغال") {
		t.Errorf("Place on a water lot reused the already-occupied text; text = %q", waterResp.Text)
	}
	assertNoBuildingAt(t, pool, cityID, waterX, waterY)

	// ------------------------------------------------------------------
	// 2. A chosen, fitting lot for civic_hall (2x2, needs no knowledge):
	// the unconfirmed press previews cost and time without moving
	// anything; confirmed with no timber in stock, it refuses for
	// materials rather than placing a half-paid building.
	// ------------------------------------------------------------------
	// The grid of a building the village cannot afford in materials is not
	// offered (the attempt view names what is missing instead), so a 2x2 that
	// needs none - the park - is what the lot is picked on.
	lotsResp, err := rrcm(newMetaFor(groupChatID, "settlement.build.lots", "build.lots"))(village.Lots(testCtx(t), newMetaFor(groupChatID, "settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: "park"}))
	if err != nil {
		t.Fatalf("Lots(park): %v", err)
	}
	if noGrid, err := rrcm(newMetaFor(groupChatID, "settlement.build.lots", "build.lots"))(village.Lots(testCtx(t), newMetaFor(groupChatID, "settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: "civic_hall"})); err != nil ||
		!strings.Contains(noGrid.Text, "الوار") {
		t.Errorf("Lots(civic_hall) with no timber should name the timber it lacks: %+v %v", noGrid, err)
	}
	hallGrid := parseLotGrid(t, lotsResp.View)
	hallX, hallY, ok := firstFittingFreeLot(hallGrid)
	if !ok {
		t.Fatalf("no free lot on %s's own grid fits civic_hall's 2x2 footprint", cityID)
	}
	hallLot := screens.LotToken(hallX, hallY, false)

	// Without the timber even the preview is the attempt view: what is missing
	// and where it comes from, never a cost screen for something unaffordable.
	previewResp, err := rrcm(newMetaFor(groupChatID, "settlement.build.place", "build.place"))(village.Place(testCtx(t), newMetaFor(groupChatID, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "civic_hall", Lot: hallLot}))
	if err != nil {
		t.Fatalf("Place preview: %v", err)
	}
	if previewResp.Screen == screens.ScreenLotConfirm || !strings.Contains(previewResp.Text, "الوار") {
		t.Errorf("an unconfirmed Place with no timber = %q %q, want the attempt view naming the timber", previewResp.Screen, previewResp.Text)
	}
	assertNoBuildingAt(t, pool, cityID, hallX, hallY)

	shortResp, err := rrcm(newMetaFor(groupChatID, "settlement.build.place", "build.place"))(village.Place(testCtx(t), newMetaFor(groupChatID, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "civic_hall", Lot: hallLot, Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		t.Fatalf("Place confirmed with no timber: %v", err)
	}
	if !strings.Contains(shortResp.Text, "الوار") || !strings.Contains(shortResp.Text, "هیزم‌شکنی") {
		t.Errorf("Place confirmed with no timber did not refuse for materials; text = %q", shortResp.Text)
	}
	assertNoBuildingAt(t, pool, cityID, hallX, hallY)

	// ------------------------------------------------------------------
	// 3. Seed timber, place again: it lands at exactly the chosen lot,
	// timber is drawn from the settlement's own public stock, and money
	// leaves the treasury.
	// ------------------------------------------------------------------
	seedTimber(t, uow, cityID, 20)
	treasuryBefore := cashBalance(t, pool, application.AccountCityTreasury, cityID)

	// With the timber in stock the preview shows cost and time and changes nothing.
	if preview, err := rrcm(newMetaFor(groupChatID, "settlement.build.place", "build.place"))(village.Place(testCtx(t), newMetaFor(groupChatID, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "civic_hall", Lot: hallLot})); err != nil || preview.Screen != screens.ScreenLotConfirm {
		t.Errorf("an unconfirmed Place's own screen = %+v %v, want %q", preview, err, screens.ScreenLotConfirm)
	}
	assertNoBuildingAt(t, pool, cityID, hallX, hallY)

	if _, err := rrcm(newMetaFor(groupChatID, "settlement.build.place", "build.place"))(village.Place(testCtx(t), newMetaFor(groupChatID, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "civic_hall", Lot: hallLot, Confirm: screens.VillageBuildConfirm})); err != nil {
		t.Fatalf("Place confirmed with timber in stock: %v", err)
	}

	var buildingID string
	var placedX, placedY int
	var status string
	var queuedAt time.Time
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT id::text, lot_x, lot_y, status, queued_at FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'civic_hall'`,
		cityID).Scan(&buildingID, &placedX, &placedY, &status, &queuedAt); err != nil {
		t.Fatalf("reading the placed civic_hall: %v", err)
	}
	if placedX != hallX || placedY != hallY {
		t.Errorf("civic_hall landed at (%d,%d), want the chosen lot (%d,%d)", placedX, placedY, hallX, hallY)
	}
	if status != "building" {
		t.Errorf("civic_hall status = %q, want building", status)
	}

	var timberLeft int64
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'timber'`,
		cityID).Scan(&timberLeft); err != nil {
		t.Fatalf("reading remaining timber: %v", err)
	}
	if timberLeft != 15 {
		t.Errorf("timber remaining = %d, want 15 (20 seeded minus civic_hall's own cost_materials: 5)", timberLeft)
	}
	if got := cashBalance(t, pool, application.AccountCityTreasury, cityID) - treasuryBefore; got != -1000 {
		t.Errorf("treasury changed by %d, want -1000 (civic_hall's own cost_money)", got)
	}

	// A second attempt at the same lot, now occupied, is refused as
	// occupied (not unbuildable) — the two reasons really are distinct.
	occupiedResp, err := rrcm(newMetaFor(groupChatID, "settlement.build.place", "build.place"))(village.Place(testCtx(t), newMetaFor(groupChatID, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "road", Lot: hallLot, Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		t.Fatalf("Place on an occupied lot: %v", err)
	}
	if !strings.Contains(occupiedResp.Text, "اشغال") {
		t.Errorf("Place on an occupied lot did not refuse as occupied; text = %q", occupiedResp.Text)
	}

	// ------------------------------------------------------------------
	// 4. Demolition credits back demolition_salvage_bps of the building's
	// own money cost once it stands complete (ADR 0028 section 6.2) —
	// 20% of civic_hall's 1,000, 200.
	// ------------------------------------------------------------------
	def, ok := snap.SettlementBuildingDef("civic_hall")
	if !ok {
		t.Fatal("shipped content has no civic_hall")
	}
	clk.Advance(queuedAt.Add(def.Def().BuildTime).Sub(clk.Now()) + time.Second)
	if _, err := rrcm(newMetaFor(groupChatID, "settlement.built", "built"))(village.Built(testCtx(t), newMetaFor(groupChatID, "settlement.built", "built"),
		handlers.CrimeScheduledRequest{ReferenceID: buildingID})); err != nil {
		t.Fatalf("Built: %v", err)
	}

	treasuryBeforeDemolish := cashBalance(t, pool, application.AccountCityTreasury, cityID)
	if _, err := rrcm(newMetaFor(groupChatID, "settlement.build.demolish", "build.demolish"))(village.Demolish(testCtx(t), newMetaFor(groupChatID, "settlement.build.demolish", "build.demolish"),
		handlers.VillageBuildingRequest{ID: buildingID})); err != nil {
		t.Fatalf("Demolish: %v", err)
	}
	if got := cashBalance(t, pool, application.AccountCityTreasury, cityID) - treasuryBeforeDemolish; got != 200 {
		t.Errorf("treasury changed by %d on demolition, want +200 (2,000 bps of civic_hall's own 1,000 cost_money)", got)
	}

	var demolishedStatus string
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT status FROM settlement_buildings WHERE id = $1::uuid`, buildingID).Scan(&demolishedStatus); err != nil {
		t.Fatalf("reading the demolished building's own status: %v", err)
	}
	if demolishedStatus != "demolished" {
		t.Errorf("civic_hall status after Demolish = %q, want demolished", demolishedStatus)
	}

	t.Logf("village placement: water lot (%d,%d) refused unbuildable, civic_hall placed at the chosen lot (%d,%d), demolition credited 200 back", waterX, waterY, hallX, hallY)
}
