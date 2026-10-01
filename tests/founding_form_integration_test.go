//go:build integration

// Integration test of the founding form (docs/adr/0028-world-and-
// settlements.md section 3): «ساخت روستا» opens a draft, only its founder can
// submit it, and the submission founds the village with the chosen name,
// emblem, motto and reserved currency - in one transaction, idempotently, and
// never twice under concurrency. What no unit test can show lives in
// PostgreSQL: the unique indexes and the row lock.
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
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

type foundingEnv struct {
	pool  *postgres.Pool
	h     *handlers.SettlementsHandler
	clock *testClock
	bot   string
	chats []int64
	// cache is the world the test planet is read from.
	cache *application.WorldCache
}

// group is a new group chat with a founder in it.
func (e *foundingEnv) group(t *testing.T) (envelope.Metadata, *application.Player) {
	t.Helper()
	p := insertPlayer(t, e.pool)
	m := validMeta(t)
	m.BotID, m.TelegramUserID = e.bot, p.TelegramUserID
	m.TelegramChatID = -newTelegramUserID(t)
	m.ChatType = "group"
	m.Command, m.Action = "settlement.found", "found"
	e.chats = append(e.chats, m.TelegramChatID)
	// Registered after the player, so it runs before the player is deleted.
	chat := m.TelegramChatID
	cleanupFounding(t, e.pool, func() []int64 { return []int64{chat} })
	return m, p
}

// asPlayer is the same group, another player asking.
func asPlayer(m envelope.Metadata, p *application.Player) envelope.Metadata {
	m.TelegramUserID = p.TelegramUserID
	m.RequestID = "req_" + randomTokenFor(p)
	return m
}

func randomTokenFor(p *application.Player) string { return strings.ReplaceAll(p.ID, "-", "") }

func newFoundingEnv(t *testing.T) *foundingEnv {
	t.Helper()
	pool := requirePostgres(t)
	ctx := testCtx(t)
	worlds := postgres.NewWorldRepository(pool)
	if _, err := worlds.Active(ctx); err == nil {
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
	w, err := worlds.Create(ctx, application.World{
		Seed: 20280930777, GeneratorVersion: worldgen.GeneratorVersion, ParamsHash: "integration-test-founding-form", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("creating the test world: %v", err)
	}
	e := &foundingEnv{pool: pool, bot: insertBot(t, pool), clock: &testClock{now: time.Now().UTC().Truncate(time.Second)}}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(c, `DELETE FROM worlds WHERE id = $1::uuid`, w.ID); err != nil {
			t.Errorf("cleaning up the test world: %v", err)
		}
	})

	e.cache = application.NewWorldCache(worlds, params, wgContent)
	e.h = handlers.NewSettlementsHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), workIDs{t}, nil,
		e.cache, staticContentSource{snap: loadTestContent(t)}, gametime.Scale(1),
		wsettle.Params{MinSpawnDistanceKm: 30, ThreatRadiusKm: 150, SearchMaxCells: 2000, SearchMaxAttempts: 50,
			ExcludedBiomes: []string{"polar_ice"}, MaxAbsLatitudeDeg: 70},
		168*time.Hour, 5, time.Hour, testFoundingConfig(), e.clock.Now)
	return e
}

