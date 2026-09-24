//go:build integration

// Integration tests for going somewhere to do something
// (internal/application/handlers/places_then.go) and for handing a group's
// payment off to the private chat (internal/gateway/groups/deeplink.go),
// against a live PostgreSQL through the real handlers and unit of work.
//
// Every row written here is removed again: the player's walks, jobs, shifts,
// scheduled actions, ledger rows and the outbox rows their commands wrote.
package tests

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// standAtWorkplace puts a player at the place their career's category is
// worked at in a city, where a shift can start.
func standAtWorkplace(t *testing.T, pool *postgres.Pool, registry *content.Registry, playerID, cityCode, career string) {
	t.Helper()
	snap := registry.Current()
	def, ok := snap.CareerDef(career)
	if !ok {
		t.Fatalf("no career %q in the active content", career)
	}
	wp, ok := snap.CityMap(cityCode).ForWork(def.Category)
	if !ok {
		return // a city without places: a shift starts anywhere in it
	}
	code := any(wp.Code)
	if wp.Default {
		code = nil
	}
	if _, err := pool.Raw().Exec(testCtx(t), `UPDATE players SET place_code = $2, place_since = now() WHERE id = $1::uuid`,
		playerID, code); err != nil {
		t.Fatal(err)
	}
}

// purgeWalksFor removes a player's walks and the commands their arrivals
// published.
func purgeWalksFor(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	for _, stmt := range []string{
		`DELETE FROM outbox WHERE metadata->>'player_id' = $1 OR payload::text LIKE '%' || $1 || '%'`,
		`DELETE FROM place_moves WHERE player_id = $1::uuid`,
	} {
		if _, err := pool.Raw().Exec(ctx, stmt, playerID); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(stmt), err)
		}
	}
}

