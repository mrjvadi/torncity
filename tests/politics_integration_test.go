//go:build integration

// Integration tests of stage F politics (migration 0024,
// docs/adr/0024-property-and-politics.md), through the real handlers, unit
// of work, resolver and ledger:
//
//	The mayor of Aldrin Hollow proposes a budget: police, public transport,
//	defence. The council is seated, so it goes to their vote instead of into
//	force. One councillor votes yes (twice: one vote counts), one no; the
//	third yes passes it at once, and the allocation is announced once,
//	through SetPolicy's record. The window's close finds it decided and does
//	nothing. After the notice the city's period ends: the treasury pays each
//	line its share of the budget, once, the defence line into the country's
//	defence fund, and the bus fare from the city falls by what the transit
//	line bought. A large tax change goes to the council; a small one does
//	not.
//
//	A presidential election opened in a country is announced to every city
//	of it.
package tests

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/budget"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

const politicsCity = "aldrin_hollow"

func TestBudgetPassedByTheCouncilPaysItsLines(t *testing.T) {
	w := newFWorld(t, politicsCity)
	ctx := testCtx(t)
	def, _ := w.snap.Budget()

	mayor := w.resident(0)
	councillors := []*application.Player{w.resident(0), w.resident(0), w.resident(0)}
	w.seat(mayor, "mayor", w.city.JurisdictionID, 1)
	for i, c := range councillors {
		w.seat(c, "city_council", w.city.JurisdictionID, i+1)
	}

	// The fare before any budget: the bus to Kessmoor.
	travel := handlers.NewTravelHandler(w.uow, transportIDs{t}, nil, w.cities, snapshotNetwork{w.snap},
		postgres.NewPolicyReader(w.pool, w.now), 60, 25, time.Hour, w.now)
	rider := travelPlayer(t, w.pool, w.city.ID, 0)
	t.Cleanup(func() { purgeLedgerFor(t, w.pool, rider.ID) })
	fareBefore := busFare(t, travel, rider, "kessmoor")

	// The mayor proposes: police 30%, transit 50%, defence 10%, in the
	// categories' order (police, hospital, transit, education,
	// infrastructure, marketing, defence), steps of 5%.
	draft := "60a0002"
	resp, err := w.gov.AllocSet(ctx, w.meta(mayor, "gov.allocset"),
		handlers.GovAllocRequest{Lever: def.Lever, Place: w.city.Code, Draft: draft})
	w.ok("propose the budget", resp, err)
	no := w.billNo(w.city.JurisdictionID, def.Lever)
	if s := w.billStatus(no); s != application.ProposalOpen {
		t.Fatalf("the budget's proposal is %s, want open", s)
	}
	if n := w.count(`SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid AND lever_code = $2`,
		w.city.JurisdictionID, def.Lever); n != 0 {
		t.Fatalf("a budget the council must confirm was set %d times before its vote", n)
	}
	// A second proposal of the same budget is refused while one is open.
	resp, err = w.gov.AllocSet(ctx, w.meta(mayor, "gov.allocset"),
		handlers.GovAllocRequest{Lever: def.Lever, Place: w.city.Code, Draft: "2000000"})
	w.ok("propose again", resp, err)
	if n := w.count(`SELECT count(*) FROM proposals WHERE jurisdiction_id = $1::uuid AND subject = $2 AND status = 'open'`,
		w.city.JurisdictionID, def.Lever); n != 1 {
		t.Fatalf("%d open proposals of the budget, want 1", n)
	}
	// Only a member votes.
	if resp, err := w.leg.Vote(ctx, w.meta(mayor, "law.vote"), handlers.LegislatureRequest{No: fmt.Sprint(no),
		Vote: application.VoteYes}); err != nil || resp == nil {
		t.Fatalf("a non-member's vote: %v", err)
	}

	// Yes, the same press again, then no: still open.
	first := w.vote(councillors[0], no, application.VoteYes)
	if _, err := w.leg.Vote(ctx, first, handlers.LegislatureRequest{No: fmt.Sprint(no), Vote: application.VoteYes}); err != nil {
		t.Fatalf("a redelivered vote: %v", err)
	}
	w.vote(councillors[0], no, application.VoteNo) // a second press of the other button: already voted
	w.vote(councillors[1], no, application.VoteNo)
	if n := w.count(`SELECT count(*) FROM proposal_votes v JOIN proposals p ON p.id = v.proposal_id WHERE p.no = $1`, no); n != 2 {
		t.Fatalf("%d votes recorded, want 2 (one per member)", n)
	}
	if s := w.billStatus(no); s != application.ProposalOpen {
		t.Fatalf("after one yes and one no the proposal is %s, want open", s)
	}
	// The third member's yes settles it: 2 to 1.
	w.vote(councillors[2], no, application.VoteYes)
	if s := w.billStatus(no); s != application.ProposalPassed {
		t.Fatalf("after 2 yes and 1 no the proposal is %s, want passed", s)
	}
	if n := w.count(`SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid AND lever_code = $2
	   AND value_kind = 'structured'`, w.city.JurisdictionID, def.Lever); n != 1 {
		t.Fatalf("the passed budget was set %d times, want once", n)
	}
	if n := w.count(`SELECT count(*) FROM policy_changes WHERE jurisdiction_id = $1::uuid AND lever_code = $2
	   AND set_by_player_id = $3::uuid AND office_code = 'mayor'`, w.city.JurisdictionID, def.Lever, mayor.ID); n != 1 {
		t.Fatalf("%d public records of the budget in the mayor's name, want 1", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox WHERE subject LIKE 'game.event.legislature.decided.%'
	   AND payload->>'no' = $1`, fmt.Sprint(no)); n != 1 {
		t.Fatalf("%d decisions announced, want 1", n)
	}

	// The window's close finds it decided.
	var closeID, bid string
	if err := w.pool.Raw().QueryRow(ctx, `SELECT close_action_id::text, id::text FROM proposals WHERE no = $1`, no).
		Scan(&closeID, &bid); err != nil {
		t.Fatal(err)
	}
	w.advance(48*time.Hour + time.Minute)
	if _, err := w.leg.Close(ctx, w.scheduler("law.close"), handlers.CrimeScheduledRequest{ActionID: closeID,
		ReferenceID: bid}); err != nil {
		t.Fatalf("closing a decided proposal: %v", err)
	}
	if n := w.count(`SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid AND lever_code = $2`,
		w.city.JurisdictionID, def.Lever); n != 1 {
		t.Fatalf("the close set the budget again (%d rows)", n)
	}

	// The notice has passed: the allocation is in force.
	v, err := postgres.NewPolicyReader(w.pool, w.now).Get(ctx, w.city.JurisdictionID, def.Lever)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"police": 3000, "transit": 5000, "defence": 1000}
	if !application.SameAllocation(v.Allocation, want) {
		t.Fatalf("the budget in force is %v, want %v", v.Allocation, want)
	}

	// The city's period pays each line, once.
	w.fundTreasury(200_000)
	before := w.treasury()
	country := cityCountry(t, w.pool, w.city.ID)
	fundBefore := cashBalance(t, w.pool, application.AccountDefenceFund, country)
	w.settleCity()
	out := budget.Spend(def.Rules(), before, want)
	if got := before - w.treasury(); got != out.Spent {
		t.Fatalf("the treasury paid %d, want %d", got, out.Spent)
	}
	if got := cashBalance(t, w.pool, application.AccountDefenceFund, country) - fundBefore; got != out.Defence {
		t.Fatalf("the defence fund gained %d, want %d", got, out.Defence)
	}
	if n := w.count(`SELECT count(*) FROM city_budget_periods WHERE city_id = $1::uuid AND ended_at >= $2`,
		w.city.ID, w.started); n != 1 {
		t.Fatalf("%d budget periods recorded, want 1", n)
	}

	if f := w.verify(); f.BudgetLedger == 0 || f.DefenceLedger == 0 {
		t.Fatalf("the invariants saw no budget money: %+v", f)
	}

	// Public transport is cheaper by what the transit line bought.
	fareAfter := busFare(t, travel, rider, "kessmoor")
	if fareAfter >= fareBefore {
		t.Fatalf("the bus fare is %d after the budget, was %d: the subsidy did nothing", fareAfter, fareBefore)
	}
}

// A small tax change is the mayor's alone; a large one goes to the council.
func TestLargeTaxChangeGoesToTheCouncil(t *testing.T) {
	w := newFWorld(t, politicsCity)
	ctx := testCtx(t)
	mayor := w.resident(0)
	member := w.resident(0)
	w.seat(mayor, "mayor", w.city.JurisdictionID, 1)
	w.seat(member, "city_council", w.city.JurisdictionID, 1)
	current, err := postgres.NewPolicyReader(w.pool, w.now).Get(ctx, w.city.JurisdictionID, "city.tax_rate")
	if err != nil {
		t.Fatal(err)
	}
	large := current.Value + 1000
	if large > 2500 {
		large = current.Value - 1000
	}
	resp, err := w.gov.Set(ctx, w.meta(mayor, "gov.set"), handlers.GovLeverRequest{Lever: "city.tax_rate",
		Place: w.city.Code, Value: fmt.Sprint(large)})
	w.ok("a large tax change", resp, err)
	no := w.billNo(w.city.JurisdictionID, "city.tax_rate")
	if w.billStatus(no) != application.ProposalOpen {
		t.Fatal("a large tax change was not put to the council")
	}
	// The one member's no rejects it; nothing is set.
	w.vote(member, no, application.VoteNo)
	if s := w.billStatus(no); s != application.ProposalFailed {
		t.Fatalf("the rejected change is %s, want failed", s)
	}
	if n := w.count(`SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid AND lever_code = 'city.tax_rate'
	   AND set_at >= $2`, w.city.JurisdictionID, w.started); n != 0 {
		t.Fatalf("a rejected change was set (%d rows)", n)
	}
	// A small change needs nobody.
	small := current.Value + 100
	resp, err = w.gov.Set(ctx, w.meta(mayor, "gov.set"), handlers.GovLeverRequest{Lever: "city.tax_rate",
		Place: w.city.Code, Value: fmt.Sprint(small)})
	w.ok("a small tax change", resp, err)
	if n := w.count(`SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid AND lever_code = 'city.tax_rate'
	   AND set_at >= $2 AND value = $3`, w.city.JurisdictionID, w.started, small); n != 1 {
		t.Fatalf("a small change was not set by the mayor alone (%d rows)", n)
	}
}

// A country's election is announced in the groups of every city of it.
func TestCountryElectionIsAnnouncedInEveryCity(t *testing.T) {
	w := newFWorld(t, politicsCity)
	ctx := testCtx(t)
	country := cityCountry(t, w.pool, w.city.ID)
	var cities int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT count(*) FROM cities c JOIN jurisdictions j ON j.id = c.jurisdiction_id
	   WHERE j.parent_id = $1::uuid`, country).Scan(&cities); err != nil {
		t.Fatal(err)
	}
	var e application.Election
	err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		if e, err = handlers.OpenElection(ctx, tx, workIDs{t}, w.snap, "president", country, w.now()); err != nil {
			return err
		}
		return handlers.AnnounceElectionOpened(ctx, tx, w.cities, w.scheduler("election.open"), e)
	})
	if errors.Is(err, application.ErrElectionUnderWay) {
		t.Skip("a presidential election is already under way here")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM outbox WHERE payload->>'election_id' = $1`,
			`DELETE FROM game_actions WHERE reference_type = 'elections' AND reference_id = $1::uuid`,
			`DELETE FROM elections WHERE id = $1::uuid`,
		} {
			if _, err := w.pool.Raw().Exec(context.Background(), stmt, e.ID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	var n int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT COALESCE(jsonb_array_length(payload->'city_ids'), 0) FROM outbox
	   WHERE subject LIKE 'game.event.election.opened.%' AND payload->>'election_id' = $1`, e.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != cities || n < 2 {
		t.Fatalf("the presidential election is announced to %d cities, want all %d of the country", n, cities)
	}
}

