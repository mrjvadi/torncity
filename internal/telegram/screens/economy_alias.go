package screens

import "github.com/mrjvadi/torncity/internal/presentation/economy"

const AddrBank = economy.AddrBank
const AddrDeposit = economy.AddrDeposit
const AddrWithdraw = economy.AddrWithdraw
const AddrPay = economy.AddrPay
const AddrPaySend = economy.AddrPaySend
const PayCash = economy.PayCash
const PayCard = economy.PayCard

type AmountOption = economy.AmountOption
type BankView = economy.BankView
type PayView = economy.PayView
type PayConfirmView = economy.PayConfirmView
type PaySentView = economy.PaySentView

const MethodCash = economy.MethodCash
const MethodCard = economy.MethodCard

type PaymentDeclinedView = economy.PaymentDeclinedView

const AddrBudget = economy.AddrBudget

type BudgetLineView = economy.BudgetLineView
type BudgetPeriodView = economy.BudgetPeriodView
type BudgetView = economy.BudgetView

const AddrGold = economy.AddrGold
const AddrGoldBuy = economy.AddrGoldBuy
const AddrGoldSell = economy.AddrGoldSell

type GoldPoint = economy.GoldPoint
type GoldView = economy.GoldView
type GoldTradeView = economy.GoldTradeView

const AddrLoanHub = economy.AddrLoanHub
const AddrLoanOffer = economy.AddrLoanOffer
const AddrLoanTake = economy.AddrLoanTake
const AddrLoanView = economy.AddrLoanView
const AddrLoanRepay = economy.AddrLoanRepay
const AddrSavings = economy.AddrSavings
const AddrSavingsDeposit = economy.AddrSavingsDeposit
const AddrSavingsWithdraw = economy.AddrSavingsWithdraw
const AddrInsurance = economy.AddrInsurance
const AddrInsureBuy = economy.AddrInsureBuy
const AddrInsureCancel = economy.AddrInsureCancel
const CommandSavingsDeposit = economy.CommandSavingsDeposit
const CommandSavingsWithdraw = economy.CommandSavingsWithdraw
const FinanceYes = economy.FinanceYes

type CreditView = economy.CreditView
type LoanProductLine = economy.LoanProductLine
type LoanLine = economy.LoanLine
type FinanceHubView = economy.FinanceHubView
type LoanOption = economy.LoanOption
type PledgeLine = economy.PledgeLine
type LoanOfferView = economy.LoanOfferView
type LoanConfirmView = economy.LoanConfirmView
type LoanDetailView = economy.LoanDetailView
type SavingsView = economy.SavingsView
type InsuranceProductLine = economy.InsuranceProductLine
type PolicyLine = economy.PolicyLine
type InsuranceView = economy.InsuranceView
type InsureConfirmView = economy.InsureConfirmView

const FinanceRefusedNoBank = economy.FinanceRefusedNoBank
const FinanceRefusedScore = economy.FinanceRefusedScore
const FinanceRefusedTooMany = economy.FinanceRefusedTooMany
const FinanceRefusedBankDry = economy.FinanceRefusedBankDry
const FinanceRefusedAmount = economy.FinanceRefusedAmount
const FinanceRefusedPledge = economy.FinanceRefusedPledge
const FinanceRefusedNotOwner = economy.FinanceRefusedNotOwner
const FinanceRefusedShort = economy.FinanceRefusedShort
const FinanceRefusedSavingsCap = economy.FinanceRefusedSavingsCap
const FinanceRefusedNoProduct = economy.FinanceRefusedNoProduct
const FinanceRefusedHeld = economy.FinanceRefusedHeld
const FinanceRefusedPledged = economy.FinanceRefusedPledged
const FinanceRefusedNotListed = economy.FinanceRefusedNotListed
const FinanceRefusedListed = economy.FinanceRefusedListed
const FinanceRefusedTooYoung = economy.FinanceRefusedTooYoung
const FinanceRefusedTooSmall = economy.FinanceRefusedTooSmall
const FinanceRefusedInDebt = economy.FinanceRefusedInDebt
const FinanceRefusedFloat = economy.FinanceRefusedFloat
const FinanceRefusedNoShares = economy.FinanceRefusedNoShares
const FinanceRefusedNoMoney = economy.FinanceRefusedNoMoney
const FinanceRefusedTooManyOrd = economy.FinanceRefusedTooManyOrd
const FinanceRefusedOrder = economy.FinanceRefusedOrder
const FinanceRefusedGoldStock = economy.FinanceRefusedGoldStock
const FinanceRefusedGoldHeld = economy.FinanceRefusedGoldHeld
const FinanceRefusedNotYours = economy.FinanceRefusedNotYours
const FinanceRefusedOwnCompany = economy.FinanceRefusedOwnCompany
const FinanceRefusedFundClosed = economy.FinanceRefusedFundClosed
const FinanceRefusedNoPlayerFor = economy.FinanceRefusedNoPlayerFor

