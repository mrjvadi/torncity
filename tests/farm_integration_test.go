//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The farm cycle, the water works and the mill (ADR 0067): a crop is sown, tended and harvested in shifts; its yield follows
// the soil, the water and the tending; a farm that stood before the rule date keeps its flat shift through the grace; the water
// master of a water work is a daily service and the works wear; a citizen grinds his own grain for the miller's toll.

type farmEnv struct {
	*researchEnv
	rules application.FarmRules
}

func newFarmEnv(t *testing.T) *farmEnv {
	t.Helper()
	r := newResearchEnv(t)
	cfg := config.Defaults()
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	r.village.WithService(handlers.ServiceRules{Clock: clock})
	rules := application.FarmRules{RuleAt: r.clock.Now().Add(-48 * time.Hour), GraceDays: 0}
	r.village.WithFarm(rules)
	e := &farmEnv{researchEnv: r, rules: rules}
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`DELETE FROM service_days WHERE settlement_id = $1::uuid`,
			`DELETE FROM farm_cycles WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_mill_policy WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_private_buildings WHERE settlement_id = $1::uuid`,
		} {
			_, _ = r.pool.Raw().Exec(c, stmt, r.cityID)
		}
	})
	// the village has granaries (a harvest is more than the base room), seed, bread for the hands and wheat for the master's day
	for i := 0; i < 3; i++ {
		e.placeAt("granary", 70+i, 20)
	}
	e.stock("wheat", 150)
	e.stock("bread", 40)
	return e
}

// placeAt puts a finished building of a type on a lot (a farm covers three by three from there).
func (e *farmEnv) placeAt(code string, x, y int) string {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'complete', now(), now())`, id, e.cityID, code, x, y); err != nil {
		e.t.Fatal(err)
	}
	return id
}

type cycleRow struct {
	sow, tended, harvest  int
	yield, seed, waterSum int64
	waterN, soil          int
	result                string
	closed                bool
	found                 bool
}

func (e *farmEnv) cycle(building string) cycleRow {
	e.t.Helper()
	var c cycleRow
	rows, err := e.pool.Raw().Query(testCtx(e.t), `SELECT sow_started, tended, harvest_started, yield_total, seed_spent, water_sum, water_n, soil_bps, result, closed_at IS NOT NULL
		FROM farm_cycles WHERE building_id = $1::uuid ORDER BY ordered_at DESC, id DESC LIMIT 1`, building)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		c.found = true
		if err := rows.Scan(&c.sow, &c.tended, &c.harvest, &c.yield, &c.seed, &c.waterSum, &c.waterN, &c.soil, &c.result, &c.closed); err != nil {
			e.t.Fatal(err)
		}
	}
	return c
}

// order sows the farm as the head; it returns the refusal kind, "" when the order was taken.
func (e *farmEnv) order(building string) string {
	e.t.Helper()
	resp, err := rrc(e.village.FarmSow(testCtx(e.t), e.as(e.head, "settlement.farm.sow", "farm.sow"), handlers.VillageFarmRequest{ID: building}))
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.Screen == screens.ScreenVillageRefusal {
		return refusalKind(e.t, resp.View)
	}
	return ""
}

// shift works one shift at the farm as a resident and lets it end; it returns the refusal kind, "" when the shift was done.
func (e *farmEnv) shift(who *application.Player, building string) string {
	e.t.Helper()
	resp, err := rrc(e.village.Work(testCtx(e.t), e.as(who, "settlement.work", "work"), handlers.VillageWorkRequest{ID: building}))
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

func (e *farmEnv) verify() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	if !v.VillageInvariants.WorkNodesOK() {
		e.t.Errorf("the working-node invariants broke: %+v", v.VillageInvariants)
	}
	if i := v.VillageInvariants; i.FarmSeedShifts != i.FarmSeedCycles || i.FarmShiftsWithoutCycle != 0 || i.FarmCountersBroken != 0 || i.FarmHarvestOver != 0 {
		e.t.Errorf("the farm invariants broke: seed %d/%d, without cycle %d, counters %d, over %d", i.FarmSeedShifts, i.FarmSeedCycles,
			i.FarmShiftsWithoutCycle, i.FarmCountersBroken, i.FarmHarvestOver)
	}
}

// yard gives a citizen a workplace of his own, whose yard is his home store.
func (e *farmEnv) yard(owner *application.Player) {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'carpentry_workshop_own', $3, 12, 'complete', now(), now())`, id, e.cityID, 20+nextYard); err != nil {
		e.t.Fatal(err)
	}
	nextYard++
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, 2000, now())`, id, e.cityID, owner.ID); err != nil {
		e.t.Fatal(err)
	}
}

var nextYard = 0

// give puts goods in a citizen's home store.
func (e *farmEnv) give(p *application.Player, item string, qty int64) {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, To: p.ID, ToHolding: application.HoldHome,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(e.t), At: time.Now().UTC()})
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *farmEnv) homeHeld(p *application.Player, item string) int64 {
	e.t.Helper()
	return e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_stacks WHERE player_id = $1::uuid AND holding = 'home' AND item_code = $2`, p.ID, item)
}

