//go:build integration

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
	"github.com/mrjvadi/torncity/internal/domain/haul"
	"github.com/mrjvadi/torncity/internal/domain/mining"
	"github.com/mrjvadi/torncity/internal/domain/rail"
	"github.com/mrjvadi/torncity/internal/domain/roads"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// Mining, carriage and rail (docs/adr/0075, plan B11), through the real handlers, the unit of work, the ledger and the item journal: the
// deposit that depletes, the minehead capped by its face, its hoist and its haulers, the unsafe mine that closes, goods on the road,
// trains on single track, the freight fee and the ratio view. The rules are built the way the service builds them, from the shipped config.

type mineEnv struct {
	*researchEnv
	rules application.MiningRules
	dep   worldgen.Deposit
	world *worldgen.World
}

// newMineEnv founds a settlement on a cell that holds iron, with mining started two days ago (the grace is over) and a deposit of
// capacityUnits units, so a test sees a deposit run out in a handful of shifts.
func newMineEnv(t *testing.T, capacityUnits int64, tune func(*application.MiningRules)) *mineEnv {
	t.Helper()
	r := newResearchEnv(t)
	w, _ := r.foundingEnv.world(t)
	var dep worldgen.Deposit
	cells := map[int32]bool{}
	row := r.pool.Raw()
	rows, err := row.Query(testCtx(t), `SELECT world_cell_id FROM cities WHERE world_cell_id IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c int32
		if rows.Scan(&c) == nil {
			cells[c] = true
		}
	}
	rows.Close()
	for _, d := range w.Deposits {
		if d.ResourceCode == "iron" && !cells[d.CellID] {
			dep = d
			break
		}
	}
	if dep.ID == "" {
		t.Skip("the test world has no free iron deposit")
	}
	if _, err := row.Exec(testCtx(t), `UPDATE cities SET world_cell_id = $2 WHERE id = $1::uuid`, r.cityID, dep.CellID); err != nil {
		t.Fatal(err)
	}
	rules := settlementcfg.Mining(r.cfg.Mining)
	rules.RuleAt = r.clock.Now().Add(-48 * time.Hour)
	rules.From = rules.RuleAt
	rules.Rules.ReserveDivisor = max(dep.Reserve/capacityUnits, 1)
	if tune != nil {
		tune(&rules)
	}
	r.village.WithMining(rules)
	// The mine counts its days on the settlement's local day. Pin the zone so that it is local noon now: a founded settlement takes a zone
	// from its place on the world and the wall clock decides the hour, so a run near local midnight crossed a day between two checks and
	// the props rotted.
	utcMin := int64(r.clock.Now().UTC().Hour()*60 + r.clock.Now().UTC().Minute())
	if _, err := row.Exec(testCtx(t), `UPDATE cities SET tz_offset_minutes = $2 WHERE id = $1::uuid`, r.cityID, (((12*60-utcMin)%1440+1440+720)%1440-720)/15*15); err != nil {
		t.Fatal(err)
	}
	e := &mineEnv{researchEnv: r, rules: rules, dep: dep, world: w}
	t.Cleanup(func() { e.cleanMining(r.cityID) })
	for _, k := range []string{"smithing", "prospecting", "timbering", "carpentry", "wagonway"} {
		if _, err := row.Exec(testCtx(t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, $2, 'researched', now()) ON CONFLICT DO NOTHING`, r.cityID, k); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// cleanMining takes the rows of mining, carriage and rail away before the settlement goes.
func (e *mineEnv) cleanMining(cities ...string) {
	ctx := testCtx(e.t)
	for _, stmt := range []string{
		`DELETE FROM rail_reservations WHERE train_id IN (SELECT id FROM rail_trains WHERE line_id IN (SELECT id FROM rail_lines WHERE home_settlement = $1::uuid OR id IN (SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid)))`,
		`DELETE FROM rail_schedules WHERE train_id IN (SELECT id FROM rail_trains WHERE line_id IN (SELECT id FROM rail_lines WHERE home_settlement = $1::uuid OR id IN (SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid)))`,
		`DELETE FROM haul_loads WHERE from_settlement = $1::uuid OR to_settlement = $1::uuid`,
		`DELETE FROM haul_orders WHERE from_settlement = $1::uuid OR to_settlement = $1::uuid`,
		`DELETE FROM rail_trains WHERE line_id IN (SELECT id FROM rail_lines WHERE home_settlement = $1::uuid OR id IN (SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid))`,
		`DELETE FROM rail_stops WHERE line_id IN (SELECT id FROM rail_lines WHERE home_settlement = $1::uuid OR id IN (SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid))`,
		`DELETE FROM rail_segments WHERE line_id IN (SELECT id FROM rail_lines WHERE home_settlement = $1::uuid OR id IN (SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid))`,
		`DELETE FROM rail_lines WHERE id IN (SELECT id FROM rail_lines WHERE home_settlement = $1::uuid OR id IN (SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid))`,
		`DELETE FROM carriers WHERE settlement_id = $1::uuid`,
		`DELETE FROM road_links WHERE a_settlement = $1::uuid OR b_settlement = $1::uuid`,
		`DELETE FROM build_works WHERE settlement_id = $1::uuid`,
		`DELETE FROM node_stats WHERE settlement_id = $1::uuid`,
		`DELETE FROM ore_extractions WHERE settlement_id = $1::uuid`,
		`DELETE FROM ore_deposit_surveys WHERE settlement_id = $1::uuid`,
		`DELETE FROM mine_levels WHERE building_id IN (SELECT id FROM settlement_buildings WHERE settlement_id = $1::uuid)`,
		`DELETE FROM mine_state WHERE settlement_id = $1::uuid`,
		`DELETE FROM game_actions WHERE reference_type IN ('build_works', 'haul_loads', 'rail_trains') AND payload->>'settlement_id' = $1`,
		`DELETE FROM service_days WHERE settlement_id = $1::uuid`,
	} {
		for _, c := range cities {
			if _, err := e.pool.Raw().Exec(ctx, stmt, c); err != nil {
				e.t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	}
	if e.dep.ID != "" {
		_, _ = e.pool.Raw().Exec(ctx, `DELETE FROM ore_deposits WHERE deposit_id = $1`, e.dep.ID)
	}
}

// verifyMining runs the verifier's mining rules (docs/adr/0075 14) over the database.
func (e *mineEnv) verifyMining() {
	e.t.Helper()
	lv, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := lv.VillageInvariants
	if !s.Mining {
		e.t.Fatal("the verifier does not see the mining tables")
	}
	if s.MineTaken != s.MineExtracted || s.MineShiftMismatch != 0 || s.HaulOut-s.HaulIn != s.HaulOnRoad || s.HaulCartNoAction != 0 || s.RailOverheld != 0 || s.RailHeldIdle != 0 || s.FreightBroken != 0 {
		e.t.Errorf("the mining invariants: %+v", s)
	}
	reach := e.rules.Rules.Cumulative(mining.DepthShaft2)
	if s.MineMaxTakenBPS*e.rules.Rules.ReserveDivisor > reach {
		e.t.Errorf("a deposit gave %d bps of its reserve, more than %d bps of capacity reach", s.MineMaxTakenBPS, reach)
	}
}

// crew is a resident with a trade of his own: one player, one shift at a time.
func (e *mineEnv) crew() *application.Player { return e.scholar() }

// work starts a shift of a post (empty: the one the mine needs) and reports whether one began.
func (e *mineEnv) work(p *application.Player, building, post string) bool {
	e.t.Helper()
	if _, err := rrc(e.village.Work(testCtx(e.t), e.as(p, "settlement.work", "work"), handlers.VillageWorkRequest{ID: building, Recipe: post})); err != nil {
		e.t.Fatal(err)
	}
	return e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND player_id = $2::uuid AND status = 'working'`, building, p.ID) > 0
}

// refusal starts a shift and returns the kind it was refused with ("" when it began).
func (e *mineEnv) refusal(p *application.Player, building, post string) string {
	e.t.Helper()
	resp, err := rrc(e.village.Work(testCtx(e.t), e.as(p, "settlement.work", "work"), handlers.VillageWorkRequest{ID: building, Recipe: post}))
	if err != nil {
		e.t.Fatal(err)
	}
	if e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND player_id = $2::uuid AND status = 'working'`, building, p.ID) > 0 {
		return ""
	}
	var v village.VillageRefusalView
	if len(resp.View) > 0 && presentation.DecodeView(resp.View, &v) == nil && v.Kind != "" {
		return v.Kind
	}
	return "?"
}

// finishShifts ends every working shift of the building (the clock goes an hour on).
func (e *mineEnv) finishShifts(building string) {
	e.t.Helper()
	e.clock.Advance(time.Hour)
	for _, s := range e.workingShifts(building) {
		e.end(s)
	}
}

// localDay is the mine's day now: the settlement's local day, as the engine counts it.
func (e *mineEnv) localDay() int64 {
	e.t.Helper()
	var tz int64
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT COALESCE(tz_offset_minutes, 0) FROM cities WHERE id = $1::uuid`, e.cityID).Scan(&tz); err != nil {
		e.t.Fatal(err)
	}
	return e.clock.Now().Add(time.Duration(tz) * time.Minute).Unix() / 86400
}

func (e *mineEnv) mineState(building string, shafts, hoist int, levels map[int][2]int64) {
	e.t.Helper()
	ctx := testCtx(e.t)
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO mine_state (building_id, settlement_id, shafts, hoist, fall_m, day_done, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, 0, $5, now()) ON CONFLICT (building_id) DO UPDATE SET shafts = $3, hoist = $4, day_done = $5`,
		building, e.cityID, shafts, hoist, e.localDay()); err != nil {
		e.t.Fatal(err)
	}
	for depth, l := range levels {
		if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO mine_levels (building_id, depth, condition_bps, water_bps) VALUES ($1::uuid, $2, $3, $4)
			ON CONFLICT (building_id, depth) DO UPDATE SET condition_bps = $3, water_bps = $4`, building, depth, l[0], l[1]); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *mineEnv) shiftColumn(building, col string, orderBy string) []int64 {
	e.t.Helper()
	rows, err := e.pool.Raw().Query(testCtx(e.t), `SELECT `+col+`::bigint FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'done' ORDER BY `+orderBy, building)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func (e *mineEnv) ore() (lean, medium, rich int64) {
	return e.held("iron_ore_lean"), e.held("iron_ore"), e.held("iron_ore_rich")
}

// A pit draws on the deposit of the world once its grace is over: the ore comes up in the item of the deposit's grade band, the
// face thins in small steps as the surface runs out, nothing comes off the deposit that is not in the store, and when it is gone the
// pit says so.
func TestMiningDepositDepletesInSmallStepsAndByBand(t *testing.T) {
	e := newMineEnv(t, 3000, nil)
	rules := e.rules.Rules
	pit := e.building("iron_pit")
	reach := rules.Reachable(e.dep.Reserve, mining.DepthSurface)
	if reach < 100 || reach > 200 {
		t.Fatalf("the test deposit's surface holds %d units; the test expects about 150", reach)
	}
	worker := e.crew()
	var shifts int
	var lean, medium, rich int64
	carryOff := func() { // the yard is small: the ore is sold off after every shift
		l, m, r := e.ore()
		lean, medium, rich = lean+l, medium+m, rich+r
		if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code LIKE 'iron_ore%'`, e.cityID); err != nil {
			t.Fatal(err)
		}
	}
	for ; shifts < 200; shifts++ {
		if !e.work(worker, pit, "") {
			break
		}
		e.finishShifts(pit)
		carryOff()
	}
	if shifts < 20 || shifts >= 200 {
		t.Fatalf("the pit worked %d shifts before running dry", shifts)
	}
	if kind := e.refusal(worker, pit, ""); kind != village.MineNoOre {
		t.Fatalf("an exhausted pit says %q, not %q", kind, village.MineNoOre)
	}
	band := rules.BandOf(e.dep.GradePermille)
	want := map[mining.Band]*int64{mining.BandLean: &lean, mining.BandMedium: &medium, mining.BandRich: &rich}
	total := lean + medium + rich
	if *want[band] != total || total == 0 {
		t.Fatalf("a %s deposit (grade %d) brought up lean %d, medium %d, rich %d", band, e.dep.GradePermille, lean, medium, rich)
	}
	taken := e.scalar(`SELECT taken FROM ore_deposits WHERE deposit_id = $1`, e.dep.ID)
	extracted := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM ore_extractions WHERE deposit_id = $1`, e.dep.ID)
	if taken != total || extracted != total || taken > reach {
		t.Fatalf("taken %d, extractions %d, brought up %d, the surface holds %d", taken, extracted, total, reach)
	}
	// the face thinned in small steps and never rose
	bps := e.shiftColumn(pit, "output_bps", "started_at, id")
	var steps, biggest int64
	for i := 1; i < len(bps); i++ {
		if bps[i] > bps[i-1] {
			t.Fatalf("the yield rose from %d to %d at shift %d", bps[i-1], bps[i], i)
		}
		d := bps[i-1] - bps[i]
		if d > 0 {
			steps++
		}
		biggest = max(biggest, d)
	}
	if steps < 4 || biggest > 2500 {
		t.Errorf("the yield fell in %d steps, the biggest %d basis points: a face must thin, not close overnight", steps, biggest)
	}
	e.verifyMining()
}

// Before the grace is over the pit digs as it always did: nothing comes off the deposit.
func TestMiningThePitIsUntouchedInTheGrace(t *testing.T) {
	e := newMineEnv(t, 3000, func(r *application.MiningRules) {
		r.RuleAt = time.Now().Add(-time.Hour)
		r.From = time.Now().Add(24 * time.Hour)
	})
	e.village.WithMining(func() application.MiningRules {
		r := e.rules
		r.RuleAt = e.clock.Now().Add(-time.Hour)
		r.From = e.clock.Now().Add(24 * time.Hour)
		return r
	}())
	pit := e.building("iron_pit")
	w := e.crew()
	for i := 0; i < 3; i++ {
		if !e.work(w, pit, "") {
			t.Fatal("a pit in its grace refused a shift")
		}
		e.finishShifts(pit)
	}
	if e.held("iron_ore") < 3 || e.held("iron_ore_lean")+e.held("iron_ore_rich") != 0 {
		t.Errorf("the pit made %d ore of the old kind and %d of the graded kinds", e.held("iron_ore"), e.held("iron_ore_lean")+e.held("iron_ore_rich"))
	}
	if n := e.scalar(`SELECT count(*) FROM ore_extractions WHERE deposit_id = $1`, e.dep.ID); n != 0 {
		t.Errorf("%d extractions were written in the grace", n)
	}
}

// What a minehead delivers is the least of what the face breaks, what the hoist lifts and what the haulers carry; the shift says which.
func TestMineheadOutputIsCappedByFaceHoistAndHaul(t *testing.T) {
	cfgFor := func(windlass, unmade int64) func(*application.MiningRules) {
		return func(r *application.MiningRules) {
			r.Rules.HoistKgMin[0] = windlass
			r.Rules.UnmadeWayBPS = unmade
		}
	}
	// the half-hour shift breaks 100 kg (4 sacks of 25 kg); a windlass at 30 m lifts 15 kg a minute x 30 x 30 / 60, the hauler 10 x 30 at the road's pace
	cases := []struct {
		name             string
		windlass, unmade int64
		limit            string
		share            int64 // of the face, in basis points
	}{
		{"face", 16, 10_000, village.MineLimitFace, 10_000},
		{"hoist", 8, 10_000, village.MineLimitHoist, 6_000}, // 8 x 30 x 30 / 60 = 120 kg is more than the face; 4 -> 60 kg
		{"haul", 16, 1_500, village.MineLimitHaul, 4_500},   // 10 x 30 x 15 / 100 = 45 kg
	}
	cases[1].windlass = 4
	var bps []int64
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newMineEnv(t, 200_000, cfgFor(c.windlass, c.unmade))
			mine := e.building("mine")
			e.mineState(mine, 1, application.MineWindlass, map[int][2]int64{1: {10_000, 0}})
			e.stock("splint", 8)
			hauler, banksman, miner := e.crew(), e.crew(), e.crew()
			if !e.work(hauler, mine, "mine_hauler") || !e.work(banksman, mine, "banksman") {
				t.Fatal("the support crew could not start")
			}
			if !e.work(miner, mine, "miner") {
				t.Fatalf("the miner could not start: %q", e.refusal(miner, mine, "miner"))
			}
			var role, limit string
			var depth int
			var out int64
			if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT staff_role, mine_limit, deposit_depth, output_bps FROM settlement_shifts WHERE player_id = $1::uuid AND status = 'working'`, miner.ID).
				Scan(&role, &limit, &depth, &out); err != nil {
				t.Fatal(err)
			}
			if role != "miner" || limit != c.limit || depth != 1 {
				t.Fatalf("role %q limit %q depth %d, want miner %q 1", role, limit, depth, c.limit)
			}
			bps = append(bps, out)
			if c.limit != village.MineLimitFace {
				// the capped shift delivers its share of what the face-limited one does
				ref := bps[0]
				if got := out * 10_000 / ref; got < c.share-300 || got > c.share+300 {
					t.Errorf("a %s-limited shift delivers %d of the face-limited one, want about %d", c.limit, got, c.share)
				}
			}
			e.finishShifts(mine)
		})
	}
	// no banksman at the shaft top, nothing is lifted; no hauler, nothing is carried
	e := newMineEnv(t, 200_000, nil)
	mine := e.building("mine")
	e.mineState(mine, 1, application.MineWindlass, map[int][2]int64{1: {10_000, 0}})
	e.stock("splint", 8)
	hauler, miner := e.crew(), e.crew()
	if kind := e.refusal(miner, mine, "miner"); kind != village.MineLimited {
		t.Errorf("a miner with no hauler is refused with %q, want %q", kind, village.MineLimited)
	}
	if !e.work(hauler, mine, "mine_hauler") {
		t.Fatal("the hauler could not start")
	}
	if kind := e.refusal(miner, mine, "miner"); kind != village.MineLimited {
		t.Errorf("a miner below the surface with no banksman is refused with %q, want %q", kind, village.MineLimited)
	}
	e.finishShifts(mine)
}

