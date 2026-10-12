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
		`ALTER TABLE charter_audit DISABLE TRIGGER charter_audit_no_change`,
		`DELETE FROM charter_audit WHERE settlement_id IN ($1::uuid, $2::uuid, $3::uuid)`,
		`ALTER TABLE charter_audit ENABLE TRIGGER charter_audit_no_change`,
		`DELETE FROM charter_seats WHERE office_id IN (SELECT id FROM charter_offices WHERE settlement_id IN ($1::uuid, $2::uuid, $3::uuid) AND acquisition <> 'head')`,
		`DELETE FROM charter_offices WHERE settlement_id IN ($1::uuid, $2::uuid, $3::uuid) AND acquisition <> 'head'`,
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

// retune changes the rules the handler runs by in the middle of a test.
func (e *specialEnv) retune(f func(*application.SpecialRules)) {
	f(&e.special)
	e.village.WithSpecial(e.special)
}

// agentOf sets up a bureau of A, a player of B who works for it, and the offer taken: it returns the player and the agent's id.
func (e *specialEnv) agentOf() (*application.Player, string) {
	e.t.Helper()
	e.know(e.cityID, "field_craft", "subversion_craft", "record_keeping", "communal_watch")
	if e.scalarInt(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'intelligence_office'`, e.cityID) == 0 {
		e.put(e.cityID, "intelligence_office")
	}
	e.stock("paper", 50)
	e.earn(e.cityID, 5000)
	agent := e.resident("B")
	e.mustIntel(e.head, handlers.VillageIntelRequest{Action: village.IntelActionEngage, Player: agent.PublicCode, Code: e.codeOf(e.attCity)})
	id := e.text(`SELECT id::text FROM intel_agents WHERE player_id = $1::uuid AND status = 'offered'`, agent.ID)
	e.mustIntel(agent, handlers.VillageIntelRequest{Action: village.IntelActionAccept, ID: id})
	return agent, id
}

// surer makes every operation succeed or fail as the test wants: the chance is pinned.
func (e *specialEnv) pinChance(bps int64) {
	e.retune(func(r *application.SpecialRules) {
		r.Intel.BaseChanceBPS, r.Intel.ChanceFloorBPS, r.Intel.ChanceCapBPS = bps, bps, bps
	})
}

// watchOf records a judged day of a settlement with one open watch post, so the target has someone to notice a stranger.
func (e *specialEnv) watchOf(city string) string {
	e.t.Helper()
	post := e.text(`SELECT COALESCE((SELECT id::text FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'lookout_post' LIMIT 1), '')`, city)
	if post == "" {
		post = e.put(city, "lookout_post")
	}
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		s, err := tx.Settlements().ByID(ctx, city)
		if err != nil {
			return err
		}
		today := clockOf(e.t).DayAtIn(e.clock.Now(), s.Zone())
		for off := int64(-3); off <= 3; off++ {
			// a day already judged stays as it is (the row is the fence)
			if _, err := tx.ServiceDays().RecordDay(ctx, application.ServiceDay{SettlementID: city, Day: today + off, At: e.clock.Now(),
				Posts: []application.ServicePost{{BuildingID: post, Service: "local_security", Held: true, Staff: 1}}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return post
}

// A survey brings back bands and nothing else: no figure, no name, a confidence and an age; the bureau's fee and the agent's wage are paid whatever
// the day brought, and no report stands without an operation behind it.
func TestAReportHoldsBandNamesAndNeverAFigureOrAPerson(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.pinChance(10_000)
	agent, agentID := e.agentOf()
	// the target's money is known exactly to the engine: 60 000 and a few hundred more; the report must not say so
	treasuryB := e.cityCash(e.attCity)
	e.putStock(e.attCity, "wheat", 777)
	treasuryA := e.cityCash(e.cityID)
	cash0 := e.cash(agent)
	v := e.mustIntel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_stock"})
	if v.Notice != village.IntelNoticeClean && v.Notice != village.IntelNoticePartial {
		t.Fatalf("the day should bring a report, the notice was %q", v.Notice)
	}
	if got := treasuryA - e.cityCash(e.cityID); got != e.special.Intel.OpFee+e.special.Intel.AgentWage {
		t.Fatalf("the bureau pays its fee to the sink and the agent's wage: %d, fee %d wage %d", got, e.special.Intel.OpFee, e.special.Intel.AgentWage)
	}
	if got := e.cash(agent) - cash0; got != e.special.Intel.AgentWage {
		t.Fatalf("the agent is paid, got %d", got)
	}
	// the sponsor's reader sees bands
	iv := e.mustIntel(e.head, handlers.VillageIntelRequest{Action: village.IntelActionOpen})
	if len(iv.Reports) != 1 {
		t.Fatalf("one report, got %d", len(iv.Reports))
	}
	r := iv.Reports[0]
	if r.Target.Code != e.codeOf(e.attCity) || r.Confidence == "" || len(r.Fields) != 3 {
		t.Fatalf("the report names the target, its confidence and three metrics: %+v", r)
	}
	for _, f := range r.Fields {
		for _, ch := range f.Band + f.Metric {
			if ch >= '0' && ch <= '9' {
				t.Fatalf("a report field holds a figure: %+v", f)
			}
		}
	}
	raw := e.text(`SELECT fields::text FROM intel_reports LIMIT 1`)
	for _, secret := range []string{fmt.Sprint(treasuryB), "777", agent.PublicCode, e.head.PublicCode} {
		if strings.Contains(raw, secret) {
			t.Fatalf("the stored report %s gives away %s", raw, secret)
		}
	}
	// the day's limit: the same operation on the same target is done once a day
	if _, kind := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_stock"}); kind != village.IntelDone {
		t.Fatalf("an operation is done once a day, got %q", kind)
	}
	// a stranger's agent cannot read: the target has no report of its own and sees none of the sponsor's
	if tv := e.mustIntel(e.att, handlers.VillageIntelRequest{Action: village.IntelActionOpen}); len(tv.Reports) != 0 {
		t.Fatalf("the target sees no report about itself, got %d", len(tv.Reports))
	}
	e.verifySpecial()
}

// An agent has to live in the target: cover is a real residence. A bureau that is closed for the day sends nobody out. Without the research
// the operation is refused too.
func TestAnAgentNeedsRealCoverAnOpenBureauAndTheResearch(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.pinChance(10_000)
	agent, agentID := e.agentOf()
	// the agent moves house: no cover in the target any more
	e.pool.Raw().Exec(testCtx(t), `UPDATE players SET residence_city_id = $1::uuid WHERE id = $2::uuid`, e.cityC, agent.ID)
	if _, kind := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_stock"}); kind != village.IntelNoCover {
		t.Fatalf("an agent living elsewhere has no cover, got %q", kind)
	}
	e.pool.Raw().Exec(testCtx(t), `UPDATE players SET residence_city_id = $1::uuid WHERE id = $2::uuid`, e.attCity, agent.ID)
	// the bureau runs out of paper: closed for the day
	e.drainStock("paper")
	e.clock.Advance(48 * time.Hour)
	if _, kind := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_stock"}); kind != village.IntelBureauClosed {
		t.Fatalf("a bureau without paper stands closed, got %q", kind)
	}
	e.stock("paper", 20)
	e.earn(e.cityID, 5000)
	e.clock.Advance(48 * time.Hour)
	// a sponsor that has not researched what the operation asks cannot send it
	e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid AND code = 'field_craft'`, e.cityID)
	if _, kind := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_stock"}); kind != village.IntelResearch {
		t.Fatalf("no research, no operation, got %q", kind)
	}
	e.know(e.cityID, "field_craft")
	if _, kind := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_stock"}); kind != "" {
		t.Fatalf("with cover, an open bureau and the research the day works, got %q", kind)
	}
	e.verifySpecial()
}

