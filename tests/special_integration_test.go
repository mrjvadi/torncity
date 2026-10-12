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
	return e.scalarInt(`SELECT COALESCE(sum(qty), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2 AND holding = 'warehouse'`, city, item)
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
