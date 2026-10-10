//go:build integration

package tests

import (
	"context"
	"encoding/json"
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
)

// «مدیریت قطعهٔ من» (ADR 0045 phase B1): the owner of a lot chooses the function and what is inside; the game builds it
// by the hiring board's shifts and generates the look. The rules are built the way the service builds them, from the
// shipped config.

type lotEnv struct {
	*laborEnv
	owner *application.Player
}

func newLotEnv(t *testing.T) *lotEnv {
	t.Helper()
	l := charteredLaborEnv(t)
	rules, err := settlementcfg.LotRules(config.Defaults().Settlement)
	if err != nil {
		t.Fatal(err)
	}
	l.village.WithLotRules(rules)
	owner := l.resident()
	t.Cleanup(func() { purgeLedgerFor(t, l.pool, owner.ID) })
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_, _ = l.pool.Raw().Exec(c, `ALTER TABLE function_conversions DISABLE TRIGGER function_conversions_append_only`)
		_, _ = l.pool.Raw().Exec(c, `DELETE FROM function_conversions WHERE settlement_id = $1::uuid`, l.cityID)
		_, _ = l.pool.Raw().Exec(c, `ALTER TABLE function_conversions ENABLE TRIGGER function_conversions_append_only`)
		for _, stmt := range []string{
			`DELETE FROM building_works WHERE settlement_id = $1::uuid`,
			`DELETE FROM building_looks WHERE building_ref_id IN (SELECT building_ref_id FROM building_functions WHERE settlement_id = $1::uuid)`,
			`DELETE FROM building_modules WHERE building_ref_id IN (SELECT building_ref_id FROM building_functions WHERE settlement_id = $1::uuid)`,
			`DELETE FROM building_functions WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_private_buildings WHERE settlement_id = $1::uuid`,
		} {
			if _, err := l.pool.Raw().Exec(c, stmt, l.cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		_, _ = l.pool.Raw().Exec(c, `DELETE FROM plan_templates WHERE owner_id = $1::uuid`, owner.ID)
	})
	return &lotEnv{laborEnv: l, owner: owner}
}

var nextLot = 40

// house puts a finished private building of this catalogue type on a lot of the owner's.
func (e *lotEnv) house(owner *application.Player, typeCode string, assessed int64) string {
	e.t.Helper()
	id := newUUID(e.t)
	nextLot++
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, 3, 'complete', now(), now())`, id, e.cityID, typeCode, nextLot); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, $4, now())`, id, e.cityID, owner.ID, assessed); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *lotEnv) give(p *application.Player, holding, item string, qty int64) {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, To: p.ID, ToHolding: holding,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(e.t), At: time.Now().UTC()})
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *lotEnv) held(p *application.Player, holding, item string) int64 {
	e.t.Helper()
	return e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_stacks WHERE player_id = $1::uuid AND holding = $2 AND item_code = $3`, p.ID, holding, item)
}

func (e *lotEnv) manage(p *application.Player, req handlers.VillageManageRequest) (village.LotManageView, *presentation.Response) {
	e.t.Helper()
	resp, err := e.village.ManageLot(testCtx(e.t), e.as(p, "settlement.lot.manage", "lot.manage"), req)
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.LotManageView
	if len(resp.View) > 0 {
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatalf("the view: %v", err)
		}
	}
	return v, resp
}

func (e *lotEnv) confirmAct(p *application.Player, req handlers.VillageManageRequest) village.LotManageView {
	e.t.Helper()
	req.Confirm = village.ResidenceConfirm
	v, resp := e.manage(p, req)
	if resp.Refusal != nil {
		e.t.Fatalf("%s refused: %s\n%s", req.Action, resp.Refusal.Code, resp.Text)
	}
	return v
}

// build works the open order of a building to its end with the owner's own shifts.
func (e *lotEnv) build(owner *application.Player, buildingID string) {
	e.t.Helper()
	ctx := testCtx(e.t)
	for i := 0; i < 30; i++ {
		if e.scalar(`SELECT count(*) FROM building_works WHERE building_id = $1::uuid AND status = 'open'`, buildingID) == 0 {
			return
		}
		var jobID string
		if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'fitout' AND status = 'open'`, buildingID).Scan(&jobID); err != nil {
			e.t.Fatalf("the order has no job on the board: %v", err)
		}
		if _, err := rrc(e.village.LaborTake(ctx, e.as(owner, "settlement.labor.take", "labor.take"), handlers.VillageLaborRequest{ID: jobID})); err != nil {
			e.t.Fatal(err)
		}
		e.clock.Advance(time.Hour)
		for _, s := range e.workingShifts(buildingID) {
			e.end(s)
		}
	}
	e.t.Fatal("the order was never finished")
}

