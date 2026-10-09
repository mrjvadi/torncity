//go:build integration

package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Research capacity and speed (ADR 0048): the free slot every settlement has, the slots a staffed library opens, what
// the scholars and the upkeep cost, the quote a project starts on, the scholars' posts, the sharing pacts and the
// breakthrough progress. The rules are built the way the service builds them, from the shipped config.

type researchEnv struct {
	*laborEnv
	cfg *config.Config
}

func newResearchEnv(t *testing.T) *researchEnv {
	t.Helper()
	l := newLaborEnv(t)
	cfg := config.Defaults()
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	l.village.WithResearch(handlers.ResearchRules{Rules: settlementcfg.Research(cfg.Settlement), Clock: clock,
		ExperiencePerShift: cfg.Settlement.ResearchExperiencePerShift, ScholarXP: cfg.Settlement.ResearchScholarXP})
	e := &researchEnv{laborEnv: l, cfg: cfg}
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`DELETE FROM research_posts WHERE settlement_id = $1::uuid`,
			`DELETE FROM research_pacts WHERE settlement_a = $1::uuid OR settlement_b = $1::uuid`,
			`DELETE FROM settlement_experience WHERE settlement_id = $1::uuid`,
			`DELETE FROM research_days WHERE settlement_id = $1::uuid`,
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM village_storage_days WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, l.cityID)
			}
			if _, err := l.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	return e
}

// scholar is a resident whose posts, skills and money are cleaned up with the test.
func (e *researchEnv) scholar() *application.Player {
	e.t.Helper()
	p := e.resident()
	e.t.Cleanup(func() {
		c := testCtx(e.t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM research_posts WHERE player_id = $1::uuid`, p.ID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM player_skills WHERE player_id = $1::uuid`, p.ID)
		purgeLedgerFor(e.t, e.pool, p.ID)
	})
	return p
}

// building puts a finished research building on a lot of the settlement.
var nextResearchLot = 90

func (e *researchEnv) building(code string) string {
	e.t.Helper()
	id := newUUID(e.t)
	nextResearchLot++
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, 3, 'complete', now(), now())`, id, e.cityID, code, nextResearchLot); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// stock puts goods in the settlement's store.
func (e *researchEnv) stock(item string, qty int64) {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, ToOrg: application.SettlementOrg(e.cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(e.t), At: time.Now().UTC()})
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *researchEnv) held(item string) int64 {
	e.t.Helper()
	return e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2`, e.cityID, item)
}

func (e *researchEnv) treasury() int64 { return treasuryOf(e.t, e.pool, e.cityID) }

// drainTreasury sends all the treasury holds to the sink.
func (e *researchEnv) drainTreasury() {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, e.cityID)
		if err != nil {
			return err
		}
		bal, err := tx.Ledger().Balance(ctx, acct.ID)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonPenalty, CreatedAt: time.Now().UTC(),
			Entries: []application.LedgerEntry{{AccountID: acct.ID, Amount: money.FromMinor(-bal.Minor())}, {AccountID: application.SystemSinkAccountID, Amount: bal}}})
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
}

// desk asks the research desk, with an act or without.
func (e *researchEnv) desk(p *application.Player, act, code string) (village.ResearchBoardView, *presentation.Response) {
	e.t.Helper()
	resp, err := e.village.ResearchDesk(testCtx(e.t), e.as(p, "settlement.research", "research"), handlers.VillageResearchRequest{Action: act, Code: code})
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.ResearchBoardView
	if len(resp.View) > 0 && resp.Refusal == nil {
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatalf("the desk view: %v", err)
		}
	}
	return v, resp
}