// The owner's report: a shift started from another place of the city left
// the player where they were. Now the start walks them to the workplace; the
// arrival — delivered twice — publishes job.work for them once; the game runs
// it — delivered twice — and one shift starts, with the player at the
// workplace, and they are still there when it ends.
func TestAShiftFromAnotherPlaceWalksThereAndStartsOnce(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := workRegistry(t, pool)
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	if len(registry.Current().CityMap(city.Code).Places) == 0 {
		t.Skip("the active content has no places; run `admin content load`")
	}

	p := insertPlayer(t, pool)
	t.Cleanup(func() { purgeLedgerFor(t, pool, p.ID) })
	t.Cleanup(func() { purgeWorkFor(t, pool, p.ID) })
	t.Cleanup(func() { purgeWalksFor(t, pool, p.ID) })
	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'university', place_since = now()
		  WHERE id = $1::uuid`, p.ID, city.ID); err != nil {
		t.Fatal(err)
	}

	clock := time.Now().UTC()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	jobs := handlers.NewJobsHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, 5, time.Hour, now)
	places := handlers.NewPlacesHandler(uow, workIDs{t}, nil, registry, cities, gameScale, time.Hour, now)
	metaFor := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = p.TelegramUserID
		m.TelegramChatID = p.TelegramUserID
		m.Command = command
		m.Language = "en"
		return m
	}

	if _, err := jobs.Apply(ctx, metaFor("job.apply"), handlers.JobRequest{Role: "retail"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	walk, err := jobs.Work(ctx, metaFor("job.work"))
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if walk == nil || !strings.Contains(walk.Text, "place.then.work") {
		t.Fatalf("the start from the university = %+v, want a walk to work", walk)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM shift_sessions WHERE player_id = $1::uuid`, p.ID); n != 0 {
		t.Fatalf("a shift started away from the workplace (%d)", n)
	}
	var (
		moveID, to string
		arrives    time.Time
		payload    []byte
	)
	if err := pool.Raw().QueryRow(ctx,
		`SELECT m.id::text, m.to_place, m.arrives_at, a.payload FROM place_moves m JOIN game_actions a ON a.id = m.game_action_id
		  WHERE m.player_id = $1::uuid AND m.status = 'moving'`, p.ID).Scan(&moveID, &to, &arrives, &payload); err != nil {
		t.Fatalf("reading the walk: %v", err)
	}
	if to != "business_district" {
		t.Fatalf("walking to %q, want the business district", to)
	}

	// The scheduler ends the walk; the dispatch arrives twice at once.
	clockMu.Lock()
	clock = arrives.Add(time.Second)
	clockMu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := metaFor("place.arrive")
			m.TelegramUserID = 0
			if _, err := places.Arrive(context.Background(), m, handlers.PlaceScheduledRequest{
				ActorID: p.ID, ReferenceType: application.PlaceMoveReference, ReferenceID: moveID, Payload: payload,
			}); err != nil {
				t.Errorf("Arrive: %v", err)
			}
		}()
	}
	wg.Wait()
	var place *string
	if err := pool.Raw().QueryRow(ctx, `SELECT place_code FROM players WHERE id = $1::uuid`, p.ID).Scan(&place); err != nil {
		t.Fatal(err)
	}
	if place == nil || *place != "business_district" {
		t.Fatalf("after the walk the player stands at %v", place)
	}
	rows, err := pool.Raw().Query(ctx,
		`SELECT metadata FROM outbox WHERE subject = 'game.command.job.work.v1' AND metadata->>'player_id' = $1`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var published []envelope.Metadata
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var m envelope.Metadata
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		published = append(published, m)
	}
	rows.Close()
	if len(published) != 1 {
		t.Fatalf("job.work published %d times on arrival, want once", len(published))
	}
	follow := published[0]
	if err := follow.Validate(); err != nil || follow.TelegramUserID != p.TelegramUserID || follow.TelegramChatID != p.TelegramUserID {
		t.Fatalf("the published command = %+v, %v", follow, err)
	}

	// The game runs it, and a redelivery of it starts nothing more.
	for i := 0; i < 2; i++ {
		if _, err := jobs.Work(ctx, follow); err != nil {
			t.Fatalf("job.work on arrival #%d: %v", i+1, err)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM shift_sessions WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Fatalf("shifts started = %d, want exactly one", n)
	}
	var energy int
	if err := pool.Raw().QueryRow(ctx, `SELECT energy FROM player_stats WHERE player_id = $1::uuid`, p.ID).Scan(&energy); err != nil {
		t.Fatal(err)
	}
	if energy != 100-15 {
		t.Errorf("energy = %d, want one shift's 15 spent", energy)
	}

	// The shift ends where it was worked.
	var sessionID string
	var endsAt time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, ends_at FROM shift_sessions WHERE player_id = $1::uuid`, p.ID).
		Scan(&sessionID, &endsAt); err != nil {
		t.Fatal(err)
	}
	clockMu.Lock()
	clock = endsAt.Add(time.Second)
	clockMu.Unlock()
	fin := metaFor("job.finish_shift")
	fin.TelegramUserID = 0
	if _, err := jobs.FinishShift(ctx, fin, handlers.FinishShiftRequest{ActorID: p.ID, ReferenceType: "shift_sessions", ReferenceID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT place_code FROM players WHERE id = $1::uuid`, p.ID).Scan(&place); err != nil {
		t.Fatal(err)
	}
	if place == nil || *place != "business_district" {
		t.Errorf("after the shift the player stands at %v, want the workplace", place)
	}
}

// handoffAPI is the Bot API as a player who never started the bot meets it:
// the private chat refuses, the group accepts.
type handoffAPI struct {
	mu     sync.Mutex
	player int64
	lines  []any
}

func (a *handoffAPI) SendMessageWith(_ context.Context, chatID int64, _ string, markup any, _ client.SendOptions) (*client.Message, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if chatID == a.player {
		return nil, &client.APIError{Code: 403, Description: "Forbidden: bot can't initiate conversation with a user"}
	}
	a.lines = append(a.lines, markup)
	return &client.Message{MessageID: 9}, nil
}

func (a *handoffAPI) EditMessageText(context.Context, int64, int64, string, any) error { return nil }

func (a *handoffAPI) AnswerCallback(context.Context, client.CallbackAnswer) error { return nil }

