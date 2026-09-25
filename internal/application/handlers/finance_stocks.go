package handlers

import (
	"context"
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Exchange handles stock.list: every listed company, public.
func (h *FinanceHandler) Exchange(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	var view screens.ExchangeView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		if _, err := h.player(ctx, tx, meta, &lang); err != nil {
			return err
		}
		listed, err := tx.Stocks().Listed(ctx, h.now().Add(-h.periodWait(def)))
		if err != nil {
			return err
		}
		for _, l := range listed {
			line, err := h.listedLine(ctx, snap, l)
			if err != nil {
				return err
			}
			view.Lines = append(view.Lines, line)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.Exchange(h.screen(meta, lang), view), nil
}

// listedLine is a listed company for the exchange's list.
func (h *FinanceHandler) listedLine(ctx context.Context, snap *content.Snapshot, l application.ListedCompany) (screens.ListedLine, error) {
	price := l.LastPrice
	if price == 0 {
		price = l.IPOPrice
	}
	prev := l.PrevPrice
	if prev == 0 {
		prev = l.IPOPrice
	}
	line := screens.ListedLine{Company: named(l.Company.Code, l.Company.Name), Type: named(l.Company.TypeCode, l.Company.TypeCode),
		Price: price, Prev: prev, Volume: l.Volume, Cap: price * l.Company.TotalShares}
	if t, _, ok := snap.CompanyType(l.Company.TypeCode); ok {
		line.Type.Name = t.Name
	}
	city, err := h.cities.ByID(ctx, l.Company.CityID)
	if err != nil {
		return line, err
	}
	line.City = cityPlace(*city)
	return line, nil
}

// Stock handles stock.view: one company on the exchange.
func (h *FinanceHandler) Stock(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	return h.stock(ctx, meta, req.Code, "", nil)
}

func (h *FinanceHandler) stock(ctx context.Context, meta envelope.Metadata, code, notice string, args map[string]any) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	var view screens.StockView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.company(ctx, tx, code, false)
		if err != nil {
			return err
		}
		view, err = h.stockView(ctx, tx, snap, def, *c, p)
		view.Notice, view.NoticeArgs = notice, args
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Stock(h.screen(meta, lang), view), nil
}

// company reads an active company by its code.
func (h *FinanceHandler) company(ctx context.Context, tx application.Tx, code string, lock bool) (*application.Company, error) {
	c, err := tx.Companies().ByCode(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if isSentinel(err, application.ErrCompanyNotFound) || (err == nil && !c.Active()) {
		return nil, refuseFinance(screens.FinanceRefusedNotListed, screens.AddrExchange)
	}
	if err != nil || !lock {
		return c, err
	}
	return tx.Companies().Lock(ctx, c.ID)
}

// refPrice is a company's reference price: its last trade, else its listing
// price, else its book per share.
func refPrice(last int64, listing *application.StockListing, book, total int64) int64 {
	switch {
	case last > 0:
		return last
	case listing != nil:
		return listing.Price
	}
	return finance.BookPerShare(book, total)
}

// stockView is a company as its page shows it to p.
func (h *FinanceHandler) stockView(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	c application.Company, p *application.Player,
) (screens.StockView, error) {
	v := screens.StockView{Company: named(c.Code, c.Name), Type: named(c.TypeCode, c.TypeCode), Total: c.TotalShares,
		Owner: c.OwnerID == p.ID, FeeBPS: 0}
	if t, _, ok := snap.CompanyType(c.TypeCode); ok {
		v.Type.Name = t.Name
	}
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return v, err
	}
	v.City = cityPlace(*city)
	listing, err := tx.Stocks().Listing(ctx, c.ID)
	if err != nil {
		return v, err
	}
	book, err := tx.Stocks().Book(ctx, c.ID)
	if err != nil {
		return v, err
	}
	last, err := tx.Stocks().LastPrice(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Book, v.Listed = finance.BookPerShare(book, c.TotalShares), listing != nil
	v.Price = refPrice(last, listing, book, c.TotalShares)
	v.Prev = v.Price
	if listing != nil {
		v.IPO = listing.Price
		trades, err := tx.Stocks().Trades(ctx, c.ID, def.Stocks.Trades)
		if err != nil {
			return v, err
		}
		for i, t := range trades {
			v.Trades = append(v.Trades, screens.TradeLine{Qty: t.Qty, Price: t.Price, At: t.At})
			if i == 1 {
				v.Prev = t.Price
			}
		}
		if len(trades) == 1 {
			v.Prev = listing.Price
		}
		orders, err := tx.Stocks().OpenOrders(ctx, c.ID)
		if err != nil {
			return v, err
		}
		v.Bids, v.Asks = depth(orders, def.Stocks.BookDepth)
	}
	hold, err := tx.Stocks().Holding(ctx, c.ID, p.ID)
	if err != nil {
		return v, err
	}
	v.Holding, v.Locked, v.Cost = hold.Shares, hold.Locked, hold.Cost
	owner, err := tx.Players().GetByID(ctx, c.OwnerID)
	if err == nil {
		v.Controller = shownName(owner)
	}
	divs, err := tx.Stocks().Dividends(ctx, c.ID, 1)
	if err != nil {
		return v, err
	}
	if len(divs) > 0 {
		v.LastDiv = divs[0].PerShare
	}
	for _, step := range def.Stocks.PriceSteps {
		price := max(finance.OfBPS(v.Price, step), 1)
		qty := def.Stocks.QtyOptions[0]
		v.Buys = append(v.Buys, screens.PriceOption{Qty: qty, Price: price})
		if hold.Free() >= qty {
			v.Sells = append(v.Sells, screens.PriceOption{Qty: qty, Price: price})
		}
	}
	for _, qty := range def.Stocks.QtyOptions[1:] {
		v.Buys = append(v.Buys, screens.PriceOption{Qty: qty, Price: v.Price})
		if hold.Free() >= qty {
			v.Sells = append(v.Sells, screens.PriceOption{Qty: qty, Price: v.Price})
		}
	}
	return v, nil
}

// depth sums a book's open orders by price, the best n of each side.
func depth(orders []application.ShareOrder, n int) (bids, asks []screens.BookLevel) {
	sum := map[string]map[int64]int64{"buy": {}, "sell": {}}
	for _, o := range orders {
		sum[o.Side][o.Price] += o.Qty - o.Filled
	}
	level := func(side string, desc bool) []screens.BookLevel {
		var out []screens.BookLevel
		for p, q := range sum[side] {
			out = append(out, screens.BookLevel{Price: p, Qty: q})
		}
		sort.Slice(out, func(i, j int) bool {
			if desc {
				return out[i].Price > out[j].Price
			}
			return out[i].Price < out[j].Price
		})
		return out[:min(n, len(out))]
	}
	return level("buy", true), level("sell", false)
}

// BuyShares handles stock.buy.
func (h *FinanceHandler) BuyShares(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	return h.order(ctx, meta, req, market.Buy)
}

// SellShares handles stock.sell.
func (h *FinanceHandler) SellShares(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	return h.order(ctx, meta, req, market.Sell)
}
