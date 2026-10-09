//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The real village goods (ADR 0050, plan A2): firewood, paper, tools, clay, hide and rags have producers and users, the
// older stand-ins serve during the grace only, and a workplace without tools works bare-handed once the grace is over.
// The rules are built the way the service builds them (cmd/game realItemRules), from the shipped config.

func realItemRules(cfg *config.Config, now time.Time, ruleAgo time.Duration) handlers.RealItemRules {
	return handlers.RealItemRules{From: now.Add(-ruleAgo), GraceDays: cfg.Settlement.RealItemsGraceDays, BareHandsBPS: cfg.Settlement.ToolBareHandsBPS}
}

// The research upkeep is paper and firewood; during the grace wool and timber still serve a settlement with none of them,
// afterwards they do not, and a stock of the real goods is always used first.
func TestRealItemsResearchStandInsServeOnlyInTheGrace(t *testing.T) {
	e := newResearchEnv(t)
	e.village.WithRealItems(realItemRules(e.cfg, e.clock.Now(), time.Hour))
	lib := e.building("library")
	_ = lib
	e.stock("wool", 3)

	d, resp := e.desk(e.head, "", "")
	if resp.Refusal != nil {
		t.Fatalf("%s", resp.Text)
	}
	if len(d.Buildings) != 1 || !d.Buildings[0].Open {
		t.Fatalf("in the grace the library works on wool: %+v", d.Buildings)
	}
	u := d.Buildings[0].Upkeep
	if len(u) != 1 || u[0].Item.Code != "paper" || u[0].StandIn.Code != "wool" || d.StandInUntil.IsZero() {
		t.Errorf("the desk should name the stand-in and when it ends: %+v until %v", u, d.StandInUntil)
	}
	if e.held("wool") != 2 || e.held("paper") != 0 {
		t.Errorf("one wool used, no paper: wool %d paper %d", e.held("wool"), e.held("paper"))
	}

	// a day with paper in the stock uses the paper, not the wool
	e.clock.Advance(26 * time.Hour)
	e.stock("paper", 2)
	e.desk(e.head, "", "")
	if e.held("paper") != 1 || e.held("wool") != 2 {
		t.Errorf("paper first: paper %d wool %d", e.held("paper"), e.held("wool"))
	}

	// the grace is over (7 days after the rule): the wool no longer serves
	e.clock.Advance(9 * 24 * time.Hour)
	e.clock.Advance(26 * time.Hour)
	e.drainStock("paper")
	d, _ = e.desk(e.head, "", "")
	if len(d.Buildings) != 1 || d.Buildings[0].Open || d.Buildings[0].Idle != village.ResearchIdleNoUpkeep {
		t.Errorf("after the grace wool does not stand in for paper: %+v", d.Buildings)
	}
	if e.held("wool") != 2 {
		t.Errorf("a day that did not work uses nothing: wool %d", e.held("wool"))
	}
	if !d.StandInUntil.IsZero() {
		t.Errorf("the desk still announces a stand-in: %v", d.StandInUntil)
	}
	e.verify()
}

