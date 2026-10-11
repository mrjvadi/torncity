//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// Survival (ADR 0070): thirst, warmth, garments, sickness and the collapse into care, in a founded settlement; the neutral city is exempt.

type survivalEnv struct {
	*careEnv
	sv *handlers.SurvivalHandler
}

func newSurvivalEnv(t *testing.T) *survivalEnv {
	t.Helper()
	e := &survivalEnv{careEnv: newCareEnv(t)}
	clock, err := e.cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	rules := application.SurvivalRules{RuleAt: e.clock.Now().Add(-48 * time.Hour), GraceDays: 0}
	e.sv = handlers.NewSurvivalHandler(postgres.NewUnitOfWork(e.pool, testDefaultLanguage), workIDs{t}, nil, staticContentSource{snap: loadTestContent(t)},
		e.cache, postgres.NewCityRepository(e.pool), gametime.Scale(1), e.clock.Now).WithRules(rules, e.village, e.village, clock)
	e.village.WithSurvivalFactor(e.sv.ShiftFactor)
	return e
}

// citizen is a resident standing in the settlement.
func (e *survivalEnv) citizen() *application.Player {
	e.t.Helper()
	p := e.resident()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, p.ID, e.cityID); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *survivalEnv) view(resp *presentation.Response, err error) plife.SurvivalView {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
	var v plife.SurvivalView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		e.t.Fatalf("the survival view (%s): %v", resp.Screen, err)
	}
	return v
}

func (e *survivalEnv) needs(p *application.Player) plife.SurvivalView {
	e.t.Helper()
	resp, err := e.sv.Needs(testCtx(e.t), e.as(p, "health.survival", "survival"))
	return e.view(resp, err)
}

func (e *survivalEnv) drink(p *application.Player, source string) (plife.SurvivalView, string) {
	e.t.Helper()
	resp, err := e.sv.Drink(testCtx(e.t), e.as(p, "health.drink", "drink"), handlers.SurvivalRequest{Source: source})
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.Refusal != nil {
		return plife.SurvivalView{}, resp.Refusal.Code
	}
	return e.view(resp, nil), ""
}

// give puts goods in the player's bag.
func (e *survivalEnv) give(p *application.Player, item string, qty int64) {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, To: p.ID, ToHolding: application.HoldCarried,
			Reason: application.ItemGrant, At: e.clock.Now()})
	}); err != nil {
		e.t.Fatal(err)
	}
}

// placeAtCell moves the settlement to the land cell the pick ranks first (the lowest score), so the weather there is known.
func (e *survivalEnv) placeAtCell(score func(cellTemp int32, river bool, lake bool) (int, bool)) {
	e.t.Helper()
	_, w, err := e.cache.Active(testCtx(e.t))
	if err != nil {
		e.t.Fatal(err)
	}
	best, bestScore := -1, 0
	for i, c := range w.Cells {
		if c.IsOcean {
			continue
		}
		if sc, ok := score(int32(c.Temperature), c.RiverFlow > 0, c.IsLake); ok && (best < 0 || sc < bestScore) {
			best, bestScore = i, sc
		}
	}
	if best < 0 {
		e.t.Skip("the test world has no such cell")
	}
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `UPDATE cities SET world_cell_id = $2 WHERE id = $1::uuid`, e.cityID, best); err != nil {
		e.t.Fatal(err)
	}
}

func (e *survivalEnv) verifySurvival() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Survival || s.SurvivalGarmentJournal != s.SurvivalGarmentRows || s.SurvivalDoubleSlot != 0 {
		e.t.Errorf("the survival invariants do not hold: %+v", s)
	}
	e.verifyCare()
}