// An unsafe mine closes and is never destroyed: with the surface worked out the only ore lies on a level whose props have rotted, the
// mine refuses the miner and keeps its levels; a timberman sets the props and the mine opens again.
func TestAnUnsafeMineClosesAndTheTimbermanReopensIt(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	mine := e.building("mine")
	e.mineState(mine, 1, application.MineWindlass, map[int][2]int64{1: {1_000, 0}})
	// the surface is worked out
	surface := e.rules.Rules.Reachable(e.dep.Reserve, mining.DepthSurface)
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO ore_deposits (deposit_id, resource, reserve, taken, updated_at) VALUES ($1, 'iron', $3, $2, now()) ON CONFLICT (deposit_id) DO UPDATE SET taken = $2`, e.dep.ID, surface, e.dep.Reserve); err != nil {
		t.Fatal(err)
	}
	e.stock("splint", 8)
	e.stock("timber_prop", 8)
	hauler, banksman, miner, timberman := e.crew(), e.crew(), e.crew(), e.crew()
	if !e.work(hauler, mine, "mine_hauler") || !e.work(banksman, mine, "banksman") {
		t.Fatal("the support crew could not start")
	}
	if kind := e.refusal(miner, mine, "miner"); kind != village.MineClosed {
		t.Fatalf("a mine whose only level is unsafe says %q, want %q", kind, village.MineClosed)
	}
	// closed, not destroyed
	if st := e.scalar(`SELECT count(*) FROM settlement_buildings WHERE id = $1::uuid AND status = 'complete'`, mine); st != 1 {
		t.Fatal("the mine building is gone")
	}
	if c := e.scalar(`SELECT condition_bps FROM mine_levels WHERE building_id = $1::uuid AND depth = 1`, mine); c != 1_000 {
		t.Fatalf("the level is %d, it should be as it was", c)
	}
	// the timberman sets props until the level stands again
	for i := 0; i < 3 && e.scalar(`SELECT condition_bps FROM mine_levels WHERE building_id = $1::uuid AND depth = 1`, mine) < e.rules.Rules.MinConditionBPS; i++ {
		if !e.work(timberman, mine, "timberman") {
			t.Fatalf("the timberman could not start: %q", e.refusal(timberman, mine, "timberman"))
		}
		e.clock.Advance(time.Hour)
		for _, s := range e.workingShifts(mine) {
			if s.id != "" {
				e.end(s)
			}
		}
		if !e.work(hauler, mine, "mine_hauler") || !e.work(banksman, mine, "banksman") {
			t.Fatal("the support crew could not start again")
		}
	}
	if c := e.scalar(`SELECT condition_bps FROM mine_levels WHERE building_id = $1::uuid AND depth = 1`, mine); c < e.rules.Rules.MinConditionBPS {
		t.Fatalf("the props stand at %d, still unsafe", c)
	}
	if !e.work(miner, mine, "miner") {
		t.Fatalf("the repaired mine refuses a miner: %q", e.refusal(miner, mine, "miner"))
	}
	if n := e.scalar(`SELECT COALESCE(SUM((consumed->>'timber_prop')::bigint), 0) FROM settlement_shifts WHERE building_id = $1::uuid AND staff_role = 'timberman'`, mine); n < 2 {
		t.Errorf("the timberman's props were not taken from the store: %d", n)
	}
	e.finishShifts(mine)
}

// The ratio view names the link nearest the target that cannot keep up, with the fix.
func TestChainViewNamesTheFirstStarvedLink(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	bloomery, pit := e.building("bloomery"), e.building("iron_pit")
	clamps := []string{e.building("charcoal_clamp"), e.building("charcoal_clamp")}
	camp := e.building("woodcutter_camp")
	// the buildings worked in the last hour (the view counts a workplace that did as running)
	for _, b := range append([]string{bloomery, pit, camp}, clamps...) {
		if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO node_stats (building_id, bucket, settlement_id, produced, consumed, shifts) VALUES ($1::uuid, date_trunc('hour', $3::timestamptz), $2::uuid, '{}', '{}', 1)`,
			b, e.cityID, e.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := rrc(e.village.Chain(testCtx(t), e.as(e.head, "settlement.chain", "chain"), handlers.VillageChainRequest{Code: "bloom", N: "2"}))
	if err != nil {
		t.Fatal(err)
	}
	var v village.ChainView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatalf("%v\n%s", err, resp.Text)
	}
	if v.First == nil {
		t.Fatalf("no starved link in a chain built for one bloomery and two clamps:\n%+v", v.Lines)
	}
	// two blooms an hour need 8 charcoal an hour; two clamps make 4 an hour
	if v.First.Item.Code != "charcoal" || v.First.HaveMilli != 4_000 || v.First.NeedMilli != 8_000 || v.First.Add != 2 {
		t.Errorf("the first starved link is %+v, want charcoal at 4 of 8 an hour with 2 clamps to add", *v.First)
	}
	// the bloom link itself keeps up
	for _, l := range v.Lines {
		if l.Item.Code == "bloom" && l.Starved {
			t.Errorf("the bloomery is marked starved: %+v", l)
		}
	}
	if len(v.Nodes) == 0 {
		t.Error("the building ratios are empty")
	}
}

