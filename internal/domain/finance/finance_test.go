package finance

import (
	"testing"
	"time"
)

func creditRules() CreditRules {
	return CreditRules{Min: 300, Max: 850,
		Weights:           CreditWeights{Payment: 3500, Debt: 3000, History: 1500, Income: 1000, Worth: 500, NewCredit: 500},
		NeutralPaymentBPS: 6000, HistoryFull: 30 * 24 * time.Hour, IncomeFull: 20, WorthFull: 200_000,
		NewCreditStepBPS: 2500, MissedPoints: 40, DefaultPoints: 150}
}

func TestCreditScoreBoundsAndOrder(t *testing.T) {
	r := creditRules()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	fresh := r.Score(CreditHistory{})
	if fresh.Score < r.Min || fresh.Score > r.Max {
		t.Fatalf("a fresh score %d outside the range", fresh.Score)
	}
	good := r.Score(CreditHistory{OnTime: 20, Age: 60 * 24 * time.Hour, Shifts: 30, NetWorth: 500_000})
	if good.Score != r.Max {
		t.Fatalf("a perfect record scores %d, want %d", good.Score, r.Max)
	}
	missed := r.Score(CreditHistory{OnTime: 20, Missed: 1, Age: 60 * 24 * time.Hour, Shifts: 30, NetWorth: 500_000})
	if missed.Score >= good.Score {
		t.Fatalf("a missed instalment did not lower the score: %d >= %d", missed.Score, good.Score)
	}
	ruined := r.Score(CreditHistory{Missed: 10, Defaults: 3})
	if ruined.Score != r.Min {
		t.Fatalf("a ruined record scores %d, want the floor %d", ruined.Score, r.Min)
	}
	indebted := r.Score(CreditHistory{OnTime: 20, Age: 60 * 24 * time.Hour, Shifts: 30, NetWorth: 10_000, Owed: 90_000})
	if indebted.Score >= good.Score || indebted.DebtBPS != 1000 {
		t.Fatalf("heavy debt: score %d, debt factor %d", indebted.Score, indebted.DebtBPS)
	}
}

func TestBands(t *testing.T) {
	bands := []Band{{MinScore: 300, LimitBPS: 0}, {MinScore: 580, LimitBPS: 3000, PremiumBPS: 800},
		{MinScore: 740, LimitBPS: 10000}}
	if err := ValidateBands(bands, creditRules()); err != nil {
		t.Fatal(err)
	}
	if _, ok := BandFor(bands, 450); ok {
		t.Fatal("a poor score was offered a loan")
	}
	if b, ok := BandFor(bands, 600); !ok || b.LimitBPS != 3000 {
		t.Fatalf("600 → %+v %v", b, ok)
	}
	if b, _ := BandFor(bands, 850); b.LimitBPS != 10000 {
		t.Fatalf("850 → %+v", b)
	}
}

func TestScheduleAddsUpExactly(t *testing.T) {
	for _, terms := range []LoanTerms{
		{Principal: 100_000, RateBPS: 2100, Periods: 14, PeriodsPerYear: 52},
		{Principal: 7, RateBPS: 999, Periods: 3, PeriodsPerYear: 52},
		{Principal: 1_000_003, RateBPS: 0, Periods: 28, PeriodsPerYear: 365},
	} {
		s, err := terms.Schedule()
		if err != nil {
			t.Fatal(err)
		}
		var p, i int64
		for k := int64(1); k <= s.Periods; k++ {
			pk, ik := s.Instalment(k)
			p, i = p+pk, i+ik
		}
		if p != s.Principal || i != s.Interest {
			t.Fatalf("%+v: instalments add to %d + %d, schedule says %d + %d", terms, p, i, s.Principal, s.Interest)
		}
	}
	s, _ := LoanTerms{Principal: 100_000, RateBPS: 2600, Periods: 13, PeriodsPerYear: 52}.Schedule()
	// 100000 × 26% × 13/52 = 6500.
	if s.Interest != 6500 {
		t.Fatalf("interest %d, want 6500", s.Interest)
	}
}

