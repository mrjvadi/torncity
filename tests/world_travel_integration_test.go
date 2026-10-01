//go:build integration

// Integration test of world-derived travel (docs/adr/0034-world-travel.md):
// Support and founded villages are joined by no route in routes.yml, so a
// journey between them is priced from where they stand on the generated
// world. It runs the whole path against a real PostgreSQL: the destination
// list, the choice of transport, the departure and its fare, the arrival that
// moves the player (and only their location, not their home), the direct trip
// «سفر به این روستا» from a village's own group, and its refusals.
package tests

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// worldTransport is the live world transport over one snapshot.
type worldTransport struct {
	snap  *content.Snapshot
	reach map[string]int
}

func (w worldTransport) Derived(km int) ([]handlers.TransportOption, int) {
	var out []handlers.TransportOption
	for _, o := range w.snap.DerivedTransport(km, w.reach) {
		out = append(out, handlers.TransportOption{Mode: o.Mode, Name: o.Name, DistanceKM: o.DistanceKM})
	}
	return out, w.snap.Version()
}

func (w worldTransport) EmblemText(e application.EmblemCodes) string {
	return handlers.EmblemTextOf(w.snap, e)
}

// startButtons are the "travel:start:<city>:<mode>:<fare>" buttons of a
// response, by mode.
func startButtons(resp *presenter.Response, city string) map[string]string {
	out := map[string]string{}
	if resp == nil || resp.Keyboard == nil {
		return out
	}
	prefix := "travel:start:" + city + ":"
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if rest, ok := strings.CutPrefix(b.CallbackData, prefix); ok {
				mode, fare, _ := strings.Cut(rest, ":")
				out[mode] = fare
			}
		}
	}
	return out
}