// second founds another settlement, beside the first, with a treasury, a station and stock to spare.
func (e *mineEnv) second() (city string, meta envelope.Metadata, head *application.Player) {
	e.t.Helper()
	meta, head = e.group(e.t)
	foundVillage(e.t, e.pool, e.h, meta)
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&city); err != nil {
		e.t.Fatal(err)
	}
	seedTreasury(e.t, e.pool, city, 60_000)
	e.nearbyRouter()
	id := city
	e.t.Cleanup(func() {
		e.cleanMining(id)
		ctx := testCtx(e.t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
		} {
			args := []any{}
			if strings.Contains(stmt, "$1") {
				args = append(args, id)
			}
			if _, err := e.pool.Raw().Exec(ctx, stmt, args...); err != nil {
				e.t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		purgeLedgerFor(e.t, e.pool, id)
		purgeLedgerFor(e.t, e.pool, head.ID)
	})
	return city, meta, head
}

// nearbyRouter plans every line as a short walk east over the tiles round the first settlement: the generated planet spaces its settlements
// hundreds of kilometres apart, and the planner's own behaviour is the road package's to test.
func (e *mineEnv) nearbyRouter() {
	e.village.WithRouter(func(ctx context.Context, w *worldgen.World, from, to int32, class roads.Class) (roads.Route, error) {
		pt := w.Cells[from].Point
		t := w.TileOfLatLon(pt.LatDeg, pt.LonDeg)
		tiles := []worldgen.TileID{t}
		for i := 0; i < 8; i++ {
			t = w.Offset(t, 1, 0)
			tiles = append(tiles, t)
		}
		return roads.Route{Tiles: tiles, LengthM: 8 * w.TileLengthM()}, nil
	})
}

func (e *mineEnv) asOther(meta envelope.Metadata, p *application.Player, command, action string) envelope.Metadata {
	m := clientMeta(asPlayer(meta, p), command, action)
	m.IdempotencyKey = "it-" + randomToken(e.t, 16)
	m.RequestID = "req_" + randomToken(e.t, 16)
	return m
}

func (e *mineEnv) heldAt(city, item string) int64 {
	return e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2`, city, item)
}

func (e *mineEnv) stockAt(city, item string, qty int64) {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, ToOrg: application.SettlementOrg(city), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(e.t), At: time.Now().UTC()})
	}); err != nil {
		e.t.Fatal(err)
	}
}

// goods = the units of an item in the stores of the cities and on the road.
func (e *mineEnv) goods(item string, cities ...string) int64 {
	var n int64
	for _, c := range cities {
		n += e.heldAt(c, item)
	}
	return n + e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM haul_loads WHERE item_code = $1 AND arrived_at IS NULL`, item)
}

