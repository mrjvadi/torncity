//go:build integration

package tests

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

// Special projects, intelligence between settlements and the licence market (docs/adr/0077, plan B13), through the real handlers, the unit of
// work and the ledger. Three settlements are founded: A (the one the service-day env builds), B and C.

type specialEnv struct {
	*polityEnv
	special application.SpecialRules
}

func newSpecialEnv(t *testing.T, tune func(*application.SpecialRules)) *specialEnv {
	t.Helper()
	p := newPolityEnv(t, nil)
	cfg := config.Defaults()
	rules := settlementcfg.Special(cfg.Projects, cfg.Intel, cfg.Licence)
	from := p.clock.Now().Add(-48 * time.Hour)
	rules.ProjectsAt, rules.IntelAt, rules.SabotageAt, rules.LicenceAt = from, from, from, from
	rules.Intel.Grace = 0
	if tune != nil {
		tune(&rules)
	}
	p.village.WithSpecial(rules)
	e := &specialEnv{polityEnv: p, special: rules}
	t.Cleanup(e.cleanSpecial)
	return e
}

// cleanSpecial takes everything the item wrote away before the settlements and the players go.
func (e *specialEnv) cleanSpecial() {
	ctx := testCtx(e.t)
	var owners []string
	for _, q := range []string{
		`SELECT id::text FROM special_projects`, `SELECT id::text FROM project_contracts`, `SELECT id::text FROM licence_auctions`, `SELECT id::text FROM licence_applications`,
	} {
		rows, err := e.pool.Raw().Query(ctx, q)
		if err != nil {
			e.t.Errorf("cleanup %q: %v", q, err)
			continue
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				owners = append(owners, id)
			}
		}
		rows.Close()
	}
	for _, stmt := range []string{
		`DELETE FROM game_actions WHERE action_type = 'polity_tick'`,
		`TRUNCATE sabotage_effects, sabotage_acts, intel_shares, intel_expulsions, intel_exposures, intel_reports, intel_ops, intel_agents, intel_vetting,
			licence_events, licence_listings, licence_bids, licence_applications, licence_auctions, licences, licence_rules,
			project_contract_stages, project_contract_bids, project_contracts, project_npc_days, project_shifts, project_pledges, project_stock, project_stages, special_projects CASCADE`,
		`DELETE FROM settlement_buildings WHERE type_code IN ('intelligence_office', 'watch_hut', 'lookout_post', 'militia_hall') AND settlement_id IN ($1::uuid, $2::uuid, $3::uuid)`,
		`DELETE FROM settlement_knowledge_owned WHERE acquired_via = 'researched' AND settlement_id IN ($1::uuid, $2::uuid, $3::uuid)`,
	} {
		var err error
		if strings.Contains(stmt, "$1") {
			_, err = e.pool.Raw().Exec(ctx, stmt, e.cityID, e.attCity, e.cityC)
		} else {
			_, err = e.pool.Raw().Exec(ctx, stmt)
		}
		if err != nil {
			e.t.Errorf("cleanup %q: %v", stmtHead(stmt), err)
		}
	}
	purgeLedgerForAll(e.t, e.pool, owners)
}