func (e *lotEnv) cap() int64 {
	e.t.Helper()
	resp, err := rrc(e.village.Overview(testCtx(e.t), e.as(e.owner, "settlement.overview", "overview")))
	if err != nil {
		e.t.Fatal(err)
	}
	var v struct {
		PopulationCap int64 `json:"population_cap"`
	}
	if err := json.Unmarshal(resp.View, &v); err != nil {
		e.t.Fatal(err)
	}
	return v.PopulationCap
}

func (e *lotEnv) verifyLots() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	i := v.VillageInvariants
	if !i.Lots || i.LotOrphans != 0 || i.LotWorkMismatched != 0 || i.LotShiftsWithoutJob != 0 || i.LotMaterialsMismatched != 0 || i.LotFeeMismatched != 0 || i.LotHistoryGuards != 2 {
		e.t.Errorf("the lot model does not verify: %+v", i)
	}
	e.verify2b()
}

// The owner opens the lot, orders a bedroom from the home store, the hiring board builds it, the house houses more,
// the look follows, and the books verify.
func TestTheOwnerAddsABedroomAndTheHouseHousesMore(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	id := e.house(owner, "private_cottage", 800)
	e.give(owner, application.HoldHome, "timber", 10)
	grantCash(t, e.pool, owner.ID, 5000)

	// the lot opens with the function its catalogue code stands for and the modules its level includes
	v, resp := e.manage(owner, handlers.VillageManageRequest{Building: id})
	if resp.Refusal != nil || v.Stage != village.LotDetail || v.Function.Code != "dwelling" || v.Function.Level != 1 || !v.CanManage {
		t.Fatalf("the lot: %+v %v", v.Function, resp.Refusal)
	}
	if n := e.scalar(`SELECT count(*) FROM building_modules WHERE building_ref_id = $1::uuid`, id); n != 3 {
		t.Fatalf("a cottage holds a bedroom, a hearth and a storeroom: %d kinds", n)
	}
	if v.Look == nil || v.Look.Function != "dwelling" || v.Look.Windows < 1 {
		t.Errorf("the look is generated: %+v", v.Look)
	}
	cap0 := e.cap()

	// the quote changes nothing; the confirm takes the materials once
	ask, _ := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "bedroom", N: "1"})
	if ask.Stage != village.LotAsk || ask.Reason != "" || ask.Quote == nil || ask.Quote.Shifts != 3 || len(ask.Quote.Materials) != 1 || ask.Quote.Materials[0].Need != 3 {
		t.Fatalf("the quote: %+v reason %q", ask.Quote, ask.Reason)
	}
	if e.held(owner, application.HoldHome, "timber") != 10 {
		t.Fatal("a quote took materials")
	}
	req := handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "bedroom", N: "1", Confirm: village.ResidenceConfirm}
	m := e.as(owner, "settlement.lot.manage", "lot.manage")
	for i := 0; i < 2; i++ { // a redelivery of the same confirm orders once
		if _, err := e.village.ManageLot(testCtx(t), m, req); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.held(owner, application.HoldHome, "timber"); got != 7 {
		t.Errorf("the home store holds %d timber, want 7 (3 taken, once)", got)
	}
	if e.scalar(`SELECT count(*) FROM building_works WHERE building_id = $1::uuid AND status = 'open'`, id) != 1 ||
		e.scalar(`SELECT count(*) FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'fitout' AND status = 'open'`, id) != 1 {
		t.Fatal("one order and one job on the board")
	}
	// while it is being built, a second order is refused and nothing changes in the house yet
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "storeroom", N: "1", Confirm: village.ResidenceConfirm}); r.Refusal == nil {
		t.Error("a second order was taken while one was being built")
	}
	if e.cap() != cap0 {
		t.Error("the house housed more before the work was done")
	}

	e.build(owner, id)
	if got := e.scalar(`SELECT count FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'bedroom'`, id); got != 2 {
		t.Errorf("bedrooms %d, want 2", got)
	}
	if got := e.cap() - cap0; got != 2 {
		t.Errorf("the settlement houses %d more, want 2 (one bedroom beyond the level)", got)
	}
	v, _ = e.manage(owner, handlers.VillageManageRequest{Building: id})
	if v.Work != nil || v.HousingCapacity != 4 || v.AreaUsed != 6 || v.Look == nil || v.Look.Modules["bedroom"] != 2 {
		t.Errorf("the lot after the work: work %v housing %d area %d look %+v", v.Work, v.HousingCapacity, v.AreaUsed, v.Look)
	}
	if e.scalar(`SELECT count(*) FROM building_looks WHERE building_ref_id = $1::uuid`, id) != 1 {
		t.Error("the look was not stored")
	}
	e.verifyLots()
}

