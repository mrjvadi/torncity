package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// parseCount reads a positive whole number a button carried.
func parseCount(raw string) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || v < 1 {
		return 0, errors.InvalidInput("not a positive whole number")
	}
	return v, nil
}

// Take handles loan.take: without a nonce, the loan's terms and the button
// that takes it; with one, the loan — lent from the national bank, once.
func (h *FinanceHandler) Take(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	amount, err := parseCount(req.Amount)
	if err != nil {
		return nil, err
	}
	term, err := parseCount(req.Term)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	var (
		confirm  *screens.LoanConfirmView
		taken    int64
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		if isNonce(req.Nonce) {
			fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		now := h.now()
		o, err := h.offerFor(ctx, tx, snap, def, p, req.Product, req.Pledge, now)
		if err != nil {
			return err
		}
		if productKind(o.product) != "player" && o.pledge == nil {
			return refuseFinance(screens.FinanceRefusedPledge, screens.AddrLoanOffer, o.product.Code)
		}
		if !o.allows(def, amount, term) {
			r := refuseFinance(screens.FinanceRefusedAmount, screens.AddrLoanOffer, o.product.Code)
			r.view.Amount = o.most
			return r
		}
		s, err := finance.LoanTerms{Principal: amount, RateBPS: o.rate, Periods: term,
			PeriodsPerYear: def.PeriodsPerYear}.Schedule()
		if err != nil {
			return errors.Internal(err)
		}
		if !isNonce(req.Nonce) {
			confirm = &screens.LoanConfirmView{Product: named(o.product.Code, o.product.Name), Amount: amount, Term: term,
				RateBPS: o.rate, Interest: s.Interest, Instalment: s.Amount(1), Total: s.Total(),
				FirstAt: firstDue(nextAt(ctx, tx), h.periodWait(def), now), Pledge: o.pledge, Nonce: h.nonce()}
			return nil
		}
		loan, err := h.lend(ctx, tx, snap, def, p, o, s, now)
		if err != nil {
			return err
		}
		taken = loan.No
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case replayed:
		return h.Hub(ctx, meta)
	case confirm != nil:
		return screens.LoanConfirm(h.screen(meta, lang), *confirm), nil
	}
	return h.view(ctx, meta, taken, false, "taken")
}

// lend makes the loan: the principal from the national bank to the borrower,
// the loan and its first credit event. The player's running loans are
// counted under their player row's lock, so two presses cannot both take
// the last one allowed.
func (h *FinanceHandler) lend(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	p *application.Player, o offer, s finance.Schedule, now time.Time,
) (application.Loan, error) {
	if err := tx.Finance().LockBorrower(ctx, p.ID); err != nil {
		return application.Loan{}, err
	}
	running, err := tx.Finance().ActiveLoans(ctx, p.ID)
	if err != nil {
		return application.Loan{}, err
	}
	if running >= def.Bank.MaxActiveLoans {
		r := refuseFinance(screens.FinanceRefusedTooMany)
		r.view.Count = int64(def.Bank.MaxActiveLoans)
		return application.Loan{}, r
	}
	loan := application.Loan{ID: h.ids.NewID(), Product: o.product.Code, BorrowerKind: application.BorrowerPlayer,
		PlayerID: p.ID, CountryID: o.bank.country.ID, Principal: s.Principal, RateBPS: o.rate, Interest: s.Interest,
		Periods: s.Periods, OpenedAt: now}
	to, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.ID)
	if err != nil {
		return loan, err
	}
	switch productKind(o.product) {
	case "mortgage":
		no, _ := strconv.ParseInt(pledgeArgOf(o.pledge), 10, 64)
		pr, err := tx.Property().ByNo(ctx, no, true)
		if err != nil {
			return loan, err
		}
		if pr.OwnerID != p.ID || pr.Status != application.PropertyOwned {
			return loan, refuseFinance(screens.FinanceRefusedPledge)
		}
		loan.PropertyID = pr.ID
	case "company":
		c, err := tx.Companies().ByCode(ctx, o.pledge.Code)
		if err != nil {
			return loan, err
		}
		if c, err = tx.Companies().Lock(ctx, c.ID); err != nil {
			return loan, err
		}
		if c.OwnerID != p.ID || !c.Active() {
			return loan, refuseFinance(screens.FinanceRefusedNotOwner)
		}
		loan.BorrowerKind, loan.CompanyID = application.BorrowerCompany, c.ID
		if to, err = tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, c.ID); err != nil {
			return loan, err
		}
	}
	current, _, err := tx.Finance().CurrentPeriod(ctx)
	if err != nil {
		return loan, err
	}
	loan.FirstPeriod = current + 1
	txID, err := move(ctx, tx, o.bank.account.ID, to.ID, s.Principal, application.ReasonLoanDisbursement,
		application.LoanReference, loan.ID, "", now)
	if stderrors.Is(err, application.ErrInsufficientFunds) {
		return loan, refuseFinance(screens.FinanceRefusedBankDry)
	}
	if err != nil {
		return loan, err
	}
	loan.DisbursementTx = txID
	if loan, err = tx.Finance().CreateLoan(ctx, loan); err != nil {
		return loan, err
	}
	_, err = tx.Finance().AddCreditEvent(ctx, application.CreditEvent{ID: h.ids.NewID(), PlayerID: p.ID, LoanID: loan.ID,
		Kind: application.CreditOpened, PeriodNo: current, At: now})
	return loan, err
}

// pledgeArgOf is a pledge's argument.
func pledgeArgOf(p *screens.PledgeLine) string {
	if p == nil {
		return ""
	}
	if p.Code != "" {
		return p.Code
	}
	return strconv.FormatInt(p.No, 10)
}
