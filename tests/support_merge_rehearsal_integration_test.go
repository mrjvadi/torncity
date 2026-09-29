//go:build integration

// The rehearsal of the Support merge (docs/adr/0032-support-merge.md).
//
// TestSupportMerge proves the mechanics on a small hand-built world. This test
// proves the merge against a world the game itself made: it loads the legacy
// content (seven cities), then drives the real handlers and repositories in
// all seven cities - founding companies (two with the same name), buying
// property, shopping, resting market orders and auctions, journeys in flight,
// jail and hospital stays, recruitment campaigns with their advertising fees,
// office holders, city periods that spend budgets - and runs the full
// `admin economy verify` before the merge, after it, after it is rolled back
// and after it is applied again. Every one of those runs must be green.
//
// Nothing here uses the helpers that register cleanups (insertPlayer,
// crimePlayer, newFWorld ...): the database is a scratch one and is dropped.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/content/testworld"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/operator"
)

var legacyCityCodes = []string{"ostmarch", "fenwick_span", "aldrin_hollow", "brennhaven", "kessmoor", "calderis", "vantor_reach"}

// legacyPack is the world before the merge: the shipped content with the
// seven cities added back and Support taken out again.
func legacyPack(t *testing.T) *content.Pack {
	t.Helper()
	pack, err := content.Load("../configs/content")
	if err != nil {
		t.Fatal(err)
	}
	pack = testworld.Extend(pack)
	cities := pack.Cities[:0:0]
	for _, c := range pack.Cities {
		if c.Code != "support" {
			cities = append(cities, c)
		}
	}
	pack.Cities = cities
	routes := pack.Routes[:0:0]
	for _, r := range pack.Routes {
		if r.From != "support" && r.To != "support" {
			routes = append(routes, r)
		}
	}
	pack.Routes = routes
	cm := pack.CompanyMarkets[:0:0]
	for _, m := range pack.CompanyMarkets {
		if m.City != "support" {
			cm = append(cm, m)
		}
	}
	pack.CompanyMarkets = cm
	pm := pack.PropertyMarkets[:0:0]
	for _, m := range pack.PropertyMarkets {
		if m.City != "support" {
			pm = append(pm, m)
		}
	}
	pack.PropertyMarkets = pm
	for i := range pack.Recruitment {
		rc := pack.Recruitment[i].Cities[:0:0]
		for _, c := range pack.Recruitment[i].Cities {
			if c.City != "support" {
				rc = append(rc, c)
			}
		}
		pack.Recruitment[i].Cities = rc
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("the legacy world does not validate: %v", err)
	}
	return pack
}

// rehearsal is the handlers over the scratch database and a clock the test moves.
type rehearsal struct {
	t        *testing.T
	pool     *postgres.Pool
	registry *content.Registry
	uow      application.UnitOfWork
	cities   application.CityRepository
	policy   application.PolicyReader

	mu    sync.Mutex
	clock time.Time

	companies *handlers.CompaniesHandler
	property  *handlers.PropertyHandler
	shops     *handlers.ShopsHandler
	market    *handlers.MarketHandler
	auctions  *handlers.AuctionsHandler
	travel    *handlers.TravelHandler
	recruit   *handlers.RecruitHandler
	civic     *handlers.CityHandler

	cityID map[string]string
	seq    int
}

func (r *rehearsal) now() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.clock }
func (r *rehearsal) advance(d time.Duration) {
	r.mu.Lock()
	r.clock = r.clock.Add(d)
	r.mu.Unlock()
}

func (r *rehearsal) meta(p *application.Player, command string) envelope.Metadata {
	m := validMeta(r.t)
	m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
	m.IdempotencyKey = "it-" + randomToken(r.t, 16)
	return m
}

func (r *rehearsal) scheduler(command string) envelope.Metadata {
	m := validMeta(r.t)
	m.TelegramUserID, m.Command = 0, command
	return m
}

func (r *rehearsal) nonce() string { return strings.ReplaceAll(newUUID(r.t), "-", "")[:12] }

func (r *rehearsal) exec(sql string, args ...any) {
	r.t.Helper()
	if _, err := r.pool.Raw().Exec(testCtx(r.t), sql, args...); err != nil {
		r.t.Fatalf("%v\n%.200s", err, sql)
	}
}

