package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The player market: a city's books, one good's book with what the player
// can do on it, the checkout of a buy, an order placed or cancelled, and a
// player's own orders. The book is public; balances show only in the
// player's own chat (PaymentChoice).

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
	Item                   Named
	BestBid, BestAsk, Last int64
}

// MarketView is a city's books.
type MarketView struct {
	CityCode, City string
	Books          []BookSummary
	// Yours are goods the player carries with no book yet here.
	Yours    []Named
	AtMarket bool
}

// priceOrDash is a price, or the catalogue's "none yet".
func (c Context) priceOrNone(v int64) string {
	if v <= 0 {
		return c.T("market.none_yet", nil)
	}
	return FormatMoney(c, v)
}

// Market renders a city's books.
func Market(c Context, v MarketView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{}
	if len(v.Books) == 0 {
		lines = append(lines, c.T("market.empty", nil))
	}
	var buttons []presenter.Button
	for _, b := range v.Books {
		lines = append(lines, c.T("market.book_line", map[string]any{
			"item": c.ItemName(b.Item), "bid": c.priceOrNone(b.BestBid), "ask": c.priceOrNone(b.BestAsk),
		}))
		if btn, ok := keyboards.Button(c.T("market.button.book", map[string]any{"item": c.ItemName(b.Item)}), AddrMarketBook, b.Item.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	for _, it := range v.Yours {
		if btn, ok := keyboards.Button(c.T("market.button.book", map[string]any{"item": c.ItemName(it)}), AddrMarketBook, it.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	kb.Grid(2, buttons...)
	mine, _ := keyboards.Button(c.T("market.button.mine", nil), AddrMarketMine)
	auctions, _ := keyboards.Button(c.T("auction.button.house", nil), AddrAuctions)
	kb.Row(mine, auctions)
	var where string
	if !v.AtMarket {
		where = c.T("market.away", nil)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap, RefreshData: AddrMarket}))
	return c.respond(paragraphs(c.T("market.title", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		body(lines...), where), kb.Build())
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
	// Nonce binds the sell buttons: one press is one order.
	Nonce string
}

// Book renders one good's book, and — at the market place — a buy button at
// the best ask and at the reference price, and for a holder a sell button at
// the best bid and at the reference and ten percent above.
func Book(c Context, v BookView) *presenter.Response {
	name := c.ItemName(v.Item)
	var asks, bids, trades []string
	asks = append(asks, c.T("market.asks", nil))
	for _, l := range v.Asks {
		asks = append(asks, c.T("market.level", map[string]any{"price": FormatMoney(c, l.Price), "qty": FormatNumber(c, l.Qty)}))
	}
	if len(v.Asks) == 0 {
		asks = append(asks, c.T("market.level_none", nil))
	}
	bids = append(bids, c.T("market.bids", nil))
	for _, l := range v.Bids {
		bids = append(bids, c.T("market.level", map[string]any{"price": FormatMoney(c, l.Price), "qty": FormatNumber(c, l.Qty)}))
	}
	if len(v.Bids) == 0 {
		bids = append(bids, c.T("market.level_none", nil))
	}
	if len(v.Trades) > 0 {
		trades = append(trades, c.T("market.trades", nil))
		for _, t := range v.Trades {
			trades = append(trades, c.T("market.trade", map[string]any{
				"qty": FormatNumber(c, t.Qty), "price": FormatMoney(c, t.Price), "time": FormatClock(c, t.At),
			}))
		}
	}
	kb := keyboards.New()
	item := v.Item.Code
	order := func(side string, qty, price int64, extra ...string) []string {
		parts := []string{AddrMarketOrder, side, item, strconv.FormatInt(qty, 10), strconv.FormatInt(price, 10)}
		return append(parts, extra...)
	}
	if v.AtMarket {
		var buys []presenter.Button
		if len(v.Asks) > 0 {
			if btn, ok := keyboards.Button(c.T("market.button.buy_at", map[string]any{"price": FormatMoney(c, v.Asks[0].Price)}),
				order(SideBuy, 1, v.Asks[0].Price)...); ok {
				buys = append(buys, btn)
			}
		}
		if btn, ok := keyboards.Button(c.T("market.button.bid_at", map[string]any{"price": FormatMoney(c, v.Reference)}),
			order(SideBuy, 1, v.Reference)...); ok {
			buys = append(buys, btn)
		}
		kb.Row(buys...)
		if v.Holding > 0 {
			var sells []presenter.Button
			if len(v.Bids) > 0 {
				if btn, ok := keyboards.Button(c.T("market.button.sell_at", map[string]any{"price": FormatMoney(c, v.Bids[0].Price)}),
					order(SideSell, 1, v.Bids[0].Price, v.Nonce)...); ok {
					sells = append(sells, btn)
				}
			}
			for _, p := range []int64{v.Reference, v.Reference + v.Reference/10} {
				if btn, ok := keyboards.Button(c.T("market.button.ask_at", map[string]any{"price": FormatMoney(c, p)}),
					order(SideSell, 1, p, v.Nonce)...); ok {
					sells = append(sells, btn)
				}
			}
			kb.Grid(3, sells...)
			if v.Holding > 1 {
				if btn, ok := keyboards.Button(c.T("market.button.ask_all", map[string]any{
					"qty": FormatNumber(c, v.Holding), "price": FormatMoney(c, v.Reference)}),
					order(SideSell, v.Holding, v.Reference, v.Nonce)...); ok {
					kb.Row(btn)
				}
			}
		}
	}
	var footer []string
	if v.Holding > 0 {
		footer = append(footer, c.T("market.you_hold", map[string]any{"qty": FormatNumber(c, v.Holding)}))
	}
	if !v.AtMarket {
		footer = append(footer, c.T("market.away", nil))
	}
	footer = append(footer, c.T("market.fee_note", nil))
	mine, _ := keyboards.Button(c.T("market.button.mine", nil), AddrMarketMine)
	kb.Row(mine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMarket, RefreshData: keyboards.Data(AddrMarketBook, item)}))
	return c.respond(paragraphs(
		c.T("market.book_title", map[string]any{"item": name, "city": c.CityName(v.CityCode, v.City)}),
		body(asks...), body(bids...), body(trades...), body(footer...),
	), kb.Build())
}

// MarketCheckoutView is a buy order's escrow and the ways to pay it.
type MarketCheckoutView struct {
	Item       Named
	Qty, Price int64
	Reserve    int64
	Payment    PaymentChoice
	Nonce      string
}

// MarketCheckout renders a buy order's checkout: what is set aside, and a
// button per way to pay it.
func MarketCheckout(c Context, v MarketCheckoutView) *presenter.Response {
	kb := keyboards.New()
	var pay string
	if len(v.Payment.Usable) > 0 {
		pay = body(c.T("market.escrow_how", nil), c.paymentNote(v.Payment))
		c.paymentButtons(kb, v.Payment, func(m string) []string {
			return []string{AddrMarketOrder, SideBuy, v.Item.Code, strconv.FormatInt(v.Qty, 10), strconv.FormatInt(v.Price, 10), v.Nonce, m}
		})
	} else {
		pay = body(c.T("payment.cannot_afford", nil), c.paymentNote(v.Payment))
		if btn, ok := keyboards.Button(c.T("button.bank", nil), AddrBank); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMarketBook, v.Item.Code)}))
	return c.respond(paragraphs(
		c.T("market.checkout_title", map[string]any{"item": c.ItemName(v.Item)}),
		body(
			c.T("market.checkout_order", map[string]any{"qty": FormatNumber(c, v.Qty), "price": FormatMoney(c, v.Price)}),
			c.T("market.checkout_reserve", map[string]any{"reserve": FormatMoney(c, v.Reserve)}),
		),
		pay,
	), kb.Build())
}

