//go:build integration

// Integration test of the building panels, batch placement, automatic roads
// (village work of 2026-09-30; the paid land growth of that day was retired by ADR 0044, land now opens by roads, see roads_open_land_integration_test.go):
//
//   - settlement.build.place_many: one command lays a run of roads; the total
//     is charged in one spend; a batch with a bad lot is refused as a whole,
//     naming every offending lot, and changes nothing; roads never hold a slot
//     of the concurrent-construction cap.
//   - settlement.building.view: each type shows its own panel (storage
//     contents, school literacy, civic hall numbers, construction progress);
//     demolish is a last, quiet button that only the head sees.
//   - placing a building lays the road that connects it (finished at once,
//     small fee), refuses a building no road could reach, and leaves a
//     building that already touches the network alone.
//
// The village is founded directly on a land cell (no founding kit) so the
// test lays its own hall and road; every handler exercised runs through the
// real handler and a real Postgres transaction.
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
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// findDryCell returns a land cell whose whole 5x5 grid is buildable.
func findDryCell(t *testing.T, w *worldgen.World) int32 {
	t.Helper()
	for id, cell := range w.Cells {
		if cell.IsOcean || cell.IsLake {
			continue
		}
		grid := wsettle.SampleGrid(w, cell.Point.LatDeg, cell.Point.LonDeg, 5, int32(id))
		dry := true
		for _, row := range grid {
			for _, lot := range row {
				dry = dry && lot.Buildable
			}
		}
		if dry {
			return int32(id)
		}
	}
	t.Fatal("no land cell in this world has a fully buildable 5x5 grid")
	return 0
}

func viewMap(t *testing.T, r *presenter.Response) map[string]any {
	t.Helper()
	if len(r.View) == 0 {
		t.Fatalf("the response (%s) carried no structured view; text: %q", r.Screen, r.Text)
	}
	var m map[string]any
	if err := json.Unmarshal(r.View, &m); err != nil {
		t.Fatalf("reading the view: %v", err)
	}
	return m
}

func buttonTexts(r *presenter.Response) []string {
	var out []string
	if r.Keyboard == nil {
		return out
	}
	for _, row := range r.Keyboard.Rows {
		for _, b := range row {
			out = append(out, b.Text)
		}
	}
	return out
}

