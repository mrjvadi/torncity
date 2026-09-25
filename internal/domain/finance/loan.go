package finance

import (
	"fmt"
)

// A loan (docs/adr/0026 section 2) is lent at a yearly rate fixed when it is
// taken — the central bank's policy rate, the product's spread and the
// borrower's band premium — and repaid in equal instalments, one per finance
// period (a game day), for its term.
//
// FLAT (ADD-ON) INTEREST. The interest is worked out once on the whole
// principal for the whole term, rounded up, and spread over the instalments
// with the principal:
//
//	interest = ceil(principal × rate × periods ÷ (10000 × periods_per_year))
//	instalment k of n: principal ÷ n + interest ÷ n, the remainders on the last
//
// Everything is an integer and the instalments add up to principal +
// interest exactly, so what the ledger books as repaid principal and as
// interest is known to the unit for every instalment. A rate of 0 lends for
// free.

// LoanTerms are what a loan is taken on.
type LoanTerms struct {
	Principal int64
	// RateBPS is the yearly rate, basis points.
	RateBPS int64
	// Periods is the term: instalments, one per finance period.
	Periods int64
	// PeriodsPerYear is how many finance periods make the year the rate is
	// written per (content).
	PeriodsPerYear int64
}

// Schedule is a loan's repayment: its principal and interest over Periods
// instalments.
type Schedule struct {
	Principal, Interest int64
	Periods             int64
}

// Schedule works out the repayment.
func (t LoanTerms) Schedule() (Schedule, error) {
	if t.Principal < 1 || t.RateBPS < 0 || t.RateBPS > 100*BPSWhole || t.Periods < 1 || t.Periods > 10_000 ||
		t.PeriodsPerYear < 1 {
		return Schedule{}, fmt.Errorf("%w: loan terms %+v", ErrInvalid, t)
	}
	num, err := mulDiv(t.RateBPS, t.Periods, 1)
	if err != nil {
		return Schedule{}, err
	}
	interest, err := mulDivCeil(t.Principal, num, BPSWhole*t.PeriodsPerYear)
	if err != nil {
		return Schedule{}, err
	}
	if interest > (1<<62)-t.Principal {
		return Schedule{}, ErrArithmetic
	}
	return Schedule{Principal: t.Principal, Interest: interest, Periods: t.Periods}, nil
}

// Instalment is instalment k (1-based) of the schedule: its principal and
// its interest. Zero for a k outside 1..Periods.
func (s Schedule) Instalment(k int64) (principal, interest int64) {
	if k < 1 || k > s.Periods || s.Periods < 1 {
		return 0, 0
	}
	principal, interest = s.Principal/s.Periods, s.Interest/s.Periods
	if k == s.Periods {
		principal += s.Principal % s.Periods
		interest += s.Interest % s.Periods
	}
	return principal, interest
}

// Amount is instalment k in all.
func (s Schedule) Amount(k int64) int64 {
	p, i := s.Instalment(k)
	return p + i
}

// Total is everything the schedule repays.
func (s Schedule) Total() int64 { return s.Principal + s.Interest }

// Remaining is what is left after paid instalments: principal and interest.
func (s Schedule) Remaining(paid int64) (principal, interest int64) {
	for k := max(paid, 0) + 1; k <= s.Periods; k++ {
		p, i := s.Instalment(k)
		principal += p
		interest += i
	}
	return principal, interest
}

// LoanState is where a loan stands when a period comes due.
type LoanState struct {
	Schedule Schedule
	// Paid is the instalments paid so far; Arrears those due and unpaid.
	Paid, Arrears int64
	// FeesDue are late fees charged and not yet paid.
	FeesDue int64
}

// LoanRules are a product's collection rules.
type LoanRules struct {
	// LateFeeBPS of the instalment missed is charged as a late fee.
	LateFeeBPS int64
	// DefaultAfter instalments in arrears default the loan.
	DefaultAfter int64
}

// Paid is one instalment collected.
type Paid struct {
	K                   int64
	Principal, Interest int64
}