// The neutral city has no thirst or cold; a founded settlement does; before the rule date nothing counts.
func TestSurvivalOnlyCountsInFoundedSettlements(t *testing.T) {
	e := newSurvivalEnv(t)
	p := e.citizen()
	if v := e.needs(p); !v.Founded {
		t.Fatalf("a founded settlement has thirst and cold: %+v", v)
	}
	var support string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM cities WHERE code = 'support'`).Scan(&support); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, p.ID, support); err != nil {
		t.Fatal(err)
	}
	if v := e.needs(p); v.Founded {
		t.Fatalf("the neutral city is exempt: %+v", v)
	}
	// before the rule date plus its grace
	e.sv.WithRules(application.SurvivalRules{RuleAt: e.clock.Now().Add(24 * time.Hour), GraceDays: 14}, e.village, e.village, gametime.Clock{})
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, p.ID, e.cityID); err != nil {
		t.Fatal(err)
	}
	if v := e.needs(p); v.Founded {
		t.Fatalf("before the rule nothing counts: %+v", v)
	}
}

// Thirst rises; a drink at the well takes a unit of the settlement's water and lowers it; the cooldown holds; a dry well refuses; left
// without water for days the player collapses into a hospital stay of cause dehydration, once.
func TestThirstDrinkingAndTheCollapseIntoCare(t *testing.T) {
	e := newSurvivalEnv(t)
	p := e.citizen()
	e.placeAt("well", 30, 30)
	e.stock("spring_water", 3)
	e.needs(p)
	e.clock.Advance(26 * time.Hour)
	v := e.needs(p)
	if v.Thirst < 35 || v.Thirst > 50 {
		t.Fatalf("a day of thirst is about 40 points: %d", v.Thirst)
	}
	v, refused := e.drink(p, plife.DrinkWell)
	if refused != "" || v.Thirst > 25 || e.held("spring_water") != 2 {
		t.Fatalf("a drink at the well: refused %q, thirst %d, water left %d", refused, v.Thirst, e.held("spring_water"))
	}
	if _, refused := e.drink(p, plife.DrinkWell); refused != "health_drink_wait" {
		t.Errorf("a second drink at once waits: %q", refused)
	}
	if e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'drunk' AND from_org = $1::uuid`, e.cityID) != 1 {
		t.Error("the water left the settlement stock as drunk")
	}
	// the bag
	e.give(p, "spring_water", 2)
	e.clock.Advance(time.Minute)
	if _, refused := e.drink(p, plife.DrinkBag); refused != "" {
		t.Errorf("a drink from the bag: %q", refused)
	}
	// no water at all: the dry well refuses
	e.drainStock("spring_water")
	e.clock.Advance(time.Minute)
	if _, refused := e.drink(p, plife.DrinkWell); refused != "health_no_water" {
		t.Errorf("a dry well: %q", refused)
	}
	// days without water: collapse into care, once
	e.clock.Advance(5 * 24 * time.Hour)
	v = e.needs(p)
	if v.Notice != plife.NoticeCollapsed {
		t.Fatalf("five days without water: %+v", v)
	}
	if e.scalar(`SELECT count(*) FROM hospital_stays WHERE player_id = $1::uuid AND cause = 'dehydration'`, p.ID) != 1 {
		t.Fatal("a collapse is a hospital stay of cause dehydration")
	}
	e.needs(p)
	if e.scalar(`SELECT count(*) FROM hospital_stays WHERE player_id = $1::uuid AND cause = 'dehydration'`, p.ID) != 1 {
		t.Error("lying in bed, the player is not admitted again")
	}
	e.verifySurvival()
}

