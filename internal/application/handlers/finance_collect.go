package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// collectLoans collects one period of every running loan due, once each:
// the oldest instalments first, whole, from the borrower's bank and then
// cash (a company's free money for a business loan); an instalment unpaid is
// missed — a late fee, a mark on the record — and too many in arrears
// default the loan: its property is repossessed and the city that takes it
// pays the bank what it recovers, the rest written off.
func (h *FinanceHandler) collectLoans(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	meta envelope.Metadata, period int64, now, nextDue time.Time,
) error {
	loans, err := tx.Finance().DueLoans(ctx, period)
	if err != nil {
		return err
	}
	for i := range loans {
		if err := h.collectLoan(ctx, tx, snap, def, meta, &loans[i], period, now, nextDue); err != nil {
			return err
		}
	}
	return nil
}

// collectLoan collects one period of one loan.
func (h *FinanceHandler) collectLoan(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	meta envelope.Metadata, l *application.Loan, period int64, now, nextDue time.Time,
) error {
	product, ok := def.Loan(l.Product)
	rules := finance.LoanRules{LateFeeBPS: 0, DefaultAfter: 0}
	if ok {
		rules = product.Rules()
	}
	due := int64(0)
	if l.PaidPeriods+l.Arrears < l.Periods {
		due = 1
	}
	pu, err := h.loanPurse(ctx, tx, *l)
	if err != nil {
		return err
	}
	c := finance.Collect(l.State(), due, pu.total(), rules)
	var principal, interest int64
	for _, p := range c.Instalments {
		principal += p.Principal
		interest += p.Interest
	}
	fresh, err := tx.Finance().RecordLoanPeriod(ctx, application.LoanPeriod{LoanID: l.ID, PeriodNo: period,
		Due: min(l.Arrears+due, l.Periods-l.PaidPeriods), Paid: int64(len(c.Instalments)), Principal: principal,
		Interest: interest, Fees: c.Fees, NewFee: c.NewFee, Missed: c.Missed, Defaulted: c.Defaulted, At: now})
	if err != nil || !fresh {
		return err
	}
	if err := h.repay(ctx, tx, l, &pu, principal, interest, c.Fees, now); err != nil {
		return err
	}
	l.PaidPeriods += int64(len(c.Instalments))
	l.Arrears, l.FeesDue, l.UpdatedAt = c.Arrears, c.FeesDue, now
	kind := application.CreditOnTime
	if c.Missed {
		kind = application.CreditMissed
		l.MissedTotal++
	}
	if due > 0 || c.Missed {
		if _, err := tx.Finance().AddCreditEvent(ctx, application.CreditEvent{ID: h.ids.NewID(), PlayerID: l.PlayerID,
			LoanID: l.ID, Kind: kind, PeriodNo: period, At: now}); err != nil {
			return err
		}
	}
	product.Name = productName(def, l.Product)
	switch {
	case c.Defaulted:
		return h.defaultLoan(ctx, tx, snap, meta, l, product, period, now)
	case c.Repaid:
		l.Status, l.ClosedAt = application.LoanRepaid, &now
		if _, err := tx.Finance().AddCreditEvent(ctx, application.CreditEvent{ID: h.ids.NewID(), PlayerID: l.PlayerID,
			LoanID: l.ID, Kind: application.CreditRepaid, PeriodNo: period, At: now}); err != nil {
			return err
		}
		if err := tx.Finance().SaveLoan(ctx, *l); err != nil {
			return err
		}
		return h.loanEvent(ctx, tx, meta, "repaid", *l, product, l.Principal+l.Interest, 0, time.Time{})
	}
	if err := tx.Finance().SaveLoan(ctx, *l); err != nil {
		return err
	}
	if c.Missed {
		if err := h.loanEvent(ctx, tx, meta, "missed", *l, product, l.Next(), c.NewFee, nextDue); err != nil {
			return err
		}
	}
	// Warn a borrower who cannot cover the next instalment.
	if next := l.Next(); next > 0 && pu.total() < next+l.FeesDue {
		return h.loanEvent(ctx, tx, meta, "due", *l, product, next, pu.total(), nextDue)
	}
	return nil
}

// productName is a product's authored name, its code when the content
// dropped it.
func productName(def content.FinanceDef, code string) string {
	if d, ok := def.Loan(code); ok {
		return d.Name
	}
	return code
}

// defaultLoan defaults a loan: a mortgage's property goes back to its city,
// which pays the bank what it recovers; what is left of the principal is
// written off; the record remembers.
func (h *FinanceHandler) defaultLoan(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	l *application.Loan, product content.LoanDef, period int64, now time.Time,
) error {
	outstanding := l.Principal - l.PrincipalPaid
	def, _ := snap.Finance()
	var seized *application.Property
	if l.PropertyID != "" {
		pr, err := tx.Property().ByID(ctx, l.PropertyID, true)
		if err != nil {
			return err
		}
		if pr.Status == application.PropertyOwned && pr.OwnerID == l.PlayerID {
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, pr.CityID)
			if err != nil {
				return err
			}
			bank, err := tx.Ledger().AccountFor(ctx, application.AccountNationalBank, l.CountryID)
			if err != nil {
				return err
			}
			recovered := finance.Recovery(outstanding, pr.Value, def.Bank.RecoveryBPS, treasury.Balance.Minor())
			if _, err := move(ctx, tx, treasury.ID, bank.ID, recovered, application.ReasonLoanRecovery,
				application.LoanReference, l.ID, "", now); err != nil {
				return err
			}
			l.Recovered = recovered
			if err := repossessProperty(ctx, tx, pr, now); err != nil {
				return err
			}
			if err := tx.Property().Save(ctx, *pr); err != nil {
				return err
			}
			seized = pr
		}
	}
	l.WrittenOff = outstanding - l.Recovered
	l.FeesDue = 0
	l.Status, l.ClosedAt = application.LoanDefaulted, &now
	if err := tx.Finance().SaveLoan(ctx, *l); err != nil {
		return err
	}
	if _, err := tx.Finance().AddCreditEvent(ctx, application.CreditEvent{ID: h.ids.NewID(), PlayerID: l.PlayerID,
		LoanID: l.ID, Kind: application.CreditDefault, PeriodNo: period, At: now}); err != nil {
		return err
	}
	payload := map[string]any{"kind": "defaulted", "player_id": l.PlayerID, "no": l.No, "product": l.Product,
		"product_name": product.Name, "amount": l.WrittenOff, "other": l.Recovered}
	if seized != nil {
		line, err := h.propertyLine(ctx, snap, *seized)
		if err != nil {
			return err
		}
		payload["property_no"], payload["property_type"], payload["property_type_name"] = line.No, line.Type.Code, line.Type.Name
		payload["city_code"], payload["city_name"], payload["value"] = line.City.Code, line.City.Name, line.Value
	}
	return appendDomainEvent(ctx, tx, meta, "loan", "defaulted", l.ID, payload)
}

// loanEvent tells a borrower about their loan: an instalment due they
// cannot cover, one missed, the loan repaid.
func (h *FinanceHandler) loanEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, kind string,
	l application.Loan, product content.LoanDef, amount, other int64, at time.Time,
) error {
	payload := map[string]any{"kind": kind, "player_id": l.PlayerID, "no": l.No, "product": l.Product,
		"product_name": product.Name, "amount": amount, "other": other, "count": l.Arrears}
	if !at.IsZero() {
		payload["at"] = at.UTC()
	}
	return appendDomainEvent(ctx, tx, meta, "loan", kind, l.ID, payload)
}
