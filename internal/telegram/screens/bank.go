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

// Bank renders the bank: the branch, both balances, the city's terms, and
// buttons to deposit and withdraw round amounts, everything, or any amount
// typed in.
func Bank(c Context, v BankView) *presenter.Response {
	return c.withView(renderBank(c, v), ScreenBank, v)
}

func renderBank(c Context, v BankView) *presenter.Response {
	kb := keyboards.New()

	balances := body(
		c.T("profile.cash", map[string]any{"cash": FormatMoney(c, v.Cash)}),
		c.T("profile.bank", map[string]any{"bank": FormatMoney(c, v.Bank)}),
	)

	title := c.T("bank.title", nil)
	var terms, hint string
	switch {
	case v.Travelling:
		terms = c.T("bank.closed_travelling", nil)
	case v.NoCity:
		terms = c.T("bank.closed_no_city", nil)
	default:
		title = c.T("bank.title_branch", map[string]any{"city": c.CityName(v.CityCode, v.City)})
		if v.WithdrawalFeeBPS > 0 {
			terms = c.T("bank.withdrawal_fee", map[string]any{"percent": PercentFromBPS(c, int(v.WithdrawalFeeBPS))})
		} else {
			terms = c.T("bank.withdrawal_free", nil)
		}
		var lines []string
		if v.CanDeposit {
			amountButtons(c, kb, "button.deposit", "button.deposit_all", AddrDeposit, v.Deposits)
			kb.Add(c.T("button.deposit_custom", nil), AddrAsk, commandDeposit)
		} else {
			lines = append(lines, c.T("bank.nothing_to_deposit", nil))
		}
		switch {
		case v.Jailed:
			lines = append(lines, c.T("bank.jailed_no_withdrawal", nil))
		case v.CanWithdraw:
			amountButtons(c, kb, "button.withdraw", "button.withdraw_all", AddrWithdraw, v.Withdrawals)
			kb.Add(c.T("button.withdraw_custom", nil), AddrAsk, commandWithdraw)
		default:
			lines = append(lines, c.T("bank.nothing_to_withdraw", nil))
		}
		if v.CanDeposit || (v.CanWithdraw && !v.Jailed) {
			lines = append(lines, c.T("bank.hint", nil))
		}
		hint = body(lines...)
	}

	pay, _ := keyboards.Button(c.T("button.pay_player", nil), AddrPay)
	// The national bank: loans, savings, insurance, investing
	// (docs/adr/0026).
	national, _ := keyboards.Button(c.T("finance.button.bank", nil), AddrLoanHub)
	kb.Row(pay, national)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrBank}))

	text := paragraphs(
		htmlEscape(bankNotice(c, v)),
		htmlBold(htmlEscape(title)),
		htmlEscape(body(balances, terms)),
		htmlEscape(hint),
		htmlEscape(c.T("bank.pay_hint", nil)),
	)
	return c.respond(text, kb.Build()).AsHTML()
}

// The commands a «✏️ مبلغ دلخواه» button asks an amount for; each is listed
// under input in configs/commands.yml.
const (
	commandDeposit  = "bank.deposit"
	commandWithdraw = "bank.withdraw"
	commandPay      = "bank.pay"
)

// amountButtons lays out quick amounts two to a row; «all of it» has its own
// label.
func amountButtons(c Context, kb *keyboards.Builder, labelKey, allKey, addr string, options []AmountOption) {
	buttons := make([]presenter.Button, 0, len(options))
	for _, o := range options {
		key := labelKey
		if o.All {
			key = allKey
		}
		btn, ok := keyboards.Button(
			c.T(key, map[string]any{"amount": FormatMoney(c, o.Amount)}),
			addr, strconv.FormatInt(o.Amount, 10), o.Nonce)
		if ok {
			buttons = append(buttons, btn)
		}
	}
	if len(buttons) > 0 {
		kb.Grid(2, buttons...)
	}
}

// bankNotice words the outcome of a deposit or a withdrawal for the top of
// the bank screen, from the code and the sums the view carries.
func bankNotice(c Context, v BankView) string {
	if v.Notice == "" {
		return ""
	}
	return c.T("bank."+v.Notice, moneyArgs(c, v.NoticeArgs))
}

