package company

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Books is a company's money as the rules read it.
type Books struct {
	// Balance is the treasury account's balance.
	Balance money.Amount
	// Reserved is the wages promised to the shifts being worked for the
	// company now: each shift reserves its wage when it starts.
	Reserved money.Amount
	// Debt is upkeep owed and not yet paid.
	Debt money.Amount
}

// Available is what the company may spend: its balance less the wages it
// has promised, never below zero.
func (b Books) Available() money.Amount {
	v := b.Balance.Minor() - b.Reserved.Minor()
	if v < 0 {
		v = 0
	}
	return money.FromMinor(v)
}

// CanReserve refuses a shift whose wage the company cannot promise: a
// shift is only started when the money to pay it is there and set aside.
func (b Books) CanReserve(wage money.Amount) error {
	if wage.IsNegative() {
		return fmt.Errorf("%w: wage %s", ErrInvalidAmount, wage)
	}
	if avail := b.Available(); avail.Minor() < wage.Minor() {
		return Shortfall{Need: wage, Available: avail}
	}
	return nil
}

// Withdrawal is money taken out of a company to its owner: Gross leaves the
// treasury, Tax goes to the city (city.corporate_tax), Net to the owner.
// Gross is always exactly Net + Tax.
type Withdrawal struct {
	Gross, Tax, Net money.Amount
}

// Withdraw splits a withdrawal of amount at taxBPS. It is refused while the
// company owes upkeep (ErrInDebt: debts are paid before profits) and beyond
// its available money. Tax floors, so rounding favours the owner by under
// one minor unit.
func Withdraw(b Books, amount money.Amount, taxBPS int) (Withdrawal, error) {
	if amount.IsNegative() || amount.IsZero() {
		return Withdrawal{}, fmt.Errorf("%w: %s", ErrInvalidAmount, amount)
	}
	if !b.Debt.IsZero() {
		return Withdrawal{}, fmt.Errorf("%w: %s", ErrInDebt, b.Debt)
	}
	if avail := b.Available(); avail.Minor() < amount.Minor() {
		return Withdrawal{}, Shortfall{Need: amount, Available: avail}
	}
	return split(amount, taxBPS)
}

func split(gross money.Amount, taxBPS int) (Withdrawal, error) {
	tax, err := bps(gross, taxBPS)
	if err != nil {
		return Withdrawal{}, err
	}
	net, err := gross.Sub(tax)
	if err != nil {
		return Withdrawal{}, err
	}
	return Withdrawal{Gross: gross, Tax: tax, Net: net}, nil
}

// Upkeep is what one period's upkeep did to a company.
type Upkeep struct {
	// Due is this period's upkeep plus the debt carried in.
	Due money.Amount
	// Paid is what the treasury paid of it now.
	Paid money.Amount
	// Debt is what is still owed.
	Debt money.Amount
	// Arrears is how many settlements in a row have ended in debt.
	Arrears int
	// Insolvent means Arrears reached the grace allowed: the company is
	// dissolved.
	Insolvent bool
}

// ChargeUpkeep settles one period's upkeep.
//
// The company pays what it can of the period's upkeep and any debt carried
// in, from its AVAILABLE money only — the wages it has promised are never
// touched — and the rest is owed. A company that ends grace settlements in
// a row owing money is insolvent. A company that pays everything owes
// nothing and its arrears reset. grace below 1 is treated as 1.
func ChargeUpkeep(b Books, upkeep money.Amount, arrears, grace int) (Upkeep, error) {
	if upkeep.IsNegative() || b.Debt.IsNegative() {
		return Upkeep{}, fmt.Errorf("%w: upkeep %s, debt %s", ErrInvalidAmount, upkeep, b.Debt)
	}
	due, err := upkeep.Add(b.Debt)
	if err != nil {
		return Upkeep{}, err
	}
	paid := b.Available()
	if paid.Minor() > due.Minor() {
		paid = due
	}
	debt, err := due.Sub(paid)
	if err != nil {
		return Upkeep{}, err
	}
	out := Upkeep{Due: due, Paid: paid, Debt: debt}
	if !debt.IsZero() {
		out.Arrears = max(arrears, 0) + 1
	}
	out.Insolvent = out.Arrears >= max(grace, 1)
	return out, nil
}

// Closing is what closing a company does with its money: the debt it owes
// is paid first, as far as the treasury goes, and what is left goes to the
// owner, taxed as a withdrawal. WrittenOff is debt nothing was left to pay.
type Closing struct {
	DebtPaid   money.Amount
	WrittenOff money.Amount
	Payout     Withdrawal
}

// Close settles a company's money on closing. It is refused while shifts
// are being worked for it (ErrShiftsRunning): their wages are promised and
// are paid when they end.
func Close(b Books, taxBPS int) (Closing, error) {
	if !b.Reserved.IsZero() {
		return Closing{}, fmt.Errorf("%w: %s reserved", ErrShiftsRunning, b.Reserved)
	}
	if b.Balance.IsNegative() || b.Debt.IsNegative() {
		return Closing{}, fmt.Errorf("%w: balance %s, debt %s", ErrInvalidAmount, b.Balance, b.Debt)
	}
	paid := b.Debt
	if paid.Minor() > b.Balance.Minor() {
		paid = b.Balance
	}
	left, err := b.Balance.Sub(paid)
	if err != nil {
		return Closing{}, err
	}
	off, err := b.Debt.Sub(paid)
	if err != nil {
		return Closing{}, err
	}
	payout, err := split(left, taxBPS)
	if err != nil {
		return Closing{}, err
	}
	return Closing{DebtPaid: paid, WrittenOff: off, Payout: payout}, nil
}

// Prorate is amount scaled by the share of a period, in basis points,
// floored; a share outside 0..10000 is clamped.
func Prorate(amount money.Amount, shareBPS int) (money.Amount, error) {
	if amount.IsNegative() {
		return money.Amount{}, fmt.Errorf("%w: %s", ErrInvalidAmount, amount)
	}
	return bps(amount, min(max(shareBPS, 0), bpsWhole))
}
