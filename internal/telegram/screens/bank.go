package screens

import (
	stderrors "errors"
	"strconv"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The bank's screens: the bank itself, paying another player, and the notice
// a payee receives.
//
// PRIVACY: every screen in this file shows a player's own money — balances,
// amounts, fees — except PayHelp. In a group chat they must be shown to their
// owner only. PaymentNotice goes to the payee's own chat with the bot.

// Bank callback addresses. Amounts travel in them as whole minor units and a
// payee as their PUBLIC code, never a record id: an address is a hint the core
// re-checks, and a code is short enough to leave room for an amount and a
// one-time token inside Telegram's 64 bytes.
const (
	AddrBank     = "bank:show"
	AddrDeposit  = "bank:deposit"
	AddrWithdraw = "bank:withdraw"
	AddrPay      = "bank:pay"
	AddrPaySend  = "bank:pay.send"
)

// Payment methods as they travel in an address and are read back by the
// handler. They are the domain's values (bank.MethodCash, bank.MethodCard),
// restated here because a screen does not import the domain's rules.
const (
	PayCash = "cash"
	PayCard = "card"
)

// AmountOption is one quick-amount button: the amount it moves and the
// one-time token that makes a second press of the same button a replay.
type AmountOption struct {
	Amount int64
	Nonce  string
}

// BankView is the bank screen.
type BankView struct {
	// CityCode and City are where the player is, or, on the road, empty.
	CityCode string
	City     string
	// Travelling and NoCity explain why the bank is closed.
	Travelling bool
	NoCity     bool

	Cash int64
	Bank int64

	// WithdrawalFeeBPS is the city's withdrawal fee, in basis points.
	WithdrawalFeeBPS int64

	Deposits    []AmountOption
	Withdrawals []AmountOption

	// Notice is the outcome of what the player just did, already worded
	// (see BankNotice); empty on a plain visit.
	Notice string
}

// Bank renders the bank: cash and bank balance, the city's withdrawal fee,
// and quick amounts to deposit and withdraw.
func Bank(c Context, v BankView) *presenter.Response {
	kb := keyboards.New()

	lines := []string{
		c.T("profile.cash", map[string]any{"cash": FormatMoney(c, v.Cash)}),
		c.T("profile.bank", map[string]any{"bank": FormatMoney(c, v.Bank)}),
	}

	var where, terms, hint string
	switch {
	case v.Travelling:
		where = c.T("bank.closed_travelling", nil)
	case v.NoCity:
		where = c.T("bank.closed_no_city", nil)
	default:
		city := c.CityName(v.CityCode, v.City)
		where = c.T("bank.branch", map[string]any{"city": city})
		if v.WithdrawalFeeBPS > 0 {
			terms = c.T("bank.withdrawal_fee", map[string]any{"percent": PercentFromBPS(int(v.WithdrawalFeeBPS))})
		} else {
			terms = c.T("bank.withdrawal_free", nil)
		}
		hint = c.T("bank.hint", nil)

		amountButtons(c, kb, "button.deposit", AddrDeposit, v.Deposits)
		amountButtons(c, kb, "button.withdraw", AddrWithdraw, v.Withdrawals)
	}

	if btn, ok := keyboards.Button(c.T("button.find_player", nil), AddrSearch); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrBank}))

	text := paragraphs(
		v.Notice,
		c.T("bank.title", nil),
		body(where, terms),
		body(lines...),
		hint,
		c.T("bank.pay_hint", nil),
	)
	return c.respond(text, kb.Build())
}

// amountButtons lays out quick amounts two to a row.
func amountButtons(c Context, kb *keyboards.Builder, labelKey, addr string, options []AmountOption) {
	buttons := make([]presenter.Button, 0, len(options))
	for _, o := range options {
		btn, ok := keyboards.Button(
			c.T(labelKey, map[string]any{"amount": FormatMoney(c, o.Amount)}),
			addr, strconv.FormatInt(o.Amount, 10), o.Nonce)
		if ok {
			buttons = append(buttons, btn)
		}
	}
	if len(buttons) > 0 {
		kb.Grid(2, buttons...)
	}
}

// BankNotice words the outcome of a deposit or a withdrawal for the top of
// the bank screen.
func BankNotice(c Context, deposited bool, amount, fee int64) string {
	switch {
	case deposited:
		return c.T("bank.deposited", map[string]any{"amount": FormatMoney(c, amount)})
	case fee > 0:
		return c.T("bank.withdrew_fee", map[string]any{
			"amount": FormatMoney(c, amount),
			"fee":    FormatMoney(c, fee),
		})
	}
	return c.T("bank.withdrew", map[string]any{"amount": FormatMoney(c, amount)})
}

