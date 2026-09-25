package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Finance joins the snapshot harness as an area of its own —
// testdata/snapshots/<language>/finance.txt (docs/adr/0026): the national
// bank, loans, savings, insurance, the stock exchange, the portfolio and the
// gold dealer, and every refusal and notice.
func init() { snapshotAreas["finance"] = financeSnapshots }

func financeSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	bankSnapshots(c, add)
	insuranceSnapshots(c, add)
	stockSnapshots(c, who, add)
	goldSnapshots(c, add)
}

func bankSnapshots(c Context, add func(string, *presenter.Response)) {
	country := GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	personal := Named{Code: "personal", Name: "Personal loan"}
	mortgage := Named{Code: "mortgage", Name: "Mortgage"}
	business := Named{Code: "business", Name: "Business loan"}
	credit := CreditView{Score: 672, Min: 300, Max: 850, PaymentBPS: 9000, DebtBPS: 7500, HistoryBPS: 4000,
		IncomeBPS: 6500, WorthBPS: 3000, NewCreditBPS: 7500, Missed: 1}
	due := snapshotNow.Add(18 * time.Minute)
	hub := FinanceHubView{Country: country, Credit: credit, PolicyBPS: 1500, Lendable: 800000, NextAt: due,
		Products: []LoanProductLine{
			{Product: personal, Kind: "player", RateBPS: 2600, Limit: 40000, MinScore: 500},
			{Product: mortgage, Kind: "mortgage", RateBPS: 2100, Limit: 320000, MinScore: 560},
			{Product: business, Kind: "company", RateBPS: 2300, Limit: 0, MinScore: 700},
		},
		Loans: []LoanLine{
			{No: 12, Product: personal, Status: "active", Next: 1786, Owed: 21430, Left: 12},
			{No: 9, Product: business, Company: Named{Code: "Q7M2K9B", Name: companyNames[c.Lang][0]}, Status: "active",
				Next: 3900, Owed: 42000, Left: 11},
			{No: 4, Product: personal, Status: "repaid"},
		},
		Savings: 15000, SavingsBPS: 900}
	add("Bank · the national bank with offers and loans", FinanceHub(c, hub))
	fresh := FinanceHubView{Country: country, Credit: CreditView{Score: 541, Min: 300, Max: 850, PaymentBPS: 6000,
		DebtBPS: 10000}, PolicyBPS: 1500,
		Products: []LoanProductLine{{Product: personal, Kind: "player", RateBPS: 3600, Limit: 10000, MinScore: 500},
			{Product: mortgage, Kind: "mortgage", RateBPS: 3100, MinScore: 560}}}
	add("Bank · a newcomer, the bank run dry", FinanceHub(c, fresh))
	taken := fresh
	taken.Notice, taken.Lendable = "taken", 50000
	add("Bank · just borrowed", FinanceHub(c, taken))

	add("Loan offer · a personal loan's amounts and terms", LoanOffer(c, LoanOfferView{Product: personal, Kind: "player",
		RateBPS: 2600, Limit: 40000, Terms: []int64{7, 14, 28}, LateFeeBPS: 500, DefaultAfter: 4,
		Options: []LoanOption{{Amount: 10000, Term: 7, Instalment: 1500}, {Amount: 10000, Term: 14, Instalment: 786},
			{Amount: 40000, Term: 28, Instalment: 1786}}}))
	home := PledgeLine{No: 3, Type: Named{Code: "studio", Name: "Studio flat"},
		City: GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}, Value: 46350, Limit: 25000}
	add("Loan offer · a mortgage, choosing the property", LoanOffer(c, LoanOfferView{Product: mortgage, Kind: "mortgage",
		RateBPS: 2100, Limit: 320000, LateFeeBPS: 300, DefaultAfter: 4, Pledges: []PledgeLine{home}}))
	add("Loan offer · a mortgage against a home", LoanOffer(c, LoanOfferView{Product: mortgage, Kind: "mortgage",
		RateBPS: 2100, Limit: 25000, LateFeeBPS: 300, DefaultAfter: 4, Pledges: []PledgeLine{home}, Pledge: &home,
		Options: []LoanOption{{Amount: 25000, Term: 28, Instalment: 1015}}}))
	add("Loan offer · nothing left to lend", LoanOffer(c, LoanOfferView{Product: personal, Kind: "player", RateBPS: 2600,
		LateFeeBPS: 500, DefaultAfter: 4}))
	add("Loan · confirming the terms", LoanConfirm(c, LoanConfirmView{Product: personal, Amount: 40000, Term: 28,
		RateBPS: 2600, Interest: 5600, Instalment: 1628, Total: 45600, FirstAt: due, Nonce: "a1b2c3d4e5f6"}))

	active := LoanDetailView{Loan: LoanLine{No: 12, Product: personal, Status: "active", Next: 1786, Owed: 21430, Left: 12,
		Arrears: 1}, Principal: 40000, Interest: 5600, RateBPS: 2600, Periods: 28, Paid: 15, FeesDue: 89,
		Payoff: 21430, OpenedAt: snapshotNow.Add(-6 * time.Hour), NextAt: due, Missed: 1}
	add("Loan · one in arrears", LoanDetail(c, active))
	confirm := active
	confirm.ConfirmOpen, confirm.Nonce = true, "f6e5d4c3b2a1"
	add("Loan · confirming a payoff", LoanDetail(c, confirm))
	add("Loan · a defaulted mortgage", LoanDetail(c, LoanDetailView{Loan: LoanLine{No: 7, Product: mortgage,
		Status: "defaulted"}, Principal: 25000, Interest: 2500, RateBPS: 2100, Periods: 28, Paid: 6, Pledge: &home,
		OpenedAt: snapshotNow.Add(-72 * time.Hour), Missed: 4, Recovered: 18000, WrittenOff: 1200}))
	add("Loan · paid off", LoanDetail(c, LoanDetailView{Loan: LoanLine{No: 4, Product: personal, Status: "repaid"},
		Principal: 10000, Interest: 350, RateBPS: 2600, Periods: 7, Paid: 7, OpenedAt: snapshotNow.Add(-30 * time.Hour),
		Notice: "paid_off"}))

	add("Savings · an account", Savings(c, SavingsView{Balance: 15000, RateBPS: 900, Earned: 480, Next: 25, Bank: 21000,
		Deposits: []int64{5250, 10500, 21000}, Withdrawals: []int64{3750, 7500, 15000}, NextAt: due}))
	add("Savings · just deposited", Savings(c, SavingsView{Balance: 20000, RateBPS: 900, Bank: 16000,
		Deposits: []int64{4000, 8000, 16000}, Withdrawals: []int64{5000, 10000, 20000}, Notice: "deposited",
		NoticeArgs: map[string]any{"amount": int64(5000)}}))

	for _, kind := range []string{FinanceRefusedNoBank, FinanceRefusedTooMany, FinanceRefusedBankDry, FinanceRefusedAmount,
		FinanceRefusedPledge, FinanceRefusedNotOwner, FinanceRefusedShort, FinanceRefusedSavingsCap, FinanceRefusedNoProduct,
		FinanceRefusedHeld, FinanceRefusedPledged, FinanceRefusedNotListed, FinanceRefusedListed, FinanceRefusedTooYoung,
		FinanceRefusedTooSmall, FinanceRefusedInDebt, FinanceRefusedFloat, FinanceRefusedNoShares, FinanceRefusedNoMoney,
		FinanceRefusedTooManyOrd, FinanceRefusedOrder, FinanceRefusedGoldStock, FinanceRefusedGoldHeld,
		FinanceRefusedNotYours, FinanceRefusedOwnCompany, FinanceRefusedFundClosed, FinanceRefusedNoPlayerFor} {
		add("Refused · "+kind, FinanceRefusal(c, FinanceRefusalView{Kind: kind, Amount: 40000, Count: 2}))
	}
	add("Refused · score", FinanceRefusal(c, FinanceRefusalView{Kind: FinanceRefusedScore, Score: 600}))

	n := func(title string, v FinanceNoticeView) { add("Notice · "+title, FinanceNotice(sent(c), v)) }
	n("an instalment due, not covered", FinanceNoticeView{Kind: "due", No: 12, Product: personal, Amount: 1786, Other: 900,
		At: due})
	n("an instalment missed", FinanceNoticeView{Kind: "missed", No: 12, Product: personal, Amount: 1786, Other: 89, Count: 2})
	n("a mortgage defaulted, its home taken", FinanceNoticeView{Kind: "defaulted", No: 7, Product: mortgage, Amount: 1200,
		Other: 18000, Pledge: &home})
	n("a loan repaid", FinanceNoticeView{Kind: "repaid", No: 4, Product: personal, Amount: 10350})
}