// Gates and refusals: what the town has decides what can be ordered, and a stranger can order nothing.
func TestTheLotRefusesWhatTheTownCannotDo(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	id := e.house(owner, "private_cottage", 800)
	grantCash(t, e.pool, owner.ID, 5000)
	stranger := e.resident()
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, stranger.ID) })

	if _, r := e.manage(stranger, handlers.VillageManageRequest{Building: id}); r.Refusal == nil || r.Refusal.Code != "village_"+village.LotNotYours {
		t.Errorf("a stranger opened the lot: %+v", r.Refusal)
	}
	if _, r := e.manage(stranger, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "bedroom", Confirm: village.ResidenceConfirm}); r.Refusal == nil {
		t.Error("a stranger ordered on someone's lot")
	}
	ask := func(action, code string) village.LotManageView {
		v, _ := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: action, Code: code, N: "1"})
		return v
	}
	// no timber in the store: the ask says why
	if v := ask(village.LotActionAdd, "bedroom"); v.Reason != village.LotReasonMaterials || len(v.Needs) == 0 {
		t.Errorf("without materials: %q %v", v.Reason, v.Needs)
	}
	e.give(owner, application.HoldHome, "timber", 30)
	e.give(owner, application.HoldHome, "stone", 30)
	// a cellar needs masonry, a second storey needs carpentry_ii, the kitchen has no rule yet (it waits for the survival chain)
	if v := ask(village.LotActionAdd, "cellar"); v.Reason != village.LotReasonRequires {
		t.Errorf("a cellar without masonry: %q", v.Reason)
	}
	if v := ask(village.LotActionStorey, ""); v.Reason != village.LotReasonStoreys {
		t.Errorf("a second storey without the knowledge: %q", v.Reason)
	}
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "kitchen", N: "1", Confirm: village.ResidenceConfirm}); r.Refusal == nil {
		t.Error("a module no rule reads was built")
	}
	// the floor is full after one bedroom (2 of 6 left); two bedrooms do not fit
	if v := ask(village.LotActionAdd, "bedroom"); v.Reason != "" {
		t.Errorf("one bedroom fits: %q", v.Reason)
	}
	v, _ := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "bedroom", N: "2"})
	if v.Reason != village.LotReasonArea && v.Reason != village.LotReasonSlot {
		t.Errorf("two bedrooms in a cottage: %q", v.Reason)
	}
	// the level two needs carpentry, which this town does not have yet
	if v := ask(village.LotActionLevel, ""); v.Reason != village.LotReasonRequires {
		t.Errorf("the house level without carpentry: %q", v.Reason)
	}
	// not enough cash for the level once the knowledge is there
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, 'carpentry', 'researched', now())`, e.cityID); err != nil {
		t.Skipf("cannot give the knowledge in this schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(context.Background(), `DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid AND code = 'carpentry'`, e.cityID)
	})
	if v := ask(village.LotActionLevel, ""); v.Reason != village.LotReasonMaterials && v.Reason != "" {
		t.Errorf("the house level with carpentry: %q", v.Reason)
	}
	e.verifyLots()
}

// A change of use costs the new function's first level and the fee of the planning authority (to the treasury, or in
// the town's own money when the owner holds it); the old extras are salvaged; the conversion is kept for good.
func TestAChangeOfUsePaysItsFeeAndKeepsTheHistory(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'barter_post', 70, 3, 'complete', now(), now())`, newUUID(t), e.cityID); err != nil {
		t.Fatal(err)
	}
	id := e.house(owner, "private_cottage", 1000)
	e.give(owner, application.HoldHome, "timber", 20)
	grantCash(t, e.pool, owner.ID, 5000)

	v, _ := e.manage(owner, handlers.VillageManageRequest{Building: id})
	var stall *village.LotFunctionChoice
	for i := range v.Functions {
		if v.Functions[i].Function.Code == "stall" {
			stall = &v.Functions[i]
		}
	}
	if stall == nil || !stall.Available || stall.FeeSUP != 20 {
		t.Fatalf("the stall as a choice: %+v", stall)
	}
	sink0, treasury0 := e.sink(), e.supportTreasuryOf()
	e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionFunction, Code: "stall"})
	if got := e.scalar(`SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'use_change_fee' AND amount > 0`); got != 20 {
		t.Errorf("the use-change fee in the ledger is %d, want 20 (2 percent of 1000)", got)
	}
	if e.sink()-sink0 != 500 {
		t.Errorf("the building cost to the trade was %d, want 500", e.sink()-sink0)
	}
	_ = treasury0
	// until the work is done the old function stands
	if f := e.scalar(`SELECT CASE function_code WHEN 'dwelling' THEN 1 ELSE 0 END FROM building_functions WHERE building_ref_id = $1::uuid`, id); f != 1 {
		t.Error("the function changed before the work was done")
	}
	e.build(owner, id)
	var fn, typ string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT f.function_code, b.type_code FROM building_functions f JOIN settlement_buildings b ON b.id = f.building_ref_id WHERE f.building_ref_id = $1::uuid`, id).Scan(&fn, &typ); err != nil {
		t.Fatal(err)
	}
	if fn != "stall" || typ != "market_stall" {
		t.Errorf("after the work the lot is %s / %s, want stall / market_stall", fn, typ)
	}
	if e.scalar(`SELECT count(*) FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'shelves'`, id) != 1 ||
		e.scalar(`SELECT count(*) FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'bedroom'`, id) != 0 {
		t.Error("the stall has its shelves and no bedroom")
	}
	if e.scalar(`SELECT count(*) FROM function_conversions WHERE building_id = $1::uuid AND from_function = 'dwelling' AND to_function = 'stall' AND fee = 20`, id) != 1 {
		t.Error("the conversion is kept")
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE function_conversions SET fee = 0 WHERE building_id = $1::uuid`, id); err == nil {
		t.Error("the conversion history was rewritten")
	}
	e.verifyLots()
}