func (e *specialEnv) drainStock(item string) {
	e.t.Helper()
	n := e.stockOf(e.cityID, item)
	if n == 0 {
		return
	}
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: n, FromOrg: application.SettlementOrg(e.cityID), FromHolding: application.HoldWarehouse,
			Reason: application.ItemUsed, ReferenceType: "test", ReferenceID: newUUID(e.t), At: time.Now().UTC()})
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// A blown operation tests the target's open watch posts, one seeded roll for each post and operation; a post that notices puts the agent out of the
// settlement for a time, with nothing of theirs lost. The sponsor is named to the target only if the trace rolls true.
func TestABlownOperationIsNoticedByTheWatchAndPutsTheAgentOut(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.pinChance(0) // every operation is blown
	e.retune(func(r *application.SpecialRules) { r.Intel.DetectBPS, r.Intel.AttributionBPS = 10_000, 0 })
	agent, agentID := e.agentOf()
	post := e.watchOf(e.attCity)
	cash0 := e.cash(agent)
	home := e.text(`SELECT residence_city_id::text FROM players WHERE id = $1::uuid`, agent.ID)
	if home != e.attCity {
		t.Fatal("the agent lives in the target")
	}
	v := e.mustIntel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_stock"})
	if v.Notice != village.IntelNoticeExposed {
		t.Fatalf("a watch that sees a blown operation exposes the agent, the notice was %q", v.Notice)
	}
	if e.scalarInt(`SELECT count(*) FROM intel_reports`) != 0 {
		t.Fatal("a blown operation brings no report")
	}
	if e.scalarInt(`SELECT count(*) FROM intel_exposures WHERE detector_id = $1::uuid AND detected`, post) != 1 {
		t.Fatal("the post's roll is written once")
	}
	if e.text(`SELECT status FROM intel_agents WHERE id = $1::uuid`, agentID) != "expelled" {
		t.Fatal("the agent is put out")
	}
	until := e.text(`SELECT until::text FROM intel_expulsions WHERE agent_id = $1::uuid`, agentID)
	if until == "" || e.scalarInt(`SELECT count(*) FROM intel_expulsions WHERE agent_id = $1::uuid AND NOT attributed`, agentID) != 1 {
		t.Fatal("the stay is recorded and, the trace failing, the sponsor is not named")
	}
	if e.cash(agent) < cash0 {
		t.Fatalf("nothing the agent has is taken: %d -> %d", cash0, e.cash(agent))
	}
	// the target sees that someone was put out, not who sent them
	tv := e.mustIntel(e.att, handlers.VillageIntelRequest{Action: village.IntelActionOpen})
	if len(tv.Expelled) != 1 || tv.Expelled[0].Sponsor != nil {
		t.Fatalf("the target sees the expulsion and no sponsor: %+v", tv.Expelled)
	}
	// the operation is not repeated for the same agent: the agent is out
	if _, kind := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: agentID, Code: "survey_garrison"}); kind != village.IntelNoAgent {
		t.Fatalf("an agent put out works no more, got %q", kind)
	}
	// the residence in the target ended: they were sent to the neutral city, with their money
	if now := e.text(`SELECT COALESCE(residence_city_id::text, '') FROM players WHERE id = $1::uuid`, agent.ID); now == e.attCity {
		t.Fatalf("the agent's residence in the target ended, still %s", now)
	}
	e.pool.Raw().Exec(testCtx(t), `UPDATE players SET residence_city_id = $1::uuid WHERE id = $2::uuid`, e.attCity, agent.ID)
	// a new offer to the same person: while the stay lasts the cover is void
	e.mustIntel(e.head, handlers.VillageIntelRequest{Action: village.IntelActionEngage, Player: agent.PublicCode, Code: e.codeOf(e.attCity)})
	again := e.text(`SELECT id::text FROM intel_agents WHERE player_id = $1::uuid AND status = 'offered'`, agent.ID)
	e.mustIntel(agent, handlers.VillageIntelRequest{Action: village.IntelActionAccept, ID: again})
	if _, kind := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: again, Code: "survey_garrison"}); kind != village.IntelExpelled {
		t.Fatalf("an agent put out may not work there again until the stay is over, got %q", kind)
	}
	// with a trace that always lands, the target is told whose agent it was
	e.retune(func(r *application.SpecialRules) { r.Intel.AttributionBPS = 10_000 })
	e.clock.Advance(time.Duration(e.special.Intel.ExpelDays+1) * 24 * time.Hour)
	e.watchOf(e.attCity) // before anyone looks at the new day of the target
	e.mustIntel(e.head, handlers.VillageIntelRequest{Action: village.IntelActionRecall, ID: again})
	e.mustIntel(e.head, handlers.VillageIntelRequest{Action: village.IntelActionEngage, Player: agent.PublicCode, Code: e.codeOf(e.attCity)})
	third := e.text(`SELECT id::text FROM intel_agents WHERE player_id = $1::uuid AND status = 'offered'`, agent.ID)
	e.mustIntel(agent, handlers.VillageIntelRequest{Action: village.IntelActionAccept, ID: third})
	e.stock("paper", 20)
	e.earn(e.cityID, 5000)
	e.pool.Raw().Exec(testCtx(t), `UPDATE players SET residence_city_id = $1::uuid WHERE id = $2::uuid`, e.attCity, agent.ID)
	if v := e.mustIntel(agent, handlers.VillageIntelRequest{Action: village.IntelActionOp, ID: third, Code: "survey_stock"}); v.Notice != village.IntelNoticeExposed {
		t.Fatalf("the second exposure, got %q", v.Notice)
	}
	tv = e.mustIntel(e.att, handlers.VillageIntelRequest{Action: village.IntelActionOpen})
	named := 0
	for _, x := range tv.Expelled {
		if x.Sponsor != nil && x.Sponsor.Code == e.codeOf(e.cityID) {
			named++
		}
	}
	if named != 1 {
		t.Fatalf("when the trace lands the target is told whose agent it was: %+v", tv.Expelled)
	}
	e.verifySpecial()
}

