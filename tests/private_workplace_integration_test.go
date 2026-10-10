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

// A player employs workers in the workplace he owns (ADR 0066): the owner works it for no wage out of his own store and the
// goods come back to it, a stranger cannot work it, the hands he hires are fed from his store and paid from his cash with the
// levy to the treasury, and the books verify.
func TestAPlayerEmploysWorkersInHisOwnWorkplace(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	ctx := testCtx(t)
	id := e.house(owner, "carpentry_workshop_own", 2000)
	e.resident()
	e.resident()
	stranger := e.resident()
	grantCash(t, e.pool, owner.ID, 5000)
	e.give(owner, application.HoldHome, "timber", 12)
	e.give(owner, application.HoldHome, "bread", 6)
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM game_actions WHERE reference_type = 'settlement_shift'`)
	})

	// a stranger cannot work another citizen's workplace without his job
	resp, err := rrc(e.village.Work(ctx, e.as(stranger, "settlement.work", "work"), handlers.VillageWorkRequest{ID: id}))
	if err != nil {
		t.Fatal(err)
	}
	if got := refusalKind(t, resp.View); resp.Screen != screens.ScreenVillageRefusal || got == "" {
		t.Fatalf("a stranger working the owner's shop should be refused: screen %q view %s", resp.Screen, resp.View)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid`, id); n != 0 {
		t.Fatalf("a refused stranger left %d shifts", n)
	}

	// the owner works it himself: inputs and board from his store, no wage
	if _, err := rrc(e.village.Work(ctx, e.as(owner, "settlement.work", "work"), handlers.VillageWorkRequest{ID: id})); err != nil {
		t.Fatal(err)
	}
	if got := e.held(owner, application.HoldHome, "timber"); got != 9 {
		t.Fatalf("the shift took %d timber out of the owner's store, want 3 (9 left)", 12-got)
	}
	if got := e.held(owner, application.HoldHome, "bread"); got >= 6 {
		t.Errorf("the owner's board should come out of his bread: %d left", got)
	}
	cash0 := cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID)
	e.clock.Advance(3 * time.Hour)
	for _, s := range e.workingShifts(id) {
		e.end(s)
	}
	if got := e.held(owner, application.HoldHome, "plank"); got < 1 {
		t.Fatalf("the planks should come home: %d", got)
	}
	if got := cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID); got != cash0 {
		t.Errorf("working his own shop costs the owner no wage: cash %d -> %d", cash0, got)
	}

	// he hires a hand: the job is his, the wage leaves his cash, the hand is fed from his store
	if _, err := rrc(e.village.LaborPost(ctx, e.as(owner, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: id})); err != nil {
		t.Fatal(err)
	}
	var jobID, employerKind, employerID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text, employer_kind, employer_id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'production' AND status = 'open'`, id).
		Scan(&jobID, &employerKind, &employerID); err != nil || employerKind != "player" || employerID != owner.ID {
		t.Fatalf("the job is the owner's: %q %q (%v)", employerKind, employerID, err)
	}
	if _, err := rrc(e.village.LaborHire(ctx, e.as(owner, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "1"})); err != nil {
		t.Fatal(err)
	}
	working := e.workingShifts(id)
	if len(working) != 1 {
		t.Fatalf("the hired hand should be at work: %d shifts", len(working))
	}
	var wage, payerOK int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT wage, (payer_kind = 'player' AND payer_id = $2::uuid AND worker_kind = 'npc')::int FROM settlement_shifts WHERE id = $1::uuid`,
		working[0].id, owner.ID).Scan(&wage, &payerOK); err != nil || payerOK != 1 || wage <= 0 {
		t.Fatalf("the shift is the owner's, an NPC's, with a wage: wage %d payer ok %d (%v)", wage, payerOK, err)
	}
	if got := cash0 - cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID); got != wage {
		t.Fatalf("the wage set aside from his cash is %d, want %d", got, wage)
	}
	treasury0 := treasuryOf(t, e.pool, e.cityID)
	e.clock.Advance(3 * time.Hour)
	for _, s := range e.workingShifts(id) {
		e.end(s)
	}
	if got := treasuryOf(t, e.pool, e.cityID) - treasury0; got <= 0 {
		t.Errorf("the employer levy should reach the treasury: %d", got)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'done' AND payer_kind = 'player'`, id); n != 2 {
		t.Errorf("%d finished private shifts, want 2", n)
	}

	// the owner sees his workplace in the lot panel
	lot, lresp := e.manage(owner, handlers.VillageManageRequest{Building: id})
	if lresp.Refusal != nil || lot.Workplace == nil {
		t.Fatalf("the lot panel should carry the workplace block: %+v", lresp.Refusal)
	}
	w := lot.Workplace
	if w.Total.Shifts != 2 || w.Today.Shifts != 2 || w.Total.Levy <= 0 || w.Total.Wages != wage || w.Slots != 2 || len(w.Inputs) != 1 ||
		w.Job == nil || w.Job.ID != jobID || w.Job.Crew != 1 || w.ToolsHave != 0 || w.Total.ProducedValue <= 0 {
		t.Errorf("the workplace block is wrong: %+v job %+v", w, w.Job)
	}

	// the books agree
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	i := v.VillageInvariants
	if i.LaborWageLedger != i.LaborWageRows || i.LaborMismatched != 0 || i.LaborEscrowLedger != i.LaborEscrowRows ||
		i.WageLedger != i.WageRows || i.WageMismatched != 0 || i.ShiftItems != i.ShiftItemRows {
		t.Errorf("the books do not agree: %+v", i)
	}
	e.verify2b()
}

// A private workplace stops with the reason: no inputs in the owner's store, no room in it, no cash for the wage, no food for
// a hired hand (ADR 0066). Nothing is taken from a store by a refused shift.
func TestAPrivateWorkplaceStopsWithItsReason(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	ctx := testCtx(t)
	id := e.house(owner, "carpentry_workshop_own", 2000)
	e.resident()
	e.resident()
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM game_actions WHERE reference_type = 'settlement_shift'`)
	})
	work := func() string {
		t.Helper()
		resp, err := rrc(e.village.Work(ctx, e.as(owner, "settlement.work", "work"), handlers.VillageWorkRequest{ID: id}))
		if err != nil {
			t.Fatal(err)
		}
		return refusalKind(t, resp.View)
	}
	// no timber in the store
	if got := work(); got != "materials" {
		t.Errorf("no inputs: refused as %q, want materials", got)
	}
	// a store full of other goods: no room for the planks
	e.give(owner, application.HoldHome, "timber", 3)
	e.give(owner, application.HoldHome, "stone", 38)
	if got := work(); got != "storage_full" {
		t.Errorf("no room: refused as %q, want storage_full", got)
	}
	if got := e.held(owner, application.HoldHome, "timber"); got != 3 {
		t.Errorf("a refused shift took timber: %d left, want 3", got)
	}
	// a hired hand with no cash to pay: the crew pauses
	if _, err := e.pool.Raw().Exec(ctx, `DELETE FROM item_stacks WHERE player_id = $1::uuid AND item_code = 'stone'`, owner.ID); err != nil {
		t.Fatal(err)
	}
	e.give(owner, application.HoldHome, "bread", 3)
	if _, err := rrc(e.village.LaborPost(ctx, e.as(owner, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: id})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'production' AND status = 'open'`, id).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(e.village.LaborHire(ctx, e.as(owner, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "1"})); err != nil {
		t.Fatal(err)
	}
	var paused string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT COALESCE(paused, '') FROM labor_jobs WHERE id = $1::uuid`, jobID).Scan(&paused); err != nil || paused != "employer_broke" {
		t.Errorf("the crew should pause for want of the wage: %q (%v)", paused, err)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid`, id); n != 0 {
		t.Errorf("a paused crew started %d shifts", n)
	}
}