func (e *foundingEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.Raw().QueryRow(testCtx(t), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func problemCodes(v map[string]any) map[string]bool {
	out := map[string]bool{}
	list, _ := v["problems"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		out[m["field"].(string)+":"+m["code"].(string)] = true
	}
	return out
}

func TestFoundingFormFlow(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	groupMeta, founder := e.group(t)
	chat := groupMeta.TelegramChatID

	// 1. «ساخت روستا» opens a draft and founds nothing.
	resp, err := rr(e.h.Found(ctx, groupMeta))
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if !strings.Contains(resp.Text, "founding.draft.title") || !strings.Contains(resp.Text, "founding.draft.deadline") {
		t.Fatalf("the group's message is not the draft explanation: %q", resp.Text)
	}
	draft := openDraftID(t, e.pool, chat)
	if resp.Keyboard == nil || len(resp.Keyboard.Rows) != 1 || resp.Keyboard.Rows[0][0].MiniAppParam != screensParam(draft) {
		t.Fatalf("the group message has no Mini App button for the draft: %+v", resp.Keyboard)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE founded_by_group_id = $1`, chat); n != 0 {
		t.Fatalf("a village exists before the form was submitted")
	}
	var suggested string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT suggested_name FROM settlement_founding_drafts WHERE id = $1::uuid`, draft).Scan(&suggested); err != nil || suggested == "" {
		t.Fatalf("the draft holds no generated name (%q, %v)", suggested, err)
	}

	// 2. Asking again is the same draft (idempotent); another player asking
	// sees it is pending, still one draft.
	again := groupMeta
	again.RequestID = "req_" + randomToken(t, 20)
	if _, err := e.h.Found(ctx, again); err != nil {
		t.Fatal(err)
	}
	other := insertPlayer(t, e.pool)
	resp, err = rr(e.h.Found(ctx, asPlayer(groupMeta, other)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "founding.draft.pending_title") {
		t.Errorf("another player asking should see the draft is pending: %q", resp.Text)
	}
	if n := e.count(t, `SELECT count(*) FROM settlement_founding_drafts WHERE chat_id = $1`, chat); n != 1 || openDraftID(t, e.pool, chat) != draft {
		t.Fatalf("a group must hold one open draft (got %d rows)", n)
	}

	// 3. The form's facts: mine for the founder, other for anybody else, and
	// nothing for a draft that does not exist.
	client := func(p *application.Player, command, action string) envelope.Metadata {
		return clientMeta(asPlayer(groupMeta, p), command, action)
	}
	resp, err = rr(e.h.FoundDraft(ctx, client(founder, "settlement.found.draft", "found.draft"), handlers.FoundDraftRequest{Draft: strings.ReplaceAll(draft, "-", "")}))
	if err != nil {
		t.Fatal(err)
	}
	v := viewOf(t, resp)
	if v["state"] != "mine" || v["suggested_name"] != suggested || len(v["shapes"].([]any)) < 3 || len(v["palette"].([]any)) < 6 {
		t.Errorf("the founder's form view is wrong: %v", v)
	}
	resp, err = rr(e.h.FoundDraft(ctx, client(other, "settlement.found.draft", "found.draft"), handlers.FoundDraftRequest{Draft: draft}))
	if err != nil {
		t.Fatal(err)
	}
	if v := viewOf(t, resp); v["state"] != "other" {
		t.Errorf("another player should read the form only: state %v", v["state"])
	}
	resp, err = rr(e.h.FoundDraft(ctx, client(founder, "settlement.found.draft", "found.draft"), handlers.FoundDraftRequest{}))
	if err != nil || viewOf(t, resp)["draft"] != draft {
		t.Errorf("without an id the founder's own open draft is meant: %v %v", err, resp)
	}
	resp, err = rr(e.h.FoundDraft(ctx, client(founder, "settlement.found.draft", "found.draft"), handlers.FoundDraftRequest{Draft: newUUID(t)}))
	if err != nil || viewOf(t, resp)["kind"] != "no_draft" {
		t.Errorf("an unknown draft should be refused as no_draft: %v %v", err, resp)
	}

	// 4. Only the founder submits.
	good := validFoundingRequest(t, draft)
	resp, err = rr(e.h.Submit(ctx, client(other, "settlement.found.submit", "found.submit"), good))
	if err != nil {
		t.Fatal(err)
	}
	if viewOf(t, resp)["kind"] != "not_founder" {
		t.Errorf("a stranger's submit should be refused: %v", viewOf(t, resp))
	}

	// 5. A form with problems is refused with coded problems and founds
	// nothing.
	bad := good
	bad.Name, bad.CurrencyCode, bad.Shape = "ab", "SUP", "blob"
	resp, err = rr(e.h.Submit(ctx, client(founder, "settlement.found.submit", "found.submit"), bad))
	if err != nil {
		t.Fatal(err)
	}
	codes := problemCodes(viewOf(t, resp))
	for _, want := range []string{"name:name_short", "currency_code:currency_code_reserved", "emblem:emblem_invalid"} {
		if !codes[want] {
			t.Errorf("missing problem %s in %v", want, codes)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE founded_by_group_id = $1`, chat); n != 0 {
		t.Fatal("an invalid form founded a village")
	}

	// 6. A check validates and founds nothing.
	check := good
	check.Check = "1"
	resp, err = rr(e.h.Submit(ctx, client(founder, "settlement.found.submit", "found.submit"), check))
	if err != nil || resp.Screen != "founding_checked" {
		t.Fatalf("a check of a good form should answer founding_checked: %v %+v", err, resp)
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE founded_by_group_id = $1`, chat); n != 0 {
		t.Fatal("a check founded a village")
	}

	// 7. The founder's submit founds the village with the chosen details.
	resp, err = rr(e.h.Submit(ctx, client(founder, "settlement.found.submit", "found.submit"), good))
	if err != nil {
		t.Fatal(err)
	}
	fv := viewOf(t, resp)
	cityID, _ := fv["settlement_id"].(string)
	if cityID == "" || fv["name"] != good.Name || fv["currency_code"] != strings.ToUpper(good.CurrencyCode) {
		t.Fatalf("the founded view is wrong: %v", fv)
	}
	var name, nameKey, motto, shape, colorA, colorB, icon, ccode, cname, csym string
	if err := e.pool.Raw().QueryRow(ctx,
		`SELECT c.name, c.name_key, c.motto, c.emblem_shape, c.emblem_color_a, c.emblem_color_b, c.emblem_icon,
		        r.code, r.name, r.symbol
		   FROM cities c JOIN village_currency_reservations r ON r.settlement_id = c.id WHERE c.id = $1::uuid`, cityID).
		Scan(&name, &nameKey, &motto, &shape, &colorA, &colorB, &icon, &ccode, &cname, &csym); err != nil {
		t.Fatalf("reading the founded village: %v", err)
	}
	if name != good.Name || nameKey != wsettle.NameKey(good.Name) || motto != good.Motto ||
		shape != "shield" || colorA != "crimson" || colorB != "gold" || icon != "wheat" ||
		ccode != strings.ToUpper(good.CurrencyCode) || cname != good.CurrencyName || csym != "M" {
		t.Errorf("the village does not hold the chosen details: %s %s %s %s/%s/%s/%s %s %s %s", name, nameKey, motto, shape, colorA, colorB, icon, ccode, cname, csym)
	}
	var holder string
	if err := e.pool.Raw().QueryRow(ctx,
		`SELECT COALESCE(o.holder_player_id::text, '') FROM offices o JOIN cities c ON c.jurisdiction_id = o.jurisdiction_id
		  WHERE c.id = $1::uuid AND o.office_code = 'village_head'`, cityID).Scan(&holder); err != nil || holder != founder.ID {
		t.Errorf("the founder should be the village head: %q %v", holder, err)
	}
	var home string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT COALESCE(residence_city_id::text, '') FROM players WHERE id = $1::uuid`, founder.ID).Scan(&home); err != nil || home != cityID {
		t.Errorf("the founder should live in the new village: %q %v", home, err)
	}
	var here string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT COALESCE(city_id::text, '') FROM players WHERE id = $1::uuid`, founder.ID).Scan(&here); err != nil || here != cityID {
		t.Errorf("the founder should stand in the new village, not in Support: %q %v", here, err)
	}
	var status, submittedTo string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT status, COALESCE(settlement_id::text, '') FROM settlement_founding_drafts WHERE id = $1::uuid`, draft).Scan(&status, &submittedTo); err != nil || status != "submitted" || submittedTo != cityID {
		t.Errorf("the draft should be submitted to the village: %s %s %v", status, submittedTo, err)
	}
	var ev string
	if err := e.pool.Raw().QueryRow(ctx,
		`SELECT payload::text FROM outbox WHERE subject = 'game.event.settlement.founded.v1' AND payload->>'settlement_id' = $1`, cityID).Scan(&ev); err != nil {
		t.Fatalf("the settlement.founded event: %v", err)
	}
	for _, want := range []string{`"chat_id"`, `"emblem"`, good.Name, strings.ToUpper(good.CurrencyCode), `"founder_name"`} {
		if !strings.Contains(ev, want) {
			t.Errorf("the settlement.founded event lacks %s: %s", want, ev)
		}
	}

	// 8. A repeated submit answers from the village; nothing is duplicated.
	resp, err = rr(e.h.Submit(ctx, client(founder, "settlement.found.submit", "found.submit"), good))
	if err != nil {
		t.Fatal(err)
	}
	if viewOf(t, resp)["settlement_id"] != cityID {
		t.Errorf("a repeated submit should answer with the same village: %v", viewOf(t, resp))
	}
	if e.count(t, `SELECT count(*) FROM cities WHERE founded_by_group_id = $1`, chat) != 1 ||
		e.count(t, `SELECT count(*) FROM village_currency_reservations WHERE settlement_id = $1::uuid`, cityID) != 1 ||
		e.count(t, `SELECT count(*) FROM outbox WHERE subject = 'game.event.settlement.founded.v1' AND payload->>'settlement_id' = $1`, cityID) != 1 {
		t.Error("a repeated submit duplicated something")
	}
	// The draft reads as founded, and asking again in the group says so.
	resp, err = rr(e.h.FoundDraft(ctx, client(other, "settlement.found.draft", "found.draft"), handlers.FoundDraftRequest{Draft: draft}))
	if err != nil || viewOf(t, resp)["state"] != "founded" {
		t.Errorf("the submitted draft should read founded: %v %v", err, resp)
	}
	resp, err = rr(e.h.Found(ctx, again))
	if err != nil || !strings.Contains(resp.Text, "settlement.found.already") {
		t.Errorf("asking again after founding should say the group already has a village: %v %v", err, resp)
	}

	// 9. A second group cannot take the first village's name (however it is
	// spelled), currency code or currency name.
	group2, founder2 := e.group(t)
	if _, err := e.h.Found(ctx, group2); err != nil {
		t.Fatal(err)
	}
	draft2 := openDraftID(t, e.pool, group2.TelegramChatID)
	clash := validFoundingRequest(t, draft2)
	clash.Name = strings.ToUpper(good.Name[:3]) + " " + good.Name[3:] // same name key
	clash.CurrencyCode, clash.CurrencyName = good.CurrencyCode, good.CurrencyName
	resp, err = rr(e.h.Submit(ctx, clientMeta(group2, "settlement.found.submit", "found.submit"), clash))
	if err != nil {
		t.Fatal(err)
	}
	codes = problemCodes(viewOf(t, resp))
	for _, want := range []string{"name:name_taken", "currency_code:currency_code_taken", "currency_name:currency_name_taken"} {
		if !codes[want] {
			t.Errorf("missing problem %s in %v", want, codes)
		}
	}
	_ = founder2

	// 10. A draft that waits too long founds nothing, and the group may ask
	// again for a new one.
	e.clock.Advance(31 * time.Minute)
	resp, err = rr(e.h.Submit(ctx, clientMeta(group2, "settlement.found.submit", "found.submit"), validFoundingRequest(t, draft2)))
	if err != nil {
		t.Fatal(err)
	}
	if viewOf(t, resp)["kind"] != "expired" {
		t.Errorf("an expired draft should be refused: %v", viewOf(t, resp))
	}
	if s := e.count(t, `SELECT count(*) FROM settlement_founding_drafts WHERE id = $1::uuid AND status = 'expired'`, draft2); s != 1 {
		t.Error("the draft should be marked expired")
	}
	if n := e.count(t, `SELECT count(*) FROM cities WHERE founded_by_group_id = $1`, group2.TelegramChatID); n != 0 {
		t.Fatal("an expired draft founded a village")
	}
	if _, err := e.h.Found(ctx, group2); err != nil {
		t.Fatal(err)
	}
	draft3 := openDraftID(t, e.pool, group2.TelegramChatID)
	if draft3 == draft2 {
		t.Fatal("a new draft should replace the expired one")
	}
	resp, err = rr(e.h.Submit(ctx, clientMeta(group2, "settlement.found.submit", "found.submit"), validFoundingRequest(t, draft3)))
	if err != nil || viewOf(t, resp)["settlement_id"] == nil {
		t.Fatalf("the new draft should found the village: %v %+v", err, resp)
	}
}

func randomTokenSafe(i int) string { return strings.Repeat("c", 8) + string(rune('a'+i)) }

// viewOfNoT is viewOf for a goroutine: string values only, "" when absent.
func viewOfNoT(resp *presenter.Response) map[string]string {
	var raw map[string]any
	_ = json.Unmarshal(resp.View, &raw)
	out := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// screensParam mirrors screens.FoundingStartParam without importing it twice.
func screensParam(draft string) string { return "found_" + strings.ReplaceAll(draft, "-", "") }

// Two replicas (or a double tap) submitting one draft at once found exactly
// one village: the draft's row lock serialises them.
func TestFoundingFormConcurrentSubmit(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	groupMeta, _ := e.group(t)
	if _, err := e.h.Found(ctx, groupMeta); err != nil {
		t.Fatal(err)
	}
	req := validFoundingRequest(t, openDraftID(t, e.pool, groupMeta.TelegramChatID))
	const n = 6
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := clientMeta(groupMeta, "settlement.found.submit", "found.submit")
			m.RequestID = "req_" + randomTokenSafe(i)
			resp, err := rr(e.h.Submit(ctx, m, req))
			errs[i] = err
			if err == nil && len(resp.View) > 0 {
				ids[i] = viewOfNoT(resp)["settlement_id"]
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	if c := e.count(t, `SELECT count(*) FROM cities WHERE founded_by_group_id = $1`, groupMeta.TelegramChatID); c != 1 {
		t.Fatalf("%d villages were founded, want 1", c)
	}
	if c := e.count(t, `SELECT count(*) FROM village_currency_reservations r JOIN cities c ON c.id = r.settlement_id WHERE c.founded_by_group_id = $1`, groupMeta.TelegramChatID); c != 1 {
		t.Fatalf("%d currency reservations, want 1", c)
	}
	for i, id := range ids {
		if id == "" || id != ids[0] {
			t.Errorf("submit %d answered village %q, want %q", i, id, ids[0])
		}
	}
}