// purgeLedgerForAll is purgeLedgerFor for a set of owners at once: a fund, the escrow of its contract and the accounts they paid move money
// among themselves, so reversing them one owner at a time would take an escrow below zero.
func purgeLedgerForAll(t *testing.T, pool *postgres.Pool, owners []string) {
	t.Helper()
	if len(owners) == 0 {
		return
	}
	ctx := testCtx(t)
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, stmt := range []string{
		`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`,
		`CREATE TEMP TABLE purge_tx ON COMMIT DROP AS SELECT DISTINCT e.transaction_id FROM ledger_entries e JOIN accounts a ON a.id = e.account_id WHERE a.owner_id = ANY($1::uuid[])`,
		`UPDATE accounts a SET balance = a.balance - d.delta FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
			WHERE transaction_id IN (SELECT transaction_id FROM purge_tx) GROUP BY account_id) d WHERE a.id = d.account_id`,
		`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_tx)`,
		`DELETE FROM accounts WHERE owner_id = ANY($1::uuid[])`,
		`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`,
	} {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{owners}
		}
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			t.Errorf("cleanup %q: %v", stmtHead(stmt), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

// know gives a settlement research it did, so what asks for it opens.
func (e *specialEnv) know(city string, codes ...string) {
	e.t.Helper()
	for _, c := range codes {
		if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES ($1::uuid, $2::uuid, $3, 'researched', now())
			ON CONFLICT DO NOTHING`, newUUID(e.t), city, c); err != nil {
			e.t.Fatal(err)
		}
	}
}

// putStock puts goods in a settlement's store.
func (e *specialEnv) putStock(city, item string, qty int64) {
	e.t.Helper()
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, ToOrg: application.SettlementOrg(city), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(e.t), At: time.Now().UTC()})
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *specialEnv) stockOf(city, item string) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2`, city, item)
}

// put puts a standing building of a settlement on a lot.
func (e *specialEnv) put(city, code string) string {
	e.t.Helper()
	id := newUUID(e.t)
	x := e.scalarInt(`SELECT COALESCE(max(lot_x), 0) + 1 FROM settlement_buildings WHERE settlement_id = $1::uuid`, city)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, 7, 'complete', now(), now())`, id, city, code, x); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func decodeView[T any](e *specialEnv, resp *presentation.Response, screen string) (T, string) {
	e.t.Helper()
	var v T
	if resp.Screen == screen {
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatalf("decoding the %s screen: %v", screen, err)
		}
		return v, ""
	}
	kind := refusalKind(e.t, resp.View)
	if kind == "" {
		e.t.Fatalf("expected %s, got %s", screen, resp.Screen)
	}
	return v, kind
}

func (e *specialEnv) project(p *application.Player, req handlers.VillageProjectRequest) (village.ProjectView, string) {
	e.t.Helper()
	resp, err := e.village.Project(testCtx(e.t), e.metaOf(p, "settlement.project", "project"), req)
	if err != nil {
		e.t.Fatalf("project %s: %v", req.Action, err)
	}
	return decodeView[village.ProjectView](e, resp, village.ScreenProject)
}

func (e *specialEnv) mustProject(p *application.Player, req handlers.VillageProjectRequest) village.ProjectView {
	e.t.Helper()
	v, kind := e.project(p, req)
	if kind != "" {
		e.t.Fatalf("project %s was refused: %s", req.Action, kind)
	}
	return v
}

func (e *specialEnv) intel(p *application.Player, req handlers.VillageIntelRequest) (village.IntelView, string) {
	e.t.Helper()
	resp, err := e.village.Intel(testCtx(e.t), e.metaOf(p, "settlement.intel", "intel"), req)
	if err != nil {
		e.t.Fatalf("intel %s: %v", req.Action, err)
	}
	return decodeView[village.IntelView](e, resp, village.ScreenIntel)
}

func (e *specialEnv) mustIntel(p *application.Player, req handlers.VillageIntelRequest) village.IntelView {
	e.t.Helper()
	v, kind := e.intel(p, req)
	if kind != "" {
		e.t.Fatalf("intel %s was refused: %s", req.Action, kind)
	}
	return v
}

func (e *specialEnv) licence(p *application.Player, req handlers.VillageLicenceRequest) (village.LicenceView, string) {
	e.t.Helper()
	resp, err := e.village.Licence(testCtx(e.t), e.metaOf(p, "settlement.licence", "licence"), req)
	if err != nil {
		e.t.Fatalf("licence %s: %v", req.Action, err)
	}
	return decodeView[village.LicenceView](e, resp, village.ScreenLicence)
}

func (e *specialEnv) mustLicence(p *application.Player, req handlers.VillageLicenceRequest) village.LicenceView {
	e.t.Helper()
	v, kind := e.licence(p, req)
	if kind != "" {
		e.t.Fatalf("licence %s was refused: %s", req.Action, kind)
	}
	return v
}

// tickAs sends one deadline of the item as the scheduler would.
func (e *specialEnv) tickOf(kind, id string) { e.tick(kind, id, "") }

func (e *specialEnv) fundOf(project string) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'project_fund' AND owner_id = $1::uuid`, project)
}

