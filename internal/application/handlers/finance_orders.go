package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The stock exchange's book (docs/adr/0026 section 5): one book per listed
// company, matched by the one engine of internal/domain/market — price-time
// priority, the resting price, self-trade cancelling the resting order — and
// settled here: a buy's money set aside from its owner's bank, a sell's
// shares locked on its holder's row, every fill paying the seller's bank
// less the fee (the company's city's market fee) and moving the shares, all
// under the company's lock.

// maxShareOrder bounds an order's quantity and price so no product
// overflows.
const (
	maxShareQty   = 1_000_000
	maxSharePrice = 1_000_000_000
)

// shareKey is a company's book.
func shareKey(companyID string) market.AssetKey {
	return market.AssetKey{Type: market.AssetShare, ID: companyID, Channel: market.ChannelPublic}
}

// domainShareOrder is a stored order as the matcher takes it.
func domainShareOrder(o application.ShareOrder) market.Order {
	return market.Order{ID: o.ID, Side: market.Side(o.Side), Kind: market.Limit, Asset: shareKey(o.CompanyID),
		Quantity: o.Qty, Filled: o.Filled, UnitPrice: money.FromMinor(o.Price), Owner: o.OwnerID,
		CreatedAt: o.CreatedAt, ExpiresAt: o.ExpiresAt}
}

// feeOf is the market fee of a company's city, bps.
func (h *FinanceHandler) feeOf(ctx context.Context, c application.Company) (int64, error) {
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return 0, err
	}
	v, err := h.policy.Get(ctx, city.JurisdictionID, LeverMarketFee)
	if err != nil {
		return 0, err
	}
	return v.Value, nil
}

// placeShares sets an order's escrow aside, matches it against the
// company's book, settles every fill, and rests what is left. The company
// is locked by the caller.
func (h *FinanceHandler) placeShares(ctx context.Context, tx application.Tx, meta envelope.Metadata, c application.Company,
	p *application.Player, side market.Side, qty, price int64, now time.Time,
) (screens.StockOrderView, error) {
	view := screens.StockOrderView{Company: named(c.Code, c.Name), Side: string(side), Qty: qty, Price: price, Placed: true}
	open, err := tx.Stocks().CountOpen(ctx, p.ID)
	if err != nil {
		return view, err
	}
	if open >= h.limits.MaxOpen {
		r := refuseFinance(screens.FinanceRefusedTooManyOrd, screens.AddrStock, c.Code)
		r.view.Count = int64(h.limits.MaxOpen)
		return view, r
	}
	order := application.ShareOrder{ID: h.ids.NewID(), CompanyID: c.ID, Side: string(side), Qty: qty, Price: price,
		OwnerID: p.ID, Status: application.OrderOpen, CreatedAt: now, ExpiresAt: now.Add(h.limits.OrderTTL)}
	if side == market.Buy {
		bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.ID)
		if err != nil {
			return view, err
		}
		escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, p.ID)
		if err != nil {
			return view, err
		}
		if _, err := move(ctx, tx, bank.ID, escrow.ID, qty*price, application.ReasonShareEscrow,
			application.ShareOrderReference, order.ID, "", now); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				r := refuseFinance(screens.FinanceRefusedShort, screens.AddrStock, c.Code)
				r.view.Amount = qty * price
				return view, r
			}
			return view, err
		}
	} else {
		hold, err := tx.Stocks().Holding(ctx, c.ID, p.ID)
		if err != nil {
			return view, err
		}
		if hold.Free() < qty {
			r := refuseFinance(screens.FinanceRefusedNoShares, screens.AddrStock, c.Code)
			r.view.Count = hold.Free()
			return view, r
		}
		hold.Locked += qty
		if err := tx.Stocks().SaveHolding(ctx, hold); err != nil {
			return view, err
		}
	}
	resting, err := tx.Stocks().OpenOrders(ctx, c.ID)
	if err != nil {
		return view, err
	}
	book := market.NewBook(shareKey(c.ID))
	stored := map[string]application.ShareOrder{}
	for _, o := range resting {
		stored[o.ID] = o
		d := domainShareOrder(o)
		if d.Side == market.Buy {
			book.Bids = append(book.Bids, d)
		} else {
			book.Asks = append(book.Asks, d)
		}
	}
	res, err := market.Match(book, domainShareOrder(order), now)
	if err != nil {
		return view, errors.Internal(err)
	}
	order.Filled = res.Incoming.Filled
	if !res.Rests {
		order.Status = application.OrderFilled
		order.ClosedAt = &now
	}
	placed, err := tx.Stocks().PlaceOrder(ctx, order)
	if err != nil {
		return view, err
	}
	fee, err := h.feeOf(ctx, c)
	if err != nil {
		return view, err
	}
	for _, t := range res.Trades {
		got, err := h.settleShares(ctx, tx, meta, c, t, placed, fee, now)
		if err != nil {
			return view, err
		}
		view.Spent += t.Notional.Minor()
		view.Got += got
	}
	for _, u := range res.Updated {
		if err := tx.Stocks().UpdateOrder(ctx, u.ID, u.Filled, application.OrderOpen, nil); err != nil {
			return view, err
		}
	}
	for _, r := range res.Removed {
		status := application.OrderFilled
		switch r.Reason {
		case market.RemovedExpired:
			status = application.OrderExpired
		case market.RemovedSelfTrade:
			status = application.OrderCancelled
		}
		closed := now
		if err := tx.Stocks().UpdateOrder(ctx, r.Order.ID, r.Order.Filled, status, &closed); err != nil {
			return view, err
		}
		if status != application.OrderFilled {
			if err := h.releaseShares(ctx, tx, stored[r.Order.ID], r.Order.Remaining(), now); err != nil {
				return view, err
			}
		}
	}
	if side == market.Buy && view.Spent > 0 {
		if improvement := order.Filled*price - view.Spent; improvement > 0 {
			if err := h.refundShares(ctx, tx, placed, improvement, now); err != nil {
				return view, err
			}
		}
	}
	view.No, view.Filled, view.Rests, view.ExpiresAt = placed.No, order.Filled, res.Rests, order.ExpiresAt
	return view, nil
}