// farmLine reads the crop as the work screen shows it.
func (e *farmEnv) farmLine(who *application.Player, building string) *village.FarmLine {
	e.t.Helper()
	resp, err := rrc(e.village.Work(testCtx(e.t), e.as(who, "settlement.work", "work"), handlers.VillageWorkRequest{}))
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.WorkView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		e.t.Fatal(err)
	}
	for _, p := range v.Places {
		if p.ID == building {
			return p.Farm
		}
	}
	return nil
}

// A farm with its water work: nothing works until the sowing is ordered; the sowing takes the seed once; tending is capped and
// then the field waits; the harvest comes in shares that add up to the yield, from the soil, the water and the tending.
func TestAFarmIsSownTendedAndHarvested(t *testing.T) {
	e := newFarmEnv(t)
	hand := e.resident()
	canal := e.placeAt("canal_channel", 44, 0)
	farm := e.placeAt("farm_canal", 40, 0)
	_ = canal

	if got := e.shift(hand, farm); got != "farm_idle" {
		t.Fatalf("nothing is sown: the shift is refused as farm_idle, not %q", got)
	}
	if got := e.order(farm); got != "" {
		t.Fatalf("the head orders the sowing: %q", got)
	}
	if got := e.order(farm); got != "farm_busy" {
		t.Fatalf("a second order over a crop in the ground is refused as farm_busy, not %q", got)
	}
	wheat0 := e.held("wheat")
	for i := 0; i < 8; i++ {
		if got := e.shift(hand, farm); got != "" {
			t.Fatalf("sowing shift %d: refused as %q", i+1, got)
		}
		if i == 0 && wheat0-e.held("wheat") < 50 {
			t.Fatalf("the first sowing shift takes the seed (50 wheat): %d taken", wheat0-e.held("wheat"))
		}
	}
	c := e.cycle(farm)
	if c.sow != 8 || c.seed != 50 || c.tended != 0 {
		t.Fatalf("the crop after the sowing: %+v", c)
	}
	// growing: the farmer weeds, at most six times, then there is nothing to do
	for i := 0; i < 6; i++ {
		if got := e.shift(hand, farm); got != "" {
			t.Fatalf("tending shift %d: refused as %q", i+1, got)
		}
	}
	if got := e.shift(hand, farm); got != "farm_waiting" {
		t.Fatalf("the weeding is done and the crop is not ripe: farm_waiting, not %q", got)
	}
	if fl := e.farmLine(hand, farm); fl == nil || fl.Stage != "growing" || fl.Tended != 6 {
		t.Fatalf("the farm line says growing, 6 tended: %+v", fl)
	}
	c = e.cycle(farm)
	if c.waterN != 14 || c.waterSum != 14*10000 {
		t.Errorf("a canal farm with its canal and a master on duty draws full water at every sample: %d over %d", c.waterSum, c.waterN)
	}
	// ripe: the harvest, twelve shifts
	e.clock.Advance(7 * time.Hour)
	if fl := e.farmLine(hand, farm); fl == nil || fl.Stage != "ripe" || fl.Expected <= 0 {
		t.Fatalf("the farm line says ripe with an expected harvest: %+v", fl)
	}
	before := e.held("wheat")
	for i := 0; i < 12; i++ {
		if got := e.shift(hand, farm); got != "" {
			t.Fatalf("harvest shift %d: refused as %q", i+1, got)
		}
	}
	c = e.cycle(farm)
	if c.harvest != 12 || c.yield <= 0 {
		t.Fatalf("the crop after the harvest: %+v", c)
	}
	// 400 x soil x 1.0 water x 1.12 tending
	if want := int64(400) * int64(c.soil) / 10000 * 112 / 100; c.yield < want-2 || c.yield > want+2 {
		t.Errorf("the yield is base x soil x water x tending: %d, want about %d (soil %d)", c.yield, want, c.soil)
	}
	got := e.held("wheat") - before
	if got <= 0 || got > c.yield {
		t.Errorf("the harvest shifts bring the yield in (the hands' skill takes its share): %d of %d", got, c.yield)
	}
	if got := e.shift(hand, farm); got != "farm_idle" {
		t.Errorf("a harvested field is idle until it is sown again: %q", got)
	}
	// the next crop can be ordered, and closes this one
	if got := e.order(farm); got != "" {
		t.Errorf("a harvested field is sown again: %q", got)
	}
	if e.scalar(`SELECT count(*) FROM farm_cycles WHERE building_id = $1::uuid AND closed_at IS NOT NULL AND result = 'harvested'`, farm) != 1 {
		t.Error("the first crop is closed as harvested")
	}
	e.verify()
}

