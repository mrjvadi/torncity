//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Recipes at the stations, the tool tiers and crafting at home (ADR 0068): a workshop crew makes the recipe it is told to and not
// one whose research the town lacks; a better tool lifts and a lesser one lowers the output once the tiers count; a citizen
// crafts at a home station of his own from his own store.

type craftEnv struct {
	*farmEnv
}

func newCraftEnv(t *testing.T) *craftEnv {
	t.Helper()
	e := &craftEnv{newFarmEnv(t)}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM craft_jobs WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM game_actions WHERE action_type = 'craft_done'`)
	})
	return e
}

// grant gives the settlement a piece of knowledge itself.
func (e *craftEnv) grant(code string) {
	e.t.Helper()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, $2, 'researched', now())`,
		e.cityID, code); err != nil {
		e.t.Fatal(err)
	}
}

// shiftWith works one shift with a recipe and lets it end; "" when it was done, else the refusal kind.
func (e *craftEnv) shiftWith(who *application.Player, building, recipe string) string {
	e.t.Helper()
	resp, err := rrc(e.village.Work(testCtx(e.t), e.as(who, "settlement.work", "work"), handlers.VillageWorkRequest{ID: building, Recipe: recipe}))
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.Screen == screens.ScreenVillageRefusal {
		return refusalKind(e.t, resp.View)
	}
	e.clock.Advance(40 * time.Minute)
	for _, s := range e.workingShifts(building) {
		e.end(s)
	}
	return ""
}

func (e *craftEnv) lastBPS(building string) int64 {
	e.t.Helper()
	return e.scalar(`SELECT output_bps::bigint FROM settlement_shifts WHERE building_id = $1::uuid ORDER BY started_at DESC LIMIT 1`, building)
}

