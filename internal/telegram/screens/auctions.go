package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The auction house: unique pieces sold to the highest bid by a fixed time on
// the game clock. The list and an auction are public; a bid's checkout shows
// balances only in the player's own chat. An auction is named by its number,
// never an id.

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
}

func (c Context) auctionLine(a AuctionLine) string {
	args := map[string]any{
		"no": FormatNumber(c, a.No), "item": c.ItemName(a.Item), "quality": FormatNumber(c, int64(a.Quality)),
		"bid": FormatMoney(c, a.HighBid), "reserve": FormatMoney(c, a.Reserve), "remaining": FormatDuration(c, a.Remaining),
	}
	if a.HighBid > 0 {
		return c.T("auction.line_bid", args)
	}
	return c.T("auction.line_reserve", args)
}

// Auctions renders a city's open auctions.
func Auctions(c Context, v AuctionsView) *presenter.Response {
	return c.withView(renderAuctions(c, v), ScreenAuctions, v)
}

func renderAuctions(c Context, v AuctionsView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{}
	if len(v.Auctions) == 0 {
		lines = append(lines, c.T("auction.none", nil))
	}
	var buttons []presenter.Button
	for _, a := range v.Auctions {
		lines = append(lines, c.auctionLine(a))
		if btn, ok := keyboards.Button(c.T("auction.button.open", map[string]any{"no": FormatNumber(c, a.No)}),
			AddrAuction, strconv.FormatInt(a.No, 10)); ok {
			buttons = append(buttons, btn)
		}
	}
	kb.Grid(3, buttons...)
	mine, _ := keyboards.Button(c.T("auction.button.mine", nil), AddrAuctionMine)
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(mine, bag)
	var where string
	if !v.AtHouse {
		where = c.T("auction.away", nil)
		c.wayButton(kb, v.Way, "auction.list")
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMarket, RefreshData: AddrAuctions}))
	return c.respond(paragraphs(c.T("auction.title", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		body(lines...), where), kb.Build())
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

// AuctionDetail renders one auction.
func AuctionDetail(c Context, v AuctionView) *presenter.Response {
	return c.withView(renderAuctionDetail(c, v), ScreenAuctionDetail, v)
}

func renderAuctionDetail(c Context, v AuctionView) *presenter.Response {
	a := v.Line
	lines := []string{
		c.T("auction.item", map[string]any{"item": c.ItemName(a.Item), "quality": FormatNumber(c, int64(a.Quality))}),
		c.T("auction.seller", map[string]any{"player": v.Seller}),
		c.T("auction.reserve", map[string]any{"reserve": FormatMoney(c, a.Reserve)}),
	}
	if a.HighBid > 0 {
		lines = append(lines, c.T("auction.high_bid", map[string]any{"bid": FormatMoney(c, a.HighBid), "bids": FormatNumber(c, int64(v.Bids))}))
	} else {
		lines = append(lines, c.T("auction.no_bids", nil))
	}
	if a.Status == "open" {
		lines = append(lines, c.T("auction.ends_in", map[string]any{"remaining": FormatDuration(c, a.Remaining)}),
			clockLine(c, "auction.ends_at", a.EndsAt))
	} else {
		lines = append(lines, c.T("auction.status."+a.Status, nil))
	}
	kb := keyboards.New()
	var bid string
	switch {
	case a.Mine && a.Status == "open":
		bid = c.T("auction.yours", nil)
	case a.Leading && a.Status == "open":
		bid = c.T("auction.leading", nil)
	case v.Payment != nil && len(v.Payment.Usable) > 0:
		bid = body(c.T("auction.bid_how", map[string]any{"amount": FormatMoney(c, v.MinNext)}), c.paymentNote(*v.Payment))
		c.paymentButtons(kb, *v.Payment, func(m string) []string {
			return []string{AddrAuctionBid, strconv.FormatInt(a.No, 10), strconv.FormatInt(v.MinNext, 10), v.Nonce, m}
		})
	case v.Payment != nil:
		bid = body(c.T("payment.cannot_afford", nil), c.paymentNote(*v.Payment))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrAuctions, RefreshData: keyboards.Data(AddrAuction, strconv.FormatInt(a.No, 10))}))
	return c.respond(paragraphs(c.T("auction.detail_title", map[string]any{"no": FormatNumber(c, a.No)}), body(lines...), bid), kb.Build())
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

// AuctionNew renders the choice of a reserve and a length for a piece.
func AuctionNew(c Context, v AuctionNewView) *presenter.Response {
	return c.withView(renderAuctionNew(c, v), ScreenAuctionNew, v)
}

func renderAuctionNew(c Context, v AuctionNewView) *presenter.Response {
	kb := keyboards.New()
	for i, d := range v.Durations {
		var row []presenter.Button
		for _, r := range v.Reserves {
			if btn, ok := keyboards.Button(c.T("auction.button.terms", map[string]any{
				"reserve": FormatMoney(c, r), "duration": FormatDuration(c, d)}),
				AddrAuctionNew, v.Ref, strconv.FormatInt(r, 10), strconv.Itoa(i), v.Nonce); ok {
				row = append(row, btn)
			}
		}
		kb.Row(row...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrItem, v.Ref)}))
	return c.respond(paragraphs(
		c.T("auction.new_title", map[string]any{"item": c.ItemName(v.Item), "quality": FormatNumber(c, int64(v.Quality))}),
		c.T("auction.new_hint", nil),
	), kb.Build())
}

// AuctionOpenedView is an auction opened.
type AuctionOpenedView struct {
	No       int64
	Item     Named
	Reserve  int64
	Duration time.Duration
	EndsAt   time.Time
}