// OrderPlacedView is an order placed: what traded at once, what rests.
type OrderPlacedView struct {
	Item        Named
	Side        string
	No          int64
	Qty, Filled int64
	Price       int64
	Rests       bool
	Spent, Got  int64
	ExpiresAt   time.Time
	Method      string
}

// OrderPlaced renders an order placed.
func OrderPlaced(c Context, v OrderPlacedView) *presenter.Response {
	args := map[string]any{
		"item": c.ItemName(v.Item), "qty": FormatNumber(c, v.Qty), "price": FormatMoney(c, v.Price),
		"filled": FormatNumber(c, v.Filled), "no": FormatNumber(c, v.No),
	}
	lines := []string{c.T("market.placed_"+v.Side, args)}
	if v.Filled > 0 {
		if v.Side == SideBuy {
			lines = append(lines, c.T("market.filled_buy", map[string]any{"filled": FormatNumber(c, v.Filled), "spent": FormatMoney(c, v.Spent)}))
		} else {
			lines = append(lines, c.T("market.filled_sell", map[string]any{"filled": FormatNumber(c, v.Filled), "got": FormatMoney(c, v.Got)}))
		}
	}
	if v.Rests {
		lines = append(lines, c.T("market.rests", map[string]any{"left": FormatNumber(c, v.Qty-v.Filled)}))
		lines = append(lines, clockLine(c, "market.expires_at", v.ExpiresAt))
	}
	if v.Side == SideBuy {
		lines = append(lines, c.paidLine(v.Method))
	}
	kb := keyboards.New()
	mine, _ := keyboards.Button(c.T("market.button.mine", nil), AddrMarketMine)
	book, _ := keyboards.Button(c.T("market.button.back_to_book", nil), AddrMarketBook, v.Item.Code)
	kb.Row(mine, book)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMarket}))
	return c.respond(body(lines...), kb.Build())
}