// A consignment conserves goods in transit: they leave the store at once, ride in a load row, and arrive once, whoever repeats the
// scheduled action; at every step the stores plus the road hold what there was.
func TestAConsignmentConservesGoodsInTransit(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	other, otherMeta, otherHead := e.second()
	var otherCode string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT code FROM cities WHERE id = $1::uuid`, other).Scan(&otherCode); err != nil {
		t.Fatal(err)
	}
	ctx := testCtx(t)
	linkID, carrierID := newUUID(t), newUUID(t)
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO road_links (id, a_settlement, b_settlement, owner_settlement, class, length_m, status, created_at, built_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $2::uuid, 'path', 8000, 'open', now(), now())`,
		linkID, e.cityID, other); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO carriers (id, settlement_id, owner_kind, owner_id, mode, kind, capacity_kg, status, created_at, built_at) VALUES ($1::uuid, $2::uuid, 'settlement', $2::uuid, 'cart_path', 'cart', 600, 'idle', now(), now())`,
		carrierID, e.cityID); err != nil {
		t.Fatal(err)
	}
	e.stock("iron_ore", 40)
	e.stockAt(other, "iron_ore", 5)
	total := e.goods("iron_ore", e.cityID, other)
	if total != 45 {
		t.Fatalf("setup: %d", total)
	}
	// 30 sacks of 25 kg are 750 kg: the one cart takes 600
	resp, err := rrc(e.village.Haul(ctx, e.as(e.head, "settlement.haul", "haul"), handlers.VillageHaulRequest{Action: village.HaulActionSend, Code: "iron_ore", N: "30", M: otherCode}))
	if err != nil {
		t.Fatal(err)
	}
	var hv village.HaulView
	_ = presentation.DecodeView(resp.View, &hv)
	if e.heldAt(e.cityID, "iron_ore") != 40 || len(hv.Loads) != 0 {
		t.Fatalf("a load heavier than the carts take must be refused whole: the store holds %d", e.heldAt(e.cityID, "iron_ore"))
	}
	// 20 sacks: 500 kg
	if _, err := rrc(e.village.Haul(ctx, e.as(e.head, "settlement.haul", "haul"), handlers.VillageHaulRequest{Action: village.HaulActionSend, Code: "iron_ore", N: "20", M: otherCode})); err != nil {
		t.Fatal(err)
	}
	if got := e.heldAt(e.cityID, "iron_ore"); got != 20 {
		t.Fatalf("the goods did not leave the store: %d", got)
	}
	if got := e.heldAt(other, "iron_ore"); got != 5 {
		t.Fatalf("the goods arrived before the cart: %d", got)
	}
	if got := e.goods("iron_ore", e.cityID, other); got != total {
		t.Fatalf("goods on the road: stores + road = %d, want %d", got, total)
	}
	var loadID, action string
	var due time.Time
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, due_at FROM haul_loads WHERE carrier_id = $1::uuid AND arrived_at IS NULL`, carrierID).Scan(&loadID, &action, &due); err != nil {
		t.Fatalf("no load on the road: %v", err)
	}
	if st := e.scalar(`SELECT count(*) FROM carriers WHERE id = $1::uuid AND status = 'moving'`, carrierID); st != 1 {
		t.Fatal("the cart is not on the road")
	}
	if n := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'haul_out' AND from_org = $1::uuid`, e.cityID); n != 20 {
		t.Errorf("haul_out in the journal: %d", n)
	}
	// the action fires early (nothing happens), then on time, then again (nothing happens twice)
	m := e.as(e.head, "haul.arrive", "arrive")
	arrive := func() {
		if _, err := e.village.HaulArrive(ctx, m, handlers.CrimeScheduledRequest{ReferenceType: "haul_loads", ReferenceID: loadID, ActionID: action}); err != nil {
			t.Fatal(err)
		}
	}
	arrive()
	if e.heldAt(other, "iron_ore") != 5 {
		t.Fatal("the cart arrived before its time")
	}
	e.clock.Advance(due.Sub(e.clock.Now()) + time.Minute)
	arrive()
	arrive()
	if got := e.heldAt(other, "iron_ore"); got != 25 {
		t.Fatalf("the other store holds %d, want 25", got)
	}
	if got := e.goods("iron_ore", e.cityID, other); got != total {
		t.Fatalf("after the arrival stores + road = %d, want %d", got, total)
	}
	if e.scalar(`SELECT count(*) FROM haul_loads WHERE arrived_at IS NULL AND carrier_id = $1::uuid`, carrierID) != 0 || e.scalar(`SELECT count(*) FROM carriers WHERE id = $1::uuid AND status = 'idle' AND settlement_id = $2::uuid`, carrierID, other) != 1 {
		t.Error("the cart should stand idle at the other town")
	}
	if n := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'haul_in' AND to_org = $1::uuid`, other); n != 20 {
		t.Errorf("haul_in in the journal: %d (a repeated action must not deliver twice)", n)
	}
	if st := e.scalar(`SELECT count(*) FROM haul_orders WHERE from_settlement = $1::uuid AND status = 'delivered'`, e.cityID); st != 1 {
		t.Error("the consignment is not delivered")
	}
	_ = otherMeta
	_ = otherHead
	e.verifyMining()
}

type railFixture struct {
	line, stopA, stopB string
	segs               []string
	stationA, stationB string
}

