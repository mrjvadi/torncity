package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// settleShares books one fill: the money from the buyer's escrow to the
// seller's bank less the fee (which leaves the economy), the shares from the
// seller's row to the buyer's, the trade, the watch's checks, and a takeover
// when the buyer now holds enough. It returns what the seller received.
func (h *FinanceHandler) settleShares(ctx context.Context, tx application.Tx, meta envelope.Metadata, c application.Company,
	t market.Trade, incoming application.ShareOrder, feeBPS int64, now time.Time,
) (int64, error) {
	s, err := market.Settle(t, feeBPS)
	if err != nil {
		return 0, errors.Internal(err)
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, t.Buyer)
	if err != nil {
		return 0, err
	}
	sellerBank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, t.Seller)
	if err != nil {
		return 0, err
	}
	tradeID := h.ids.NewID()
	txID, err := move(ctx, tx, escrow.ID, sellerBank.ID, s.SellerReceives.Minor(), application.ReasonShareTrade,
		application.ShareTradeReference, tradeID, "", now)
	if err != nil {
		return 0, err
	}
	feeTx, err := move(ctx, tx, escrow.ID, application.SystemSinkAccountID, s.Fee.Minor(), application.ReasonMarketFee,
		application.ShareTradeReference, tradeID, "", now)
	if err != nil {
		return 0, err
	}
	if txID == "" {
		txID = feeTx
	}
	// The shares: the seller's locked shares leave with a share of what
	// they cost; the buyer's come at what they paid.
	seller, err := tx.Stocks().Holding(ctx, c.ID, t.Seller)
	if err != nil {
		return 0, err
	}
	if seller.Shares < t.Quantity || seller.Locked < t.Quantity {
		return 0, errors.Internal(errShares)
	}
	costOut := seller.Cost * t.Quantity / seller.Shares
	seller.Shares, seller.Locked, seller.Cost = seller.Shares-t.Quantity, seller.Locked-t.Quantity, seller.Cost-costOut
	if err := tx.Stocks().SaveHolding(ctx, seller); err != nil {
		return 0, err
	}
	buyer, err := tx.Stocks().Holding(ctx, c.ID, t.Buyer)
	if err != nil {
		return 0, err
	}
	if buyer.Shares == 0 {
		buyer.AcquiredAt = now
	}
	buyer.Shares, buyer.Cost = buyer.Shares+t.Quantity, buyer.Cost+t.Notional.Minor()
	if err := tx.Stocks().SaveHolding(ctx, buyer); err != nil {
		return 0, err
	}
	if err := tx.Stocks().RecordTrade(ctx, application.ShareTrade{ID: tradeID, CompanyID: c.ID, BuyOrder: t.BuyOrderID,
		SellOrder: t.SellOrderID, Buyer: t.Buyer, Seller: t.Seller, Qty: t.Quantity, Price: t.UnitPrice.Minor(),
		Notional: t.Notional.Minor(), Fee: s.Fee.Minor(), LedgerTx: txID, At: now}); err != nil {
		return 0, err
	}
	if err := h.watchShares(ctx, tx, c, t, now); err != nil {
		return 0, err
	}
	if err := h.takeover(ctx, tx, meta, c, buyer, now); err != nil {
		return 0, err
	}
	// The resting side's owner is told: their order filled while they were
	// elsewhere.
	restingOwner, restingSide, amount := t.Seller, string(market.Sell), s.SellerReceives.Minor()
	if t.SellOrderID == incoming.ID {
		restingOwner, restingSide, amount = t.Buyer, string(market.Buy), t.Notional.Minor()
	}
	return s.SellerReceives.Minor(), appendDomainEvent(ctx, tx, meta, "stock", "filled", tradeID, map[string]any{
		"kind": "filled", "player_id": restingOwner, "side": restingSide, "company_code": c.Code, "company_name": c.Name,
		"qty": t.Quantity, "price": t.UnitPrice.Minor(), "amount": amount})
}

// errShares means a fill found fewer shares locked than it sold: a bug.
var errShares = stderrors.New("handlers: a share fill found fewer locked shares than it sold")

// watchShares checks a fill for wash trading and an off-market price.
func (h *FinanceHandler) watchShares(ctx context.Context, tx application.Tx, c application.Company, t market.Trade,
	now time.Time,
) error {
	if h.watch == nil {
		return nil
	}
	ab, ba, err := tx.Stocks().PairTrades(ctx, c.ID, t.Seller, t.Buyer, now.Add(-h.watch.Window))
	if err != nil {
		return err
	}
	if f, ok := h.watch.WashTrades(ab, ba); ok {
		if _, err := raise(ctx, tx, f, t.Seller, t.Buyer, now); err != nil {
			return err
		}
	}
	book, err := tx.Stocks().Book(ctx, c.ID)
	if err != nil {
		return err
	}
	return watchTrade(ctx, tx, h.watch, t.Seller, t.Buyer, t.UnitPrice.Minor(),
		finance.BookPerShare(book, c.TotalShares), t.Quantity, now)
}

// takeover passes control of the company to a holder who now holds the
// takeover share of it (finance.yml stocks.takeover_bps).
func (h *FinanceHandler) takeover(ctx context.Context, tx application.Tx, meta envelope.Metadata, c application.Company,
	holder application.ShareHolding, now time.Time,
) error {
	def, ok := h.content.Current().Finance()
	if !ok || holder.PlayerID == c.OwnerID || !finance.Controls(holder.Shares, c.TotalShares, def.Stocks.TakeoverBPS) {
		return nil
	}
	if err := tx.Stocks().TransferControl(ctx, c.ID, holder.PlayerID, now); err != nil {
		return err
	}
	for _, who := range []string{holder.PlayerID, c.OwnerID} {
		if err := appendDomainEvent(ctx, tx, meta, "stock", "takeover", c.ID, map[string]any{
			"kind": "takeover", "player_id": who, "company_code": c.Code, "company_name": c.Name,
			"qty": holder.Shares, "gained": who == holder.PlayerID}); err != nil {
			return err
		}
	}
	return nil
}

// sumShares is a list of holdings' shares.
func sumShares(hs []application.ShareHolding) (map[string]int64, int64) {
	out := make(map[string]int64, len(hs))
	var total int64
	for _, h := range hs {
		out[h.PlayerID] += h.Shares
		total += h.Shares
	}
	return out, total
}

// sortedKeys lists a map's keys in order.
func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