func TestCollectPaysOldestFirstAndDefaults(t *testing.T) {
	s, _ := LoanTerms{Principal: 1000, RateBPS: 0, Periods: 4, PeriodsPerYear: 52}.Schedule()
	rules := LoanRules{LateFeeBPS: 1000, DefaultAfter: 3}
	st := LoanState{Schedule: s}
	c := Collect(st, 1, 5000, rules)
	if len(c.Instalments) != 1 || c.Missed || c.Taken() != 250 {
		t.Fatalf("an instalment covered: %+v", c)
	}
	st.Paid = 1
	c = Collect(st, 1, 100, rules)
	if !c.Missed || c.Arrears != 1 || c.NewFee != 25 || c.Taken() != 0 || c.Defaulted {
		t.Fatalf("a missed instalment: %+v", c)
	}
	st.Arrears, st.FeesDue = 1, 25
	c = Collect(st, 1, 520, rules)
	if len(c.Instalments) != 2 || c.Fees != 20 || c.FeesDue != 5 || c.Missed {
		t.Fatalf("catching up: %+v", c)
	}
	st = LoanState{Schedule: s, Paid: 1, Arrears: 2, FeesDue: 50}
	c = Collect(st, 1, 0, rules)
	if !c.Defaulted || c.Arrears != 3 {
		t.Fatalf("three in arrears must default: %+v", c)
	}
	st = LoanState{Schedule: s, Paid: 3}
	c = Collect(st, 1, 250, rules)
	if !c.Repaid {
		t.Fatalf("the last instalment repays: %+v", c)
	}
	p, i, f := Payoff(LoanState{Schedule: s, Paid: 1, FeesDue: 9})
	if p != 750 || i != 0 || f != 9 {
		t.Fatalf("payoff %d %d %d", p, i, f)
	}
}

func TestLendableKeepsTheReserve(t *testing.T) {
	if got := Lendable(100_000, 100_000, 2000); got != 60_000 {
		t.Fatalf("lendable %d, want 60000", got)
	}
	if got := Lendable(10_000, 100_000, 5000); got != 0 {
		t.Fatalf("an over-lent bank lends %d", got)
	}
	if got := Recovery(1000, 5000, 5000, 400); got != 400 {
		t.Fatalf("recovery %d, want what the payer holds", got)
	}
}

func TestClaimNeverExceedsTheFund(t *testing.T) {
	due, paid := Claim(1000, 8000, 500, 10_000)
	if due != 500 || paid != 500 {
		t.Fatalf("capped claim %d %d", due, paid)
	}
	due, paid = Claim(1000, 8000, 0, 300)
	if due != 800 || paid != 300 {
		t.Fatalf("a poor fund pays %d of %d", paid, due)
	}
}

func TestGoldWalkIsBoundedAndRepeatable(t *testing.T) {
	r := GoldRules{Min: 1000, Max: 5000, StepBPS: 300, DemandBPSPerKG: 100, DemandCapBPS: 500, SpreadBPS: 200}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	price := int64(2000)
	for p := uint64(1); p < 5000; p++ {
		next := r.Next(price, Roll(p), int64(p%7)*1000-3000)
		if next < r.Min || next > r.Max {
			t.Fatalf("price %d left its bounds", next)
		}
		if d := next - price; d*10000 > price*800+10000 || -d*10000 > price*800+10000 {
			t.Fatalf("a move of %d from %d is more than step and pull allow", d, price)
		}
		price = next
	}
	if Roll(42) != Roll(42) {
		t.Fatal("a roll is not repeatable")
	}
	buy, sell := r.Quote(2000)
	if buy != 2020 || sell != 1980 {
		t.Fatalf("quote %d / %d", buy, sell)
	}
}

func TestListingAndControl(t *testing.T) {
	r := ListingRules{MinAge: 72 * time.Hour, MinRevenue: 1000, MinFloatBPS: 1000, MaxFloatBPS: 4900, TakeoverBPS: 5001}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := r.Eligible(time.Hour, 5000, 0, 2000); got != ListTooYoung {
		t.Fatalf("young: %q", got)
	}
	if got := r.Eligible(100*time.Hour, 5000, 0, 5000); got != ListBadFloat {
		t.Fatalf("float: %q", got)
	}
	if got := r.Eligible(100*time.Hour, 5000, 0, 2000); got != ListCompliant {
		t.Fatalf("eligible: %q", got)
	}
	if Controls(500, 1000, 5001) || !Controls(501, 1000, 5001) {
		t.Fatal("control starts above half")
	}
}

func TestDividendBalances(t *testing.T) {
	d, err := SplitDividend(10_007, 1000, map[string]int64{"a": 600, "b": 399, "c": 1})
	if err != nil {
		t.Fatal(err)
	}
	if d.PerShare != 10 || d.Paid != 10_000 || d.Payments["a"] != 6000 || d.Payments["c"] != 10 {
		t.Fatalf("split %+v", d)
	}
	var sum int64
	for _, v := range d.Payments {
		sum += v
	}
	if sum != d.Paid {
		t.Fatalf("payments %d, paid %d", sum, d.Paid)
	}
	if _, err := SplitDividend(100, 1000, map[string]int64{"a": 999}); err == nil {
		t.Fatal("holdings that do not add up were split")
	}
}
