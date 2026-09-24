package company

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func m(v int64) money.Amount { return money.FromMinor(v) }

func TestAvailableKeepsPromisedWages(t *testing.T) {
	b := Books{Balance: m(1000), Reserved: m(300)}
	if b.Available().Minor() != 700 {
		t.Fatalf("available %s", b.Available())
	}
	if err := b.CanReserve(m(700)); err != nil {
		t.Errorf("reserve within available refused: %v", err)
	}
	var short Shortfall
	if err := b.CanReserve(m(701)); !errors.As(err, &short) || short.Available.Minor() != 700 {
		t.Errorf("reserve beyond available: %v", err)
	}
	if (Books{Balance: m(10), Reserved: m(50)}).Available().Minor() != 0 {
		t.Error("available went negative")
	}
}

func TestWithdraw(t *testing.T) {
	w, err := Withdraw(Books{Balance: m(1000), Reserved: m(200)}, m(800), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if w.Gross.Minor() != 800 || w.Tax.Minor() != 80 || w.Net.Minor() != 720 {
		t.Fatalf("withdrawal %+v", w)
	}
	w, _ = Withdraw(Books{Balance: m(1000)}, m(15), 1000)
	if w.Tax.Minor() != 1 || w.Net.Minor() != 14 {
		t.Errorf("tax floors: %+v", w)
	}
	if _, err := Withdraw(Books{Balance: m(1000), Reserved: m(200)}, m(801), 0); !errors.Is(err, ErrNotEnoughAvailable) {
		t.Errorf("beyond available: %v", err)
	}
	if _, err := Withdraw(Books{Balance: m(1000), Debt: m(1)}, m(10), 0); !errors.Is(err, ErrInDebt) {
		t.Errorf("in debt: %v", err)
	}
	if _, err := Withdraw(Books{Balance: m(1000)}, m(0), 0); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("zero: %v", err)
	}
}

func TestChargeUpkeep(t *testing.T) {
	u, err := ChargeUpkeep(Books{Balance: m(1000), Reserved: m(100)}, m(500), 0, 3)
	if err != nil || u.Paid.Minor() != 500 || !u.Debt.IsZero() || u.Arrears != 0 || u.Insolvent {
		t.Fatalf("paid in full: %+v %v", u, err)
	}
	// Only the available money pays: the promised wages are untouched.
	u, _ = ChargeUpkeep(Books{Balance: m(400), Reserved: m(100), Debt: m(50)}, m(500), 1, 3)
	if u.Due.Minor() != 550 || u.Paid.Minor() != 300 || u.Debt.Minor() != 250 || u.Arrears != 2 || u.Insolvent {
		t.Fatalf("partly paid: %+v", u)
	}
	u, _ = ChargeUpkeep(Books{Balance: m(0), Debt: m(250)}, m(500), 2, 3)
	if !u.Insolvent || u.Arrears != 3 {
		t.Fatalf("third period in debt is insolvent: %+v", u)
	}
	u, _ = ChargeUpkeep(Books{Balance: m(5000), Debt: m(750)}, m(500), 2, 3)
	if u.Paid.Minor() != 1250 || !u.Debt.IsZero() || u.Arrears != 0 {
		t.Fatalf("debt cleared: %+v", u)
	}
}

func TestClose(t *testing.T) {
	c, err := Close(Books{Balance: m(1000), Debt: m(200)}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if c.DebtPaid.Minor() != 200 || !c.WrittenOff.IsZero() || c.Payout.Gross.Minor() != 800 || c.Payout.Tax.Minor() != 80 {
		t.Fatalf("closing %+v", c)
	}
	c, _ = Close(Books{Balance: m(100), Debt: m(300)}, 1000)
	if c.DebtPaid.Minor() != 100 || c.WrittenOff.Minor() != 200 || !c.Payout.Gross.IsZero() {
		t.Fatalf("insolvent closing %+v", c)
	}
	if _, err := Close(Books{Balance: m(100), Reserved: m(10)}, 0); !errors.Is(err, ErrShiftsRunning) {
		t.Errorf("running shifts: %v", err)
	}
}

func TestProrate(t *testing.T) {
	if v, _ := Prorate(m(500), 5000); v.Minor() != 250 {
		t.Errorf("half: %s", v)
	}
	if v, _ := Prorate(m(500), 20000); v.Minor() != 500 {
		t.Errorf("clamped: %s", v)
	}
}