// research starts a project; the response tells a refusal.
func (e *researchEnv) research(code, slot string) *presentation.Response {
	e.t.Helper()
	resp, err := e.village.Research(testCtx(e.t), e.as(e.head, "settlement.knowledge.research", "knowledge.research"),
		handlers.VillageKnowledgeRequest{Code: code, Slot: slot})
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

type projectRow struct {
	ID, Slot                                string
	Cost, Speed, Ahead, Discount, Share, Sp int64
	Started, Finish                         time.Time
}

func (e *researchEnv) project(code string) projectRow {
	e.t.Helper()
	var p projectRow
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT id::text, slot_ref, cost, speed_bps, ahead_bps, discount_bps, share_bps, spent_points, started_at, finish_at
		FROM settlement_research WHERE settlement_id = $1::uuid AND code = $2`, e.cityID, code).
		Scan(&p.ID, &p.Slot, &p.Cost, &p.Speed, &p.Ahead, &p.Discount, &p.Share, &p.Sp, &p.Started, &p.Finish); err != nil {
		e.t.Fatalf("project %s: %v", code, err)
	}
	return p
}

func refusalOf(resp *presentation.Response) string {
	if resp.Refusal == nil {
		return ""
	}
	return resp.Refusal.Code
}

// finishAll runs every project to its end.
func (e *researchEnv) finishAll() {
	e.t.Helper()
	ctx := testCtx(e.t)
	rows, err := e.pool.Raw().Query(ctx, `SELECT id::text, game_action_id::text, finish_at FROM settlement_research WHERE settlement_id = $1::uuid AND status = 'running' ORDER BY finish_at`, e.cityID)
	if err != nil {
		e.t.Fatal(err)
	}
	type run struct {
		id, action string
		at         time.Time
	}
	var runs []run
	for rows.Next() {
		var r run
		if err := rows.Scan(&r.id, &r.action, &r.at); err != nil {
			e.t.Fatal(err)
		}
		runs = append(runs, r)
	}
	rows.Close()
	for _, r := range runs {
		if e.clock.Now().Before(r.at) {
			e.clock.Advance(r.at.Sub(e.clock.Now()) + time.Second)
		}
		for i := 0; i < 2; i++ { // twice: a redelivery changes nothing
			if _, err := rrc(e.village.Researched(ctx, e.as(e.head, "settlement.researched", "researched"), handlers.CrimeScheduledRequest{ActionID: r.action, ReferenceID: r.id})); err != nil {
				e.t.Fatal(err)
			}
		}
	}
}

func (e *researchEnv) verify() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	if !v.VillageInvariants.Research {
		e.t.Fatal("migration 0134 is not applied")
	}
	t := v.VillageInvariants
	if t.ResearchWageLedger != t.ResearchWageRows || t.ResearchWageMismatched != 0 || t.ResearchDayBroken != 0 ||
		t.ResearchUpkeepLedger != t.ResearchUpkeepRows || t.ResearchSlotBroken != 0 || t.ResearchPostBroken != 0 || t.ResearchPactBroken != 0 {
		e.t.Errorf("the research invariants do not hold: %+v", t)
	}
}

// With no research building a settlement has exactly what it had: one project at a time, at the price and pace of
// the catalogue; a second is refused as busy; and a project that was running before the migration (it carries only
// the defaults of the new columns) finishes untouched.
func TestResearchTheFreeSlotKeepsTheOldBehaviour(t *testing.T) {
	e := newResearchEnv(t)
	d, resp := e.desk(e.head, "", "")
	if resp.Refusal != nil || d.Capacity != 1 || len(d.Slots) != 1 || d.Slots[0].Ref != "free" || len(d.Buildings) != 0 {
		t.Fatalf("a settlement with no research building: %+v (%s)", d, resp.Text)
	}
	before := e.scalar(`SELECT count(*) FROM research_days WHERE settlement_id = $1::uuid`, e.cityID)

	if r := refusalOf(e.research("carpentry", "")); r != "" {
		t.Fatalf("the first project was refused: %s", r)
	}
	p := e.project("carpentry")
	if p.Slot != "free" || p.Cost != 1000 || p.Speed != 10_000 || p.Ahead != 10_000 || p.Discount != 0 || p.Share != 0 {
		t.Errorf("the free slot must keep the catalogue's price and pace: %+v", p)
	}
	if got := p.Finish.Sub(p.Started); got != 12*time.Hour {
		t.Errorf("carpentry takes %v, the catalogue says 12h", got)
	}
	if got := e.scalar(`SELECT count(*) FROM research_days WHERE settlement_id = $1::uuid`, e.cityID); got != before {
		t.Errorf("a settlement with no research building writes no research day (%d -> %d)", before, got)
	}
	if r := refusalOf(e.research("masonry", "")); r != "village_busy" {
		t.Errorf("a second project with one slot: %q, want village_busy", r)
	}
	if r := refusalOf(e.research("carpentry", "")); r == "" {
		t.Error("the same project twice was accepted")
	}

	// a project that began before the migration: the defaults of the new columns are the old project
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE settlement_research SET slot_ref = 'free', speed_bps = 10000, ahead_bps = 10000, discount_bps = 0, share_bps = 0, spent_points = 0
		WHERE id = $1::uuid`, p.ID); err != nil {
		t.Fatal(err)
	}
	e.finishAll()
	if !ownedKnowledge(t, e.pool, e.cityID)["carpentry"] {
		t.Error("the project that was running did not finish")
	}
	if r := refusalOf(e.research("masonry", "")); r != "" {
		t.Errorf("after the first finished the slot is free again, got %q", r)
	}
	e.verify()
}

