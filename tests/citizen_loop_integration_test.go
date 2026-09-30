//go:build integration

// Integration test of the citizen loop (docs/adr/0033 sections 4.4-4.5,
// migration 0058): a resident buys a lot from the village (the price goes to
// the village treasury), builds a private house on it from their own cash
// (permit to the treasury, materials from the pocket or bought), lives in it,
// and pays the property tax; a non-resident, a lot that is not theirs and the
// head's own placement are all refused; `admin economy verify` proves the
// ledger and the journal rows agree.
package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func TestCitizenLoop(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool
	admin := postgres.NewEconomyAdmin(pool)
	v0, err := admin.VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Citizen {
		t.Skip("migration 0058 is not applied")
	}

	e.h.WithFoundingGrant(10_000)
	metaA, head := e.group(t)
	foundVillage(t, pool, e.h, metaA)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaA.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { purgeCitizenFootprint(t, pool, cityID) })

	rules := handlers.CitizenRules{
		LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000,
		TaxBPS: 200, TaxBPSMax: 500, TaxPeriod: 24 * time.Hour, MaterialMarkupBPS: 12_000,
		MaxLotsPerPlayer: 3, PrivateShareMaxBPS: 6000, HomeRestCooldown: 6 * time.Hour, HomeRestHealth: 10, HomeRestHappiness: 5,
	}
	snap := loadTestContent(t)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, nil, staticContentSource{snap: snap}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now).WithDonationRules(100, 100_000, []int64{250}).WithCitizenRules(rules)

	// Three people who are not the head: a resident who builds, a resident
	// who owns nothing, and a stranger who lives in Support.
	support := cityIDByCode(t, pool, "support")
	resident, neighbour, stranger := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	for _, p := range []*application.Player{resident, neighbour, stranger} {
		placePlayer(t, pool, p.ID, support, "")
	}
	group := func(p *application.Player, command string) envelope.Metadata {
		m := asPlayer(metaA, p)
		m.Command = command
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	// A game client's own private channel: the answers carry the structured view.
	client := func(p *application.Player, command string) envelope.Metadata {
		m := clientMeta(asPlayer(metaA, p), command, command)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	for _, p := range []*application.Player{resident, neighbour} {
		if _, err := village.Join(ctx, group(p, "settlement.join"), handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm}); err != nil {
			t.Fatal(err)
		}
	}
	grantCash(t, pool, resident.ID, 5_000)
	grantCash(t, pool, neighbour.ID, 5_000)
	grantCash(t, pool, stranger.ID, 5_000)
	cashOfPlayer := func(p *application.Player) int64 {
		var bal int64
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
			if err != nil {
				return err
			}
			b, err := tx.Ledger().Balance(ctx, acct.ID)
			bal = b.Minor()
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return bal
	}
	countRows := func(query string, args ...any) int { return e.count(t, query, args...) }

	// ---- the land: free lots are on offer ---------------------------------
	land, err := village.Land(ctx, client(resident, "settlement.land"))
	if err != nil {
		t.Fatal(err)
	}
	var free []string
	for _, row := range viewOf(t, land)["rows"].([]any) {
		for _, c := range row.([]any) {
			cell := c.(map[string]any)
			if cell["state"] == screens.LandFree {
				free = append(free, screens.LotToken(int(cell["x"].(float64)), int(cell["y"].(float64)), false))
			}
		}
	}
	if len(free) < 5 {
		t.Skipf("the test village has only %d free lots", len(free))
	}
	lotA, lotB, lotC, lotD := free[0], free[1], free[2], free[3]

	// ---- a non-resident cannot buy ----------------------------------------
	if r, err := village.BuyLot(ctx, client(stranger, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: lotA, Confirm: screens.ResidenceConfirm}); err != nil ||
		!strings.Contains(r.Text, "village.refusal.not_resident") && !strings.Contains(r.Text, "village.refusal.no_settlement") {
		t.Fatalf("a stranger bought a lot: %+v %v", r, err)
	}
	if n := countRows(`SELECT count(*) FROM settlement_lots WHERE settlement_id = $1::uuid`, cityID); n != 0 {
		t.Fatalf("a refused purchase left %d lots", n)
	}

	// ---- buy a lot: the price goes to the treasury ------------------------
	treasury0, cash0 := treasuryOf(t, pool, cityID), cashOfPlayer(resident)
	ask, err := village.BuyLot(ctx, client(resident, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: lotA})
	if err != nil || !strings.Contains(ask.Text, "citizen.buy.ask_title") {
		t.Fatalf("asking to buy: %+v %v", ask, err)
	}
	if treasuryOf(t, pool, cityID) != treasury0 || cashOfPlayer(resident) != cash0 {
		t.Fatal("asking to buy moved money")
	}
	done, err := village.BuyLot(ctx, client(resident, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: lotA, Confirm: screens.ResidenceConfirm})
	if err != nil || !strings.Contains(done.Text, "citizen.buy.done_title") {
		t.Fatalf("buying: %+v %v", done, err)
	}
	if got := treasuryOf(t, pool, cityID); got != treasury0+400 {
		t.Fatalf("the treasury has %d, want %d: the lot price must land in it", got, treasury0+400)
	}
	if got := cashOfPlayer(resident); got != cash0-400 {
		t.Fatalf("the buyer has %d, want %d", got, cash0-400)
	}
	if n := countRows(`SELECT count(*) FROM settlement_lots WHERE settlement_id = $1::uuid AND owner_id = $2::uuid`, cityID, resident.ID); n != 1 {
		t.Fatalf("%d lots recorded for the buyer", n)
	}
	// The same lot again, by anyone: taken. A repeated confirm changes nothing.
	if r, err := village.BuyLot(ctx, client(neighbour, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: lotA, Confirm: screens.ResidenceConfirm}); err != nil ||
		!strings.Contains(r.Text, "citizen.refusal.citizen_lot_taken") {
		t.Fatalf("a taken lot was sold twice: %+v %v", r, err)
	}
	// The per-player limit (3 in this test).
	for _, lot := range []string{lotB, lotC} {
		if r, err := village.BuyLot(ctx, client(resident, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: lot, Confirm: screens.ResidenceConfirm}); err != nil ||
			!strings.Contains(r.Text, "citizen.buy.done_title") {
			t.Fatalf("buying %s: %+v %v", lot, r, err)
		}
	}
	if r, err := village.BuyLot(ctx, client(resident, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: lotD, Confirm: screens.ResidenceConfirm}); err != nil ||
		!strings.Contains(r.Text, "citizen.refusal.citizen_lot_limit") {
		t.Fatalf("the lot limit was not kept: %+v %v", r, err)
	}

	// ---- the head keeps off private land; a private building is not the head's ----
	headMeta := func(command string) envelope.Metadata {
		m := asPlayer(metaA, head)
		m.Command = command
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	if r, err := village.Place(ctx, headMeta("settlement.build.place"), handlers.VillageBuildRequest{Code: "road", Lot: lotB, Confirm: screens.VillageBuildConfirm}); err != nil ||
		!strings.Contains(r.Text, "citizen.refusal.citizen_lot_private") {
		t.Fatalf("the head built on a resident's lot: %+v %v", r, err)
	}
	if r, err := village.Place(ctx, headMeta("settlement.build.place"), handlers.VillageBuildRequest{Code: "cottage", Lot: lotD, Confirm: screens.VillageBuildConfirm}); err != nil ||
		!strings.Contains(r.Text, "citizen.refusal.citizen_private_only") {
		t.Fatalf("the head placed a private building: %+v %v", r, err)
	}

	// ---- the citizen catalogue shows only what can be built now -----------
	menu, err := village.PrivateMenu(ctx, client(resident, "settlement.private"))
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, l := range viewOf(t, menu)["lines"].([]any) {
		codes[l.(map[string]any)["building"].(map[string]any)["code"].(string)] = true
	}
	if !codes["cottage"] || !codes["market_stall"] || codes["village_house"] || codes["home_workshop"] {
		t.Fatalf("the citizen catalogue offers %v: the carpentry buildings need the knowledge first", codes)
	}

	// ---- cannot build on another's lot ------------------------------------
	place := func(p *application.Player, code, lot, confirm string) (*presenter.Response, error) {
		return village.PrivatePlace(ctx, client(p, "settlement.private.place"), handlers.VillagePrivateRequest{Code: code, Lot: lot, Confirm: confirm})
	}
	if r, err := place(neighbour, "cottage", lotA, screens.VillageBuildConfirm); err != nil || !strings.Contains(r.Text, "citizen.refusal.citizen_not_owner") {
		t.Fatalf("a neighbour built on another's lot: %+v %v", r, err)
	}
	if r, err := place(neighbour, "cottage", lotD, screens.VillageBuildConfirm); err != nil || !strings.Contains(r.Text, "citizen.refusal.citizen_not_owner") {
		t.Fatalf("a neighbour built on commons: %+v %v", r, err)
	}
	if n := countRows(`SELECT count(*) FROM settlement_private_buildings WHERE settlement_id = $1::uuid`, cityID); n != 0 {
		t.Fatalf("refused builds left %d private buildings", n)
	}

	// ---- build a house on one's own lot -----------------------------------
	treasury1, cash1 := treasuryOf(t, pool, cityID), cashOfPlayer(resident)
	ask, err = place(resident, "cottage", lotA, "")
	if err != nil || !strings.Contains(ask.Text, "citizen.confirm.title") {
		t.Fatalf("asking to build: %+v %v", ask, err)
	}
	if treasuryOf(t, pool, cityID) != treasury1 || cashOfPlayer(resident) != cash1 {
		t.Fatal("asking to build moved money")
	}
	started, err := place(resident, "cottage", lotA, screens.VillageBuildConfirm)
	if err != nil || !strings.Contains(started.Text, "citizen.mine.title") {
		t.Fatalf("building: %+v %v", started, err)
	}
	cottage, _ := snap.SettlementBuildingDef("cottage")
	timber, _ := snap.ComponentDef("timber")
	bought := cottage.CostMaterials["timber"] * (timber.BasePrice * int64(rules.MaterialMarkupBPS) / 10_000)
	if got := treasuryOf(t, pool, cityID); got != treasury1+rules.PermitFee {
		t.Fatalf("the treasury has %d, want %d: the permit must land in it", got, treasury1+rules.PermitFee)
	}
	if got, want := cashOfPlayer(resident), cash1-cottage.CostMoney-rules.PermitFee-bought; got != want {
		t.Fatalf("the builder has %d, want %d (cost %d, permit %d, materials %d)", got, want, cottage.CostMoney, rules.PermitFee, bought)
	}
	var buildingID string
	if err := pool.Raw().QueryRow(ctx, `SELECT b.building_id::text FROM settlement_private_buildings b WHERE b.settlement_id = $1::uuid AND b.owner_id = $2::uuid`,
		cityID, resident.ID).Scan(&buildingID); err != nil {
		t.Fatalf("the private building row: %v", err)
	}
	if n := countRows(`SELECT count(*) FROM settlement_buildings WHERE id = $1::uuid AND status = 'building' AND type_code = 'cottage'`, buildingID); n != 1 {
		t.Fatal("the building did not start construction")
	}
	// Again on the same lot: occupied.
	if r, err := place(resident, "cottage", lotA, screens.VillageBuildConfirm); err != nil || !strings.Contains(r.Text, "village.refusal.occupied") {
		t.Fatalf("built twice on one lot: %+v %v", r, err)
	}
	// The head can neither cancel nor demolish a resident's building.
	if r, err := village.Cancel(ctx, headMeta("settlement.build.cancel"), handlers.VillageBuildingRequest{ID: buildingID}); err != nil ||
		!strings.Contains(r.Text, "citizen.refusal.citizen_lot_private") {
		t.Fatalf("the head cancelled a resident's building: %+v %v", r, err)
	}

	// ---- live in it ---------------------------------------------------------
	if r, err := village.HomeRest(ctx, group(resident, "settlement.home.rest")); err != nil || !strings.Contains(r.Text, "citizen.refusal.citizen_no_house") {
		t.Fatalf("rested before the house stood: %+v %v", r, err)
	}
	e.clock.Advance(cottage.Def().BuildTime + time.Second)
	if _, err := village.Built(ctx, group(resident, "settlement.built"), handlers.CrimeScheduledRequest{ReferenceID: buildingID}); err != nil {
		t.Fatal(err)
	}
	mine, err := village.Mine(ctx, client(resident, "settlement.mine"))
	if err != nil {
		t.Fatal(err)
	}
	if mv := viewOf(t, mine); mv["home"] == nil {
		t.Fatalf("a finished house is not the resident's home: %v", mv)
	}
	cashBeforeRest := cashOfPlayer(resident)
	if r, err := village.HomeRest(ctx, group(resident, "settlement.home.rest")); err != nil || !strings.Contains(r.Text, "citizen.mine.notice.rested") {
		t.Fatalf("resting at home: %+v %v", r, err)
	}
	if cashOfPlayer(resident) != cashBeforeRest {
		t.Fatal("living at home paid money: residency is not a faucet")
	}
	if r, err := village.HomeRest(ctx, group(resident, "settlement.home.rest")); err != nil || !strings.Contains(r.Text, "citizen.refusal.citizen_rest_wait") {
		t.Fatalf("rested twice inside the cooldown: %+v %v", r, err)
	}

	// ---- the head's terms, inside their bounds ----------------------------
	terms := func(p *application.Player, req handlers.VillageTermsRequest) *presenter.Response {
		r, err := village.Terms(ctx, group(p, "settlement.terms"), req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := terms(resident, handlers.VillageTermsRequest{LotPrice: "150"}); !strings.Contains(r.Text, "village.refusal.not_office_holder") {
		t.Fatalf("a resident set the terms: %+v", r)
	}
	if r := terms(head, handlers.VillageTermsRequest{LotPrice: "50"}); !strings.Contains(r.Text, "citizen.refusal.citizen_terms_range") {
		t.Fatalf("a price below the bound was accepted: %+v", r)
	}
	if r := terms(head, handlers.VillageTermsRequest{LotPrice: "250", TaxBPS: "100"}); !strings.Contains(r.Text, "citizen.terms.title") {
		t.Fatalf("the head's terms: %+v", r)
	}
	tr0, nc0 := treasuryOf(t, pool, cityID), cashOfPlayer(neighbour)
	if r, err := village.BuyLot(ctx, client(neighbour, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: lotD, Confirm: screens.ResidenceConfirm}); err != nil ||
		!strings.Contains(r.Text, "citizen.buy.done_title") {
		t.Fatalf("buying at the head's price: %+v %v", r, err)
	}
	if treasuryOf(t, pool, cityID) != tr0+250 || cashOfPlayer(neighbour) != nc0-250 {
		t.Fatal("the lot did not sell at the head's price")
	}
	if r := terms(head, handlers.VillageTermsRequest{LotPrice: "400", TaxBPS: "200"}); !strings.Contains(r.Text, "citizen.terms.title") {
		t.Fatalf("resetting the terms: %+v", r)
	}

	// ---- property tax: assessed on the lots and the house, once a period ---
	tax := func(at time.Time) {
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return village.SettleTax(ctx, tx, cityID, at)
		}); err != nil {
			t.Fatal(err)
		}
	}
	// resident: 3 lots at 400/400/400 (lotB, lotC were bought at 400; lotA too) + the cottage
	var assessed int64 = 3*400 + cottage.CostMoney + cottage.CostMaterials["timber"]*timber.BasePrice
	wantTax := application.PropertyTaxDue(assessed, rules.TaxBPS)
	treasury2, cash2 := treasuryOf(t, pool, cityID), cashOfPlayer(resident)
	tax(e.clock.Now())
	if got := treasuryOf(t, pool, cityID) - treasury2; got < wantTax {
		t.Fatalf("the property tax credited %d, want at least the resident's %d", got, wantTax)
	}
	if got := cash2 - cashOfPlayer(resident); got != wantTax {
		t.Fatalf("the resident paid %d in tax, want %d", got, wantTax)
	}
	treasury3 := treasuryOf(t, pool, cityID)
	tax(e.clock.Now()) // the same period again: nothing more
	if treasuryOf(t, pool, cityID) != treasury3 {
		t.Fatal("a period was charged twice")
	}
	// A resident with nothing left owes it: a debt that a later payment clears.
	pauper := insertPlayer(t, pool)
	placePlayer(t, pool, pauper.ID, support, "")
	if _, err := village.Join(ctx, group(pauper, "settlement.join"), handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm}); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, pauper.ID, 400)
	if r, err := village.BuyLot(ctx, client(pauper, "settlement.lot.buy"), handlers.VillageLotRequest{Lot: free[4%len(free)], Confirm: screens.ResidenceConfirm}); err != nil ||
		!strings.Contains(r.Text, "citizen.buy.done_title") {
		t.Fatalf("the pauper's lot: %+v %v", r, err)
	}
	e.clock.Advance(48 * time.Hour)
	tax(e.clock.Now())
	debt := countRows(`SELECT count(*) FROM settlement_property_tax WHERE settlement_id = $1::uuid AND player_id = $2::uuid AND paid_at IS NULL`, cityID, pauper.ID)
	if debt != 1 {
		t.Fatalf("a resident with no cash should owe exactly this period's tax, owes %d rows", debt)
	}
	if r, err := village.PayTax(ctx, client(pauper, "settlement.tax.pay")); err != nil || !strings.Contains(r.Text, "citizen.mine.title") {
		t.Fatalf("paying with no cash: %+v %v", r, err)
	}
	if n := countRows(`SELECT count(*) FROM settlement_property_tax WHERE settlement_id = $1::uuid AND player_id = $2::uuid AND paid_at IS NULL`, cityID, pauper.ID); n != 1 {
		t.Fatal("the debt vanished without being paid")
	}
	grantCash(t, pool, pauper.ID, 100)
	if _, err := village.PayTax(ctx, client(pauper, "settlement.tax.pay")); err != nil {
		t.Fatal(err)
	}
	if n := countRows(`SELECT count(*) FROM settlement_property_tax WHERE settlement_id = $1::uuid AND player_id = $2::uuid AND paid_at IS NULL`, cityID, pauper.ID); n != 0 {
		t.Fatal("the debt was not paid once the cash was there")
	}

	// ---- the ledger and the rows agree --------------------------------------
	v, err := admin.VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	c := v.CitizenInvariants
	if c.LotSaleLedger-c.LotSaleRows != v0.LotSaleLedger-v0.LotSaleRows || c.LotSaleMismatched != 0 ||
		c.PermitLedger-c.PermitRows != v0.PermitLedger-v0.PermitRows ||
		c.ConstructionLedger-c.ConstructionRows != v0.ConstructionLedger-v0.ConstructionRows ||
		c.MaterialsLedger-c.MaterialsRows != v0.MaterialsLedger-v0.MaterialsRows || c.BuildingMismatched != 0 ||
		c.TaxLedger-c.TaxRows != v0.TaxLedger-v0.TaxRows || c.TaxMismatched != 0 || c.CitizenPaidToPlayer != 0 {
		t.Errorf("the citizen loop's ledger and rows drifted apart: before %+v, after %+v", v0.CitizenInvariants, c)
	}

	// ---- the layout shows owners to members only ------------------------------
	worldSvc := &clientapi.WorldService{Source: e.cache, CacheEntries: 8, RecheckEvery: time.Minute}
	villages := &clientapi.VillageService{Settlements: postgres.NewSettlementReader(pool), Buildings: postgres.NewSettlementBuildingReader(pool),
		World: worldSvc, Content: registryOf(snap), VillageGridLots: 5, Now: e.clock.Now,
		Citizens: postgres.NewCitizenReader(pool),
		CitizenTerms: application.CitizenBounds{LotPrice: rules.LotPrice, LotPriceMin: rules.LotPriceMin, LotPriceMax: rules.LotPriceMax,
			PermitFee: rules.PermitFee, PermitFeeMax: rules.PermitFeeMax, TaxBPS: rules.TaxBPS, TaxBPSMax: rules.TaxBPSMax}.Effective}
	member, err := villages.Layout(ctx, resident.ID, cityID)
	if err != nil {
		t.Fatal(err)
	}
	mineLots, private := 0, 0
	for _, l := range member.Tenure {
		if l.Mine {
			mineLots++
		}
	}
	for _, b := range member.Buildings {
		if b.Private && b.Mine && b.Type == "cottage" {
			private++
		}
	}
	if mineLots != 3 || private != 1 || member.Terms == nil || member.Terms.LotPrice != 400 || !member.Viewer.Resident {
		t.Fatalf("the member's layout: %d own lots, %d own private buildings, terms %+v, viewer %+v", mineLots, private, member.Terms, member.Viewer)
	}
	if other, err := villages.Layout(ctx, stranger.ID, cityID); err != nil || len(other.Tenure) != 0 || other.Terms != nil {
		t.Fatalf("a stranger sees who owns what: %+v %v", other.Tenure, err)
	} else {
		for _, b := range other.Buildings {
			if b.Owner != "" || b.Private {
				t.Fatalf("a stranger sees a building's owner: %+v", b)
			}
		}
	}
	if head1, err := villages.Layout(ctx, head.ID, cityID); err != nil || head1.Version == member.Version {
		t.Errorf("the head's and the member's layout versions must differ (canPlace): %v", err)
	}
}