// Without a water work the farm draws 60 percent; a rain-fed farm needs none and sows half the seed.
func TestAFarmWithoutWaterYieldsLessAndARainFedOneNeedsNone(t *testing.T) {
	e := newFarmEnv(t)
	hand := e.resident()
	dry := e.placeAt("farm_dry", 40, 0)
	thirsty := e.placeAt("farm_canal", 60, 0)
	for _, id := range []string{dry, thirsty} {
		if got := e.order(id); got != "" {
			t.Fatalf("the sowing is ordered: %q", got)
		}
		if got := e.shift(hand, id); got != "" {
			t.Fatalf("the first sowing shift: %q", got)
		}
	}
	d, w := e.cycle(dry), e.cycle(thirsty)
	if d.seed != 25 || d.waterSum != 10000 || d.waterN != 1 {
		t.Errorf("a rain-fed farm sows 25 and draws its own full water: %+v", d)
	}
	if w.seed != 50 || w.waterSum != 6000 {
		t.Errorf("a canal farm with no canal sows 50 and draws 60 percent: %+v", w)
	}
	if fl := e.farmLine(hand, thirsty); fl == nil || fl.Water == nil || fl.Water.Reason != "no_work" {
		t.Errorf("the farm line says why the water is short: %+v", fl)
	}
	e.verify()
}

// A crop nobody harvests spoils past its window and rots; the field can be sown again.
func TestACropNobodyHarvestsRots(t *testing.T) {
	e := newFarmEnv(t)
	hand := e.resident()
	farm := e.placeAt("farm_dry", 40, 0)
	if got := e.order(farm); got != "" {
		t.Fatal(got)
	}
	for i := 0; i < 8; i++ {
		if got := e.shift(hand, farm); got != "" {
			t.Fatalf("sowing shift %d: %q", i+1, got)
		}
	}
	e.clock.Advance(6*time.Hour + 12*time.Hour + 31*time.Hour) // grown, past its window, past ten steps of spoiling
	if fl := e.farmLine(hand, farm); fl == nil || fl.Stage != "rotted" {
		t.Fatalf("the crop has rotted: %+v", fl)
	}
	if got := e.shift(hand, farm); got != "farm_idle" {
		t.Errorf("a rotted field takes no shift: %q", got)
	}
	if got := e.order(farm); got != "" {
		t.Errorf("a rotted field is sown again: %q", got)
	}
	if e.scalar(`SELECT count(*) FROM farm_cycles WHERE building_id = $1::uuid AND result = 'rotted'`, farm) != 1 {
		t.Error("the spoiled crop is closed as rotted")
	}
	e.verify()
}

