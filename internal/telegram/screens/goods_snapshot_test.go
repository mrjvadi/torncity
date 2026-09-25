package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Goods join the snapshot harness as two areas: what a player carries and
// the city shops (goods.txt), and the player market and the auction house
// (trade.txt).
func init() {
	snapshotAreas["goods"] = goodsSnapshots
	snapshotAreas["trade"] = tradeSnapshots
}

var (
	snapBread    = Named{Code: "bread", Name: "Bread"}
	snapBandage  = Named{Code: "bandage", Name: "Bandage"}
	snapLockpick = Named{Code: "lockpick_set", Name: "Lockpick set"}
	snapPhone    = Named{Code: "phone", Name: "Phone"}
	snapHardware = Named{Code: "hardware_store", Name: "Hardware store"}
	snapGrocery  = Named{Code: "grocery", Name: "Grocery"}
	snapPawn     = Named{Code: "pawnshop", Name: "Pawnshop"}
	snapBazaar   = Named{Code: "bazaar", Name: "Bazaar"}
	snapHomes    = Named{Code: "residential_area", Name: "Residential area"}
)

func goodsSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	add("Bag", Inventory(c, InventoryView{
		Lines: []InventoryLine{
			{Item: snapBread, Category: "food", Qty: 3},
			{Item: snapBandage, Category: "medicine", Qty: 1},
			{Item: snapLockpick, Category: "gear", Serial: "A1B2C3D4E5", Quality: 62, UsesLeft: 7, Durability: 10},
			{Item: snapPhone, Category: "electronics", Serial: "F0E1D2C3B4", Quality: 55},
		},
		Page: 1, Pages: 1, Total: 4, InEscrow: 2,
	}))
	add("Bag · empty", Inventory(c, InventoryView{Page: 1, Pages: 1}))
	add("Item · bread, a friend here", ItemDetail(c, ItemDetailView{
		Item: snapBread, Category: "food", Qty: 3, Ref: "bread", Worth: 40,
		Effects: []EffectLine{{Target: "energy", Op: "add", Value: 10}, {Target: "happiness", Op: "add", Value: 2}},
		Usable:  true, Tradeable: true, Cooldown: 30 * time.Minute, Nonce: "0a1b2c3d4e5f",
		GiveTo: []Named{{Code: friendCode, Name: who.friend}},
	}))
	add("Item · bandage, still cooling", ItemDetail(c, ItemDetailView{
		Item: snapBandage, Category: "medicine", Qty: 1, Ref: "bandage", Worth: 80,
		Effects: []EffectLine{{Target: "health", Op: "add", Value: 15}},
		Usable:  true, Tradeable: true, Cooldown: time.Hour, CoolingFor: 12 * time.Minute,
		ReadyAt: snapshotNow.Add(12 * time.Minute),
	}))
	add("Item · a lockpick set", ItemDetail(c, ItemDetailView{
		Item: snapLockpick, Category: "gear", Ref: "A1B2C3D4E5", Piece: true, Quality: 62, UsesLeft: 7, Durability: 10,
		Worth: 600, Tradeable: true,
		Gear: &GearLine{
			Crimes:     []Named{{Code: "home_burglary", Name: "Home burglary"}, {Code: "car_break_in", Name: "Car break-in"}},
			SuccessBPS: 1000, CatchBPS: -300, Nerve: -1, Confiscated: true,
		},
	}))
	add("Used · bread", ItemUsed(c, ItemUsedView{
		Item: snapBread, Changes: []VitalChange{{Target: "energy", Before: 40, After: 50, Max: 100}},
		Left: 2, Cooldown: 30 * time.Minute, ReadyAt: snapshotNow.Add(30 * time.Minute),
	}))
	add("Given · bread to a friend", ItemGiven(c, ItemGivenView{Item: snapBread, To: Named{Code: friendCode, Name: who.friend}}))
	add("Notice · a friend gave you bread", ItemReceivedNotice(sent(c), snapBread, who.friend))
	add("Drop · are you sure", DropConfirm(c, ItemDroppedView{Item: snapPhone, Ref: "F0E1D2C3B4", Nonce: "0a1b2c3d4e60"}))
	add("Drop · done", ItemDropped(c, ItemDroppedView{Item: snapPhone}))
	for _, r := range []struct {
		title string
		view  ItemRefusalView
	}{
		{"not carried", ItemRefusalView{Kind: ItemRefusedNotHeld, Item: snapPhone}},
		{"not usable", ItemRefusalView{Kind: ItemRefusedNotUsable, Item: snapLockpick}},
		{"still cooling", ItemRefusalView{Kind: ItemRefusedCooling, Item: snapBandage, Wait: 12 * time.Minute, ReadyAt: snapshotNow.Add(12 * time.Minute)}},
		{"no effect", ItemRefusalView{Kind: ItemRefusedNoEffect, Item: snapBread}},
		{"not tradeable", ItemRefusalView{Kind: ItemRefusedNotTradeable, Item: snapLockpick}},
		{"not together", ItemRefusalView{Kind: ItemRefusedNotTogether, Item: snapBread}},
	} {
		add("Item refused · "+r.title, ItemRefusal(c, r.view))
	}

	add("Shops", Shops(c, ShopsView{CityCode: "ostmarch", City: "Ostmarch", Shops: []ShopLine{
		{Shop: snapGrocery, Place: snapBazaar, Here: true},
		{Shop: snapHardware, Place: snapBazaar, Here: true},
		{Shop: snapPawn, Place: snapHomes},
	}}))
	add("Shops · none", Shops(c, ShopsView{CityCode: "ostmarch", City: "Ostmarch"}))
	add("Shops · at one place", Shops(c, ShopsView{CityCode: "ostmarch", City: "Ostmarch", Place: &snapBazaar,
		Shops: []ShopLine{{Shop: snapGrocery, Place: snapBazaar}, {Shop: snapHardware, Place: snapBazaar}}}))
	shelves := []ShelfLine{
		{Item: snapLockpick, Price: 600, Stock: 4, Buyback: 300},
		{Item: Named{Code: "crowbar", Name: "Crowbar"}, Price: 390, Stock: 2, Busy: true},
		{Item: Named{Code: "gloves", Name: "Pair of gloves"}, Price: 120, Stock: 0, NextRestock: snapshotNow.Add(20 * time.Minute)},
	}
	add("Shop · at the counter", ShopDetail(c, ShopView{Shop: snapHardware, Place: snapBazaar, Here: true, Shelves: shelves, TaxBPS: 500}))
	add("Shop · from across town", ShopDetail(c, ShopView{Shop: snapHardware, Place: snapBazaar, Walk: 15 * time.Second, Shelves: shelves, TaxBPS: 500}))
	add("Checkout · both ways cover it", ShopCheckout(c, ShopCheckoutView{
		Shop: snapHardware, Item: snapLockpick, Qty: 1, Unit: 600, Total: 630, Tax: 30, TaxBPS: 500, Stock: 4,
		Payment: PaymentChoice{Amount: 630, Accepted: []string{MethodCash, MethodCard}, Usable: []string{MethodCash, MethodCard}, Cash: 2000, Bank: 9000},
		Nonce:   "0a1b2c3d4e61",
	}))
	add("Checkout · neither covers it", ShopCheckout(c, ShopCheckoutView{
		Shop: snapHardware, Item: snapLockpick, Qty: 1, Unit: 600, Total: 630, Tax: 30, TaxBPS: 500, Stock: 4,
		Payment: PaymentChoice{Amount: 630, Accepted: []string{MethodCash, MethodCard}, Cash: 100, Bank: 200},
	}))
	add("Bought · with tax", ShopBought(c, ShopBoughtView{Shop: snapHardware, Item: snapLockpick, Qty: 1, Total: 630, Tax: 30, Method: MethodCard}))
	add("Sell · who buys a phone", SellOffers(c, SellOffersView{Item: snapPhone, Ref: "F0E1D2C3B4", Offers: []SellOffer{
		{Shop: snapPawn, Place: snapHomes, Price: 1100},
		{Shop: Named{Code: "electronics_store", Name: "Electronics store"}, Place: Named{Code: "business_district", Name: "Business district"}, Price: 1250},
	}}))
	add("Sell · nobody buys it", SellOffers(c, SellOffersView{Item: snapBread, Ref: "bread"}))
	add("Sold · a phone to the pawnshop", ShopSold(c, ShopSoldView{Shop: snapPawn, Item: snapPhone, Price: 1100}))
	for _, r := range []struct {
		title string
		view  ShopRefusalView
	}{
		{"no such shop", ShopRefusalView{Kind: ShopRefusedNoShop}},
		{"not sold here", ShopRefusalView{Kind: ShopRefusedNotSold, Shop: snapGrocery, Item: snapLockpick}},
		{"sold out", ShopRefusalView{Kind: ShopRefusedSoldOut, Shop: snapHardware, Item: snapLockpick, NextRestock: snapshotNow.Add(20 * time.Minute)}},
		{"not carried", ShopRefusalView{Kind: ShopRefusedNotHeld, Shop: snapPawn, Item: snapPhone}},
		{"not bought back", ShopRefusalView{Kind: ShopRefusedNoBuyback, Shop: snapGrocery, Item: snapPhone}},
	} {
		add("Shop refused · "+r.title, ShopRefusal(c, r.view))
	}
}

func tradeSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	add("Market", Market(c, MarketView{CityCode: "ostmarch", City: "Ostmarch", AtMarket: true, Books: []BookSummary{
		{Item: snapBread, BestBid: 35, BestAsk: 42, Last: 40},
		{Item: snapBandage, BestAsk: 90},
	}, Yours: []Named{snapLockpick}}))
	add("Market · empty, from across town", Market(c, MarketView{CityCode: "ostmarch", City: "Ostmarch"}))
	book := BookView{
		Item: snapBread, CityCode: "ostmarch", City: "Ostmarch",
		Asks:      []BookLevel{{Price: 42, Qty: 10}, {Price: 45, Qty: 4}},
		Bids:      []BookLevel{{Price: 35, Qty: 6}},
		Trades:    []TradeLine{{Qty: 2, Price: 40, At: snapshotNow.Add(-5 * time.Minute)}},
		Reference: 40, Holding: 3, AtMarket: true, Nonce: "0a1b2c3d4e62",
	}
	add("Book · bread, at the market, holding some", Book(c, book))
	far := book
	far.AtMarket, far.Holding, far.Asks, far.Bids, far.Trades = false, 0, nil, nil, nil
	add("Book · nothing on it, from across town", Book(c, far))
	add("Checkout · a buy order", MarketCheckout(c, MarketCheckoutView{
		Item: snapBread, Qty: 1, Price: 42, Reserve: 42, Nonce: "0a1b2c3d4e63",
		Payment: PaymentChoice{Amount: 42, Accepted: []string{MethodCash, MethodCard}, Usable: []string{MethodCash}, Cash: 500, Bank: 10},
	}))
	add("Placed · a buy, filled at once", OrderPlaced(c, OrderPlacedView{
		Item: snapBread, Side: SideBuy, No: 41, Qty: 1, Filled: 1, Price: 42, Spent: 42, Method: MethodCash,
	}))
	add("Placed · a sell, resting", OrderPlaced(c, OrderPlacedView{
		Item: snapBread, Side: SideSell, No: 42, Qty: 3, Filled: 1, Price: 44, Got: 43, Rests: true,
		ExpiresAt: snapshotNow.Add(168 * time.Minute),
	}))
	add("Cancelled · a buy", OrderCancelled(c, OrderCancelledView{Item: snapBread, Side: SideBuy, No: 41, Left: 2, Refund: 84}))
	add("Cancelled · a sell", OrderCancelled(c, OrderCancelledView{Item: snapBread, Side: SideSell, No: 42, Left: 2}))
	add("My orders", MyOrders(c, MyOrdersView{Orders: []OrderLine{
		{No: 42, Item: snapBread, Side: SideSell, Qty: 3, Filled: 1, Price: 44, Status: "open", CityCode: "ostmarch", City: "Ostmarch"},
		{No: 41, Item: snapBread, Side: SideBuy, Qty: 1, Filled: 1, Price: 42, Status: "filled", CityCode: "ostmarch", City: "Ostmarch"},
		{No: 17, Item: snapBandage, Side: SideBuy, Qty: 2, Price: 80, Status: "expired", CityCode: "brennhaven", City: "Brennhaven"},
	}}))
	add("My orders · none", MyOrders(c, MyOrdersView{}))
	add("Notice · your sell order filled", MarketFilledNotice(sent(c), MarketFilledView{
		Side: SideSell, Item: snapBread, Qty: 2, Price: 44, Amount: 88, Fee: 2, CityCode: "ostmarch", City: "Ostmarch",
	}))
	add("Notice · your buy order filled", MarketFilledNotice(sent(c), MarketFilledView{
		Side: SideBuy, Item: snapBread, Qty: 2, Price: 44, Amount: 88, CityCode: "ostmarch", City: "Ostmarch",
	}))
	add("Notice · a buy order expired", MarketExpiredNotice(sent(c), SideBuy, snapBread, 2, 41))
	add("Notice · a sell order expired", MarketExpiredNotice(sent(c), SideSell, snapBread, 2, 42))
	for _, r := range []struct {
		title string
		view  MarketRefusalView
	}{
		{"not traded", MarketRefusalView{Kind: MarketRefusedNotTraded, Item: snapLockpick}},
		{"too big", MarketRefusalView{Kind: MarketRefusedTooBig, Item: snapBread}},
		{"too many open", MarketRefusalView{Kind: MarketRefusedTooMany, Count: 20}},
		{"not enough", MarketRefusalView{Kind: MarketRefusedNotEnough, Item: snapBread}},
		{"no such order", MarketRefusalView{Kind: MarketRefusedNoOrder}},
		{"order closed", MarketRefusalView{Kind: MarketRefusedClosed}},
	} {
		add("Market refused · "+r.title, MarketRefusal(c, r.view))
	}

	lines := []AuctionLine{
		{No: 7, Item: snapPhone, Quality: 72, HighBid: 2600, Reserve: 1250, Remaining: 40 * time.Minute, Status: "open", Leading: true},
		{No: 8, Item: Named{Code: "watch", Name: "Wristwatch"}, Quality: 55, Reserve: 800, Remaining: 3 * time.Minute, Status: "open"},
	}
	add("Auction house", Auctions(c, AuctionsView{CityCode: "ostmarch", City: "Ostmarch", Auctions: lines, AtHouse: true}))
	add("Auction house · none, from across town", Auctions(c, AuctionsView{CityCode: "ostmarch", City: "Ostmarch"}))
	add("Auction · open to bid", AuctionDetail(c, AuctionView{
		Line: lines[1], MinNext: 800, Seller: who.friend, Nonce: "0a1b2c3d4e64",
		Payment: &PaymentChoice{Amount: 800, Accepted: []string{MethodCash, MethodCard}, Usable: []string{MethodCash, MethodCard}, Cash: 1000, Bank: 5000},
	}))
	add("Auction · your bid leads", AuctionDetail(c, AuctionView{Line: lines[0], MinNext: 2730, Bids: 3, Seller: who.friend}))
	mine := lines[0]
	mine.Mine, mine.Leading = true, false
	add("Auction · your own", AuctionDetail(c, AuctionView{Line: mine, MinNext: 2730, Bids: 3, Seller: who.me}))
	sold := lines[0]
	sold.Status, sold.Leading = "sold", false
	add("Auction · over", AuctionDetail(c, AuctionView{Line: sold, Bids: 3, Seller: who.friend}))
	add("Auction · put a phone up", AuctionNew(c, AuctionNewView{
		Item: snapPhone, Ref: "F0E1D2C3B4", Quality: 72, Reserves: []int64{1250, 2500, 3750},
		Durations: []time.Duration{time.Minute, 6 * time.Minute, 24 * time.Minute}, Nonce: "0a1b2c3d4e65",
	}))
	add("Auction · opened", AuctionOpened(c, AuctionOpenedView{
		No: 9, Item: snapPhone, Reserve: 2500, Duration: 6 * time.Minute, EndsAt: snapshotNow.Add(6 * time.Minute),
	}))
	add("Bid · placed", BidPlaced(c, BidPlacedView{No: 8, Item: Named{Code: "watch", Name: "Wristwatch"}, Amount: 800, Method: MethodCard,
		EndsAt: snapshotNow.Add(3 * time.Minute)}))
	add("My auctions", MyAuctions(c, MyAuctionsView{Auctions: []AuctionLine{mine, lines[0], {No: 3, Item: snapPhone, Quality: 40,
		HighBid: 900, Reserve: 700, Status: "sold"}}}))
	add("My auctions · none", MyAuctions(c, MyAuctionsView{}))
	for _, kind := range []string{"outbid", "won", "sold", "unsold"} {
		add("Notice · auction "+kind, AuctionNotice(sent(c), AuctionNoticeView{Kind: kind, No: 7, Item: snapPhone, Amount: 2600, Fee: 130}))
	}
	for _, r := range []struct {
		title string
		view  AuctionRefusalView
	}{
		{"no such auction", AuctionRefusalView{Kind: AuctionRefusedNone}},
		{"over", AuctionRefusalView{Kind: AuctionRefusedClosed, No: 7}},
		{"bid too low", AuctionRefusalView{Kind: AuctionRefusedTooLow, No: 7, MinNext: 2730}},
		{"your own", AuctionRefusalView{Kind: AuctionRefusedOwn, No: 7}},
		{"already leading", AuctionRefusalView{Kind: AuctionRefusedLeading, No: 7}},
		{"not carried", AuctionRefusalView{Kind: AuctionRefusedNotHeld}},
		{"too many open", AuctionRefusalView{Kind: AuctionRefusedTooMany, Count: 5}},
		{"not a piece", AuctionRefusalView{Kind: AuctionRefusedNotSellable}},
	} {
		add("Auction refused · "+r.title, AuctionRefusal(c, r.view))
	}
}