func TestWorldTravelSupportAndVillages(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	requireGovernance(t, pool)
	ctx := testCtx(t)

	var exists bool
	if err := pool.Raw().QueryRow(ctx, `SELECT to_regclass('public.worlds') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Skip("worlds does not exist; apply migration 0042 first")
	}
	worlds := postgres.NewWorldRepository(pool)
	if _, err := worlds.Active(ctx); err == nil {
		t.Skip("a world is already active; this test only runs against a database with none")
	}

	wgPack, err := content.LoadWorldGen("../configs/content")
	if err != nil {
		t.Fatal(err)
	}
	wgContent, err := wgPack.ToContent()
	if err != nil {
		t.Fatal(err)
	}
	params := worldgen.DefaultParams()
	params.CellCount = 4000
	w, err := worlds.Create(ctx, application.World{
		Seed: 20280930042, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: "integration-test", CreatedBy: "integration-test",
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

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	snap := loadTestContent(t)
	src := staticContentSource{snap: snap}
	now := func() time.Time { return time.Now().UTC() }

	// Two villages, founded by two groups.
	foundH := handlers.NewSettlementsHandler(uow, workIDs{t}, nil, worldCache, src, gametime.Scale(1),
		wsettle.Params{MinSpawnDistanceKm: 30, ThreatRadiusKm: 150, SearchMaxCells: 2000, SearchMaxAttempts: 50,
			ExcludedBiomes: []string{"polar_ice"}, MaxAbsLatitudeDeg: 70},
		168*time.Hour, 5, time.Hour, testFoundingConfig(), now)
	bot := insertBot(t, pool)
	found := func() (envelope.Metadata, string) {
		founder := insertPlayer(t, pool)
		meta := validMeta(t)
		meta.BotID, meta.TelegramUserID = bot, founder.TelegramUserID
		meta.TelegramChatID, meta.ChatType = -newTelegramUserID(t), "group"
		meta.Command, meta.Action = "settlement.found", "found"
		chat := meta.TelegramChatID
		// Registered after the founder's own cleanup, so it runs before it.
		cleanupFounding(t, pool, func() []int64 { return []int64{chat} })
		foundVillage(t, pool, foundH, meta)
		var id string
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&id); err != nil {
			t.Fatalf("reading the founded city: %v", err)
		}
		return meta, id
	}
	groupA, villageA := found()
	groupB, villageB := found()
	var codeA, codeB string
	for id, dst := range map[string]*string{villageA: &codeA, villageB: &codeB} {
		if err := pool.Raw().QueryRow(ctx, `SELECT code FROM cities WHERE id = $1::uuid`, id).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}

	spots := map[string]handlers.GeoPoint{"support": {LatDeg: 32.30, LonDeg: -47.70}}
	reach := map[string]int{"walk": 60, "cart": 500, "car": 21000}
	routes := handlers.NewWorldRoutes(worldCache, spots, params.PlanetRadiusKm, 60, worldTransport{snap: snap, reach: reach})
	travelH := handlers.NewTravelHandler(uow, transportIDs{t}, nil, postgres.NewCityRepository(pool), snapshotNetwork{snap},
		postgres.NewPolicyReader(pool, nil), 60, 25, 24*time.Hour, nil).WithWorld(routes)
	mapH := handlers.NewMapHandler(uow, nil, postgres.NewCityRepository(pool), postgres.NewTravelRepository(pool),
		emptyRoutes{}, handlers.DefaultPageSize, nil).WithWorld(routes)

	support := cityIDByCode(t, pool, "support")
	rider := travelPlayer(t, pool, support, 50_000_000)
	residence := func() string {
		var r *string
		if err := pool.Raw().QueryRow(ctx, `SELECT residence_city_id::text FROM players WHERE id = $1::uuid`, rider.ID).Scan(&r); err != nil {
			t.Fatal(err)
		}
		if r == nil {
			return ""
		}
		return *r
	}
	homeBefore := residence()
	location := func() string {
		var c *string
		if err := pool.Raw().QueryRow(ctx, `SELECT city_id::text FROM players WHERE id = $1::uuid`, rider.ID).Scan(&c); err != nil {
			t.Fatal(err)
		}
		if c == nil {
			return ""
		}
		return *c
	}

	// The destination list from Support: both villages, no Support, priced.
	resp, err := mapH.List(ctx, travelMeta(rider, "map"), handlers.PageRequest{})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	view := viewOf(t, resp)
	dests, _ := view["destinations"].([]any)
	seen := map[string]map[string]any{}
	for _, d := range dests {
		m := d.(map[string]any)
		seen[m["code"].(string)] = m
	}
	for _, code := range []string{codeA, codeB} {
		d, ok := seen[code]
		if !ok {
			t.Fatalf("village %s is not in the destination list %v", code, dests)
		}
		if d["village"] != true || d["settlement_id"] == "" || d["distance_km"].(float64) < 1 || d["wait_seconds"].(float64) <= 0 {
			t.Errorf("destination %s = %v", code, d)
		}
	}
	if _, ok := seen["support"]; ok {
		t.Error("Support is listed as a destination from Support")
	}

	// Support -> village A: the choice of transport, then a departure.
	resp, err = rr(travelH.Options(ctx, travelMeta(rider, "opts"), handlers.TravelOptionsRequest{City: codeA}))
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	buttons := startButtons(resp, codeA)
	if len(buttons) == 0 {
		t.Fatalf("no way to travel to the village: %+v", resp)
	}
	var mode, fare string
	for m, f := range buttons {
		mode, fare = m, f
		break
	}
	cashBefore := cashBalance(t, pool, application.AccountPlayerCash, rider.ID)
	if _, err := travelH.Start(ctx, travelMeta(rider, "go"), handlers.StartTravelRequest{City: codeA, Mode: mode, Max: fare, Method: "cash"}); err != nil {
		t.Fatalf("departure: %v", err)
	}
	var paid int64
	fmt.Sscanf(fare, "%d", &paid) //nolint:errcheck // a miss leaves zero, caught by the cash check below
	if got := cashBalance(t, pool, application.AccountPlayerCash, rider.ID); got != cashBefore-paid {
		t.Errorf("cash %d, want %d", got, cashBefore-paid)
	}
	arrive := func(label string) {
		t.Helper()
		var travelID string
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM travels WHERE player_id = $1::uuid AND status = 'in_transit'`, rider.ID).Scan(&travelID); err != nil {
			t.Fatalf("%s: no journey in transit: %v", label, err)
		}
		m := travelMeta(rider, "arrive-"+label)
		m.Command, m.TelegramUserID = "travel.arrive", 0
		if _, err := travelH.Complete(ctx, m, handlers.ArriveTravelRequest{ActorID: rider.ID, ReferenceID: travelID}); err != nil {
			t.Fatalf("%s: arrival: %v", label, err)
		}
	}
	arrive("to-a")
	if got := location(); got != villageA {
		t.Errorf("after arriving the player is in %q, want village A %q", got, villageA)
	}
	if got := residence(); got != homeBefore {
		t.Errorf("travelling moved the player's home from %q to %q", homeBefore, got)
	}
	// The web asks where the player stands: a foreign village, not home.
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	worldSvc := &clientapi.WorldService{Source: worldCache, CacheEntries: 8, RecheckEvery: time.Minute}
	villages := &clientapi.VillageService{Settlements: postgres.NewSettlementReader(pool),
		Buildings: postgres.NewSettlementBuildingReader(pool), World: worldSvc, Content: registryOf(snap), VillageGridLots: 5, Now: now}
	web := &clientapi.World{Players: postgres.NewPlayerRepository(pool, testDefaultLanguage),
		Cities: postgres.NewCityRepository(pool), CityCodes: postgres.NewCityRepository(pool),
		Companies: postgres.NewCompanyRepository(pool), Content: registryOf(snap), Villages: villages, Msgs: catalog, Now: now,
		CitySpots: map[string]clientapi.Spot{"support": {Lat: 32.30, Lon: -47.70}}}
	boot, err := web.Bootstrap(ctx, clientapi.Principal{PlayerID: rider.ID, Lang: "fa"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	loc := boot.Location
	if loc == nil || loc.Kind != clientapi.LocationSettlement || loc.SettlementID != villageA || loc.Code != codeA ||
		loc.Home || loc.Centre == nil || loc.LayoutPath == "" || loc.WorldCell == nil {
		t.Errorf("bootstrap location in village A = %+v", loc)
	}
	var announced int
	if err := pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE subject = 'game.event.travel.completed.v1' AND payload->>'player_id' = $1 AND payload->>'to_city_id' = $2`,
		rider.ID, villageA).Scan(&announced); err != nil || announced != 1 {
		t.Errorf("arrival events for the village = %d, %v; want 1", announced, err)
	}

	// From village A, in group B: straight to village B, privately.
	inGroup := func(group envelope.Metadata, id string) envelope.Metadata {
		m := travelMeta(rider, id)
		m.ChatType, m.TelegramChatID, m.BotID = "group", group.TelegramChatID, group.BotID
		m.Command = "travel.here"
		return m
	}
	resp, err = rr(travelH.Here(ctx, inGroup(groupB, "here-b")))
	if err != nil {
		t.Fatalf("here: %v", err)
	}
	if !resp.Private || !strings.Contains(resp.Text, "travel.options_title") || len(startButtons(resp, codeB)) == 0 {
		t.Errorf("the direct trip to village B is not a private choice of transport: %+v", resp)
	}
	// In the village's own group while standing in it: already there.
	resp, err = rr(travelH.Here(ctx, inGroup(groupA, "here-a")))
	if err != nil || resp == nil || !strings.Contains(resp.Text, "travel.here.already_there") {
		t.Errorf("standing in village A and asking in its group: %+v, %v", resp, err)
	}
	// A group with no village: a polite refusal.
	stranger := groupB
	stranger.TelegramChatID = -newTelegramUserID(t)
	resp, err = rr(travelH.Here(ctx, inGroup(stranger, "here-none")))
	if err != nil || resp == nil || !strings.Contains(resp.Text, "travel.here.no_village") {
		t.Errorf("a group with no village: %+v, %v", resp, err)
	}
	// Not in a group at all.
	pv := travelMeta(rider, "here-pv")
	pv.Command = "travel.here"
	resp, err = rr(travelH.Here(ctx, pv))
	if err != nil || resp == nil || !strings.Contains(resp.Text, "travel.here.group_only") {
		t.Errorf("the words sent in private: %+v, %v", resp, err)
	}

	// Village A -> village B by the group alias, then back to Support.
	resp, err = rr(travelH.Here(ctx, inGroup(groupB, "here-b2")))
	if err != nil {
		t.Fatal(err)
	}
	buttons = startButtons(resp, codeB)
	for m, f := range buttons {
		mode, fare = m, f
		break
	}
	if _, err := travelH.Start(ctx, travelMeta(rider, "go-b"), handlers.StartTravelRequest{City: codeB, Mode: mode, Max: fare, Method: "cash"}); err != nil {
		t.Fatalf("departure to B: %v", err)
	}
	arrive("to-b")
	if got := location(); got != villageB {
		t.Errorf("the player is in %q, want village B %q", got, villageB)
	}
	resp, err = rr(travelH.Options(ctx, travelMeta(rider, "opts-s"), handlers.TravelOptionsRequest{City: "support"}))
	if err != nil {
		t.Fatalf("options to Support: %v", err)
	}
	buttons = startButtons(resp, "support")
	if len(buttons) == 0 {
		t.Fatalf("no way back to Support: %+v", resp)
	}
	for m, f := range buttons {
		mode, fare = m, f
		break
	}
	if _, err := travelH.Start(ctx, travelMeta(rider, "go-s"), handlers.StartTravelRequest{City: "support", Mode: mode, Max: fare, Method: "cash"}); err != nil {
		t.Fatalf("departure to Support: %v", err)
	}
	arrive("to-s")
	if got := location(); got != support {
		t.Errorf("the player is in %q, want Support %q", got, support)
	}
	boot, err = web.Bootstrap(ctx, clientapi.Principal{PlayerID: rider.ID, Lang: "fa"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if loc := boot.Location; loc == nil || loc.Kind != clientapi.LocationCity || loc.Code != "support" || loc.Centre == nil ||
		loc.Centre.Lat != 32.30 || loc.SettlementID != "" {
		t.Errorf("bootstrap location in Support = %+v", loc)
	}
	// Back in Support the villages are offered again.
	resp, err = rr(travelH.Options(ctx, travelMeta(rider, "o1"), handlers.TravelOptionsRequest{City: codeA}))
	if err != nil || len(startButtons(resp, codeA)) == 0 {
		t.Errorf("no options to village A from Support after coming back: %v, %v", resp, err)
	}
}

// emptyRoutes is the content route network of the shipped world for the
// map: nothing is joined by a content route.
type emptyRoutes struct{}

func (emptyRoutes) DistanceBetween(from, to string) (int, error) {
	return 0, fmt.Errorf("no content route between %q and %q", from, to)
}
func (emptyRoutes) Has(string) bool { return false }
