package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The stock exchange and the portfolio (docs/adr/0026-finance.md section 5):
// the listed companies with their prices, one company's book, its trades and
// the player's holding, an order, a listing, a dividend and the portfolio of
// shares, gold and savings.

// companyName is a company's name on the exchange.
func (c Context) companyName(n Named) string { return c.named("company_name."+n.Code, n.Name) }

// change is a price's move from prev, in basis points.
func change(price, prev int64) int64 {
	if prev <= 0 {
		return 0
	}
	return (price - prev) * 10000 / prev
}

// moveText is a move in percent with its arrow.
func (c Context) moveText(price, prev int64) string {
	d := change(price, prev)
	switch {
	case d > 0:
		return c.T("stock.move_up", map[string]any{"pct": PercentFromBPS(c, int(d))})
	case d < 0:
		return c.T("stock.move_down", map[string]any{"pct": PercentFromBPS(c, int(-d))})
	}
	return c.T("stock.move_flat", nil)
}

// Exchange renders the listed companies. It shows no one's money: market
// data is public.
func Exchange(c Context, v ExchangeView) *presenter.Response {
	return c.withView(renderExchange(c, v), ScreenExchange, v)
}

func renderExchange(c Context, v ExchangeView) *presenter.Response {
	if v.Unavailable != nil {
		return renderUnavailable(c, v.Unavailable, AddrHome)
	}
	lines := []string{c.T("stock.exchange_title", nil)}
	kb := keyboards.New()
	var buttons []presenter.Button
	for _, l := range v.Lines {
		lines = append(lines, c.T("stock.exchange_line", map[string]any{"company": c.companyName(l.Company),
			"type": c.CompanyTypeName(l.Type), "city": c.PlaceName(l.City), "price": FormatMoney(c, l.Price),
			"move": c.moveText(l.Price, l.Prev), "volume": FormatNumber(c, l.Volume), "cap": FormatMoney(c, l.Cap)}))
		if btn, ok := keyboards.Button(c.T("stock.button.company", map[string]any{"company": c.companyName(l.Company)}),
			AddrStock, l.Company.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	if len(v.Lines) == 0 {
		lines = append(lines, c.T("stock.exchange_empty", nil))
	}
	lines = append(lines, c.T("stock.exchange_hint", nil))
	kb.Grid(2, buttons...)
	kb.Add(c.T("finance.button.portfolio", nil), AddrPortfolio)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrExchange}))
	return c.respond(body(lines...), kb.Build())
}

// A company's book and trades use the market's BookLevel and TradeLine
// (market.go): one engine, one shape.

// Stock renders a company on the exchange: its price and book, its last
// trades, and — in the player's own chat — their holding and the orders
// they may place.
func Stock(c Context, v StockView) *presenter.Response {
	return c.withView(renderStock(c, v), ScreenStock, v)
}

