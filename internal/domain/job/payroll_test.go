package job

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

var (
	periodStart = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	period      = Period{Start: periodStart, End: periodStart.Add(30 * 24 * time.Hour)}
)

func TestPayroll(t *testing.T) {
	employees := []Employee{
		{ID: "full", Salary: money.FromMinor(30_000), HiredAt: periodStart.Add(-time.Hour)},
		{ID: "half", Salary: money.FromMinor(30_000), HiredAt: periodStart.Add(15 * 24 * time.Hour)},
		{ID: "left", Salary: money.FromMinor(30_000), HiredAt: periodStart.Add(-99 * time.Hour),
			LeftAt: periodStart.Add(10 * 24 * time.Hour)},
		{ID: "odd", Salary: money.FromMinor(10_001), HiredAt: periodStart.Add(20 * 24 * time.Hour)},
		{ID: "shifts", Salary: money.FromMinor(0), ShiftPay: money.FromMinor(777), HiredAt: periodStart},
		{ID: "gone", Salary: money.FromMinor(30_000), HiredAt: periodStart.Add(-99 * time.Hour),
			LeftAt: periodStart.Add(-time.Hour)},
		{ID: "future", Salary: money.FromMinor(30_000), HiredAt: period.End},
	}
	got, err := Payroll(employees, period, 1_000) // 10%
	if err != nil {
		t.Fatalf("Payroll() = %v", err)
	}
	want := []Payment{
		{EmployeeID: "full", Gross: money.FromMinor(30_000), Tax: money.FromMinor(3_000), Net: money.FromMinor(27_000)},
		{EmployeeID: "half", Gross: money.FromMinor(15_000), Tax: money.FromMinor(1_500), Net: money.FromMinor(13_500)},
		{EmployeeID: "left", Gross: money.FromMinor(10_000), Tax: money.FromMinor(1_000), Net: money.FromMinor(9_000)},
		// floor(10001 * 10/30) = floor(3333.67) = 3333; tax floor(333.3) = 333.
		{EmployeeID: "odd", Gross: money.FromMinor(3_333), Tax: money.FromMinor(333), Net: money.FromMinor(3_000)},
		// floor(777 * 0.1) = 77: rounding on tax favours the employee.
		{EmployeeID: "shifts", Gross: money.FromMinor(777), Tax: money.FromMinor(77), Net: money.FromMinor(700)},
	}
	if len(got) != len(want) {
		t.Fatalf("Payroll() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("payment %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	tot, err := Total(got)
	if err != nil {
		t.Fatalf("Total() = %v", err)
	}
	if tot != (Totals{Gross: money.FromMinor(59_110), Tax: money.FromMinor(5_910), Net: money.FromMinor(53_200)}) {
		t.Errorf("Total() = %+v", tot)
	}
}

func TestPayrollTaxRateEdges(t *testing.T) {
	emp := []Employee{{ID: "a", Salary: money.FromMinor(12_345), HiredAt: periodStart}}
	for _, tt := range []struct {
		bps      int
		tax, net int64
	}{
		{0, 0, 12_345},
		{10_000, 12_345, 0},
		{1, 1, 12_344},
	} {
		got, err := Payroll(emp, period, tt.bps)
		if err != nil {
			t.Fatalf("Payroll(%d bps) = %v", tt.bps, err)
		}
		if got[0].Tax.Minor() != tt.tax || got[0].Net.Minor() != tt.net {
			t.Errorf("%d bps: tax %s net %s, want %d and %d", tt.bps, got[0].Tax, got[0].Net, tt.tax, tt.net)
		}
	}
}

func TestPayrollRefusals(t *testing.T) {
	ok := Employee{ID: "a", Salary: money.FromMinor(100), HiredAt: periodStart}
	tests := []struct {
		name      string
		employees []Employee
		period    Period
		bps       int
		want      error
	}{
		{"zero period", []Employee{ok}, Period{}, 0, ErrInvalidPeriod},
		{"backwards period", []Employee{ok}, Period{Start: period.End, End: period.Start}, 0, ErrInvalidPeriod},
		{"empty period", []Employee{ok}, Period{Start: periodStart, End: periodStart}, 0, ErrInvalidPeriod},
		{"negative tax", []Employee{ok}, period, -1, ErrInvalidTaxRate},
		{"tax above 100%", []Employee{ok}, period, 10_001, ErrInvalidTaxRate},
		{"empty id", []Employee{{Salary: money.FromMinor(1)}}, period, 0, ErrInvalidEmployee},
		{"duplicate id", []Employee{ok, ok}, period, 0, ErrInvalidEmployee},
		{"negative salary", []Employee{{ID: "a", Salary: money.FromMinor(-1)}}, period, 0, ErrInvalidEmployee},
		{"negative shift pay", []Employee{{ID: "a", ShiftPay: money.FromMinor(-1)}}, period, 0, ErrInvalidEmployee},
		{"gross overflows", []Employee{{ID: "a", Salary: money.FromMinor(math.MaxInt64), HiredAt: periodStart,
			ShiftPay: money.FromMinor(1)}}, period, 0, money.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Payroll(tt.employees, tt.period, tt.bps)
			if !errors.Is(err, tt.want) || got != nil {
				t.Fatalf("Payroll() = %v, %v; want nil, %v", got, err, tt.want)
			}
		})
	}
}

func TestPayrollHugeSalaryDoesNotOverflowProration(t *testing.T) {
	// MaxInt64 * (a 30-day duration in ns) is far beyond int64; the 128-bit
	// intermediate must still give the exact answer.
	half := Employee{ID: "a", Salary: money.FromMinor(math.MaxInt64), HiredAt: periodStart.Add(15 * 24 * time.Hour)}
	got, err := Payroll([]Employee{half}, period, 5_000)
	if err != nil {
		t.Fatalf("Payroll() = %v", err)
	}
	if got[0].Gross.Minor() != math.MaxInt64/2 {
		t.Errorf("gross = %s, want %d", got[0].Gross, int64(math.MaxInt64/2))
	}
	sum, err := got[0].Net.Add(got[0].Tax)
	if err != nil || sum != got[0].Gross {
		t.Errorf("net + tax = %s (%v), want %s", sum, err, got[0].Gross)
	}
}

func TestTotalOverflow(t *testing.T) {
	p := Payment{EmployeeID: "a", Gross: money.FromMinor(math.MaxInt64)}
	if _, err := Total([]Payment{p, p}); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("Total() = %v, want overflow", err)
	}
}