// In a cold place a bare person freezes toward the worst; garments put on lower it; each costs a unit out of the bag; a garment worn
// out vanishes; the wardrobe keeps what is taken off.
func TestWarmthGarmentsAndTheWardrobe(t *testing.T) {
	e := newSurvivalEnv(t)
	e.placeAtCell(func(temp int32, _, _ bool) (int, bool) { return int(temp), true }) // the coldest land
	p := e.citizen()
	e.needs(p)
	e.clock.Advance(14 * time.Hour)
	bare := e.needs(p)
	if bare.Warmth < 40 {
		t.Skipf("the cold place was not cold enough on this day: %d", bare.Warmth)
	}
	for _, item := range []string{"wool_cloak", "wool_tunic", "wool_cap", "wool_mittens", "hide_boots", "wool_vest"} {
		e.give(p, item, 1)
		resp, err := e.sv.Wear(testCtx(t), e.as(p, "health.wear", "wear"), handlers.SurvivalRequest{Item: item})
		if v := e.view(resp, err); v.Notice != plife.NoticeDressed {
			t.Fatalf("putting on %s: %+v", item, v)
		}
	}
	if e.scalar(`SELECT count(*) FROM player_garments WHERE player_id = $1::uuid AND worn`, p.ID) != 6 {
		t.Error("six garments are worn")
	}
	if e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'garment_worn' AND from_player = $1::uuid`, p.ID) != 6 {
		t.Error("each garment left the bag through the item journal")
	}
	e.clock.Advance(14 * time.Hour)
	dressed := e.needs(p)
	if dressed.Warmth >= bare.Warmth {
		t.Errorf("clothes lower the cold: bare %d, dressed %d", bare.Warmth, dressed.Warmth)
	}
	if len(dressed.Worn) != 6 {
		t.Errorf("the screen lists what is worn: %+v", dressed.Worn)
	}
	// a second cloak replaces the first: one per slot
	e.give(p, "hide_wrap", 1)
	resp, err := e.sv.Wear(testCtx(t), e.as(p, "health.wear", "wear"), handlers.SurvivalRequest{Item: "hide_wrap"})
	v := e.view(resp, err)
	if len(v.Worn) != 6 || len(v.Kept) != 1 {
		t.Errorf("the hide wrap took the cloak's slot and the cloak is kept in the wardrobe: worn %d, kept %d", len(v.Worn), len(v.Kept))
	}
	// taking off keeps it
	resp, err = e.sv.Remove(testCtx(t), e.as(p, "health.remove", "remove"), handlers.SurvivalRequest{Slot: "head"})
	if v := e.view(resp, err); len(v.Worn) != 5 || len(v.Kept) != 2 {
		t.Errorf("taking off the cap: worn %d, kept %d", len(v.Worn), len(v.Kept))
	}
	// worn out: a garment worn for more than its days is gone
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE player_garments SET uses_left = 5 WHERE player_id = $1::uuid AND item = 'hide_boots'`, p.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Hour)
	v = e.needs(p)
	if e.scalar(`SELECT count(*) FROM player_garments WHERE player_id = $1::uuid AND item = 'hide_boots' AND status = 'worn_out'`, p.ID) != 1 {
		t.Errorf("the boots wore out: %+v", v.Worn)
	}
	// a shift in this cold costs a share of its output
	if f, err := e.shiftFactorOf(p); err != nil || f > 10_000 {
		t.Errorf("the shift factor: %d %v", f, err)
	}
	e.verifySurvival()
}

func (e *survivalEnv) shiftFactorOf(p *application.Player) (int64, error) {
	var f int64
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		var err error
		f, err = e.sv.ShiftFactor(ctx, tx, p, e.clock.Now())
		return err
	})
	return f, err
}