func renderStock(c Context, v StockView) *presenter.Response {
	if v.Unavailable != nil {
		return renderUnavailable(c, v.Unavailable, AddrHome)
	}
	var notice string
	if v.Notice != "" {
		notice = c.T("stock.notice."+v.Notice, moneyArgs(c, v.NoticeArgs))
	}
	head := []string{c.T("stock.title", map[string]any{"company": c.companyName(v.Company),
		"type": c.CompanyTypeName(v.Type), "city": c.PlaceName(v.City)})}
	if !v.Listed {
		head = append(head, c.T("stock.not_listed", nil))
	} else {
		head = append(head, c.T("stock.price", map[string]any{"price": FormatMoney(c, v.Price),
			"move": c.moveText(v.Price, v.Prev), "ipo": FormatMoney(c, v.IPO)}))
	}
	head = append(head, c.T("stock.book", map[string]any{"book": FormatMoney(c, v.Book),
		"total": FormatNumber(c, v.Total), "cap": FormatMoney(c, v.Price*v.Total)}))
	if v.Controller != "" {
		head = append(head, c.T("stock.controller", map[string]any{"player": c.playerName(v.Controller)}))
	}
	if v.LastDiv > 0 {
		head = append(head, c.T("stock.last_dividend", map[string]any{"amount": FormatMoney(c, v.LastDiv)}))
	}
	var book []string
	if v.Listed {
		book = append(book, c.T("stock.book_title", nil))
		for _, a := range v.Asks {
			book = append(book, c.T("stock.ask_line", map[string]any{"price": FormatMoney(c, a.Price),
				"qty": FormatNumber(c, a.Qty)}))
		}
		for _, b := range v.Bids {
			book = append(book, c.T("stock.bid_line", map[string]any{"price": FormatMoney(c, b.Price),
				"qty": FormatNumber(c, b.Qty)}))
		}
		if len(v.Asks)+len(v.Bids) == 0 {
			book = append(book, c.T("stock.book_empty", nil))
		}
	}
	var trades []string
	if len(v.Trades) > 0 {
		trades = append(trades, c.T("stock.trades_title", nil))
		for _, t := range v.Trades {
			trades = append(trades, c.T("stock.trade_line", map[string]any{"qty": FormatNumber(c, t.Qty),
				"price": FormatMoney(c, t.Price), "time": FormatClock(c, t.At)}))
		}
	}
	var mine string
	if !c.Shared && v.Holding > 0 {
		mine = c.T("stock.holding", map[string]any{"shares": FormatNumber(c, v.Holding),
			"locked": FormatNumber(c, v.Locked), "value": FormatMoney(c, v.Holding*max(v.Price, v.Book)),
			"cost": FormatMoney(c, v.Cost)})
	}
	text := paragraphs(notice, body(head...), body(book...), body(trades...), mine)
	kb := keyboards.New()
	if v.Listed && !c.Shared {
		for _, list := range []struct {
			addr, key string
			opts      []PriceOption
		}{{AddrStockBuy, "stock.button.buy", v.Buys}, {AddrStockSell, "stock.button.sell", v.Sells}} {
			var row []presenter.Button
			for _, o := range list.opts {
				label := c.T(list.key, map[string]any{"qty": FormatNumber(c, o.Qty), "price": FormatMoney(c, o.Price)})
				if btn, ok := keyboards.Button(label, list.addr, v.Company.Code, strconv.FormatInt(o.Qty, 10),
					strconv.FormatInt(o.Price, 10)); ok {
					row = append(row, btn)
				}
			}
			kb.Grid(2, row...)
		}
	}
	if v.Owner && !c.Shared {
		if !v.Listed {
			kb.Add(c.T("stock.button.ipo", nil), AddrStockIPO, v.Company.Code)
		}
		kb.Add(c.T("stock.button.dividend", nil), AddrStockDividend, v.Company.Code)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrExchange, RefreshData: keyboards.Data(AddrStock, v.Company.Code)}))
	return c.respond(text, kb.Build())
}

// StockOrder renders an order: its confirmation, or what became of it.
func StockOrder(c Context, v StockOrderView) *presenter.Response {
	return c.withView(renderStockOrder(c, v), ScreenStockOrder, v)
}

func renderStockOrder(c Context, v StockOrderView) *presenter.Response {
	args := map[string]any{"company": c.companyName(v.Company), "qty": FormatNumber(c, v.Qty),
		"price": FormatMoney(c, v.Price), "reserve": FormatMoney(c, v.Reserve), "bank": FormatMoney(c, v.Bank),
		"fee": PercentFromBPS(c, int(v.FeeBPS)), "no": FormatNumber(c, v.No), "filled": FormatNumber(c, v.Filled),
		"spent": FormatMoney(c, v.Spent), "got": FormatMoney(c, v.Got), "time": FormatClock(c, v.ExpiresAt)}
	kb := keyboards.New()
	var lines []string
	if !v.Placed {
		lines = append(lines, c.T("stock.order.confirm_"+v.Side, args), c.T("stock.order.fee", args))
		addr := AddrStockBuy
		if v.Side == "sell" {
			addr = AddrStockSell
		}
		kb.Add(c.T("stock.button.confirm_"+v.Side, args), addr, v.Company.Code, strconv.FormatInt(v.Qty, 10),
			strconv.FormatInt(v.Price, 10), v.Nonce)
	} else {
		lines = append(lines, c.T("stock.order.placed_"+v.Side, args))
		if v.Filled > 0 {
			lines = append(lines, c.T("stock.order.filled_"+v.Side, args))
		}
		if v.Rests {
			lines = append(lines, c.T("stock.order.rests", args))
			kb.Add(c.T("stock.button.cancel", map[string]any{"no": FormatNumber(c, v.No)}), AddrStockCancel,
				strconv.FormatInt(v.No, 10))
		}
	}
	kb.Add(c.T("stock.button.company", args), AddrStock, v.Company.Code)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrPortfolio}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// Portfolio renders a player's shares, gold, savings and open orders.