// TestPayrollConservation is the property the whole payroll exists to keep:
// for random employees, periods and tax rates, every payment and the totals
// satisfy gross == net + tax exactly, no payment is negative, and no salary
// line pays more than its full-period salary.
func TestPayrollConservation(t *testing.T) {
	rng := rand.New(rand.NewSource(20260923))
	for round := 0; round < 500; round++ {
		start := periodStart.Add(time.Duration(rng.Int63n(int64(365 * 24 * time.Hour))))
		p := Period{Start: start, End: start.Add(time.Duration(1 + rng.Int63n(int64(60*24*time.Hour))))}
		span := p.End.Sub(p.Start)
		bps := rng.Intn(bpsWhole + 1)

		n := rng.Intn(40)
		emps := make([]Employee, n)
		for i := range emps {
			// Salaries up to 1e14 so that the sum of up to 40 lines, and tax times
			// 10000 in the rounding check, stay inside int64.
			e := Employee{
				ID:       fmt.Sprintf("e%d", i),
				Salary:   money.FromMinor(rng.Int63n(100_000_000_000_000)),
				ShiftPay: money.FromMinor(rng.Int63n(1_000_000_000)),
				HiredAt:  p.Start.Add(time.Duration(rng.Int63n(int64(2*span))) - span),
			}
			if rng.Intn(3) == 0 {
				e.LeftAt = e.HiredAt.Add(time.Duration(rng.Int63n(int64(2 * span))))
			}
			emps[i] = e
		}

		pays, err := Payroll(emps, p, bps)
		if err != nil {
			t.Fatalf("round %d: Payroll() = %v", round, err)
		}
		byID := make(map[string]Employee, n)
		for _, e := range emps {
			byID[e.ID] = e
		}
		for _, pay := range pays {
			sum, err := pay.Net.Add(pay.Tax)
			if err != nil || sum != pay.Gross {
				t.Fatalf("round %d %s: net %s + tax %s != gross %s", round, pay.EmployeeID, pay.Net, pay.Tax, pay.Gross)
			}
			if pay.Gross.IsNegative() || pay.Tax.IsNegative() || pay.Net.IsNegative() || pay.Gross.IsZero() {
				t.Fatalf("round %d %s: bad payment %+v", round, pay.EmployeeID, pay)
			}
			e := byID[pay.EmployeeID]
			if pay.Gross.Minor()-e.ShiftPay.Minor() > e.Salary.Minor() {
				t.Fatalf("round %d %s: prorated salary above full salary", round, pay.EmployeeID)
			}
			if pay.Tax.Minor()*bpsWhole > pay.Gross.Minor()*int64(bps) {
				t.Fatalf("round %d %s: tax rounded against the employee", round, pay.EmployeeID)
			}
		}
		tot, err := Total(pays)
		if err != nil {
			t.Fatalf("round %d: Total() = %v", round, err)
		}
		sum, err := tot.Net.Add(tot.Tax)
		if err != nil || sum != tot.Gross {
			t.Fatalf("round %d: totals net %s + tax %s != gross %s", round, tot.Net, tot.Tax, tot.Gross)
		}
	}
}
