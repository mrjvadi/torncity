package economy

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The economy area's screens, as the core builds them (docs/adr/0039). Each
// constructor takes the view a handler worked out and returns a neutral
// response: the screen's name, the view and the actions the viewer may take
// next. Nothing here is worded, laid out or marked up; the Telegram edge
// (internal/telegram/render) and the web client each do that themselves.
//
// The actions are what the viewer MAY do, whatever an edge decides to show:
// Telegram leaves the player's own buttons out of a group screen, the web
// places the ones its layout has room for.

// Screens, each defined once with the type of its view.
var (
	screenBank        = presentation.Define[BankView](ScreenBank, "economy")
	screenPay         = presentation.Define[PayView](ScreenPay, "economy")
	screenPayHelp     = presentation.Define[PayHelpView](ScreenPayHelp, "economy")
	screenPayConfirm  = presentation.Define[PayConfirmView](ScreenPayConfirm, "economy")
	screenPaySent     = presentation.Define[PaySentView](ScreenPaySent, "economy")
	screenDeclined    = presentation.Define[PaymentDeclinedView](ScreenPaymentDeclined, "economy", presentation.Private())
	screenBudget      = presentation.Define[BudgetView](ScreenBudget, "economy")
	screenGold        = presentation.Define[GoldView](ScreenGold, "economy")
	screenGoldTrade   = presentation.Define[GoldTradeView](ScreenGoldTrade, "economy", presentation.Private())
	screenFinanceHub  = presentation.Define[FinanceHubView](ScreenFinanceHub, "economy", presentation.Private())
	screenLoanOffer   = presentation.Define[LoanOfferView](ScreenLoanOffer, "economy", presentation.Private())
	screenLoanConfirm = presentation.Define[LoanConfirmView](ScreenLoanConfirm, "economy", presentation.Private())
	screenLoanDetail  = presentation.Define[LoanDetailView](ScreenLoanDetail, "economy", presentation.Private())
	screenSavings     = presentation.Define[SavingsView](ScreenSavings, "economy", presentation.Private())
	screenInsurance   = presentation.Define[InsuranceView](ScreenInsurance, "economy", presentation.Private())
	screenInsureAsk   = presentation.Define[InsureConfirmView](ScreenInsureConfirm, "economy", presentation.Private())
	screenFinanceNo   = presentation.Define[FinanceRefusalView](ScreenFinanceRefusal, "economy", presentation.Private(), presentation.Refusal())
	screenExchange    = presentation.Define[ExchangeView](ScreenExchange, "economy")
	screenStock       = presentation.Define[StockPageView](ScreenStock, "economy")
	screenStockOrder  = presentation.Define[StockOrderView](ScreenStockOrder, "economy", presentation.Private())
	screenPortfolio   = presentation.Define[PortfolioView](ScreenPortfolio, "economy", presentation.Private())
	screenListing     = presentation.Define[ListingView](ScreenListing, "economy", presentation.Private())
	screenDividend    = presentation.Define[DividendView](ScreenDividend, "economy", presentation.Private())
	screenMarket      = presentation.Define[MarketView](ScreenMarket, "economy")
	screenBook        = presentation.Define[BookView](ScreenBook, "economy")
	screenMarketPay   = presentation.Define[MarketCheckoutView](ScreenMarketCheckout, "economy")
	screenOrderPlaced = presentation.Define[OrderPlacedView](ScreenOrderPlaced, "economy")
	screenOrderGone   = presentation.Define[OrderCancelledView](ScreenOrderCancelled, "economy")
	screenMyOrders    = presentation.Define[MyOrdersView](ScreenMyOrders, "economy", presentation.Private())
	screenMarketNo    = presentation.Define[MarketRefusalView](ScreenMarketRefusal, "economy", presentation.Refusal())
	screenShops       = presentation.Define[ShopsView](ScreenShops, "economy")
	screenShop        = presentation.Define[ShopView](ScreenShopDetail, "economy")
	screenShopPay     = presentation.Define[ShopCheckoutView](ScreenShopCheckout, "economy")
	screenShopBought  = presentation.Define[ShopBoughtView](ScreenShopBought, "economy")
	screenSellOffers  = presentation.Define[SellOffersView](ScreenSellOffers, "economy")
	screenShopSold    = presentation.Define[ShopSoldView](ScreenShopSold, "economy")
	screenShopNo      = presentation.Define[ShopRefusalView](ScreenShopRefusal, "economy", presentation.Refusal())
	screenNoRoom      = presentation.Define[NoRoomView](ScreenNoRoom, "economy", presentation.Refusal())
	screenAuctions    = presentation.Define[AuctionsView](ScreenAuctions, "economy")
	screenAuction     = presentation.Define[AuctionDetailView](ScreenAuctionDetail, "economy")
	screenAuctionNew  = presentation.Define[AuctionNewView](ScreenAuctionNew, "economy")
	screenAuctionOpen = presentation.Define[AuctionOpenedView](ScreenAuctionOpened, "economy")
	screenBidPlaced   = presentation.Define[BidPlacedView](ScreenBidPlaced, "economy")
	screenMyAuctions  = presentation.Define[MyAuctionsView](ScreenMyAuctions, "economy", presentation.Private())
	screenAuctionNo   = presentation.Define[AuctionRefusalView](ScreenAuctionRefusal, "economy", presentation.Refusal())
)