func insuranceSnapshots(c Context, add func(string, *presenter.Response)) {
	country := GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	health := Named{Code: "health", Name: "Health insurance"}
	prop := Named{Code: "property", Name: "Property insurance"}
	home := PledgeLine{No: 3, Type: Named{Code: "studio", Name: "Studio flat"},
		City: GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}, Value: 46350}
	products := []InsuranceProductLine{
		{Product: health, Covers: "hospital", Premium: 40, CoverBPS: 8000, MaxClaim: 5000, Waiting: 24 * time.Minute},
		{Product: prop, Covers: "war_damage", Premium: 20, PremBPS: 8, CoverBPS: 7000, MaxClaim: 250000,
			Waiting: 48 * time.Minute, Targets: []PledgeLine{home}},
	}
	add("Insurance · nothing held yet", Insurance(c, InsuranceView{Country: country, Fund: 84000, Products: products}))
	held := products
	held[0].Held, held[1].Targets = true, nil
	policies := []PolicyLine{
		{No: 5, Product: health, Covers: "hospital", Status: "active", Premium: 40, Paid: 3200, From: snapshotNow,
			Claimable: true},
		{No: 6, Product: prop, Covers: "war_damage", Property: &home, Status: "active", Premium: 57,
			From: snapshotNow.Add(20 * time.Minute)},
		{No: 2, Product: health, Covers: "hospital", Status: "ended", Reason: "lapsed"},
	}
	add("Insurance · two policies and a lapsed one", Insurance(c, InsuranceView{Country: country, Fund: 84000,
		Products: held, Policies: policies, Notice: "insured"}))
	cancel := policies[0]
	add("Insurance · cancelling a policy", Insurance(c, InsuranceView{Country: country, Fund: 84000, Products: held,
		Policies: policies, Cancel: &cancel}))
	both := []string{MethodCash, MethodCard}
	add("Insurance · buying health cover", InsureConfirm(c, InsureConfirmView{Product: health, Covers: "hospital",
		Premium: 40, CoverBPS: 8000, MaxClaim: 5000, Waiting: 24 * time.Minute,
		Payment: PaymentChoice{Amount: 40, Accepted: both, Usable: both, Cash: 900, Bank: 21000}}))
	add("Insurance · insuring a home", InsureConfirm(c, InsureConfirmView{Product: prop, Covers: "war_damage",
		Property: &home, Premium: 57, CoverBPS: 7000, MaxClaim: 250000, Waiting: 48 * time.Minute,
		Payment: PaymentChoice{Amount: 57, Accepted: both, Usable: []string{MethodCard}, Cash: 10, Bank: 21000}}))
	n := func(title string, v FinanceNoticeView) { add("Notice · "+title, FinanceNotice(sent(c), v)) }
	n("a claim paid", FinanceNoticeView{Kind: "claimed", No: 5, Product: health, Amount: 1440, Other: 1440})
	n("a policy lapsed", FinanceNoticeView{Kind: "lapsed", No: 5, Product: health, Amount: 40})
	n("a policy's property gone", FinanceNoticeView{Kind: "gone", No: 6, Product: prop})
}

func stockSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	grocer := Named{Code: "Q7M2K9B", Name: companyNames[c.Lang][0]}
	workshop := Named{Code: "H4T8W2C", Name: companyNames[c.Lang][1]}
	grocery := Named{Code: "grocery", Name: "Grocery store"}
	brennhaven := GovPlace{Kind: "city", Code: "brennhaven", Name: "Brennhaven"}
	lines := []ListedLine{
		{Company: grocer, Type: grocery, City: brennhaven, Price: 132, Prev: 120, Volume: 240, Cap: 132000},
		{Company: workshop, Type: Named{Code: "repair_workshop", Name: "Repair workshop"},
			City: GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}, Price: 88, Prev: 95, Volume: 0, Cap: 88000},
	}
	add("Exchange · two listed companies", Exchange(c, ExchangeView{Lines: lines}))
	add("Exchange · read in a group", Exchange(group(c), ExchangeView{Lines: lines[:1]}))
	add("Exchange · nothing listed yet", Exchange(c, ExchangeView{}))

	at := snapshotNow.Add(-15 * time.Minute)
	page := StockView{Company: grocer, Type: grocery, City: brennhaven, Listed: true, Price: 132, Prev: 120, IPO: 110,
		Book: 96, Total: 1000, Controller: who.friend, LastDiv: 4,
		Asks:    []BookLevel{{Price: 135, Qty: 40}, {Price: 140, Qty: 100}},
		Bids:    []BookLevel{{Price: 128, Qty: 25}},
		Trades:  []TradeLine{{Qty: 10, Price: 132, At: at}, {Qty: 50, Price: 120, At: at.Add(-5 * time.Minute)}},
		Holding: 60, Locked: 10, Cost: 7200,
		Buys:  []PriceOption{{Qty: 1, Price: 125}, {Qty: 1, Price: 132}, {Qty: 1, Price: 139}, {Qty: 10, Price: 132}},
		Sells: []PriceOption{{Qty: 1, Price: 125}, {Qty: 1, Price: 132}, {Qty: 1, Price: 139}, {Qty: 10, Price: 132}}}
	add("Stock · a listed company, holding some", Stock(c, page))
	add("Stock · the same, read in a group", Stock(group(c), page))
	add("Stock · the owner's unlisted company", Stock(c, StockView{Company: grocer, Type: grocery, City: brennhaven,
		Book: 96, Price: 96, Total: 1000, Owner: true, Controller: who.me, Holding: 1000}))

	add("Order · confirming a buy", StockOrder(c, StockOrderView{Company: grocer, Side: "buy", Qty: 10, Price: 132,
		Reserve: 1320, Bank: 21000, FeeBPS: 200, Nonce: "a1b2c3d4e5f6"}))
	add("Order · confirming a sell", StockOrder(c, StockOrderView{Company: grocer, Side: "sell", Qty: 10, Price: 139,
		FeeBPS: 200, Nonce: "a1b2c3d4e5f6"}))
	add("Order · a buy partly filled, the rest resting", StockOrder(c, StockOrderView{Company: grocer, Side: "buy", Qty: 50,
		Price: 132, Placed: true, No: 31, Filled: 40, Spent: 5280, Rests: true, ExpiresAt: snapshotNow.Add(168 * time.Hour)}))
	add("Order · a sell filled at once", StockOrder(c, StockOrderView{Company: grocer, Side: "sell", Qty: 10, Price: 128,
		Placed: true, No: 32, Filled: 10, Got: 1254}))

	add("Portfolio · shares, gold, savings and orders", Portfolio(c, PortfolioView{
		Holdings: []HoldingLine{{Company: grocer, Shares: 60, Price: 132, Value: 7920, Cost: 7200, Listed: true},
			{Company: workshop, Shares: 1000, Price: 12, Value: 12000}},
		Orders: []OpenOrderLine{{No: 31, Company: grocer, Side: "buy", Qty: 50, Filled: 40, Price: 132},
			{No: 33, Company: grocer, Side: "sell", Qty: 10, Price: 150}},
		Gold: 25, GoldVal: 19500, Savings: 15000, Value: 54420, Gain: 2120}))
	add("Portfolio · an investor at a loss", Portfolio(c, PortfolioView{Gold: 10, GoldVal: 7200, Value: 7200, Gain: -800,
		Notice: "cancelled", NoticeArgs: map[string]any{"no": int64(33)}}))

	floats := []PriceOption{{Qty: 100, Price: 1000}, {Qty: 250, Price: 2500}, {Qty: 490, Price: 4900}}
	prices := []PriceOption{{Qty: 10000, Price: 96}, {Qty: 12500, Price: 120}, {Qty: 15000, Price: 144}}
	listing := ListingView{Company: grocer, MinAge: 72 * time.Minute, MinRevenue: 5000, Revenue: 18400, Book: 96,
		Total: 1000, Fee: 5000, Floats: floats, Prices: prices}
	add("Listing · choosing the float and the price", Listing(c, listing))
	chosen := listing
	chosen.Chosen, chosen.Nonce = &PriceOption{Qty: 250, Price: 120}, "a1b2c3d4e5f6"
	add("Listing · confirming", Listing(c, chosen))
	young := listing
	young.Refused, young.Age = "too_young", 30*time.Minute
	add("Listing · too young", Listing(c, young))
	small := listing
	small.Refused, small.Revenue = "too_small", 1200
	add("Listing · not earned enough", Listing(c, small))

	div := DividendView{Company: grocer, Free: 42000, TaxBPS: 1000, Total: 1000,
		Options: []PriceOption{{Qty: 3, Price: 4200}, {Qty: 9, Price: 10500}, {Qty: 18, Price: 21000}}}
	add("Dividend · choosing", Dividend(c, div))
	pick := div
	pick.Chosen, pick.Nonce = &div.Options[1], "a1b2c3d4e5f6"
	add("Dividend · confirming", Dividend(c, pick))
	paid := div
	paid.Paid, paid.PerShare, paid.Holders, paid.Free = 9000, 9, 4, 31500
	add("Dividend · paid", Dividend(c, paid))
	add("Dividend · a company in debt", Dividend(c, DividendView{Company: grocer, TaxBPS: 1000, Total: 1000,
		Refused: "in_debt"}))

	s := func(title string, v StockNoticeView) { add("Notice · "+title, StockNotice(sent(c), v)) }
	s("a buy order filled", StockNoticeView{Kind: "filled", Side: "buy", Company: grocer, Qty: 10, Price: 132, Amount: 1320})
	s("a sell order filled", StockNoticeView{Kind: "filled", Side: "sell", Company: grocer, Qty: 10, Price: 132, Amount: 1294})
	s("a dividend received", StockNoticeView{Kind: "dividend", Company: grocer, Qty: 60, Price: 9, Amount: 540})
	s("control taken", StockNoticeView{Kind: "takeover", Company: grocer, Qty: 510})
	s("control lost", StockNoticeView{Kind: "takeover_lost", Company: grocer, Qty: 510})
}

