//go:build integration

package tests

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// lastCallback is the address of a screen's first button whose address
// starts with prefix.
func lastCallback(t *testing.T, resp *presenter.Response, prefix string) string {
	t.Helper()
	if resp != nil && resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			for _, b := range row {
				if strings.HasPrefix(b.CallbackData, prefix) {
					return b.CallbackData
				}
			}
		}
	}
	t.Fatalf("no button %s… on the screen:\n%v", prefix, resp)
	return ""
}

// nonceOf is the one-time token a confirmation button carries last.
func nonceOf(t *testing.T, resp *presenter.Response, prefix string) string {
	parts := strings.Split(lastCallback(t, resp, prefix), ":")
	return parts[len(parts)-1]
}

var scoreText = regexp.MustCompile(`credit score: ([0-9,]+)`)

// score reads a player's credit score off the bank's screen.
func (w *financeWorld) score(p *application.Player) int64 {
	w.t.Helper()
	r, err := w.fin.Hub(testCtx(w.t), w.meta(p, "loan.hub"))
	resp := w.ok("the bank", r, err)
	m := scoreText.FindStringSubmatch(resp.Text)
	if m == nil {
		w.t.Fatalf("no credit score on the bank's screen:\n%s", resp.Text)
	}
	v, _ := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	return v
}

// borrow takes a loan: the terms, then the confirmation pressed twice.
func (w *financeWorld) borrow(p *application.Player, product string, amount, term int64, pledge string) application.Loan {
	w.t.Helper()
	ctx := testCtx(w.t)
	req := handlers.FinanceRequest{Product: product, Amount: strconv.FormatInt(amount, 10),
		Term: strconv.FormatInt(term, 10), Pledge: pledge}
	resp, err := w.fin.Take(ctx, w.meta(p, "loan.take"), req)
	w.ok("the loan's terms", resp, err, "Terms of the")
	req.Nonce = nonceOf(w.t, resp, "loan:take:")
	for i := 0; i < 2; i++ {
		resp, err = w.fin.Take(ctx, w.meta(p, "loan.take"), req)
		w.ok("taking the loan", resp, err)
	}
	if n := w.count(`SELECT count(*) FROM loans WHERE player_id = $1::uuid AND product = $2`, p.ID, product); n != 1 {
		w.t.Fatalf("a double press took %d loans", n)
	}
	var l application.Loan
	if err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		loans, err := tx.Finance().LoansOf(ctx, p.ID, 5)
		for _, x := range loans {
			if x.Product == product {
				l = x
			}
		}
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	return l
}

