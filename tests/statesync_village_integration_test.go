//go:build integration

// Client state sync, phase P3 (docs/adr/0034): the residence and the
// settlement summary in a player's log, cut per kind of viewer, with the
// layout version the layout endpoint answers that viewer; a settlement event
// re-projects the summary of everyone it concerns (residents and visitors).
package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/statesync"
)

func TestStateSyncSettlementSummary(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	worlds := postgres.NewWorldRepository(pool)
	if _, err := worlds.Active(ctx); err == nil {
		t.Skip("a world is already active; this test only runs against a database with none")
	}
	w, err := worlds.Create(ctx, application.World{Seed: 20281002001, GeneratorVersion: worldgen.GeneratorVersion,
		ParamsHash: "integration-test-state-sync", CreatedBy: "integration-test"})
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
	bot := insertBot(t, pool)
	head := insertPlayer(t, pool)
	visitor := insertPlayer(t, pool)
	for _, id := range []string{head.ID, visitor.ID} {
		id := id
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			for _, stmt := range []string{`DELETE FROM player_updates WHERE player_id = $1::uuid`,
				`DELETE FROM entity_versions WHERE player_id = $1::uuid`} {
				_, _ = pool.Raw().Exec(ctx, stmt, id)
			}
		})
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	now := time.Now().UTC()
	var cityID string
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		out, err := tx.Settlements().Found(ctx, application.Founding{
			WorldID: w.ID, WorldCellID: 1, Tier: "village", Code: "v-sync-" + randomToken(t, 10), Name: "Sync Village",
			CountryCode: application.DefaultFoundingCountryCode, FounderPlayerID: head.ID, FoundedByGroupChatID: -newTelegramUserID(t),
			FoundedByBotID: bot, GroupLanguage: "fa", FoundedAt: now, ProtectedUntil: now.Add(time.Hour),
		})
		if err != nil {
			return err
		}
		cityID = out.CityID
		_, _, err = application.FoundOffice(ctx, tx, "village_head", out.JurisdictionID, 1, head.ID, now)
		return err
	}); err != nil {
		t.Fatalf("founding: %v", err)
	}
	t.Cleanup(func() { cleanupPlacementSettlement(t, pool, cityID) })
	seedTreasury(t, pool, cityID, 1_000)
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET residence_city_id = $2::uuid, city_id = $2::uuid WHERE id = $1::uuid`,
		head.ID, cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, visitor.ID, cityID); err != nil {
		t.Fatal(err)
	}

	// a standing hall and a watch hut: the overlay has something to say
	for i, code := range []string{"civic_hall", "watch_hut"} {
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
			VALUES (gen_random_uuid(), $1::uuid, $2, $3, 0, 'complete', now(), now())`, cityID, code, i*3); err != nil {
			t.Fatal(err)
		}
	}
	villages := &clientapi.VillageService{Settlements: postgres.NewSettlementReader(pool), Buildings: postgres.NewSettlementBuildingReader(pool),
		Content: registryOf(loadTestContent(t)), VillageGridLots: 5, Citizens: postgres.NewCitizenReader(pool),
		Overlay: postgres.NewVillageFacts(pool), StockBaseCapacity: 60}
	store := postgres.NewStateSync(pool, postgres.StateRules{EnergyRegenAmount: player.EnergyRegenAmount,
		EnergyRegenInterval: player.EnergyRegenInterval, NerveMax: 20, NerveRegenAmount: 1, NerveRegenInterval: time.Minute})
	store.Layouts = villages
	store.Overlays = villages
	svc := &statesync.Service{Store: store, Cfg: statesync.Config{Enabled: true, Epoch: "1", FanoutLimit: 50}}

	summary := func(p *application.Player) (statesync.ResidenceData, statesync.SettlementData, bool) {
		snap, err := svc.State(ctx, p.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		var res statesync.ResidenceData
		_, hasRes := snap.Entities[statesync.KindResidence][statesync.SelfID]
		if hasRes {
			_ = json.Unmarshal(snap.Entities[statesync.KindResidence][statesync.SelfID].D, &res)
		}
		var s statesync.SettlementData
		_ = json.Unmarshal(snap.Entities[statesync.KindSettlement][cityID].D, &s)
		return res, s, hasRes
	}

	res, s, ok := summary(head)
	if !ok || res.Settlement != cityID || !res.IsHead || !res.Resident || s.Viewer != statesync.ViewerHead || s.Treasury == nil {
		t.Fatalf("the head's residence %+v and summary %+v", res, s)
	}
	headLayout := func() string {
		// the layout the endpoint answers the head; only its version matters
		v, _, err := villages.LayoutVersions(ctx, cityID)
		if err != nil {
			t.Fatal(err)
		}
		return v.Head
	}
	if s.LayoutVersion == "" || s.LayoutVersion != headLayout() {
		t.Fatalf("the head's summary says layout %q, the layout is %q", s.LayoutVersion, headLayout())
	}
	// the per-viewer overlay: the head acts on the hall, the visitor only looks
	var hall *statesync.BuildingOverlay
	for i := range s.Buildings {
		if s.Buildings[i].Role == "governance" {
			hall = &s.Buildings[i]
		}
	}
	if hall == nil || len(hall.Actions) < 3 || hall.Actions[0] != statesync.ActionInfo {
		t.Fatalf("the head's overlay of the hall: %+v (all %+v)", hall, s.Buildings)
	}
	hasAct := func(o statesync.BuildingOverlay, a string) bool {
		for _, x := range o.Actions {
			if x == a {
				return true
			}
		}
		return false
	}
	if !hasAct(*hall, statesync.ActionTreasury) || !hasAct(*hall, statesync.ActionResearch) {
		t.Fatalf("the head should treasure and research at the hall: %v", hall.Actions)
	}
	if snap, err := svc.State(ctx, head.ID, nil); err != nil {
		t.Fatal(err)
	} else if g, ok := snap.Entities[statesync.KindGoal][statesync.SelfID]; ok {
		var goal statesync.GoalData
		_ = json.Unmarshal(g.D, &goal)
		if goal.Code == "" || goal.GoTo == "" || goal.Target <= 0 {
			t.Fatalf("the goal entity %+v", goal)
		}
	}
	_, vs, vres := summary(visitor)
	for _, o := range vs.Buildings {
		if len(o.Actions) != 1 || o.Actions[0] != statesync.ActionInfo || o.CanUpgrade {
			t.Fatalf("a visitor's overlay must be info only: %+v", o)
		}
	}
	if vres || vs.Viewer != statesync.ViewerPublic || vs.Treasury != nil {
		t.Fatalf("a visitor's summary %+v (residence %v)", vs, vres)
	}

	// the village is renamed by someone else: one settlement event, and both
	// the head's and the visitor's summaries move on, each to its own version
	if _, err := pool.Raw().Exec(ctx, `UPDATE cities SET name = 'Sync Hamlet' WHERE id = $1::uuid`, cityID); err != nil {
		t.Fatal(err)
	}
	headPTS, visitorPTS := maxPTSOf(t, pool, head.ID), maxPTSOf(t, pool, visitor.ID)
	env := &envelope.Envelope{Metadata: envelope.Metadata{RequestID: newUUID(t), EventID: newUUID(t), ReceivedAt: time.Now().UTC()},
		Payload: json.RawMessage(`{"settlement_id":"` + cityID + `"}`)}
	if err := svc.HandleEvent(ctx, env); err != nil {
		t.Fatal(err)
	}
	if maxPTSOf(t, pool, head.ID) <= headPTS || maxPTSOf(t, pool, visitor.ID) <= visitorPTS {
		t.Fatal("a settlement event did not reach its residents and visitors")
	}
	_, s2, _ := summary(head)
	if s2.Name != "Sync Hamlet" || s2.LayoutVersion == s.LayoutVersion || s2.LayoutVersion != headLayout() {
		t.Fatalf("after the rename the head's summary is %+v", s2)
	}
	versions, _, _ := villages.LayoutVersions(ctx, cityID)
	if _, vs2, _ := summary(visitor); vs2.LayoutVersion != versions.Public {
		t.Fatalf("the visitor's version %q, the public layout %q", vs2.LayoutVersion, versions.Public)
	}
}

func maxPTSOf(t *testing.T, pool *postgres.Pool, playerID string) int64 {
	t.Helper()
	var n int64
	_ = pool.Raw().QueryRow(testCtx(t), `SELECT COALESCE(max(pts), 0) FROM player_updates WHERE player_id = $1::uuid`, playerID).Scan(&n)
	return n
}