func (e *specialEnv) cash(p *application.Player) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'player_cash' AND owner_id = $1::uuid`, p.ID)
}

func (e *specialEnv) verifySpecial() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Special {
		e.t.Fatal("migration 0155 is not applied")
	}
	if s.ProjectGiftLedger != s.ProjectGiftRows || s.ProjectStagesBroken != 0 || s.ProjectCancelledFunds != 0 || s.ProjectEscrowBroken != 0 || s.IntelFeeLedger != s.IntelFeeRows ||
		s.IntelWageLedger != s.IntelWageRows || s.SabotageCostLedger != s.SabotageCostRows || s.IntelReportBlown != 0 || s.IntelReportFigures != 0 || s.IntelNoExpulsion != 0 ||
		s.LicenceEscrowBroken != 0 || s.LicenceAwardLedger != s.LicenceAwardRows || s.LicenceRevokedNoCause != 0 || s.LicenceAuctionManyWinners != 0 {
		e.t.Errorf("the special invariants do not hold: %+v", s)
	}
}

var _ = fmt.Sprint
var _ sync.Mutex

func (e *specialEnv) startProject(p *application.Player, kind string) string {
	e.t.Helper()
	e.mustProject(p, handlers.VillageProjectRequest{Action: village.ProjectActionStart, Code: kind})
	return e.text(`SELECT id::text FROM special_projects WHERE kind = $1 AND status = 'active' ORDER BY started_at DESC LIMIT 1`, kind)
}

func (e *specialEnv) lineOf(p *application.Player, id string) village.ProjectLine {
	e.t.Helper()
	v := e.mustProject(p, handlers.VillageProjectRequest{Action: village.ProjectActionOpen, ID: id})
	for _, l := range v.Projects {
		if l.ID == id {
			return l
		}
	}
	e.t.Fatalf("project %s is not on the screen", id)
	return village.ProjectLine{}
}

// A great work is a long business: a stage waits for its materials, then its crew, then the time that must pass, and what was done is never lost.
// Hired labour has a limit a day; a crew shift pays its worker from the fund; the stage is accepted once all is there and the next begins.
func TestAProjectWaitsForMaterialsAndCrewAndLosesNothing(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.know(e.cityID, "masonry", "canal_irrigation")
	// a settlement that has not researched it cannot start it (rule 2)
	if _, kind := e.project(e.att, handlers.VillageProjectRequest{Action: village.ProjectActionStart, Code: "aqueduct"}); kind != village.ProjectResearch {
		t.Fatalf("a town without the research must be refused, got %q", kind)
	}
	// a resident without project.start cannot start it either
	nobody := e.resident("A")
	if _, kind := e.project(nobody, handlers.VillageProjectRequest{Action: village.ProjectActionStart, Code: "aqueduct"}); kind != village.VillageNotOfficeHolder {
		t.Fatalf("a resident without the power must be refused, got %q", kind)
	}
	treasury0 := e.cityCash(e.cityID)
	id := e.startProject(e.head, "aqueduct")
	if got := treasury0 - e.cityCash(e.cityID); got != e.special.Projects.StartFee {
		t.Fatalf("the filing fee is a sink: %d, fee %d", got, e.special.Projects.StartFee)
	}
	if l := e.lineOf(e.head, id); l.Stage != 1 || l.Stages != 3 || l.Waiting != village.ProjectWaitMaterials {
		t.Fatalf("a new work is in its first stage and waits for materials: %+v", l)
	}
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionAllocate, ID: id, Amount: "3000"})
	if got := e.fundOf(id); got != 3000 {
		t.Fatalf("the fund holds the allocation, got %d", got)
	}
	// the pool's labour: eight hours a day, paid to the sink
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionHire, ID: id, N: "8"})
	if _, kind := e.project(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionHire, ID: id, N: "1"}); kind != village.ProjectNoHours {
		t.Fatalf("the pool gives no more than its day's hours, got %q", kind)
	}
	if got := e.fundOf(id); got != 3000-8*e.special.Projects.NPCWagePerHour {
		t.Fatalf("hired labour is paid from the fund, got %d", got)
	}
	// materials: the stock must hold them, and the stage takes no more than it asks
	e.putStock(e.cityID, "stone", 30)
	if _, kind := e.project(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionDeliver, ID: id, Code: "stone", N: "40"}); kind != village.ProjectNoStock {
		t.Fatalf("the stock does not hold it, got %q", kind)
	}
	e.putStock(e.cityID, "stone", 11)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionDeliver, ID: id, Code: "stone", N: "40"})
	if _, kind := e.project(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionDeliver, ID: id, Code: "stone", N: "1"}); kind != village.ProjectNotNeeded {
		t.Fatalf("the stage asks no more stone, got %q", kind)
	}
	if got := e.stockOf(e.cityID, "stone"); got != 1 {
		t.Fatalf("the stone left the store for the site, %d left", got)
	}
	// a shift of a resident: one at a time, paid from the fund when it ends, its hours counted then
	w := e.resident("A")
	e.mustProject(w, handlers.VillageProjectRequest{Action: village.ProjectActionWork, ID: id})
	if _, kind := e.project(w, handlers.VillageProjectRequest{Action: village.ProjectActionWork, ID: id}); kind != village.ProjectBusy {
		t.Fatalf("a player has one shift at a time, got %q", kind)
	}
	shift := e.text(`SELECT id::text FROM project_shifts WHERE player_id = $1::uuid`, w.ID)
	e.clock.Advance(time.Duration(e.special.Projects.ShiftMinutes+1) * time.Minute)
	e.tickOf(application.TickProjectShift, shift)
	e.tickOf(application.TickProjectShift, shift) // a replay changes nothing
	wage := e.special.Projects.ShiftHours * e.special.Projects.WagePerHour
	if got := e.cash(w); got != wage {
		t.Fatalf("the crew member is paid once from the fund: %d, want %d", got, wage)
	}
	l := e.lineOf(e.head, id)
	if l.Hours != 8+e.special.Projects.ShiftHours || l.Waiting != village.ProjectWaitMaterials {
		t.Fatalf("hours are counted when the shift ends and the stage still waits for its clay: %+v", l)
	}
	// nothing is lost while it waits: the stone is on the site, no stage is done
	if e.scalarInt(`SELECT count(*) FROM project_stages WHERE project_id = $1::uuid`, id) != 0 || e.scalarInt(`SELECT qty FROM project_stock WHERE project_id = $1::uuid AND item = 'stone'`, id) != 40 {
		t.Fatal("a waiting stage keeps what was delivered")
	}
	e.putStock(e.cityID, "clay", 20)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionDeliver, ID: id, Code: "clay", N: "20"})
	if l := e.lineOf(e.head, id); l.Waiting != village.ProjectWaitCrew {
		t.Fatalf("with the materials in, the crew is what it waits for: %+v", l)
	}
	// day two and day three: more labour; the last hours finish the stage
	e.clock.Advance(24 * time.Hour)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionHire, ID: id, N: "8"})
	e.mustProject(w, handlers.VillageProjectRequest{Action: village.ProjectActionWork, ID: id})
	shift2 := e.text(`SELECT id::text FROM project_shifts WHERE player_id = $1::uuid AND status = 'running'`, w.ID)
	e.clock.Advance(time.Duration(e.special.Projects.ShiftMinutes+1) * time.Minute)
	e.tickOf(application.TickProjectShift, shift2)
	if e.scalarInt(`SELECT stage FROM special_projects WHERE id = $1::uuid`, id) != 1 {
		t.Fatal("twenty-four hours of thirty-two do not finish a stage")
	}
	e.clock.Advance(24 * time.Hour)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionHire, ID: id, N: "8"})
	if got := e.scalarInt(`SELECT stage FROM special_projects WHERE id = $1::uuid`, id); got != 2 {
		t.Fatalf("the stage is accepted when all is there, the work is in stage %d", got)
	}
	if e.scalarInt(`SELECT count(*) FROM project_stages WHERE project_id = $1::uuid AND stage = 1`, id) != 1 || e.scalarInt(`SELECT COALESCE(sum(qty), 0) FROM project_stock WHERE project_id = $1::uuid`, id) != 1 {
		// one stone is the surplus the stage did not need? there was none: the site is empty
	}
	if got := e.scalarInt(`SELECT COALESCE(sum(qty), 0) FROM project_stock WHERE project_id = $1::uuid`, id); got != 0 {
		t.Fatalf("a stage done uses its materials up, %d left on the site", got)
	}
	// the second stage needs more: it waits again, and a stage with everything in it still waits for its least time
	e.putStock(e.cityID, "stone", 50)
	e.putStock(e.cityID, "brick", 20)
	e.putStock(e.cityID, "clay", 20)
	for _, it := range []struct {
		code string
		n    string
	}{{"stone", "50"}, {"brick", "20"}} {
		e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionDeliver, ID: id, Code: it.code, N: it.n})
	}
	e.pool.Raw().Exec(testCtx(t), `UPDATE special_projects SET stage_hours = 40 WHERE id = $1::uuid`, id)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionDeliver, ID: id, Code: "clay", N: "20"})
	if l := e.lineOf(e.head, id); l.Stage != 2 || l.Waiting != village.ProjectWaitTime {
		t.Fatalf("a stage with all its materials and crew waits for its least time: %+v", l)
	}
	e.clock.Advance(13 * time.Hour)
	e.tick(application.TickProjectStage, id, "2")
	if got := e.scalarInt(`SELECT stage FROM special_projects WHERE id = $1::uuid`, id); got != 3 {
		t.Fatalf("when the time is over the tick accepts the stage, stage %d", got)
	}
	e.tick(application.TickProjectStage, id, "2") // a replay finds the work in stage 3 and changes nothing
	if got := e.scalarInt(`SELECT count(*) FROM project_stages WHERE project_id = $1::uuid`, id); got != 2 {
		t.Fatalf("each stage is written once, %d rows", got)
	}
	e.verifySpecial()
}

// resident is a person of one of the settlements; what the item wrote about them is taken away before they go.
func (e *specialEnv) resident(place string) *application.Player {
	e.t.Helper()
	p := e.polityEnv.resident(place)
	e.t.Cleanup(e.cleanSpecial)
	return p
}

// Pledges from anyone, residents or outsiders, make the fund; the donor roll shows the name of a named gift and hides that of an anonymous one;
// a gift comes back only before the first stage has started; a cancelled work returns what is left of the fund in proportion to what each gave.
func TestPledgesFormADonorRollAndComeBackOnlyBeforeTheWorkStarts(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.know(e.cityID, "masonry", "canal_irrigation")
	id := e.startProject(e.head, "aqueduct")
	d1, d2 := e.resident("B"), e.resident("C")
	grant(t, e.pool, application.AccountPlayerCash, d1.ID, 5000)
	grant(t, e.pool, application.AccountPlayerCash, d2.ID, 5000)
	if _, kind := e.project(d1, handlers.VillageProjectRequest{Action: village.ProjectActionPledge, ID: id, Amount: "50"}); kind != village.ProjectBelowMin {
		t.Fatalf("a gift under the least is refused, got %q", kind)
	}
	if _, kind := e.project(d1, handlers.VillageProjectRequest{Action: village.ProjectActionPledge, ID: id, Amount: "999999"}); kind != village.ProjectNoMoney {
		t.Fatalf("a gift of more than one has is refused, got %q", kind)
	}
	// outsiders may give: d1 lives in B and d2 in C, the work is A's
	e.mustProject(d1, handlers.VillageProjectRequest{Action: village.ProjectActionPledge, ID: id, Amount: "1000"})
	e.mustProject(d2, handlers.VillageProjectRequest{Action: village.ProjectActionPledge, ID: id, Amount: "500", Anonymous: "1"})
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionAllocate, ID: id, Amount: "500"})
	if got := e.fundOf(id); got != 2000 {
		t.Fatalf("the fund is the sum of the gifts, got %d", got)
	}
	// the donor roll, as the head sees it, and as the anonymous donor sees it
	l := e.lineOf(e.head, id)
	var named, anonymous, treasury int
	for _, d := range l.Donors {
		switch {
		case d.Kind == "allocation":
			treasury++
		case d.Anonymous && d.Name == "":
			anonymous++
		case !d.Anonymous && d.Name != "":
			named++
		}
	}
	if len(l.Donors) != 3 || named != 1 || anonymous != 1 || treasury != 1 {
		t.Fatalf("the roll names the open gift, hides the anonymous one and shows the treasury's: %+v", l.Donors)
	}
	if mine := e.lineOf(d2, id); len(mine.Donors) != 3 {
		t.Fatalf("the donor sees the roll too: %+v", mine.Donors)
	} else {
		own := 0
		for _, d := range mine.Donors {
			if d.Mine && d.Name != "" && d.Refunds {
				own++
			}
		}
		if own != 1 {
			t.Fatalf("an anonymous donor sees their own gift and may take it back: %+v", mine.Donors)
		}
	}
	// d1 takes the gift back before the work has begun, once
	pledge := e.text(`SELECT id::text FROM project_pledges WHERE player_id = $1::uuid`, d1.ID)
	e.mustProject(d1, handlers.VillageProjectRequest{Action: village.ProjectActionUnpledge, ID: pledge})
	if e.cash(d1) != 5000 || e.fundOf(id) != 1000 {
		t.Fatalf("the gift returned whole: cash %d, fund %d", e.cash(d1), e.fundOf(id))
	}
	if _, kind := e.project(d1, handlers.VillageProjectRequest{Action: village.ProjectActionUnpledge, ID: pledge}); kind != village.ProjectNoPledge {
		t.Fatalf("a gift cannot be taken back twice, got %q", kind)
	}
	// the work begins with the first material; from then the gifts are spent
	e.mustProject(d1, handlers.VillageProjectRequest{Action: village.ProjectActionPledge, ID: id, Amount: "700"})
	pledge2 := e.text(`SELECT id::text FROM project_pledges WHERE player_id = $1::uuid AND status = 'held'`, d1.ID)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionHire, ID: id, N: "8"}) // starts the stage
	if _, kind := e.project(d1, handlers.VillageProjectRequest{Action: village.ProjectActionUnpledge, ID: pledge2}); kind != village.ProjectSpent {
		t.Fatalf("a gift cannot be taken back once the work has begun, got %q", kind)
	}
	// a cancel before stage 2 returns what is left in proportion; no one but the holder of the power cancels
	if _, kind := e.project(d2, handlers.VillageProjectRequest{Action: village.ProjectActionCancel, ID: id}); kind != village.VillageNotOfficeHolder {
		t.Fatalf("a donor cannot cancel the work, got %q", kind)
	}
	fund := e.fundOf(id)
	cash1, cash2, treasury0 := e.cash(d1), e.cash(d2), e.cityCash(e.cityID)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionCancel, ID: id})
	back1, back2, backT := e.cash(d1)-cash1, e.cash(d2)-cash2, e.cityCash(e.cityID)-treasury0
	if back1+back2+backT != fund || e.fundOf(id) != 0 {
		t.Fatalf("the whole fund goes back: %d + %d + %d of %d, left %d", back1, back2, backT, fund, e.fundOf(id))
	}
	if !(back1 > back2 && back2 >= backT-1 && back1 > 0) {
		t.Fatalf("in proportion to what each gave (700, 500, 500): %d, %d, %d", back1, back2, backT)
	}
	if e.text(`SELECT status FROM special_projects WHERE id = $1::uuid`, id) != "cancelled" || e.scalarInt(`SELECT count(*) FROM project_pledges WHERE project_id = $1::uuid AND status = 'held'`, id) != 0 {
		t.Fatal("a cancelled work holds no gift")
	}
	if _, kind := e.project(d1, handlers.VillageProjectRequest{Action: village.ProjectActionPledge, ID: id, Amount: "200"}); kind != village.ProjectNotActive {
		t.Fatalf("nobody gives to a cancelled work, got %q", kind)
	}
	// from stage two a work cannot be cancelled
	id2 := e.startProject(e.head, "aqueduct")
	e.pool.Raw().Exec(testCtx(t), `UPDATE special_projects SET stage = 2 WHERE id = $1::uuid`, id2)
	if _, kind := e.project(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionCancel, ID: id2}); kind != village.ProjectTooLate {
		t.Fatalf("after the first stage it is too late, got %q", kind)
	}
	e.pool.Raw().Exec(testCtx(t), `UPDATE special_projects SET stage = 1 WHERE id = $1::uuid`, id2)
	e.verifySpecial()
}

func (e *specialEnv) finishedWork(city, kind string, stages int) string {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO special_projects (id, kind, settlement_id, started_by, stages, stage, status, started_at, finished_at)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $5, 'finished', now(), now())`, id, kind, city, e.head.ID, stages); err != nil {
		e.t.Fatal(err)
	}
	for k := 1; k <= stages; k++ {
		if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO project_stages (project_id, stage, hours, completed_at) VALUES ($1::uuid, $2, 10, now())`, id, k); err != nil {
			e.t.Fatal(err)
		}
	}
	return id
}

// lastDay is the settlement's last judged service day.
func (e *specialEnv) lastDay(city string) *application.ServiceDay {
	e.t.Helper()
	var d *application.ServiceDay
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		var err error
		d, err = tx.ServiceDays().Last(ctx, city)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *specialEnv) postOf(day *application.ServiceDay, id string) *application.ServicePost {
	if day == nil {
		return nil
	}
	for i := range day.Posts {
		if day.Posts[i].BuildingID == id {
			return &day.Posts[i]
		}
	}
	return nil
}

// A finished work is a post of the town like any other: its keepers are paid and its fuel burnt every day. With nobody to pay them or nothing to burn it stands
// idle - neglected, not destroyed - and opens again when the town can keep it. The great beacon is then a detector that sees.
func TestAFinishedWorkStandsIdleWhenNobodyKeepsItAndOpensWhenKept(t *testing.T) {
	e := newSpecialEnv(t, nil)
	work := e.finishedWork(e.cityID, "great_beacon", 4)
	e.drainTreasury()
	e.overview() // judges today
	post := e.postOf(e.lastDay(e.cityID), work)
	if post == nil || post.Held || post.Idle == "" {
		t.Fatalf("a work nobody pays or feeds stands idle: %+v", post)
	}
	if e.text(`SELECT status FROM special_projects WHERE id = $1::uuid`, work) != "finished" {
		t.Fatal("neglect never destroys a work")
	}
	// a day on, with wood in the store and money in the treasury, its keepers are paid and the fire is lit
	e.clock.Advance(48 * time.Hour)
	e.stock("firewood", 20)
	e.earn(e.cityID, 5000)
	treasury0 := e.treasury()
	e.overview()
	post = e.postOf(e.lastDay(e.cityID), work)
	if post == nil || !post.Held || post.Staff < 1 {
		t.Fatalf("with wood and a wage the work stands open: %+v", post)
	}
	if e.treasury() >= treasury0 {
		t.Fatal("the keepers' wage came out of the treasury")
	}
	if got := e.held("firewood"); got >= 20 {
		t.Fatalf("the fire burnt wood, %d left", got)
	}
	e.verifySpecial()
}

func (e *specialEnv) contractOf(project string) string {
	return e.text(`SELECT id::text FROM project_contracts WHERE project_id = $1::uuid ORDER BY opens_at DESC, id LIMIT 1`, project)
}

func (e *specialEnv) projectEscrow(contract string) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'project_escrow' AND owner_id = $1::uuid`, contract)
}

