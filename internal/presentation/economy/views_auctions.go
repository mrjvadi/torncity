package economy

import (
	"time"
)

// Callback addresses of the auction house.
const (
	AddrAuctions    = "auction:list"
	AddrAuction     = "auction:view"
	AddrAuctionNew  = "auction:new"
	AddrAuctionBid  = "auction:bid"
	AddrAuctionMine = "auction:mine"
)

// AuctionLine is one auction on a list.
type AuctionLine struct {
	No        int64
	Item      Named
	Quality   int
	HighBid   int64
	Reserve   int64
	Remaining time.Duration
	EndsAt    time.Time
	Status    string
	// Mine marks the viewer's own auction; Leading the viewer's standing
	// bid.
	Mine, Leading bool
}

// AuctionsView is the open auctions of a city.
type AuctionsView struct {
	CityCode, City string
	Auctions       []AuctionLine
	AtHouse        bool
	// Way is the walk to the auction house when the player is elsewhere.
	Way *Way
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// AuctionView is one auction in detail, with the bid the viewer may make.
type AuctionView struct {
	Line AuctionLine
	// MinNext is the least a new bid must be; Bids how many were made.
	MinNext int64
	Bids    int
	// Payment is how the next bid can be paid, nil when the viewer cannot
	// bid (their own auction, their standing bid, closed, not at the house).
	Payment *PaymentChoice
	Nonce   string
	// Seller names the seller.
	Seller string
}

// AuctionNewView is a piece the player may put up, and the terms offered.
type AuctionNewView struct {
	Item      Named
	Ref       string
	Quality   int
	Reserves  []int64
	Durations []time.Duration
	// Duration is the chosen length's index, when a reserve is being
	// chosen.
	Nonce string
}

// AuctionOpenedView is an auction opened.
type AuctionOpenedView struct {
	No       int64
	Item     Named
	Reserve  int64
	Duration time.Duration
	EndsAt   time.Time
}

// BidPlacedView is a bid made.
type BidPlacedView struct {
	No     int64
	Item   Named
	Amount int64
	Method string
	EndsAt time.Time
}

// MyAuctionsView is what the player sells and bids on.
type MyAuctionsView struct {
	Auctions []AuctionLine
}

// Auction refusal kinds.
const (
	AuctionRefusedNone        = "none"
	AuctionRefusedClosed      = "closed"
	AuctionRefusedTooLow      = "too_low"
	AuctionRefusedOwn         = "own"
	AuctionRefusedLeading     = "leading"
	AuctionRefusedNotHeld     = "not_held"
	AuctionRefusedTooMany     = "too_many"
	AuctionRefusedNotSellable = "not_sellable"
)

// AuctionRefusalView is a refused auction request.
type AuctionRefusalView struct {
	Kind    string
	No      int64
	MinNext int64
	Count   int
}
