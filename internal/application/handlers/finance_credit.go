package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// A player's credit score (docs/adr/0026 section 3) is worked out whenever
// it is needed, from what the game recorded: instalments paid and missed,
// defaults, loans opened lately, time in the game on the game clock, shifts
// worked lately, net worth and what they owe.

// credit is a player's score, and the history it was worked out from.
type credit struct {
	history finance.CreditHistory
	score   finance.CreditScore
	rules   finance.CreditRules
}

// view is the score as screens show it.
func (c credit) view() screens.CreditView {
	s := c.score
	return screens.CreditView{Score: s.Score, Min: c.rules.Min, Max: c.rules.Max, PaymentBPS: s.PaymentBPS,
		DebtBPS: s.DebtBPS, HistoryBPS: s.HistoryBPS, IncomeBPS: s.IncomeBPS, WorthBPS: s.WorthBPS,
		NewCreditBPS: s.NewCreditBPS, Missed: c.history.Missed, Defaults: c.history.Defaults}
}

// creditOf works out a player's credit score now.
func (h *FinanceHandler) creditOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	p *application.Player, now time.Time,
) (credit, error) {
	c := credit{rules: def.CreditRules()}
	rec, err := tx.Finance().CreditRecord(ctx, p.ID, h.realBefore(now, def.Credit.Memory),
		h.realBefore(now, def.Credit.NewCreditWindow))
	if err != nil {
		return c, err
	}
	shifts, err := tx.Finance().ShiftsSince(ctx, p.ID, h.realBefore(now, def.Credit.IncomeWindow))
	if err != nil {
		return c, err
	}
	owed, err := tx.Finance().PersonalOwed(ctx, p.ID)
	if err != nil {
		return c, err
	}
	w, err := worthOf(ctx, tx, snap, h.cities, p.ID)
	if err != nil {
		return c, err
	}
	worth := w.Total()
	c.history = finance.CreditHistory{OnTime: rec.OnTime, Missed: rec.Missed, Defaults: rec.Defaults,
		Opened: rec.Opened, Age: h.gameSince(p.CreatedAt, now), Shifts: shifts, NetWorth: worth, Owed: owed}
	c.score = c.rules.Score(c.history)
	return c, nil
}
