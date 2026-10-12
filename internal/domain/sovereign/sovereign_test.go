package sovereign

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/finance"
)

var bounds = Bounds{MaxInstalments: 24, MaxRateBPS: 3000, ServiceCapBPS: 3000, LenderReserveBPS: 1000}

func offer(principal int64) Offer {
	return Offer{Lender: "a", Borrower: "b", Terms: finance.LoanTerms{Principal: principal, RateBPS: 1000, Periods: 4, PeriodsPerYear: 52}}
}

func TestAnOfferIsCheckedAgainstTheLenderAndTheBorrower(t *testing.T) {
	if _, err := Check(offer(1000), bounds, 10_000, 0, 5_000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(offer(9_500), bounds, 10_000, 0, 50_000, 0); !errors.Is(err, ErrLender) {
		t.Fatalf("the lender must keep its reserve: %v", err)
	}
	if _, err := Check(offer(4000), bounds, 100_000, 0, 1_000, 0); !errors.Is(err, ErrService) {
		t.Fatalf("a borrower with 1000 a period cannot carry 1000+ a period: %v", err)
	}
	if _, err := Check(Offer{Lender: "a", Borrower: "a", Terms: offer(1).Terms}, bounds, 1_000, 0, 1_000, 0); !errors.Is(err, ErrSelf) {
		t.Fatal(err)
	}
	o := offer(1000)
	o.Terms.Periods = 25
	if _, err := Check(o, bounds, 10_000, 0, 5_000, 0); !errors.Is(err, ErrTerms) {
		t.Fatalf("too many instalments: %v", err)
	}
	o = offer(1000)
	o.Terms.RateBPS = 3001
	if _, err := Check(o, bounds, 10_000, 0, 5_000, 0); !errors.Is(err, ErrTerms) {
		t.Fatalf("too dear: %v", err)
	}
}

func TestADefaultComesAfterTheConfiguredArrears(t *testing.T) {
	sched, err := Check(offer(1000), bounds, 10_000, 0, 5_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	rules := finance.LoanRules{LateFeeBPS: 500, DefaultAfter: 3}
	state := finance.LoanState{Schedule: sched}
	var last Standing
	for period := 1; period <= 3; period++ {
		c := finance.Collect(state, 1, 0, rules) // the borrower pays nothing
		state.Arrears, state.FeesDue = c.Arrears, c.FeesDue
		last = StandingOf(c)
		if period < 3 && last != Late {
			t.Fatalf("period %d: standing %s, want late", period, last)
		}
		// nothing is ever taken beyond what the borrower holds
		if c.Taken() != 0 {
			t.Fatalf("a borrower with nothing paid %d", c.Taken())
		}
	}
	if last != Defaulted {
		t.Fatalf("three missed instalments default the loan, got %s", last)
	}
}

func TestAPaidLoanMovesNoMoneyBeyondItsTotal(t *testing.T) {
	sched, _ := Check(offer(1000), bounds, 10_000, 0, 5_000, 0)
	rules := finance.LoanRules{LateFeeBPS: 500, DefaultAfter: 3}
	state := finance.LoanState{Schedule: sched}
	var taken int64
	for k := 0; k < 4; k++ {
		c := finance.Collect(state, 1, 1_000_000, rules)
		taken += c.Taken()
		state.Paid += int64(len(c.Instalments))
		state.Arrears = c.Arrears
		if k == 3 && StandingOf(c) != Repaid {
			t.Fatalf("the last instalment repays: %s", StandingOf(c))
		}
	}
	if taken != sched.Total() {
		t.Fatalf("the borrower repays exactly principal plus interest: %d vs %d", taken, sched.Total())
	}
}

func TestARestructuringMayNotRaiseTheDebt(t *testing.T) {
	terms := finance.LoanTerms{Principal: 600, RateBPS: 500, Periods: 6, PeriodsPerYear: 52}
	if _, err := Restructure(800, terms, bounds); err != nil {
		t.Fatalf("a haircut with a longer term: %v", err)
	}
	terms.Principal = 801
	if _, err := Restructure(800, terms, bounds); !errors.Is(err, ErrRestructure) {
		t.Fatal(err)
	}
	if Sweep(50, 80) != 50 || Sweep(100, 80) != 80 || Sweep(-1, 5) != 0 {
		t.Fatal("a sweep never takes more than the stream or the instalment")
	}
}