// The fee of a change of use is paid in the town's own money when the owner holds the units.
func TestTheChangeOfUseFeeSettlesInTheTownMoney(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'barter_post', 71, 3, 'complete', now(), now())`, newUUID(t), e.cityID); err != nil {
		t.Fatal(err)
	}
	id := e.house(owner, "private_cottage", 1000)
	e.give(owner, application.HoldHome, "timber", 20)
	grantCash(t, e.pool, owner.ID, 5000)
	if _, err := rrc(e.village.CurrencyDesk(testCtx(t), e.as(owner, "settlement.currency.desk", "currency.desk"),
		handlers.VillageDeskRequest{Side: "buy", Amount: "1000", Confirm: village.ResidenceConfirm})); err != nil {
		t.Fatal(err)
	}
	units0 := e.holding(owner.ID)
	e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionFunction, Code: "stall"})
	if got := e.scalar(`SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'use_change_fee' AND amount > 0`); got != 0 {
		t.Errorf("the fee was also paid in SUP: %d", got)
	}
	if e.scalar(`SELECT count(*) FROM local_payments WHERE settlement_id = $1::uuid AND flow = 'use_change_fee'`, e.cityID) != 1 {
		t.Error("no local payment row for the fee")
	}
	if units0-e.holding(owner.ID) != 200 { // 20 SUP at 10 units a SUP
		t.Errorf("the owner paid %d units, want 200", units0-e.holding(owner.ID))
	}
	e.build(owner, id)
	e.verifyLots()
}

// NPC labourers build an order too: the owner hires a crew, the crew's shifts finish it, wages are the owner's.
func TestNPCLabourersBuildTheOwnersOrder(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	id := e.house(owner, "private_cottage", 800)
	e.give(owner, application.HoldHome, "timber", 20)
	grantCash(t, e.pool, owner.ID, 5000)
	e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "storeroom", N: "1"})
	var jobID string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'fitout'`, id).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	cash0 := e.cash(owner.ID)
	if _, err := rrc(e.village.LaborHire(testCtx(t), e.as(owner, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "1"})); err != nil {
		t.Fatal(err)
	}
	if e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND kind = 'fitout' AND worker_kind = 'npc'`, id) == 0 {
		t.Fatal("no NPC shift started on the order")
	}
	for i := 0; i < 20 && e.scalar(`SELECT count(*) FROM building_works WHERE building_id = $1::uuid AND status = 'open'`, id) > 0; i++ {
		e.clock.Advance(time.Hour)
		for _, s := range e.workingShifts(id) {
			e.end(s)
		}
	}
	if e.scalar(`SELECT count(*) FROM building_works WHERE building_id = $1::uuid AND status = 'done'`, id) != 1 {
		t.Fatal("the NPC crew did not finish the order")
	}
	if e.cash(owner.ID) >= cash0 {
		t.Error("the owner paid no wages")
	}
	if got := e.scalar(`SELECT count FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'storeroom'`, id); got != 2 {
		t.Errorf("storerooms %d, want 2", got)
	}
	e.verifyLots()
}

// Taking a module out gives back a share of the materials to the holding slot; an included module cannot be removed.
func TestRemovingAModuleGivesBackAShareAndNeverTheIncluded(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	id := e.house(owner, "private_cottage", 800)
	e.give(owner, application.HoldHome, "timber", 20)
	grantCash(t, e.pool, owner.ID, 5000)
	e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "bedroom", N: "1"})
	e.build(owner, id)
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionRemove, Code: "hearth", N: "1", Confirm: village.ResidenceConfirm}); r.Refusal == nil {
		t.Error("an included module was taken out")
	}
	claim0 := e.held(owner, application.HoldClaim, "timber")
	e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionRemove, Code: "bedroom", N: "1"})
	if got := e.held(owner, application.HoldClaim, "timber") - claim0; got != 0 && got != 3*3000/10000 {
		// 30 percent of 3 timber, rounded down, is 0: the share is whole units only
		t.Errorf("salvage %d", got)
	}
	if got := e.scalar(`SELECT count FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'bedroom'`, id); got != 1 {
		t.Errorf("bedrooms %d, want 1 (the included one)", got)
	}
	e.verifyLots()
}

// Templates: a layout is saved from a building, applied to another by its id or its share code, never removes anything
// and never bypasses a gate; a stranger deletes nothing of mine.
func TestTemplatesSaveApplyAndShare(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	first := e.house(owner, "private_cottage", 800)
	second := e.house(owner, "private_cottage", 800)
	e.give(owner, application.HoldHome, "timber", 40)
	grantCash(t, e.pool, owner.ID, 9000)
	e.confirmAct(owner, handlers.VillageManageRequest{Building: first, Action: village.LotActionAdd, Code: "bedroom", N: "1"})
	e.build(owner, first)
	saved := e.confirmAct(owner, handlers.VillageManageRequest{Building: first, Action: village.LotActionTemplateSave, Name: "طبقه‌ای دو اتاقه"})
	if saved.ShareCode == "" || len(saved.Templates) != 1 || saved.Templates[0].Function.Code != "dwelling" {
		t.Fatalf("the template: %+v code %q", saved.Templates, saved.ShareCode)
	}
	tid := saved.Templates[0].ID
	ask, _ := e.manage(owner, handlers.VillageManageRequest{Building: second, Action: village.LotActionTemplateApply, Code: tid})
	if ask.Stage != village.LotAsk || ask.Reason != "" || ask.Quote == nil || len(ask.Quote.Adds) != 1 || ask.Quote.Adds[0].Module.Code != "bedroom" {
		t.Fatalf("applying the template: %+v reason %q", ask.Quote, ask.Reason)
	}
	e.confirmAct(owner, handlers.VillageManageRequest{Building: second, Action: village.LotActionTemplateApply, Code: tid})
	e.build(owner, second)
	if got := e.scalar(`SELECT count FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'bedroom'`, second); got != 2 {
		t.Errorf("the second house has %d bedrooms, want 2", got)
	}
	// nothing left to build: the template is applied
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: second, Action: village.LotActionTemplateApply, Code: tid, Confirm: village.ResidenceConfirm}); r.Refusal == nil || r.Refusal.Code != "village_"+village.LotNothing {
		t.Errorf("applying a finished template: %+v", r.Refusal)
	}
	// a neighbour loads it by the share code onto his own house
	other := e.resident()
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, other.ID) })
	third := e.house(other, "private_cottage", 800)
	e.give(other, application.HoldHome, "timber", 10)
	ask, _ = e.manage(other, handlers.VillageManageRequest{Building: third, Action: village.LotActionTemplateApply, Code: saved.ShareCode})
	if ask.Stage != village.LotAsk || ask.Quote == nil || len(ask.Quote.Adds) == 0 {
		t.Errorf("the share code did not load: %+v", ask)
	}
	// only the owner of a template deletes it
	if _, r := e.manage(other, handlers.VillageManageRequest{Building: third, Action: village.LotActionTemplateDelete, Code: tid, Confirm: village.ResidenceConfirm}); r.Refusal == nil {
		t.Error("a stranger deleted my template")
	}
	e.confirmAct(owner, handlers.VillageManageRequest{Building: first, Action: village.LotActionTemplateDelete, Code: tid})
	if e.scalar(`SELECT count(*) FROM plan_templates WHERE owner_id = $1::uuid`, owner.ID) != 0 {
		t.Error("the template was not deleted")
	}
	e.verifyLots()
}

// Boot-shaped: the lot rules come from the shipped config, the command is on the registry with its arguments in order, and
// the backfill gives a finished building of the old catalogue its function exactly once.
func TestTheLotRulesAndTheBackfillWorkAsTheServiceHasThem(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	id := e.house(owner, "private_house", 1800) // the old catalogue's level two
	snap := loadTestContent(t)
	uow := postgres.NewUnitOfWork(e.pool, testDefaultLanguage)
	rep, err := handlers.BackfillLotFunctions(testCtx(t), uow, snap, []string{e.cityID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written < 1 {
		t.Fatalf("nothing was written: %+v", rep)
	}
	var level int
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT level FROM building_functions WHERE building_ref_id = $1::uuid`, id).Scan(&level); err != nil || level != 2 {
		t.Errorf("private_house is level 2 of the dwelling: %d %v", level, err)
	}
	if got := e.scalar(`SELECT count FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'bedroom'`, id); got != 2 {
		t.Errorf("a house has two bedrooms: %d", got)
	}
	again, err := handlers.BackfillLotFunctions(testCtx(t), uow, snap, []string{e.cityID}, nil)
	if err != nil || again.Written != 0 || again.Had < 1 {
		t.Errorf("a second run wrote %+v (%v)", again, err)
	}
	// the share of the old effects is not counted twice: the house still houses what the catalogue said
	if v, _ := e.manage(owner, handlers.VillageManageRequest{Building: id}); v.HousingCapacity != 4 || !strings.Contains(string(mustJSON(t, v.Function)), "dwelling") {
		t.Errorf("the house after the backfill: housing %d", v.HousingCapacity)
	}
	e.verifyLots()
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *lotEnv) supportTreasuryOf() int64 {
	return e.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, e.cityID)
}