func (r *rehearsal) count(sql string, args ...any) int64 {
	r.t.Helper()
	var n int64
	if err := r.pool.Raw().QueryRow(testCtx(r.t), sql, args...).Scan(&n); err != nil {
		r.t.Fatalf("%v\n%.200s", err, sql)
	}
	return n
}

// player makes a player who lives in the city, with cash, standing at place.
func (r *rehearsal) player(city string, cash int64, place string) *application.Player {
	r.t.Helper()
	ctx := testCtx(r.t)
	p := &application.Player{TelegramUserID: newTelegramUserID(r.t), DisplayName: "rehearsal", Language: "en", Status: "active"}
	if err := postgres.NewPlayerRepository(r.pool, testDefaultLanguage).Create(ctx, p); err != nil {
		r.t.Fatalf("creating a player: %v", err)
	}
	now := r.now()
	r.exec(`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = $3, place_since = $4,
	          created_at = $5, last_active_at = $6 WHERE id = $1::uuid`,
		p.ID, r.cityID[city], place, now.Add(-time.Hour), now.Add(-30*24*time.Hour), now.Add(-time.Minute))
	if _, err := postgres.NewStatsRepository(r.pool).EnsureDefaults(ctx, p.ID, application.Stats{
		PlayerID: p.ID, Level: 5, Health: 100, MaxHealth: 100, Energy: 100, MaxEnergy: 100, UpdatedAt: now,
	}); err != nil {
		r.t.Fatal(err)
	}
	if cash > 0 {
		grantCash(r.t, r.pool, p.ID, cash)
	}
	cid := r.cityID[city]
	p.CityID = &cid
	return p
}

func (r *rehearsal) seat(p *application.Player, office, jurisdictionID string, seat int) {
	r.t.Helper()
	if err := r.uow.Do(testCtx(r.t), func(ctx context.Context, tx application.Tx) error {
		_, _, err := application.AppointToOffice(ctx, tx, office, jurisdictionID, seat, p.ID, r.now())
		return err
	}); err != nil {
		r.t.Fatalf("seating in %s: %v", office, err)
	}
}

