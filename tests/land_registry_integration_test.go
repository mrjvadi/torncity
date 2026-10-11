//go:build integration

// Integration test of the land registry (docs/adr/0072, migration 0150): every bought lot is a parcel of one cell, the owner joins and
// splits his parcels for a fee, a holder of zone.open opens zones that bind from the end of their notice (and never reach a building
// that stands), the commons are not for sale, a holder of land.claim claims a tile of the world and the treasury pays its upkeep, and a
// foundation lets a building stand on a sloped lot. `admin economy verify` proves every coin balances.
package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/landuse"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func TestLandRegistry(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool
	e.h.WithFoundingGrant(10_000)
	metaA, head := e.group(t)
	foundVillage(t, pool, e.h, metaA)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaA.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, q := range []string{
			`DELETE FROM parcel_cells WHERE settlement_id = $1::uuid`,
			`DELETE FROM parcels WHERE settlement_id = $1::uuid`,
			`DELETE FROM zone_cells WHERE settlement_id = $1::uuid`,
			`DELETE FROM zones WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_claims WHERE settlement_id = $1::uuid`,
			`DELETE FROM lot_foundations WHERE settlement_id = $1::uuid`,
			`DELETE FROM land_audit WHERE settlement_id = $1::uuid`,
		} {
			_, _ = pool.Raw().Exec(c, q, cityID)
		}
		purgeCitizenFootprint(t, pool, cityID)
	})

	rules := handlers.CitizenRules{
		LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000,
		TaxBPS: 200, TaxBPSMax: 500, TaxPeriod: 24 * time.Hour, MaterialMarkupBPS: 12_000,
		MaxLotsPerPlayer: 6, PrivateShareMaxBPS: 10_000, HomeRestCooldown: 6 * time.Hour, HomeRestHealth: 10, HomeRestHappiness: 5,
	}
	past := e.clock.Now().Add(-48 * time.Hour)
	reg := application.RegistryRules{
		ParcelMaxCells: 4, ParcelChangeFee: 50, ZoneOpenFee: 20, ZoneMaxCells: 12, ZoneNotice: 10 * time.Minute,
		ZoningRuleAt: past, SlopeRuleAt: past,
		Slope:           landuse.Bands{Flat: 8, Gentle: 20, Unusable: 500},
		FoundationStone: 2, FoundationWage: 30, ClaimCost: 700, ClaimUpkeepPerDay: 5, ClaimMaxTiles: 3,
	}
	snap := loadTestContent(t)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	newVillage := func(r application.RegistryRules) *handlers.VillageHandler {
		return handlers.NewVillageHandler(uow, workIDs{t}, nil, staticContentSource{snap: snap}, e.cache,
			postgres.NewCityRepository(pool), gametime.Scale(1),
			handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000, AutoRoadCost: 10,
				BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
			time.Hour, e.clock.Now).WithDonationRules(100, 100_000, []int64{250}).WithCitizenRules(rules).WithRegistryRules(r)
	}
	village := newVillage(reg)
	tight := reg
	// bands so tight that nearly any drop is a slope: the test world's own terrain supplies one
	tight.Slope = landuse.Bands{Flat: 1, Gentle: 2, Unusable: 500}
	villageTight := newVillage(tight)

	support := cityIDByCode(t, pool, "support")
	resident, neighbour := insertPlayer(t, pool), insertPlayer(t, pool)
	for _, p := range []*application.Player{resident, neighbour} {
		placePlayer(t, pool, p.ID, support, "")
	}
	client := func(p *application.Player, command string) envelope.Metadata {
		m := clientMeta(asPlayer(metaA, p), command, command)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	for _, p := range []*application.Player{resident, neighbour} {
		m := asPlayer(metaA, p)
		m.Command, m.IdempotencyKey = "settlement.join", "it-"+randomToken(t, 16)
		if _, err := rrm(m)(village.Join(ctx, m, handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm})); err != nil {
			t.Fatal(err)
		}
		grantCash(t, pool, p.ID, 20_000)
	}
	cashOf := func(p *application.Player) int64 {
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
	count := func(query string, args ...any) int { return e.count(t, query, args...) }

	// the free lots of the first grid
	type xy struct{ x, y int }
	freeCells := func() map[xy]bool {
		land, err := rrm(client(resident, "settlement.land"))(village.Land(ctx, client(resident, "settlement.land")))
		if err != nil {
			t.Fatal(err)
		}
		out := map[xy]bool{}
		for _, row := range viewOf(t, land)["rows"].([]any) {
			for _, c := range row.([]any) {
				if cell := c.(map[string]any); cell["state"] == screens.LandFree {
					out[xy{int(cell["x"].(float64)), int(cell["y"].(float64))}] = true
				}
			}
		}
		return out
	}
	free := freeCells()
	// two lots that touch, a third that does not touch them
	var a, b, c xy
	found := false
	for p := range free {
		q := xy{p.x + 1, p.y}
		if free[q] {
			a, b, found = p, q, true
			break
		}
	}
	if !found {
		t.Skip("the test village has no two free lots side by side")
	}
	for p := range free {
		if p != a && p != b && (p.x < a.x-1 || p.x > b.x+1 || p.y < a.y-1 || p.y > a.y+1) {
			c = p
			break
		}
	}
	if c == (xy{}) && !(a.x != 0 && a.y != 0) {
		t.Skip("the test village has no third free lot apart")
	}
	tok := func(p xy) string { return screens.LotToken(p.x, p.y, false) }

	// ---- every bought lot is a parcel of one cell ----------------------------
	buyLot := func(p xy) {
		t.Helper()
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return tx.Citizens().InsertLot(ctx, application.SettlementLot{ID: (workIDs{t}).NewID(), SettlementID: cityID, X: p.x, Y: p.y,
				Tenure: application.TenureFreehold, OwnerID: resident.ID, Price: 400, LedgerTransactionID: (workIDs{t}).NewID(), AcquiredAt: e.clock.Now()})
		}); err != nil {
			t.Fatal(err)
		}
	}
	buyLot(a)
	buyLot(b)
	buyLot(c)
	if n := count(`SELECT count(*) FROM parcels WHERE settlement_id = $1::uuid AND owner_id = $2::uuid`, cityID, resident.ID); n != 3 {
		t.Fatalf("three lots, %d parcels", n)
	}
	registry := func(p *application.Player) map[string]any {
		t.Helper()
		m := client(p, "settlement.land.registry")
		r, err := rrm(m)(village.LandRegistry(ctx, m))
		if err != nil {
			t.Fatal(err)
		}
		return viewOf(t, r)
	}
	parcelIDs := func() map[xy]string {
		out := map[xy]string{}
		for _, raw := range registry(resident)["parcels"].([]any) {
			pm := raw.(map[string]any)
			if pm["mine"] != true {
				continue
			}
			for _, cell := range pm["cells"].([]any) {
				cm := cell.(map[string]any)
				out[xy{int(cm["x"].(float64)), int(cm["y"].(float64))}] = pm["id"].(string)
			}
		}
		return out
	}
	ids := parcelIDs()
	if len(ids) != 3 {
		t.Fatalf("the registry shows %d of my cells, want 3", len(ids))
	}

	// ---- join: the quote changes nothing, a stranger is refused, the confirm pays the fee ----
	join := func(p *application.Player, keep, take, confirm string) *presenter.Response {
		m := client(p, "settlement.parcel.join")
		r, err := rrm(m)(village.ParcelJoin(ctx, m, handlers.VillageRegistryRequest{Parcel: keep, With: take, Confirm: confirm}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cash0, treasury0 := cashOf(resident), treasuryOf(t, pool, cityID)
	if r := join(resident, ids[a], ids[b], ""); viewOf(t, r)["quote"] == nil || cashOf(resident) != cash0 {
		t.Fatalf("a join quote moves money or shows no quote: %s", r.Text)
	}
	if r := join(neighbour, ids[a], ids[b], screens.ResidenceConfirm); !strings.Contains(r.Text, "village.refusal.land_not_yours") {
		t.Fatalf("a stranger joined my parcels: %s", r.Text)
	}
	if r := join(resident, ids[a], ids[c], screens.ResidenceConfirm); !strings.Contains(r.Text, "village.refusal.land_not_adjacent") {
		t.Fatalf("two parcels that do not touch were joined: %s", r.Text)
	}
	if r := join(resident, ids[a], ids[b], screens.ResidenceConfirm); strings.Contains(r.Text, "refusal") {
		t.Fatalf("joining: %s", r.Text)
	}
	if got := cashOf(resident); got != cash0-50 || treasuryOf(t, pool, cityID) != treasury0+50 {
		t.Fatalf("the join fee: cash %d (want %d), treasury %d (want %d)", got, cash0-50, treasuryOf(t, pool, cityID), treasury0+50)
	}
	if n := count(`SELECT count(*) FROM parcels WHERE settlement_id = $1::uuid AND owner_id = $2::uuid`, cityID, resident.ID); n != 2 {
		t.Fatalf("after the join %d parcels, want 2", n)
	}
	// a repeated confirm (the same key) changes nothing
	m := client(resident, "settlement.parcel.join")
	m.IdempotencyKey = "it-fixed-join"
	_, _ = rrm(m)(village.ParcelJoin(ctx, m, handlers.VillageRegistryRequest{Parcel: parcelIDs()[a], With: parcelIDs()[c], Confirm: screens.ResidenceConfirm}))

	// ---- split: cut one cell out again ---------------------------------------
	ids = parcelIDs()
	split := func(parcel string, from, to xy, confirm string) *presenter.Response {
		m := client(resident, "settlement.parcel.split")
		r, err := rrm(m)(village.ParcelSplit(ctx, m, handlers.VillageRegistryRequest{Parcel: parcel, From: tok(from), To: tok(to), Confirm: confirm}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := split(ids[a], a, b, screens.ResidenceConfirm); !strings.Contains(r.Text, "village.refusal.land_whole_parcel") {
		t.Fatalf("splitting the whole parcel: %s", r.Text)
	}
	if r := split(ids[a], b, b, screens.ResidenceConfirm); strings.Contains(r.Text, "refusal") {
		t.Fatalf("splitting: %s", r.Text)
	}
	ids = parcelIDs()
	if ids[a] == ids[b] || len(ids) != 3 {
		t.Fatalf("after the split a and b share a parcel or a cell is lost: %v", ids)
	}

	// ---- zones -------------------------------------------------------------------
	headMeta := func(command string) envelope.Metadata {
		m := clientMeta(asPlayer(metaA, head), command, command)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	freeNow := freeCells()
	// a free block of 2x2 lots (a woodcutter's camp is 2x2), none of them mine
	var z1, z2, z3 xy
	found = false
	taken := map[xy]bool{a: true, b: true, c: true}
	for p := range freeNow {
		cells := []xy{p, {p.x + 1, p.y}, {p.x, p.y + 1}, {p.x + 1, p.y + 1}}
		ok := true
		for _, q := range cells {
			ok = ok && freeNow[q] && !taken[q]
		}
		if ok {
			z1, z2, z3, found = p, cells[1], cells[3], true
			break
		}
	}
	if !found {
		t.Skip("no free block of 2x2 lots left for a zone")
	}
	zoneOpen := func(m envelope.Metadata, from, to xy, kind, name, confirm string) *presenter.Response {
		r, err := rrm(m)(village.ZoneOpen(ctx, m, handlers.VillageRegistryRequest{From: tok(from), To: tok(to), Kind: kind, Name: name, Confirm: confirm}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := zoneOpen(client(resident, "settlement.zone.open"), z1, z3, "residential", "Homes", screens.ResidenceConfirm); !strings.Contains(r.Text, "village.refusal.village_not_office_holder") && !strings.Contains(r.Text, "refusal") {
		t.Fatalf("a resident with no office opened a zone: %s", r.Text)
	}
	if r := zoneOpen(headMeta("settlement.zone.open"), a, a, "farm", "Mine", screens.ResidenceConfirm); !strings.Contains(r.Text, "village.refusal.land_cell_owned") {
		t.Fatalf("a zone over another's lot: %s", r.Text)
	}
	t0 := treasuryOf(t, pool, cityID)
	if r := zoneOpen(headMeta("settlement.zone.open"), z1, z3, "residential", "Homes", ""); viewOf(t, r)["quote"] == nil || treasuryOf(t, pool, cityID) != t0 {
		t.Fatalf("a zone quote moves money or shows no quote: %s", r.Text)
	}
	if r := zoneOpen(headMeta("settlement.zone.open"), z1, z3, "residential", "Homes", screens.ResidenceConfirm); strings.Contains(r.Text, "refusal") {
		t.Fatalf("opening a zone: %s", r.Text)
	}
	if treasuryOf(t, pool, cityID) != t0-80 || count(`SELECT count(*) FROM zones WHERE settlement_id = $1::uuid AND closed_at IS NULL`, cityID) != 1 {
		t.Fatalf("the zone fee: treasury %d (want %d)", treasuryOf(t, pool, cityID), t0-80)
	}
	if r := zoneOpen(headMeta("settlement.zone.open"), z1, z1, "core", "Again", screens.ResidenceConfirm); !strings.Contains(r.Text, "village.refusal.land_cell_zoned") {
		t.Fatalf("a lot in two zones: %s", r.Text)
	}
	// a woodcutter's camp (zones farm, industry) is refused on residential ground once the notice is over, and allowed before
	placeWith := func(vh *handlers.VillageHandler, code string, p xy) *presenter.Response {
		m := headMeta("settlement.build.place")
		r, err := rrm(m)(vh.Place(ctx, m, handlers.VillageBuildRequest{Code: code, Lot: tok(p), Confirm: ""}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	place := func(code string, p xy) *presenter.Response { return placeWith(village, code, p) }
	if r := place("woodcutter_camp", z1); strings.Contains(r.Text, "land_zone_forbids") {
		t.Fatalf("the zone bound before its notice was over: %s", r.Text)
	}
	e.clock.Advance(time.Hour)
	if r := place("woodcutter_camp", z1); !strings.Contains(r.Text, "village.refusal.land_zone_forbids") {
		t.Fatalf("a woodcutter on residential ground: %s", r.Text)
	}
	// the commons are not sold, and closing a zone frees its lots
	var zoneID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM zones WHERE settlement_id = $1::uuid AND closed_at IS NULL`, cityID).Scan(&zoneID); err != nil {
		t.Fatal(err)
	}
	closeZone := func(m envelope.Metadata, id string) *presenter.Response {
		r, err := rrm(m)(village.ZoneClose(ctx, m, handlers.VillageRegistryRequest{Zone: id}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := closeZone(headMeta("settlement.zone.close"), zoneID); strings.Contains(r.Text, "refusal") {
		t.Fatalf("closing a zone: %s", r.Text)
	}
	if count(`SELECT count(*) FROM zone_cells WHERE settlement_id = $1::uuid`, cityID) != 0 {
		t.Fatal("closing a zone left its lots in it")
	}
	if r := zoneOpen(headMeta("settlement.zone.open"), z1, z2, "commons", "Green", screens.ResidenceConfirm); strings.Contains(r.Text, "refusal") {
		t.Fatalf("opening the commons: %s", r.Text)
	}
	if count(`SELECT count(*) FROM parcels WHERE settlement_id = $1::uuid AND owner_kind = 'treasury'`, cityID) != 1 {
		t.Fatal("the commons made no treasury parcel")
	}
	buy := client(resident, "settlement.lot.buy")
	if r, err := rrm(buy)(village.BuyLot(ctx, buy, handlers.VillageLotRequest{Lot: tok(z1), Confirm: screens.ResidenceConfirm})); err != nil || !strings.Contains(r.Text, "village.refusal.land_commons") {
		t.Fatalf("a lot of the commons was sold: %v %v", r, err)
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, err := tx.Registry().CloseZone(ctx, cityID, func() string {
			var id string
			_ = pool.Raw().QueryRow(ctx, `SELECT id::text FROM zones WHERE settlement_id = $1::uuid AND closed_at IS NULL`, cityID).Scan(&id)
			return id
		}(), head.ID, e.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// ---- claims ----------------------------------------------------------------
	claim := func(p xy, confirm string) *presenter.Response {
		m := headMeta("settlement.claim")
		r, err := rrm(m)(village.Claim(ctx, m, handlers.VillageRegistryRequest{Lot: tok(p), Confirm: confirm}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	t1 := treasuryOf(t, pool, cityID)
	if r := claim(z1, ""); viewOf(t, r)["quote"] == nil {
		t.Fatalf("a claim quote: %s", r.Text)
	}
	if r := claim(z1, screens.ResidenceConfirm); strings.Contains(r.Text, "refusal") {
		t.Fatalf("claiming the home tile: %s", r.Text)
	}
	if treasuryOf(t, pool, cityID) != t1-700 || count(`SELECT count(*) FROM settlement_claims WHERE settlement_id = $1::uuid`, cityID) != 1 {
		t.Fatalf("the claim price: treasury %d (want %d)", treasuryOf(t, pool, cityID), t1-700)
	}
	if r := claim(z1, screens.ResidenceConfirm); !strings.Contains(r.Text, "village.refusal.land_tile_claimed") {
		t.Fatalf("the same tile claimed twice: %s", r.Text)
	}
	// the upkeep: three days later the registry charges three days from the treasury
	e.clock.Advance(3 * 24 * time.Hour)
	t2 := treasuryOf(t, pool, cityID)
	registry(resident)
	if got := t2 - treasuryOf(t, pool, cityID); got < 10 || got > 20 {
		t.Fatalf("the claim's upkeep for three days is 10 to 20, charged %d", got)
	}

	// ---- foundations --------------------------------------------------------------
	// the mason's craft is the settlement's own research: without it no foundation is laid
	{
		m := headMeta("settlement.foundation.lay")
		if r, err := rrm(m)(villageTight.FoundationLay(ctx, m, handlers.VillageRegistryRequest{Lot: tok(z1)})); err != nil || !strings.Contains(r.Text, "village.refusal.land_no_masonry") {
			t.Fatalf("a foundation without masonry: %v %v", r, err)
		}
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, 'masonry', 'researched', now())`, cityID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, 'carpentry', 'researched', now())`, cityID); err != nil {
		t.Fatal(err)
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "timber", Qty: 20, ToOrg: application.SettlementOrg(cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: e.clock.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	// a carpentry workshop (2x2) takes a flat lot unaided; the lots of the test world drop a metre or two, so the tight bands call
	// some of them sloped: find a free block of that kind
	var sp xy
	found = false
	for p := range freeCells() {
		cells := []xy{p, {p.x + 1, p.y}, {p.x, p.y + 1}, {p.x + 1, p.y + 1}}
		ok := true
		for _, q := range cells {
			ok = ok && freeNow[q] && !taken[q]
		}
		if !ok {
			continue
		}
		if r := placeWith(villageTight, "carpentry_workshop", p); strings.Contains(r.Text, "village.refusal.land_needs_foundation") {
			sp, found = p, true
			break
		}
	}
	if !found {
		t.Skip("no free block of the test world has a lot with a drop of a metre")
	}
	block := []xy{sp, {sp.x + 1, sp.y}, {sp.x, sp.y + 1}, {sp.x + 1, sp.y + 1}}
	z1 = sp
	if r := placeWith(village, "carpentry_workshop", sp); strings.Contains(r.Text, "land_needs_foundation") {
		t.Fatalf("with the real bands the same lots are flat: %s", r.Text)
	}
	laid := 0
	for _, p := range block {
		m := headMeta("settlement.foundation.lay")
		tt := treasuryOf(t, pool, cityID)
		if q, err := rrm(m)(villageTight.FoundationLay(ctx, m, handlers.VillageRegistryRequest{Lot: tok(p)})); err != nil {
			t.Fatal(err)
		} else if viewOf(t, q)["quote"] == nil {
			if !strings.Contains(q.Text, "land_no_foundation_needed") {
				t.Fatalf("the foundation quote: %s", q.Text)
			}
			continue
		}
		if treasuryOf(t, pool, cityID) != tt {
			t.Fatal("a foundation quote moved money")
		}
		m = headMeta("settlement.foundation.lay")
		if r, err := rrm(m)(villageTight.FoundationLay(ctx, m, handlers.VillageRegistryRequest{Lot: tok(p), Confirm: screens.ResidenceConfirm})); err != nil || strings.Contains(r.Text, "refusal") {
			t.Fatalf("laying a foundation: %v %v", r, err)
		}
		if treasuryOf(t, pool, cityID) >= tt {
			t.Fatal("a foundation was not paid by the treasury")
		}
		m = headMeta("settlement.foundation.lay")
		if r, _ := rrm(m)(villageTight.FoundationLay(ctx, m, handlers.VillageRegistryRequest{Lot: tok(p), Confirm: screens.ResidenceConfirm})); !strings.Contains(r.Text, "village.refusal.land_foundation_exists") {
			t.Fatalf("a second foundation on a lot: %s", r.Text)
		}
		laid++
	}
	if laid == 0 || count(`SELECT count(*) FROM lot_foundations WHERE settlement_id = $1::uuid`, cityID) != laid {
		t.Fatalf("%d foundations laid, %d recorded", laid, count(`SELECT count(*) FROM lot_foundations WHERE settlement_id = $1::uuid`, cityID))
	}
	if r := placeWith(villageTight, "carpentry_workshop", z1); strings.Contains(r.Text, "land_needs_foundation") || strings.Contains(r.Text, "land_too_steep") {
		t.Fatalf("the foundations did not lift the slope rule: %s", r.Text)
	}

	// ---- releasing a lot takes its cell out of the parcel -----------------------------
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		lots, err := tx.Citizens().Lots(ctx, cityID)
		if err != nil {
			return err
		}
		for _, l := range lots {
			if l.X == c.x && l.Y == c.y {
				ok, err := tx.Citizens().ReleaseLot(ctx, application.LotRelease{LotID: l.ID, Kind: application.ReleaseDedicated, At: e.clock.Now()})
				if err != nil || !ok {
					t.Fatalf("releasing: %v %v", ok, err)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count(`SELECT count(*) FROM parcel_cells WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3`, cityID, c.x, c.y) != 0 {
		t.Fatal("a released lot is still a cell of a parcel")
	}
	if n := count(`SELECT count(*) FROM parcels p WHERE p.settlement_id = $1::uuid AND p.owner_kind = 'player' AND NOT EXISTS (SELECT 1 FROM parcel_cells WHERE parcel_id = p.id)`, cityID); n != 0 {
		t.Fatalf("%d parcels have no cell", n)
	}
	// the three lots were written without a sale (the repository directly): take them out before the ledger is checked
	if _, err := pool.Raw().Exec(ctx, `DELETE FROM settlement_lots WHERE settlement_id = $1::uuid`, cityID); err != nil {
		t.Fatal(err)
	}
	if v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10); err != nil {
		t.Fatal(err)
	} else if !v.OK() {
		t.Fatalf("the ledger does not verify: %+v", v)
	}
}