// Collection is what one period collects from a loan.
type Collection struct {
	// Instalments paid, oldest first.
	Instalments []Paid
	// Fees are late fees paid; NewFee the late fee charged this period.
	Fees, NewFee int64
	// Missed says an instalment due went unpaid; Arrears is how many are
	// now due and unpaid, FeesDue the late fees still owed.
	Missed  bool
	Arrears int64
	FeesDue int64
	// Defaulted: arrears reached DefaultAfter. Repaid: every instalment
	// and every fee is paid.
	Defaulted, Repaid bool
}

// Taken is the money the collection takes.
func (c Collection) Taken() int64 {
	t := c.Fees
	for _, p := range c.Instalments {
		t += p.Principal + p.Interest
	}
	return t
}

// Collect works out one period of a loan: dueNow instalments fall due (1 in
// a period the loan is repaid in, 0 in one that only chases arrears and
// fees), the oldest unpaid are paid first, whole, as far as available goes;
// then late fees, as far as the rest goes. An instalment due and unpaid is
// missed: a late fee is charged on it, and DefaultAfter instalments in
// arrears default the loan. Nothing is ever taken beyond available.
func Collect(s LoanState, dueNow int64, available int64, r LoanRules) Collection {
	c := Collection{FeesDue: s.FeesDue}
	unpaid := max(s.Schedule.Periods-s.Paid, 0)
	due := min(max(s.Arrears, 0)+max(dueNow, 0), unpaid)
	left := max(available, 0)
	k := s.Paid
	for n := int64(0); n < due; n++ {
		amount := s.Schedule.Amount(k + 1)
		if amount > left {
			break
		}
		p, i := s.Schedule.Instalment(k + 1)
		c.Instalments = append(c.Instalments, Paid{K: k + 1, Principal: p, Interest: i})
		left -= amount
		k++
	}
	c.Arrears = due - int64(len(c.Instalments))
	if c.Arrears == 0 && c.FeesDue > 0 {
		c.Fees = min(c.FeesDue, left)
		c.FeesDue -= c.Fees
	}
	if c.Arrears > 0 {
		c.Missed = true
		c.NewFee = OfBPS(s.Schedule.Amount(k+1), r.LateFeeBPS)
		c.FeesDue += c.NewFee
		c.Defaulted = r.DefaultAfter > 0 && c.Arrears >= r.DefaultAfter
	}
	c.Repaid = k >= s.Schedule.Periods && c.FeesDue == 0
	return c
}

// Payoff is everything left to settle a loan now: the instalments not yet
// paid, in full, and the late fees owed. Paying a loan off early saves
// nothing, so borrowing and repaying at once cannot buy a record.
func Payoff(s LoanState) (principal, interest, fees int64) {
	principal, interest = s.Schedule.Remaining(s.Paid)
	return principal, interest, max(s.FeesDue, 0)
}

// RateOf is a loan's yearly rate: the policy rate, the product's spread and
// the band's premium, never below zero.
func RateOf(policyBPS, spreadBPS, premiumBPS int64) int64 {
	return max(policyBPS+spreadBPS+premiumBPS, 0)
}

// PeriodInterest is what balance earns in one period at a yearly rate:
// floor(balance × rate ÷ (10000 × periods_per_year)).
func PeriodInterest(balance, rateBPS, periodsPerYear int64) int64 {
	if balance <= 0 || rateBPS <= 0 || periodsPerYear < 1 {
		return 0
	}
	v, err := mulDiv(balance, rateBPS, BPSWhole*periodsPerYear)
	if err != nil {
		return 0
	}
	return v
}

// Lendable is what a bank may still lend: what it holds less the reserve it
// must keep, reserveBPS of everything it owns (its money and the principal
// out on loan).
func Lendable(balance, outstanding, reserveBPS int64) int64 {
	reserve := OfBPS(max(balance, 0)+max(outstanding, 0), clamp(reserveBPS, 0, BPSWhole))
	return max(balance-reserve, 0)
}

// Recovery is what a bank gets back from collateral taken on a default: at
// most what the loan still owes in principal, recoveryBPS of the
// collateral's value, and what the payer holds.
func Recovery(outstanding, value, recoveryBPS, payer int64) int64 {
	return max(min(outstanding, OfBPS(value, recoveryBPS), payer), 0)
}