func Portfolio(c Context, v PortfolioView) *presenter.Response {
	return c.withView(renderPortfolio(c, v), ScreenPortfolio, v)
}

func renderPortfolio(c Context, v PortfolioView) *presenter.Response {
	if v.Unavailable != nil {
		return renderUnavailable(c, v.Unavailable, AddrHome)
	}
	var notice string
	if v.Notice != "" {
		notice = c.T("stock.notice."+v.Notice, moneyArgs(c, v.NoticeArgs))
	}
	lines := []string{c.T("stock.portfolio.title", nil)}
	kb := keyboards.New()
	for _, h := range v.Holdings {
		key := "stock.portfolio.holding"
		if !h.Listed {
			key = "stock.portfolio.holding_private"
		}
		lines = append(lines, c.T(key, map[string]any{"company": c.companyName(h.Company),
			"shares": FormatNumber(c, h.Shares), "price": FormatMoney(c, h.Price), "value": FormatMoney(c, h.Value),
			"cost": FormatMoney(c, h.Cost)}))
		kb.Add(c.T("stock.button.company", map[string]any{"company": c.companyName(h.Company)}), AddrStock, h.Company.Code)
	}
	if len(v.Holdings) == 0 {
		lines = append(lines, c.T("stock.portfolio.no_shares", nil))
	}
	lines = append(lines,
		c.T("stock.portfolio.gold", map[string]any{"grams": FormatNumber(c, v.Gold), "value": FormatMoney(c, v.GoldVal)}),
		c.T("stock.portfolio.savings", map[string]any{"amount": FormatMoney(c, v.Savings)}),
		c.T("stock.portfolio.total", map[string]any{"value": FormatMoney(c, v.Value)}))
	gainKey := "stock.portfolio.gain"
	if v.Gain < 0 {
		gainKey = "stock.portfolio.loss"
	}
	lines = append(lines, c.T(gainKey, map[string]any{"amount": FormatMoney(c, max(v.Gain, -v.Gain))}))
	var orders []string
	if len(v.Orders) > 0 {
		orders = append(orders, c.T("stock.portfolio.orders", nil))
		for _, o := range v.Orders {
			orders = append(orders, c.T("stock.portfolio.order_"+o.Side, map[string]any{"no": FormatNumber(c, o.No),
				"company": c.companyName(o.Company), "qty": FormatNumber(c, o.Qty-o.Filled),
				"price": FormatMoney(c, o.Price)}))
			kb.Add(c.T("stock.button.cancel", map[string]any{"no": FormatNumber(c, o.No)}), AddrStockCancel,
				strconv.FormatInt(o.No, 10))
		}
	}
	exchange, _ := keyboards.Button(c.T("finance.button.exchange", nil), AddrExchange)
	gold, _ := keyboards.Button(c.T("finance.button.gold", nil), AddrGold)
	kb.Row(exchange, gold)
	kb.Add(c.T("finance.button.savings", nil), AddrSavings)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLoanHub, RefreshData: AddrPortfolio}))
	return c.respond(paragraphs(notice, body(lines...), body(orders...)), kb.Build()).MarkPrivate()
}

// Listing renders the listing of a company on the exchange.
func Listing(c Context, v ListingView) *presenter.Response {
	return c.withView(renderListing(c, v), ScreenListing, v)
}