// cityCountry is the country a city belongs to.
func cityCountry(t *testing.T, pool *postgres.Pool, cityID string) string {
	t.Helper()
	var id string
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT j.parent_id::text FROM cities c
	   JOIN jurisdictions j ON j.id = c.jurisdiction_id WHERE c.id = $1::uuid`, cityID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A trade on Ostmarch's market between a resident of the Commonwealth
// (buying) and one of the Vantor Federation (selling) pays the
// Commonwealth's tariff, withheld from the seller's proceeds, into its
// national treasury, once; a trade agreement between the two would spare it.
func TestBorderTariffOnACrossBorderTrade(t *testing.T) {
	w := newFWorld(t, "ostmarch")
	ctx := testCtx(t)
	keepShelves(t, w.pool, w.city.ID)
	country := cityCountry(t, w.pool, w.city.ID)
	vantor := cityIDByCode(t, w.pool, "vantor_reach")

	// The president sets a 4% tariff, under parliament's threshold, and it
	// comes into force after its notice.
	president := w.resident(0)
	w.seat(president, "president", country, 1)
	if err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, err := application.SetPolicy(ctx, tx, president.ID, country, handlers.LeverBorderTariff, 400, w.now())
		return err
	}); err != nil {
		t.Fatalf("setting the tariff: %v", err)
	}
	w.advance(73 * time.Hour)
	policy := postgres.NewPolicyReader(w.pool, w.now)

	seller := w.resident(1_000)
	buyer := w.resident(5_000)
	for _, p := range []*application.Player{seller, buyer} {
		if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'bazaar', place_since = now() WHERE id = $1::uuid`,
			p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET residence_city_id = $2::uuid WHERE id = $1::uuid`,
		seller.ID, vantor); err != nil {
		t.Fatal(err)
	}
	scale := gametime.Scale(gameScale)
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, policy, scale, &crimeDice{}, time.Hour, w.now)
	market := handlers.NewMarketHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, policy, scale,
		handlers.MarketLimits{OrderTTL: time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, w.now)
	nonce := func() string { return strings.ReplaceAll(newUUID(t), "-", "")[:12] }
	w.do("the seller stocks up", func() (*presenter.Response, error) {
		return shops.Buy(ctx, w.meta(seller, "shop.buy"), handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: "2",
			Method: "cash", Nonce: nonce()})
	})
	w.do("a sell order", func() (*presenter.Response, error) {
		return market.Order(ctx, w.meta(seller, "market.order"), handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "2",
			Price: "1000", Nonce: nonce()})
	})
	treasury := cashBalance(t, w.pool, application.AccountStateTreasury, country)
	bank := cashBalance(t, w.pool, application.AccountPlayerBank, seller.ID)
	buy := w.meta(buyer, "market.order")
	req := handlers.MarketRequest{Side: "buy", Item: "bread", Qty: "2", Price: "1000", Method: "cash", Nonce: nonce()}
	w.do("a buy order", func() (*presenter.Response, error) { return market.Order(ctx, buy, req) })
	w.do("the same press", func() (*presenter.Response, error) { return market.Order(ctx, buy, req) })

	var fee int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(fee), 0) FROM market_trades WHERE buyer_id = $1::uuid`,
		buyer.ID).Scan(&fee); err != nil {
		t.Fatal(err)
	}
	const tariff = 2000 * 400 / 10000
	if got := cashBalance(t, w.pool, application.AccountStateTreasury, country) - treasury; got != tariff {
		t.Fatalf("the Commonwealth's treasury took %d in tariff, want %d", got, tariff)
	}
	if got := cashBalance(t, w.pool, application.AccountPlayerBank, seller.ID) - bank; got != 2000-fee-tariff {
		t.Fatalf("the seller received %d, want %d (the price less the fee and the tariff)", got, 2000-fee-tariff)
	}
	if n := w.count(`SELECT count(*) FROM border_tariffs WHERE importer_id = $1::uuid AND reference_type = 'market_trades'
	   AND at >= $2`, country, w.started); n != 1 {
		t.Fatalf("%d tariffs recorded, want 1", n)
	}
	if f := w.verify(); f.TariffLedger == 0 {
		t.Fatalf("the invariants saw no tariff: %+v", f)
	}
}