func (r *rehearsal) jurisdictionOf(city string) string {
	var j string
	if err := r.pool.Raw().QueryRow(testCtx(r.t), `SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, r.cityID[city]).Scan(&j); err != nil {
		r.t.Fatal(err)
	}
	return j
}

// fare reads the price of the bus to a city off the options screen, as a
// player would.
func (r *rehearsal) busFare(p *application.Player, to string) int64 {
	r.t.Helper()
	resp, err := r.travel.Options(testCtx(r.t), travelMeta(p, "req-options-"+randomToken(r.t, 6)), handlers.TravelOptionsRequest{City: to})
	if err != nil {
		r.t.Fatalf("options: %v", err)
	}
	prefix := "travel:start:" + to + ":bus:"
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, prefix) {
				var fare int64
				fmt.Sscanf(strings.TrimPrefix(b.CallbackData, prefix), "%d", &fare) //nolint:errcheck // a miss is caught below
				if fare > 0 {
					return fare
				}
			}
		}
	}
	r.t.Fatalf("no priced bus to %s: %q", to, resp.Text)
	return 0
}

// verify runs the full economy verifier and fails, naming the failed checks,
// when any of them does.
func (r *rehearsal) verify(label string) {
	r.t.Helper()
	v, err := postgres.NewEconomyAdmin(r.pool).VerifyLedger(testCtx(r.t), 20)
	if err != nil {
		r.t.Fatalf("%s: verifying: %v", label, err)
	}
	list := operator.VerifyChecks(v, nil)
	if !operator.AllHold(v, list) {
		var bad []string
		for _, c := range list {
			if !c.OK {
				bad = append(bad, c.Text+" "+strings.Join(c.Details, "; "))
			}
		}
		r.t.Fatalf("%s: the economy does not verify: %v\n%+v", label, bad, v)
	}
	r.t.Logf("%s: all %d checks hold (%d accounts, %d entries)", label, len(list), v.Accounts, v.Entries)
}

// settleCity runs a city's period to its end, once, with the real handler.
func (r *rehearsal) settleCity(city string) {
	r.t.Helper()
	ctx := testCtx(r.t)
	id := r.cityID[city]
	if err := r.civic.StartClock(ctx, id); err != nil {
		r.t.Fatal(err)
	}
	var action, payload string
	var at time.Time
	if err := r.pool.Raw().QueryRow(ctx, `SELECT g.id::text, g.payload::text, g.finish_at FROM city_clocks c
	   JOIN game_actions g ON g.id = c.action_id WHERE c.city_id = $1::uuid`, id).Scan(&action, &payload, &at); err != nil {
		r.t.Fatalf("the city's clock: %v", err)
	}
	if wait := at.Sub(r.now()); wait > 0 {
		r.advance(wait + time.Second)
	}
	req := handlers.CrimeScheduledRequest{ActionID: action, ReferenceID: id, Payload: []byte(payload)}
	if _, err := r.civic.Settle(ctx, r.scheduler("city.settle"), req); err != nil {
		r.t.Fatalf("settling %s: %v", city, err)
	}
}

func newRehearsal(t *testing.T, s *scratch) *rehearsal {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.New(ctx, s.dsn)
	if err != nil {
		t.Fatalf("opening a pool on the scratch database: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := postgres.NewContentStore(pool).Apply(ctx, legacyPack(t), postgres.ApplyRequest{
		Actor: "rehearsal", Reason: "the seven-city world before the Support merge (docs/adr/0032)"}); err != nil {
		t.Fatalf("loading the legacy world: %v", err)
	}
	registry := workRegistry(t, pool)
	r := &rehearsal{t: t, pool: pool, registry: registry, clock: time.Now().UTC(), cityID: map[string]string{}}
	r.uow = postgres.NewUnitOfWork(pool, testDefaultLanguage)
	r.cities = postgres.NewCityRepository(pool)
	r.policy = postgres.NewPolicyReader(pool, nil)
	for _, code := range legacyCityCodes {
		r.cityID[code] = cityIDByCode(t, pool, code)
	}

	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	citizens := company.CitizenRules{ShiftsPerPeriod: 2, ProductivityBPS: 7000}
	scale := gametime.Scale(gameScale)
	r.companies = handlers.NewCompaniesHandler(r.uow, workIDs{t}, nil, registry, r.cities, r.policy,
		postgres.NewPlayerSearchRepository(pool), gameScale, handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2,
			NameMin: 3, NameMax: 24, FoundingShares: 1000, InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5,
			PriceStepBPS: 1000, Limits: limits, Citizens: citizens, CitizenLabourShareBPS: 500}, time.Hour, r.now)
	r.property = handlers.NewPropertyHandler(r.uow, workIDs{t}, nil, registry, r.cities, r.policy, gameScale,
		handlers.PropertyRules{ForeclosurePeriods: 3, EvictionPeriods: 2, MaxOwned: 5, MaxPrice: 100_000_000,
			MaxRent: 1_000_000, RestCooldown: 8 * time.Hour, ListSize: 10}, time.Hour, r.now)
	r.shops = handlers.NewShopsHandler(r.uow, workIDs{t}, nil, registry, r.cities, r.policy, scale, &crimeDice{}, time.Hour, r.now)
	r.market = handlers.NewMarketHandler(r.uow, workIDs{t}, nil, registry, r.cities, r.policy, scale,
		handlers.MarketLimits{OrderTTL: 48 * time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, r.now)
	r.auctions = handlers.NewAuctionsHandler(r.uow, workIDs{t}, nil, registry, r.cities, r.policy, scale,
		handlers.AuctionRules{Durations: []time.Duration{48 * time.Hour}, MaxReserve: 1_000_000, StepBPS: 500, MinStep: 10, MaxOpen: 5,
			ReservesBPS: []int{5000, 10000}}, 10, time.Hour, r.now)
	r.travel = handlers.NewTravelHandler(r.uow, transportIDs{t}, nil, r.cities, snapshotNetwork{registry.Current()},
		postgres.NewPolicyReader(pool, r.now), 60, 25, 24*time.Hour, r.now)
	r.recruit = handlers.NewRecruitHandler(r.uow, workIDs{t}, nil, registry, r.cities, gameScale, handlers.RecruitRules{
		CheckEvery: 6 * time.Hour, Checks: 4, MaxCampaigns: 2, MaxPositions: 5, MaxCandidates: 6, MaxStaff: 10,
		Patience: 24 * time.Hour, Period: 24 * time.Hour, Limits: limits}, time.Hour, r.now)
	r.civic = handlers.NewCityHandler(r.uow, workIDs{t}, nil, registry, r.cities, r.policy, gameScale, 24*time.Hour,
		time.Hour, r.now).WithProperty(r.property)
	return r
}

func TestSupportMergeRehearsal(t *testing.T) {
	s := newScratch(t)
	s.applyUpTo("0047")
	r := newRehearsal(t, s)
	ctx := testCtx(t)
	up47, up48, down48 := migrationSQL(t, "0047", "up"), migrationSQL(t, "0048", "up"), migrationSQL(t, "0048", "down")
	_ = up47

	// The world starts on the old money, exactly like production: apply the
	// currency migration only after the ledger holds IRR history? The
	// handlers post in DefaultCurrency (SUP), so 0047 runs first here and the
	// relabel itself is covered by TestSupportMerge.
	s.exec(up47)

	var techOwners []*application.Player
	for i, code := range legacyCityCodes {
		j := r.jurisdictionOf(code)
		mayor := r.player(code, 0, "city_hall")
		r.seat(mayor, "mayor", j, 1)
		for k := 0; k < 2; k++ {
			r.seat(r.player(code, 0, "city_hall"), "city_council", j, k+1)
		}

		// A company in every city; two of them are called the same.
		owner := r.player(code, 2_000_000, "city_hall")
		name := []string{"Acme Foods", "Acme Foods", "Grocers Beta", "Grocers Gamma", "Grocers Delta", "Grocers Zeta", "Grocers Eta"}[i]
		resp, err := r.companies.Found(ctx, r.meta(owner, "company.found"),
			handlers.CompanyRequest{Type: "grocery", Method: "cash", Name: name})
		if err != nil {
			t.Fatalf("founding in %s: %v", code, err)
		}
		if r.count(`SELECT count(*) FROM companies WHERE owner_player_id = $1::uuid`, owner.ID) != 1 {
			t.Fatalf("no company was founded in %s: %q", code, resp.Text)
		}

		// A home, bought from the city.
		home := r.player(code, 500_000, "city_hall")
		if _, err := r.property.Purchase(ctx, r.meta(home, "property.purchase"),
			handlers.PropertyRequest{Type: "studio", Method: "cash"}); err != nil {
			t.Fatalf("buying in %s: %v", code, err)
		}

		// Shopping fills a shelf; a resting sell order and, in some cities,
		// a resting buy order (its money in escrow).
		seller := r.player(code, 3_000, "bazaar")
		if _, err := r.shops.Buy(ctx, r.meta(seller, "shop.buy"),
			handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: "3", Method: "cash", Nonce: r.nonce()}); err != nil {
			t.Fatalf("shop in %s: %v", code, err)
		}
		if _, err := r.market.Order(ctx, r.meta(seller, "market.order"),
			handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "2", Price: "900", Nonce: r.nonce()}); err != nil {
			t.Fatalf("sell order in %s: %v", code, err)
		}
		if i%2 == 0 {
			buyer := r.player(code, 5_000, "bazaar")
			if _, err := r.market.Order(ctx, r.meta(buyer, "market.order"),
				handlers.MarketRequest{Side: "buy", Item: "bread", Qty: "1", Price: "100", Method: "cash", Nonce: r.nonce()}); err != nil {
				t.Fatalf("buy order in %s: %v", code, err)
			}
		}

		// Auctions in two cities.
		if i < 2 {
			ah := r.player(code, 5_000, "business_district")
			if _, err := r.shops.Buy(ctx, r.meta(ah, "shop.buy"),
				handlers.ShopRequest{Shop: "electronics_store", Item: "phone", Qty: "1", Method: "cash", Nonce: r.nonce()}); err != nil {
				t.Fatalf("phone in %s: %v", code, err)
			}
			var serial string
			if err := r.pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'phone'`, ah.ID).Scan(&serial); err != nil {
				t.Fatalf("the phone in %s: %v", code, err)
			}
			if _, err := r.auctions.New(ctx, r.meta(ah, "auction.new"),
				handlers.AuctionRequest{Item: serial, Reserve: "1000", Duration: "0", Nonce: r.nonce()}); err != nil {
				t.Fatalf("auction in %s: %v", code, err)
			}
			var no int64
			if err := r.pool.Raw().QueryRow(ctx, `SELECT no FROM auctions WHERE seller_id = $1::uuid`, ah.ID).Scan(&no); err != nil {
				t.Fatalf("the auction in %s: %v", code, err)
			}
			bidder := r.player(code, 5_000, "business_district")
			if _, err := r.auctions.Bid(ctx, r.meta(bidder, "auction.bid"),
				handlers.AuctionRequest{No: fmt.Sprint(no), Amount: "1000", Nonce: r.nonce(), Method: "cash"}); err != nil {
				t.Fatalf("bid in %s: %v", code, err)
			}
		}

		// A journey in flight from four cities.
		if i < 4 {
			rider := r.player(code, 5_000, "bus_terminal")
			to := legacyCityCodes[(i+1)%4]
			fare := r.busFare(rider, to)
			if _, err := r.travel.Start(ctx, travelMeta(rider, "req-go"), handlers.StartTravelRequest{
				City: to, Mode: "bus", Max: fmt.Sprint(fare), Method: "cash"}); err != nil {
				t.Fatalf("journey from %s: %v", code, err)
			}
		}

		// Jail in three cities, hospital in three others.
		if i%2 == 1 {
			p := r.player(code, 100, "police_station")
			r.jail(p, code)
		}
		if i%3 == 0 {
			p := r.player(code, 100, "hospital")
			r.admit(p, code)
		}

		if i == 2 || i == 5 {
			techOwners = append(techOwners, r.player(code, 3_000_000, "city_hall"))
		}
	}

	// Recruitment: two campaigns advertise in their country's cities, so ad
	// fees land in several city treasuries.
	for _, owner := range techOwners {
		r.campaign(owner)
	}

	// City periods spend budgets from the treasuries the fees filled.
	r.settleCity("ostmarch")
	r.settleCity("fenwick_span")
	r.settleCity("kessmoor")

	// A company market period: the NPC population buys from the companies.
	r.settleCompanyMarket("ostmarch")

	before := r.state()
	t.Logf("the rehearsal world: %+v", before)
	if before.companies < 9 || before.properties < 7 || before.orders < 7 || before.auctions < 2 || before.travels < 4 ||
		before.jails < 3 || before.stays < 3 || before.adFees < 6 || before.shelves < 7 || before.treasuries < 7 ||
		before.holders < 21 || before.clocks < 3 {
		t.Fatalf("the rehearsal did not produce a realistic world: %+v", before)
	}
	r.verify("before the merge")

	// --- the merge ---------------------------------------------------------
	s.exec(up48)
	r.verify("after the merge")
	after := r.state()
	if after.companies != before.companies || after.properties != before.properties || after.orders != before.orders ||
		after.auctions != before.auctions || after.travels != before.travels || after.jails != before.jails ||
		after.stays != before.stays || after.adFees != before.adFees || after.players != before.players ||
		after.cash != before.cash || after.escrow != before.escrow {
		t.Fatalf("the merge changed what players own:\n before %+v\n after  %+v", before, after)
	}
	sup := s.str(`SELECT id::text FROM cities WHERE code = 'support'`)
	for _, q := range []string{
		`SELECT count(*) FROM players WHERE city_id <> $1 OR residence_city_id <> $1`,
		`SELECT count(*) FROM companies WHERE city_id <> $1`,
		`SELECT count(*) FROM properties WHERE city_id <> $1`,
		`SELECT count(*) FROM market_orders WHERE city_id <> $1`,
		`SELECT count(*) FROM auctions WHERE city_id <> $1`,
		`SELECT count(*) FROM jail_sentences WHERE city_id <> $1`,
		`SELECT count(*) FROM hospital_stays WHERE city_id <> $1`,
	} {
		if n := s.int64(q, sup); n != 0 {
			t.Errorf("%d rows are not in Support: %s", n, q)
		}
	}
	if n := s.int64(`SELECT count(*) FROM companies WHERE name = 'Acme Foods' AND status = 'active'`); n != 1 {
		t.Errorf("%d active companies still share the name", n)
	}
	if orphans := s.referencesToLegacy(); len(orphans) != 0 {
		t.Errorf("rows still name a legacy city: %v", orphans)
	}
	// --- rolled back -------------------------------------------------------
	s.exec(down48)
	r.verify("after rolling the merge back")
	back := r.state()
	if back != before || back.companies != before.companies || back.cash != before.cash || back.clocks != before.clocks ||
		back.shelves != before.shelves || back.holders != before.holders {
		t.Fatalf("the rollback did not restore the world:\n before %+v\n back   %+v", before, back)
	}

	// --- and applied again -------------------------------------------------
	s.exec(up48)
	r.verify("after merging again")
}