// loan reads a loan again.
func (w *financeWorld) loan(no int64) application.Loan {
	w.t.Helper()
	var l *application.Loan
	if err := w.uow.Do(testCtx(w.t), func(ctx context.Context, tx application.Tx) error {
		var err error
		l, err = tx.Finance().LoanByNo(ctx, no, false)
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	return *l
}

// drain moves everything a player holds to another, as a card payment.
func (w *financeWorld) drain(from, to *application.Player) {
	w.t.Helper()
	ledger := postgres.NewLedgerRepository(w.pool)
	for _, kind := range []application.AccountKind{application.AccountPlayerBank, application.AccountPlayerCash} {
		src, err := ledger.AccountFor(testCtx(w.t), kind, from.ID)
		if err != nil {
			w.t.Fatal(err)
		}
		dst, err := ledger.AccountFor(testCtx(w.t), application.AccountPlayerBank, to.ID)
		if err != nil {
			w.t.Fatal(err)
		}
		if src.Balance.Minor() > 0 {
			if _, err := ledger.Post(testCtx(w.t), transfer(src.ID, dst.ID, src.Balance.Minor(),
				application.ReasonCardPayment)); err != nil {
				w.t.Fatal(err)
			}
		}
	}
}

func TestLoansByScoreRepaidMissedAndDefaulted(t *testing.T) {
	w := newFinanceWorld(t, nil)
	ctx := testCtx(t)
	w.fundCountry(2_000_000)
	w.settle() // period 1: the treasury funds the bank.
	bankAcct := func() int64 { return w.balance(application.AccountNationalBank, w.country) }
	if bankAcct() <= 0 {
		t.Fatal("the national treasury did not fund its bank")
	}

	// A month in the game and money in the bank: a good score, a loan.
	p := w.resident(200_000)
	if s := w.score(p); s < 680 {
		t.Fatalf("a settled player with savings scores %d; want a band that lends", s)
	}
	loan := w.borrow(p, "personal", 10_000, 7, "")
	if got := w.balance(application.AccountPlayerBank, p.ID); got != 210_000 {
		t.Fatalf("the loan paid %d into the bank, want 10000", got-200_000)
	}

	// A player with nothing: a fair score, a loan, then everything spent.
	q := w.resident(0)
	qLoan := w.borrow(q, "personal", 10_000, 7, "")
	w.drain(q, p)
	before := w.score(q)

	// A mortgage against a home, then the owner broke.
	m := w.resident(200_000)
	home := w.home(m, 50_000)
	mLoan := w.borrow(m, "mortgage", 30_000, 28, strconv.FormatInt(home.No, 10))
	if mLoan.PropertyID != home.ID {
		t.Fatal("the mortgage is not secured by the home")
	}
	w.drain(m, p)

	w.settle() // the period the loans were taken in: nothing falls due yet.
	if l := w.loan(loan.No); l.PaidPeriods != 0 {
		t.Fatalf("an instalment was taken in the loan's first period (%d paid)", l.PaidPeriods)
	}
	bankBefore := bankAcct()
	w.settle() // the first instalment, delivered twice: collected once.
	l := w.loan(loan.No)
	inst := l.Schedule().Amount(1)
	if l.PaidPeriods != 1 || l.Arrears != 0 {
		t.Fatalf("after one period: %d paid, %d in arrears; want 1, 0", l.PaidPeriods, l.Arrears)
	}
	pr, in := l.Schedule().Instalment(1)
	if l.PrincipalPaid != pr || l.InterestPaid != in {
		t.Fatalf("the loan booked %d + %d, the schedule says %d + %d", l.PrincipalPaid, l.InterestPaid, pr, in)
	}
	if n := w.count(`SELECT count(*) FROM loan_periods WHERE loan_id = $1::uuid`, l.ID); n != 1 {
		t.Fatalf("%d loan periods recorded, want 1", n)
	}
	if got := bankAcct() - bankBefore; got < inst {
		t.Fatalf("the bank took in %d, less than the instalment %d", got, inst)
	}

	// The broke borrower missed: a late fee, a mark, a lower score.
	ql := w.loan(qLoan.No)
	if ql.Arrears != 1 || ql.FeesDue <= 0 || ql.MissedTotal != 1 {
		t.Fatalf("a missed instalment: arrears %d, fees %d, missed %d", ql.Arrears, ql.FeesDue, ql.MissedTotal)
	}
	if after := w.score(q); after >= before {
		t.Fatalf("a missed instalment left the score at %d (was %d)", after, before)
	}
	if n := w.count(`SELECT count(*) FROM outbox WHERE subject = 'game.event.loan.missed.v1' AND payload->>'player_id' = $1`,
		q.ID); n != 1 {
		t.Fatalf("%d missed notices, want 1", n)
	}

	// Three more missed instalments default the mortgage: the home goes
	// back to the city, which pays the bank what it recovers.
	treasury := w.balance(application.AccountCityTreasury, home.CityID)
	for i := 0; i < 3; i++ {
		w.settle()
	}
	ml := w.loan(mLoan.No)
	if ml.Status != application.LoanDefaulted {
		t.Fatalf("the mortgage is %s after four missed instalments", ml.Status)
	}
	want := finance.Recovery(ml.Principal, home.Value, w.def.Bank.RecoveryBPS, treasury)
	if ml.Recovered != want || ml.WrittenOff != ml.Principal-want {
		t.Fatalf("recovered %d and wrote off %d; want %d and %d", ml.Recovered, ml.WrittenOff, want, ml.Principal-want)
	}
	if n := w.count(`SELECT count(*) FROM properties WHERE id = $1::uuid AND status = 'repossessed' AND owner_player_id IS NULL`,
		home.ID); n != 1 {
		t.Fatal("the defaulted mortgage's home was not repossessed")
	}
	if n := w.count(`SELECT count(*) FROM credit_events WHERE loan_id = $1::uuid AND kind = 'default'`, ml.ID); n != 1 {
		t.Fatal("the default is not on the record")
	}
	_ = ctx
	w.verify()
}

// home gives a player a property the city sold them, paid for from their
// cash, as a purchase does.
func (w *financeWorld) home(p *application.Player, value int64) application.Property {
	w.t.Helper()
	ctx := testCtx(w.t)
	grantCash(w.t, w.pool, p.ID, value)
	pr := application.Property{ID: newUUID(w.t), CityID: w.city.ID, TypeCode: "studio", OwnerID: p.ID, Value: value, AcquiredAt: w.now(),
		ContentVersion: w.registry.Current().Version()}
	if err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		if pr, err = tx.Property().Create(ctx, pr); err != nil {
			return err
		}
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
		if err != nil {
			return err
		}
		city, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, w.city.ID)
		if err != nil {
			return err
		}
		t := transfer(cash.ID, city.ID, value, application.ReasonPropertyPurchase)
		t.ReferenceType, t.ReferenceID = application.PropertyReference, pr.ID
		_, err = tx.Ledger().Post(ctx, t)
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	// After finance's rows, before the player's: cleanups run last first.
	w.t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := w.pool.Raw().Exec(c, `DELETE FROM properties WHERE id = $1::uuid`, pr.ID); err != nil {
			w.t.Errorf("cleanup: the home: %v", err)
		}
	})
	w.t.Cleanup(w.purge)
	return pr
}
