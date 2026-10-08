//go:build integration

package tests

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	screens "github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

// A lot and a building permit are paid in the village's own money by a citizen who holds the units or
// converts at the desk inside the confirm; a citizen with SUP only who does not convert pays SUP.
func TestALotAndAPermitArePaidInTheVillageMoney(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool
	if v0, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10); err != nil || !v0.Citizen {
		t.Skip("migration 0058 is not applied")
	}
	e.h.WithFoundingGrant(60_000).WithCurrencyRules(testCurrencyRules())
	metaA, _ := e.group(t)
	foundVillage(t, pool, e.h, metaA)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaA.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { purgeCitizenFootprint(t, pool, cityID) })
	currencyCleanup(t, pool, cityID)

	rules := handlers.CitizenRules{
		LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000,
		TaxBPS: 200, TaxBPSMax: 500, TaxPeriod: 24 * time.Hour, MaterialMarkupBPS: 12_000,
		MaxLotsPerPlayer: 3, PrivateShareMaxBPS: 6000, HomeRestCooldown: 6 * time.Hour, HomeRestHealth: 10, HomeRestHappiness: 5,
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, nil, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now).WithDonationRules(100, 100_000, []int64{250}).WithCitizenRules(rules).WithCurrencyRules(localRules())

	resident := insertPlayer(t, pool)
	placePlayer(t, pool, resident.ID, cityIDByCode(t, pool, "support"), "")
	t.Cleanup(func() { purgeLedgerFor(t, pool, resident.ID) })
	group := func(command string) envelope.Metadata {
		m := asPlayer(metaA, resident)
		m.Command = command
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	client := func(command string) envelope.Metadata {
		m := clientMeta(asPlayer(metaA, resident), command, command)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	if _, err := village.Join(ctx, group("settlement.join"), handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm}); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, resident.ID, 20_000)

	holding := func(owner string) int64 {
		return int64(e.count(t, `SELECT COALESCE(SUM(balance), 0)::int FROM accounts WHERE kind = 'foreign_holding' AND owner_id = $1::uuid`, owner))
	}
	cash := func() int64 {
		return int64(e.count(t, `SELECT COALESCE(SUM(balance), 0)::int FROM accounts WHERE kind = 'player_cash' AND owner_id = $1::uuid`, resident.ID))
	}
	localRows := func() int {
		return e.count(t, `SELECT count(*) FROM local_payments WHERE settlement_id = $1::uuid`, cityID)
	}
	freeNow := func() string {
		land, err := rrm(client("settlement.land"))(village.Land(ctx, client("settlement.land")))
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range viewOf(t, land)["rows"].([]any) {
			for _, c := range row.([]any) {
				if cell := c.(map[string]any); cell["state"] == screens.LandFree && cell["access"] != "none" {
					return screens.LotToken(int(cell["x"].(float64)), int(cell["y"].(float64)), false)
				}
			}
		}
		t.Fatal("no free lot left")
		return ""
	}
	buyLot := func(lot string, confirm bool, ls handlers.LocalSettle) *presentation.Response {
		t.Helper()
		req := handlers.VillageLotRequest{Lot: lot, LocalSettle: ls}
		if confirm {
			req.Confirm = screens.ResidenceConfirm
		}
		r, err := village.BuyLot(ctx, client("settlement.lot.buy"), req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	desk := func(side string, amount int64) {
		t.Helper()
		if _, err := village.CurrencyDesk(ctx, client("settlement.currency.desk"),
			handlers.VillageDeskRequest{Side: side, Amount: strconv.FormatInt(amount, 10), Confirm: screens.ResidenceConfirm, Settlement: cityID}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. holding SUP only: the ask offers the desk with its price; a plain confirm pays SUP
	lotA := freeNow()
	ask := buyLot(lotA, false, handlers.LocalSettle{})
	if ask.Offer == nil || ask.Offer.Local || !ask.Offer.CanConvert || ask.Offer.Units != 4000 || ask.Offer.Convert == nil {
		t.Fatalf("the lot's offer: %+v", ask.Offer)
	}
	cash0 := cash()
	buyLot(lotA, true, handlers.LocalSettle{})
	if cash0-cash() < 400 || localRows() != 0 {
		t.Fatalf("a citizen who does not convert pays SUP: cash %d -> %d, local rows %d", cash0, cash(), localRows())
	}

	// 2. holding units: the next lot is paid in them, 400 SUP = 4000 units, and no SUP leaves for the price
	desk("buy", 1000)
	lotB := freeNow()
	if r := buyLot(lotB, false, handlers.LocalSettle{}); r.Offer == nil || !r.Offer.Local {
		t.Fatalf("holding the units the offer says local: %+v", r.Offer)
	}
	u0, c0 := holding(resident.ID), cash()
	buyLot(lotB, true, handlers.LocalSettle{})
	if got := u0 - holding(resident.ID); got != 4000 {
		t.Errorf("the lot cost %d units, want 4000", got)
	}
	if got := c0 - cash(); got >= 400 {
		t.Errorf("the price was also taken in SUP (%d)", got)
	}
	if localRows() != 1 {
		t.Errorf("one local payment: %d", localRows())
	}

	// 3. short of units: convert inside the confirm
	desk("sell", holding(resident.ID)-1500)
	lotC := freeNow()
	r := buyLot(lotC, false, handlers.LocalSettle{})
	if r.Offer == nil || r.Offer.Local || !r.Offer.CanConvert || r.Offer.ConvertUnits != 2500 {
		t.Fatalf("the offer when short: %+v", r.Offer)
	}
	trades0 := e.count(t, `SELECT count(*) FROM currency_desk_trades WHERE settlement_id = $1::uuid`, cityID)
	c1 := cash()
	if d := buyLot(lotC, true, handlers.LocalSettle{Convert: "1", MaxSUP: strconv.FormatInt(r.Offer.ConvertSUP, 10)}); d.Refusal != nil {
		t.Fatalf("buying with the conversion: %+v", d.Refusal)
	}
	if got := e.count(t, `SELECT count(*) FROM currency_desk_trades WHERE settlement_id = $1::uuid`, cityID); got != trades0+1 {
		t.Errorf("one conversion inside the purchase: %d trades", got-trades0)
	}
	if localRows() != 2 {
		t.Errorf("two local payments (lots B and C): %d", localRows())
	}
	if got := c1 - cash(); got < r.Offer.ConvertSUP {
		t.Errorf("the conversion cost %d SUP, the offer said %d", got, r.Offer.ConvertSUP)
	}

	// 4. the permit of a building on the first lot is paid in units: 100 SUP = 1000 units
	desk("buy", 500)
	place := func(confirm string, ls handlers.LocalSettle) *presentation.Response {
		t.Helper()
		p, err := village.PrivatePlace(ctx, client("settlement.private.place"), handlers.VillagePrivateRequest{Code: "private_cottage", Lot: lotA, Confirm: confirm, LocalSettle: ls})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	pa := place("", handlers.LocalSettle{})
	if pa.Offer == nil || !pa.Offer.Local || pa.Offer.Units != 1000 {
		t.Fatalf("the permit's offer: %+v", pa.Offer)
	}
	u1 := holding(resident.ID)
	if d := place(screens.VillageBuildConfirm, handlers.LocalSettle{}); d.Refusal != nil {
		t.Fatalf("building: %+v", d.Refusal)
	}
	if got := u1 - holding(resident.ID); got != 1000 {
		t.Errorf("the permit cost %d units, want 1000", got)
	}
	if got := e.count(t, `SELECT count(*) FROM local_payments WHERE settlement_id = $1::uuid AND flow = 'settlement_permit_fee'`, cityID); got != 1 {
		t.Errorf("a local payment for the permit: %d", got)
	}

	// 5. the property tax of a period is paid in units by an owner who holds them, never converted unasked
	c2, u2 := cash(), holding(resident.ID)
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return village.SettleTax(ctx, tx, cityID, e.clock.Now())
	}); err != nil {
		t.Fatal(err)
	}
	if got := e.count(t, `SELECT count(*) FROM local_payments WHERE settlement_id = $1::uuid AND flow = 'settlement_property_tax'`, cityID); got != 1 {
		t.Errorf("the tax was paid in units: %d local payments", got)
	}
	if cash() != c2 || holding(resident.ID) >= u2 {
		t.Errorf("the owner paid in units, not SUP: cash %d -> %d, units %d -> %d", c2, cash(), u2, holding(resident.ID))
	}
	if n := e.count(t, `SELECT count(*) FROM settlement_property_tax WHERE settlement_id = $1::uuid AND paid_at IS NULL`, cityID); n != 0 {
		t.Errorf("%d tax rows unpaid", n)
	}

	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Citizen || v.CitizenInvariants.LotSaleMismatched != 0 || v.CitizenInvariants.LotSaleLedger != v.CitizenInvariants.LotSaleRows ||
		v.CitizenInvariants.PermitLedger != v.CitizenInvariants.PermitRows || v.CitizenInvariants.BuildingMismatched != 0 {
		t.Errorf("the citizen invariants: %+v", v.CitizenInvariants)
	}
	i := v.VillageInvariants
	if i.LocalMismatched != 0 || i.DeskMismatched != 0 || i.LocalLedgerCollect != i.LocalRowsCollect || i.SupplyMismatched != 0 {
		t.Errorf("the local invariants: %+v", i)
	}
}
