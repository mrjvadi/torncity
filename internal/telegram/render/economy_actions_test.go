package render

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// economyFixtures are the economy screens with realistic views, for the
// actions tests: every action the core lists is a command the game serves, and
// every button Telegram draws is one of them.
func economyFixtures() []fixture {
	c := presentation.Ctx{Lang: "fa"}
	at := time.Date(2026, 3, 4, 10, 20, 30, 0, time.UTC)
	pay := presentation.PaymentChoice{Amount: 500, Accepted: []string{"cash", "card"}, Usable: []string{"cash", "card"}, Cash: 900, Bank: 4000}
	opts := []economy.AmountOption{{Amount: 1000, Nonce: "n1"}, {Amount: 5000, Nonce: "n1", All: true}}
	place := economy.GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}
	loan := economy.LoanLine{No: 4, Product: named("personal"), Status: "active", Next: 100, Owed: 900, Left: 5}
	pledge := economy.PledgeLine{No: 7, Type: named("house"), City: place, Value: 9000, Limit: 5000}
	book := []economy.BookLevel{{Price: 10, Qty: 3}, {Price: 12, Qty: 1}}
	auction := economy.AuctionLine{No: 9, Item: named("sword"), Quality: 2, HighBid: 50, Reserve: 40, Remaining: time.Hour, EndsAt: at, Status: "open"}
	way := &economy.Way{Place: named("market_square"), Walk: time.Minute}

	bank := economy.BankView{CityCode: "ostmarch", City: "Ostmarch", Cash: 100, Bank: 900, WithdrawalFeeBPS: 100,
		Deposits: opts, Withdrawals: opts, CanDeposit: true, CanWithdraw: true, Notice: "deposited", NoticeArgs: map[string]any{"amount": int64(500)}}
	payV := economy.PayView{PayeeName: "Sara", PayeeCode: "AB12", Together: true, CityCode: "ostmarch", City: "Ostmarch",
		PayerCityCode: "ostmarch", PayerCity: "Ostmarch", CardFeeBPS: 100, Cash: 100, Bank: 900,
		CashOptions: opts, CardOptions: opts, CanCash: true, CanCard: true, Origin: "-100123"}
	confirm := economy.PayConfirmView{PayeeName: "Sara", PayeeCode: "AB12", Method: "card", Amount: 500, Fee: 5, Total: 505, After: 395, Nonce: "n2", Origin: "-100123"}
	sent := economy.PaySentView{PayeeName: "Sara", PayeeCode: "AB12", Method: "card", Amount: 500}
	declined := economy.PaymentDeclinedView{Amount: 500, Cash: 1, Bank: 2, Accepted: []string{"cash"}, BackLabel: "shop.button.back_to_shop",
		Back: presentation.RefOfAddress(economy.AddrShop + ":grocery")}
	budget := economy.BudgetView{City: place, Lever: "city.allocation", CanPropose: true, Treasury: 1000,
		Order: []string{"roads"}, Allocation: map[string]int64{"roads": 10000}, NextAt: at, NextIn: time.Hour}
	gold := economy.GoldView{Buy: 110, Sell: 100, Mid: 105, Prev: 100, Stock: 50, Grams: 6, Cost: 500, Options: []int64{1, 5, 10}, NextAt: at,
		History: []economy.GoldPoint{{Price: 100, At: at}}}
	goldTrade := economy.GoldTradeView{Side: "buy", Grams: 5, Price: 110, Total: 550, Payment: pay, Nonce: "n3"}
	goldSell := economy.GoldTradeView{Side: "sell", Grams: 5, Price: 100, Total: 500, Nonce: "n3"}
	hub := economy.FinanceHubView{Country: place, Credit: economy.CreditView{Score: 600, Min: 300, Max: 850},
		Products: []economy.LoanProductLine{{Product: named("personal"), Kind: "player", RateBPS: 500, Limit: 1000, MinScore: 400}},
		Loans:    []economy.LoanLine{loan}, Savings: 10, SavingsBPS: 100, Lendable: 1000, NextAt: at}
	offer := economy.LoanOfferView{Product: named("personal"), Kind: "player", RateBPS: 500, Limit: 1000, Terms: []int64{6},
		Options: []economy.LoanOption{{Amount: 500, Term: 6, Instalment: 90}}}
	mortgage := economy.LoanOfferView{Product: named("mortgage"), Kind: "mortgage", Pledges: []economy.PledgeLine{pledge}}
	loanConfirm := economy.LoanConfirmView{Product: named("personal"), Amount: 500, Term: 6, RateBPS: 500, Interest: 30, Instalment: 90, Total: 530, FirstAt: at, Nonce: "n4"}
	loanDetail := economy.LoanDetailView{Loan: loan, Principal: 500, Payoff: 900, OpenedAt: at, NextAt: at, Nonce: "n5", ConfirmOpen: true}
	savings := economy.SavingsView{Balance: 100, RateBPS: 50, Bank: 900, Deposits: []int64{100, 500}, Withdrawals: []int64{100}, NextAt: at}
	insurance := economy.InsuranceView{Country: place, Fund: 1000,
		Products: []economy.InsuranceProductLine{{Product: named("health"), Covers: "health", Premium: 10}, {Product: named("war"), Covers: "war_damage", Targets: []economy.PledgeLine{pledge}}},
		Policies: []economy.PolicyLine{{No: 3, Product: named("health"), Covers: "health", Status: "active", From: at, Started: at}}}
	insureConfirm := economy.InsureConfirmView{Product: named("health"), Covers: "health", Premium: 10, CoverBPS: 5000, MaxClaim: 900, Waiting: time.Hour, Payment: pay}
	finRefusal := economy.FinanceRefusalView{Kind: economy.FinanceRefusedScore, Score: 500, Back: presentation.RefOfAddress(economy.AddrLoanHub)}

	exchange := economy.ExchangeView{Lines: []economy.ListedLine{{Company: named("acme"), Type: named("farm"), City: place, Price: 10, Prev: 9, Volume: 5, Cap: 100}}}
	stock := economy.StockView{Company: named("acme"), Type: named("farm"), City: place, Listed: true, Price: 10, Prev: 9, IPO: 8, Book: 7, Total: 100,
		Bids: book, Asks: book, Holding: 3, Owner: true, Buys: []economy.PriceOption{{Qty: 1, Price: 10}}, Sells: []economy.PriceOption{{Qty: 1, Price: 10}}}
	stockOrder := economy.StockOrderView{Company: named("acme"), Side: "buy", Qty: 2, Price: 10, Reserve: 20, Bank: 100, FeeBPS: 25, Nonce: "n6"}
	stockPlaced := economy.StockOrderView{Company: named("acme"), Side: "sell", Qty: 2, Price: 10, Placed: true, No: 5, Rests: true, ExpiresAt: at}
	portfolio := economy.PortfolioView{Holdings: []economy.HoldingLine{{Company: named("acme"), Shares: 3, Price: 10, Value: 30, Cost: 25, Listed: true}},
		Orders: []economy.OpenOrderLine{{No: 5, Company: named("acme"), Side: "buy", Qty: 2, Price: 10}}, Gold: 6, GoldVal: 600, Savings: 10, Value: 640, Gain: 15}
	listing := economy.ListingView{Company: named("acme"), MinAge: time.Hour, Age: time.Hour, Book: 100, Total: 100, Fee: 5,
		Floats: []economy.PriceOption{{Qty: 10, Price: 0}}, Prices: []economy.PriceOption{{Qty: 0, Price: 12}}}
	dividend := economy.DividendView{Company: named("acme"), Free: 100, TaxBPS: 500, Total: 10, Options: []economy.PriceOption{{Qty: 1, Price: 10}}}

	market := economy.MarketView{CityCode: "ostmarch", City: "Ostmarch", Books: []economy.BookSummary{{Item: named("bread"), BestBid: 3, BestAsk: 4, Last: 3}},
		Yours: []presentation.Named{named("sword")}, AtMarket: false, Way: way}
	bookV := economy.BookView{Item: named("bread"), CityCode: "ostmarch", City: "Ostmarch", Bids: book, Asks: book,
		Trades: []economy.TradeLine{{Qty: 1, Price: 3, At: at}}, Reference: 10, Holding: 4, AtMarket: true, Nonce: "n7"}
	bookAway := bookV
	bookAway.AtMarket, bookAway.Way = false, way
	checkout := economy.MarketCheckoutView{Item: named("bread"), Qty: 2, Price: 4, Reserve: 8, Payment: pay, Nonce: "n8"}
	placed := economy.OrderPlacedView{Item: named("bread"), Side: "buy", No: 12, Qty: 2, Filled: 1, Price: 4, Rests: true, Spent: 4, ExpiresAt: at, Method: "cash"}
	cancelled := economy.OrderCancelledView{Item: named("bread"), Side: "buy", No: 12, Left: 1, Refund: 4}
	myOrders := economy.MyOrdersView{Orders: []economy.OrderLine{{No: 12, Item: named("bread"), Side: "buy", Qty: 2, Price: 4, Status: "open", CityCode: "ostmarch", City: "Ostmarch", ExpiresAt: at}}}
	marketNo := economy.MarketRefusalView{Kind: economy.MarketRefusedTooMany, Item: named("bread"), Count: 3}

	shops := economy.ShopsView{CityCode: "ostmarch", City: "Ostmarch", Shops: []economy.ShopLine{{Shop: named("grocery"), Place: named("square"), Here: true}}, Place: &presentation.Named{Code: "square"}}
	shop := economy.ShopView{Shop: named("grocery"), Place: named("square"), Here: true,
		Shelves: []economy.ShelfLine{{Item: named("bread"), Price: 3, Stock: 4, Buyback: 1}}, TaxBPS: 100}
	shopAway := shop
	shopAway.Here, shopAway.Walk = false, time.Minute
	shopPay := economy.ShopCheckoutView{Shop: named("grocery"), Item: named("bread"), Qty: 1, Unit: 3, Total: 3, Tax: 1, TaxBPS: 100, Stock: 10, Payment: pay, Nonce: "n9"}
	bought := economy.ShopBoughtView{Shop: named("grocery"), Item: named("bread"), Qty: 1, Total: 3, Tax: 1, Method: "cash"}
	offers := economy.SellOffersView{Item: named("bread"), Ref: "ab12", Offers: []economy.SellOffer{{Shop: named("grocery"), Place: named("square"), Price: 2}}}
	sold := economy.ShopSoldView{Shop: named("grocery"), Item: named("bread"), Price: 2, Left: 1}
	shopNo := economy.ShopRefusalView{Kind: economy.ShopRefusedSoldOut, Shop: named("grocery"), Item: named("bread"), NextRestock: at}

	auctions := economy.AuctionsView{CityCode: "ostmarch", City: "Ostmarch", Auctions: []economy.AuctionLine{auction}, Way: way}
	auctionV := economy.AuctionView{Line: auction, MinNext: 55, Bids: 2, Payment: &pay, Nonce: "n10", Seller: "Ali"}
	auctionNew := economy.AuctionNewView{Item: named("sword"), Ref: "ab12", Quality: 2, Reserves: []int64{40, 80}, Durations: []time.Duration{time.Hour}, Nonce: "n11"}
	opened := economy.AuctionOpenedView{No: 9, Item: named("sword"), Reserve: 40, Duration: time.Hour, EndsAt: at}
	bid := economy.BidPlacedView{No: 9, Item: named("sword"), Amount: 55, Method: "cash", EndsAt: at}
	mine := economy.MyAuctionsView{Auctions: []economy.AuctionLine{auction}}
	auctionNo := economy.AuctionRefusalView{Kind: economy.AuctionRefusedTooLow, No: 9, MinNext: 55}

	gone := &economy.Unavailable{Service: "loans", Stage: "city", Here: "village",
		Requires: []economy.NeedBuilding{{Code: "bank"}}, Nearest: &presentation.Named{Code: "support", Name: "Central"}}
	hubGone := economy.FinanceHubView{Unavailable: gone}
	exchangeGone := economy.ExchangeView{Unavailable: gone}
	auctionsGone := economy.AuctionsView{Unavailable: gone}
	goldGone := economy.GoldView{Unavailable: gone}

	tg := func(f func(screens.Context) *presenter.Response) func(screens.Context) *presenter.Response { return f }
	return []fixture{
		{"finance hub unavailable", economy.FinanceHub(c, hubGone), tg(func(x screens.Context) *presenter.Response { return screens.FinanceHub(x, hubGone) })},
		{"exchange unavailable", economy.Exchange(c, exchangeGone), tg(func(x screens.Context) *presenter.Response { return screens.Exchange(x, exchangeGone) })},
		{"auctions unavailable", economy.Auctions(c, auctionsGone), tg(func(x screens.Context) *presenter.Response { return screens.Auctions(x, auctionsGone) })},
		{"gold unavailable", economy.Gold(c, goldGone), tg(func(x screens.Context) *presenter.Response { return screens.Gold(x, goldGone) })},
		{"bank", economy.Bank(c, bank), tg(func(x screens.Context) *presenter.Response { return screens.Bank(x, bank) })},
		{"pay", economy.Pay(c, payV), tg(func(x screens.Context) *presenter.Response { return screens.Pay(x, payV) })},
		{"pay help", economy.PayHelp(c), tg(func(x screens.Context) *presenter.Response { return screens.PayHelp(x) })},
		{"pay confirm", economy.PayConfirm(c, confirm), tg(func(x screens.Context) *presenter.Response { return screens.PayConfirm(x, confirm) })},
		{"pay sent", economy.PaySent(c, sent), tg(func(x screens.Context) *presenter.Response { return screens.PaySent(x, sent) })},
		{"payment declined", economy.PaymentDeclined(c, declined), tg(func(x screens.Context) *presenter.Response { return screens.PaymentDeclined(x, declined) })},
		{"budget", economy.Budget(c, budget), tg(func(x screens.Context) *presenter.Response { return screens.Budget(x, budget) })},
		{"gold", economy.Gold(c, gold), tg(func(x screens.Context) *presenter.Response { return screens.Gold(x, gold) })},
		{"gold buy", economy.GoldTrade(c, goldTrade), tg(func(x screens.Context) *presenter.Response { return screens.GoldTrade(x, goldTrade) })},
		{"gold sell", economy.GoldTrade(c, goldSell), tg(func(x screens.Context) *presenter.Response { return screens.GoldTrade(x, goldSell) })},
		{"finance hub", economy.FinanceHub(c, hub), tg(func(x screens.Context) *presenter.Response { return screens.FinanceHub(x, hub) })},
		{"loan offer", economy.LoanOffer(c, offer), tg(func(x screens.Context) *presenter.Response { return screens.LoanOffer(x, offer) })},
		{"loan pledges", economy.LoanOffer(c, mortgage), tg(func(x screens.Context) *presenter.Response { return screens.LoanOffer(x, mortgage) })},
		{"loan confirm", economy.LoanConfirm(c, loanConfirm), tg(func(x screens.Context) *presenter.Response { return screens.LoanConfirm(x, loanConfirm) })},
		{"loan detail", economy.LoanDetail(c, loanDetail), tg(func(x screens.Context) *presenter.Response { return screens.LoanDetail(x, loanDetail) })},
		{"savings", economy.Savings(c, savings), tg(func(x screens.Context) *presenter.Response { return screens.Savings(x, savings) })},
		{"insurance", economy.Insurance(c, insurance), tg(func(x screens.Context) *presenter.Response { return screens.Insurance(x, insurance) })},
		{"insure confirm", economy.InsureConfirm(c, insureConfirm), tg(func(x screens.Context) *presenter.Response { return screens.InsureConfirm(x, insureConfirm) })},
		{"finance refusal", economy.FinanceRefusal(c, finRefusal), tg(func(x screens.Context) *presenter.Response { return screens.FinanceRefusal(x, finRefusal) })},
		{"exchange", economy.Exchange(c, exchange), tg(func(x screens.Context) *presenter.Response { return screens.Exchange(x, exchange) })},
		{"stock", economy.Stock(c, stock), tg(func(x screens.Context) *presenter.Response { return screens.Stock(x, stock) })},
		{"stock order", economy.StockOrder(c, stockOrder), tg(func(x screens.Context) *presenter.Response { return screens.StockOrder(x, stockOrder) })},
		{"stock placed", economy.StockOrder(c, stockPlaced), tg(func(x screens.Context) *presenter.Response { return screens.StockOrder(x, stockPlaced) })},
		{"portfolio", economy.Portfolio(c, portfolio), tg(func(x screens.Context) *presenter.Response { return screens.Portfolio(x, portfolio) })},
		{"listing", economy.Listing(c, listing), tg(func(x screens.Context) *presenter.Response { return screens.Listing(x, listing) })},
		{"dividend", economy.Dividend(c, dividend), tg(func(x screens.Context) *presenter.Response { return screens.Dividend(x, dividend) })},
		{"market", economy.Market(c, market), tg(func(x screens.Context) *presenter.Response { return screens.Market(x, market) })},
		{"book", economy.Book(c, bookV), tg(func(x screens.Context) *presenter.Response { return screens.Book(x, bookV) })},
		{"book away", economy.Book(c, bookAway), tg(func(x screens.Context) *presenter.Response { return screens.Book(x, bookAway) })},
		{"market checkout", economy.MarketCheckout(c, checkout), tg(func(x screens.Context) *presenter.Response { return screens.MarketCheckout(x, checkout) })},
		{"order placed", economy.OrderPlaced(c, placed), tg(func(x screens.Context) *presenter.Response { return screens.OrderPlaced(x, placed) })},
		{"order cancelled", economy.OrderCancelled(c, cancelled), tg(func(x screens.Context) *presenter.Response { return screens.OrderCancelled(x, cancelled) })},
		{"my orders", economy.MyOrders(c, myOrders), tg(func(x screens.Context) *presenter.Response { return screens.MyOrders(x, myOrders) })},
		{"market refusal", economy.MarketRefusal(c, marketNo), tg(func(x screens.Context) *presenter.Response { return screens.MarketRefusal(x, marketNo) })},
		{"shops", economy.Shops(c, shops), tg(func(x screens.Context) *presenter.Response { return screens.Shops(x, shops) })},
		{"shop", economy.ShopDetail(c, shop), tg(func(x screens.Context) *presenter.Response { return screens.ShopDetail(x, shop) })},
		{"shop away", economy.ShopDetail(c, shopAway), tg(func(x screens.Context) *presenter.Response { return screens.ShopDetail(x, shopAway) })},
		{"shop checkout", economy.ShopCheckout(c, shopPay), tg(func(x screens.Context) *presenter.Response { return screens.ShopCheckout(x, shopPay) })},
		{"shop bought", economy.ShopBought(c, bought), tg(func(x screens.Context) *presenter.Response { return screens.ShopBought(x, bought) })},
		{"sell offers", economy.SellOffers(c, offers), tg(func(x screens.Context) *presenter.Response { return screens.SellOffers(x, offers) })},
		{"shop sold", economy.ShopSold(c, sold), tg(func(x screens.Context) *presenter.Response { return screens.ShopSold(x, sold) })},
		{"shop refusal", economy.ShopRefusal(c, shopNo), tg(func(x screens.Context) *presenter.Response { return screens.ShopRefusal(x, shopNo) })},
		{"auctions", economy.Auctions(c, auctions), tg(func(x screens.Context) *presenter.Response { return screens.Auctions(x, auctions) })},
		{"auction", economy.AuctionDetail(c, auctionV), tg(func(x screens.Context) *presenter.Response { return screens.AuctionDetail(x, auctionV) })},
		{"auction new", economy.AuctionNew(c, auctionNew), tg(func(x screens.Context) *presenter.Response { return screens.AuctionNew(x, auctionNew) })},
		{"auction opened", economy.AuctionOpened(c, opened), tg(func(x screens.Context) *presenter.Response { return screens.AuctionOpened(x, opened) })},
		{"bid placed", economy.BidPlaced(c, bid), tg(func(x screens.Context) *presenter.Response { return screens.BidPlaced(x, bid) })},
		{"my auctions", economy.MyAuctions(c, mine), tg(func(x screens.Context) *presenter.Response { return screens.MyAuctions(x, mine) })},
		{"auction refusal", economy.AuctionRefusal(c, auctionNo), tg(func(x screens.Context) *presenter.Response { return screens.AuctionRefusal(x, auctionNo) })},
	}
}