// ShopQtyChoices are the quantities the checkout offers besides the chosen
// one, when the shelf holds them.
var ShopQtyChoices = []int64{1, 5, 10}

// RefusalCode is the code of a refused request: the area's name and the kind,
// "finance_score", "auction_too_low".
func RefusalCode(area, kind string) string { return area + "_" + kind }

func act(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Do(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func back(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Back(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func refresh(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Refresh(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func confirm(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Confirm(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

// ask is the action whose last value the player types.
func ask(command string, args ...string) presentation.Action {
	return presentation.Do(command, args...).Asking()
}

func n(v int64) string { return strconv.FormatInt(v, 10) }

// payWith adds one action per method that covers the price: build gives the
// command's arguments, the method last.
func payWith(a []presentation.Action, p PaymentChoice, addr string, build func(method string) []string) []presentation.Action {
	for _, m := range p.Usable {
		a = append(a, act(addr, build(m)...).Named("payment."+m))
	}
	return a
}

// ---- the bank and paying a player ----

// Bank is the bank: balances, the city's terms and the amounts to move.
func Bank(c presentation.Ctx, v BankView) *presentation.Response {
	var a []presentation.Action
	if v.Unavailable != nil {
		a = v.Unavailable.actions(AddrHome)
		return screenBank.Response(c.Lang, v, append([]presentation.Action{act(AddrPay).Named("bank.pay")}, a...)...)
	}
	if !v.Travelling && !v.NoCity {
		if v.CanDeposit {
			for _, o := range v.Deposits {
				id := "bank.deposit"
				if o.All {
					id = "bank.deposit_all"
				}
				a = append(a, act(AddrDeposit, n(o.Amount), o.Nonce).Named(id))
			}
			a = append(a, ask("bank.deposit").Named("bank.deposit_custom"))
		}
		if !v.Jailed && v.CanWithdraw {
			for _, o := range v.Withdrawals {
				id := "bank.withdraw"
				if o.All {
					id = "bank.withdraw_all"
				}
				a = append(a, act(AddrWithdraw, n(o.Amount), o.Nonce).Named(id))
			}
			a = append(a, ask("bank.withdraw").Named("bank.withdraw_custom"))
		}
	}
	a = append(a, act(AddrPay).Named("bank.pay"), act(AddrLoanHub).Named("bank.finance"),
		back(AddrHome), refresh(AddrBank))
	return screenBank.Response(c.Lang, v, a...)
}

// Pay is paying one player: which ways are open, at what fee, and amounts.
func Pay(c presentation.Ctx, v PayView) *presentation.Response {
	var a []presentation.Action
	pay := func(method string, id string, opts []AmountOption) {
		for _, o := range opts {
			name := id
			if o.All {
				name += "_all"
			}
			args := []string{v.PayeeCode, n(o.Amount), method}
			if v.Origin != "" {
				args = append(args, v.Origin)
			}
			a = append(a, act(AddrPay, args...).Named(name))
		}
		args := []string{v.PayeeCode, method}
		if v.Origin != "" {
			args = append(args, v.Origin)
		}
		a = append(a, ask("bank.pay", args...).Named(id+"_custom"))
	}
	if v.Together && v.CanCash {
		pay(MethodCash, "pay.cash", v.CashOptions)
	}
	if v.CanCard {
		pay(MethodCard, "pay.card", v.CardOptions)
	}
	a = append(a, back(AddrBank), refresh(AddrPay, v.PayeeCode))
	return screenPay.Response(c.Lang, v, a...)
}

// PayHelpView is the view of the explanation of how to pay a player: it has no
// facts of its own.
type PayHelpView struct{}

// PayHelp explains how to pay a player when a payment names nobody, and offers
// the search for one.
func PayHelp(c presentation.Ctx) *presentation.Response {
	return screenPayHelp.Response(c.Lang, PayHelpView{}, act(AddrSearch).Named("social.search"), back(AddrBank))
}

// PayConfirm asks the player to confirm one payment.
func PayConfirm(c presentation.Ctx, v PayConfirmView) *presentation.Response {
	args := []string{v.PayeeCode, n(v.Amount), v.Method, v.Nonce}
	if v.Origin != "" {
		args = append(args, v.Origin)
	}
	a := []presentation.Action{
		confirm(AddrPaySend, args...).Named("pay.confirm"),
		act(AddrPay, v.PayeeCode).Named("pay.cancel"),
		back(AddrBank),
	}
	return screenPayConfirm.Response(c.Lang, v, a...)
}

// PaySent is a payment made.
func PaySent(c presentation.Ctx, v PaySentView) *presentation.Response {
	return screenPaySent.Response(c.Lang, v,
		act(AddrBank).Named("bank.show"), act(AddrPay, v.PayeeCode).Named("pay.again"), back(AddrHome))
}

// PaymentDeclined is a charge nothing the player holds can pay.
func PaymentDeclined(c presentation.Ctx, v PaymentDeclinedView) *presentation.Response {
	var a []presentation.Action
	if v.BackLabel != "" && v.Back.Command != "" {
		a = append(a, presentation.Do(v.Back.Command, v.Back.Args...).Named("payment.back"))
	}
	a = append(a, act(AddrBank).Named("bank.show"), back(AddrHome))
	return screenDeclined.Response(c.Lang, v, a...)
}

// ---- the city budget ----

// Budget is a city's budget.
func Budget(c presentation.Ctx, v BudgetView) *presentation.Response {
	if v.NoCity {
		return screenBudget.Response(c.Lang, v, back(AddrGovCity))
	}
	var a []presentation.Action
	if v.CanPropose && v.Lever != "" {
		a = append(a, act(AddrGovLever, v.Lever, v.City.Code).Named("budget.change"))
	}
	a = append(a, act(AddrBills).Named("budget.bills"), back(AddrGovCity, v.City.Code), refresh(AddrBudget, v.City.Code))
	return screenBudget.Response(c.Lang, v, a...)
}

// ---- the gold dealer ----

// Gold is the gold dealer's price, stock and the amounts to trade.
func Gold(c presentation.Ctx, v GoldView) *presentation.Response {
	if v.Unavailable != nil {
		return screenGold.Response(c.Lang, v, v.Unavailable.actions(AddrHome)...)
	}
	var a []presentation.Action
	for _, g := range v.Options {
		a = append(a, act(AddrGoldBuy, n(g)).Named("gold.buy").About("gold"))
	}
	for _, g := range v.Options {
		if g <= v.Grams {
			a = append(a, act(AddrGoldSell, n(g)).Named("gold.sell").About("gold"))
		}
	}
	a = append(a, act(AddrPortfolio).Named("finance.portfolio"), back(AddrHome), refresh(AddrGold))
	return screenGold.Response(c.Lang, v, a...)
}

// GoldTrade is a purchase's price with the ways to pay it, or a sale's
// confirmation.
func GoldTrade(c presentation.Ctx, v GoldTradeView) *presentation.Response {
	var a []presentation.Action
	if v.Side == SideBuy {
		a = payWith(a, v.Payment, AddrGoldBuy, func(m string) []string { return []string{n(v.Grams), m, v.Nonce} })
	} else {
		a = append(a, confirm(AddrGoldSell, n(v.Grams), v.Nonce).Named("gold.sell_yes"))
	}
	a = append(a, back(AddrGold))
	return screenGoldTrade.Response(c.Lang, v, a...)
}

// ---- the national bank: loans, savings, insurance ----

// FinanceHub is the national bank.
func FinanceHub(c presentation.Ctx, v FinanceHubView) *presentation.Response {
	if v.Unavailable != nil {
		return screenFinanceHub.Response(c.Lang, v, v.Unavailable.actions(AddrBank)...)
	}
	var a []presentation.Action
	for _, p := range v.Products {
		if p.Limit > 0 {
			a = append(a, act(AddrLoanOffer, p.Product.Code).Named("finance.product").About(p.Product.Code))
		}
	}
	for _, l := range v.Loans {
		if l.Status == "active" {
			a = append(a, act(AddrLoanView, n(l.No)).Named("finance.loan"))
		}
	}
	a = append(a,
		act(AddrSavings).Named("finance.savings"), act(AddrInsurance).Named("finance.insurance"),
		act(AddrExchange).Named("finance.exchange"), act(AddrGold).Named("finance.gold"),
		act(AddrPortfolio).Named("finance.portfolio"),
		back(AddrBank), refresh(AddrLoanHub))
	return screenFinanceHub.Response(c.Lang, v, a...)
}

// LoanOffer is one loan product's offer.
func LoanOffer(c presentation.Ctx, v LoanOfferView) *presentation.Response {
	if v.Unavailable != nil {
		return screenLoanOffer.Response(c.Lang, v, v.Unavailable.actions(AddrBank)...)
	}
	var a []presentation.Action
	switch {
	case len(v.Pledges) > 0 && v.Pledge == nil:
		for i := range v.Pledges {
			a = append(a, act(AddrLoanOffer, v.Product.Code, PledgeArg(&v.Pledges[i])).Named("finance.pledge"))
		}
	case len(v.Options) == 0:
	default:
		for _, o := range v.Options {
			a = append(a, act(AddrLoanTake, v.Product.Code, n(o.Amount), n(o.Term), PledgeArg(v.Pledge)).Named("finance.option"))
		}
	}
	a = append(a, back(AddrLoanHub))
	return screenLoanOffer.Response(c.Lang, v, a...)
}

// LoanConfirm is the terms of a loan with the action that takes it.
func LoanConfirm(c presentation.Ctx, v LoanConfirmView) *presentation.Response {
	return screenLoanConfirm.Response(c.Lang, v,
		confirm(AddrLoanTake, v.Product.Code, n(v.Amount), n(v.Term), PledgeArg(v.Pledge), v.Nonce).Named("finance.take"),
		back(AddrLoanOffer, v.Product.Code))
}

// LoanDetail is one loan.
func LoanDetail(c presentation.Ctx, v LoanDetailView) *presentation.Response {
	var a []presentation.Action
	if v.Loan.Status == "active" && v.Payoff > 0 {
		if v.ConfirmOpen {
			a = append(a, confirm(AddrLoanRepay, n(v.Loan.No), v.Nonce).Named("finance.payoff_yes"))
		} else {
			a = append(a, act(AddrLoanRepay, n(v.Loan.No)).Named("finance.payoff"))
		}
	}
	a = append(a, back(AddrLoanHub), refresh(AddrLoanView, n(v.Loan.No)))
	return screenLoanDetail.Response(c.Lang, v, a...)
}

// Savings is a savings account.
func Savings(c presentation.Ctx, v SavingsView) *presentation.Response {
	if v.Unavailable != nil {
		return screenSavings.Response(c.Lang, v, v.Unavailable.actions(AddrBank)...)
	}
	var a []presentation.Action
	for _, x := range v.Deposits {
		a = append(a, act(AddrSavingsDeposit, n(x)).Named("savings.deposit"))
	}
	for _, x := range v.Withdrawals {
		a = append(a, act(AddrSavingsWithdraw, n(x)).Named("savings.withdraw"))
	}
	a = append(a, ask(CommandSavingsDeposit).Named("savings.deposit_custom"),
		ask(CommandSavingsWithdraw).Named("savings.withdraw_custom"),
		back(AddrLoanHub), refresh(AddrSavings))
	return screenSavings.Response(c.Lang, v, a...)
}

// Insurance is the insurance counter: products and the player's policies.
func Insurance(c presentation.Ctx, v InsuranceView) *presentation.Response {
	if v.Unavailable != nil {
		return screenInsurance.Response(c.Lang, v, v.Unavailable.actions(AddrBank)...)
	}
	var a []presentation.Action
	for _, p := range v.Products {
		switch {
		case p.Covers == "war_damage":
			for _, t := range p.Targets {
				a = append(a, act(AddrInsureBuy, p.Product.Code, n(t.No)).Named("insurance.insure_property").About(p.Product.Code))
			}
		case !p.Held:
			a = append(a, act(AddrInsureBuy, p.Product.Code, "0").Named("insurance.insure").About(p.Product.Code))
		}
	}
	for _, p := range v.Policies {
		if p.Status == "active" && v.Cancel == nil {
			a = append(a, act(AddrInsureCancel, n(p.No)).Named("insurance.cancel"))
		}
	}
	if v.Cancel != nil {
		a = append(a, confirm(AddrInsureCancel, n(v.Cancel.No), FinanceYes).Named("insurance.cancel_yes"))
	}
	a = append(a, back(AddrLoanHub), refresh(AddrInsurance))
	return screenInsurance.Response(c.Lang, v, a...)
}

// InsureConfirm is a policy's price with the ways to pay it.
func InsureConfirm(c presentation.Ctx, v InsureConfirmView) *presentation.Response {
	prop := "0"
	if v.Property != nil {
		prop = n(v.Property.No)
	}
	a := payWith(nil, v.Payment, AddrInsureBuy, func(m string) []string { return []string{v.Product.Code, prop, m} })
	a = append(a, back(AddrInsurance))
	return screenInsureAsk.Response(c.Lang, v, a...)
}

// FinanceRefusal is a refused finance request.
func FinanceRefusal(c presentation.Ctx, v FinanceRefusalView) *presentation.Response {
	args := map[string]any{}
	if v.Amount != 0 {
		args["amount"] = v.Amount
	}
	if v.Score != 0 {
		args["score"] = v.Score
	}
	if v.Count != 0 {
		args["count"] = v.Count
	}
	if v.Wait > 0 {
		args["remaining_seconds"] = int64((v.Wait + 999999999) / 1e9)
	}
	if len(args) == 0 {
		args = nil
	}
	return screenFinanceNo.Response(c.Lang, v, presentation.BackTo(v.Back, presentation.RefOfAddress(AddrLoanHub))).
		Refused(RefusalCode("finance", v.Kind), args)
}

// ---- the stock exchange ----

// Exchange is every listed company.
func Exchange(c presentation.Ctx, v ExchangeView) *presentation.Response {
	if v.Unavailable != nil {
		return screenExchange.Response(c.Lang, v, v.Unavailable.actions(AddrHome)...)
	}
	var a []presentation.Action
	for _, l := range v.Lines {
		a = append(a, act(AddrStock, l.Company.Code).Named("stock.open").About(l.Company.Code))
	}
	a = append(a, act(AddrPortfolio).Named("finance.portfolio"), back(AddrHome), refresh(AddrExchange))
	return screenExchange.Response(c.Lang, v, a...)
}

// Stock is one company on the exchange.
func Stock(c presentation.Ctx, v StockPageView) *presentation.Response {
	if v.Unavailable != nil {
		return screenStock.Response(c.Lang, v, v.Unavailable.actions(AddrHome)...)
	}
	var a []presentation.Action
	if v.Listed {
		for _, o := range v.Buys {
			a = append(a, act(AddrStockBuy, v.Company.Code, n(o.Qty), n(o.Price)).Named("stock.buy").About(v.Company.Code))
		}
		for _, o := range v.Sells {
			a = append(a, act(AddrStockSell, v.Company.Code, n(o.Qty), n(o.Price)).Named("stock.sell").About(v.Company.Code))
		}
	}
	if v.Owner {
		if !v.Listed {
			a = append(a, act(AddrStockIPO, v.Company.Code).Named("stock.ipo").About(v.Company.Code))
		}
		a = append(a, act(AddrStockDividend, v.Company.Code).Named("stock.dividend").About(v.Company.Code))
	}
	a = append(a, back(AddrExchange), refresh(AddrStock, v.Company.Code))
	return screenStock.Response(c.Lang, v, a...)
}

// StockOrder is an order about to be placed, or one just placed.
func StockOrder(c presentation.Ctx, v StockOrderView) *presentation.Response {
	var a []presentation.Action
	if !v.Placed {
		addr := AddrStockBuy
		if v.Side == "sell" {
			addr = AddrStockSell
		}
		a = append(a, confirm(addr, v.Company.Code, n(v.Qty), n(v.Price), v.Nonce).Named("stock.confirm_"+v.Side))
	} else if v.Rests {
		a = append(a, act(AddrStockCancel, n(v.No)).Named("stock.cancel"))
	}
	a = append(a, act(AddrStock, v.Company.Code).Named("stock.open").About(v.Company.Code), back(AddrPortfolio))
	return screenStockOrder.Response(c.Lang, v, a...)
}

// Portfolio is everything a player invested in.
func Portfolio(c presentation.Ctx, v PortfolioView) *presentation.Response {
	if v.Unavailable != nil {
		return screenPortfolio.Response(c.Lang, v, v.Unavailable.actions(AddrHome)...)
	}
	var a []presentation.Action
	for _, h := range v.Holdings {
		a = append(a, act(AddrStock, h.Company.Code).Named("stock.open").About(h.Company.Code))
	}
	for _, o := range v.Orders {
		a = append(a, act(AddrStockCancel, n(o.No)).Named("stock.cancel"))
	}
	a = append(a, act(AddrExchange).Named("finance.exchange"), act(AddrGold).Named("finance.gold"),
		act(AddrSavings).Named("finance.savings"), back(AddrLoanHub), refresh(AddrPortfolio))
	return screenPortfolio.Response(c.Lang, v, a...)
}

// Listing is a company's listing on the exchange.
func Listing(c presentation.Ctx, v ListingView) *presentation.Response {
	var a []presentation.Action
	switch {
	case v.Refused != "":
	case v.Chosen != nil:
		a = append(a, confirm(AddrStockIPO, v.Company.Code, n(v.Chosen.Qty), n(v.Chosen.Price), v.Nonce).Named("stock.ipo_yes"))
	default:
		for _, f := range v.Floats {
			for _, p := range v.Prices {
				a = append(a, act(AddrStockIPO, v.Company.Code, n(f.Qty), n(p.Price)).Named("stock.ipo_option"))
			}
		}
	}
	a = append(a, back(AddrStock, v.Company.Code))
	return screenListing.Response(c.Lang, v, a...)
}

// Dividend is a dividend to declare, its confirmation or what it paid.
func Dividend(c presentation.Ctx, v DividendView) *presentation.Response {
	var a []presentation.Action
	switch {
	case v.Refused != "":
	case v.Paid > 0:
	case v.Chosen != nil:
		a = append(a, confirm(AddrStockDividend, v.Company.Code, n(v.Chosen.Price), v.Nonce).Named("stock.dividend_yes"))
	default:
		for _, o := range v.Options {
			a = append(a, act(AddrStockDividend, v.Company.Code, n(o.Price)).Named("stock.dividend_option"))
		}
	}
	a = append(a, back(AddrStock, v.Company.Code))
	return screenDividend.Response(c.Lang, v, a...)
}

// ---- the item market ----

// Market is a city's books.
func Market(c presentation.Ctx, v MarketView) *presentation.Response {
	if v.Unavailable != nil {
		return screenMarket.Response(c.Lang, v, v.Unavailable.actions(AddrHome)...)
	}
	var a []presentation.Action
	for _, b := range v.Books {
		a = append(a, act(AddrMarketBook, b.Item.Code).Named("market.book").About(b.Item.Code))
	}
	for _, it := range v.Yours {
		a = append(a, act(AddrMarketBook, it.Code).Named("market.book").About(it.Code))
	}
	a = append(a, act(AddrMarketMine).Named("market.mine"), act(AddrAuctions).Named("market.auctions"),
		act(AddrCompanyGoods).Named("market.goods"))
	if !v.AtMarket {
		if w, ok := walk(v.Way, "market.list"); ok {
			a = append(a, w)
		}
	}
	a = append(a, back(AddrMap), refresh(AddrMarket))
	return screenMarket.Response(c.Lang, v, a...)
}

// Book is one good's book, with the orders the player may place.
func Book(c presentation.Ctx, v BookView) *presentation.Response {
	if v.Unavailable != nil {
		return screenBook.Response(c.Lang, v, v.Unavailable.actions(AddrHome)...)
	}
	var a []presentation.Action
	item := v.Item.Code
	order := func(side string, qty, price int64, extra ...string) presentation.Action {
		return act(AddrMarketOrder, append([]string{side, item, n(qty), n(price)}, extra...)...).About(item)
	}
	if v.AtMarket {
		if len(v.Asks) > 0 {
			a = append(a, order(SideBuy, 1, v.Asks[0].Price).Named("market.buy_at"))
		}
		a = append(a, order(SideBuy, 1, v.Reference).Named("market.bid_at"))
		if v.Holding > 0 {
			if len(v.Bids) > 0 {
				a = append(a, order(SideSell, 1, v.Bids[0].Price, v.Nonce).Named("market.sell_at"))
			}
			for _, p := range []int64{v.Reference, v.Reference + v.Reference/10} {
				a = append(a, order(SideSell, 1, p, v.Nonce).Named("market.ask_at"))
			}
			if v.Holding > 1 {
				a = append(a, order(SideSell, v.Holding, v.Reference, v.Nonce).Named("market.ask_all"))
			}
		}
	} else if w, ok := walk(v.Way, "market.book", item); ok {
		a = append(a, w)
	}
	a = append(a, act(AddrMarketMine).Named("market.mine"), back(AddrMarket), refresh(AddrMarketBook, item))
	return screenBook.Response(c.Lang, v, a...)
}

// MarketCheckout is a buy order's escrow and the ways to pay it.
func MarketCheckout(c presentation.Ctx, v MarketCheckoutView) *presentation.Response {
	var a []presentation.Action
	if len(v.Payment.Usable) > 0 {
		a = payWith(a, v.Payment, AddrMarketOrder, func(m string) []string {
			return []string{SideBuy, v.Item.Code, n(v.Qty), n(v.Price), v.Nonce, m}
		})
	} else {
		a = append(a, act(AddrBank).Named("bank.show"))
	}
	a = append(a, back(AddrMarketBook, v.Item.Code))
	return screenMarketPay.Response(c.Lang, v, a...)
}

// OrderPlaced is an order placed: what traded at once, what rests.
func OrderPlaced(c presentation.Ctx, v OrderPlacedView) *presentation.Response {
	return screenOrderPlaced.Response(c.Lang, v,
		act(AddrMarketMine).Named("market.mine"),
		act(AddrMarketBook, v.Item.Code).Named("market.book").About(v.Item.Code),
		back(AddrMarket))
}

// OrderCancelled is an order taken off the book.
func OrderCancelled(c presentation.Ctx, v OrderCancelledView) *presentation.Response {
	return screenOrderGone.Response(c.Lang, v, act(AddrMarketMine).Named("market.mine"), back(AddrMarket))
}

// MyOrders is the player's orders, each open one with a cancel.
func MyOrders(c presentation.Ctx, v MyOrdersView) *presentation.Response {
	var a []presentation.Action
	for _, o := range v.Orders {
		if o.Status == "open" {
			a = append(a, act(AddrMarketCancel, n(o.No)).Named("market.cancel"))
		}
	}
	a = append(a, back(AddrMarket), refresh(AddrMarketMine))
	return screenMyOrders.Response(c.Lang, v, a...)
}

// MarketRefusal is a refused market request.
func MarketRefusal(c presentation.Ctx, v MarketRefusalView) *presentation.Response {
	args := map[string]any{}
	if v.Count != 0 {
		args["count"] = v.Count
	}
	if len(args) == 0 {
		args = nil
	}
	if v.Unavailable != nil {
		return screenMarketNo.Response(c.Lang, v, v.Unavailable.actions(AddrHome)...).
			Refused(RefusalCode("market", v.Kind), args)
	}
	return screenMarketNo.Response(c.Lang, v, act(AddrMarket).Named("market.list"), back(AddrHome)).
		Refused(RefusalCode("market", v.Kind), args)
}

// ---- the city shops ----

// Shops is the shops of the player's city, or of one place.
func Shops(c presentation.Ctx, v ShopsView) *presentation.Response {
	var a []presentation.Action
	for _, s := range v.Shops {
		a = append(a, act(AddrShop, s.Shop.Code).Named("shop.open").About(s.Shop.Code))
	}
	refreshAddr := []string{}
	if v.Place != nil {
		a = append(a, act(AddrShops).Named("shop.all"))
		refreshAddr = append(refreshAddr, v.Place.Code)
	}
	a = append(a, act(AddrInventory).Named("item.bag"), back(AddrMap), refresh(AddrShops, refreshAddr...))
	return screenShops.Response(c.Lang, v, a...)
}

// ShopDetail is one shop's shelves.
func ShopDetail(c presentation.Ctx, v ShopView) *presentation.Response {
	var a []presentation.Action
	if v.Here {
		for _, s := range v.Shelves {
			if s.Stock > 0 {
				a = append(a, act(AddrShopBuy, v.Shop.Code, s.Item.Code, "1").Named("shop.buy").About(s.Item.Code))
			}
		}
	} else if v.Walk > 0 {
		if w, ok := walk(&Way{Place: v.Place, Walk: v.Walk}, "shop.view", v.Shop.Code); ok {
			a = append(a, w)
		}
	}
	a = append(a, back(AddrShops), refresh(AddrShop, v.Shop.Code))
	return screenShop.Response(c.Lang, v, a...)
}

// ShopCheckout is the price of a purchase and the ways to pay it.
func ShopCheckout(c presentation.Ctx, v ShopCheckoutView) *presentation.Response {
	var a []presentation.Action
	if len(v.Payment.Usable) > 0 {
		a = payWith(a, v.Payment, AddrShopBuy, func(m string) []string {
			return []string{v.Shop.Code, v.Item.Code, n(v.Qty), m, v.Nonce}
		})
	} else {
		a = append(a, act(AddrBank).Named("bank.show"))
	}
	for _, q := range ShopQtyChoices {
		if q == v.Qty || q > v.Stock {
			continue
		}
		a = append(a, act(AddrShopBuy, v.Shop.Code, v.Item.Code, n(q)).Named("shop.qty"))
	}
	a = append(a, back(AddrShop, v.Shop.Code))
	return screenShopPay.Response(c.Lang, v, a...)
}

// ShopBought is a purchase made.
func ShopBought(c presentation.Ctx, v ShopBoughtView) *presentation.Response {
	return screenShopBought.Response(c.Lang, v,
		act(AddrInventory).Named("item.bag"), act(AddrShop, v.Shop.Code).Named("shop.open").About(v.Shop.Code), back(AddrHome))
}

// SellOffers is who buys a good the player carries.
func SellOffers(c presentation.Ctx, v SellOffersView) *presentation.Response {
	var a []presentation.Action
	for _, o := range v.Offers {
		a = append(a, act(AddrShopSell, o.Shop.Code, v.Ref).Named("shop.sell_to").About(o.Shop.Code))
	}
	a = append(a, back(AddrItem, v.Ref))
	return screenSellOffers.Response(c.Lang, v, a...)
}

// ShopSold is a good sold back.
func ShopSold(c presentation.Ctx, v ShopSoldView) *presentation.Response {
	return screenShopSold.Response(c.Lang, v, act(AddrInventory).Named("item.bag"), back(AddrHome))
}

// NoRoom is a request refused for lack of room: the way to the bags (to free
// some room or put a bag on) and the way back.
func NoRoom(c presentation.Ctx, v NoRoomView) *presentation.Response {
	backTo := v.Back
	if backTo == "" {
		backTo = AddrHome
	}
	return screenNoRoom.Response(c.Lang, v, act(AddrInventory).Named("item.bag"), back(backTo)).
		Refused("no_room", map[string]any{"short": v.Short})
}

// ShopRefusal is a refused shop request.
func ShopRefusal(c presentation.Ctx, v ShopRefusalView) *presentation.Response {
	args := map[string]any{}
	if v.Stock != 0 {
		args["stock"] = v.Stock
	}
	if len(args) == 0 {
		args = nil
	}
	return screenShopNo.Response(c.Lang, v, act(AddrShops).Named("shop.list"), back(AddrHome)).
		Refused(RefusalCode("shop", v.Kind), args)
}

// ---- the auction house ----

// Auctions is a city's open auctions.
func Auctions(c presentation.Ctx, v AuctionsView) *presentation.Response {
	if v.Unavailable != nil {
		return screenAuctions.Response(c.Lang, v, v.Unavailable.actions(AddrMarket)...)
	}
	var a []presentation.Action
	for _, x := range v.Auctions {
		a = append(a, act(AddrAuction, n(x.No)).Named("auction.open"))
	}
	a = append(a, act(AddrAuctionMine).Named("auction.mine"), act(AddrInventory).Named("item.bag"))
	if !v.AtHouse {
		if w, ok := walk(v.Way, "auction.list"); ok {
			a = append(a, w)
		}
	}
	a = append(a, back(AddrMarket), refresh(AddrAuctions))
	return screenAuctions.Response(c.Lang, v, a...)
}

// AuctionDetail is one auction, with the bid the viewer may make.
func AuctionDetail(c presentation.Ctx, v AuctionDetailView) *presentation.Response {
	var a []presentation.Action
	l := v.Line
	switch {
	case l.Mine && l.Status == "open":
	case l.Leading && l.Status == "open":
	case v.Payment != nil && len(v.Payment.Usable) > 0:
		a = payWith(a, *v.Payment, AddrAuctionBid, func(m string) []string {
			return []string{n(l.No), n(v.MinNext), v.Nonce, m}
		})
	}
	a = append(a, back(AddrAuctions), refresh(AddrAuction, n(l.No)))
	return screenAuction.Response(c.Lang, v, a...)
}

// AuctionNew is the choice of a reserve and a length for a piece.
func AuctionNew(c presentation.Ctx, v AuctionNewView) *presentation.Response {
	var a []presentation.Action
	for i := range v.Durations {
		for _, r := range v.Reserves {
			a = append(a, act(AddrAuctionNew, v.Ref, n(r), strconv.Itoa(i), v.Nonce).Named("auction.terms"))
		}
	}
	a = append(a, back(AddrItem, v.Ref))
	return screenAuctionNew.Response(c.Lang, v, a...)
}

// AuctionOpened is an auction opened.
func AuctionOpened(c presentation.Ctx, v AuctionOpenedView) *presentation.Response {
	return screenAuctionOpen.Response(c.Lang, v, act(AddrAuction, n(v.No)).Named("auction.open"), back(AddrAuctions))
}

// BidPlaced is a bid made.
func BidPlaced(c presentation.Ctx, v BidPlacedView) *presentation.Response {
	return screenBidPlaced.Response(c.Lang, v, act(AddrAuction, n(v.No)).Named("auction.open"), back(AddrAuctions))
}

// MyAuctions is what the player sells and bids on.
func MyAuctions(c presentation.Ctx, v MyAuctionsView) *presentation.Response {
	var a []presentation.Action
	for _, x := range v.Auctions {
		a = append(a, act(AddrAuction, n(x.No)).Named("auction.open"))
	}
	a = append(a, back(AddrAuctions), refresh(AddrAuctionMine))
	return screenMyAuctions.Response(c.Lang, v, a...)
}

// AuctionRefusal is a refused auction request.
func AuctionRefusal(c presentation.Ctx, v AuctionRefusalView) *presentation.Response {
	var a []presentation.Action
	if v.No > 0 {
		a = append(a, act(AddrAuction, n(v.No)).Named("auction.open"))
	}
	a = append(a, act(AddrAuctions).Named("auction.house"), back(AddrHome))
	args := map[string]any{}
	if v.MinNext != 0 {
		args["min"] = v.MinNext
	}
	if v.Count != 0 {
		args["count"] = v.Count
	}
	if len(args) == 0 {
		args = nil
	}
	return screenAuctionNo.Response(c.Lang, v, a...).Refused(RefusalCode("auction", v.Kind), args)
}