// Sabotage is small, capped and written down: twelve hours of delay at the most, five percent of one stack, two acts in a week, one of a kind on one
// target in a week, never in a town still in its founding grace, and it can reach only a work, a stack in the store, a watch post or a service
// post of the target - never a building, a house or a person.
func TestSabotageIsCappedAndNeverTouchesBuildingsPeopleOrHouses(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.pinChance(10_000)
	agent, agentID := e.agentOf()
	// B: a work in its first stage, a store of wheat, a lookout, and a house
	work := newUUID(t)
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO special_projects (id, kind, settlement_id, started_by, stages, stage, status, started_at) VALUES ($1::uuid, 'aqueduct', $2::uuid, $3::uuid, 3, 1, 'active', now())`,
		work, e.attCity, e.att.ID); err != nil {
		t.Fatal(err)
	}
	e.putStock(e.attCity, "wheat", 100)
	lookout := e.watchOf(e.attCity)
	house := e.put(e.attCity, "house")
	buildings0 := e.scalarInt(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid`, e.attCity)
	people0 := e.scalarInt(`SELECT count(*) FROM players WHERE residence_city_id = $1::uuid`, e.attCity)
	sab := func(kind, ref string) string {
		_, k := e.intel(agent, handlers.VillageIntelRequest{Action: village.IntelActionSab, ID: agentID, Code: kind, Ref: ref})
		return k
	}
	// the things sabotage may not reach
	for _, c := range []struct{ kind, ref string }{
		{"blind_post", house},          // a house is not a watch post
		{"skip_service_day", house},    // nor a service post
		{"delay_work", house},          // nor a work
		{"delay_work", newUUID(t)},     // a work that is not there
		{"spoil_stock", "plutonium"},   // a stack the store does not hold
		{"burn_down", house},           // there is no such act
	} {
		if kind := sab(c.kind, c.ref); kind == "" {
			t.Fatalf("%s on %s was done, it must not be", c.kind, c.ref)
		}
	}
	// a town in its founding grace is out of reach
	e.pool.Raw().Exec(testCtx(t), `UPDATE cities SET protected_until = now() + interval '30 days' WHERE id = $1::uuid`, e.attCity)
	if kind := sab("delay_work", work); kind != village.IntelSabRefused+"_protected" {
		t.Fatalf("a town in its grace is protected, got %q", kind)
	}
	e.pool.Raw().Exec(testCtx(t), `UPDATE cities SET protected_until = now() - interval '1 day' WHERE id = $1::uuid`, e.attCity)
	treasuryA := e.cityCash(e.cityID)
	// the first act: a delay of twelve hours at the most
	if kind := sab("delay_work", work); kind != "" {
		t.Fatalf("delaying a work is allowed, got %q", kind)
	}
	var delayed time.Time
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT delayed_until FROM special_projects WHERE id = $1::uuid`, work).Scan(&delayed); err != nil {
		t.Fatal(err)
	}
	if delayed.After(e.clock.Now().Add(time.Duration(e.special.Intel.SabotageDelayHours)*time.Hour + time.Second)) {
		t.Fatalf("a delay is at most %d hours, got until %v", e.special.Intel.SabotageDelayHours, delayed)
	}
	if got := treasuryA - e.cityCash(e.cityID); got != e.special.Intel.SabotageCost+e.special.Intel.AgentWage {
		t.Fatalf("an act costs the sponsor its price and the agent's wage: %d", got)
	}
	// the same act on the same work again: too soon
	if kind := sab("delay_work", work); kind != village.IntelSabRefused+"_too_soon" {
		t.Fatalf("one delay of a work a week, got %q", kind)
	}
	// the second act of the week: five percent of one stack, no more
	if kind := sab("spoil_stock", "wheat"); kind != "" {
		t.Fatalf("spoiling a stack is allowed, got %q", kind)
	}
	if got := e.stockOf(e.attCity, "wheat"); got != 95 {
		t.Fatalf("five percent of 100 is five, %d left", got)
	}
	// the third act of the week: the limit
	if kind := sab("blind_post", lookout); kind != village.IntelSabRefused+"_weekly_limit" {
		t.Fatalf("two acts in a week at the most, got %q", kind)
	}
	// nothing was destroyed, nobody was touched, and every act says how it is mended
	if got := e.scalarInt(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid`, e.attCity); got != buildings0 {
		t.Fatalf("no building is destroyed: %d -> %d", buildings0, got)
	}
	if got := e.scalarInt(`SELECT count(*) FROM players WHERE residence_city_id = $1::uuid`, e.attCity); got != people0 {
		t.Fatalf("nobody is touched: %d -> %d", people0, got)
	}
	if e.text(`SELECT status FROM special_projects WHERE id = $1::uuid`, work) != "active" || e.scalarInt(`SELECT stage FROM special_projects WHERE id = $1::uuid`, work) != 1 {
		t.Fatal("the work is slowed, not undone")
	}
	if e.scalarInt(`SELECT count(*) FROM sabotage_acts WHERE repair NOT IN ('passes', 'replace', 'recovers', 'catch_up')`) != 0 || e.scalarInt(`SELECT count(*) FROM sabotage_acts`) != 2 {
		t.Fatal("each act is written with its repair")
	}
	// a week on: the lookout is blinded for a day and its chance falls; a service post is skipped for a day
	e.clock.Advance(8 * 24 * time.Hour)
	e.stock("paper", 20)
	e.earn(e.cityID, 5000)
	if kind := sab("blind_post", lookout); kind != "" {
		t.Fatalf("a week on the limit is lifted, got %q", kind)
	}
	var blind int
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT bps FROM sabotage_effects WHERE building_id = $1::uuid AND kind = 'blind_post'`, lookout).Scan(&blind); err != nil || blind != int(e.special.Intel.SabotageBlindBPS) {
		t.Fatalf("the lookout is blinded by %d bps, got %d (%v)", e.special.Intel.SabotageBlindBPS, blind, err)
	}
	// and a service post skipped for a day: the day is judged with the post shut, and it costs the town no wage and no wood that day
	if kind := sab("skip_service_day", lookout); kind != "" {
		t.Fatalf("skipping a day of a service post is allowed, got %q", kind)
	}
	e.clock.Advance(26 * time.Hour) // today was judged already (the act itself looked at the watch): the skip falls on the next day
	e.mustIntel(e.att, handlers.VillageIntelRequest{Action: village.IntelActionOpen}) // judges B's day
	if post := e.postOf(e.lastDay(e.attCity), lookout); post == nil || post.Held || post.Idle != application.ServiceIdleSabotaged {
		t.Fatalf("the skipped day finds the post shut by sabotage: %+v", post)
	}
	e.verifySpecial()
	v, _ := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(t), 10)
	if v.VillageInvariants.SabotageMaxWeek > e.special.Intel.SabotageWeeklyLimit {
		t.Fatalf("no sponsor did more than the limit in a week: %d", v.VillageInvariants.SabotageMaxWeek)
	}
}

func (e *specialEnv) setKind(p *application.Player, code, mode string, fee, reserve, termDays int64, transferable bool) string {
	e.t.Helper()
	tr := ""
	if transferable {
		tr = "1"
	}
	_, kind := e.licence(p, handlers.VillageLicenceRequest{Action: village.LicenceActionSetKind, Code: code, Mode: mode, Fee: fmt.Sprint(fee), Reserve: fmt.Sprint(reserve), Term: fmt.Sprint(termDays), Transferable: tr})
	return kind
}

func (e *specialEnv) escrowOf(kind string) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = $1`, kind)
}

