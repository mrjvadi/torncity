package economy

import (
	"time"
)

// Addresses of the gold dealer.
const (
	AddrGold     = "gold:show"
	AddrGoldBuy  = "gold:buy"
	AddrGoldSell = "gold:sell"
)

// GoldPoint is one period's price.
type GoldPoint struct {
	Price int64
	At    time.Time
}

// GoldView is the dealer's counter.
type GoldView struct {
	// Buy is what a gram costs, Sell what the dealer pays for one; Mid
	// its price and Prev the one before.
	Buy, Sell, Mid, Prev int64
	Stock                int64
	History              []GoldPoint
	// Grams is what the player holds, Cost what it cost them; left out of
	// a shared screen.
	Grams, Cost int64
	// Options are the grams offered.
	Options    []int64
	NextAt     time.Time
	Notice     string
	NoticeArgs map[string]any
	// Unavailable is set, with nothing else, when the service is not offered
	// where the player stands.
	Unavailable *Unavailable
}

// GoldTradeView is a trade with the dealer: its confirmation (a purchase's
// ways to pay, a sale's button) or what it came to.
type GoldTradeView struct {
	Side    string
	Grams   int64
	Price   int64
	Total   int64
	Payment PaymentChoice
	Nonce   string
}
