package payment

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Method{"cash": Cash, "CARD": Card, " card ": Card} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := Parse("cheque"); !errors.Is(err, ErrUnknownMethod) {
		t.Errorf("Parse(cheque) = %v", err)
	}
	if Method("cheque").Valid() || !Cash.Valid() || !Card.Valid() {
		t.Error("Valid disagrees with the closed set")
	}
}

func TestAcceptsDefaultsToEverything(t *testing.T) {
	var none Accepts
	if got := none.Normalize(); len(got) != 2 || got[0] != Cash || got[1] != Card {
		t.Fatalf("Normalize(nil) = %v", got)
	}
	if !none.Has(Card) || !none.Has(Cash) {
		t.Fatal("an unstated service takes every method")
	}
	cashOnly := Accepts{Cash}
	if cashOnly.Has(Card) {
		t.Fatal("a cash-only service takes cards")
	}
	if got := (Accepts{Card, Cash, Card}).Normalize(); len(got) != 2 || got[0] != Cash {
		t.Fatalf("Normalize keeps display order without repeats: %v", got)
	}
}

func TestParseAccepts(t *testing.T) {
	if a, err := ParseAccepts(nil); err != nil || a != nil {
		t.Fatalf("ParseAccepts(nil) = %v, %v", a, err)
	}
	if _, err := ParseAccepts([]string{"cash", "cash"}); err == nil {
		t.Fatal("a repeated method is accepted")
	}
	if _, err := ParseAccepts([]string{"cash", "coins"}); !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("an unknown method: %v", err)
	}
}

func TestPlan(t *testing.T) {
	w := Wallet{Cash: money.FromMinor(105), Bank: money.FromMinor(5000)}
	fee := money.FromMinor(2000)

	p := PlanFor(fee, nil, w)
	if !p.Payable() || p.CanPay(Cash) || !p.CanPay(Card) {
		t.Fatalf("the bank pays what the cash cannot: %+v", p)
	}
	if p.Check(Cash) != ShortFunds || p.Check(Card) != ShortNone {
		t.Fatalf("Check = %v, %v", p.Check(Cash), p.Check(Card))
	}

	cashOnly := PlanFor(fee, Accepts{Cash}, w)
	if cashOnly.Payable() || cashOnly.Check(Card) != ShortNotAccepted {
		t.Fatalf("a cash-only service is paid by card: %+v", cashOnly)
	}

	both := PlanFor(money.FromMinor(100), nil, w)
	if len(both.Usable) != 2 {
		t.Fatalf("both purses cover a small sum: %+v", both)
	}

	broke := PlanFor(money.FromMinor(9000), nil, w)
	if broke.Payable() {
		t.Fatalf("nothing covers it: %+v", broke)
	}

	free := PlanFor(money.FromMinor(0), nil, Wallet{})
	if len(free.Usable) != 2 {
		t.Fatalf("a free charge is payable any way: %+v", free)
	}
}