// payNotice words why a payment brought the player back to the payment
// screen: the two are apart, or the way chosen does not cover it.
func payNotice(c Context, v PayView) string {
	if v.Notice == "" {
		return ""
	}
	args := moneyArgs(c, v.NoticeArgs)
	if name, ok := args["player"].(string); ok {
		args["player"] = c.playerName(name)
	}
	return c.T("pay."+v.Notice, args)
}

// PayHelp explains how to pay a player, when a payment names nobody.
func PayHelp(c Context) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.find_player", nil), AddrSearch); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank}))
	return c.respond(c.T("pay.help", nil), kb.Build())
}

// Pay renders the payment screen: who is paid, which ways are open and at
// what fee, both balances, and amounts for each way. Cash is offered only
// when the two are together.
func Pay(c Context, v PayView) *presenter.Response {
	return c.withView(renderPay(c, v), ScreenPay, v)
}

func renderPay(c Context, v PayView) *presenter.Response {
	name := c.playerName(v.PayeeName)
	kb := keyboards.New()

	var where string
	if v.Together {
		where = c.T("pay.together", map[string]any{"city": c.CityName(v.CityCode, v.City)})
		if v.CanCash {
			payButtons(c, kb, "button.pay_cash", "button.pay_cash_all", v, PayCash, v.CashOptions)
			addAsk(kb, c.T("button.pay_cash_custom", nil), commandPay, v.PayeeCode, PayCash, v.Origin)
		}
	} else {
		where = c.T("pay.apart", nil)
	}
	if v.CanCard {
		payButtons(c, kb, "button.pay_card", "button.pay_card_all", v, PayCard, v.CardOptions)
		addAsk(kb, c.T("button.pay_card_custom", nil), commandPay, v.PayeeCode, PayCard, v.Origin)
	}

	var terms string
	payerCity := c.CityName(v.PayerCityCode, v.PayerCity)
	switch {
	case payerCity == "":
	case v.CardFeeBPS > 0:
		terms = c.T("pay.card_fee", map[string]any{
			"city":    payerCity,
			"percent": PercentFromBPS(c, int(v.CardFeeBPS)),
		})
	default:
		terms = c.T("pay.card_free", map[string]any{"city": payerCity})
	}

	hint := c.T("pay.hint", nil)
	if !v.CanCard && !(v.Together && v.CanCash) {
		hint = c.T("pay.cannot_afford", nil)
	}

	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank, RefreshData: payAddr(v.PayeeCode, "", "", v.Origin)}))

	var code string
	if v.PayeeCode != "" {
		code = c.T("profile.code", map[string]any{"code": v.PayeeCode})
	}
	text := paragraphs(
		payNotice(c, v),
		body(c.T("pay.title", map[string]any{"player": name}), code),
		body(where, terms),
		body(
			c.T("profile.cash", map[string]any{"cash": FormatMoney(c, v.Cash)}),
			c.T("profile.bank", map[string]any{"bank": FormatMoney(c, v.Bank)}),
		),
		hint,
	)
	return c.respond(text, kb.Build())
}

// payAddr is the address of the payment screen or, with an amount and a
// method, its confirmation. The origin group goes last, and is left off when
// it would not fit Telegram's 64 bytes: telling the group is a courtesy, the
// button is not.
func payAddr(code, amount, method, origin string) string {
	parts := []string{AddrPay, code}
	if amount != "" {
		parts = append(parts, amount, method)
	}
	if origin != "" {
		if amount == "" {
			// bank.pay's arguments are positional: to, amount, method,
			// origin. A screen address without an amount leaves the
			// origin off rather than misplace it.
			return keyboards.Data(parts...)
		}
		if data := keyboards.Data(append(parts, origin)...); data != "" {
			return data
		}
	}
	return keyboards.Data(parts...)
}

// addAsk adds a «✏️» button that asks for the amount of command, carrying
// the fixed arguments; the origin is dropped when it would not fit.
func addAsk(kb *keyboards.Builder, label, command string, args ...string) {
	parts := append([]string{AddrAsk, command}, args...)
	for len(parts) > 2 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if keyboards.Data(parts...) == "" && len(parts) > 3 {
		parts = parts[:len(parts)-1]
	}
	kb.Add(label, parts...)
}

