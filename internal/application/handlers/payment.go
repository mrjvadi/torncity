package handlers

import (
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/presentation"
)

// Paying for a service, as every handler that charges a player does it
// (application.Wallet.Pay): the player picks cash or card on the price screen,
// the button carries the method as its last argument, and the handler takes
// the charge from that purse alone. A press without a method shows the price
// screen; a method the service does not take is refused; a method that does
// not cover the price answers with economy.PaymentDeclined.

// chosenMethod reads the method a press carries. ok is false when the press
// named none — the price screen should be shown. A word that is not a method
// is ErrPaymentNotAccepted: a button this game never drew.
func chosenMethod(raw string) (payment.Method, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	m, err := payment.Parse(raw)
	if err != nil {
		// The bare sentinel, so its own sentence is found (screens.Error
		// matches by identity).
		return "", false, application.ErrPaymentNotAccepted
	}
	return m, true, nil
}

// refOf is a way back written as an address and the arguments that follow it.
func refOf(parts []string) presentation.Ref {
	if len(parts) == 0 {
		return presentation.Ref{}
	}
	r := presentation.RefOfAddress(parts[0])
	r.Args = append(append([]string(nil), r.Args...), parts[1:]...)
	return r
}

// methodNames spells methods as a screen takes them.
func methodNames(ms []payment.Method) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(m))
	}
	return out
}

// paymentChoice is the price screen's view of a plan.
func paymentChoice(plan payment.Plan, w application.Wallet) presentation.PaymentChoice {
	return presentation.PaymentChoice{
		Amount:   plan.Amount.Minor(),
		Accepted: methodNames(plan.Accepted),
		Usable:   methodNames(plan.Usable),
		Cash:     w.Cash.Balance.Minor(),
		Bank:     w.Bank.Balance.Minor(),
	}
}

// paymentDeclined is the refusal of a charge nothing the player holds pays,
// with the way back to the price.
func paymentDeclined(plan payment.Plan, w application.Wallet, backLabel string, backAddr ...string) economy.PaymentDeclinedView {
	return economy.PaymentDeclinedView{
		Amount:    plan.Amount.Minor(),
		Cash:      w.Cash.Balance.Minor(),
		Bank:      w.Bank.Balance.Minor(),
		Accepted:  methodNames(plan.Accepted),
		BackLabel: backLabel,
		Back:      refOf(backAddr),
	}
}

// declinedPayment carries a payment refusal out of a unit of work, so the
// transaction rolls back with its idempotency key and the handler answers
// with economy.PaymentDeclined instead of an error.
type declinedPayment struct{ view economy.PaymentDeclinedView }

func (d *declinedPayment) Error() string { return "handlers: payment declined" }

// declined builds the refusal for a plan the chosen method cannot pay.
func declined(plan payment.Plan, w application.Wallet, backLabel string, backAddr ...string) *declinedPayment {
	return &declinedPayment{view: paymentDeclined(plan, w, backLabel, backAddr...)}
}

// asDeclined reports whether err is a payment refusal carried out of a unit
// of work, or the wallet's own refusal (a balance that moved between the read
// and the post), and returns its view. A ledger refusal carries no view, so
// its caller's fallback view is used.
func asDeclined(err error, fallback economy.PaymentDeclinedView) (economy.PaymentDeclinedView, bool) {
	var d *declinedPayment
	if stderrors.As(err, &d) {
		return d.view, true
	}
	if stderrors.Is(err, application.ErrPaymentDeclined) {
		return fallback, true
	}
	return economy.PaymentDeclinedView{}, false
}

// checkMethod refuses a method the plan cannot pay with: not accepted is a
// forged press (ErrPaymentNotAccepted); not covered is declined.
func checkMethod(plan payment.Plan, m payment.Method, w application.Wallet, backLabel string, backAddr ...string) error {
	switch plan.Check(m) {
	case payment.ShortNotAccepted:
		return application.ErrPaymentNotAccepted
	case payment.ShortFunds:
		return declined(plan, w, backLabel, backAddr...)
	}
	return nil
}
