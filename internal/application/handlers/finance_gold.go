package handlers

import (
	"context"
	stderrors "errors"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The gold dealer (docs/adr/0026 section 6): the NPC economy's gold, a
// finite reserve, sold above its mid price and bought back below it. Gold
// bought leaves the player's money to the NPC economy (gold_purchase, a
// drain), gold sold back brings money in (gold_sale, a faucet); grams are
// conserved — the dealer's stock and every player's add up to the reserve.

// Gold handles gold.show.
func (h *FinanceHandler) Gold(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.gold(ctx, meta, "", nil)
}

func (h *FinanceHandler) gold(ctx context.Context, meta envelope.Metadata, notice string, args map[string]any) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	view := screens.GoldView{Notice: notice, NoticeArgs: args, Options: def.Gold.GramOptions}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		d, err := tx.Finance().Dealer(ctx, h.dealerStart(def, h.now()), false)
		if err != nil {
			return err
		}
		view.Mid, view.Stock = d.Price, d.Stock
		view.Buy, view.Sell = def.GoldRules().Quote(d.Price)
		view.Prev = d.Price
		prices, err := tx.Finance().GoldPrices(ctx, def.Gold.History+1)
		if err != nil {
			return err
		}
		for i, pr := range prices {
			if i == 1 {
				view.Prev = pr.Price
			}
			if i < def.Gold.History {
				view.History = append(view.History, screens.GoldPoint{Price: pr.Price, At: pr.At})
			}
		}
		hold, err := tx.Finance().GoldHolding(ctx, p.ID)
		if err != nil {
			return err
		}
		view.Grams, view.Cost = hold.Grams, hold.Cost
		view.NextAt = nextAt(ctx, tx)
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Gold(h.screen(meta, lang), view), nil
}

// GoldBuy handles gold.buy: without a method, the price and the ways to pay
// it; with one, the gold, once.
func (h *FinanceHandler) GoldBuy(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	return h.goldTrade(ctx, meta, req, true)
}

// GoldSell handles gold.sell: without a nonce, the price; with one, the sale.
func (h *FinanceHandler) GoldSell(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	return h.goldTrade(ctx, meta, req, false)
}

// goldTrade buys or sells gold.
func (h *FinanceHandler) goldTrade(ctx context.Context, meta envelope.Metadata, req FinanceRequest, buy bool) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	grams, err := parseCount(req.Grams)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	if grams > def.Gold.MaxGrams {
		return h.finish(meta, meta.Language, refuseFinance(screens.FinanceRefusedAmount, screens.AddrGold))
	}
	method := payment.Method(strings.TrimSpace(req.Method))
	confirmed := isNonce(req.Nonce) && (!buy || method != "")
	lang := meta.Language
	var (
		confirm *screens.GoldTradeView
		notice  string
		args    map[string]any
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
			if err != nil || !fresh {
				return err
			}
		}
		now := h.now()
		d, err := tx.Finance().Dealer(ctx, h.dealerStart(def, now), confirmed)
		if err != nil {
			return err
		}
		buyAt, sellAt := def.GoldRules().Quote(d.Price)
		price, side := buyAt, "buy"
		if !buy {
			price, side = sellAt, "sell"
		}
		total := grams * price
		hold, err := tx.Finance().GoldHolding(ctx, p.ID)
		if err != nil {
			return err
		}
		switch {
		case buy && d.Stock < grams:
			r := refuseFinance(screens.FinanceRefusedGoldStock, screens.AddrGold)
			r.view.Count = d.Stock
			return r
		case !buy && hold.Grams < grams:
			r := refuseFinance(screens.FinanceRefusedGoldHeld, screens.AddrGold)
			r.view.Count = hold.Grams
			return r
		}
		if !confirmed {
			confirm = &screens.GoldTradeView{Side: side, Grams: grams, Price: price, Total: total, Nonce: h.nonce()}
			if buy {
				wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
				if err != nil {
					return err
				}
				confirm.Payment = paymentChoice(wallet.Plan(moneyOf(total), snap.Accepts(content.ServiceGold)), wallet)
			}
			return nil
		}
		trade := application.GoldTrade{ID: h.ids.NewID(), PlayerID: p.ID, Side: side, Grams: grams, UnitPrice: price,
			Total: total, At: now}
		if buy {
			wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return err
			}
			plan := wallet.Plan(moneyOf(total), snap.Accepts(content.ServiceGold))
			if err := checkMethod(plan, method, wallet, "gold.button.back", screens.AddrGold); err != nil {
				return err
			}
			if trade.LedgerTx, err = wallet.Pay(ctx, tx.Ledger(), application.Charge{Method: method, Accepted: plan.Accepted,
				Reason: application.ReasonGoldPurchase, ReferenceType: application.GoldTradeReference, ReferenceID: trade.ID,
				To: []application.LedgerEntry{{AccountID: application.SystemSinkAccountID, Amount: moneyOf(total)}},
				CreatedAt: now}); err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "gold.button.back", screens.AddrGold)
				}
				return err
			}
			trade.Method = string(method)
			d.Stock -= grams
			hold.Grams, hold.Cost = hold.Grams+grams, hold.Cost+total
		} else {
			bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.ID)
			if err != nil {
				return err
			}
			if trade.LedgerTx, err = move(ctx, tx, application.SystemSourceAccountID, bank.ID, total,
				application.ReasonGoldSale, application.GoldTradeReference, trade.ID, "", now); err != nil {
				return err
			}
			d.Stock += grams
			hold.Cost -= hold.Cost * grams / hold.Grams
			hold.Grams -= grams
		}
		hold.UpdatedAt, d.UpdatedAt = now, now
		if err := tx.Finance().RecordGoldTrade(ctx, trade); err != nil {
			return err
		}
		if err := tx.Finance().SaveGoldHolding(ctx, hold); err != nil {
			return err
		}
		notice, args = side+"_done", map[string]any{"grams": grams, "total": total}
		return tx.Finance().SaveDealer(ctx, *d)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirm != nil {
		return screens.GoldTrade(h.screen(meta, lang), *confirm), nil
	}
	return h.gold(ctx, meta, notice, args)
}