// payButtons lays out quick amounts for one method, two to a row. Each opens
// the confirmation; none moves money on its own.
func payButtons(c Context, kb *keyboards.Builder, labelKey, allKey string, v PayView, method string, options []AmountOption) {
	buttons := make([]presenter.Button, 0, len(options))
	for _, o := range options {
		key := labelKey
		if o.All {
			key = allKey
		}
		data := payAddr(v.PayeeCode, strconv.FormatInt(o.Amount, 10), method, v.Origin)
		if data == "" {
			continue
		}
		buttons = append(buttons, presenter.Button{
			Text:         c.T(key, map[string]any{"amount": FormatMoney(c, o.Amount)}),
			CallbackData: data,
		})
	}
	if len(buttons) > 0 {
		kb.Grid(2, buttons...)
	}
}

// PayConfirm asks the player to confirm one payment: to whom, how, the
// amount, the fee and the total, and what is left afterwards. The funds were
// checked before this screen was shown.
func PayConfirm(c Context, v PayConfirmView) *presenter.Response {
	return c.withView(renderPayConfirm(c, v), ScreenPayConfirm, v)
}

func renderPayConfirm(c Context, v PayConfirmView) *presenter.Response {
	args := map[string]any{
		"player": c.playerName(v.PayeeName),
		"amount": FormatMoney(c, v.Amount),
		"fee":    FormatMoney(c, v.Fee),
		"total":  FormatMoney(c, v.Total),
		"after":  FormatMoney(c, v.After),
	}
	method, after := "pay.confirm_method_card", "pay.confirm_after_bank"
	if v.Method == PayCash {
		method, after = "pay.confirm_method_cash", "pay.confirm_after_cash"
	}
	var fee string
	if v.Fee > 0 {
		fee = body(c.T("pay.confirm_fee", args), c.T("pay.confirm_total", args))
	}
	text := paragraphs(
		c.T("pay.confirm_title", args),
		body(c.T(method, args), c.T("pay.confirm_amount", args), fee),
		c.T(after, args),
	)

	kb := keyboards.New()
	amount := strconv.FormatInt(v.Amount, 10)
	data := ""
	if v.Origin != "" {
		data = keyboards.Data(AddrPaySend, v.PayeeCode, amount, v.Method, v.Nonce, v.Origin)
	}
	if data == "" {
		data = keyboards.Data(AddrPaySend, v.PayeeCode, amount, v.Method, v.Nonce)
	}
	cancel, okCancel := keyboards.Button(c.T("button.cancel", nil), AddrPay, v.PayeeCode)
	if data != "" && okCancel {
		kb.Row(presenter.Button{Text: c.T("button.confirm_pay", nil), CallbackData: data}, cancel)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank}))
	return c.respond(text, kb.Build())
}

// PaySent confirms a payment to the payer.
func PaySent(c Context, v PaySentView) *presenter.Response {
	return c.withView(renderPaySent(c, v), ScreenPaySent, v)
}

func renderPaySent(c Context, v PaySentView) *presenter.Response {
	args := map[string]any{
		"player": c.playerName(v.PayeeName),
		"amount": FormatMoney(c, v.Amount),
		"fee":    FormatMoney(c, v.Fee),
	}
	var text string
	switch {
	case v.Held:
		text = c.T("pay.held", args)
	case v.Method == PayCash:
		text = c.T("pay.sent_cash", args)
	case v.Fee > 0:
		text = body(c.T("pay.sent_card", args), c.T("pay.sent_fee", args))
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

// PaymentMadeView is the public line a group reads when one of its players
// paid another: who paid whom, never how much.
type PaymentMadeView struct {
	PayerName string
	PayeeName string
}

// PaymentMade is that line. It has no buttons: it is an announcement.
func PaymentMade(c Context, v PaymentMadeView) string {
	return c.T("pay.announced", map[string]any{
		"payer": c.playerName(v.PayerName),
		"payee": c.playerName(v.PayeeName),
	})
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
	return c.withView(renderPaymentNotice(c, v), ScreenPaymentNotice, v)
}

func renderPaymentNotice(c Context, v PaymentNoticeView) *presenter.Response {
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