// River water without boiling can sicken; boiling costs a firewood and never does; a sickness is treated at an open health house with a
// medicine of the settlement stock and ends sooner.
func TestRiverWaterBoilingAndTheCure(t *testing.T) {
	e := newSurvivalEnv(t)
	e.placeAtCell(func(temp int32, river, lake bool) (int, bool) { return int(temp), river || lake })
	p := e.citizen()
	e.give(p, "firewood", 40)
	e.needs(p)
	// boiled water never sickens, and burns a firewood each
	for i := 0; i < 12; i++ {
		e.clock.Advance(time.Minute)
		v, refused := e.drink(p, plife.DrinkBoiled)
		if refused != "" || len(v.Illness) != 0 {
			t.Fatalf("boiled river water %d: refused %q, illness %+v", i, refused, v.Illness)
		}
	}
	if e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'boiled' AND from_player = $1::uuid`, p.ID) != 12 {
		t.Error("each boiling burnt a firewood")
	}
	// raw river water: sooner or later it sickens
	var sick plife.SurvivalView
	for i := 0; i < 80 && len(sick.Illness) == 0; i++ {
		e.clock.Advance(7 * time.Minute)
		sick, _ = e.drink(p, plife.DrinkRiver)
	}
	if len(sick.Illness) == 0 || sick.Illness[0].Kind != application.IllnessWaterborne {
		t.Fatalf("eighty drinks of unboiled river water sicken someone: %+v", sick)
	}
	// no health house yet: nothing to treat with
	resp, err := e.sv.Cure(testCtx(t), e.as(p, "health.cure", "cure"))
	if err != nil || resp.Refusal == nil {
		t.Fatalf("no health house, no cure: %v %+v", err, resp)
	}
	e.building("health_house")
	e.stock("cloth", 4)
	e.stock("spring_water", 8)
	e.stock("painkillers", 2)
	before := e.scalar(`SELECT EXTRACT(EPOCH FROM ends_at)::bigint FROM player_illness WHERE player_id = $1::uuid AND status = 'active'`, p.ID)
	if v := e.needs(p); !v.CanCure {
		t.Fatalf("an open house with painkillers can treat: %+v", v)
	}
	resp, err = e.sv.Cure(testCtx(t), e.as(p, "health.cure", "cure"))
	if v := e.view(resp, err); v.Notice != plife.NoticeCured {
		t.Fatalf("the cure: %+v", v)
	}
	after := e.scalar(`SELECT EXTRACT(EPOCH FROM ends_at)::bigint FROM player_illness WHERE player_id = $1::uuid AND status = 'active'`, p.ID)
	if after >= before || e.held("painkillers") != 1 {
		t.Errorf("a medicine shortens the course: %d -> %d, painkillers left %d", before, after, e.held("painkillers"))
	}
	if e.scalar(`SELECT count(*) FROM player_illness WHERE player_id = $1::uuid AND medicine_units = 1`, p.ID) != 1 {
		t.Error("the treatment is recorded on the sickness")
	}
	// a second cure is refused: one treatment per sickness
	resp, err = e.sv.Cure(testCtx(t), e.as(p, "health.cure", "cure"))
	if err != nil || resp.Refusal == nil {
		t.Errorf("one treatment per sickness: %v", err)
	}
	// the sickness ends by itself
	e.clock.Advance(10 * 24 * time.Hour)
	if v := e.needs(p); len(v.Illness) != 0 {
		t.Errorf("the sickness is over: %+v", v.Illness)
	}
	e.verifySurvival()
}

// A warming house open for the day (a keeper from the pool, firewood in the stock) shelters whoever warms himself at it; closed, it does
// not.
func TestTheWarmingHouseShelters(t *testing.T) {
	e := newSurvivalEnv(t)
	p := e.citizen()
	e.placeAt("warming_house", 30, 40)
	e.needs(p)
	if resp, err := e.sv.Warm(testCtx(t), e.as(p, "health.warm", "warm")); err != nil || resp.Refusal == nil {
		t.Fatalf("no firewood, a cold hearth: %v", err)
	}
	e.clock.Advance(26 * time.Hour)
	e.stock("firewood", 6)
	resp, err := e.sv.Warm(testCtx(t), e.as(p, "health.warm", "warm"))
	v := e.view(resp, err)
	if v.Notice != plife.NoticeWarmed || !v.Sheltered {
		t.Fatalf("the open warming house shelters: %+v", v)
	}
	if e.held("firewood") != 3 {
		t.Errorf("the fire burns three firewood a day: %d left of 6", e.held("firewood"))
	}
	e.verifySurvival()
}
