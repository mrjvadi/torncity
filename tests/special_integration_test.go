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
	for _, id := range owners {
		purgeLedgerFor(e.t, e.pool, id)
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
	return e.text(`SELECT id::text FROM special_projects WHERE kind = $1 ORDER BY started_at DESC LIMIT 1`, kind)
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