// state counts what the merge must not lose or duplicate.
type rehearsalState struct {
	players, companies, properties, orders, auctions, travels, jails, stays int64
	adFees, shelves, treasuries, holders, clocks                            int64
	cash, escrow                                                            int64
}

func (r *rehearsal) state() rehearsalState {
	r.t.Helper()
	return rehearsalState{
		players:    r.count(`SELECT count(*) FROM players`),
		companies:  r.count(`SELECT count(*) FROM companies`),
		properties: r.count(`SELECT count(*) FROM properties`),
		orders:     r.count(`SELECT count(*) FROM market_orders WHERE status = 'open'`),
		auctions:   r.count(`SELECT count(*) FROM auctions WHERE status = 'open'`),
		travels:    r.count(`SELECT count(*) FROM travels WHERE status = 'in_transit'`),
		jails:      r.count(`SELECT count(*) FROM jail_sentences WHERE status = 'serving'`),
		stays:      r.count(`SELECT count(*) FROM hospital_stays WHERE status = 'admitted'`),
		adFees:     r.count(`SELECT count(*) FROM recruit_ad_fees`),
		shelves:    r.count(`SELECT count(*) FROM shop_shelves`),
		treasuries: r.count(`SELECT count(*) FROM accounts WHERE kind = 'city_treasury' AND balance > 0`),
		holders:    r.count(`SELECT count(*) FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id WHERE j.kind = 'city' AND o.holder_player_id IS NOT NULL`),
		clocks:     r.count(`SELECT count(*) FROM city_clocks`) + r.count(`SELECT count(*) FROM company_markets`),
		cash:       r.count(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'player_cash'`),
		escrow:     r.count(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'player_escrow'`),
	}
}