// The owner's report: «پرداخت ۵۰۰۰» as a reply in a group went to the private
// chat without the payee, and asked who to pay. The hand-off now carries the
// payee's public code and the amount; opening it runs the payment screen for
// that player, the amount offered first.
func TestAGroupPaymentHandsOffWithItsPayee(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	city := seedCity(t, pool, "pay hand-off")
	payer := bankPlayer(t, pool, city.ID)
	payee := bankPlayer(t, pool, city.ID)
	t.Cleanup(func() { purgeLedgerFor(t, pool, city.ID) })
	ctx := testCtx(t)
	ledger := postgres.NewLedgerRepository(pool)
	if _, err := application.GrantStartingCash(ctx, ledger, payer.ID, money.FromMinor(10000), "integration-test", time.Now()); err != nil {
		t.Fatal(err)
	}
	limits, err := bank.NewLimits(1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewBankHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), seqUUID{t}, nil,
		placedCities{postgres.NewCityRepository(pool)},
		bankStubPolicy{bps: map[string]int64{application.LeverCardTransferFee: 100}},
		postgres.NewPlayerSearchRepository(pool), limits, time.Hour, nil)

	// In the group: a reply to the payee, «پرداخت ۵۰۰۰». The gateway has
	// aimed it at the replied-to player (groups.AimAtReply).
	inGroup := bankMeta(t, payer, "bank.pay")
	inGroup.ChatType, inGroup.TelegramChatID = "supergroup", -1001234567
	inGroup.ReplyToPlayerID = payee.ID
	aimed := groups.AimAtReply("bank.pay", map[string]any{"to": "5000"}, payee.ID)
	req := handlers.PayRequest{Amount: aimed["amount"].(string)}
	req.Player, _ = aimed["player"].(string)
	resp, err := h.Pay(ctx, inGroup, req)
	if err != nil {
		t.Fatalf("Pay in the group: %v", err)
	}
	if len(resp.Resume) != 2 || resp.Resume[0] != payee.PublicCode || resp.Resume[1] != "5000" {
		t.Fatalf("the screen reopens with %v, want the payee's code and the amount", resp.Resume)
	}

	// The gateway hands it off: the payer never opened the bot, so the
	// group gets a link to it, and the link carries the payment.
	policy, err := groups.LoadPolicy("../configs/commands.yml")
	if err != nil {
		t.Fatal(err)
	}
	api := &handoffAPI{player: payer.TelegramUserID}
	r := groups.NewRenderer(nil, groups.Settings{CallbackAlertMaxRunes: 200, Policy: policy})
	out, err := r.Render(ctx, api, groups.Bot{Username: "torn_bot"}, inGroup, resp)
	if err != nil || out.Route != groups.RouteDeepLink || len(api.lines) != 1 {
		t.Fatalf("hand-off = %+v, %v, lines %d", out, err, len(api.lines))
	}
	raw, _ := json.Marshal(api.lines[0])
	want := "https://t.me/torn_bot?start=" + groups.StartPayload("bank.pay", payee.PublicCode, "5000")
	if !strings.Contains(string(raw), want) {
		t.Fatalf("the group's link = %s, want %s", raw, want)
	}

	// The player opens it: "/start <payload>" replays the payment privately.
	cmd, args, ok := groups.CommandFromStart("/start " + groups.StartPayload("bank.pay", payee.PublicCode, "5000"))
	if !ok {
		t.Fatal("the link does not replay")
	}
	domain, action, _ := strings.Cut(cmd, ".")
	command, payload, err := routing.ParseText(strings.Join(append([]string{"/" + domain, action}, args...), " "))
	if err != nil || command != "bank.pay" {
		t.Fatalf("replayed as %q, %v", command, err)
	}
	private := bankMeta(t, payer, "bank.pay")
	screen, err := h.Pay(ctx, private, handlers.PayRequest{To: payload["to"].(string), Amount: payload["amount"].(string)})
	if err != nil {
		t.Fatalf("Pay in the private chat: %v", err)
	}
	// The handler runs without a catalogue here, so the screen reads as its
	// keys: the payment screen, not the form asking whom to pay.
	if !strings.Contains(screen.Text, "pay.title") || strings.Contains(screen.Text, "pay.help") {
		t.Fatalf("the private chat opened on %q, want the payment to %s", screen.Text, payee.PublicCode)
	}
	if first := firstPayButton(screen); !strings.HasPrefix(first, "bank:pay:"+payee.PublicCode+":5000:") {
		t.Errorf("the named amount is not offered first: %q", first)
	}
}

// firstPayButton is the first quick amount a payment screen offers.
func firstPayButton(resp *presenter.Response) string {
	if resp.Keyboard == nil {
		return ""
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "bank:pay:") {
				return b.CallbackData
			}
		}
	}
	return ""
}