// drainStock removes an item from the settlement's store (the test's own housekeeping).
func (e *researchEnv) drainStock(item string) {
	e.t.Helper()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2`, e.cityID, item); err != nil {
		e.t.Fatal(err)
	}
}

// A hearth burns firewood; timber serves in its place during the grace only.
func TestRealItemsHearthBurnsTimberOnlyInTheGrace(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	e.village.WithCitizenRules(handlers.CitizenRules{
		LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000, TaxBPS: 200, TaxBPSMax: 500,
		TaxPeriod: 24 * time.Hour, MaterialMarkupBPS: 12_000, MaxLotsPerPlayer: 3, PrivateShareMaxBPS: 6000,
		HomeRestCooldown: time.Hour, HomeRestHealth: 40, HomeRestHappiness: 20,
	})
	cfg := config.Defaults()
	e.village.WithRealItems(realItemRules(cfg, e.clock.Now(), time.Hour))
	id := e.house(owner, "private_cottage", 800)
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id}); r.Refusal != nil {
		t.Fatalf("opening the lot: %+v", r.Refusal)
	}
	rest := func() string {
		t.Helper()
		resp, err := rrc(e.village.HomeRest(testCtx(t), e.as(owner, "settlement.home.rest", "home.rest")))
		if err != nil {
			t.Fatal(err)
		}
		var v village.MineView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v.Notice
	}
	e.give(owner, application.HoldHome, "timber", 3)
	if n := rest(); n != "rested_warm" || e.held(owner, application.HoldHome, "timber") != 2 {
		t.Errorf("in the grace timber still warms the rest: %q, timber %d", n, e.held(owner, application.HoldHome, "timber"))
	}
	// firewood first when the home has it
	e.clock.Advance(2 * time.Hour)
	e.give(owner, application.HoldHome, "firewood", 1)
	if n := rest(); n != "rested_warm" || e.held(owner, application.HoldHome, "firewood") != 0 || e.held(owner, application.HoldHome, "timber") != 2 {
		t.Errorf("firewood is burnt before timber: %q, firewood %d timber %d", n, e.held(owner, application.HoldHome, "firewood"), e.held(owner, application.HoldHome, "timber"))
	}
	// after the grace only firewood burns
	e.clock.Advance(8 * 24 * time.Hour)
	if n := rest(); n != "rested" || e.held(owner, application.HoldHome, "timber") != 2 {
		t.Errorf("after the grace timber does not burn: %q, timber %d", n, e.held(owner, application.HoldHome, "timber"))
	}
	e.verifyLots()
}

// workplaceEnv is a research env with a finished workplace, a resident to work in it and food for the shifts.
type workplaceEnv struct {
	*researchEnv
	camp   string
	worker *application.Player
}

func newWorkplaceEnv(t *testing.T, code string, knowledge ...string) *workplaceEnv {
	t.Helper()
	e := newResearchEnv(t)
	ctx := testCtx(t)
	for _, k := range knowledge {
		if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, $2, 'researched', now())`, e.cityID, k); err != nil {
			t.Fatal(err)
		}
	}
	w := &workplaceEnv{researchEnv: e, camp: e.building(code), worker: e.scholar()}
	e.stock("wheat", 40)
	return w
}

// shift runs one shift to its end.
func (w *workplaceEnv) shift() { w.shiftIn(w.camp) }

// shiftIn runs one shift in a building of the settlement to its end (the longest shift is the charcoal clamp's 8 hours).
func (w *workplaceEnv) shiftIn(id string) {
	w.t.Helper()
	if _, err := rrc(w.village.Work(testCtx(w.t), w.as(w.worker, "settlement.work", "work"), handlers.VillageWorkRequest{ID: id})); err != nil {
		w.t.Fatal(err)
	}
	w.clock.Advance(9 * time.Hour)
	for _, s := range w.workingShifts(id) {
		w.end(s)
	}
}

func (w *workplaceEnv) lastOutputBPS() int64 {
	return w.scalar(`SELECT output_bps FROM settlement_shifts WHERE building_id = $1::uuid ORDER BY started_at DESC, id LIMIT 1`, w.camp)
}

func (w *workplaceEnv) setWear(n int64) {
	w.t.Helper()
	if _, err := w.pool.Raw().Exec(testCtx(w.t), `UPDATE settlement_buildings SET carry = jsonb_build_object('~tools', $2::bigint) WHERE id = $1::uuid`, w.camp, n); err != nil {
		w.t.Fatal(err)
	}
}

func (w *workplaceEnv) wear() int64 {
	return w.scalar(`SELECT COALESCE((carry->>'~tools')::bigint, 0) FROM settlement_buildings WHERE id = $1::uuid`, w.camp)
}