// A farm that stood before the rule date keeps its flat shift through the grace, and is sown from then on.
func TestAFarmThatStoodBeforeTheRuleKeepsItsFlatShiftThroughTheGrace(t *testing.T) {
	e := newFarmEnv(t)
	hand := e.resident()
	rules := application.FarmRules{RuleAt: e.clock.Now().Add(-24 * time.Hour), GraceDays: 14}
	e.village.WithFarm(rules)
	farm := newUUID(t)
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'farm_canal', 40, 0, 'complete', now() - interval '10 days', now() - interval '10 days')`, farm, e.cityID); err != nil {
		t.Fatal(err)
	}
	wheat0 := e.held("wheat")
	if got := e.shift(hand, farm); got != "" {
		t.Fatalf("in the grace the farm works its flat shift: %q", got)
	}
	if e.held("wheat") <= wheat0 {
		t.Error("the flat shift brings wheat into the store")
	}
	if e.cycle(farm).found {
		t.Error("a farm in its grace has no crop")
	}
	if fl := e.farmLine(hand, farm); fl == nil || !fl.Legacy || fl.LegacyUntil == nil {
		t.Errorf("the work screen says the farm keeps its flat shift until a date: %+v", fl)
	}
	e.clock.Advance(15 * 24 * time.Hour)
	if got := e.shift(hand, farm); got != "farm_idle" {
		t.Errorf("after the grace the farm needs a sowing: %q", got)
	}
	e.verify()
}

// The water works are daily services: a master is on duty, the work silts up and is repaired by labour; the settlement can
// post the repair; a farm draws less water from a worn work.
func TestAWaterWorkHasAMasterWearsAndIsRepaired(t *testing.T) {
	e := newFarmEnv(t)
	ctx := testCtx(t)
	canal := e.placeAt("canal_channel", 44, 0)
	farm := e.placeAt("farm_canal", 40, 0)
	hand := e.resident()
	line := func() *village.WaterWork {
		t.Helper()
		resp, err := rrc(e.village.BuildingView(ctx, e.as(e.head, "settlement.building.view", "building.view"), handlers.VillageBuildingViewRequest{BuildingID: canal}))
		if err != nil {
			t.Fatal(err)
		}
		var v village.BuildingView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		if v.Work == nil {
			t.Fatal("the canal has a work block")
		}
		return v.Work.Water
	}
	w := line()
	if w == nil || !w.Open || w.ConditionBPS != 10000 || len(w.Serves) != 1 {
		t.Fatalf("a new canal has its master on duty and serves the farm: %+v", w)
	}
	// 40 days of silt (200 a day): condition 20 percent, under the half the water needs
	e.clock.Advance(40 * 24 * time.Hour)
	if w = line(); w.ConditionBPS > 3000 {
		t.Fatalf("the canal has silted up: %+v", w)
	}
	if got := e.order(farm); got != "" {
		t.Fatal(got)
	}
	if got := e.shift(hand, farm); got != "" {
		t.Fatalf("the first sowing shift: %q", got)
	}
	if c := e.cycle(farm); c.waterSum != 6000 {
		t.Errorf("a silted canal gives the farm 60 percent: %d", c.waterSum)
	}
	if fl := e.farmLine(hand, farm); fl == nil || fl.Water == nil || fl.Water.Reason != "worn" {
		t.Errorf("the farm line says the work is worn: %+v", fl)
	}
	// the head posts the repair: the labourers restore it
	if _, err := rrc(e.village.LaborPost(ctx, e.as(e.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: canal, N: "repair"})); err != nil {
		t.Fatal(err)
	}
	if e.scalar(`SELECT count(*) FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'repair' AND status = 'open'`, canal) != 1 {
		t.Fatal("a repair job is open on the canal")
	}
	e.verify()
}

// A citizen grinds his own wheat at the village mill and pays the statute's toll in kind into the village's stock; the head sets
// the toll inside the range; the toll is carried exactly over many batches.
func TestACitizenGrindsHisOwnWheatForTheTollOfTheStatute(t *testing.T) {
	e := newFarmEnv(t)
	ctx := testCtx(t)
	mill := e.placeAt("mill", 40, 0)
	citizen := e.resident()
	e.yard(citizen)
	e.give(citizen, "wheat", 40)
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, citizen.ID) })

	// the statute: out of range refused, in range taken
	if resp, err := rrc(e.village.MillToll(ctx, e.as(e.head, "settlement.mill.toll", "mill.toll"), handlers.VillageMillRequest{BPS: "2000"})); err != nil || resp.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a toll of a fifth is out of the statute: %v %q", err, resp.Screen)
	}
	if resp, err := rrc(e.village.MillToll(ctx, e.as(e.head, "settlement.mill.toll", "mill.toll"), handlers.VillageMillRequest{BPS: "1000"})); err != nil || resp.Screen == screens.ScreenVillageRefusal {
		t.Fatalf("a toll of a tenth is the top of the statute: %v", err)
	}
	if resp, err := rrc(e.village.MillToll(ctx, e.as(citizen, "settlement.mill.toll", "mill.toll"), handlers.VillageMillRequest{BPS: "500"})); err != nil || resp.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a citizen without the permission cannot set the toll: %v %q", err, resp.Screen)
	}
	flour0, wheat0 := e.held("flour_sack"), e.held("wheat")
	var ground int64
	for i := 0; i < 5; i++ {
		if resp, err := rrc(e.village.MillGrind(ctx, e.as(citizen, "settlement.mill.grind", "mill.grind"), handlers.VillageMillRequest{ID: mill})); err != nil || resp.Screen == screens.ScreenVillageRefusal {
			t.Fatalf("grinding batch %d: %v %s", i+1, err, resp.Screen)
		}
		e.clock.Advance(time.Hour)
		for _, s := range e.workingShifts(mill) {
			e.end(s)
		}
		ground++
	}
	if got := e.homeHeld(citizen, "wheat"); got != 0 {
		t.Errorf("five batches of 8 took all 40 wheat out of his store: %d left", got)
	}
	home := e.homeHeld(citizen, "flour_sack")
	toll := e.held("flour_sack") - flour0
	if home <= 0 || toll <= 0 {
		t.Fatalf("flour comes home and the toll goes to the village: home %d, toll %d", home, toll)
	}
	made := e.scalar(`SELECT COALESCE(SUM((produced->>'flour_sack')::bigint), 0)::bigint FROM settlement_shifts WHERE farm_phase = 'grind'`)
	if home+toll != made {
		t.Errorf("the flour made is the flour that went home and the toll: %d + %d != %d", home, toll, made)
	}
	// a tenth of what was made (the fraction is carried by the mill, so the sum is exact)
	if want := made / 10; toll < want-1 || toll > want+1 {
		t.Errorf("the toll is a tenth of the flour, %d of %d (wanted about %d)", toll, made, want)
	}
	if e.held("wheat") != wheat0 {
		t.Errorf("his own grain is not the village's: the village's wheat changed %d -> %d", wheat0, e.held("wheat"))
	}
	e.verify()
}

// The pasture grazes open land: with trees and rocks round it the herd cannot be worked; once the land is cleared it can.
func TestAPastureNeedsOpenLandToGraze(t *testing.T) {
	landBoost = true
	t.Cleanup(func() { landBoost = false })
	e := newFarmEnv(t)
	hand := e.resident()
	rules := application.LandRules{RuleAt: time.Now().UTC().Add(-48 * time.Hour), Ring: 3, RegrowEvery: 120 * time.Hour,
		SaplingFor: 24 * time.Hour, FellRadius: 4, QuarryRadius: 3, RockShifts: 2}
	e.village.WithLand(rules)
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_land WHERE settlement_id = $1::uuid`, e.cityID)
	})
	// a pasture on the commons ring, where the boosted world has trees on every lot
	pasture := e.placeAt("pasture_range", 8, 0)
	if got := e.shift(hand, pasture); got != "no_grazing" {
		t.Fatalf("a pasture ringed by trees cannot graze: %q", got)
	}
	// cut every tree and rock round it
	for y := -4; y <= 8; y++ {
		for x := 4; x <= 14; x++ {
			if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_land (settlement_id, lot_x, lot_y, trees_cut, rocks_cut, regrow_anchor, updated_at) VALUES ($1::uuid, $2, $3, 99, 99, now(), now())
				ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET trees_cut = 99, rocks_cut = 99`, e.cityID, x, y); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := e.shift(hand, pasture); got != "" {
		t.Errorf("a pasture with open land round it works: %q", got)
	}
}

var _ = gametime.Scale(1)

// A citizen's own field (the farm's citizen twin): the owner sows it from his own store of seed, works it for no wage, a
// stranger may not, and the harvest comes into his home store.
func TestAPlayerFarmsHisOwnField(t *testing.T) {
	e := newFarmEnv(t)
	ctx := testCtx(t)
	owner := e.resident()
	stranger := e.resident()
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, owner.ID) })
	for i := 0; i < 5; i++ {
		e.yard(owner)
	}
	field := newUUID(t)
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'farm_dry_own', 40, 30, 'complete', now(), now())`, field, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, 2000, now())`, field, e.cityID, owner.ID); err != nil {
		t.Fatal(err)
	}
	e.give(owner, "wheat", 40)
	e.give(owner, "bread", 12)
	// the head has no say over a citizen's field; a stranger cannot work it
	if got := e.order(field); got == "" {
		t.Fatal("the head cannot sow a citizen's field")
	}
	if resp, err := rrc(e.village.FarmSow(ctx, e.as(owner, "settlement.farm.sow", "farm.sow"), handlers.VillageFarmRequest{ID: field})); err != nil || resp.Screen == screens.ScreenVillageRefusal {
		t.Fatalf("the owner sows his field: %v", err)
	}
	if got := e.shift(stranger, field); got == "" {
		t.Fatal("a stranger may not work a citizen's field")
	}
	cash0 := cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID)
	for i := 0; i < 8; i++ {
		if got := e.shift(owner, field); got != "" {
			t.Fatalf("sowing shift %d of the owner: %q", i+1, got)
		}
	}
	if got := e.homeHeld(owner, "wheat"); got != 15 {
		t.Errorf("the seed (25) left his own store: %d wheat left of 40", got)
	}
	if c := e.cycle(field); c.seed != 25 || c.sow != 8 {
		t.Errorf("the private crop: %+v", c)
	}
	e.clock.Advance(7 * time.Hour)
	for i := 0; i < 12; i++ {
		if got := e.shift(owner, field); got != "" {
			t.Fatalf("harvest shift %d of the owner: %q", i+1, got)
		}
	}
	c := e.cycle(field)
	if got := e.homeHeld(owner, "wheat") - 15; got <= 0 || got > c.yield {
		t.Errorf("the harvest comes into his own store, %d of %d", got, c.yield)
	}
	if cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID) != cash0 {
		t.Error("the owner works his own field for no wage and pays none")
	}
	e.verify()
}

// The water mill grinds three times what the hand mill does in a shift.
func TestAWaterMillGrindsThreeTimesTheHandMill(t *testing.T) {
	e := newFarmEnv(t)
	hand := e.resident()
	hands := e.placeAt("mill", 40, 0)
	water := e.placeAt("water_mill", 50, 0)
	w0 := e.held("wheat")
	if got := e.shift(hand, hands); got != "" {
		t.Fatalf("the hand mill: %q", got)
	}
	handWheat := w0 - e.held("wheat")
	w1 := e.held("wheat")
	if got := e.shift(hand, water); got != "" {
		t.Fatalf("the water mill: %q", got)
	}
	waterWheat := w1 - e.held("wheat")
	if handWheat != 8 || waterWheat != 24 {
		t.Errorf("a hand mill takes 8 wheat a shift and a water mill 24: %d and %d", handWheat, waterWheat)
	}
	e.verify()
}

// A crew of the village's labourers sows the field, tends it and waits for the crop: the job pauses with the reason.
func TestACrewSowsTheFieldAndWaitsForTheCrop(t *testing.T) {
	e := newFarmEnv(t)
	ctx := testCtx(t)
	farm := e.placeAt("farm_dry", 40, 0)
	if got := e.order(farm); got != "" {
		t.Fatal(got)
	}
	if _, err := rrc(e.village.LaborPost(ctx, e.as(e.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: farm})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'production' AND status = 'open'`, farm).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(e.village.LaborHire(ctx, e.as(e.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "2"})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200 && e.cycle(farm).tended < 6; i++ {
		e.clock.Advance(time.Hour)
		for _, s := range e.workingShifts(farm) {
			e.end(s)
		}
	}
	c := e.cycle(farm)
	if c.sow != 8 || c.tended != 6 {
		t.Fatalf("the crew sowed (8) and tended (6): %+v", c)
	}
	e.clock.Advance(time.Hour)
	for _, s := range e.workingShifts(farm) {
		e.end(s)
	}
	var paused string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT COALESCE(paused, '') FROM labor_jobs WHERE id = $1::uuid`, jobID).Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if paused != "crop_growing" {
		t.Errorf("the crew waits for the crop and says so: %q", paused)
	}
	e.verify()
}
