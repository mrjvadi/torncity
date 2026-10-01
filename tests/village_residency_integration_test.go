//go:build integration

// Integration test of village residency: the founder becomes a resident in
// the founding transaction, every other member joins with settlement.join
// (ask, then confirm), and leaves with settlement.leave; a cool-down runs
// from every change of residence; the head cannot leave; the settlement
// channel appears in a client's realtime token once its player lives in the
// village; and the population counts residents.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/content/testworld"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func TestVillageResidency(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

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
		Seed: 20280930001, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: "integration-test-residency", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("creating the test world: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(c, `DELETE FROM worlds WHERE id = $1::uuid`, w.ID); err != nil {
			t.Errorf("cleaning up the test world: %v", err)
		}
	})

	worldCache := application.NewWorldCache(worlds, params, wgContent)
	bot := insertBot(t, pool)
	founder, member, other := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	support := cityIDByCode(t, pool, "support")
	for _, p := range []*application.Player{founder, member, other} {
		placePlayer(t, pool, p.ID, support, "")
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	snap := loadTestContent(t)
	source := staticContentSource{snap: snap}
	cities := postgres.NewCityRepository(pool)
	clk := &testClock{now: time.Now().UTC()}
	const cooldown = 72 * time.Hour

	settlements := handlers.NewSettlementsHandler(uow, workIDs{t}, nil, worldCache, source, gametime.Scale(1),
		wsettle.Params{MinSpawnDistanceKm: 30, ThreatRadiusKm: 150, SearchMaxCells: 2000, SearchMaxAttempts: 50,
			ExcludedBiomes: []string{"polar_ice"}, MaxAbsLatitudeDeg: 70},
		168*time.Hour, 5, time.Second, testFoundingConfig(), clk.Now)
	village := handlers.NewVillageHandler(uow, workIDs{t}, nil, source, worldCache, cities, gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			ResidenceCooldown: cooldown, HomeCityCode: "support",
		}, time.Hour, clk.Now)

	groupChatID := -newTelegramUserID(t)
	metaOf := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.BotID = bot
		m.TelegramUserID = p.TelegramUserID
		m.PlayerID = p.ID
		m.TelegramChatID = groupChatID
		m.ChatType = "group"
		m.Command = command
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	residenceOf := func(p *application.Player) string {
		var id string
		if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(residence_city_id::text, '') FROM players WHERE id = $1::uuid`, p.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	var cityID string
	t.Cleanup(func() {
		if cityID == "" {
			return
		}
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		var jurisdictionID string
		_ = pool.Raw().QueryRow(c, `SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, cityID).Scan(&jurisdictionID)
		for _, p := range []*application.Player{founder, member, other} {
			_, _ = pool.Raw().Exec(c, `UPDATE players SET residence_city_id = $2::uuid, city_id = $2::uuid WHERE id = $1::uuid`, p.ID, support)
			_, _ = pool.Raw().Exec(c, `DELETE FROM client_devices WHERE player_id = $1::uuid`, p.ID)
		}
		for _, stmt := range []string{
			`DELETE FROM outbox WHERE payload->>'settlement_id' = $1 OR payload->>'to_city_id' = $1 OR payload->>'from_city_id' = $1`,
			`DELETE FROM game_actions WHERE reference_type = 'settlement' AND reference_id = $1::uuid`,
			`DELETE FROM settlement_literacy WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid`,
			`DELETE FROM city_group_links WHERE city_id = $1::uuid`,
			`DELETE FROM village_currency_reservations WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_founding_drafts WHERE settlement_id = $1::uuid OR chat_id IN (SELECT founded_by_group_id FROM cities WHERE id = $1::uuid)`,
		} {
			if _, err := pool.Raw().Exec(c, stmt, cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if jurisdictionID != "" {
			_, _ = pool.Raw().Exec(c, `DELETE FROM offices WHERE jurisdiction_id = $1::uuid`, jurisdictionID)
		}
		if _, err := pool.Raw().Exec(c, `DELETE FROM cities WHERE id = $1::uuid`, cityID); err != nil {
			t.Errorf("cleaning up the founded city: %v", err)
		}
		if jurisdictionID != "" {
			_, _ = pool.Raw().Exec(c, `DELETE FROM jurisdictions WHERE id = $1::uuid`, jurisdictionID)
		}
	})

	// 1. The founder is a resident the moment the village exists, and the
	// move is on the outbox for realtime and presence.
	foundVillage(t, pool, settlements, metaOf(founder, "settlement.found"))
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, groupChatID).Scan(&cityID); err != nil {
		t.Fatalf("reading the founded city: %v", err)
	}
	if got := residenceOf(founder); got != cityID {
		t.Fatalf("the founder's residence = %s, want the new village %s", got, cityID)
	}
	movesTo := func(via string) int {
		var n int
		if err := pool.Raw().QueryRow(ctx,
			`SELECT count(*) FROM outbox WHERE subject LIKE 'game.event.residence.changed.%' AND payload->>'to_city_id' = $1 AND payload->>'via' = $2`,
			cityID, via).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if movesTo("founding") != 1 {
		t.Errorf("residence.changed (founding) events = %d, want 1", movesTo("founding"))
	}
	count := func() int64 {
		var n int64
		uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			var err error
			n, err = tx.Settlements().ResidentCount(ctx, cityID)
			return err
		})
		return n
	}
	if count() != 1 {
		t.Errorf("residents after founding = %d, want 1", count())
	}

	// 2. A member who lives in Support sees the join button; the head, who
	// is a resident, does not.
	overview := func(p *application.Player) *presenterView {
		resp, err := rrm(metaOf(p, "settlement.overview"))(village.Overview(ctx, metaOf(p, "settlement.overview")))
		if err != nil {
			t.Fatalf("overview: %v", err)
		}
		return &presenterView{text: resp.Text, buttons: buttonData(resp.Keyboard)}
	}
	if o := overview(member); !o.has(screens.AddrVillageJoin) || o.has(screens.AddrVillageLeave) {
		t.Errorf("a non-resident's overview buttons = %v, want the join button only", o.buttons)
	}
	if o := overview(founder); o.has(screens.AddrVillageJoin) || !o.has(screens.AddrVillageLeave) {
		t.Errorf("a resident's overview buttons = %v, want the leave button only", o.buttons)
	}

	// 3. Joining asks first and changes nothing; the outside of a group has no
	// village to join.
	ask, err := rrm(metaOf(member, "settlement.join"))(village.Join(ctx, metaOf(member, "settlement.join"), handlers.VillageJoinRequest{}))
	if err != nil || !strings.Contains(ask.Text, "village.residence.join.ask_title") {
		t.Fatalf("join (ask): %v %+v", err, ask)
	}
	if residenceOf(member) != support {
		t.Fatal("asking to join moved the player")
	}
	private := metaOf(member, "settlement.join")
	private.ChatType, private.TelegramChatID = "private", member.TelegramUserID
	if r, err := rrm(private)(village.Join(ctx, private, handlers.VillageJoinRequest{})); err != nil || !strings.Contains(r.Text, "village.refusal.no_settlement") {
		t.Errorf("join outside a group: %v %+v", err, r)
	}

	// 4. Confirming moves the home, stamps the cool-down clock and announces
	// the move.
	done, err := rrm(metaOf(member, "settlement.join"))(village.Join(ctx, metaOf(member, "settlement.join"), handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm}))
	if err != nil || !strings.Contains(done.Text, "village.residence.join.done_title") {
		t.Fatalf("join (confirm): %v %+v", err, done)
	}
	if residenceOf(member) != cityID {
		t.Fatalf("the member's residence = %s, want %s", residenceOf(member), cityID)
	}
	var here string
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(city_id::text, '') FROM players WHERE id = $1::uuid`, member.ID).Scan(&here); err != nil || here != cityID {
		t.Errorf("a member who joins stands in the village, not in Support: %q %v", here, err)
	}
	if movesTo("join") != 1 {
		t.Errorf("residence.changed (join) events = %d, want 1", movesTo("join"))
	}
	if count() != 2 {
		t.Errorf("residents after the join = %d, want 2", count())
	}
	if o := overview(member); o.has(screens.AddrVillageJoin) {
		t.Errorf("the member's overview after joining = %+v", o)
	}

	// 4b. A content load never retires a founded village: it is not in
	// cities.yml, and it now has residents (the live 2026-09-30 deploy was
	// refused with "a city in use would be removed" until this held).
	{
		pack, err := content.Load("../configs/content")
		if err != nil {
			t.Fatalf("loading content: %v", err)
		}
		pack = testworld.Extend(pack)
		applyContent(t, pool, pack, "reload with a founded, inhabited village")
	}

	// 5. Refusals: already a resident; the cool-down; the head cannot leave;
	// someone who is not a resident cannot leave.
	if r, err := rrm(metaOf(member, "settlement.join"))(village.Join(ctx, metaOf(member, "settlement.join"), handlers.VillageJoinRequest{})); err != nil || !strings.Contains(r.Text, "village.refusal.already_resident") {
		t.Errorf("join twice: %v %+v", err, r)
	}
	if r, err := rrm(metaOf(member, "settlement.leave"))(village.Leave(ctx, metaOf(member, "settlement.leave"), handlers.VillageJoinRequest{})); err != nil || !strings.Contains(r.Text, "village.refusal.residence_cooldown") {
		t.Errorf("leave during the cool-down: %v %+v", err, r)
	}
	if r, err := rrm(metaOf(other, "settlement.leave"))(village.Leave(ctx, metaOf(other, "settlement.leave"), handlers.VillageJoinRequest{})); err != nil || !strings.Contains(r.Text, "village.refusal.not_resident") {
		t.Errorf("a non-resident leaving: %v %+v", err, r)
	}
	// The cool-down bites the other direction too: a player who just moved
	// (here: the founder, whose founding stamped it) cannot be moved by join.
	clk.Advance(cooldown + time.Minute)
	if r, err := rrm(metaOf(founder, "settlement.leave"))(village.Leave(ctx, metaOf(founder, "settlement.leave"), handlers.VillageJoinRequest{})); err != nil || !strings.Contains(r.Text, "village.refusal.holds_office") {
		t.Errorf("the head leaving: %v %+v", err, r)
	}
	if residenceOf(founder) != cityID {
		t.Error("the head was moved out of the village")
	}

	// 6. The realtime token of a signed-in client gains the settlement channel
	// once the player lives in the village, and loses it when they leave.
	if rdb := redisIfAvailable(t); rdb != nil {
		ensureClientTables(t, pool)
		catalog, err := i18n.Load("../configs/locales")
		if err != nil {
			t.Fatal(err)
		}
		codes := &memLinkCodes{codes: map[string]application.ClientLinkClaim{}, byReq: map[string]application.ClientLinkCode{}}
		auth, err := clientapi.NewAuth(clientapi.AuthConfig{
			Secret: []byte("integration-secret-integration-secret"), AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour,
			MaxDevices: 5, TelegramMaxAge: time.Hour, DefaultLanguage: testDefaultLanguage,
			Codes: codes, Devices: postgres.NewClientDevices(pool), Players: postgres.NewPlayerRepository(pool, testDefaultLanguage),
			Once: memLimiter{}, NewID: clientapi.NewID,
		})
		if err != nil {
			t.Fatal(err)
		}
		svc := &application.PresenceService{Store: infraredis.NewPresence(rdb, time.Minute), Repo: postgres.NewPresenceRepository(pool), RosterLimit: 50}
		srv := httptest.NewServer(clientapi.NewServer(clientapi.ServerConfig{
			Auth: auth, Limits: memLimiter{}, Realtime: centrifugo.NewTokens("x", time.Minute), Msgs: catalog,
			Presence: svc, Versions: infraredis.NewSettlementVersions(rdb, time.Hour), PresenceTTL: time.Minute,
			SignInsPerMinute: 100, CommandsPerMinute: 100, MaxBodyBytes: 16384,
		}).Handler())
		defer srv.Close()
		channelsOf := func(p *application.Player) []any {
			codes.mu.Lock()
			codes.codes["WXYZ2345"] = application.ClientLinkClaim{PlayerID: p.ID, BotID: bot}
			codes.mu.Unlock()
			b, _ := json.Marshal(map[string]string{"code": "WXYZ2345", "device_name": "itest"})
			resp, err := http.Post(srv.URL+"/api/v1/auth/link", "application/json", bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
			tok, _ := out["access_token"].(string)
			req, _ := http.NewRequest("GET", srv.URL+"/api/v1/realtime/token", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			r2, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r2.Body.Close()
			var tokOut map[string]any
			_ = json.NewDecoder(r2.Body).Decode(&tokOut)
			ch, _ := tokOut["channels"].([]any)
			return ch
		}
		if ch := channelsOf(member); len(ch) != 2 || ch[1] != "settlement:"+cityID {
			t.Errorf("the resident's channels = %v, want their own and settlement:%s", ch, cityID)
		}
		if ch := channelsOf(other); len(ch) != 1 {
			t.Errorf("a Support resident's channels = %v, want only their own", ch)
		}
	}

	// 7. Once the cool-down has run, leaving asks, then sends the player back
	// to Support; the village loses a resident and the outbox says so.
	askLeave, err := rrm(metaOf(member, "settlement.leave"))(village.Leave(ctx, metaOf(member, "settlement.leave"), handlers.VillageJoinRequest{}))
	if err != nil || !strings.Contains(askLeave.Text, "village.residence.leave.ask_title") {
		t.Fatalf("leave (ask): %v %+v", err, askLeave)
	}
	if residenceOf(member) != cityID {
		t.Fatal("asking to leave moved the player")
	}
	left, err := rrm(metaOf(member, "settlement.leave"))(village.Leave(ctx, metaOf(member, "settlement.leave"), handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm}))
	if err != nil || !strings.Contains(left.Text, "village.residence.leave.done_title") {
		t.Fatalf("leave (confirm): %v %+v", err, left)
	}
	if residenceOf(member) != support {
		t.Errorf("the member's residence after leaving = %s, want Support", residenceOf(member))
	}
	if count() != 1 {
		t.Errorf("residents after leaving = %d, want 1", count())
	}
	// Moving again straight away is the cool-down's job.
	if r, err := rrm(metaOf(member, "settlement.join"))(village.Join(ctx, metaOf(member, "settlement.join"), handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm})); err != nil || !strings.Contains(r.Text, "village.refusal.residence_cooldown") {
		t.Errorf("rejoining straight away: %v %+v", err, r)
	}
	if residenceOf(member) != support {
		t.Error("a join during the cool-down moved the player")
	}
}

// presenterView is a response reduced to what the assertions read.
type presenterView struct {
	text    string
	buttons []string
}

func (v *presenterView) has(addr string) bool {
	for _, b := range v.buttons {
		if b == addr || strings.HasPrefix(b, addr+":") {
			return true
		}
	}
	return false
}

// redisIfAvailable is requireRedis without the skip: the token part of the
// test runs only where Redis does.
func redisIfAvailable(t *testing.T) *infraredis.Client {
	t.Helper()
	if strings.TrimSpace(os.Getenv(envRedis)) == "" {
		return nil
	}
	return requireRedis(t)
}

// buttonData lists the callback data of a keyboard.
func buttonData(kb *presenter.Keyboard) []string {
	var out []string
	if kb == nil {
		return out
	}
	for _, row := range kb.Rows {
		for _, b := range row {
			if b.CallbackData != "" {
				out = append(out, b.CallbackData)
			}
		}
	}
	return out
}