// resident with cash, in settlement A.
func (e *specialEnv) rich(place string, cash int64) *application.Player {
	e.t.Helper()
	p := e.resident(place)
	grant(e.t, e.pool, application.AccountPlayerCash, p.ID, cash)
	return p
}

// appointRevoker gives a second player the power to revoke licences, through the charter as a player would.
func (e *specialEnv) appointWith(perms []string, who *application.Player) {
	e.t.Helper()
	var gs []handlers.VillageCharterGrant
	for _, p := range perms {
		gs = append(gs, handlers.VillageCharterGrant{Permission: p})
	}
	title := "Clerk " + randomToken(e.t, 4)
	resp, err := e.village.CharterOfficeSave(testCtx(e.t), e.metaOf(e.head, "settlement.charter.office.save", "save"), handlers.VillageCharterRequest{Title: title, Seats: 1, Grants: gs, Acquisition: "appointment"})
	if err != nil || (resp != nil && resp.Screen == village.ScreenVillageRefusal) {
		e.t.Fatalf("creating the office: %v %v", err, resp)
	}
	office := e.text(`SELECT id::text FROM charter_offices WHERE settlement_id = $1::uuid AND title = $2`, e.cityID, title)
	resp, err = e.village.CharterAppoint(testCtx(e.t), e.metaOf(e.head, "settlement.charter.appoint", "appoint"), handlers.VillageCharterRequest{Office: office, Player: who.PublicCode})
	if err != nil || (resp != nil && resp.Screen == village.ScreenVillageRefusal) {
		e.t.Fatalf("appointing: %v %v", err, resp)
	}
}

