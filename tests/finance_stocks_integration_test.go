//go:build integration

package tests

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// company founds a company for the owner the way founding leaves one — every
// share the founder's — with money in its treasury.
func (w *financeWorld) company(owner *application.Player, treasury int64) (id, code string) {
	w.t.Helper()
	ctx := testCtx(w.t)
	id = newUUID(w.t)
	code = strings.ToUpper(randomToken(w.t, 7))[:7]
	if _, err := w.pool.Raw().Exec(ctx, `INSERT INTO companies (id, code, name, name_key, type_code, city_id, owner_player_id,
		status, price_bps, total_shares, registration_fee, content_version, founded_at, updated_at)
		VALUES ($1::uuid, $2, $3, $4, 'grocery', $5::uuid, $6::uuid, 'active', 10000, 1000, 0, $7, $8, $8)`,
		id, code, "Exchange Test "+code, "exchangetest"+strings.ToLower(code), w.city.ID, owner.ID,
		w.registry.Current().Version(), w.now().Add(-240*time.Hour)); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.pool.Raw().Exec(ctx, `INSERT INTO company_shareholders (company_id, player_id, shares, acquired_at)
		VALUES ($1::uuid, $2::uuid, 1000, $3)`, id, owner.ID, w.now()); err != nil {
		w.t.Fatal(err)
	}
	ledger := postgres.NewLedgerRepository(w.pool)
	acct, err := ledger.AccountFor(ctx, application.AccountCompanyTreasury, id)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := ledger.Post(ctx, transfer(application.SystemSourceAccountID, acct.ID, treasury,
		application.ReasonAdminGrant)); err != nil {
		w.t.Fatal(err)
	}
	return id, code
}

// shares is what a player holds of a company: shares and locked.
func (w *financeWorld) shares(companyID, playerID string) (int64, int64) {
	w.t.Helper()
	var n, locked int64
	_ = w.pool.Raw().QueryRow(testCtx(w.t), `SELECT shares, locked FROM company_shareholders
		WHERE company_id = $1::uuid AND player_id = $2::uuid`, companyID, playerID).Scan(&n, &locked)
	return n, locked
}

// order places an order: the confirmation, then its button pressed twice.
func (w *financeWorld) order(p *application.Player, side, code string, qty, price int64) {
	w.t.Helper()
	ctx := testCtx(w.t)
	f := w.fin.BuyShares
	if side == "sell" {
		f = w.fin.SellShares
	}
	req := handlers.FinanceRequest{Code: code, Qty: strconv.FormatInt(qty, 10), Price: strconv.FormatInt(price, 10)}
	resp, err := f(ctx, w.meta(p, "stock."+side), req)
	w.ok("the order's confirmation", resp, err)
	req.Nonce = nonceOf(w.t, resp, "stock:"+side+":")
	for i := 0; i < 2; i++ {
		resp, err = f(ctx, w.meta(p, "stock."+side), req)
		w.ok("placing the order", resp, err)
	}
}

