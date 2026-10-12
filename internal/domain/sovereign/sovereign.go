// Package sovereign is the pure rules of loans between polities (docs/adr/0076 11): what a lender may lend, what a borrower can carry,
// and where a loan stands when instalments go unpaid. The schedule and the collection are those of ADR 0026 (internal/domain/finance).
// No clock, no storage.
package sovereign

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/finance"
)

// Sentinel errors.
var (
	// ErrSelf means a polity lending to itself.
	ErrSelf = errors.New("sovereign: a polity cannot lend to itself")
	// ErrTerms means terms outside the bounds.
	ErrTerms = errors.New("sovereign: terms outside the bounds")
	// ErrLender means a lender that does not hold what it would lend.
	ErrLender = errors.New("sovereign: the lender does not hold that much to lend")
	// ErrService means a borrower that could not carry the debt service.
	ErrService = errors.New("sovereign: the debt service is more than the borrower can carry")
	// ErrRestructure means a restructuring that gives the lender more than the debt.
	ErrRestructure = errors.New("sovereign: a restructuring may not raise the debt")
)

// Bounds are the limits of a loan, from the config.
type Bounds struct {
	MaxInstalments int64
	MaxRateBPS     int64
	// ServiceCapBPS is the most of a period's income the debt service may be.
	ServiceCapBPS int64
	// LenderReserveBPS is the reserve the lender keeps of everything it owns.
	LenderReserveBPS int64
}

// Offer is what a lender puts to a borrower.
type Offer struct {
	Lender, Borrower string
	Terms            finance.LoanTerms
}

// Check works out the schedule of an offer and checks it against the bounds, what the lender may lend (balance, outstanding principal it
// has out already) and what the borrower can carry (its income a period and the debt service it already pays).
func Check(o Offer, b Bounds, lenderBalance, lenderOutstanding, borrowerIncome, borrowerService int64) (finance.Schedule, error) {
	if o.Lender == "" || o.Borrower == "" || o.Lender == o.Borrower {
		return finance.Schedule{}, ErrSelf
	}
	t := o.Terms
	if t.Principal < 1 || t.Periods < 1 || t.Periods > b.MaxInstalments || t.RateBPS < 0 || t.RateBPS > b.MaxRateBPS {
		return finance.Schedule{}, ErrTerms
	}
	sched, err := t.Schedule()
	if err != nil {
		return finance.Schedule{}, fmt.Errorf("%w: %v", ErrTerms, err)
	}
	if t.Principal > finance.Lendable(lenderBalance, lenderOutstanding, b.LenderReserveBPS) {
		return finance.Schedule{}, ErrLender
	}
	if b.ServiceCapBPS > 0 {
		service := borrowerService + sched.Amount(1)
		if borrowerIncome <= 0 || service*finance.BPSWhole > borrowerIncome*b.ServiceCapBPS {
			return finance.Schedule{}, ErrService
		}
	}
	return sched, nil
}

// Standing is where a loan stands.
type Standing string

// The standings.
const (
	Current   Standing = "current"
	Late      Standing = "late"
	Defaulted Standing = "defaulted"
	Repaid    Standing = "repaid"
)

// StandingOf is the standing of a loan from its collection: repaid, defaulted (arrears reached the bound), late (any arrears) or current.
func StandingOf(c finance.Collection) Standing {
	switch {
	case c.Repaid:
		return Repaid
	case c.Defaulted:
		return Defaulted
	case c.Arrears > 0:
		return Late
	}
	return Current
}

// Outstanding is what a loan still owes in principal and interest, and its fees.
func Outstanding(s finance.Schedule, paid, feesDue int64) (principal, interest, fees int64) {
	principal, interest = s.Remaining(paid)
	return principal, interest, max(feesDue, 0)
}

// Restructure checks a new schedule replacing an old by agreement: the new principal may not exceed the principal still owed (a haircut,
// an extension or a rate change is fine; more debt is not).
func Restructure(owedPrincipal int64, t finance.LoanTerms, b Bounds) (finance.Schedule, error) {
	if t.Principal > owedPrincipal {
		return finance.Schedule{}, ErrRestructure
	}
	if t.Periods > b.MaxInstalments || t.RateBPS > b.MaxRateBPS {
		return finance.Schedule{}, ErrTerms
	}
	return t.Schedule()
}

// Sweep is how much of a revenue stream goes to the lender at source before the borrower's treasury sees it: the instalment due and
// unpaid, never more than the stream carries.
func Sweep(stream, due int64) int64 { return max(min(stream, due), 0) }