// A weaving shed spins and weaves in steps when told to, its standard shift stays what it was, and a recipe whose research the town
// lacks is refused with what is missing.
func TestAWorkshopMakesTheRecipeItIsToldTo(t *testing.T) {
	e := newCraftEnv(t)
	hand := e.resident()
	e.grant("weaving")
	shed := e.placeAt("weaving_shed", 40, 0)
	smithy := e.placeAt("smithy", 40, 10)
	e.stock("wool", 20)
	e.stock("roving", 8)
	e.stock("bar_iron", 6)
	e.stock("plank", 6)
	cloth0 := e.held("cloth")

	if got := e.shiftWith(hand, shed, "no_such_recipe"); got != "recipe_not_here" {
		t.Fatalf("an unknown recipe is refused: %q", got)
	}
	if got := e.shiftWith(hand, shed, "bar_iron"); got != "recipe_not_here" {
		t.Fatalf("another station's recipe is refused: %q", got)
	}
	// roving from wool, then yarn from roving
	if got := e.shiftWith(hand, shed, "roving"); got != "" {
		t.Fatalf("spinning roving: refused as %q", got)
	}
	if e.held("wool") >= 20 || e.held("roving") <= 0 {
		t.Errorf("wool went into the roving: wool %d, roving %d", e.held("wool"), e.held("roving"))
	}
	if got := e.shiftWith(hand, shed, "yarn"); got != "" {
		t.Fatalf("spinning yarn: refused as %q", got)
	}
	if e.held("yarn") <= 0 {
		t.Error("the yarn is made from the roving")
	}
	if e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND recipe = 'roving' AND status = 'done'`, shed) != 1 {
		t.Error("the shift records its recipe")
	}
	// the standard shift is untouched
	if got := e.shiftWith(hand, shed, ""); got != "" {
		t.Fatalf("the standard shift: %q", got)
	}
	if e.held("cloth") <= cloth0 {
		t.Error("the standard shift of the shed makes cloth from wool")
	}
	// a recipe whose research the town lacks
	if got := e.shiftWith(hand, smithy, "forged_tools"); got != "prerequisite" {
		t.Fatalf("forged tools need smithing_ii: refused as %q", got)
	}
	e.grant("smithing_ii")
	e.grant("smithing")
	if got := e.shiftWith(hand, smithy, "forged_tools"); got != "" {
		t.Fatalf("forged tools with the research: %q", got)
	}
	if e.held("forged_tools") <= 0 || e.held("bar_iron") >= 6 {
		t.Errorf("bar iron became forged tools: bar iron %d, forged %d", e.held("bar_iron"), e.held("forged_tools"))
	}
	e.verify()
}

// A crew told to make yarn makes yarn; told to make a recipe it has no research for it pauses with that reason.
func TestACrewMakesTheRecipeOfItsJob(t *testing.T) {
	e := newCraftEnv(t)
	ctx := testCtx(t)
	e.grant("weaving")
	shed := e.placeAt("weaving_shed", 40, 0)
	e.stock("roving", 30)
	if _, err := rrc(e.village.LaborPost(ctx, e.as(e.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: shed, Recipe: "yarn"})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'production' AND status = 'open'`, shed).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(e.village.LaborHire(ctx, e.as(e.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "1"})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		e.clock.Advance(40 * time.Minute)
		for _, s := range e.workingShifts(shed) {
			e.end(s)
		}
	}
	if e.held("yarn") <= 0 || e.scalar(`SELECT count(*) FROM settlement_shifts WHERE job_id = $1::uuid AND recipe = 'yarn'`, jobID) == 0 {
		t.Errorf("the crew spins yarn from the job's recipe: yarn %d", e.held("yarn"))
	}
	// the head changes the recipe of the open job to one the town cannot make yet
	if _, err := rrc(e.village.LaborPost(ctx, e.as(e.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: shed, Recipe: "woollen_cloth"})); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM labor_jobs WHERE id = $1::uuid AND recipe = 'woollen_cloth'`, jobID); got != 1 {
		t.Errorf("posting again with a recipe changes the open job: %d", got)
	}
	e.verify()
}

// The tool tiers count from the rule date plus the grace: before it nobody's output changes; after it a worker with only a lesser
// tool keeps 60 percent per tier short, a better tool is enough and wears slower.
func TestToolTiersLiftAndLowerTheOutputOnceTheyCount(t *testing.T) {
	e := newCraftEnv(t)
	hand := e.resident()
	shop := e.placeAt("carpentry_workshop", 40, 0) // needs tier 2 (sawn planks)
	e.grant("carpentry")
	e.stock("timber", 40)
	e.stock("tools", 3)

	// the tiers are off: the iron tool is the tool the work needs, as it always was
	e.village.WithCrafting(application.CraftRules{})
	if got := e.shiftWith(hand, shop, ""); got != "" {
		t.Fatal(got)
	}
	base := e.lastBPS(shop)

	// the rule date has come and the grace is over: an iron tool is a tier short for sawing
	e.village.WithCrafting(application.CraftRules{RuleAt: e.clock.Now().Add(-30 * 24 * time.Hour), GraceDays: 14})
	if got := e.shiftWith(hand, shop, ""); got != "" {
		t.Fatal(got)
	}
	if got := e.lastBPS(shop); got < base*5900/10000 || got > base*6100/10000 {
		t.Errorf("a tier 1 tool at tier 2 work keeps 60 percent: %d of %d", got, base)
	}
	// with the grace still running nothing changes
	e.village.WithCrafting(application.CraftRules{RuleAt: e.clock.Now().Add(-2 * 24 * time.Hour), GraceDays: 14})
	if got := e.shiftWith(hand, shop, ""); got != "" {
		t.Fatal(got)
	}
	if got := e.lastBPS(shop); got < base*9900/10000 {
		t.Errorf("in the grace the output is whole: %d of %d", got, base)
	}
	// a forged tool is enough and wears slower than an iron one would
	e.village.WithCrafting(application.CraftRules{RuleAt: e.clock.Now().Add(-30 * 24 * time.Hour), GraceDays: 14})
	e.stock("forged_tools", 2)
	if got := e.shiftWith(hand, shop, ""); got != "" {
		t.Fatal(got)
	}
	if got := e.lastBPS(shop); got < base*9900/10000 {
		t.Errorf("a tier 2 tool does the tier 2 work in full: %d of %d", got, base)
	}
	e.verify()
}

// A citizen crafts at a home station of his own: the goods leave his store, come back at the home yield when the job ends, a third
// job is refused, and a station that does not make the recipe refuses it.
func TestACitizenCraftsAtHome(t *testing.T) {
	e := newCraftEnv(t)
	ctx := testCtx(t)
	owner := e.resident()
	stranger := e.resident()
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, owner.ID) })
	e.grant("carpentry")
	for i := 0; i < 3; i++ {
		e.yard(owner)
	}
	// the owner's bench: a carpentry shop of his own stands for the carpentry station
	bench := newUUID(t)
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'carpentry_workshop_own', 41, 40, 'complete', now(), now())`, bench, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, 2000, now())`, bench, e.cityID, owner.ID); err != nil {
		t.Fatal(err)
	}
	e.give(owner, "timber", 12)
	craft := func(who *application.Player, recipe, batches string) (string, string) {
		resp, err := rrc(e.village.Craft(ctx, e.as(who, "settlement.craft", "craft"), handlers.VillageCraftRequest{Building: bench, Recipe: recipe, Batches: batches}))
		if err != nil {
			t.Fatal(err)
		}
		if resp.Screen == screens.ScreenVillageRefusal {
			return refusalKind(t, resp.View), resp.Screen
		}
		return "", resp.Screen
	}
	if k, _ := craft(stranger, "planks", "1"); k == "" {
		t.Fatal("a stranger cannot craft at another citizen's bench")
	}
	if k, _ := craft(owner, "flour", "1"); k != "craft_no_station" {
		t.Fatalf("a carpentry bench does not mill: %q", k)
	}
	if k, _ := craft(owner, "planks", "9"); k != "craft_batches" {
		t.Fatalf("a job is a few batches: %q", k)
	}
	if k, _ := craft(owner, "planks", "2"); k != "" {
		t.Fatalf("two batches of planks: %q", k)
	}
	if got := e.homeHeld(owner, "timber"); got != 10 {
		t.Errorf("two batches took two timber out of his store: %d left of 12", got)
	}
	if k, _ := craft(owner, "planks", "1"); k != "" {
		t.Fatalf("a second job runs at once: %q", k)
	}
	if k, _ := craft(owner, "planks", "1"); k != "craft_too_many" {
		t.Fatalf("a third job is refused: %q", k)
	}
	var first string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM craft_jobs WHERE player_id = $1::uuid AND batches = 2`, owner.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	// the job ends on the clock, once
	e.clock.Advance(45 * time.Minute)
	var actionID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT game_action_id::text FROM craft_jobs WHERE id = $1::uuid`, first).Scan(&actionID); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"id":"` + first + `","settlement_id":"` + e.cityID + `"}`)
	for i := 0; i < 2; i++ {
		if _, err := e.village.CraftDone(ctx, e.as(e.head, "settlement.craft.done", "craft.done"), handlers.CrimeScheduledRequest{ReferenceID: first, ActionID: actionID, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	// 2 batches of timber 1 to plank 3 is 6 planks at 90 percent: 5, the fraction carried
	if got := e.homeHeld(owner, "plank"); got != 5 {
		t.Errorf("six planks at the home yield: %d", got)
	}
	if e.scalar(`SELECT count(*) FROM craft_jobs WHERE id = $1::uuid AND status = 'done'`, first) != 1 {
		t.Error("the job is done")
	}
	e.verify()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := v.VillageInvariants; i.CraftInputsJournal != i.CraftInputsRows || i.CraftMadeJournal != i.CraftMadeRows || i.CraftOverPlanned != 0 {
		t.Errorf("the craft books do not agree: %+v", i)
	}
}