// jail puts a player in a serving sentence, as the crime handler does.
func (r *rehearsal) jail(p *application.Player, city string) {
	r.t.Helper()
	now := r.now()
	if err := r.uow.Do(testCtx(r.t), func(ctx context.Context, tx application.Tx) error {
		id, action := newUUID(r.t), newUUID(r.t)
		payload, _ := json.Marshal(handlers.CrimeActionPayload{ReferenceID: id, PlayerID: p.ID})
		ends := now.Add(6 * time.Hour)
		if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: action, ActionType: application.JailReleaseActionType,
			ActorType: "player", ActorID: p.ID, ReferenceType: application.CrimeReferenceSentence, ReferenceID: id,
			Payload: payload, StartedAt: now, FinishAt: ends}); err != nil {
			return err
		}
		return tx.Crime().Jail(ctx, application.JailSentence{ID: id, PlayerID: p.ID, CityID: r.cityID[city],
			Reason: application.SentenceForArrest, TermSeconds: 6 * 3600, GameActionID: action,
			Status: application.SentenceServing, StartsAt: now, EndsAt: ends})
	}); err != nil {
		r.t.Fatalf("jailing in %s: %v", city, err)
	}
}

// admit puts a player in a hospital bed.
func (r *rehearsal) admit(p *application.Player, city string) {
	r.t.Helper()
	now := r.now()
	if err := r.uow.Do(testCtx(r.t), func(ctx context.Context, tx application.Tx) error {
		id, action := newUUID(r.t), newUUID(r.t)
		payload, _ := json.Marshal(handlers.HospitalActionPayload{ReferenceID: id, PlayerID: p.ID})
		ends := now.Add(4 * time.Hour)
		if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: action, ActionType: application.HospitalDischargeActionType,
			ActorType: "player", ActorID: p.ID, ReferenceType: application.HospitalReference, ReferenceID: id,
			Payload: payload, StartedAt: now, FinishAt: ends}); err != nil {
			return err
		}
		return tx.Health().Admit(ctx, application.HospitalStay{ID: id, PlayerID: p.ID, CityID: r.cityID[city],
			Cause: application.CauseWork, Status: application.StayAdmitted, HealthIn: 40, HealthOut: 90,
			AdmittedAt: now, EndsAt: ends, GameActionID: action})
	}); err != nil {
		r.t.Fatalf("admitting in %s: %v", city, err)
	}
}

