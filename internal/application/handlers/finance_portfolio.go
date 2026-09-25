package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Portfolio handles stock.mine: the player's shares, gold and savings, what
// they are worth and what they made, and their open orders.
func (h *FinanceHandler) Portfolio(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.portfolio(ctx, meta, "", nil)
}

func (h *FinanceHandler) portfolio(ctx context.Context, meta envelope.Metadata, notice string, args map[string]any) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	view := screens.PortfolioView{Notice: notice, NoticeArgs: args}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		holdings, err := tx.Stocks().HoldingsOf(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, l := range holdings {
			price := l.LastPrice
			if price == 0 {
				price = finance.BookPerShare(l.Book, l.Company.TotalShares)
				if l.Listed {
					if listing, err := tx.Stocks().Listing(ctx, l.Company.ID); err == nil && listing != nil {
						price = listing.Price
					}
				}
			}
			view.Holdings = append(view.Holdings, screens.HoldingLine{Company: named(l.Company.Code, l.Company.Name),
				Shares: l.Holding.Shares, Locked: l.Holding.Locked, Price: price, Value: price * l.Holding.Shares,
				Cost: l.Holding.Cost, Listed: l.Listed})
		}
		orders, err := tx.Stocks().OrdersOf(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, o := range orders {
			c, err := tx.Companies().ByID(ctx, o.CompanyID)
			if err != nil {
				return err
			}
			view.Orders = append(view.Orders, screens.OpenOrderLine{No: o.No, Company: named(c.Code, c.Name), Side: o.Side,
				Qty: o.Qty, Filled: o.Filled, Price: o.Price})
		}
		gold, err := tx.Finance().GoldHolding(ctx, p.ID)
		if err != nil {
			return err
		}
		d, err := tx.Finance().Dealer(ctx, h.dealerStart(def, h.now()), false)
		if err != nil {
			return err
		}
		_, bid := def.GoldRules().Quote(d.Price)
		view.Gold, view.GoldVal = gold.Grams, gold.Grams*bid
		ports, err := tx.Finance().Portfolios(ctx, p.ID, bid)
		if err != nil {
			return err
		}
		if len(ports) > 0 {
			view.Savings, view.Value, view.Gain = ports[0].Savings, ports[0].Value(), ports[0].Gain()
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Portfolio(h.screen(meta, lang), view), nil
}