func TestVillagePanelsBatchRoadsAndGrowth(t *testing.T) {
	pool := requirePostgres(t)
	worlds := postgres.NewWorldRepository(pool)
	if _, err := worlds.Active(testCtx(t)); err == nil {
		t.Skip("a world is already active; this test only runs against a database with none")
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
		Seed: 20280930001, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: "integration-test-village-panels", CreatedBy: "integration-test",
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
	head := insertPlayer(t, pool)
	stranger := insertPlayer(t, pool)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	snap := loadTestContent(t)
	source := staticContentSource{snap: snap}
	cities := postgres.NewCityRepository(pool)
	clk := &testClock{now: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)}
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatalf("loading the locale catalogue: %v", err)
	}
	const (
		roadFee    = 10
		startMoney = 100_000
	)
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, source, worldCache, cities, gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			DemolitionSalvageBPS: 2_000,
			AutoRoadCost: roadFee,
		}, time.Hour, clk.Now)

	groupChatID := -newTelegramUserID(t)
	metaOf := func(p *application.Player, command, action string) envelope.Metadata {
		m := validMeta(t)
		m.BotID = bot
		m.TelegramUserID = p.TelegramUserID
		m.TelegramChatID = groupChatID
		m.ChatType = "group"
		m.Command, m.Action = command, action
		return m
	}
	// The head plays through a game client (a group's plain screens carry no
	// screen name or structured view, a client's do); the stranger stays in the
	// group, as a member who does not head the village.
	headMeta := func(command, action string) envelope.Metadata {
		m := metaOf(head, command, action)
		m.ChatType = envelope.ChatTypeClient
		m.TelegramChatID = head.TelegramUserID
		return m
	}

	asPlayer := func(p *application.Player, command, action string) envelope.Metadata {
		if p == head {
			return headMeta(command, action)
		}
		return metaOf(p, command, action)
	}

	_, worldModel, err := worldCache.Active(testCtx(t))
	if err != nil {
		t.Fatalf("reading the active world back: %v", err)
	}
	cellID := findDryCell(t, worldModel)
	cell := worldModel.Cells[cellID]

	var cityID, jurisdictionID string
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		now := clk.Now()
		out, err := tx.Settlements().Found(ctx, application.Founding{
			WorldID: w.ID, WorldCellID: cellID, LatDeg: cell.Point.LatDeg, LonDeg: cell.Point.LonDeg, Tier: "village",
			Code: "v-panels-" + randomToken(t, 10), Name: "Panel Test Village",
			CountryCode: application.DefaultFoundingCountryCode, FounderPlayerID: head.ID,
			FoundedByGroupChatID: groupChatID, FoundedByBotID: bot, GroupLanguage: "fa",
			FoundedAt: now, ProtectedUntil: now.Add(168 * time.Hour),
		})
		if err != nil {
			return err
		}
		cityID, jurisdictionID = out.CityID, out.JurisdictionID
		if _, _, err := application.FoundOffice(ctx, tx, "village_head", jurisdictionID, 1, head.ID, now); err != nil {
			return err
		}
		return village.EnsureTeaching(ctx, tx, cityID, now)
	}); err != nil {
		t.Fatalf("founding the test village: %v", err)
	}
	t.Cleanup(func() { cleanupPlacementSettlement(t, pool, cityID) })
	seedTreasury(t, pool, cityID, startMoney)
	seedTimber(t, uow, cityID, 30)
	treasury := func() int64 { return cashBalance(t, pool, application.AccountCityTreasury, cityID) }

	// Rows the test lays directly, finished (the founding kit's shape).
	lay := func(code string, x, y int) string {
		t.Helper()
		id := newUUID(t)
		now := clk.Now()
		if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
			return tx.SettlementBuildings().Place(ctx, application.SettlementBuildingInstance{
				ID: id, SettlementID: cityID, TypeCode: code, LotX: x, LotY: y, Status: "complete", QueuedAt: now, CompletedAt: &now,
			})
		}); err != nil {
			t.Fatalf("laying %s at (%d,%d): %v", code, x, y, err)
		}
		return id
	}
	hallID := lay("civic_hall", 2, 2) // covers (2,2) (3,2) (2,3) (3,3)
	lay("road", 1, 2)
	granaryID := lay("granary", 4, 0)
	circleID := lay("teaching_circle", 0, 4)

	rowsOf := func(code string) (n int, complete int) {
		t.Helper()
		if err := pool.Raw().QueryRow(testCtx(t),
			`SELECT count(*), count(*) FILTER (WHERE status = 'complete') FROM settlement_buildings
			  WHERE settlement_id = $1::uuid AND type_code = $2 AND status IN ('building','complete')`, cityID, code).Scan(&n, &complete); err != nil {
			t.Fatalf("counting %s: %v", code, err)
		}
		return
	}
	idAt := func(x, y int) string {
		t.Helper()
		var id string
		if err := pool.Raw().QueryRow(testCtx(t),
			`SELECT id::text FROM settlement_buildings WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3 AND status IN ('building','complete')`,
			cityID, x, y).Scan(&id); err != nil {
			t.Fatalf("no building at (%d,%d): %v", x, y, err)
		}
		return id
	}
	finish := func(id string) {
		t.Helper()
		clk.Advance(3 * time.Hour)
		if _, err := rrcm(headMeta("settlement.built", "built"))(village.Built(testCtx(t), headMeta("settlement.built", "built"), handlers.CrimeScheduledRequest{ReferenceID: id})); err != nil {
			t.Fatalf("Built: %v", err)
		}
	}
	place := func(code string, x, y int, confirm bool) *presenter.Response {
		t.Helper()
		req := handlers.VillageBuildRequest{Code: code, Lot: screens.LotToken(x, y, false)}
		if confirm {
			req.Confirm = screens.VillageBuildConfirm
		}
		r, err := rrcm(headMeta("settlement.build.place", "build.place"))(village.Place(testCtx(t), headMeta("settlement.build.place", "build.place"), req))
		if err != nil {
			t.Fatalf("Place %s at (%d,%d): %v", code, x, y, err)
		}
		return r
	}
	outboxHas := func(event string) []map[string]any {
		t.Helper()
		rows, err := pool.Raw().Query(testCtx(t),
			`SELECT payload FROM outbox WHERE subject LIKE '%.' || $1 || '.v1' AND payload->>'settlement_id' = $2 ORDER BY created_at`, event, cityID)
		if err != nil {
			t.Fatalf("reading the outbox: %v", err)
		}
		defer rows.Close()
		var out []map[string]any
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		return out
	}

	// ---------------------------------------------------------------
	// 1. Automatic roads: a house far from the network brings its own street.
	// ---------------------------------------------------------------
	preview := place("cottage", 0, 0, false)
	if preview.Screen != screens.ScreenLotConfirm {
		t.Fatalf("the preview screen = %q", preview.Screen)
	}
	if n, _ := viewMap(t, preview)["auto_roads"].(float64); n != 2 {
		t.Errorf("the preview announces %v auto road lots, want 2", viewMap(t, preview)["auto_roads"])
	}
	if !strings.Contains(preview.Text, "جاده") {
		t.Errorf("the preview does not mention the roads; text = %q", preview.Text)
	}
	before, roadsBefore := treasury(), func() int { n, _ := rowsOf("road"); return n }()
	place("cottage", 0, 0, true)
	if got := before - treasury(); got != 400+2*roadFee {
		t.Errorf("the house and its street cost %d, want %d (400 + 2 road lots x %d)", got, 400+2*roadFee, roadFee)
	}
	n, done := rowsOf("road")
	if n-roadsBefore != 2 || done != n {
		t.Errorf("roads after the house: %d rows (%d complete), want 2 new, all finished at once", n, done)
	}
	started := outboxHas("build_started")
	if len(started) == 0 || started[len(started)-1]["auto_roads"] == nil || started[len(started)-1]["layout_version"] == nil {
		t.Errorf("build_started carries no auto_roads/layout_version: %v", started)
	}
	house1 := idAt(0, 0)
	finish(house1)

	// A house that already touches the network needs no road and costs only itself.
	before = treasury()
	roadsBefore, _ = rowsOf("road")
	place("cottage", 1, 3, true)
	if got := before - treasury(); got != 400 {
		t.Errorf("a house on the network cost %d, want 400 (no road lots)", got)
	}
	if n, _ := rowsOf("road"); n != roadsBefore {
		t.Errorf("a house on the network laid %d roads", n-roadsBefore)
	}
	house2 := idAt(1, 3)

	// ---------------------------------------------------------------
	// 2. The cap: house2 holds the village's one slot, so another house waits;
	// roads hold none, so a batch of them goes ahead beside it.
	// ---------------------------------------------------------------
	capResp := place("cottage", 4, 1, true)
	if !strings.Contains(capResp.Text, "صف ساخت") {
		t.Errorf("a second running house was not refused by the cap; text = %q", capResp.Text)
	}

	many := func(lots []string, confirm bool) *presenter.Response {
		t.Helper()
		req := handlers.VillageBuildManyRequest{Code: "road", Lots: lots}
		if confirm {
			req.Confirm = screens.VillageBuildConfirm
		}
		r, err := rrcm(headMeta("settlement.build.place_many", "build.place_many"))(village.PlaceMany(testCtx(t), headMeta("settlement.build.place_many", "build.place_many"), req))
		if err != nil {
			t.Fatalf("PlaceMany: %v", err)
		}
		return r
	}
	batch := []string{"2-0", "3-0", "2-1"}
	pre := many(batch, false)
	if pre.Screen != screens.ScreenLotBatchConfirm {
		t.Fatalf("the batch preview screen = %q; text %q", pre.Screen, pre.Text)
	}
	if c, _ := viewMap(t, pre)["cost_money"].(float64); c != 150 {
		t.Errorf("the batch preview says %v, want 150 (3 roads x 50)", viewMap(t, pre)["cost_money"])
	}
	before = treasury()
	roadsBefore, _ = rowsOf("road")
	after := many(batch, true)
	if after.Screen != screens.ScreenConstructionProgress {
		t.Errorf("a confirmed batch answers %q, want the construction progress", after.Screen)
	}
	if got := before - treasury(); got != 150 {
		t.Errorf("the batch cost %d in total, want 150", got)
	}
	if n, _ := rowsOf("road"); n-roadsBefore != 3 {
		t.Errorf("the batch laid %d roads, want 3", n-roadsBefore)
	}
	batchEvents := outboxHas("build_batch_started")
	if len(batchEvents) != 1 || batchEvents[0]["layout_version"] == nil || int(batchEvents[0]["count"].(float64)) != 3 {
		t.Errorf("the batch made %d build_batch_started events (payload %v), want one with layout_version and count 3", len(batchEvents), batchEvents)
	}
	// The three roads were placed while house2 was still going up.
	var running int
	_ = pool.Raw().QueryRow(testCtx(t), `SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND status = 'building'`, cityID).Scan(&running)
	if running != 4 {
		t.Errorf("%d buildings are going up, want 4 (house2 and the 3 batch roads)", running)
	}

	// ---------------------------------------------------------------
	// 3. All or nothing: one bad lot refuses the whole batch and names it.
	// ---------------------------------------------------------------
	before, roadsBefore = treasury(), func() int { n, _ := rowsOf("road"); return n }()
	bad, err := rrcm(headMeta("settlement.build.place_many", "build.place_many"))(village.PlaceMany(testCtx(t), headMeta("settlement.build.place_many", "build.place_many"),
		handlers.VillageBuildManyRequest{Code: "road", Lots: []string{"3-1", "2-0", "9-9"}, Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		t.Fatal(err)
	}
	if bad.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a bad batch answered %q, want the refusal", bad.Screen)
	}
	vm := viewMap(t, bad)
	if vm["kind"] != "batch" {
		t.Errorf("refusal kind = %v, want batch", vm["kind"])
	}
	lots, _ := vm["lots"].([]any)
	kinds := map[string]string{}
	for _, l := range lots {
		lm := l.(map[string]any)
		kinds[screens.LotToken(int(lm["x"].(float64)), int(lm["y"].(float64)), false)] = lm["kind"].(string)
	}
	if kinds["2-0"] != screens.VillageOccupied || kinds["9-9"] != screens.VillageOutOfBounds || len(kinds) != 2 {
		t.Errorf("the refusal names %v, want exactly 2-0 occupied and 9-9 out_of_bounds", kinds)
	}
	if !strings.Contains(bad.Text, "چیزی پرداخت نشد") {
		t.Errorf("the refusal text does not say nothing was paid: %q", bad.Text)
	}
	if treasury() != before {
		t.Errorf("a refused batch moved money: %d -> %d", before, treasury())
	}
	if n, _ := rowsOf("road"); n != roadsBefore {
		t.Errorf("a refused batch left %d new roads behind", n-roadsBefore)
	}
	assertNoBuildingAt(t, pool, cityID, 3, 1) // the good lot of the bad batch was not built either

	// A batch of anything but a cap-exempt type is refused.
	notRoad, err := rrcm(headMeta("settlement.build.place_many", "build.place_many"))(village.PlaceMany(testCtx(t), headMeta("settlement.build.place_many", "build.place_many"),
		handlers.VillageBuildManyRequest{Code: "cottage", Lots: []string{"3-1", "4-1"}, Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		t.Fatal(err)
	}
	if notRoad.Screen != screens.ScreenVillageRefusal {
		t.Errorf("a batch of houses answered %q, want a refusal", notRoad.Screen)
	}

	// ---------------------------------------------------------------
	// 4. The panels.
	// ---------------------------------------------------------------
	view := func(p *application.Player, id, mode string) *presenter.Response {
		t.Helper()
		r, err := rrcm(asPlayer(p, "settlement.building.view", "building.view"))(village.BuildingView(testCtx(t), asPlayer(p, "settlement.building.view", "building.view"),
			handlers.VillageBuildingViewRequest{BuildingID: id, Mode: mode}))
		if err != nil {
			t.Fatalf("BuildingView: %v", err)
		}
		return r
	}

	// Storage: the village's stock, item by item.
	store := view(head, granaryID, "")
	if store.Screen != screens.ScreenBuildingView {
		t.Fatalf("the granary panel screen = %q; text %q", store.Screen, store.Text)
	}
	sm := viewMap(t, store)
	if sm["kind"] != screens.BuildingKindStorage || sm["state"] != screens.BuildingStateComplete {
		t.Errorf("granary panel kind/state = %v/%v", sm["kind"], sm["state"])
	}
	stock, _ := sm["stock"].([]any)
	var timber float64
	for _, s := range stock {
		sl := s.(map[string]any)
		if sl["item"].(map[string]any)["code"] == "timber" {
			timber = sl["qty"].(float64)
		}
	}
	var timberInDB int64
	_ = pool.Raw().QueryRow(testCtx(t), `SELECT COALESCE(SUM(quantity),0)::bigint FROM org_stacks WHERE org_kind='settlement' AND org_id=$1::uuid AND item_code='timber'`, cityID).Scan(&timberInDB)
	if int64(timber) != timberInDB || timber == 0 {
		t.Errorf("the granary panel lists timber %v, the store holds %d", timber, timberInDB)
	}
	if !strings.Contains(store.Text, "موجودی انبار") {
		t.Errorf("the granary panel does not read as a store: %q", store.Text)
	}
	// Demolish is offered, but as the panel's last action, never its face:
	// the text says what the building is before any button does.
	if !strings.Contains(strings.Join(buttonTexts(store), "|"), "تخریب") {
		t.Errorf("the head's granary panel has no demolish action: %v", buttonTexts(store))
	}
	if strings.Index(store.Text, "انبار") > strings.Index(store.Text, "تخریب") && strings.Contains(store.Text, "تخریب") {
		t.Errorf("the granary panel talks about demolition before it says what it is: %q", store.Text)
	}

	// School: literacy and teaching.
	school := view(head, circleID, "")
	scm := viewMap(t, school)
	if scm["kind"] != screens.BuildingKindSchool || scm["teaching"] != true {
		t.Errorf("school panel kind/teaching = %v/%v", scm["kind"], scm["teaching"])
	}
	if _, ok := scm["literacy_percent"]; !ok || !strings.Contains(school.Text, "سواد") {
		t.Errorf("school panel shows no literacy: %q", school.Text)
	}

	// Upgrade: revealed only when pressed.
	if ups, _ := scm["upgrades"].([]any); len(ups) != 0 {
		t.Errorf("the plain school panel already lists upgrades: %v", ups)
	}
	if hu, _ := scm["has_upgrade"].(bool); !hu {
		t.Errorf("a tier-1 education building should offer an upgrade (the next tier of its role)")
	}
	up := view(head, circleID, screens.BuildingModeUpgrade)
	if ups, _ := viewMap(t, up)["upgrades"].([]any); len(ups) == 0 {
		t.Errorf("the upgrade panel lists nothing; text %q", up.Text)
	}

	// Civic hall: village numbers and doors to the village screens.
	hall := view(head, hallID, "")
	hm := viewMap(t, hall)
	if hm["kind"] != screens.BuildingKindCivicHall {
		t.Errorf("hall panel kind = %v", hm["kind"])
	}
	if _, ok := hm["treasury"]; !ok || !strings.Contains(hall.Text, "خزانه") {
		t.Errorf("hall panel shows no treasury: %q", hall.Text)
	}
	if hall.Keyboard == nil || !strings.Contains(strings.Join(buttonTexts(hall), "|"), "پیشرفت") {
		t.Errorf("hall panel has no door to the construction progress: %v", buttonTexts(hall))
	}

	// Under construction: progress and finish time, a cancel button, no demolish.
	going := view(head, house2, "")
	gm := viewMap(t, going)
	if gm["state"] != screens.BuildingStateBuilding {
		t.Errorf("house2 state = %v, want building", gm["state"])
	}
	if gm["finish_at"] == nil || gm["progress_percent"] == nil {
		t.Errorf("an unfinished panel lacks finish_at/progress_percent: %v", gm)
	}
	gt := strings.Join(buttonTexts(going), "|")
	if !strings.Contains(gt, "لغو ساخت") || strings.Contains(gt, "تخریب") {
		t.Errorf("an unfinished panel should offer cancel and not demolish: %s", gt)
	}
	// The destructive actions confirm first.
	dm := view(head, granaryID, screens.BuildingModeDemolish)
	if !strings.Contains(dm.Text, "مطمئن") || !strings.Contains(strings.Join(buttonTexts(dm), "|"), "بله") {
		t.Errorf("demolish did not ask first: %q %v", dm.Text, buttonTexts(dm))
	}

	// A road is only information.
	road := view(head, idAt(2, 0), "")
	if viewMap(t, road)["kind"] != screens.BuildingKindRoad {
		t.Errorf("road panel kind = %v", viewMap(t, road)["kind"])
	}

	// Anyone in the village sees the panel; only the head gets actions.
	other := view(stranger, granaryID, screens.BuildingModeDemolish)
	om := viewMap(t, other)
	if om["can_manage"] == true || om["mode"] != nil && om["mode"] != "" {
		t.Errorf("a non-head got management: can_manage=%v mode=%v", om["can_manage"], om["mode"])
	}
	if strings.Contains(strings.Join(buttonTexts(other), "|"), "تخریب") || strings.Contains(strings.Join(buttonTexts(other), "|"), "ارتقا") {
		t.Errorf("a non-head sees manage buttons: %v", buttonTexts(other))
	}

	// An unknown building is a refusal, not a crash.
	gone := view(head, newUUID(t), "")
	if gone.Screen != screens.ScreenVillageRefusal {
		t.Errorf("an unknown building answered %q", gone.Screen)
	}

	// ---------------------------------------------------------------
	// 5. A building no road could ever reach is refused and costs nothing.
	// ---------------------------------------------------------------
	finish(house2)
	lay("cottage", 3, 4)
	lay("cottage", 4, 3)
	before = treasury()
	walled := place("cottage", 4, 4, true)
	if walled.Screen != screens.ScreenVillageRefusal || !strings.Contains(walled.Text, "هیچ راهی") {
		t.Errorf("a walled-in house answered %q: %q", walled.Screen, walled.Text)
	}
	if treasury() != before {
		t.Errorf("a refused house moved money")
	}
	assertNoBuildingAt(t, pool, cityID, 4, 4)
}