func goldSnapshots(c Context, add func(string, *presenter.Response)) {
	at := snapshotNow.Add(-24 * time.Minute)
	view := GoldView{Buy: 824, Sell: 776, Mid: 800, Prev: 780, Stock: 498750,
		History: []GoldPoint{{Price: 800, At: at}, {Price: 780, At: at.Add(-24 * time.Minute)},
			{Price: 792, At: at.Add(-48 * time.Minute)}},
		Grams: 25, Cost: 19800, Options: []int64{1, 10, 50, 100}, NextAt: snapshotNow.Add(24 * time.Minute)}
	add("Gold · the dealer, holding some", Gold(c, view))
	add("Gold · read in a group", Gold(group(c), view))
	done := view
	done.Notice, done.NoticeArgs = "buy_done", map[string]any{"grams": int64(10), "total": int64(8240)}
	add("Gold · just bought", Gold(c, done))
	both := []string{MethodCash, MethodCard}
	add("Gold · buying, the ways to pay", GoldTrade(c, GoldTradeView{Side: "buy", Grams: 10, Price: 824, Total: 8240,
		Nonce: "a1b2c3d4e5f6", Payment: PaymentChoice{Amount: 8240, Accepted: both, Usable: both, Cash: 9000, Bank: 21000}}))
	add("Gold · selling", GoldTrade(c, GoldTradeView{Side: "sell", Grams: 10, Price: 776, Total: 7760,
		Nonce: "a1b2c3d4e5f6"}))
}