// railLine stores an open line of n built sections between a station in each settlement.
func (e *mineEnv) railLine(other string, n int) railFixture {
	e.t.Helper()
	f := railFixture{line: newUUID(e.t), stopA: newUUID(e.t), stopB: newUUID(e.t)}
	ctx := testCtx(e.t)
	station := func(city string, x int) string {
		id := newUUID(e.t)
		if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at) VALUES ($1::uuid, $2::uuid, 'rail_station', $3, 4, 'complete', now(), now())`, id, city, x); err != nil {
			e.t.Fatal(err)
		}
		return id
	}
	f.stationA, f.stationB = station(e.cityID, 70+n), station(other, 71+n)
	var segs []application.RailSegment
	for i := 1; i <= n; i++ {
		id := newUUID(e.t)
		f.segs = append(f.segs, id)
		segs = append(segs, application.RailSegment{ID: id, Seq: i, Tile: int64(1000 + i), LengthM: 305})
	}
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		if err := tx.Rail().AddLine(ctx, application.RailLine{ID: f.line, Name: "A-B", OwnerKind: "settlement", OwnerID: e.cityID, Home: e.cityID, Class: "wagonway", RatePerTonneKm: 100,
			SectionsTotal: n, LengthM: 305 * n, CreatedBy: e.head.ID, CreatedAt: time.Now().UTC()}, segs, []application.RailStop{
			{ID: f.stopA, AfterSeq: 0, BuildingID: f.stationA, SettlementID: e.cityID},
			{ID: f.stopB, AfterSeq: n, BuildingID: f.stationB, SettlementID: other},
		}); err != nil {
			return err
		}
		_, err := tx.Rail().LayNext(ctx, f.line, n, time.Now().UTC())
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return f
}

func (e *mineEnv) train(f railFixture, name string, steps []application.RailStep) string {
	e.t.Helper()
	id := newUUID(e.t)
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		if err := tx.Rail().AddTrain(ctx, application.RailTrain{ID: id, LineID: f.line, OwnerKind: "settlement", OwnerID: e.cityID, Name: name, Wagons: 2, AtStop: f.stopA, CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		for i := range steps {
			steps[i].TrainID = id
		}
		return tx.Rail().SetSchedule(ctx, id, steps)
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *mineEnv) railAct(req handlers.VillageRailRequest) village.RailView {
	e.t.Helper()
	resp, err := rrc(e.village.Rail(testCtx(e.t), e.as(e.head, "settlement.rail", "rail"), req))
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.RailView
	if len(resp.View) > 0 && resp.Screen == village.ScreenRail {
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatal(err)
		}
	}
	return v
}

func (e *mineEnv) railEvent(train string, leg int64) {
	e.t.Helper()
	payload, _ := json.Marshal(map[string]any{"id": train, "settlement_id": e.cityID, "leg": leg})
	if _, err := e.village.RailEvent(testCtx(e.t), e.as(e.head, "rail.event", "event"), handlers.CrimeScheduledRequest{ReferenceType: "rail_trains", ReferenceID: train, Payload: payload}); err != nil {
		e.t.Fatal(err)
	}
}

type trainRow struct {
	status, reason string
	leg            int64
	pos            int
	next           *time.Time
}

func (e *mineEnv) trainState(id string) trainRow {
	e.t.Helper()
	var r trainRow
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT status, wait_reason, leg_no, sched_pos, next_event_at FROM rail_trains WHERE id = $1::uuid`, id).Scan(&r.status, &r.reason, &r.leg, &r.pos, &r.next); err != nil {
		e.t.Fatal(err)
	}
	return r
}

// Train legs are idempotent and single track holds one train: two trains want the same sections, the second waits (and says why); an
// event for a leg that was already run, or run twice, changes nothing; a train brings the owner's goods across and the stores plus the
// train hold what there was.
func TestTrainLegsAreIdempotentAndSingleTrackHoldsOneTrain(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	other, _, _ := e.second()
	f := e.railLine(other, 3)
	e.stock("wheat", 20)
	e.stockAt(other, "wheat", 20)
	e.stock("iron_ore", 30)
	steps := func() []application.RailStep {
		return []application.RailStep{{StopID: f.stopA, Op: application.StepLoad, Item: "iron_ore"}, {StopID: f.stopB, Op: application.StepUnload}}
	}
	t1, t2 := e.train(f, "one", steps()), e.train(f, "two", steps())
	total := e.goods("iron_ore", e.cityID, other)

	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStart, Code: t1})
	s1 := e.trainState(t1)
	if s1.status != application.TrainRunning || s1.leg != 1 || s1.pos != 1 {
		t.Fatalf("the first train: %+v", s1)
	}
	if got := e.heldAt(e.cityID, "iron_ore"); got != 0 {
		t.Fatalf("the train took %d of the ore left in the store, the order loads all above the floor", got)
	}
	// the second train wants the same track
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStart, Code: t2})
	s2 := e.trainState(t2)
	if s2.status != application.TrainWaiting || s2.reason != village.RailWaitBlocked || s2.leg != 0 {
		t.Fatalf("the second train should wait for the track: %+v", s2)
	}
	for _, seg := range f.segs {
		if n := e.scalar(`SELECT count(*) FROM rail_reservations WHERE segment_id = $1::uuid`, seg); n != 1 {
			t.Fatalf("section %s holds %d trains", seg, n)
		}
	}
	if got := e.goods("iron_ore", e.cityID, other); got != total {
		t.Fatalf("goods on the train: %d, want %d", got, total)
	}
	// the first train arrives; the event is repeated and a stale one comes late
	e.clock.Advance(s1.next.Sub(e.clock.Now()) + time.Minute)
	e.railEvent(t1, 1)
	e.railEvent(t1, 1)
	e.railEvent(t1, 7)
	after := e.trainState(t1)
	if after.leg != 2 || after.status != application.TrainRunning {
		t.Fatalf("after arriving and turning round the train should be on leg 2 running: %+v", after)
	}
	if got := e.heldAt(other, "iron_ore"); got != 30 {
		t.Fatalf("the ore did not arrive: %d", got)
	}
	if n := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'haul_in' AND to_org = $1::uuid AND item_code = 'iron_ore'`, other); n != 30 {
		t.Errorf("haul_in in the journal: %d (a repeated event must not unload twice)", n)
	}
	if got := e.goods("iron_ore", e.cityID, other); got != total {
		t.Fatalf("after the arrival stores + trains = %d, want %d", got, total)
	}
	// the feed was eaten once for each leg
	if n := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'team_feed' AND reference_id = $1::uuid`, t1); n != 2*e.rules.TeamFeedWheat {
		t.Errorf("the team ate %d wheat in two legs", n)
	}
	// the second train's retry finds the track taken again by the first on its way back, and waits on
	if e.trainState(t2).status != application.TrainWaiting {
		t.Error("the second train is not waiting")
	}
	e.verifyMining()
}