func TestCompanyListedTradedAndPaysADividend(t *testing.T) {
	w := newFinanceWorld(t, func(d *content.FinanceDef) { d.Stocks.MinAge, d.Stocks.MinRevenue = "0s", 0 })
	ctx := testCtx(t)
	founder := w.resident(0)
	buyer := w.resident(100_000)
	late := w.resident(100_000)
	t.Cleanup(func() { purgeCompaniesOf(t, w.pool, w.city.ID, founder.ID, buyer.ID, late.ID) })
	t.Cleanup(w.purge)
	companyID, code := w.company(founder, 50_000)

	// Listed: a tenth of the company offered at its book value per share,
	// the fee paid once from the company to its city.
	resp, err := w.fin.IPO(ctx, w.meta(founder, "stock.ipo"), handlers.FinanceRequest{Code: code})
	w.ok("the listing's choices", resp, err, "Listing")
	book := finance.BookPerShare(50_000, 1000)
	float := finance.FloatShares(1000, w.def.Stocks.FloatOptions[0])
	req := handlers.FinanceRequest{Code: code, Qty: strconv.FormatInt(float, 10), Price: strconv.FormatInt(book, 10)}
	resp, err = w.fin.IPO(ctx, w.meta(founder, "stock.ipo"), req)
	w.ok("the listing's confirmation", resp, err)
	req.Nonce = nonceOf(t, resp, "stock:ipo:")
	for i := 0; i < 2; i++ {
		resp, err = w.fin.IPO(ctx, w.meta(founder, "stock.ipo"), req)
		w.ok("listing", resp, err)
	}
	if n := w.count(`SELECT count(*) FROM stock_listings WHERE company_id = $1::uuid`, companyID); n != 1 {
		t.Fatal("the company is not listed once")
	}
	if got := w.balance(application.AccountCompanyTreasury, companyID); got != 50_000-w.def.Stocks.ListingFee {
		t.Fatalf("the treasury holds %d after the listing, want one fee off", got)
	}
	if _, locked := w.shares(companyID, founder.ID); locked != float {
		t.Fatalf("the founder has %d shares locked, want the float %d", locked, float)
	}

	// Traded: 60 bought at the listing price; then 50 bid at more, which
	// fills the 40 left at the resting price and rests the rest.
	fee, err := postgres.NewPolicyReader(w.pool, nil).Get(ctx, w.city.JurisdictionID, handlers.LeverMarketFee)
	if err != nil {
		t.Fatal(err)
	}
	w.order(buyer, "buy", code, 60, book)
	if got, _ := w.shares(companyID, buyer.ID); got != 60 {
		t.Fatalf("the buyer holds %d shares, want 60", got)
	}
	w.order(late, "buy", code, 50, book+5)
	if got, _ := w.shares(companyID, late.ID); got != float-60 {
		t.Fatalf("the late buyer holds %d shares, want %d", got, float-60)
	}
	founderShares, founderLocked := w.shares(companyID, founder.ID)
	if founderShares != 1000-float || founderLocked != 0 {
		t.Fatalf("the founder holds %d (%d locked), want %d and none locked", founderShares, founderLocked, 1000-float)
	}
	if got := w.balance(application.AccountPlayerBank, late.ID); got != 100_000-(float-60)*book-10*(book+5) {
		t.Fatalf("the late buyer's bank %d: the fill at the resting price and the rest reserved", got)
	}
	proceeds := float * book
	proceeds -= int64(w.count(`SELECT COALESCE(SUM(fee), 0) FROM share_trades WHERE company_id = $1::uuid`, companyID))
	if got := w.balance(application.AccountPlayerBank, founder.ID); got != proceeds {
		t.Fatalf("the founder received %d, want %d less the %d bps fee", got, proceeds, fee.Value)
	}
	var total int64
	_ = w.pool.Raw().QueryRow(ctx, `SELECT SUM(shares) FROM company_shareholders WHERE company_id = $1::uuid`, companyID).Scan(&total)
	if total != 1000 {
		t.Fatalf("the company's shares add up to %d, want 1000", total)
	}

	// A dividend: its choice, its confirmation pressed twice, paid once,
	// every share alike, the tax to the city.
	resp, err = w.fin.Dividend(ctx, w.meta(founder, "stock.dividend"), handlers.FinanceRequest{Code: code})
	w.ok("the dividend's choices", resp, err)
	amount := strings.Split(lastCallback(t, resp, "stock:dividend:"+code+":"), ":")[3]
	dreq := handlers.FinanceRequest{Code: code, Amount: amount}
	resp, err = w.fin.Dividend(ctx, w.meta(founder, "stock.dividend"), dreq)
	w.ok("the dividend's confirmation", resp, err)
	dreq.Nonce = nonceOf(t, resp, "stock:dividend:")
	buyerBank := w.balance(application.AccountPlayerBank, buyer.ID)
	for i := 0; i < 2; i++ {
		resp, err = w.fin.Dividend(ctx, w.meta(founder, "stock.dividend"), dreq)
		w.ok("paying the dividend", resp, err)
	}
	var perShare, paid, tax int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT per_share, paid, tax FROM dividends WHERE company_id = $1::uuid`, companyID).
		Scan(&perShare, &paid, &tax); err != nil {
		t.Fatalf("no dividend, once: %v", err)
	}
	if paid != perShare*1000 || w.count(`SELECT COALESCE(SUM(amount), 0) FROM dividend_payments`) < int(paid) {
		t.Fatalf("the dividend paid %d at %d a share; want every share alike", paid, perShare)
	}
	if got := w.balance(application.AccountPlayerBank, buyer.ID) - buyerBank; got != 60*perShare {
		t.Fatalf("the buyer received %d, want 60 × %d", got, perShare)
	}

	// The resting bid is cancelled: its money back to the bank.
	var orderNo int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT no FROM share_orders WHERE owner_id = $1::uuid AND status = 'open'`, late.ID).
		Scan(&orderNo); err != nil {
		t.Fatal(err)
	}
	lateBank := w.balance(application.AccountPlayerBank, late.ID)
	resp, err = w.fin.CancelShares(ctx, w.meta(late, "stock.cancel"), handlers.FinanceRequest{No: strconv.FormatInt(orderNo, 10)})
	w.ok("cancelling", resp, err)
	if got := w.balance(application.AccountPlayerBank, late.ID) - lateBank; got != 10*(book+5) {
		t.Fatalf("cancelling gave back %d, want what the bid held", got)
	}
	w.verify()
}