// AuctionOpened renders an auction opened.
func AuctionOpened(c Context, v AuctionOpenedView) *presenter.Response {
	return c.withView(renderAuctionOpened(c, v), ScreenAuctionOpened, v)
}

func renderAuctionOpened(c Context, v AuctionOpenedView) *presenter.Response {
	kb := keyboards.New()
	open, _ := keyboards.Button(c.T("auction.button.open", map[string]any{"no": FormatNumber(c, v.No)}), AddrAuction, strconv.FormatInt(v.No, 10))
	kb.Row(open)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrAuctions}))
	return c.respond(body(
		c.T("auction.opened", map[string]any{"no": FormatNumber(c, v.No), "item": c.ItemName(v.Item),
			"reserve": FormatMoney(c, v.Reserve), "duration": FormatDuration(c, v.Duration)}),
		clockLine(c, "auction.ends_at", v.EndsAt),
	), kb.Build())
}

// BidPlacedView is a bid made.
type BidPlacedView struct {
	No     int64
	Item   Named
	Amount int64
	Method string
	EndsAt time.Time
}

// BidPlaced renders a bid.
func BidPlaced(c Context, v BidPlacedView) *presenter.Response {
	return c.withView(renderBidPlaced(c, v), ScreenBidPlaced, v)
}

func renderBidPlaced(c Context, v BidPlacedView) *presenter.Response {
	kb := keyboards.New()
	open, _ := keyboards.Button(c.T("auction.button.open", map[string]any{"no": FormatNumber(c, v.No)}), AddrAuction, strconv.FormatInt(v.No, 10))
	kb.Row(open)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrAuctions}))
	return c.respond(body(
		c.T("auction.bid_placed", map[string]any{"no": FormatNumber(c, v.No), "item": c.ItemName(v.Item), "amount": FormatMoney(c, v.Amount)}),
		c.paidLine(v.Method),
		c.T("auction.bid_escrow", nil),
		clockLine(c, "auction.ends_at", v.EndsAt),
	), kb.Build())
}

// MyAuctionsView is what the player sells and bids on.
type MyAuctionsView struct {
	Auctions []AuctionLine
}

// MyAuctions renders the player's auctions and bids. Private.
func MyAuctions(c Context, v MyAuctionsView) *presenter.Response {
	return c.withView(renderMyAuctions(c, v), ScreenMyAuctions, v)
}

func renderMyAuctions(c Context, v MyAuctionsView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{c.T("auction.mine_title", nil)}
	if len(v.Auctions) == 0 {
		lines = append(lines, c.T("auction.mine_none", nil))
	}
	var buttons []presenter.Button
	for _, a := range v.Auctions {
		role := "auction.role_bidder"
		switch {
		case a.Mine:
			role = "auction.role_seller"
		case a.Leading:
			role = "auction.role_leading"
		}
		lines = append(lines, c.T("auction.mine_line", map[string]any{
			"line": c.auctionLine(a), "role": c.T(role, nil), "status": c.T("auction.status."+a.Status, nil),
		}))
		if btn, ok := keyboards.Button(c.T("auction.button.open", map[string]any{"no": FormatNumber(c, a.No)}),
			AddrAuction, strconv.FormatInt(a.No, 10)); ok {
			buttons = append(buttons, btn)
		}
	}
	kb.Grid(3, buttons...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrAuctions, RefreshData: AddrAuctionMine}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// AuctionNoticeView is what an auction's end or an outbid tells a player.
type AuctionNoticeView struct {
	// Kind is outbid, won, sold or unsold.
	Kind   string
	No     int64
	Item   Named
	Amount int64
	Fee    int64
}

// AuctionNotice tells a player about an auction they sell or bid on.
func AuctionNotice(c Context, v AuctionNoticeView) *presenter.Response {
	return c.withView(renderAuctionNotice(c, v), ScreenAuctionNotice, v)
}

func renderAuctionNotice(c Context, v AuctionNoticeView) *presenter.Response {
	kb := keyboards.New()
	if v.Kind == "outbid" {
		if btn, ok := keyboards.Button(c.T("auction.button.open", map[string]any{"no": FormatNumber(c, v.No)}),
			AddrAuction, strconv.FormatInt(v.No, 10)); ok {
			kb.Row(btn)
		}
	} else {
		bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
		kb.Row(bag)
	}
	return c.respond(c.T("auction.notice_"+v.Kind, map[string]any{
		"no": FormatNumber(c, v.No), "item": c.ItemName(v.Item), "amount": FormatMoney(c, v.Amount), "fee": FormatMoney(c, v.Fee),
	}), kb.Build()).MarkPrivate()
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

// AuctionRefusal renders a refused auction request.
func AuctionRefusal(c Context, v AuctionRefusalView) *presenter.Response {
	return c.withView(renderAuctionRefusal(c, v), ScreenAuctionRefusal, v)
}

func renderAuctionRefusal(c Context, v AuctionRefusalView) *presenter.Response {
	kb := keyboards.New()
	if v.No > 0 {
		if btn, ok := keyboards.Button(c.T("auction.button.open", map[string]any{"no": FormatNumber(c, v.No)}),
			AddrAuction, strconv.FormatInt(v.No, 10)); ok {
			kb.Row(btn)
		}
	}
	house, _ := keyboards.Button(c.T("auction.button.house", nil), AddrAuctions)
	kb.Row(house)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("auction.refused."+v.Kind, map[string]any{
		"no": FormatNumber(c, v.No), "min": FormatMoney(c, v.MinNext), "count": FormatNumber(c, int64(v.Count)),
	}), kb.Build())
}
