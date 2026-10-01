package economy

import (
	"time"
)

// Addresses of the stock exchange.
const (
	AddrExchange      = "stock:list"
	AddrStock         = "stock:view"
	AddrStockBuy      = "stock:buy"
	AddrStockSell     = "stock:sell"
	AddrStockCancel   = "stock:cancel"
	AddrPortfolio     = "stock:mine"
	AddrStockIPO      = "stock:ipo"
	AddrStockDividend = "stock:dividend"
)

// ListedLine is a listed company as the exchange lists it.
type ListedLine struct {
	Company Named
	Type    Named
	City    GovPlace
	// Price is the last price (the listing price before a first trade),
	// Prev the one before; Volume the shares traded lately; Cap the price
	// times all shares.
	Price, Prev, Volume, Cap int64
}

// ExchangeView is the exchange: every listed company.
type ExchangeView struct {
	Lines []ListedLine
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// PriceOption is a price or a quantity an order may be placed at.
type PriceOption struct {
	Qty, Price int64
}

// StockView is one company on the exchange.
type StockView struct {
	Company Named
	Type    Named
	City    GovPlace
	Listed  bool
	// Price is the last price, Prev the one before, IPO the listing
	// price, Book the book value per share; Total all its shares.
	Price, Prev, IPO, Book, Total int64
	Bids, Asks                    []BookLevel
	Trades                        []TradeLine
	// Holding is the viewer's shares (Locked in sell orders), Cost what
	// they paid; left out of a shared screen.
	Holding, Locked, Cost int64
	// Owner says the viewer controls the company: it offers the listing
	// and the dividend. Controller names who controls it.
	Owner      bool
	Controller string
	// Buys and Sells are the orders offered: quantity and price.
	Buys, Sells []PriceOption
	LastDiv     int64
	FeeBPS      int64
	Notice      string
	NoticeArgs  map[string]any
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// StockOrderView is an order about to be placed, or one just placed.
type StockOrderView struct {
	Company   Named
	Side      string
	Qty       int64
	Price     int64
	Reserve   int64
	Bank      int64
	FeeBPS    int64
	Nonce     string
	Placed    bool
	No        int64
	Filled    int64
	Spent     int64
	Got       int64
	Rests     bool
	ExpiresAt time.Time
}

// HoldingLine is one company in a portfolio.
type HoldingLine struct {
	Company Named
	Shares  int64
	Locked  int64
	Price   int64
	Value   int64
	Cost    int64
	Listed  bool
}

// OpenOrderLine is one of the player's open orders.
type OpenOrderLine struct {
	No      int64
	Company Named
	Side    string
	Qty     int64
	Filled  int64
	Price   int64
}

// PortfolioView is everything a player invested in.
type PortfolioView struct {
	Holdings []HoldingLine
	Orders   []OpenOrderLine
	Gold     int64
	GoldVal  int64
	Savings  int64
	// Value is the whole portfolio; Gain what it made over what went in.
	Value, Gain int64
	Notice      string
	NoticeArgs  map[string]any
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// ListingView is a company's listing: whether it may list, and the choices.
type ListingView struct {
	Company Named
	// Refused is why it may not list yet ("" when it may); Age and
	// Revenue what it has, MinAge and MinRevenue what it needs.
	Refused             string
	Age, MinAge         time.Duration
	Revenue, MinRevenue int64
	Book                int64
	Total               int64
	Fee                 int64
	// Floats are the shares offered (bps of the company, and shares);
	// Prices the prices (bps of book, and the price).
	Floats, Prices []PriceOption
	// Chosen, with Nonce, is the listing about to be made.
	Chosen *PriceOption
	Nonce  string
}

// DividendView is a dividend to declare.
type DividendView struct {
	Company Named
	// Free is the company's money that may be paid out; TaxBPS the tax on
	// it; Total its shares.
	Free    int64
	TaxBPS  int64
	Total   int64
	Options []PriceOption
	Chosen  *PriceOption
	Nonce   string
	// Paid is a dividend just paid: per share and in all.
	Paid, PerShare int64
	Holders        int64
	Refused        string
}