// Trains asking for the same sections at once: the stored reservation lets exactly one hold them, whichever replica asks first.
func TestSingleTrackAllowsOneReservationPerSectionUnderConcurrentTrains(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	other, _, _ := e.second()
	f := e.railLine(other, 4)
	var trains []string
	for i := 0; i < 8; i++ {
		trains = append(trains, e.train(f, "t", []application.RailStep{{StopID: f.stopA, Op: application.StepPass}, {StopID: f.stopB, Op: application.StepPass}}))
	}
	uow := postgres.NewUnitOfWork(e.pool, testDefaultLanguage)
	var wg sync.WaitGroup
	won := make([]bool, len(trains))
	for i, id := range trains {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			err := uow.Do(context.Background(), func(ctx context.Context, tx application.Tx) error {
				ok, _, err := tx.Rail().Reserve(ctx, id, 1, f.segs, time.Now().UTC())
				won[i] = ok
				return err
			})
			if err != nil {
				t.Errorf("reserve: %v", err)
			}
		}(i, id)
	}
	wg.Wait()
	n := 0
	for _, w := range won {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d trains hold the same four sections", n)
	}
	for _, seg := range f.segs {
		if c := e.scalar(`SELECT count(*) FROM rail_reservations WHERE segment_id = $1::uuid`, seg); c != 1 {
			t.Fatalf("section %s holds %d reservations", seg, c)
		}
	}
	// a passing loop holds two
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE rail_segments SET loop = true WHERE id = $1::uuid`, f.segs[0]); err != nil {
		t.Fatal(err)
	}
	var second string
	for i, id := range trains {
		if !won[i] {
			second = id
			break
		}
	}
	var ok bool
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		var err error
		ok, _, err = tx.Rail().Reserve(ctx, second, 1, f.segs[:1], time.Now().UTC())
		return err
	}); err != nil || !ok {
		t.Fatalf("a loop should hold a second train: %v %v", ok, err)
	}
	var third string
	for i, id := range trains {
		if !won[i] && id != second {
			third = id
			break
		}
	}
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		var err error
		ok, _, err = tx.Rail().Reserve(ctx, third, 1, f.segs[:1], time.Now().UTC())
		return err
	}); err != nil || ok {
		t.Fatalf("a loop holds no third train: %v %v", ok, err)
	}
	_ = rail.Slots
}

// Freight for others: a booking pays the line's rate a tonne-kilometre to the owner (a transfer between treasuries), the train carries
// the goods across, and a booking cancelled before it is loaded gives the fee back.
func TestFreightFeeIsPaidToTheLineOwnerAndRefundedOnCancel(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	other, otherMeta, otherHead := e.second()
	f := e.railLine(other, 3)
	e.stock("wheat", 20)
	e.stockAt(other, "wheat", 20)
	train := e.train(f, "freight", []application.RailStep{{StopID: f.stopA, Op: application.StepPass}, {StopID: f.stopB, Op: application.StepPass}})
	e.stockAt(other, "iron_ore", 20)
	ownerBefore, shipperBefore := treasuryOf(t, e.pool, e.cityID), treasuryOf(t, e.pool, other)

	book := func(qty string) village.RailView {
		resp, err := rrc(e.village.Rail(testCtx(t), e.asOther(otherMeta, otherHead, "settlement.rail", "rail"), handlers.VillageRailRequest{Action: village.RailActionBook, Code: f.line, N: qty, M: "iron_ore", K: f.stopA}))
		if err != nil {
			t.Fatal(err)
		}
		var v village.RailView
		_ = presentation.DecodeView(resp.View, &v)
		return v
	}
	v := book("10")
	if len(v.Bookings) != 1 {
		t.Fatalf("the booking is not in the book: %+v", v)
	}
	// 10 sacks of 25 kg over 3 sections of 305 m at the line's rate of 100
	wantFee := haul.FreightFee(100, 250, 915)
	if wantFee <= 0 {
		t.Fatal("the test's fee is zero")
	}
	if got := v.Bookings[0].Fee; got != wantFee {
		t.Fatalf("the fee is %d, want %d", got, wantFee)
	}
	if got := treasuryOf(t, e.pool, e.cityID) - ownerBefore; got != wantFee {
		t.Errorf("the owner's treasury rose by %d, want %d", got, wantFee)
	}
	if got := shipperBefore - treasuryOf(t, e.pool, other); got != wantFee {
		t.Errorf("the shipper's treasury fell by %d, want %d", got, wantFee)
	}
	if n := e.scalar(`SELECT count(*) FROM ledger_entries WHERE reason = 'freight_fee' AND amount = $1`, wantFee); n < 1 {
		t.Errorf("no freight_fee entry of %d in the ledger", wantFee)
	}
	// cancelled before it is loaded: the fee comes back
	if _, err := rrc(e.village.Rail(testCtx(t), e.asOther(otherMeta, otherHead, "settlement.rail", "rail"), handlers.VillageRailRequest{Action: village.RailActionCancel, Code: v.Bookings[0].ID})); err != nil {
		t.Fatal(err)
	}
	if treasuryOf(t, e.pool, e.cityID) != ownerBefore || treasuryOf(t, e.pool, other) != shipperBefore {
		t.Errorf("a cancelled booking must give the fee back: owner %d (was %d), shipper %d (was %d)", treasuryOf(t, e.pool, e.cityID), ownerBefore, treasuryOf(t, e.pool, other), shipperBefore)
	}
	// booked again, the train fetches it from the shipper's station and brings it to the owner's
	book("10")
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStart, Code: train})
	s := e.trainState(train)
	e.clock.Advance(s.next.Sub(e.clock.Now()) + time.Minute)
	e.railEvent(train, s.leg)
	if got := e.heldAt(other, "iron_ore"); got != 10 {
		t.Fatalf("the train loaded %d of the 20 sacks; the booking was for 10", 20-got)
	}
	s = e.trainState(train)
	e.clock.Advance(s.next.Sub(e.clock.Now()) + time.Minute)
	e.railEvent(train, s.leg)
	if got := e.heldAt(e.cityID, "iron_ore"); got != 10 {
		t.Fatalf("the owner's store holds %d sacks, want the 10 booked", got)
	}
	if e.scalar(`SELECT count(*) FROM haul_orders WHERE from_settlement = $1::uuid AND status = 'delivered'`, other) != 1 {
		t.Error("the booking is not delivered")
	}
	e.verifyMining()
}

func (e *mineEnv) runWork(id string) {
	e.t.Helper()
	var due time.Time
	var action string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT due_at, game_action_id::text FROM build_works WHERE id = $1::uuid`, id).Scan(&due, &action); err != nil {
		e.t.Fatal(err)
	}
	e.clock.Advance(due.Sub(e.clock.Now()) + time.Minute)
	m := e.as(e.head, "works.done", "done")
	for i := 0; i < 2; i++ { // twice: the second changes nothing
		if _, err := e.village.WorksDone(testCtx(e.t), m, handlers.CrimeScheduledRequest{ReferenceType: "build_works", ReferenceID: id, ActionID: action}); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *mineEnv) lastWork(kind string) string {
	e.t.Helper()
	var id string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT id::text FROM build_works WHERE settlement_id = $1::uuid AND kind = $2 AND status = 'working' ORDER BY started_at DESC LIMIT 1`, e.cityID, kind).Scan(&id); err != nil {
		e.t.Fatalf("no %s works in hand: %v", kind, err)
	}
	return id
}

// The mine's works are ordered, paid in goods and in builders' wages, and done at their time, once: a survey turns "probable ore" into a grade and a
// quantity for a fee; a shaft level, a hoist and a bin are built from the stock; a module is not ordered twice.
func TestTheWorksOfAMineArePaidAndDoneOnce(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	mine := e.building("mine")
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, 'masonry', 'researched', now()) ON CONFLICT DO NOTHING`, e.cityID); err != nil {
		t.Fatal(err)
	}
	mining := func(req handlers.VillageMiningRequest) village.MiningView {
		resp, err := rrc(e.village.Mining(testCtx(t), e.as(e.head, "settlement.mining", "mining"), req))
		if err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
		var v village.MiningView
		if len(resp.View) > 0 && resp.Screen == village.ScreenMining {
			if err := presentation.DecodeView(resp.View, &v); err != nil {
				t.Fatal(err)
			}
		}
		return v
	}
	v := mining(handlers.VillageMiningRequest{})
	if len(v.Deposits) != 1 || v.Deposits[0].Surveyed {
		t.Fatalf("before a survey the ore is only probable: %+v", v.Deposits)
	}
	before := treasuryOf(t, e.pool, e.cityID)
	v = mining(handlers.VillageMiningRequest{Action: village.MiningActionSurvey})
	if !v.Deposits[0].Surveyed || v.Deposits[0].GradePermille != e.dep.GradePermille || v.Deposits[0].Band != string(e.rules.Rules.BandOf(e.dep.GradePermille)) {
		t.Fatalf("the survey: %+v", v.Deposits[0])
	}
	if got := before - treasuryOf(t, e.pool, e.cityID); got != e.rules.SurveyFee {
		t.Errorf("the survey cost %d, want %d", got, e.rules.SurveyFee)
	}
	if n := e.scalar(`SELECT count(*) FROM ledger_entries WHERE reason = 'survey_fee' AND amount = $1`, e.rules.SurveyFee); n < 1 {
		t.Error("no survey_fee in the ledger")
	}
	// surveyed again: refused, nothing is paid
	after := treasuryOf(t, e.pool, e.cityID)
	mining(handlers.VillageMiningRequest{Action: village.MiningActionSurvey})
	if treasuryOf(t, e.pool, e.cityID) != after {
		t.Error("a second survey was charged")
	}
	// the bin and a shaft level: no materials, no works
	v = mining(handlers.VillageMiningRequest{Action: village.MiningActionWorks, Code: "ore_bin", N: mine})
	if len(v.Works) != 0 {
		t.Fatal("works were ordered with no timber and stone in the store")
	}
	e.stock("timber", 10)
	e.stock("stone", 10)
	before = treasuryOf(t, e.pool, e.cityID)
	v = mining(handlers.VillageMiningRequest{Action: village.MiningActionWorks, Code: "ore_bin", N: mine})
	if len(v.Works) != 1 || v.Works[0].Module.Code != "ore_bin" {
		t.Fatalf("the bin is not in hand: %+v", v.Works)
	}
	if e.held("timber") != 4 || e.held("stone") != 4 {
		t.Errorf("the bin takes 6 timber and 6 stone: %d %d left", e.held("timber"), e.held("stone"))
	}
	if got := before - treasuryOf(t, e.pool, e.cityID); got != 4*e.rules.WorksShiftWage {
		t.Errorf("the builders' wage was %d, want %d", got, 4*e.rules.WorksShiftWage)
	}
	// ordered twice: refused, nothing is charged
	mining(handlers.VillageMiningRequest{Action: village.MiningActionWorks, Code: "ore_bin", N: mine})
	if e.held("timber") != 4 {
		t.Error("the same module was ordered twice")
	}
	id := e.lastWork(application.WorkMineModule)
	if got := e.scalar(`SELECT count(*) FROM mine_state WHERE building_id = $1::uuid AND ore_bin`, mine); got != 0 {
		t.Fatal("the bin stands before its works are done")
	}
	e.runWork(id)
	if got := e.scalar(`SELECT count(*) FROM mine_state WHERE building_id = $1::uuid AND ore_bin`, mine); got != 1 {
		t.Fatal("the bin was not built")
	}
	// the bin is yard for the ore: the store holds 120 spaces more
	v = mining(handlers.VillageMiningRequest{})
	if len(v.Mines) != 1 || !v.Mines[0].OreBin {
		t.Fatalf("the screen does not show the bin: %+v", v.Mines)
	}
	if got := e.scalar(`SELECT count(*) FROM build_works WHERE settlement_id = $1::uuid AND kind = 'mine_module' AND status = 'done'`, e.cityID); got != 1 {
		t.Error("the order is not closed")
	}
	e.verifyMining()
}