// purgeCitizenFootprint takes back everything a test wrote for one village's
// citizen loop: the journal rows AND the ledger transactions they name, both
// sides together, so the integration database still verifies (the ledger is
// append-only in production; tests are the only place it is purged, the way
// companies_integration_test.go does).
func purgeCitizenFootprint(t *testing.T, pool *postgres.Pool, cityID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, stmt := range []string{
		`CREATE TEMP TABLE purge_cz ON COMMIT DROP AS
		   SELECT DISTINCT e.transaction_id FROM ledger_entries e
		    WHERE e.reason IN ('settlement_lot_sale', 'settlement_permit_fee', 'citizen_construction', 'citizen_materials', 'settlement_property_tax')
		      AND e.reference_id IN (SELECT id FROM settlement_lots WHERE settlement_id = $1::uuid
		                             UNION SELECT building_id FROM settlement_private_buildings WHERE settlement_id = $1::uuid
		                             UNION SELECT id FROM settlement_property_tax WHERE settlement_id = $1::uuid)`,
		`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`,
		`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
		          WHERE transaction_id IN (SELECT transaction_id FROM purge_cz) GROUP BY account_id) d
		  WHERE a.id = d.account_id`,
		`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_cz)`,
		`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`,
		`DELETE FROM settlement_property_tax WHERE settlement_id = $1::uuid`,
		`DELETE FROM settlement_private_buildings WHERE settlement_id = $1::uuid`,
		`DELETE FROM settlement_lots WHERE settlement_id = $1::uuid`,
	} {
		var err error
		if strings.Contains(stmt, "$1") {
			_, err = tx.Exec(ctx, stmt, cityID)
		} else {
			_, err = tx.Exec(ctx, stmt)
		}
		if err != nil {
			t.Errorf("cleanup %.40q: %v", stmt, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}