type FinanceRefusalView = economy.FinanceRefusalView

const AddrMarket = economy.AddrMarket
const AddrMarketBook = economy.AddrMarketBook
const AddrMarketOrder = economy.AddrMarketOrder
const AddrMarketCancel = economy.AddrMarketCancel
const AddrMarketMine = economy.AddrMarketMine
const SideBuy = economy.SideBuy
const SideSell = economy.SideSell

type BookSummary = economy.BookSummary
type MarketView = economy.MarketView
type BookLevel = economy.BookLevel
type TradeLine = economy.TradeLine
type BookView = economy.BookView
type MarketCheckoutView = economy.MarketCheckoutView
type OrderPlacedView = economy.OrderPlacedView
type OrderCancelledView = economy.OrderCancelledView
type OrderLine = economy.OrderLine
type MyOrdersView = economy.MyOrdersView

const MarketRefusedNotTraded = economy.MarketRefusedNotTraded
const MarketRefusedTooBig = economy.MarketRefusedTooBig
const MarketRefusedTooMany = economy.MarketRefusedTooMany
const MarketRefusedNotEnough = economy.MarketRefusedNotEnough
const MarketRefusedNoOrder = economy.MarketRefusedNoOrder
const MarketRefusedClosed = economy.MarketRefusedClosed

type MarketRefusalView = economy.MarketRefusalView

const AddrShops = economy.AddrShops
const AddrShop = economy.AddrShop
const AddrShopBuy = economy.AddrShopBuy
const AddrShopSellOffers = economy.AddrShopSellOffers
const AddrShopSell = economy.AddrShopSell

type ShopLine = economy.ShopLine
type ShopsView = economy.ShopsView
type ShelfLine = economy.ShelfLine
type ShopView = economy.ShopView
type ShopCheckoutView = economy.ShopCheckoutView
type ShopBoughtView = economy.ShopBoughtView
type SellOffer = economy.SellOffer
type SellOffersView = economy.SellOffersView
type ShopSoldView = economy.ShopSoldView

const ShopRefusedNoShop = economy.ShopRefusedNoShop
const ShopRefusedNotSold = economy.ShopRefusedNotSold
const ShopRefusedSoldOut = economy.ShopRefusedSoldOut
const ShopRefusedNotHeld = economy.ShopRefusedNotHeld
const ShopRefusedNoBuyback = economy.ShopRefusedNoBuyback

type ShopRefusalView = economy.ShopRefusalView

const AddrExchange = economy.AddrExchange
const AddrStock = economy.AddrStock
const AddrStockBuy = economy.AddrStockBuy
const AddrStockSell = economy.AddrStockSell
const AddrStockCancel = economy.AddrStockCancel
const AddrPortfolio = economy.AddrPortfolio
const AddrStockIPO = economy.AddrStockIPO
const AddrStockDividend = economy.AddrStockDividend

type ListedLine = economy.ListedLine
type ExchangeView = economy.ExchangeView
type PriceOption = economy.PriceOption
type StockView = economy.StockView
type StockOrderView = economy.StockOrderView
type HoldingLine = economy.HoldingLine
type OpenOrderLine = economy.OpenOrderLine
type PortfolioView = economy.PortfolioView
type ListingView = economy.ListingView
type DividendView = economy.DividendView

const AddrAuctions = economy.AddrAuctions
const AddrAuction = economy.AddrAuction
const AddrAuctionNew = economy.AddrAuctionNew
const AddrAuctionBid = economy.AddrAuctionBid
const AddrAuctionMine = economy.AddrAuctionMine

type AuctionLine = economy.AuctionLine
type AuctionsView = economy.AuctionsView
type AuctionView = economy.AuctionView
type AuctionNewView = economy.AuctionNewView
type AuctionOpenedView = economy.AuctionOpenedView
type BidPlacedView = economy.BidPlacedView
type MyAuctionsView = economy.MyAuctionsView

const AuctionRefusedNone = economy.AuctionRefusedNone
const AuctionRefusedClosed = economy.AuctionRefusedClosed
const AuctionRefusedTooLow = economy.AuctionRefusedTooLow
const AuctionRefusedOwn = economy.AuctionRefusedOwn
const AuctionRefusedLeading = economy.AuctionRefusedLeading
const AuctionRefusedNotHeld = economy.AuctionRefusedNotHeld
const AuctionRefusedTooMany = economy.AuctionRefusedTooMany
const AuctionRefusedNotSellable = economy.AuctionRefusedNotSellable

type AuctionRefusalView = economy.AuctionRefusalView
