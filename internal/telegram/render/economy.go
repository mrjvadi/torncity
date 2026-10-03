package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The economy and finance area (docs/adr/0039): the bank, paying a player, the
// national bank, the gold dealer, the stock exchange, the item market, the
// city shops, the auction house and the city budget. Each is drawn for
// Telegram by the renderer internal/telegram/screens has always had, from the
// view the core now sends as data.
func init() {
	Register(economy.ScreenBank, screens.Bank)
	Register(economy.ScreenPay, screens.Pay)
	RegisterEmpty(economy.ScreenPayHelp, screens.PayHelp)
	Register(economy.ScreenPayConfirm, screens.PayConfirm)
	Register(economy.ScreenPaySent, screens.PaySent)
	Register(economy.ScreenPaymentDeclined, screens.PaymentDeclined)
	Register(economy.ScreenBudget, screens.Budget)
	Register(economy.ScreenGold, screens.Gold)
	Register(economy.ScreenGoldTrade, screens.GoldTrade)
	Register(economy.ScreenFinanceHub, screens.FinanceHub)
	Register(economy.ScreenLoanOffer, screens.LoanOffer)
	Register(economy.ScreenLoanConfirm, screens.LoanConfirm)
	Register(economy.ScreenLoanDetail, screens.LoanDetail)
	Register(economy.ScreenSavings, screens.Savings)
	Register(economy.ScreenInsurance, screens.Insurance)
	Register(economy.ScreenInsureConfirm, screens.InsureConfirm)
	Register(economy.ScreenFinanceRefusal, screens.FinanceRefusal)
	Register(economy.ScreenExchange, screens.Exchange)
	Register(economy.ScreenStock, screens.Stock)
	Register(economy.ScreenStockOrder, screens.StockOrder)
	Register(economy.ScreenPortfolio, screens.Portfolio)
	Register(economy.ScreenListing, screens.Listing)
	Register(economy.ScreenDividend, screens.Dividend)
	Register(economy.ScreenMarket, screens.Market)
	Register(economy.ScreenBook, screens.Book)
	Register(economy.ScreenMarketCheckout, screens.MarketCheckout)
	Register(economy.ScreenOrderPlaced, screens.OrderPlaced)
	Register(economy.ScreenOrderCancelled, screens.OrderCancelled)
	Register(economy.ScreenMyOrders, screens.MyOrders)
	Register(economy.ScreenMarketRefusal, screens.MarketRefusal)
	Register(economy.ScreenShops, screens.Shops)
	Register(economy.ScreenShopDetail, screens.ShopDetail)
	Register(economy.ScreenShopCheckout, screens.ShopCheckout)
	Register(economy.ScreenShopBought, screens.ShopBought)
	Register(economy.ScreenSellOffers, screens.SellOffers)
	Register(economy.ScreenShopSold, screens.ShopSold)
	Register(economy.ScreenShopRefusal, screens.ShopRefusal)
	Register(economy.ScreenNoRoom, screens.NoRoom)
	Register(economy.ScreenAuctions, screens.Auctions)
	Register(economy.ScreenAuctionDetail, screens.AuctionDetail)
	Register(economy.ScreenAuctionNew, screens.AuctionNew)
	Register(economy.ScreenAuctionOpened, screens.AuctionOpened)
	Register(economy.ScreenBidPlaced, screens.BidPlaced)
	Register(economy.ScreenMyAuctions, screens.MyAuctions)
	Register(economy.ScreenAuctionRefusal, screens.AuctionRefusal)
}
