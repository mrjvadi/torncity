package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Paying: how a price screen offers the ways to pay one charge, and the one
// refusal every service shares when nothing the player holds covers it.
//
// A player pays from one of two purses — the cash they carry or their bank
// card (internal/domain/payment). A price screen shows one button per method
// the service accepts AND that covers the price; when only one does, a
// one-line note says why the other is missing. When none does, the screen is
// PaymentDeclined, which names both balances and is therefore private: in a
// group the gateway sends it to the player's chat and leaves a neutral line.
// A shared screen never shows a balance.

// Payment methods, as the core spells them (internal/domain/payment). They
// travel in a button's address as its last argument.
const (
	MethodCash = "cash"
	MethodCard = "card"
)

// PaymentChoice is what a price screen needs to offer the ways to pay.
type PaymentChoice struct {
	// Amount is the price, in minor units.
	Amount int64
	// Accepted are the methods the service takes, in display order.
	Accepted []string
	// Usable are the accepted methods that cover Amount.
	Usable []string
	// Cash and Bank are the player's balances. Shown only on a screen
	// that is not shared.
	Cash, Bank int64
}

func (p PaymentChoice) accepts(m string) bool { return hasMethod(p.Accepted, m) }

func (p PaymentChoice) usable(m string) bool { return hasMethod(p.Usable, m) }

func hasMethod(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// paymentButtons adds one row with a button per usable method. addr builds
// each button's address from the method; the method is its last part, so a
// press says which purse pays and nothing else.
func (c Context) paymentButtons(kb *keyboards.Builder, p PaymentChoice, addr func(method string) []string) {
	var row []presenter.Button
	for _, m := range p.Usable {
		label := c.T("payment.button."+m, map[string]any{"amount": FormatMoney(c, p.Amount)})
		if btn, ok := keyboards.Button(label, addr(m)...); ok {
			row = append(row, btn)
		}
	}
	if len(row) > 0 {
		kb.Row(row...)
	}
}

// paymentNote is the one line under a price saying why a method is not
// offered, and — only in the player's own chat — what they hold. Empty when
// every method is offered on a shared screen.
func (c Context) paymentNote(p PaymentChoice) string {
	var lines []string
	switch {
	case len(p.Accepted) == 1 && p.accepts(MethodCash):
		lines = append(lines, c.T("payment.cash_only", nil))
	case len(p.Accepted) == 1 && p.accepts(MethodCard):
		lines = append(lines, c.T("payment.card_only", nil))
	case p.accepts(MethodCash) && p.accepts(MethodCard) && len(p.Usable) == 1 && p.usable(MethodCard):
		lines = append(lines, c.T("payment.only_card", nil))
	case p.accepts(MethodCash) && p.accepts(MethodCard) && len(p.Usable) == 1 && p.usable(MethodCash):
		lines = append(lines, c.T("payment.only_cash", nil))
	}
	if !c.Shared {
		lines = append(lines, c.T("payment.balances", map[string]any{
			"cash": FormatMoney(c, p.Cash), "bank": FormatMoney(c, p.Bank),
		}))
	}
	return body(lines...)
}

// PaymentDeclinedView is a charge nothing the player holds can pay.
type PaymentDeclinedView struct {
	Amount     int64
	Cash, Bank int64
	// Accepted are the methods the service takes; a service taking one
	// method says so, since money in the other purse cannot help.
	Accepted []string
	// BackLabel is the catalogue key of the way back and BackAddr its
	// address: the screen the price was on.
	BackLabel string
	BackAddr  []string
}

// PaymentDeclined renders a refused charge: what it costs, both balances and
// the way back. It names the player's money, so it is private.
func PaymentDeclined(c Context, v PaymentDeclinedView) *presenter.Response {
	return c.withView(renderPaymentDeclined(c, v), ScreenPaymentDeclined, v)
}

func renderPaymentDeclined(c Context, v PaymentDeclinedView) *presenter.Response {
	lines := []string{
		c.T("payment.declined", map[string]any{
			"amount": FormatMoney(c, v.Amount),
			"cash":   FormatMoney(c, v.Cash),
			"bank":   FormatMoney(c, v.Bank),
		}),
	}
	switch {
	case len(v.Accepted) == 1 && v.Accepted[0] == MethodCash:
		lines = append(lines, c.T("payment.cash_only", nil))
	case len(v.Accepted) == 1 && v.Accepted[0] == MethodCard:
		lines = append(lines, c.T("payment.card_only", nil))
	}
	kb := keyboards.New()
	var row []presenter.Button
	if v.BackLabel != "" && len(v.BackAddr) > 0 {
		if btn, ok := keyboards.Button(c.T(v.BackLabel, nil), v.BackAddr...); ok {
			row = append(row, btn)
		}
	}
	if btn, ok := keyboards.Button(c.T("button.bank", nil), AddrBank); ok {
		row = append(row, btn)
	}
	kb.Row(row...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}
