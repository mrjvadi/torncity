package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// Stage G2 (docs/adr/0026-finance.md): finance.
type financeHandlers struct {
	finance *handlers.FinanceHandler
}

// bindFinance maps the finance commands to their handlers.
func (h phaseHandlers) bindFinance() map[string]commandFunc {
	f := h.stageG2.finance
	return map[string]commandFunc{
		"loan.hub":       bare(f.Hub),
		"loan.offer":     decoded(f.Offer),
		"loan.take":      decoded(f.Take),
		"loan.view":      decoded(f.View),
		"loan.repay":     decoded(f.Repay),
		"save.show":      bare(f.Savings),
		"save.deposit":   decoded(f.Deposit),
		"save.withdraw":  decoded(f.Withdraw),
		"insure.list":    bare(f.Insurance),
		"insure.buy":     decoded(f.Buy),
		"insure.cancel":  decoded(f.Cancel),
		"stock.list":     bare(f.Exchange),
		"stock.view":     decoded(f.Stock),
		"stock.buy":      decoded(f.BuyShares),
		"stock.sell":     decoded(f.SellShares),
		"stock.cancel":   decoded(f.CancelShares),
		"stock.mine":     bare(f.Portfolio),
		"stock.ipo":      decoded(f.IPO),
		"stock.dividend": decoded(f.Dividend),
		"gold.show":      bare(f.Gold),
		"gold.buy":       decoded(f.GoldBuy),
		"gold.sell":      decoded(f.GoldSell),
		// The scheduler's: a finance period ending.
		"finance.settle": decoded(f.Settle),
	}
}