func TestGoldBoughtAndSoldAtTheMovingPrice(t *testing.T) {
	w := newFinanceWorld(t, nil)
	ctx := testCtx(t)
	p := w.resident(1_000_000)
	grams := w.def.Gold.GramOptions[1]

	price := func() int64 {
		var v int64
		_ = w.pool.Raw().QueryRow(ctx, `SELECT price FROM gold_dealer WHERE id = 1`).Scan(&v)
		return v
	}
	resp, err := w.fin.Gold(ctx, w.meta(p, "gold.show"))
	w.ok("the dealer", resp, err, "gold dealer")
	mid := price()
	buyAt, _ := w.def.GoldRules().Quote(mid)
	req := handlers.FinanceRequest{Grams: strconv.FormatInt(grams, 10)}
	resp, err = w.fin.GoldBuy(ctx, w.meta(p, "gold.buy"), req)
	w.ok("the price", resp, err)
	parts := strings.Split(lastCallback(t, resp, "gold:buy:"), ":")
	req.Method, req.Nonce = parts[3], parts[4]
	bank := w.balance(application.AccountPlayerBank, p.ID)
	for i := 0; i < 2; i++ {
		resp, err = w.fin.GoldBuy(ctx, w.meta(p, "gold.buy"), req)
		w.ok("buying gold", resp, err)
	}
	if got := bank - w.balance(application.AccountPlayerBank, p.ID); got != grams*buyAt {
		t.Fatalf("buying %d grams cost %d, want %d once", grams, got, grams*buyAt)
	}

	// The price moves once a period, on the game clock, within its bounds.
	w.settle()
	w.settle()
	moved := price()
	if moved < w.def.Gold.MinPrice || moved > w.def.Gold.MaxPrice {
		t.Fatalf("the price %d left its bounds", moved)
	}
	if n := w.count(`SELECT count(*) FROM gold_prices WHERE set_at >= $1`, w.started); n != 2 {
		t.Fatalf("%d prices for two periods, want 2", n)
	}

	// Sold back at what the dealer pays now.
	_, sellAt := w.def.GoldRules().Quote(moved)
	sreq := handlers.FinanceRequest{Grams: strconv.FormatInt(grams, 10)}
	resp, err = w.fin.GoldSell(ctx, w.meta(p, "gold.sell"), sreq)
	w.ok("the sale's price", resp, err)
	sreq.Nonce = nonceOf(t, resp, "gold:sell:")
	bank = w.balance(application.AccountPlayerBank, p.ID)
	for i := 0; i < 2; i++ {
		resp, err = w.fin.GoldSell(ctx, w.meta(p, "gold.sell"), sreq)
		w.ok("selling gold", resp, err)
	}
	if got := w.balance(application.AccountPlayerBank, p.ID) - bank; got != grams*sellAt {
		t.Fatalf("selling %d grams paid %d, want %d once", grams, got, grams*sellAt)
	}
	w.verify()
}

func TestSavingsEarnInterestOncePerPeriod(t *testing.T) {
	w := newFinanceWorld(t, nil)
	ctx := testCtx(t)
	w.fundCountry(2_000_000)
	p := w.resident(150_000)
	req := handlers.FinanceRequest{Amount: "100000"}
	meta := w.meta(p, "save.deposit")
	for i := 0; i < 2; i++ {
		resp, err := w.fin.Deposit(ctx, meta, req)
		w.ok("depositing", resp, err)
	}
	if got := w.balance(application.AccountPlayerSavings, p.ID); got != 100_000 {
		t.Fatalf("savings hold %d after a double press, want 100000", got)
	}
	// The first period marks the balance; the second pays on it.
	w.settle()
	if got := w.balance(application.AccountPlayerSavings, p.ID); got != 100_000 {
		t.Fatalf("money just put in earned %d in its first period", got-100_000)
	}
	w.settle()
	policy, err := postgres.NewPolicyReader(w.pool, nil).Get(ctx, w.country, handlers.LeverBaseInterestRate)
	if err != nil {
		t.Fatal(err)
	}
	want := finance.PeriodInterest(100_000, max(policy.Value-w.def.Savings.SpreadBPS, 0), w.def.PeriodsPerYear)
	if got := w.balance(application.AccountPlayerSavings, p.ID) - 100_000; got != want {
		t.Fatalf("a period's interest is %d, want %d", got, want)
	}
	w.verify()
}