// releaseShares gives back what a closed order still holds: a buy's money
// to its owner's bank, a sell's shares unlocked.
func (h *FinanceHandler) releaseShares(ctx context.Context, tx application.Tx, o application.ShareOrder, remaining int64,
	now time.Time,
) error {
	if remaining <= 0 {
		return nil
	}
	if o.Side == string(market.Buy) {
		return h.refundShares(ctx, tx, o, remaining*o.Price, now)
	}
	hold, err := tx.Stocks().Holding(ctx, o.CompanyID, o.OwnerID)
	if err != nil {
		return err
	}
	hold.Locked = max(hold.Locked-remaining, 0)
	return tx.Stocks().SaveHolding(ctx, hold)
}

// refundShares moves money from an order owner's escrow back to their bank.
func (h *FinanceHandler) refundShares(ctx context.Context, tx application.Tx, o application.ShareOrder, amount int64,
	now time.Time,
) error {
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, o.OwnerID)
	if err != nil {
		return err
	}
	bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, o.OwnerID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, escrow.ID, bank.ID, amount, application.ReasonShareRelease, application.ShareOrderReference,
		o.ID, "", now)
	return err
}

// expireOrders ends every order past its time, giving back what it holds.
func (h *FinanceHandler) expireOrders(ctx context.Context, tx application.Tx, meta envelope.Metadata, now time.Time) error {
	companies, err := tx.Stocks().ExpiredCompanies(ctx, now)
	if err != nil {
		return err
	}
	for _, id := range companies {
		if _, err := tx.Companies().Lock(ctx, id); err != nil {
			return err
		}
		orders, err := tx.Stocks().OpenOrders(ctx, id)
		if err != nil {
			return err
		}
		for _, o := range orders {
			if now.Before(o.ExpiresAt) {
				continue
			}
			closed := now
			if err := tx.Stocks().UpdateOrder(ctx, o.ID, o.Filled, application.OrderExpired, &closed); err != nil {
				return err
			}
			if err := h.releaseShares(ctx, tx, o, o.Qty-o.Filled, now); err != nil {
				return err
			}
		}
	}
	return nil
}
