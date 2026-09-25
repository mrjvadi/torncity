//go:build integration

// Integration tests of property (migration 0025,
// docs/adr/0024-property-and-politics.md), through the real handlers, unit of
// work, ledger and the city's period:
//
//	A player living in Ostmarch buys a studio flat in Fenwick Span from the
//	city at its land registry — once, for a double press — and the treasury
//	takes the price; the home makes Fenwick Span their residence. They offer
//	it to let. A tenant rents it: the first period's rent goes to the
//	landlord at once, recorded for the period, and the tenant moves their
//	residence too, and rests at home, once. The city's period ends: the
//	landlord pays upkeep and tax, the rent of the period already paid is not
//	taken again. The tenant cannot pay the next two periods and is evicted.
//	An owner with nothing left after buying a plot runs into debt, and after
//	three periods the city takes the plot back.
package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/property"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

const propertyCity = "fenwick_span"

func TestHomeBoughtLetRentCollectedEvictedForeclosed(t *testing.T) {
	w := newFWorld(t, propertyCity)
	ctx := testCtx(t)
	if _, ok := w.snap.PropertyMarket(propertyCity); !ok {
		t.Skip("the active content sells no property here; run `admin content load`")
	}
	studio, _ := w.snap.PropertyType("studio")
	ostmarch := cityIDByCode(t, w.pool, "ostmarch")

	landlord := w.resident(0)
	grant(t, w.pool, application.AccountPlayerBank, landlord.ID, 200_000)
	atRegistry := func(p *application.Player) {
		if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_hall', place_since = now() WHERE id = $1::uuid`,
			p.ID); err != nil {
			t.Fatal(err)
		}
	}
	atRegistry(landlord)
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET residence_city_id = $2::uuid WHERE id = $1::uuid`,
		landlord.ID, ostmarch); err != nil {
		t.Fatal(err)
	}

	// Bought from the city, once for a double press.
	treasuryBefore := w.treasury()
	buy := w.meta(landlord, "property.purchase")
	req := handlers.PropertyRequest{Type: "studio", Method: "card"}
	w.do("buy a studio", func() (*presenter.Response, error) { return w.property.Purchase(ctx, buy, req) })
	w.do("buy it again", func() (*presenter.Response, error) { return w.property.Purchase(ctx, buy, req) })
	var no, price int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT no, value FROM properties WHERE owner_player_id = $1::uuid`, landlord.ID).
		Scan(&no, &price); err != nil {
		t.Fatalf("the studio was not bought: %v", err)
	}
	if n := w.count(`SELECT count(*) FROM properties WHERE owner_player_id = $1::uuid`, landlord.ID); n != 1 {
		t.Fatalf("a double press bought %d studios", n)
	}
	if got := w.treasury() - treasuryBefore; got != price {
		t.Fatalf("the treasury took %d, want the price %d", got, price)
	}
	if n := w.count(`SELECT count(*) FROM players WHERE id = $1::uuid AND residence_city_id = $2::uuid
	   AND residence_since IS NOT NULL`, landlord.ID, w.city.ID); n != 1 {
		t.Fatal("a home bought did not move the buyer's residence")
	}

	// The operator's panel sees the home on the owner's card and the city's.
	panel := postgres.NewEconomyAdmin(w.pool)
	var code string
	if err := w.pool.Raw().QueryRow(ctx, `SELECT public_code FROM players WHERE id = $1::uuid`, landlord.ID).
		Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "" {
		card, err := panel.PlayerCard(ctx, code)
		if err != nil || len(card.Properties) != 1 || card.Residence != propertyCity {
			t.Fatalf("player card = %+v, %v; want the studio and residence here", card, err)
		}
	}
	city, err := panel.CityCard(ctx, propertyCity)
	if err != nil || city.Properties < 1 || city.Treasury != w.treasury() {
		t.Fatalf("city card = %+v, %v", city, err)
	}
	if _, err := panel.EconomyDashboard(ctx, 24*time.Hour, w.snap, w.now()); err != nil {
		t.Fatalf("dashboard: %v", err)
	}

	// Offered to let at 500 a period.
	w.do("offer to let", func() (*presenter.Response, error) {
		return w.property.Let(ctx, w.meta(landlord, "property.let"),
			handlers.PropertyRequest{No: fmt.Sprint(no), Price: "500"})
	})
	var offerNo int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT l.no FROM property_listings l JOIN properties p ON p.id = l.property_id
	   WHERE p.no = $1 AND l.status = 'open' AND l.kind = 'rent'`, no).Scan(&offerNo); err != nil {
		t.Fatalf("no offer to let: %v", err)
	}

	// Rented: the first rent at once, recorded for this period.
	tenant := w.resident(0)
	grantCash(t, w.pool, tenant.ID, 600)
	atRegistry(tenant)
	landlordBank := cashBalance(t, w.pool, application.AccountPlayerBank, landlord.ID)
	rent := w.meta(tenant, "property.rent")
	rreq := handlers.PropertyRequest{No: fmt.Sprint(offerNo), Method: "cash"}
	w.do("rent it", func() (*presenter.Response, error) { return w.property.Rent(ctx, rent, rreq) })
	w.do("rent it again", func() (*presenter.Response, error) { return w.property.Rent(ctx, rent, rreq) })
	if got := cashBalance(t, w.pool, application.AccountPlayerBank, landlord.ID) - landlordBank; got != 500 {
		t.Fatalf("the landlord got %d of the first rent, want 500", got)
	}
	if n := w.count(`SELECT count(*) FROM property_leases WHERE tenant_player_id = $1::uuid AND status = 'active'`, tenant.ID); n != 1 {
		t.Fatalf("%d active leases, want 1", n)
	}
	if n := w.count(`SELECT count(*) FROM rent_payments r JOIN property_leases l ON l.id = r.lease_id
	   WHERE l.tenant_player_id = $1::uuid`, tenant.ID); n != 1 {
		t.Fatalf("%d rent payments recorded, want 1", n)
	}
	if n := w.count(`SELECT count(*) FROM players WHERE id = $1::uuid AND residence_city_id = $2::uuid`, tenant.ID,
		w.city.ID); n != 1 {
		t.Fatal("a rented home did not move the tenant's residence")
	}

	// The tenant rests at home, once.
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = $2, place_since = now() WHERE id = $1::uuid`,
		tenant.ID, studio.Place); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE player_stats SET energy = 10 WHERE player_id = $1::uuid`, tenant.ID); err != nil {
		t.Fatal(err)
	}
	w.do("rest", func() (*presenter.Response, error) { return w.property.Rest(ctx, w.meta(tenant, "property.rest")) })
	w.do("rest again", func() (*presenter.Response, error) { return w.property.Rest(ctx, w.meta(tenant, "property.rest")) })
	var energy int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT energy FROM player_stats WHERE player_id = $1::uuid`, tenant.ID).Scan(&energy); err != nil {
		t.Fatal(err)
	}
	if energy != 10+studio.RestEnergy {
		t.Fatalf("energy after resting twice is %d, want %d (one rest)", energy, 10+studio.RestEnergy)
	}

	// A plot bought with every last coin.
	pauper := w.resident(0)
	atRegistry(pauper)
	plot, _ := w.snap.PropertyType("plot")
	var plotPrice int64
	{
		var sold int
		if err := w.pool.Raw().QueryRow(ctx, `SELECT count(*) FROM properties WHERE city_id = $1::uuid AND type_code = 'plot'
		   AND status = 'owned'`, w.city.ID).Scan(&sold); err != nil {
			t.Fatal(err)
		}
		m, _ := w.snap.PropertyMarket(propertyCity)
		def, _ := w.snap.Property()
		plotPrice = property.CityPrice(plot.Type(), m.PriceBPS, sold, def.Demand())
	}
	grantCash(t, w.pool, pauper.ID, plotPrice)
	w.do("buy a plot", func() (*presenter.Response, error) {
		return w.property.Purchase(ctx, w.meta(pauper, "property.purchase"),
			handlers.PropertyRequest{Type: "plot", Method: "cash"})
	})
	if n := w.count(`SELECT count(*) FROM properties WHERE owner_player_id = $1::uuid`, pauper.ID); n != 1 {
		t.Fatal("the plot was not bought")
	}

	// Period 1: upkeep and tax; the rent of this period is paid already.
	w.settleCity()
	if n := w.count(`SELECT count(*) FROM property_charges c JOIN properties p ON p.id = c.property_id
	   WHERE p.owner_player_id = $1::uuid`, landlord.ID); n != 1 {
		t.Fatalf("%d charges of the landlord's studio, want 1", n)
	}
	if n := w.count(`SELECT count(*) FROM rent_payments r JOIN property_leases l ON l.id = r.lease_id
	   WHERE l.tenant_player_id = $1::uuid`, tenant.ID); n != 1 {
		t.Fatalf("the settlement took this period's rent again (%d payments)", n)
	}
	var upkeepPaid, taxPaid int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT c.upkeep_paid, c.tax_paid FROM property_charges c
	   JOIN properties p ON p.id = c.property_id WHERE p.owner_player_id = $1::uuid`, landlord.ID).Scan(&upkeepPaid, &taxPaid); err != nil {
		t.Fatal(err)
	}
	if upkeepPaid != studio.Upkeep || taxPaid <= 0 {
		t.Fatalf("the landlord paid upkeep %d (want %d) and tax %d", upkeepPaid, studio.Upkeep, taxPaid)
	}

	// Periods 2 and 3: the tenant has 100 left and pays nothing; evicted.
	w.settleCity()
	w.settleCity()
	var status, reason string
	var arrears int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT status, COALESCE(end_reason, ''), arrears FROM property_leases
	   WHERE tenant_player_id = $1::uuid`, tenant.ID).Scan(&status, &reason, &arrears); err != nil {
		t.Fatal(err)
	}
	if status != application.LeaseEnded || reason != application.LeaseEvicted || arrears != 2 {
		t.Fatalf("the lease is %s (%s) with %d periods owed, want ended by eviction after 2", status, reason, arrears)
	}
	if got := cashBalance(t, w.pool, application.AccountPlayerCash, tenant.ID); got != 100 {
		t.Fatalf("the tenant holds %d, want 100: unpaid rent is never taken in part", got)
	}

	// The plot's owner has paid nothing for three periods: the city took it.
	var pstatus string
	if err := w.pool.Raw().QueryRow(ctx, `SELECT status FROM properties WHERE type_code = 'plot' AND city_id = $1::uuid
	   AND acquired_at >= $2 ORDER BY no DESC LIMIT 1`, w.city.ID, w.started).Scan(&pstatus); err != nil {
		t.Fatal(err)
	}
	if pstatus != application.PropertyRepossessed {
		t.Fatalf("the unpaid plot is %s, want repossessed", pstatus)
	}
	if n := w.count(`SELECT count(*) FROM outbox WHERE subject LIKE 'game.event.property.foreclosed.%'
	   AND payload->>'player_id' = $1`, pauper.ID); n != 1 {
		t.Fatalf("%d foreclosure notices, want 1", n)
	}
	if f := w.verify(); f.RentLedger == 0 || f.PropertyTaxLedger == 0 || f.PropertyUpkeepLedger == 0 || f.PurchaseTransactions == 0 {
		t.Fatalf("the invariants saw no property money: %+v", f)
	}
	for _, id := range []string{pauper.ID, tenant.ID} {
		if b := cashBalance(t, w.pool, application.AccountPlayerCash, id); b < 0 {
			t.Fatalf("a balance went negative: %d", b)
		}
	}
}