// campaign founds a technology studio, funds it and posts a nationwide
// campaign, so advertising fees are paid to every city of the country.
func (r *rehearsal) campaign(owner *application.Player) {
	r.t.Helper()
	ctx := testCtx(r.t)
	if _, err := r.companies.Found(ctx, r.meta(owner, "company.found"), handlers.CompanyRequest{
		Type: "tech_studio", Method: "cash", Name: "Labs " + randomToken(r.t, 6)}); err != nil {
		r.t.Fatalf("founding a studio: %v", err)
	}
	var id, code string
	if err := r.pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid ORDER BY founded_at DESC LIMIT 1`,
		owner.ID).Scan(&id, &code); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.companies.Deposit(ctx, r.meta(owner, "company.deposit"), handlers.CompanyRequest{Company: code, Method: "cash", Amount: "900000"}); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.recruit.New(ctx, r.meta(owner, "company.rnew"), handlers.RecruitRequest{Company: code, Skill: "engineering", Level: "3"}); err != nil {
		r.t.Fatal(err)
	}
	var no int64
	var campaignID string
	if err := r.pool.Raw().QueryRow(ctx, `SELECT no, id::text FROM recruit_campaigns WHERE company_id = $1::uuid AND status = 'draft'`, id).Scan(&no, &campaignID); err != nil {
		r.t.Fatalf("no draft campaign: %v", err)
	}
	for _, set := range []handlers.RecruitRequest{
		{No: fmt.Sprint(no), Field: "scope", Value: "nation"},
		{No: fmt.Sprint(no), Field: "salary", Value: "2"},
		{No: fmt.Sprint(no), Field: "signing", Value: "1"},
		{No: fmt.Sprint(no), Field: "relocation", Value: "2"},
	} {
		if _, err := r.recruit.Set(ctx, r.meta(owner, "company.rset"), set); err != nil {
			r.t.Fatal(err)
		}
	}
	if _, err := r.recruit.Post(ctx, r.meta(owner, "company.rpost"), handlers.RecruitRequest{No: fmt.Sprint(no), Confirm: "yes"}); err != nil {
		r.t.Fatal(err)
	}
	// One check: candidates apply, specialist pools draw down.
	var action string
	var nextAt time.Time
	var done int
	if err := r.pool.Raw().QueryRow(ctx, `SELECT action_id::text, next_check_at, checks_done FROM recruit_campaigns WHERE id = $1::uuid`,
		campaignID).Scan(&action, &nextAt, &done); err != nil {
		r.t.Fatalf("no check scheduled: %v", err)
	}
	if wait := nextAt.Sub(r.now()); wait > 0 {
		r.advance(wait + time.Second)
	}
	payload, _ := json.Marshal(handlers.RecruitCheckPayload{CampaignID: campaignID, CheckNo: done + 1})
	if _, err := r.recruit.Check(ctx, r.scheduler("company.rcheck"), handlers.CrimeScheduledRequest{ActionID: action,
		ReferenceType: application.RecruitCampaignReference, ReferenceID: campaignID, Payload: payload}); err != nil {
		r.t.Fatal(err)
	}
}

// settleCompanyMarket runs a city's company period, so the NPC population
// buys from its companies.
func (r *rehearsal) settleCompanyMarket(city string) {
	r.t.Helper()
	ctx := testCtx(r.t)
	id := r.cityID[city]
	var periodNo int64
	var action string
	var nextAt time.Time
	if err := r.pool.Raw().QueryRow(ctx, `SELECT period_no, action_id::text, next_at FROM company_markets WHERE city_id = $1::uuid`, id).
		Scan(&periodNo, &action, &nextAt); err != nil {
		r.t.Fatalf("the company clock of %s: %v", city, err)
	}
	if wait := nextAt.Sub(r.now()); wait > 0 {
		r.advance(wait + time.Second)
	}
	payload, _ := json.Marshal(handlers.CompanyPeriodPayload{CityID: id, PeriodNo: periodNo})
	if _, err := r.companies.Settle(ctx, r.scheduler("company.settle"), handlers.CrimeScheduledRequest{ActionID: action,
		ReferenceType: application.CompanyMarketReference, ReferenceID: id, Payload: payload}); err != nil {
		r.t.Fatalf("settling companies in %s: %v", city, err)
	}
}