// A library opens a slot while a scholar works in it and the day's upkeep is in the stock; the day is paid once; a
// project in it starts faster than one in the free slot, on the quote it recorded.
func TestResearchAStaffedLibraryOpensASlot(t *testing.T) {
	e := newResearchEnv(t)
	lib := e.building("library")
	e.stock("wool", 5)
	treasury0 := e.treasury()

	d, resp := e.desk(e.head, "", "")
	if resp.Refusal != nil {
		t.Fatalf("%s", resp.Text)
	}
	if d.Capacity != 2 || len(d.Slots) != 2 || d.Slots[1].Ref != lib {
		t.Fatalf("a staffed library opens a second slot: %+v", d.Slots)
	}
	if len(d.Buildings) != 1 || !d.Buildings[0].Open || d.Buildings[0].NPCs != 1 || d.Buildings[0].Idle != "" {
		t.Errorf("the library should be open with one town scholar: %+v", d.Buildings)
	}
	// the day was paid once: wages to the sink, one unit of wool used up, the day on record
	wage := e.scalar(`SELECT wage_npc FROM research_days WHERE settlement_id = $1::uuid`, e.cityID)
	if wage <= 0 || treasury0-e.treasury() != wage {
		t.Errorf("the treasury paid %d, the day says %d", treasury0-e.treasury(), wage)
	}
	if got := e.held("wool"); got != 4 {
		t.Errorf("a working day uses one wool: %d left of 5", got)
	}
	for i := 0; i < 3; i++ { // looks, redeliveries and another reader change nothing
		e.desk(e.head, "", "")
		if _, err := rrc(e.village.KnowledgeList(testCtx(t), e.as(e.head, "settlement.knowledge", "knowledge"))); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.scalar(`SELECT count(*) FROM research_days WHERE settlement_id = $1::uuid`, e.cityID); got != 1 {
		t.Errorf("one research day, %d rows", got)
	}
	if treasury0-e.treasury() != wage || e.held("wool") != 4 {
		t.Errorf("the day was paid twice: treasury %d, wool %d", treasury0-e.treasury(), e.held("wool"))
	}

	// two projects at once; the quicker slot is chosen first
	if r := refusalOf(e.research("carpentry", "")); r != "" {
		t.Fatal(r)
	}
	if r := refusalOf(e.research("masonry", "")); r != "" {
		t.Fatal(r)
	}
	if r := refusalOf(e.research("weaving", "")); r != "village_busy" {
		t.Errorf("a third project with two slots: %q", r)
	}
	carp, mas := e.project("carpentry"), e.project("masonry")
	if carp.Slot != lib {
		t.Errorf("the first project should take the library (the faster slot): %+v", carp)
	}
	// 10000 + the library's 500 + the NPC scholar's (500 floor + 3 levels x 100) = 11300
	if carp.Speed != 11_300 {
		t.Errorf("the library's speed = %d, want 11300", carp.Speed)
	}
	if mas.Slot != "free" || mas.Speed != 10_000 {
		t.Errorf("the second project takes the free slot at the plain pace: %+v", mas)
	}
	if got, want := carp.Finish.Sub(carp.Started), 12*time.Hour*10_000/11_300; got < want-time.Second || got > want+time.Second {
		t.Errorf("carpentry in the library takes %v, want %v", got, want)
	}

	// the next local day the library is paid again
	e.finishAll()
	e.clock.Advance(26 * time.Hour)
	e.desk(e.head, "", "")
	if got := e.scalar(`SELECT count(*) FROM research_days WHERE settlement_id = $1::uuid`, e.cityID); got != 2 {
		t.Errorf("the next day has its own row: %d rows", got)
	}
	e.verify()
}

// A library with no wool, or with a treasury that cannot pay, stands idle and gives no slot; the desk says why.
func TestResearchAnIdleLibraryGivesNothing(t *testing.T) {
	e := newResearchEnv(t)
	e.building("library")

	d, _ := e.desk(e.head, "", "")
	if d.Capacity != 1 || len(d.Buildings) != 1 || d.Buildings[0].Open || d.Buildings[0].Idle != village.ResearchIdleNoUpkeep {
		t.Fatalf("no wool in the stock: %+v", d)
	}
	if e.scalar(`SELECT wage_npc + wage_player FROM research_days WHERE settlement_id = $1::uuid`, e.cityID) != 0 {
		t.Error("nobody is paid for a day the library did not work")
	}
	// the same day stays settled: wool arriving later today does not reopen it (a day is judged once)
	e.stock("wool", 3)
	if d, _ = e.desk(e.head, "", ""); d.Capacity != 1 {
		t.Errorf("the day was judged once: %+v", d)
	}
	// tomorrow, with wool but an empty treasury
	e.clock.Advance(26 * time.Hour)
	e.drainTreasury()
	d, _ = e.desk(e.head, "", "")
	if d.Capacity != 1 || d.Buildings[0].Open || d.Buildings[0].Idle != village.ResearchIdleNoWage {
		t.Errorf("an empty treasury cannot pay the scholars: %+v", d.Buildings)
	}
	if e.held("wool") != 3 {
		t.Errorf("a day that did not work uses nothing up: %d wool", e.held("wool"))
	}
	e.verify()
}

// A resident takes a scholar's post: the treasury pays them (their skill speeds the slot), they learn scholarship by
// doing it, one post per person, and leaving frees it.
func TestResearchAPlayerScholar(t *testing.T) {
	e := newResearchEnv(t)
	lib := e.building("library")
	e.stock("wool", 5)
	scholar := e.scholar()

	d, resp := e.desk(scholar, village.ResearchActionPost, lib)
	if resp.Refusal != nil {
		t.Fatalf("taking a post: %s %s", resp.Refusal.Code, resp.Text)
	}
	if len(d.Buildings) != 1 || !d.Buildings[0].Mine {
		t.Fatalf("the post is mine: %+v", d.Buildings)
	}
	// the library seats one scholar: a second resident is refused, and the first cannot hold a second post
	other := e.scholar()
	if _, r := e.desk(other, village.ResearchActionPost, lib); refusalOf(r) != "village_research_no_post" {
		t.Errorf("a full library: %q", refusalOf(r))
	}
	if _, r := e.desk(scholar, village.ResearchActionPost, lib); refusalOf(r) != "village_research_no_post" && refusalOf(r) != "village_research_post_held" {
		t.Errorf("the same post twice: %q", refusalOf(r))
	}
	if _, r := e.desk(e.scholar(), village.ResearchActionLeave, ""); refusalOf(r) != "village_research_no_post_held" {
		t.Errorf("leaving with no post: %q", refusalOf(r))
	}

	// the day: the player is paid, not an NPC
	e.clock.Advance(26 * time.Hour)
	cash0 := e.cash(scholar.ID)
	d, _ = e.desk(e.head, "", "")
	if !d.Buildings[0].Open || d.Buildings[0].Players != 1 || d.Buildings[0].NPCs != 0 {
		t.Fatalf("the library works with the player alone: %+v", d.Buildings)
	}
	if paid := e.cash(scholar.ID) - cash0; paid <= 0 || paid != e.scalar(`SELECT wage_player FROM research_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, e.cityID) {
		t.Errorf("the scholar was paid %d", paid)
	}
	if got := e.scalar(`SELECT COALESCE(SUM(xp), 0)::bigint FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'scholarship'`, scholar.ID); got <= 0 {
		t.Errorf("a day on duty trains scholarship: %d xp", got)
	}
	// their level speeds the slot up: a player at level 0 adds the floor (500) only
	if r := refusalOf(e.research("carpentry", "")); r != "" {
		t.Fatal(r)
	}
	if p := e.project("carpentry"); p.Slot != lib || p.Speed != 10_000+500+500 {
		t.Errorf("the library with a level-0 player scholar: %+v", p)
	}

	// leaving frees the post
	if _, r := e.desk(scholar, village.ResearchActionLeave, ""); r.Refusal != nil {
		t.Fatalf("leaving: %s", r.Text)
	}
	if _, r := e.desk(other, village.ResearchActionPost, lib); r.Refusal != nil {
		t.Errorf("the freed post: %s", r.Text)
	}
	// a stranger to the village may not take one
	if _, r := e.desk(insertPlayer(t, e.pool), village.ResearchActionPost, lib); r.Refusal == nil {
		t.Error("a non-resident took a post")
	}
	e.verify()
}

// A research-sharing pact: offered, accepted, and the partner's holding of an item speeds the project up, locked at
// its start; ended, it adds nothing to the next project.
func TestResearchAPactSharesWhatThePartnerKnows(t *testing.T) {
	e := newResearchEnv(t)
	meta2, founder2 := e.group(t)
	foundVillage(t, e.pool, e.h, meta2)
	var city2, code2 string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text, code FROM cities WHERE founded_by_group_id = $1`, meta2.TelegramChatID).Scan(&city2, &code2); err != nil {
		t.Fatal(err)
	}
	var code1 string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT code FROM cities WHERE id = $1::uuid`, e.cityID).Scan(&code1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := testCtx(t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM research_pacts WHERE settlement_a = $1::uuid OR settlement_b = $1::uuid`, city2)
	})
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, founder2.ID) })

	// the other settlement holds carpentry
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, 'carpentry', 'researched', now())`, city2); err != nil {
		t.Fatal(err)
	}

	// without a pact the plain pace
	if _, r := e.desk(e.head, village.ResearchActionPropose, code1); refusalOf(r) != "village_research_pact_self" {
		t.Errorf("a pact with oneself: %q", refusalOf(r))
	}
	if _, r := e.desk(e.head, village.ResearchActionPropose, code2); r.Refusal != nil {
		t.Fatalf("offering: %s", r.Text)
	}
	if _, r := e.desk(e.head, village.ResearchActionPropose, code2); refusalOf(r) != "village_research_pact_open" {
		t.Errorf("offering twice: %q", refusalOf(r))
	}
	// a proposal is not a pact yet: no bonus, and only the other side answers it
	var pact string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM research_pacts WHERE settlement_a = $1::uuid`, e.cityID).Scan(&pact); err != nil {
		t.Fatal(err)
	}
	if _, r := e.desk(e.head, village.ResearchActionAccept, pact); refusalOf(r) != "village_research_pact_not_found" {
		t.Errorf("the proposer answering their own offer: %q", refusalOf(r))
	}
	if _, r := e.desk(founder2, village.ResearchActionAccept, pact); r.Refusal != nil {
		t.Fatalf("accepting: %s %s", r.Refusal.Code, r.Text)
	}
	if e.scalar(`SELECT count(*) FROM research_pacts WHERE id = $1::uuid AND status = 'active'`, pact) != 1 {
		t.Fatal("the pact is not active")
	}
	if r := refusalOf(e.research("carpentry", "")); r != "" {
		t.Fatal(r)
	}
	if p := e.project("carpentry"); p.Share != 1_000 || p.Speed < 11_000 {
		t.Errorf("one partner holding the item adds 1000 bps, locked into the project: %+v", p)
	}
	// ending the pact does not change the running project
	if _, r := e.desk(e.head, village.ResearchActionEnd, pact); r.Refusal != nil {
		t.Fatal(r.Text)
	}
	if p := e.project("carpentry"); p.Share != 1_000 {
		t.Errorf("the running project keeps its quote: %+v", p)
	}
	e.finishAll()
	if r := refusalOf(e.research("masonry", "")); r != "" {
		t.Fatal(r)
	}
	if p := e.project("masonry"); p.Share != 0 {
		t.Errorf("no pact, no sharing: %+v", p)
	}
	e.verify()
}