// bidOn puts a company's bid on a project's contract and returns the refusal, if any.
func (e *specialEnv) bidOn(owner *application.Player, contract, company string, price, days int64) string {
	_, kind := e.project(owner, handlers.VillageProjectRequest{Action: village.ProjectActionBid, ID: contract, Company: e.companyCode(company), Price: fmt.Sprint(price), Days: fmt.Sprint(days)})
	return kind
}

// readyStage puts everything a stage asks on the site by the head's hand except the last material, which the test delivers when it wants the stage looked at.
func (e *specialEnv) topUp(project string, hours int64) {
	e.t.Helper()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `UPDATE special_projects SET stage_hours = $2 WHERE id = $1::uuid`, project, hours); err != nil {
		e.t.Fatal(err)
	}
}

// A work let to a contractor: one winner whatever the number of replicas, an official's own company refused, the contractor's crew paid by its
// company, the price paid by the stages the project completes less a retention, and the bond and the retention returned when the defects period ends.
func TestAProjectLetByTenderHasOneWinnerAndPaysByItsStages(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.know(e.cityID, "masonry", "canal_irrigation", "masonry_ii")
	e.clock.Advance(4 * 24 * time.Hour)
	id := e.startProject(e.head, "aqueduct")
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionAllocate, ID: id, Amount: "6000"})
	owner1, c1 := e.contractor("A", 20_000)
	owner2, c2 := e.contractor("B", 20_000)
	own := e.companyFor(e.head, e.cityID, 20_000)

	treasury0 := e.cityCash(e.cityID)
	e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionTender, ID: id, Cap: "4000", Due: "3", Window: "24", Rule: "lowest_price"})
	if got := treasury0 - e.cityCash(e.cityID); got != e.rules.TenderFee {
		t.Fatalf("the filing fee of a tender is a sink: %d, fee %d", got, e.rules.TenderFee)
	}
	contract := e.contractOf(id)
	if kind := e.bidOn(e.head, contract, own, 2500, 2); kind != village.ProjectConflict {
		t.Fatalf("the one who awards cannot award to his own company, got %q", kind)
	}
	if kind := e.bidOn(owner1, contract, c1, 4500, 2); kind != village.ProjectBadBid {
		t.Fatalf("a bid over the cap is refused, got %q", kind)
	}
	if kind := e.bidOn(owner1, contract, c1, 3000, 2); kind != "" {
		t.Fatalf("a fair bid stands, got %q", kind)
	}
	if kind := e.bidOn(owner2, contract, c2, 2800, 2); kind != "" {
		t.Fatalf("a fair bid stands, got %q", kind)
	}
	if kind := e.bidOn(owner2, contract, c2, 2700, 2); kind != village.ProjectHaveBid {
		t.Fatalf("a company bids once, got %q", kind)
	}
	// while the bids are taken the crew waits for the winner
	w := e.resident("A")
	if _, kind := e.project(w, handlers.VillageProjectRequest{Action: village.ProjectActionWork, ID: id}); kind != village.ProjectHasContract {
		t.Fatalf("no shift while the work is being let, got %q", kind)
	}
	bond2 := e.projectEscrow(contract)
	if bond2 != 300+280 {
		t.Fatalf("the escrow holds the bonds of the standing bids, got %d", bond2)
	}
	e.clock.Advance(25 * time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.tickOf(application.TickContractClose, contract)
		}()
	}
	wg.Wait()
	if e.text(`SELECT status FROM project_contracts WHERE id = $1::uuid`, contract) != "awarded" || e.text(`SELECT company_id::text FROM project_contracts WHERE id = $1::uuid`, contract) != c2 {
		t.Fatal("the lowest price wins, and one winner stands however many replicas close the bids")
	}
	if got := e.projectEscrow(contract); got != 2800+280 {
		t.Fatalf("the escrow holds the price and the winner's bond, got %d", got)
	}
	if e.companyCash(c1) != 20_000 {
		t.Fatalf("the loser's bond is back, got %d", e.companyCash(c1))
	}
	if e.fundOf(id) != 6000-2800 {
		t.Fatalf("the fund paid the price into the escrow, got %d", e.fundOf(id))
	}
	// the crew of a let work is the contractor's: the settlement hires none, and only the company's own people work it
	if _, kind := e.project(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionHire, ID: id, N: "8"}); kind != village.ProjectNoCrew {
		t.Fatalf("the settlement does not hire for a let work, got %q", kind)
	}
	if _, kind := e.project(w, handlers.VillageProjectRequest{Action: village.ProjectActionWork, ID: id}); kind != village.ProjectNoCrew {
		t.Fatalf("a stranger does not work the contractor's site, got %q", kind)
	}
	company0 := e.companyCash(c2)
	// the contractor comes from another town: its owner goes to the site
	if _, kind := e.project(owner2, handlers.VillageProjectRequest{Action: village.ProjectActionWork, ID: id}); kind != village.ProjectNotHere {
		t.Fatalf("a crew works at the site, not from afar, got %q", kind)
	}
	e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $1::uuid WHERE id = $2::uuid`, e.cityID, owner2.ID)
	e.mustProject(owner2, handlers.VillageProjectRequest{Action: village.ProjectActionWork, ID: id})
	shift := e.text(`SELECT id::text FROM project_shifts WHERE player_id = $1::uuid`, owner2.ID)
	e.clock.Advance(time.Duration(e.special.Projects.ShiftMinutes+1) * time.Minute)
	e.tickOf(application.TickProjectShift, shift)
	wage := e.special.Projects.ShiftHours * e.special.Projects.WagePerHour
	if got := company0 - e.companyCash(c2); got != wage || e.cash(owner2) != wage {
		t.Fatalf("the company pays its own crew: it paid %d, the worker got %d, wage %d", got, e.cash(owner2), wage)
	}
	// the three stages: the materials from the store, the hours topped up; each stage completed pays the contractor its share less the retention
	stages := []struct {
		items [][2]string
		hours int64
	}{{[][2]string{{"stone", "40"}, {"clay", "20"}}, 32}, {[][2]string{{"stone", "50"}, {"brick", "20"}, {"clay", "20"}}, 40}, {[][2]string{{"stone", "40"}, {"clay", "30"}}, 32}}
	var paidTotal, heldTotal int64
	for k, st := range stages {
		e.topUp(id, st.hours)
		for _, it := range st.items {
			n := parseNum(it[1])
			e.putStock(e.cityID, it[0], n)
		}
		before := e.companyCash(c2)
		for i, it := range st.items {
			if i == len(st.items)-1 {
				e.clock.Advance(13 * time.Hour) // the least time of a stage
			}
			e.mustProject(e.head, handlers.VillageProjectRequest{Action: village.ProjectActionDeliver, ID: id, Code: it[0], N: it[1]})
		}
		stage := int64(k + 1)
		if got := e.scalarInt(`SELECT count(*) FROM project_stages WHERE project_id = $1::uuid`, id); got != stage {
			t.Fatalf("after delivering stage %d it is done (rows %d)", stage, got)
		}
		share := int64(2800) / 3
		if stage == 3 {
			share += 2800 % 3
		}
		held := (share*e.rules.RetentionBPS + 9999) / 10000
		if got := e.companyCash(c2) - before; got != share-held {
			t.Fatalf("stage %d pays its share less the retention: %d, want %d", stage, got, share-held)
		}
		paidTotal += share - held
		heldTotal += held
	}
	if e.text(`SELECT status FROM special_projects WHERE id = $1::uuid`, id) != "finished" {
		t.Fatal("the last stage finishes the work")
	}
	if got := e.projectEscrow(contract); got != heldTotal+280 {
		t.Fatalf("the escrow holds the retention and the bond until the defects period is over, got %d want %d", got, heldTotal+280)
	}
	e.tickOf(application.TickContractRelease, contract) // too early: nothing moves
	if e.text(`SELECT status FROM project_contracts WHERE id = $1::uuid`, contract) != "awarded" {
		t.Fatal("the retention is not released before the defects period ends")
	}
	e.clock.Advance(time.Duration(e.rules.DefectsDays+1) * 24 * time.Hour)
	e.tickOf(application.TickContractRelease, contract)
	e.tickOf(application.TickContractRelease, contract)
	if e.text(`SELECT status FROM project_contracts WHERE id = $1::uuid`, contract) != "done" || e.projectEscrow(contract) != 0 {
		t.Fatal("the contract ends done with an empty escrow")
	}
	if got := e.companyCash(c2); got != 20_000-wage+2800 {
		t.Fatalf("the company ends with the price, less its crew's wage: %d", got)
	}
	e.verifySpecial()
}

func parseNum(s string) int64 {
	var n int64
	for _, r := range s {
		n = n*10 + int64(r-'0')
	}
	return n
}
