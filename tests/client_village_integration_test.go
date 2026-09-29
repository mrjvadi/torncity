//go:build integration

// Integration test of the client-facing village (docs/adr/0028 section 9.4,
// api/client-api.md "The world"): a game client, which has no Telegram group,
// runs the village commands through the same VillageHandler code path a
// group does — the settlement is the player's own — and reads the layout
// back from the same rows. The whole path is against real PostgreSQL:
// migration 0049 (a building's turn, finish time, cancel, a lot freed by a
// demolished or cancelled row) and the ambient transaction the handlers run in.
package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func TestClientVillageCommandsAndLayout(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	worlds := postgres.NewWorldRepository(pool)
	if _, err := worlds.Active(ctx); err == nil {
		t.Skip("a world is already active; this test only runs against a database with none (it cannot safely replace a live world)")
	}
	pack, err := content.LoadWorldGen("../configs/content")
	if err != nil {
		t.Fatal(err)
	}
	wgContent, err := pack.ToContent()
	if err != nil {
		t.Fatal(err)
	}
	params := worldgen.DefaultParams()
	params.CellCount = 4000
	w, err := worlds.Create(ctx, application.World{Seed: 20280930001, GeneratorVersion: worldgen.GeneratorVersion,
		ParamsHash: "integration-test-client-village", CreatedBy: "integration-test"})
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
	clk := &testClock{now: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)}
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, source, worldCache, postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000, BaseSchoolCapacityBPS: 10_000,
			ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, DemolitionSalvageBPS: 2_000},
		time.Hour, clk.Now)

	// A client's command: the chat is the player's own, private one, so the
	// settlement cannot come from a group.
	clientMeta := func(p *application.Player, command, action string) envelope.Metadata {
		m := validMeta(t)
		m.BotID = bot
		m.TelegramUserID = p.TelegramUserID
		m.TelegramChatID = p.TelegramUserID
		m.ChatType = envelope.ChatTypeClient
		m.Command, m.Action = command, action
		return m
	}

	// The settlement stands on a cell whose own grid holds a water lot.
	_, planet, err := worldCache.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cellID := findWaterCell(t, planet)
	cell := planet.Cells[cellID]
	var cityID, jurisdictionID string
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := clk.Now()
		out, err := tx.Settlements().Found(ctx, application.Founding{
			WorldID: w.ID, WorldCellID: cellID, LatDeg: cell.Point.LatDeg, LonDeg: cell.Point.LonDeg, Tier: "village",
			Code: "v-client-" + randomToken(t, 10), Name: "Client Test Village", CountryCode: application.DefaultFoundingCountryCode,
			FounderPlayerID: head.ID, FoundedByGroupChatID: -newTelegramUserID(t), FoundedByBotID: bot, GroupLanguage: "fa",
			FoundedAt: now, ProtectedUntil: now.Add(168 * time.Hour),
		})
		if err != nil {
			return err
		}
		cityID, jurisdictionID = out.CityID, out.JurisdictionID
		if _, _, err := application.FoundOffice(ctx, tx, "village_head", jurisdictionID, 1, head.ID, now); err != nil {
			return err
		}
		_, err = tx.SettlementKnowledge().Grant(ctx, application.SettlementKnowledgeOwned{
			SettlementID: cityID, Code: "mounted_militia", AcquiredVia: "founding", AcquiredAt: now})
		return err
	}); err != nil {
		t.Fatalf("founding: %v", err)
	}
	t.Cleanup(func() { cleanupPlacementSettlement(t, pool, cityID) })
	seedTreasury(t, pool, cityID, 50_000)
	seedTimber(t, uow, cityID, 20)

	worldSvc := &clientapi.WorldService{Source: worldCache, CacheEntries: 8, RecheckEvery: time.Minute}
	villages := &clientapi.VillageService{Settlements: postgres.NewSettlementReader(pool), Buildings: postgres.NewSettlementBuildingReader(pool),
		World: worldSvc, Content: registryOf(snap), VillageGridLots: 5, Now: clk.Now}

	// ---- where am I ---------------------------------------------------
	mine, err := villages.Mine(ctx, head.ID)
	if err != nil || mine == nil || mine.ID != cityID || !mine.IsHead || mine.Centre == nil || mine.GridLots != 5 {
		t.Fatalf("the head's settlement: %+v %v", mine, err)
	}
	if other, err := villages.Mine(ctx, stranger.ID); err != nil || other != nil {
		t.Fatalf("a stranger has a settlement: %+v %v", other, err)
	}

	// ---- the layout the placement rules agree with ------------------------
	layout, err := villages.Layout(ctx, head.ID, cityID)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Detail != clientapi.DetailFull || !layout.Viewer.CanPlace || len(layout.Buildings) != 0 {
		t.Fatalf("layout %+v", layout.Viewer)
	}
	waterX, waterY := -1, -1
	for y, row := range layout.Lots {
		for x, lot := range row {
			if !lot.Buildable {
				waterX, waterY = x, y
			}
		}
	}
	if waterX < 0 {
		t.Fatal("the layout shows no unbuildable lot on a grid that has one")
	}

	// ---- Lots, from a client, is the grid placement will use --------------
	lots, err := village.Lots(ctx, clientMeta(head, "settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: "militia_camp"})
	if err != nil {
		t.Fatal(err)
	}
	grid := parseLotGrid(t, lots.View)
	for y, row := range grid.Rows {
		for x, cell := range row {
			if (cell.State == screens.LotWater) == layout.Lots[y][x].Buildable {
				t.Errorf("lot %d,%d: the picker says %q, the layout says buildable=%v", x, y, cell.State, layout.Lots[y][x].Buildable)
			}
		}
	}

	// ---- a stranger is refused, structurally -----------------------------
	// (a stranger belongs to no settlement, so the command finds none)
	resp, err := village.Place(ctx, clientMeta(stranger, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "road", Lot: screens.LotToken(0, 0, false), Confirm: screens.VillageBuildConfirm})
	if err != nil {
		t.Fatal(err)
	}
	if refusalKind(t, resp.View) != screens.VillageNoSettlement {
		t.Errorf("a stranger's placement: screen %q view %s", resp.Screen, resp.View)
	}

	// ---- water refused, with its own code --------------------------------
	resp, err = village.Place(ctx, clientMeta(head, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "road", Lot: screens.LotToken(waterX, waterY, false), Confirm: screens.VillageBuildConfirm})
	if err != nil {
		t.Fatal(err)
	}
	if refusalKind(t, resp.View) != screens.VillageUnbuildable {
		t.Errorf("water: %q %s", resp.Screen, resp.View)
	}
	assertNoBuildingAt(t, pool, cityID, waterX, waterY)

	// ---- place a turned militia camp -------------------------------------
	// A 2x1 camp turned is 1x2: find a free lot where the turned footprint fits.
	turned, err := village.Lots(ctx, clientMeta(head, "settlement.build.lots", "build.lots"),
		handlers.VillageLotsRequest{Code: "militia_camp", Rotate: "1"})
	if err != nil {
		t.Fatal(err)
	}
	cx, cy, ok := firstFittingFreeLot(parseLotGrid(t, turned.View))
	if !ok {
		t.Fatal("no lot fits a turned militia camp")
	}
	place := func(p *application.Player, code string, x, y int, rotated bool) *clientPlaceResult {
		m := clientMeta(p, "settlement.build.place", "build.place")
		r, err := village.Place(ctx, m, handlers.VillageBuildRequest{Code: code, Lot: screens.LotToken(x, y, rotated), Confirm: screens.VillageBuildConfirm})
		if err != nil {
			t.Fatal(err)
		}
		return &clientPlaceResult{screen: r.Screen, view: r.View}
	}
	if r := place(head, "militia_camp", cx, cy, true); r.screen == screens.ScreenVillageRefusal {
		t.Fatalf("placing the turned camp was refused: %s", r.view)
	}
	var campID string
	var rotated bool
	var finish *time.Time
	var status string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text, rotated, finish_at, status FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'militia_camp'`,
		cityID).Scan(&campID, &rotated, &finish, &status); err != nil {
		t.Fatal(err)
	}
	if !rotated || status != "building" || finish == nil || !finish.After(clk.Now()) {
		t.Fatalf("camp row: rotated=%v status=%s finish=%v", rotated, status, finish)
	}

	layout, err = villages.Layout(ctx, head.ID, cityID)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.Buildings) != 1 {
		t.Fatalf("buildings %+v", layout.Buildings)
	}
	b := layout.Buildings[0]
	if b.ID != campID || b.X != cx || b.Y != cy || b.W != 1 || b.H != 2 || !b.Rotated || b.State != clientapi.StateUnderConstruction || b.FinishAt == "" {
		t.Errorf("layout building %+v", b)
	}
	// The whole turned footprint is taken, not only its corner lot.
	if r := place(head, "road", cx, cy+1, false); r.screen != screens.ScreenVillageRefusal || refusalKind(t, r.view) != screens.VillageOccupied {
		t.Errorf("the second lot of the turned footprint was free: %s %s", r.screen, r.view)
	}

	// The same layout is a stranger's coarse view: nothing under construction.
	coarse, err := villages.Layout(ctx, stranger.ID, cityID)
	if err != nil {
		t.Fatal(err)
	}
	if coarse.Detail != clientapi.DetailCoarse || len(coarse.Buildings) != 0 || len(coarse.Lots) != 5 {
		t.Errorf("stranger sees %+v (%d buildings)", coarse.Viewer, len(coarse.Buildings))
	}
	if coarse.Version == layout.Version {
		t.Error("the stranger's version equals the member's")
	}

	// ---- the head cancels it; the lot is free and can be built on again ----
	if _, err := village.Cancel(ctx, clientMeta(head, "settlement.build.cancel", "build.cancel"), handlers.VillageBuildingRequest{ID: campID}); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT status FROM settlement_buildings WHERE id = $1::uuid`, campID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("after cancel: %q %v", status, err)
	}
	resp, err = village.Cancel(ctx, clientMeta(head, "settlement.build.cancel", "build.cancel"), handlers.VillageBuildingRequest{ID: campID})
	if err != nil {
		t.Fatal(err)
	}
	if refusalKind(t, resp.View) != screens.VillageNotCancellable {
		t.Errorf("cancelling twice: %q %s", resp.Screen, resp.View)
	}
	// The scheduled completion of a cancelled build does nothing.
	if _, err := village.Built(ctx, clientMeta(head, "settlement.built", "built"), handlers.CrimeScheduledRequest{ReferenceID: campID}); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT status FROM settlement_buildings WHERE id = $1::uuid`, campID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("the scheduled completion revived a cancelled build: %q %v", status, err)
	}
	if r := place(head, "militia_camp", cx, cy, true); r.screen == screens.ScreenVillageRefusal {
		t.Fatalf("the cancelled lot cannot be built on again: %s", r.view)
	}
	layout, _ = villages.Layout(ctx, head.ID, cityID)
	if len(layout.Buildings) != 1 || layout.Buildings[0].ID == campID {
		t.Errorf("after re-placing: %+v", layout.Buildings)
	}

	// ---- a resident who is not the head may look, not build -----------------
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, stranger.ID, cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `UPDATE players SET city_id = NULL WHERE id = $1::uuid`, stranger.ID)
	})
	res, err := villages.Layout(ctx, stranger.ID, cityID)
	if err != nil || res.Detail != clientapi.DetailFull || res.Viewer.CanPlace {
		t.Fatalf("resident layout: %+v %v", res.Viewer, err)
	}
	resp, err = village.Place(ctx, clientMeta(stranger, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "road", Lot: screens.LotToken(0, 0, false), Confirm: screens.VillageBuildConfirm})
	if err != nil {
		t.Fatal(err)
	}
	if refusalKind(t, resp.View) != screens.VillageNotOfficeHolder {
		t.Errorf("a resident's placement: %q %s", resp.Screen, resp.View)
	}
	var roads int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'road'`, cityID).Scan(&roads); err != nil || roads != 0 {
		t.Errorf("a refused placement left %d roads behind (%v)", roads, err)
	}
}

type clientPlaceResult struct {
	screen string
	view   json.RawMessage
}

// refusalKind reads the kind of a village refusal view, empty for any other
// screen.
func refusalKind(t *testing.T, view json.RawMessage) string {
	t.Helper()
	var v struct {
		Kind string `json:"kind"`
	}
	if len(view) == 0 || json.Unmarshal(view, &v) != nil {
		return ""
	}
	return v.Kind
}

func registryOf(snap *content.Snapshot) *content.Registry {
	r := content.NewRegistry()
	r.Swap(snap)
	return r
}