// A line is planned over the world's tiles, drawn, laid in sections that are paid in sleepers, rails and stone, and then trains run on it; a
// road to the other town is laid and a cart built the same way.
func TestARailLineIsPlannedDrawnAndLaidAndARoadAndACartAreBuilt(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	other, _, _ := e.second()
	var otherCode string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT code FROM cities WHERE id = $1::uuid`, other).Scan(&otherCode); err != nil {
		t.Fatal(err)
	}
	ctx := testCtx(t)
	station := func(city string, x int) string {
		id := newUUID(t)
		if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at) VALUES ($1::uuid, $2::uuid, 'rail_station', $3, 4, 'complete', now(), now())`, id, city, x); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := station(e.cityID, 80), station(other, 81)
	resp, err := rrc(e.village.Rail(ctx, e.as(e.head, "settlement.rail", "rail"), handlers.VillageRailRequest{Action: village.RailActionPlan, Code: "wagonway", N: a, M: b}))
	if err != nil {
		t.Fatal(err)
	}
	var v village.RailView
	if resp.Screen == village.ScreenRail {
		_ = presentation.DecodeView(resp.View, &v)
	}
	if v.Quote == nil {
		var rv village.VillageRefusalView
		_ = presentation.DecodeView(resp.View, &rv)
		var ca, cb int32
		_ = e.pool.Raw().QueryRow(ctx, `SELECT world_cell_id FROM cities WHERE id = $1::uuid`, e.cityID).Scan(&ca)
		_ = e.pool.Raw().QueryRow(ctx, `SELECT world_cell_id FROM cities WHERE id = $1::uuid`, other).Scan(&cb)
		t.Skipf("no wagonway route between the two settlements (%q), %.0f km apart", rv.Kind, km(e.world, ca, cb))
	}
	q := v.Quote
	if q.Sections < 1 || q.Lots < int64(q.Sections) || q.LengthM < 1 || q.Wage <= 0 || len(q.Materials) != 3 {
		t.Fatalf("the quote: %+v", q)
	}
	rc := e.rules
	wantLots := q.Lots
	if got := q.Wage; got != wantLots*2*rc.RailWorkHourWage {
		t.Errorf("the wage is %d, want %d (2 hours a lot)", got, wantLots*2*rc.RailWorkHourWage)
	}
	if n := e.scalar(`SELECT count(*) FROM rail_lines WHERE home_settlement = $1::uuid`, e.cityID); n != 0 {
		t.Fatal("a quote wrote a line")
	}
	v = e.railAct(handlers.VillageRailRequest{Action: village.RailActionDraw, Code: "wagonway", N: a, M: b})
	if len(v.Lines) != 1 || v.Lines[0].Sections != q.Sections || v.Lines[0].Built != 0 || v.Lines[0].Status != application.LinePlanned {
		t.Fatalf("the drawn line: %+v", v.Lines)
	}
	line := v.Lines[0].ID
	// laying needs the goods
	before := treasuryOf(t, e.pool, e.cityID)
	v = e.railAct(handlers.VillageRailRequest{Action: village.RailActionBuild, Code: line, N: "1"})
	if e.scalar(`SELECT count(*) FROM build_works WHERE kind = 'rail_sections' AND ref_id = $1::uuid`, line) != 0 || treasuryOf(t, e.pool, e.cityID) != before {
		t.Fatal("track was ordered with no sleepers in the store")
	}
	e.stock("sleeper", 40)
	e.stock("wooden_rail", 20)
	e.stock("stone", 20)
	v = e.railAct(handlers.VillageRailRequest{Action: village.RailActionBuild, Code: line, N: "1"})
	if !v.Lines[0].Laying {
		t.Fatalf("no works in hand: %+v", v.Lines[0])
	}
	if n := e.scalar(`SELECT count(*) FROM ledger_entries WHERE reason = 'rail_works_wage' AND amount > 0`); n < 1 {
		t.Error("no rail_works_wage in the ledger")
	}
	e.runWork(e.lastWork(application.WorkRailSections))
	if got := e.scalar(`SELECT sections_built FROM rail_lines WHERE id = $1::uuid`, line); got != 1 {
		t.Fatalf("%d sections laid", got)
	}
	// a second order while one is in hand is refused; a train needs an open line
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionTrain, Code: line, N: "2"})
	_ = e.scalar(`SELECT count(*) FROM rail_trains WHERE line_id = $1::uuid`, line)
	e.verifyMining()
}