// A settlement licenses nothing by default. Once its charter creates the requirement the people already at work have a grace in which they take
// the first grant at the base price; after it, a fixed-fee kind is asked for by application and decided by an official who may not grant to himself
// or to a firm he stands behind, and the fee is held until the decision.
func TestALicenceIsRequiredAfterAGraceAndTheIncumbentsTakeTheFirstGrant(t *testing.T) {
	e := newSpecialEnv(t, nil)
	e.know(e.cityID, "record_keeping", "craft_guild_charter")
	// nothing is licensed until the charter says so
	if _, kind := e.licence(e.rich("A", 5000), handlers.VillageLicenceRequest{Action: village.LicenceActionClaim, Code: "stall_right"}); kind != village.LicenceNoRule {
		t.Fatalf("with no requirement there is nothing to claim, got %q", kind)
	}
	if kind := e.setKind(e.head, "stall_right", "auction", 0, 500, 30, true); kind != "" {
		t.Fatalf("the head sets the requirement, got %q", kind)
	}
	if kind := e.setKind(e.resident("A"), "stall_right", "auction", 0, 500, 30, true); kind != village.VillageNotOfficeHolder {
		t.Fatalf("a resident without the power cannot, got %q", kind)
	}
	if kind := e.setKind(e.head, "stall_right", "auction", 0, 50, 30, true); kind != village.LicenceReserveLow {
		t.Fatalf("a reserve under the least is refused, got %q", kind)
	}
	e.know(e.attCity, "record_keeping")
	if _, kind := e.licence(e.att, handlers.VillageLicenceRequest{Action: village.LicenceActionSetKind, Code: "craft_privilege", Mode: "fixed", Fee: "100", Term: "30"}); kind != village.LicenceResearch {
		t.Fatalf("a town without the research cannot license a craft, got %q", kind)
	}
	// the first grant in the grace: the incumbent pays the base price, no auction
	r1 := e.rich("A", 5000)
	treasury0 := e.cityCash(e.cityID)
	e.mustLicence(r1, handlers.VillageLicenceRequest{Action: village.LicenceActionClaim, Code: "stall_right"})
	if got := e.cityCash(e.cityID) - treasury0; got != 500 || e.cash(r1) != 4500 {
		t.Fatalf("the incumbent paid the reserve to the treasury: treasury %+d, cash %d", got, e.cash(r1))
	}
	if e.text(`SELECT via FROM licences WHERE holder_id = $1::uuid`, r1.ID) != "first_grant" {
		t.Fatal("the grant is a first grant")
	}
	if _, kind := e.licence(e.head, handlers.VillageLicenceRequest{Action: village.LicenceActionAuction, Code: "stall_right"}); kind != village.LicenceInGrace {
		t.Fatalf("no auction while the incumbents have their grace, got %q", kind)
	}
	// a craft by application at a posted fee
	if kind := e.setKind(e.head, "craft_privilege", "fixed", 200, 0, 30, false); kind != "" {
		t.Fatalf("a craft requirement, got %q", kind)
	}
	r2, r3 := e.rich("A", 5000), e.rich("A", 5000)
	e.mustLicence(r2, handlers.VillageLicenceRequest{Action: village.LicenceActionApply, Code: "craft_privilege", Target: "smithing"})
	if got := e.escrowOf("licence_escrow"); got != 200 || e.cash(r2) != 4800 {
		t.Fatalf("the fee is held until an official decides: escrow %d, cash %d", got, e.cash(r2))
	}
	if _, kind := e.licence(r2, handlers.VillageLicenceRequest{Action: village.LicenceActionApply, Code: "craft_privilege", Target: "smithing"}); kind != village.LicenceHave {
		t.Fatalf("one open application for a right, got %q", kind)
	}
	// an official's own application is refused, and so is a firm he stands behind
	grant(t, e.pool, application.AccountPlayerCash, e.head.ID, 1000)
	e.mustLicence(e.head, handlers.VillageLicenceRequest{Action: village.LicenceActionApply, Code: "craft_privilege", Target: "weaving"})
	apps := e.mustLicence(e.head, handlers.VillageLicenceRequest{Action: village.LicenceActionOpen}).Applications
	var smithing, weaving string
	for _, a := range apps {
		if a.Target == "smithing" {
			smithing = a.ID
		}
		if a.Target == "weaving" {
			weaving = a.ID
		}
	}
	if _, kind := e.licence(e.head, handlers.VillageLicenceRequest{Action: village.LicenceActionIssue, ID: weaving}); kind != village.LicenceConflict {
		t.Fatalf("an official does not grant to himself, got %q", kind)
	}
	if _, kind := e.licence(r2, handlers.VillageLicenceRequest{Action: village.LicenceActionIssue, ID: smithing}); kind != village.VillageNotOfficeHolder {
		t.Fatalf("only the holder of licence.issue decides, got %q", kind)
	}
	e.mustLicence(e.head, handlers.VillageLicenceRequest{Action: village.LicenceActionIssue, ID: smithing})
	if e.text(`SELECT target_ref FROM licences WHERE holder_id = $1::uuid`, r2.ID) != "smithing" || e.escrowOf("licence_escrow") != 200 /* the head's own fee is still held */ {
		t.Fatalf("the right is granted and its fee left the escrow: escrow %d", e.escrowOf("licence_escrow"))
	}
	if _, kind := e.licence(r3, handlers.VillageLicenceRequest{Action: village.LicenceActionApply, Code: "craft_privilege", Target: "smithing"}); kind != village.LicenceTaken {
		t.Fatalf("an exclusive right has one holder, got %q", kind)
	}
	// the head withdraws his own application: the fee comes back; a refused application's fee comes back too
	e.mustLicence(e.head, handlers.VillageLicenceRequest{Action: village.LicenceActionWithdraw, ID: weaving})
	e.mustLicence(r3, handlers.VillageLicenceRequest{Action: village.LicenceActionApply, Code: "craft_privilege", Target: "weaving"})
	cash3 := e.cash(r3)
	appW := e.text(`SELECT id::text FROM licence_applications WHERE holder_id = $1::uuid AND status = 'pending'`, r3.ID)
	e.mustLicence(e.head, handlers.VillageLicenceRequest{Action: village.LicenceActionDecline, ID: appW})
	if e.cash(r3) != cash3+200 || e.escrowOf("licence_escrow") != 0 {
		t.Fatalf("a refused application gets its fee back: cash %d, escrow %d", e.cash(r3)-cash3, e.escrowOf("licence_escrow"))
	}
	// after the grace the first grant is over
	e.clock.Advance(15 * 24 * time.Hour)
	if _, kind := e.licence(e.rich("A", 5000), handlers.VillageLicenceRequest{Action: village.LicenceActionClaim, Code: "stall_right"}); kind != village.LicenceGraceOver {
		t.Fatalf("after the grace the claim is over, got %q", kind)
	}
	e.verifySpecial()
}
