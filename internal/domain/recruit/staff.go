package recruit

import "fmt"

// A hired specialist at each settlement of their company's city.

// Leave reasons: why a specialist stops working for a company.
const (
	// LeaveUnpaid: unpaid UnpaidPeriods settlements in a row.
	LeaveUnpaid = "unpaid"
	// LeavePoached: paid below the market UnderpaidPeriods settlements in a
	// row — another employer's better offer took them.
	LeavePoached = "poached"
	// LeaveContractEnd: the contract ran out and was not renewed.
	LeaveContractEnd = "contract_end"
	// LeaveDismissed: the company ended it.
	LeaveDismissed = "dismissed"
	// LeaveCompanyClosed: the company closed or was dissolved.
	LeaveCompanyClosed = "company_closed"
)

// StaffRules are how patient a specialist is.
type StaffRules struct {
	// UnderpaidBPS: paid below this share of the pay-to-market ratio they
	// accepted, a specialist is underpaid.
	UnderpaidBPS int64
	// UnderpaidPeriods and UnpaidPeriods are the settlements in a row a
	// specialist bears either before leaving.
	UnderpaidPeriods, UnpaidPeriods int
}

// Validate refuses rules that could not be applied.
func (r StaffRules) Validate() error {
	if r.UnderpaidBPS < 1 || r.UnderpaidBPS > BPS || r.UnderpaidPeriods < 1 || r.UnpaidPeriods < 1 {
		return fmt.Errorf("%w: staff rules %+v", ErrInvalid, r)
	}
	return nil
}

// Standing is a specialist's contract as it stands before a settlement.
type Standing struct {
	// Due is what one period costs the company: salary and housing.
	Due int64
	// AcceptedBPS is the pay-to-market ratio they took the job at.
	AcceptedBPS int64
	// Served and Term are the periods worked and the contract's length.
	Served, Term int
	// Expiring is a contract that ran out at the last settlement and has
	// not been renewed: this is its last period.
	Expiring                bool
	UnpaidRun, UnderpaidRun int
}

// Settlement is what one settlement does to a specialist.
type Settlement struct {
	// Pay is whether the period is paid now.
	Pay bool
	// Next is the standing after it.
	Next Standing
	// Leave is why they leave now, "" when they stay.
	Leave string
	// Completed is a contract completed at this settlement: its equity
	// vests, and the company is asked to renew.
	Completed bool
	// Underpaid is a specialist paid below the market now.
	Underpaid bool
}

// Settle is one settlement: canPay says the company's free money covers the
// period's due; expectedNow is what the market expects for them today.
//
// A paid period clears the run of unpaid ones; a period at or above the
// market clears the run of underpaid ones. A specialist unpaid or underpaid
// too long leaves — unpaid first — and so does one whose contract ran out
// at the last settlement unrenewed. One who completes their contract now
// stays one more period, for the company to renew.
func Settle(s Standing, r StaffRules, canPay bool, expectedNow int64) Settlement {
	out := Settlement{Pay: canPay, Next: s}
	n := &out.Next
	if canPay {
		n.UnpaidRun = 0
	} else {
		n.UnpaidRun++
	}
	nowBPS := RatioBPS(s.Due, expectedNow)
	if s.AcceptedBPS > 0 && nowBPS < Scale(s.AcceptedBPS, r.UnderpaidBPS) {
		out.Underpaid = true
		n.UnderpaidRun++
	} else {
		n.UnderpaidRun = 0
	}
	n.Served++
	switch {
	case n.UnpaidRun >= r.UnpaidPeriods:
		out.Leave = LeaveUnpaid
	case n.UnderpaidRun >= r.UnderpaidPeriods:
		out.Leave = LeavePoached
	case s.Expiring:
		out.Leave = LeaveContractEnd
	case n.Served >= n.Term:
		out.Completed = true
		n.Expiring = true
	}
	return out
}

// Renew starts a new contract of term periods for a specialist, paying at
// least what the market expects today at the ratio they first accepted:
// the due it returns is never below the old one.
func Renew(s Standing, term int, expectedNow int64) Standing {
	s.Served, s.Term, s.Expiring, s.UnderpaidRun = 0, max(term, 1), false, 0
	s.Due = max(s.Due, MarketDue(s.AcceptedBPS, expectedNow))
	return s
}

// MarketDue is the pay that meets today's market at the ratio accepted.
func MarketDue(acceptedBPS, expectedNow int64) int64 {
	return mulDiv(expectedNow, acceptedBPS, BPS)
}
