package handlers

import (
	"context"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// order places a buy or a sell: without a nonce, its confirmation; with
// one, the order, once.
func (h *FinanceHandler) order(ctx context.Context, meta envelope.Metadata, req FinanceRequest, side market.Side) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	qty, err := parseCount(req.Qty)
	if err != nil {
		return nil, err
	}
	price, err := parseCount(req.Price)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	if _, err := h.def(snap); err != nil {
		return nil, err
	}
	lang := meta.Language
	var (
		view     screens.StockOrderView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirmed := isNonce(req.Nonce)
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		c, err := h.company(ctx, tx, req.Code, confirmed)
		if err != nil {
			return err
		}
		listing, err := tx.Stocks().Listing(ctx, c.ID)
		if err != nil {
			return err
		}
		if listing == nil {
			return refuseFinance(screens.FinanceRefusedNotListed, screens.AddrStock, c.Code)
		}
		if qty > maxShareQty || price > maxSharePrice {
			return refuseFinance(screens.FinanceRefusedOrder, screens.AddrStock, c.Code)
		}
		if !confirmed {
			fee, err := h.feeOf(ctx, *c)
			if err != nil {
				return err
			}
			bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.ID)
			if err != nil {
				return err
			}
			view = screens.StockOrderView{Company: named(c.Code, c.Name), Side: string(side), Qty: qty, Price: price,
				Reserve: qty * price, Bank: bank.Balance.Minor(), FeeBPS: fee, Nonce: h.nonce()}
			return nil
		}
		view, err = h.placeShares(ctx, tx, meta, *c, p, side, qty, price, h.now())
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.Portfolio(ctx, meta)
	}
	return screens.StockOrder(h.screen(meta, lang), view), nil
}

// CancelShares handles stock.cancel: the owner takes an open order off the
// book and gets back what it holds.
func (h *FinanceHandler) CancelShares(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	if err != nil || no < 1 {
		return nil, err
	}
	lang := meta.Language
	notice := ""
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		o, err := tx.Stocks().Order(ctx, no)
		if isSentinel(err, application.ErrShareOrderNotFound) || (err == nil && o.OwnerID != p.ID) {
			return refuseFinance(screens.FinanceRefusedNotYours, screens.AddrPortfolio)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Companies().Lock(ctx, o.CompanyID); err != nil {
			return err
		}
		// Read again under the book's lock: it may have filled meanwhile.
		if o, err = tx.Stocks().Order(ctx, no); err != nil || o.Status != application.OrderOpen {
			return err
		}
		now := h.now()
		if err := tx.Stocks().UpdateOrder(ctx, o.ID, o.Filled, application.OrderCancelled, &now); err != nil {
			return err
		}
		notice = "cancelled"
		return h.releaseShares(ctx, tx, *o, o.Qty-o.Filled, now)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.portfolio(ctx, meta, notice, map[string]any{"no": no})
}
