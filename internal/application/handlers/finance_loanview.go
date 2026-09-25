package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// View handles loan.view: one of the player's loans.
func (h *FinanceHandler) View(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	no, err := parseCount(req.No)
	if err != nil {
		return nil, err
	}
	return h.view(ctx, meta, no, false, "")
}

// view renders a loan, asking about its payoff when confirm is set.
func (h *FinanceHandler) view(ctx context.Context, meta envelope.Metadata, no int64, confirm bool, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	var view screens.LoanDetailView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		l, err := h.ownLoan(ctx, tx, p, no, false)
		if err != nil {
			return err
		}
		view, err = h.loanDetail(ctx, tx, snap, def, *l)
		if err != nil {
			return err
		}
		view.Notice, view.ConfirmOpen = notice, confirm
		if confirm {
			view.Nonce = h.nonce()
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.LoanDetail(h.screen(meta, lang), view), nil
}

// ownLoan reads a loan the player answers for.
func (h *FinanceHandler) ownLoan(ctx context.Context, tx application.Tx, p *application.Player, no int64, lock bool) (*application.Loan, error) {
	l, err := tx.Finance().LoanByNo(ctx, no, lock)
	if isSentinel(err, application.ErrLoanNotFound) || (err == nil && l.PlayerID != p.ID) {
		return nil, refuseFinance(screens.FinanceRefusedNotYours)
	}
	return l, err
}

// loanDetail is a loan as its screen shows it.
func (h *FinanceHandler) loanDetail(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	l application.Loan,
) (screens.LoanDetailView, error) {
	line, err := h.loanLine(ctx, tx, def, l)
	if err != nil {
		return screens.LoanDetailView{}, err
	}
	v := screens.LoanDetailView{Loan: line, Principal: l.Principal, Interest: l.Interest, RateBPS: l.RateBPS,
		Periods: l.Periods, Paid: l.PaidPeriods, FeesDue: l.FeesDue, Payoff: l.Owed(), OpenedAt: l.OpenedAt,
		Missed: l.MissedTotal, Recovered: l.Recovered, WrittenOff: l.WrittenOff}
	if l.Status == application.LoanActive {
		v.NextAt = nextAt(ctx, tx)
	}
	if l.PropertyID != "" {
		pr, err := tx.Property().ByID(ctx, l.PropertyID, false)
		if err != nil {
			return v, err
		}
		pl, err := h.propertyLine(ctx, snap, *pr)
		if err != nil {
			return v, err
		}
		v.Pledge = &pl
	}
	return v, nil
}

// loanPurse is where a loan's payments come from: the player's bank then
// cash, or the borrowing company's free money (locked).
func (h *FinanceHandler) loanPurse(ctx context.Context, tx application.Tx, l application.Loan) (purse, error) {
	if l.CompanyID == "" {
		return playerPurse(ctx, tx, l.PlayerID)
	}
	c, err := tx.Companies().Lock(ctx, l.CompanyID)
	if err != nil {
		return purse{}, err
	}
	if !c.Active() {
		return purse{}, nil
	}
	return companyPurse(ctx, tx, *c)
}

// repay books a payment of a loan: principal, interest and late fees, each
// under its own reason, from pu into the national bank.
func (h *FinanceHandler) repay(ctx context.Context, tx application.Tx, l *application.Loan, pu *purse, principal, interest,
	fees int64, now time.Time,
) error {
	bank, err := tx.Ledger().AccountFor(ctx, application.AccountNationalBank, l.CountryID)
	if err != nil {
		return err
	}
	for _, part := range []struct {
		amount int64
		reason application.Reason
	}{{principal, application.ReasonLoanRepayment}, {interest, application.ReasonLoanInterest},
		{fees, application.ReasonLoanPenalty}} {
		if _, err := pu.pay(ctx, tx, part.amount, part.reason, application.LoanReference, l.ID, bank.ID, "", now); err != nil {
			return err
		}
	}
	l.PrincipalPaid += principal
	l.InterestPaid += interest
	l.FeesPaid += fees
	return nil
}

// Repay handles loan.repay: without a nonce, the payoff and its button; with
// one, the whole loan paid off now — every instalment left and the fees.
func (h *FinanceHandler) Repay(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := parseCount(req.No)
	if err != nil {
		return nil, err
	}
	if !isNonce(req.Nonce) {
		return h.view(ctx, meta, no, true, "")
	}
	lang := meta.Language
	var paid int64
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
		if err != nil || !fresh {
			return err
		}
		l, err := h.ownLoan(ctx, tx, p, no, true)
		if err != nil {
			return err
		}
		if l.Status != application.LoanActive {
			return nil
		}
		now := h.now()
		principal, interest, fees := finance.Payoff(l.State())
		pu, err := h.loanPurse(ctx, tx, *l)
		if err != nil {
			return err
		}
		if pu.total() < principal+interest+fees {
			r := refuseFinance(screens.FinanceRefusedShort, screens.AddrLoanView, req.No)
			r.view.Amount = principal + interest + fees
			return r
		}
		if err := h.repay(ctx, tx, l, &pu, principal, interest, fees, now); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				return refuseFinance(screens.FinanceRefusedShort, screens.AddrLoanView, req.No)
			}
			return err
		}
		paid = principal + interest + fees
		current, _, err := tx.Finance().CurrentPeriod(ctx)
		if err != nil {
			return err
		}
		l.PaidPeriods, l.Arrears, l.FeesDue = l.Periods, 0, 0
		l.Status, l.ClosedAt, l.UpdatedAt = application.LoanRepaid, &now, now
		if err := tx.Finance().SaveLoan(ctx, *l); err != nil {
			return err
		}
		_, err = tx.Finance().AddCreditEvent(ctx, application.CreditEvent{ID: h.ids.NewID(), PlayerID: l.PlayerID,
			LoanID: l.ID, Kind: application.CreditRepaid, PeriodNo: current, At: now})
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	notice := ""
	if paid > 0 {
		notice = "paid_off"
	}
	return h.view(ctx, meta, no, false, notice)
}