func renderListing(c Context, v ListingView) *presenter.Response {
	lines := []string{
		c.T("stock.ipo.title", map[string]any{"company": c.companyName(v.Company)}),
		c.T("stock.ipo.rules", map[string]any{"age": FormatDuration(c, v.MinAge), "revenue": FormatMoney(c, v.MinRevenue),
			"fee": FormatMoney(c, v.Fee)}),
		c.T("stock.ipo.book", map[string]any{"book": FormatMoney(c, v.Book), "total": FormatNumber(c, v.Total)}),
	}
	kb := keyboards.New()
	switch {
	case v.Refused != "":
		lines = append(lines, c.T("stock.ipo.refused."+v.Refused, map[string]any{"wait": FormatDuration(c, v.MinAge-v.Age),
			"revenue": FormatMoney(c, v.Revenue)}))
	case v.Chosen != nil:
		lines = append(lines, c.T("stock.ipo.confirm", map[string]any{"shares": FormatNumber(c, v.Chosen.Qty),
			"price": FormatMoney(c, v.Chosen.Price), "fee": FormatMoney(c, v.Fee)}))
		kb.Add(c.T("stock.button.ipo_yes", nil), AddrStockIPO, v.Company.Code, strconv.FormatInt(v.Chosen.Qty, 10),
			strconv.FormatInt(v.Chosen.Price, 10), v.Nonce)
	default:
		lines = append(lines, c.T("stock.ipo.choose", nil))
		for _, f := range v.Floats {
			var row []presenter.Button
			for _, p := range v.Prices {
				label := c.T("stock.button.ipo_option", map[string]any{"shares": FormatNumber(c, f.Qty),
					"price": FormatMoney(c, p.Price)})
				if btn, ok := keyboards.Button(label, AddrStockIPO, v.Company.Code, strconv.FormatInt(f.Qty, 10),
					strconv.FormatInt(p.Price, 10)); ok {
					row = append(row, btn)
				}
			}
			kb.Grid(1, row...)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrStock, v.Company.Code)}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// Dividend renders a dividend: the choices, its confirmation or what it paid.
func Dividend(c Context, v DividendView) *presenter.Response {
	return c.withView(renderDividend(c, v), ScreenDividend, v)
}

func renderDividend(c Context, v DividendView) *presenter.Response {
	lines := []string{
		c.T("stock.dividend.title", map[string]any{"company": c.companyName(v.Company)}),
		c.T("stock.dividend.free", map[string]any{"free": FormatMoney(c, v.Free), "tax": PercentFromBPS(c, int(v.TaxBPS)),
			"total": FormatNumber(c, v.Total)}),
	}
	kb := keyboards.New()
	switch {
	case v.Refused != "":
		lines = append(lines, c.T("stock.dividend.refused."+v.Refused, nil))
	case v.Paid > 0:
		lines = append(lines, c.T("stock.dividend.paid", map[string]any{"paid": FormatMoney(c, v.Paid),
			"per_share": FormatMoney(c, v.PerShare), "holders": FormatNumber(c, v.Holders)}))
	case v.Chosen != nil:
		lines = append(lines, c.T("stock.dividend.confirm", map[string]any{"amount": FormatMoney(c, v.Chosen.Price),
			"per_share": FormatMoney(c, v.Chosen.Qty)}))
		kb.Add(c.T("stock.button.dividend_yes", nil), AddrStockDividend, v.Company.Code,
			strconv.FormatInt(v.Chosen.Price, 10), v.Nonce)
	default:
		var row []presenter.Button
		for _, o := range v.Options {
			label := c.T("stock.button.dividend_option", map[string]any{"amount": FormatMoney(c, o.Price),
				"per_share": FormatMoney(c, o.Qty)})
			if btn, ok := keyboards.Button(label, AddrStockDividend, v.Company.Code, strconv.FormatInt(o.Price, 10)); ok {
				row = append(row, btn)
			}
		}
		kb.Grid(1, row...)
		if len(row) == 0 {
			lines = append(lines, c.T("stock.dividend.refused.no_money", nil))
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrStock, v.Company.Code)}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// StockNoticeView is a notice of the exchange: an order of the player's
// filled, a dividend received, control of a company changed hands.
type StockNoticeView struct {
	Kind    string
	Company Named
	Side    string
	Qty     int64
	Price   int64
	Amount  int64
	Player  string
}

// StockNotice renders a notice of the exchange.
func StockNotice(c Context, v StockNoticeView) *presenter.Response {
	return c.withView(renderStockNotice(c, v), ScreenStockNotice, v)
}

func renderStockNotice(c Context, v StockNoticeView) *presenter.Response {
	key := "stock.event." + v.Kind
	if v.Kind == "filled" {
		key += "_" + v.Side
	}
	text := c.T(key, map[string]any{"company": c.companyName(v.Company), "qty": FormatNumber(c, v.Qty),
		"price": FormatMoney(c, v.Price), "amount": FormatMoney(c, v.Amount), "player": c.playerName(v.Player)})
	kb := keyboards.New()
	kb.Add(c.T("stock.button.company", map[string]any{"company": c.companyName(v.Company)}), AddrStock, v.Company.Code)
	kb.Add(c.T("finance.button.portfolio", nil), AddrPortfolio)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(text, kb.Build()).MarkPrivate()
}