// PayHelp explains how to pay a player, when /pay names nobody.
func PayHelp(c Context) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.find_player", nil), AddrSearch); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank}))
	return c.respond(c.T("pay.help", nil), kb.Build())
}

// PayView is the screen for paying one player.
type PayView struct {
	// PayeeName is the payee's display name, empty when they have none
	// worth showing; PayeeCode their public code, which addresses them.
	PayeeName string
	PayeeCode string

	// Together says the two are face to face, in CityCode/City, so cash can
	// change hands.
	Together bool
	CityCode string
	City     string

	// PayerCityCode and PayerCity are the city whose bank charges the card
	// fee; CardFeeBPS is that fee.
	PayerCityCode string
	PayerCity     string
	CardFeeBPS    int64

	Cash int64
	Bank int64

	CashOptions []AmountOption
	CardOptions []AmountOption

	// Notice explains a refusal that brought the player back here, already
	// worded; empty on a plain visit.
	Notice string
}

// Pay renders the payment screen: who is paid, which methods are open, and
// quick amounts for each. Cash is offered only when the two are together.
func Pay(c Context, v PayView) *presenter.Response {
	name := c.playerName(v.PayeeName)
	kb := keyboards.New()

	var where string
	if v.Together {
		where = c.T("pay.together", map[string]any{
			"player": name,
			"city":   c.CityName(v.CityCode, v.City),
		})
		payButtons(c, kb, "button.pay_cash", v.PayeeCode, PayCash, v.CashOptions)
	} else {
		where = c.T("pay.apart", map[string]any{"player": name})
	}
	payButtons(c, kb, "button.pay_card", v.PayeeCode, PayCard, v.CardOptions)

	var terms string
	payerCity := c.CityName(v.PayerCityCode, v.PayerCity)
	switch {
	case payerCity == "":
	case v.CardFeeBPS > 0:
		terms = c.T("pay.card_fee", map[string]any{
			"city":    payerCity,
			"percent": PercentFromBPS(int(v.CardFeeBPS)),
		})
	default:
		terms = c.T("pay.card_free", map[string]any{"city": payerCity})
	}

	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank, RefreshData: keyboards.Data(AddrPay, v.PayeeCode)}))

	text := paragraphs(
		v.Notice,
		c.T("pay.title", map[string]any{"player": name}),
		body(where, terms),
		body(
			c.T("profile.cash", map[string]any{"cash": FormatMoney(c, v.Cash)}),
			c.T("profile.bank", map[string]any{"bank": FormatMoney(c, v.Bank)}),
		),
		c.T("pay.hint", map[string]any{"code": v.PayeeCode}),
	)
	return c.respond(text, kb.Build())
}

// payButtons lays out quick amounts for one method, two to a row. Each opens
// the confirmation; none moves money on its own.
func payButtons(c Context, kb *keyboards.Builder, labelKey, code, method string, options []AmountOption) {
	buttons := make([]presenter.Button, 0, len(options))
	for _, o := range options {
		btn, ok := keyboards.Button(
			c.T(labelKey, map[string]any{"amount": FormatMoney(c, o.Amount)}),
			AddrPay, code, strconv.FormatInt(o.Amount, 10), method)
		if ok {
			buttons = append(buttons, btn)
		}
	}
	if len(buttons) > 0 {
		kb.Grid(2, buttons...)
	}
}

// PayConfirmView is the last look before money leaves.
type PayConfirmView struct {
	PayeeName string
	PayeeCode string
	Method    string
	Amount    int64
	Fee       int64
	Total     int64
	// Nonce makes the confirm button single-use: a second press is a
	// replay, not a second payment.
	Nonce string
}

// PayConfirm asks the player to confirm one payment, with its fee and the
// total that leaves their account.
func PayConfirm(c Context, v PayConfirmView) *presenter.Response {
	name := c.playerName(v.PayeeName)
	args := map[string]any{
		"player": name,
		"amount": FormatMoney(c, v.Amount),
		"fee":    FormatMoney(c, v.Fee),
		"total":  FormatMoney(c, v.Total),
	}
	var text string
	switch {
	case v.Method == PayCash:
		text = c.T("pay.confirm_cash", args)
	case v.Fee > 0:
		text = c.T("pay.confirm_card_fee", args)
	default:
		text = c.T("pay.confirm_card", args)
	}

	kb := keyboards.New()
	confirm, okConfirm := keyboards.Button(c.T("button.confirm", nil),
		AddrPaySend, v.PayeeCode, strconv.FormatInt(v.Amount, 10), v.Method, v.Nonce)
	cancel, okCancel := keyboards.Button(c.T("button.cancel", nil), AddrPay, v.PayeeCode)
	if okConfirm && okCancel {
		kb.Row(confirm, cancel)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank}))
	return c.respond(text, kb.Build())
}

