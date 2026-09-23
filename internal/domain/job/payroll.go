package job

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Sentinel errors for running a payroll.
var (
	// ErrInvalidPeriod means a pay period is unset or does not move forward.
	ErrInvalidPeriod = errors.New("job: invalid pay period")

	// ErrInvalidTaxRate means a withholding rate is outside 0..10000 bps.
	ErrInvalidTaxRate = errors.New("job: tax rate must be within 0..10000 bps")

	// ErrInvalidEmployee means a payroll line is unusable: no id, a negative
	// amount, or an id that appears twice. A duplicate is refused rather than
	// merged, because the payrolls table allows one payment per employee per
	// period and a duplicate is exactly the double payment that rule stops.
	ErrInvalidEmployee = errors.New("job: invalid payroll employee")
)

// Period is a pay period, half-open: [Start, End).
type Period struct {
	Start, End time.Time
}

// Employee is one line of a payroll: who, on what salary, over which part of
// the period they were employed.
type Employee struct {
	ID string
	// Salary is the pay for one whole period, in minor units.
	Salary money.Amount
	// HiredAt is when employment began. It may be before the period.
	HiredAt time.Time
	// LeftAt is when employment ended; zero means still employed.
	LeftAt time.Time
	// ShiftPay is pay already earned by shifts in this period (the Pay from
	// Work), settled in full alongside the salary.
	ShiftPay money.Amount
}

// Payment is what one employee is paid for a period. Gross is always exactly
// Net + Tax: nothing is created or lost between the employer's debit and the
// two credits it funds.
type Payment struct {
	EmployeeID string
	Gross      money.Amount
	Tax        money.Amount
	Net        money.Amount
}

// Totals sums a payroll: what the employer must fund, what the tax authority
// receives, and what employees receive.
type Totals struct {
	Gross, Tax, Net money.Amount
}

// Payroll computes every payment due for period, withholding income tax at
// taxRateBPS.
//
// The tax rate is a policy lever held by an office (ADR 0015) — the rate of
// the residence city, under ADR 0014 — so it is an argument, never a constant.
// Who funds the payroll (a company, or the city treasury for an office like
// police) is the caller's business; the arithmetic is the same.
//
// FORMULA, for each employee, all in integer minor units:
//
//	employed = overlap of [HiredAt, LeftAt) with [Start, End)
//	salary   = floor(Salary * employed / (End - Start))
//	gross    = salary + ShiftPay
//	tax      = floor(gross * taxRateBPS / 10000)
//	net      = gross - tax
//
// ROUNDING. Proration floors, so someone employed for part of a period is
// short by less than one minor unit; a whole period pays Salary exactly. Tax
// floors, so rounding on tax always favours the employee. Net is derived by
// subtraction rather than computed independently, which is what makes
// gross == net + tax hold exactly for every payment, not approximately.
//
// Products are formed in 128 bits, so no salary or duration overflows; a
// gross that would exceed int64 is refused with money.ErrOverflow.
//
// Payments come back in input order. An employee who earned nothing in the
// period (not employed during it, and no shift pay) gets no payment, because
// a zero-value row in an append-only ledger is noise.
func Payroll(employees []Employee, period Period, taxRateBPS int) ([]Payment, error) {
	if period.Start.IsZero() || period.End.IsZero() || !period.End.After(period.Start) {
		return nil, fmt.Errorf("%w: %s to %s", ErrInvalidPeriod, period.Start, period.End)
	}
	if taxRateBPS < 0 || taxRateBPS > bpsWhole {
		return nil, fmt.Errorf("%w: %d", ErrInvalidTaxRate, taxRateBPS)
	}
	length := int64(period.End.Sub(period.Start))

	seen := make(map[string]bool, len(employees))
	var out []Payment
	for _, e := range employees {
		if e.ID == "" || seen[e.ID] || e.Salary.IsNegative() || e.ShiftPay.IsNegative() {
			return nil, fmt.Errorf("%w: %q", ErrInvalidEmployee, e.ID)
		}
		seen[e.ID] = true

		salary, err := mulDiv(e.Salary.Minor(), int64(employedDuring(e, period)), length)
		if err != nil {
			return nil, err
		}
		gross, err := money.FromMinor(salary).Add(e.ShiftPay)
		if err != nil {
			return nil, err
		}
		if gross.IsZero() {
			continue
		}
		taxMinor, err := mulDiv(gross.Minor(), int64(taxRateBPS), bpsWhole)
		if err != nil {
			return nil, err
		}
		tax := money.FromMinor(taxMinor)
		net, err := gross.Sub(tax)
		if err != nil {
			return nil, err
		}
		out = append(out, Payment{EmployeeID: e.ID, Gross: gross, Tax: tax, Net: net})
	}
	return out, nil
}

// employedDuring is the overlap of an employee's employment with a period.
// It is never negative and never longer than the period, which is what keeps
// proration at or below the full salary.
func employedDuring(e Employee, p Period) time.Duration {
	from := p.Start
	if e.HiredAt.After(from) {
		from = e.HiredAt
	}
	to := p.End
	if !e.LeftAt.IsZero() && e.LeftAt.Before(to) {
		to = e.LeftAt
	}
	if !to.After(from) {
		return 0
	}
	return to.Sub(from)
}

// Total sums a payroll, failing with money.ErrOverflow rather than wrapping.
// Totals.Gross is what the payer's balance must cover before the payroll is
// posted: ADR 0014 forbids a treasury paying out more than it holds.
func Total(payments []Payment) (Totals, error) {
	var t Totals
	for _, p := range payments {
		var err error
		if t.Gross, err = t.Gross.Add(p.Gross); err != nil {
			return Totals{}, err
		}
		if t.Tax, err = t.Tax.Add(p.Tax); err != nil {
			return Totals{}, err
		}
		if t.Net, err = t.Net.Add(p.Net); err != nil {
			return Totals{}, err
		}
	}
	return t, nil
}