// Real work in a field is breakthrough progress: it makes the next project of that field cheaper, and the points it
// used are spent.
func TestResearchBreakthroughFromRealWork(t *testing.T) {
	e := newResearchEnv(t)
	// carpentry is a craft of depth 1: 100 points give the whole 40 percent, 50 give half of it
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_experience (settlement_id, field, points, updated_at) VALUES ($1::uuid, 'craft', 50, now())`, e.cityID); err != nil {
		t.Fatal(err)
	}
	if r := refusalOf(e.research("carpentry", "")); r != "" {
		t.Fatal(r)
	}
	p := e.project("carpentry")
	if p.Discount != 2_000 || p.Cost != 800 || p.Sp != 50 {
		t.Errorf("50 of 100 points give half the 40 percent: %+v", p)
	}
	if got := e.scalar(`SELECT points FROM settlement_experience WHERE settlement_id = $1::uuid AND field = 'craft'`, e.cityID); got != 0 {
		t.Errorf("the points a project used are spent: %d left", got)
	}
	e.verify()
}

// A finished work shift in a field adds to its experience (learning by doing), whoever did it.
func TestResearchWorkAddsExperience(t *testing.T) {
	e := newResearchEnv(t)
	ctx := testCtx(t)
	camp := newUUID(t)
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, 'canal_irrigation', 'researched', now())`, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'farm_canal', 81, 3, 'complete', now(), now())`, camp, e.cityID); err != nil {
		t.Fatal(err)
	}
	e.stock("wheat", 10)
	worker := e.scholar()
	if _, err := rrc(e.village.Work(ctx, e.as(worker, "settlement.work", "work"), handlers.VillageWorkRequest{ID: camp})); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(3 * time.Hour)
	for _, s := range e.workingShifts(camp) {
		e.end(s)
	}
	if got := e.scalar(`SELECT COALESCE(SUM(points), 0)::bigint FROM settlement_experience WHERE settlement_id = $1::uuid AND field = 'food'`, e.cityID); got != e.cfg.Settlement.ResearchExperiencePerShift {
		t.Errorf("one finished farm shift adds %d food points, got %d (a redelivered end adds nothing)", e.cfg.Settlement.ResearchExperiencePerShift, got)
	}
	e.verify()
}

// Two replicas starting the last slot at once: one wins, the other is refused as busy, and the settlement never runs
// more projects than its slots.
func TestResearchTheLastSlotIsTakenOnce(t *testing.T) {
	e := newResearchEnv(t)
	type result struct {
		code string
		resp *presentation.Response
		err  error
	}
	codes := []string{"carpentry", "masonry", "weaving", "pottery"}
	out := make(chan result, len(codes))
	for _, c := range codes {
		go func(code string) {
			resp, err := e.village.Research(testCtx(t), e.as(e.head, "settlement.knowledge.research", "knowledge.research"), handlers.VillageKnowledgeRequest{Code: code})
			out <- result{code, resp, err}
		}(c)
	}
	started := 0
	for range codes {
		r := <-out
		if r.err != nil {
			t.Fatalf("%s: %v", r.code, r.err)
		}
		if r.resp.Refusal == nil {
			started++
		} else if r.resp.Refusal.Code != "village_busy" {
			t.Errorf("%s refused as %q", r.code, r.resp.Refusal.Code)
		}
	}
	if running := e.scalar(`SELECT count(*) FROM settlement_research WHERE settlement_id = $1::uuid AND status = 'running'`, e.cityID); running != 1 || started != 1 {
		t.Errorf("with one slot %d were started and %d are running", started, running)
	}
}
