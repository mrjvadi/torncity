package economy

import (
	"time"
)

// Callback addresses of the market.
const (
	AddrMarket       = "market:list"
	AddrMarketBook   = "market:book"
	AddrMarketOrder  = "market:order"
	AddrMarketCancel = "market:cancel"
	AddrMarketMine   = "market:mine"
)

// Market order sides, as the core spells them.
const (
	SideBuy  = "buy"
	SideSell = "sell"
)

// BookSummary is one good's book in a city, summed.
type BookSummary struct {
	Item Named
	// Shelf is where the good sits in the item tree (a filter for a client).
	Shelf                  ShelfRef
	BestBid, BestAsk, Last int64
}

// MarketView is a city's books.
type MarketView struct {
	CityCode, City string
	Books          []BookSummary
	// Yours are goods the player carries with no book yet here.
	Yours    []Named
	AtMarket bool
	// Way is the walk to the market when the player is elsewhere.
	Way *Way
	// Unavailable is set when the settlement has no market standing: the
	// view has no other facts.
	Unavailable *Unavailable
	// Village is the settlement book's stalls and rates; nil in a city's book.
	Village *VillageBookView
}

// VillageBookView is a settlement's market as a stall holder meets it (ADR 0040
// section 5): the stalls it has and how many are taken, how many one player
// may hold and holds, the warden's rates in basis points (listing fee when an
// order takes a stall, dues on every trade), and whether it is market day
// (listing fee waived, dues halved).
type VillageBookView struct {
	Stalls, StallsUsed  int
	PerPlayer, Mine     int
	ListingBPS, DuesBPS int64
	MarketDay           bool
}

// BookLevel is one price on a side of a book and how much rests there.
type BookLevel struct {
	Price, Qty int64
}

// TradeLine is one recent trade.
type TradeLine struct {
	Qty, Price int64
	At         time.Time
}

// BookView is one good's book.
type BookView struct {
	Item           Named
	CityCode, City string
	Bids, Asks     []BookLevel
	Trades         []TradeLine
	// Reference is the price the preset buttons start from: the last trade,
	// else the good's base price.
	Reference int64
	Holding   int64
	AtMarket  bool
	// Way is the walk to the market when the player is elsewhere.
	Way *Way
	// Nonce binds the sell buttons: one press is one order.
	Nonce string
	// Unavailable is set when the settlement has no market standing.
	Unavailable *Unavailable
	// Village is the settlement book's stalls and rates; nil in a city's book.
	Village *VillageBookView
}

// MarketCheckoutView is a buy order's escrow and the ways to pay it.
type MarketCheckoutView struct {
	Item       Named
	Qty, Price int64
	Reserve    int64
	Payment    PaymentChoice
	Nonce      string
}

// OrderPlacedView is an order placed: what traded at once, what rests.
type OrderPlacedView struct {
	// ListingFee is what the order paid the settlement's treasury to take a stall
	// on the village book; zero in a city's book and on a market day.
	ListingFee int64
	Item        Named
	Side        string
	No          int64
	Qty, Filled int64
	Price       int64
	Rests       bool
	Spent, Got  int64
	ExpiresAt   time.Time
	Method      string
	// Embargoed is how many offers on the other side the order would have
	// met but may not: their owners' country and the player's are under a
	// trade embargo (docs/adr/0022).
	Embargoed int
}

// OrderCancelledView is an order taken off the book.
type OrderCancelledView struct {
	Item   Named
	Side   string
	No     int64
	Left   int64
	Refund int64
}

// OrderLine is one of the player's orders.
type OrderLine struct {
	No             int64
	Item           Named
	Side           string
	Qty, Filled    int64
	Price          int64
	Status         string
	CityCode, City string
	ExpiresAt      time.Time
}

// MyOrdersView is the player's orders.
type MyOrdersView struct {
	Orders []OrderLine
}

// Market refusal kinds.
const (
	MarketRefusedNotTraded = "not_traded"
	MarketRefusedTooBig    = "too_big"
	MarketRefusedTooMany   = "too_many"
	MarketRefusedNotEnough = "not_enough"
	MarketRefusedNoOrder   = "no_order"
	MarketRefusedClosed    = "closed"
	// MarketRefusedUnavailable: the settlement has no market standing.
	MarketRefusedUnavailable = "unavailable"
	// MarketRefusedStallsFull: every stall of the village book is taken.
	// MarketRefusedStallLimit: the player holds the most stalls they may.
	MarketRefusedStallsFull = "stalls_full"
	MarketRefusedStallLimit = "stall_limit"
)

// MarketRefusalView is a refused market request.
type MarketRefusalView struct {
	Kind  string
	Item  Named
	Count int
	// Unavailable is set on kind "unavailable": what the settlement lacks.
	Unavailable *Unavailable
}