// OrderCancelledView is an order taken off the book.
type OrderCancelledView struct {
	Item   Named
	Side   string
	No     int64
	Left   int64
	Refund int64
}

// OrderCancelled renders a cancel.
func OrderCancelled(c Context, v OrderCancelledView) *presenter.Response {
	key := "market.cancelled_sell"
	if v.Side == SideBuy {
		key = "market.cancelled_buy"
	}
	kb := keyboards.New()
	mine, _ := keyboards.Button(c.T("market.button.mine", nil), AddrMarketMine)
	kb.Row(mine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMarket}))
	return c.respond(c.T(key, map[string]any{
		"no": FormatNumber(c, v.No), "item": c.ItemName(v.Item), "left": FormatNumber(c, v.Left), "refund": FormatMoney(c, v.Refund),
	}), kb.Build())
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

// MyOrders renders the player's orders, with a cancel button per open one.
// It lists what the player trades, so it is private.
func MyOrders(c Context, v MyOrdersView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{c.T("market.mine_title", nil)}
	if len(v.Orders) == 0 {
		lines = append(lines, c.T("market.mine_none", nil))
	}
	for _, o := range v.Orders {
		lines = append(lines, c.T("market.order_"+o.Side, map[string]any{
			"no": FormatNumber(c, o.No), "item": c.ItemName(o.Item), "qty": FormatNumber(c, o.Qty),
			"filled": FormatNumber(c, o.Filled), "price": FormatMoney(c, o.Price),
			"status": c.T("market.status."+o.Status, nil), "city": c.CityName(o.CityCode, o.City),
		}))
		if o.Status == "open" {
			if btn, ok := keyboards.Button(c.T("market.button.cancel", map[string]any{"no": FormatNumber(c, o.No)}),
				AddrMarketCancel, strconv.FormatInt(o.No, 10)); ok {
				kb.Row(btn)
			}
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMarket, RefreshData: AddrMarketMine}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// MarketFilledView is a resting order that filled while its owner was away.
type MarketFilledView struct {
	Side           string
	Item           Named
	Qty, Price     int64
	Amount, Fee    int64
	CityCode, City string
}

// MarketFilledNotice tells an order's owner it traded.
func MarketFilledNotice(c Context, v MarketFilledView) *presenter.Response {
	key := "market.notice_sold"
	if v.Side == SideBuy {
		key = "market.notice_bought"
	}
	kb := keyboards.New()
	mine, _ := keyboards.Button(c.T("market.button.mine", nil), AddrMarketMine)
	kb.Row(mine)
	return c.respond(c.T(key, map[string]any{
		"item": c.ItemName(v.Item), "qty": FormatNumber(c, v.Qty), "price": FormatMoney(c, v.Price),
		"amount": FormatMoney(c, v.Amount), "fee": FormatMoney(c, v.Fee), "city": c.CityName(v.CityCode, v.City),
	}), kb.Build()).MarkPrivate()
}

// MarketExpiredNotice tells an order's owner it expired and what came back.
func MarketExpiredNotice(c Context, side string, item Named, left, no int64) *presenter.Response {
	kb := keyboards.New()
	mine, _ := keyboards.Button(c.T("market.button.mine", nil), AddrMarketMine)
	kb.Row(mine)
	return c.respond(c.T("market.notice_expired_"+side, map[string]any{
		"item": c.ItemName(item), "left": FormatNumber(c, left), "no": FormatNumber(c, no),
	}), kb.Build()).MarkPrivate()
}

// Market refusal kinds.
const (
	MarketRefusedNotTraded = "not_traded"
	MarketRefusedTooBig    = "too_big"
	MarketRefusedTooMany   = "too_many"
	MarketRefusedNotEnough = "not_enough"
	MarketRefusedNoOrder   = "no_order"
	MarketRefusedClosed    = "closed"
)

// MarketRefusalView is a refused market request.
type MarketRefusalView struct {
	Kind  string
	Item  Named
	Count int
}

// MarketRefusal renders a refused market request.
func MarketRefusal(c Context, v MarketRefusalView) *presenter.Response {
	kb := keyboards.New()
	market, _ := keyboards.Button(c.T("market.button.market", nil), AddrMarket)
	kb.Row(market)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("market.refused."+v.Kind, map[string]any{
		"item": c.ItemName(v.Item), "count": FormatNumber(c, int64(v.Count)),
	}), kb.Build())
}
