package economy

import (
	"time"
)

// Callback addresses of the shops.
const (
	AddrShops          = "shop:list"
	AddrShop           = "shop:view"
	AddrShopBuy        = "shop:buy"
	AddrShopSellOffers = "shop:offers"
	AddrShopSell       = "shop:sell"
)

// ShopLine is one shop of the city.
type ShopLine struct {
	Shop  Named
	Place Named
	// Here marks a shop at the place the player stands.
	Here bool
}

// ShopsView is the shops of the player's city.
type ShopsView struct {
	CityCode, City string
	Shops          []ShopLine
	// Place, when set, is the one place whose shops are listed (the map's
	// «🛒 مغازه‌های اینجا»); nil lists the whole city's.
	Place *Named
}

// ShelfLine is one good on a shelf, priced now.
type ShelfLine struct {
	Item Named
	// Shelf is where the good sits in the item tree (a filter for a client).
	Shelf ShelfRef
	Price int64
	Stock int64
	// Busy: demand has raised the price.
	Busy bool
	// Buyback is what the shop pays for one; zero when it does not buy it.
	Buyback     int64
	NextRestock time.Time
}

// ShopView is one shop's shelves.
type ShopView struct {
	Shop, Place Named
	Here        bool
	// Walk is the real time the walk to the shop takes, when the player
	// is elsewhere and not already walking.
	Walk    time.Duration
	Shelves []ShelfLine
	TaxBPS  int
}

// ShopCheckoutView is the price of a purchase and the ways to pay it.
type ShopCheckoutView struct {
	Shop, Item Named
	Qty        int64
	Unit       int64
	Total, Tax int64
	TaxBPS     int
	Stock      int64
	Payment    PaymentChoice
	// Nonce is shared by every pay button, so one purchase is one.
	Nonce string
}

// ShopBoughtView is a purchase made.
type ShopBoughtView struct {
	Shop, Item Named
	Qty        int64
	Total, Tax int64
	Method     string
}

// SellOffer is one shop that buys a good, and at what.
type SellOffer struct {
	Shop, Place Named
	Price       int64
}

// SellOffersView is who buys a good the player carries.
type SellOffersView struct {
	Item   Named
	Ref    string
	Offers []SellOffer
}

// ShopSoldView is a good sold back.
type ShopSoldView struct {
	Shop, Item Named
	Price      int64
	Left       int64
}

// Shop refusal kinds.
const (
	ShopRefusedNoShop    = "no_shop"
	ShopRefusedNotSold   = "not_sold"
	ShopRefusedSoldOut   = "sold_out"
	ShopRefusedNotHeld   = "not_held"
	ShopRefusedNoBuyback = "no_buyback"
)

// NoRoomView is a purchase, an order or a claim refused because the goods do
// not fit in the player's bags (storage and market audit P1, ADR 0040 req. 3):
// what was asked, the room it needs, the room there is, and so what is missing.
// The clients word it; the view carries codes and numbers only.
type NoRoomView struct {
	Item Named
	Qty  int64
	// NeedSpace, FreeSpace and Short are in «جا»; NeedG and FreeG in grams.
	NeedSpace, FreeSpace, Short int64
	NeedG, FreeG                int64
	// Heavy: the load, not the space, stopped it.
	Heavy bool
	// Back is the address the screen's back button leads to.
	Back string
}

// ShopRefusalView is a refused shop request.
type ShopRefusalView struct {
	Kind        string
	Shop, Item  Named
	Stock       int64
	NextRestock time.Time
}