// PaySentView is the outcome of a payment, for the payer.
type PaySentView struct {
	PayeeName string
	PayeeCode string
	Method    string
	Amount    int64
	Fee       int64
}

// PaySent confirms a payment to the payer.
func PaySent(c Context, v PaySentView) *presenter.Response {
	args := map[string]any{
		"player": c.playerName(v.PayeeName),
		"amount": FormatMoney(c, v.Amount),
		"fee":    FormatMoney(c, v.Fee),
	}
	var text string
	switch {
	case v.Method == PayCash:
		text = c.T("pay.sent_cash", args)
	case v.Fee > 0:
		text = c.T("pay.sent_card_fee", args)
	default:
		text = c.T("pay.sent_card", args)
	}
	kb := keyboards.New()
	bankBtn, ok1 := keyboards.Button(c.T("button.bank", nil), AddrBank)
	again, ok2 := keyboards.Button(c.T("button.pay_again", nil), AddrPay, v.PayeeCode)
	if ok1 && ok2 {
		kb.Row(bankBtn, again)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(text, kb.Build())
}

// PaymentNoticeView is what a payee is told about money they received.
type PaymentNoticeView struct {
	PayerName string
	PayerCode string
	Method    string
	Amount    int64
}

// PaymentNotice renders the notification a payment sends to its payee. It
// always sends; see notification.go.
func PaymentNotice(c Context, v PaymentNoticeView) *presenter.Response {
	args := map[string]any{
		"player": c.playerName(v.PayerName),
		"amount": FormatMoney(c, v.Amount),
	}
	key := "pay.received_card"
	if v.Method == PayCash {
		key = "pay.received_cash"
	}
	var code string
	if v.PayerCode != "" {
		code = c.T("profile.code", map[string]any{"code": v.PayerCode})
	}
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.bank", nil), AddrBank); ok {
		kb.Row(btn)
	}
	if btn, ok := keyboards.Button(c.T("button.profile", nil), AddrProfile); ok {
		kb.Row(btn)
	}
	return presenter.Message(body(c.T(key, args), code), kb.Build())
}

// bankRefusal picks the sentence for a bank refusal, with the numbers it
// needs. ok is false for anything that is not a bank refusal.
//
// Unlike errorMessage it may match with errors.Is: every target here is a
// NAMED sentinel, which errors.Is compares by identity, not by class — and it
// has to, because a refusal carrying its numbers is a copy of the sentinel
// (WithDetail), which pointer identity would not recognise.
func bankRefusal(c Context, err error) (key string, args map[string]any, ok bool) {
	switch {
	case stderrors.Is(err, application.ErrBankNotInCity):
		return "bank.error.not_in_city", nil, true
	case stderrors.Is(err, application.ErrNotTogether):
		return "bank.error.not_together", nil, true
	case stderrors.Is(err, application.ErrSelfPayment):
		return "bank.error.self_payment", nil, true
	case stderrors.Is(err, application.ErrPayeeNotFound):
		return "bank.error.payee_not_found", nil, true
	case stderrors.Is(err, application.ErrInvalidMoneyAmount):
		return "bank.error.invalid_amount", nil, true
	case stderrors.Is(err, application.ErrAmountBelowMinimum):
		return "bank.error.below_minimum", map[string]any{"min": FormatMoney(c, detailInt(err, "min"))}, true
	case stderrors.Is(err, application.ErrAmountAboveMaximum):
		return "bank.error.above_maximum", map[string]any{"max": FormatMoney(c, detailInt(err, "max"))}, true
	case stderrors.Is(err, application.ErrNotEnoughCash):
		return "bank.error.not_enough_cash", map[string]any{
			"available": FormatMoney(c, detailInt(err, "available")),
		}, true
	case stderrors.Is(err, application.ErrNotEnoughInBank):
		return "bank.error.not_enough_in_bank", map[string]any{
			"available": FormatMoney(c, detailInt(err, "available")),
			"needed":    FormatMoney(c, detailInt(err, "needed")),
		}, true
	case stderrors.Is(err, application.ErrInsufficientFunds):
		return "bank.error.insufficient", nil, true
	case stderrors.Is(err, application.ErrBankPolicyUnavailable):
		return "bank.error.unavailable", nil, true
	}
	return "", nil, false
}