// A workplace wears a share of a tool every shift; a whole tool leaves the store when it is due; with none the shift yields
// a share of its output once the grace is over, and the full output during it.
func TestRealItemsToolsWearOnTheWorkplace(t *testing.T) {
	w := newWorkplaceEnv(t, "woodcutter_camp")
	cfg := w.cfg
	// the grace is over
	w.village.WithRealItems(realItemRules(cfg, w.clock.Now(), 30*24*time.Hour))

	w.setWear(9_800) // 300 more makes a whole tool due
	w.shift()
	full := w.lastOutputBPS()
	if w.wear() != 10_000 {
		t.Errorf("without a tool the due tool stays due, capped at one: %d", w.wear())
	}
	bare := full
	if full <= 0 {
		t.Fatalf("a shift without tools still yields something: %d", full)
	}
	if want := cfg.Settlement.ToolBareHandsBPS; want >= 10_000 {
		t.Fatalf("the shipped bare-hands share is below the whole: %d", want)
	}
	// the same workplace with a tool at hand: the tool is used and the output is whole
	w.stock("tools", 2)
	w.shift()
	whole := w.lastOutputBPS()
	if w.held("tools") != 1 {
		t.Errorf("a due tool leaves the store: %d left of 2", w.held("tools"))
	}
	// the rung and the workplace's condition move a little between two shifts: compare within a twentieth
	if want := whole * cfg.Settlement.ToolBareHandsBPS / 10_000; bare < want-whole/20-1 || bare > want+whole/20+1 {
		t.Errorf("bare-handed %d should be %d of the whole %d", bare, cfg.Settlement.ToolBareHandsBPS, whole)
	}
	if w.wear() != 300 {
		t.Errorf("after the tool the wear starts again at the shift's own 300: %d", w.wear())
	}
	if n := w.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE item_code = 'tools' AND reason = 'production_input' AND from_org = $1::uuid`, w.cityID); n != 1 {
		t.Errorf("the tool is in the item journal as a shift input: %d", n)
	}
	lv, err := postgres.NewEconomyAdmin(w.pool).VerifyLedger(testCtx(t), 10)
	if err != nil {
		t.Fatal(err)
	}
	if s := lv.VillageInvariants; s.ToolInputs < 1 || s.ToolInputs != s.ToolShiftUnits || s.ToolWearBroken != 0 {
		t.Errorf("the tool wear invariants: %+v", s)
	}
	w.verify()

}

// During the grace a missing tool costs no output.
func TestRealItemsNoToolCostsNothingInTheGrace(t *testing.T) {
	g := newWorkplaceEnv(t, "woodcutter_camp")
	g.village.WithRealItems(realItemRules(g.cfg, g.clock.Now(), time.Hour))
	g.shift() // a first shift, to know the whole output
	whole := g.lastOutputBPS()
	g.setWear(9_900)
	g.shift()
	if got := g.lastOutputBPS(); got < whole-whole/20-1 {
		t.Errorf("in the grace a missing tool costs no output: %d, want about %d", got, whole)
	}
}

// Each new producer works: what it consumes leaves the store, what it makes arrives, and the chain links hold (firewood
// from the woodcutter, rags from the weaving shed, paper from rags, pots from clay and firewood, hides from the
// pasture, tools from timber and stone, clay from the pit).
func TestRealItemsProducersMakeThem(t *testing.T) {
	for _, c := range []struct {
		building  string
		knowledge []string
		in        map[string]int64
		out       []string
	}{
		{"woodcutter_camp", nil, nil, []string{"timber", "firewood", "bark"}},
		{"weaving_shed", []string{"weaving"}, map[string]int64{"wool": 30}, []string{"cloth", "rag"}},
		{"paper_mill", []string{"papermaking"}, map[string]int64{"rag": 30}, []string{"paper"}},
		{"pottery_kiln", []string{"pottery"}, map[string]int64{"clay": 30, "firewood": 10}, []string{"pots"}},
		{"clay_pit", []string{"pottery"}, nil, []string{"clay"}},
		{"tool_workshop", []string{"carpentry"}, map[string]int64{"timber": 10, "stone": 10}, []string{"tools"}},
		{"pasture_range", []string{"open_range_herding"}, nil, []string{"wool", "hide"}},
		// the function rows that became workplaces (ADR 0051)
		{"well", nil, nil, []string{"spring_water"}},
		{"mill", []string{"milling"}, map[string]int64{"wheat": 30}, []string{"flour_sack"}},
		{"bakery", []string{"milling"}, map[string]int64{"flour_sack": 30, "spring_water": 10, "firewood": 6}, []string{"bread"}},
		{"charcoal_clamp", []string{"charcoal_burning"}, map[string]int64{"firewood": 30}, []string{"charcoal"}},
		{"iron_pit", []string{"smithing"}, nil, []string{"iron_ore"}},
		{"bloomery", []string{"bloomery"}, map[string]int64{"iron_ore": 30, "charcoal": 30}, []string{"bloom"}},
		{"smithy", []string{"smithing"}, map[string]int64{"bloom": 10, "charcoal": 10}, []string{"tools"}},
		// A4b: leather from hide, bark and water; bricks from clay and fuel (ADR 0052)
		{"tannery", []string{"tanning"}, map[string]int64{"hide": 6, "bark": 6, "spring_water": 12}, []string{"leather"}},
		{"brickworks", []string{"pottery", "masonry"}, map[string]int64{"clay": 20, "firewood": 10}, []string{"brick"}},
	} {
		t.Run(c.building, func(t *testing.T) {
			w := newWorkplaceEnv(t, c.building, c.knowledge...)
			w.village.WithRealItems(realItemRules(w.cfg, w.clock.Now(), time.Hour)) // in the grace: tools are not asked
			before := map[string]int64{}
			for item, n := range c.in {
				w.stock(item, n)
			}
			for item := range c.in {
				before[item] = w.held(item)
			}
			for i := 0; i < 4; i++ {
				w.shift()
			}
			for _, o := range c.out {
				if w.held(o) <= before[o] {
					t.Errorf("%s made no %s: %d in the store", c.building, o, w.held(o))
				}
			}
			for item, n := range before {
				if w.held(item) >= n {
					t.Errorf("%s used no %s: %d left of %d", c.building, item, w.held(item), n)
				}
			}
			w.verify()
		})
	}
}

// The iron tier end to end: an iron pit digs ore, a charcoal clamp burns firewood, the bloomery smelts the two into a
// bloom, the smithy forges it into tools; nothing but firewood and food is put in by hand. Each link is a standing
// workplace with its own shifts, and a missing link stops what comes after it.
func TestRealItemsIronTierChain(t *testing.T) {
	w := newWorkplaceEnv(t, "iron_pit", "smithing", "charcoal_burning", "bloomery")
	w.village.WithRealItems(realItemRules(w.cfg, w.clock.Now(), time.Hour))
	clamp, bloomery, smithy := w.building("charcoal_clamp"), w.building("bloomery"), w.building("smithy")
	w.stock("firewood", 40)
	// without ore and charcoal the bloomery cannot start
	if _, err := rrc(w.village.Work(testCtx(t), w.as(w.worker, "settlement.work", "work"), handlers.VillageWorkRequest{ID: bloomery})); err != nil {
		t.Fatal(err)
	}
	if n := len(w.workingShifts(bloomery)); n != 0 {
		t.Fatalf("a bloomery with no ore and charcoal started %d shifts", n)
	}
	for i := 0; i < 2; i++ {
		w.shift()
	}
	for i := 0; i < 2; i++ {
		w.shiftIn(clamp)
	}
	if w.held("iron_ore") < 6 || w.held("charcoal") < 1 {
		t.Fatalf("ore %d, charcoal %d", w.held("iron_ore"), w.held("charcoal"))
	}
	w.stock("charcoal", 8) // the clamp is slow: a night's burn is two sacks; the rest is topped up by hand
	for i := 0; i < 3 && w.held("bloom") < 1; i++ {
		w.shiftIn(bloomery)
	}
	if w.held("bloom") < 1 {
		t.Fatalf("no bloom: ore %d charcoal %d", w.held("iron_ore"), w.held("charcoal"))
	}
	w.stock("charcoal", 2) // the forge's heat; the clamp is slow
	for i := 0; i < 3 && w.held("tools") < 1; i++ {
		w.shiftIn(smithy)
	}
	if w.held("tools") < 1 {
		t.Errorf("the smithy made no tools: bloom %d charcoal %d", w.held("bloom"), w.held("charcoal"))
	}
	w.verify()
}