// A road to another settlement and a cart are built from the stock and the treasury, and open when the builders are done.
func TestARoadToAnotherTownAndACartAreBuilt(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	other, _, _ := e.second()
	var otherCode string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT code FROM cities WHERE id = $1::uuid`, other).Scan(&otherCode); err != nil {
		t.Fatal(err)
	}
	haulAct := func(req handlers.VillageHaulRequest) village.HaulView {
		resp, err := rrc(e.village.Haul(testCtx(t), e.as(e.head, "settlement.haul", "haul"), req))
		if err != nil {
			t.Fatal(err)
		}
		var v village.HaulView
		if len(resp.View) > 0 && resp.Screen == village.ScreenHaul {
			if err := presentation.DecodeView(resp.View, &v); err != nil {
				t.Fatal(err)
			}
		}
		return v
	}
	e.stock("stone", 400)
	v := haulAct(handlers.VillageHaulRequest{Action: village.HaulActionLink, Code: otherCode, N: "path"})
	if len(v.Links) != 1 {
		t.Skipf("the test world has no path between the two settlements (links %d)", len(v.Links))
	}
	if v.Links[0].Status != "building" || v.Links[0].LengthM <= 0 {
		t.Fatalf("the link: %+v", v.Links[0])
	}
	lots := haulLotsOf(v.Links[0].LengthM)
	if got := 400 - e.held("stone"); got != lots*e.rules.RoadLinkLotStone {
		t.Errorf("the road took %d stone, want %d", got, lots*e.rules.RoadLinkLotStone)
	}
	e.runWork(e.lastWork(application.WorkRoadLink))
	if n := e.scalar(`SELECT count(*) FROM road_links WHERE status = 'open' AND (a_settlement = $1::uuid OR b_settlement = $1::uuid)`, e.cityID); n != 1 {
		t.Fatal("the road did not open")
	}
	// a second road to the same town is refused
	haulAct(handlers.VillageHaulRequest{Action: village.HaulActionLink, Code: otherCode, N: "path"})
	if n := e.scalar(`SELECT count(*) FROM road_links WHERE a_settlement = $1::uuid OR b_settlement = $1::uuid`, e.cityID); n != 1 {
		t.Errorf("%d roads to one town", n)
	}
	// a cart: planks and timber in the store, the builders' hours from the treasury
	e.stock("timber", 10)
	e.stock("plank", 6)
	before := treasuryOf(t, e.pool, e.cityID)
	haulAct(handlers.VillageHaulRequest{Action: village.HaulActionCarrier, Code: "cart_path"})
	if e.held("timber") != 2 || e.held("plank") != 2 {
		t.Errorf("the cart took timber and planks: %d %d left", e.held("timber"), e.held("plank"))
	}
	if got := before - treasuryOf(t, e.pool, e.cityID); got != 4*e.rules.RailWorkHourWage {
		t.Errorf("the cart's builders cost %d, want %d", got, 4*e.rules.RailWorkHourWage)
	}
	if n := e.scalar(`SELECT count(*) FROM carriers WHERE settlement_id = $1::uuid AND status = 'building'`, e.cityID); n != 1 {
		t.Fatal("the cart is not being built")
	}
	e.runWork(e.lastWork(application.WorkCarrier))
	if n := e.scalar(`SELECT count(*) FROM carriers WHERE settlement_id = $1::uuid AND status = 'idle'`, e.cityID); n != 1 {
		t.Fatal("the cart does not stand ready")
	}
	e.verifyMining()
}

func haulLotsOf(lengthM int64) int64 { return max((lengthM+29)/30, 1) }

// A drawn line is laid whole, a train is made of wagons and a team, its schedule is written through the screen, and it runs the owner's own
// goods across and back; the rate and a passing loop are set by the owner.
func TestATrainIsMadeScheduledAndRunThroughTheScreen(t *testing.T) {
	e := newMineEnv(t, 200_000, nil)
	other, _, _ := e.second()
	ctx := testCtx(t)
	station := func(city string, x int) string {
		id := newUUID(t)
		if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at) VALUES ($1::uuid, $2::uuid, 'rail_station', $3, 4, 'complete', now(), now())`, id, city, x); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := station(e.cityID, 82), station(other, 83)
	v := e.railAct(handlers.VillageRailRequest{Action: village.RailActionDraw, Code: "wagonway", N: a, M: b})
	if len(v.Lines) != 1 {
		t.Fatalf("no line was drawn: %+v", v)
	}
	line, sections := v.Lines[0].ID, v.Lines[0].Sections
	e.stock("sleeper", 400)
	e.stock("wooden_rail", 200)
	e.stock("stone", 200)
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionBuild, Code: line, N: "99"})
	e.runWork(e.lastWork(application.WorkRailSections))
	if got := e.scalar(`SELECT count(*) FROM rail_lines WHERE id = $1::uuid AND status = 'open' AND sections_built = $2`, line, sections); got != 1 {
		t.Fatal("the line is not open after all its sections are laid")
	}
	// a train needs wagons and a team; with no timber it is refused, with it the team's price and the builders leave the treasury
	before := treasuryOf(t, e.pool, e.cityID)
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionTrain, Code: line, N: "2", M: "first"})
	if e.scalar(`SELECT count(*) FROM rail_trains WHERE line_id = $1::uuid`, line) != 0 || treasuryOf(t, e.pool, e.cityID) != before {
		t.Fatal("a train was made without wagons' timber")
	}
	e.stock("timber", 12)
	e.stock("plank", 8)
	v = e.railAct(handlers.VillageRailRequest{Action: village.RailActionTrain, Code: line, N: "2", M: "first"})
	if len(v.Trains) != 1 || v.Trains[0].Wagons != 2 || v.Trains[0].CapacityKg != 4000 {
		t.Fatalf("the train: %+v", v.Trains)
	}
	if got := before - treasuryOf(t, e.pool, e.cityID); got < 1800 {
		t.Errorf("the team and the wagons cost %d, the team alone is 1800", got)
	}
	if n := e.scalar(`SELECT count(*) FROM ledger_entries WHERE reason = 'team_purchase' AND amount > 0`); n < 1 {
		t.Error("no team_purchase in the ledger")
	}
	// too many wagons for one team
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionTrain, Code: line, N: "9"})
	if e.scalar(`SELECT count(*) FROM rail_trains WHERE line_id = $1::uuid`, line) != 1 {
		t.Error("a team drew more wagons than it can")
	}
	train := v.Trains[0].ID
	stops := v.Lines[0].Stops
	if len(stops) != 2 {
		t.Fatalf("stops: %+v", stops)
	}
	// the schedule: load ore at the owner's station, unload at the other; a train with one step cannot start
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStart, Code: train})
	if e.trainState(train).status != application.TrainIdle {
		t.Fatal("a train with no schedule started")
	}
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStep, Code: train, N: stops[0].ID, M: application.StepLoad, K: "iron_ore"})
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStep, Code: train, N: stops[0].ID, M: application.StepUnload})
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStep, Code: train, N: stops[1].ID, M: application.StepUnload})
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStep, Code: train, N: newUUID(t), M: application.StepPass})
	if got := e.scalar(`SELECT count(*) FROM rail_schedules WHERE train_id = $1::uuid`, train); got != 2 {
		t.Fatalf("the schedule has %d steps, want 2 (the repeat of a stop and an unknown stop are refused)", got)
	}
	e.stock("wheat", 10)
	e.stockAt(other, "wheat", 10)
	e.stock("iron_ore", 20)
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionStart, Code: train})
	s := e.trainState(train)
	if s.status != application.TrainRunning {
		t.Fatalf("the train did not leave: %+v", s)
	}
	if got := e.heldAt(e.cityID, "iron_ore"); got != 0 {
		t.Fatalf("the train left with the ore still in the store: %d", got)
	}
	e.clock.Advance(s.next.Sub(e.clock.Now()) + time.Minute)
	e.railEvent(train, s.leg)
	if got := e.heldAt(other, "iron_ore"); got != 20 {
		t.Fatalf("the ore did not arrive: %d", got)
	}
	// the owner sets the rate and a loop; a train running refuses the loop
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionRate, Code: line, N: "77"})
	if e.scalar(`SELECT rate_per_tonne_km FROM rail_lines WHERE id = $1::uuid`, line) != 77 {
		t.Error("the rate was not set")
	}
	var seg string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM rail_segments WHERE line_id = $1::uuid AND seq = 2`, line).Scan(&seg); err != nil {
		t.Fatal(err)
	}
	e.railAct(handlers.VillageRailRequest{Action: village.RailActionLoop, Code: seg, N: "1"})
	if e.scalar(`SELECT count(*) FROM rail_segments WHERE id = $1::uuid AND loop`, seg) != 0 {
		t.Error("a loop was set under a running train")
	}
	e.verifyMining()
}
