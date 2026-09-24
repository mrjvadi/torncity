package screens

import (
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TestBankScreensRender renders every bank screen in every shipped language
// and holds each to the rules every screen follows: no catalogue key or
// placeholder left over, every button a routable address within Telegram's
// 64 bytes.
func TestBankScreensRender(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		opts := []AmountOption{{Amount: 250, Nonce: "abcdef012345"}, {Amount: 999_999_999, Nonce: "0123456789ab"}}
		for name, resp := range map[string]*presenter.Response{
			"bank open": Bank(c, BankView{CityCode: "ostmarch", City: "Ostmarch", Cash: 1000, Bank: 12500,
				WithdrawalFeeBPS: 250, Deposits: opts, Withdrawals: opts,
				Notice: BankNotice(c, false, 1000, 25)}),
			"bank free":       Bank(c, BankView{CityCode: "ostmarch", City: "Ostmarch", Notice: BankNotice(c, true, 5, 0)}),
			"bank travelling": Bank(c, BankView{Travelling: true, Cash: 1, Bank: 2}),
			"bank no city":    Bank(c, BankView{NoCity: true}),
			"pay help":        PayHelp(c),
			"pay together": Pay(c, PayView{PayeeName: "Bob", PayeeCode: "K7Q2M9A", Together: true,
				CityCode: "ostmarch", City: "Ostmarch", PayerCityCode: "ostmarch", PayerCity: "Ostmarch",
				CardFeeBPS: 100, Cash: 10, Bank: 20, CashOptions: opts, CardOptions: opts, CanCash: true, CanCard: true,
				Origin: "-1001234567890"}),
			"pay apart": Pay(c, PayView{PayeeCode: "K7Q2M9A", PayerCityCode: "ostmarch", CardOptions: opts,
				Notice: c.T("pay.not_together", map[string]any{"player": "Bob"})}),
			"confirm cash": PayConfirm(c, PayConfirmView{PayeeName: "Bob", PayeeCode: "K7Q2M9A", Method: PayCash,
				Amount: 999_999_999, Total: 999_999_999, Nonce: "abcdef012345", Origin: "-1001234567890"}),
			"confirm card fee": PayConfirm(c, PayConfirmView{PayeeCode: "K7Q2M9A", Method: PayCard,
				Amount: 1000, Fee: 10, Total: 1010, Nonce: "abcdef012345"}),
			"sent cash":     PaySent(c, PaySentView{PayeeName: "Bob", PayeeCode: "K7Q2M9A", Method: PayCash, Amount: 5}),
			"sent card fee": PaySent(c, PaySentView{PayeeCode: "K7Q2M9A", Method: PayCard, Amount: 5, Fee: 1}),
			"notice cash":   PaymentNotice(c, PaymentNoticeView{PayerName: "Ada", PayerCode: "B3C4D5F", Method: PayCash, Amount: 7}),
			"notice card":   PaymentNotice(c, PaymentNoticeView{Method: PayCard, Amount: 7}),
		} {
			t.Run(lang+"/"+name, func(t *testing.T) {
				assertRendered(t, resp)
				for _, a := range addresses(resp) {
					if len(a) > 64 {
						t.Errorf("address over 64 bytes: %q", a)
					}
				}
			})
		}
	}
}

// Cash is offered only between players who are together; the card always.
func TestPayOffersCashOnlyWhenTogether(t *testing.T) {
	c := ctx(t, "en", 0)
	opts := []AmountOption{{Amount: 100}}
	apart := Pay(c, PayView{PayeeCode: "K7Q2M9A", CashOptions: opts, CardOptions: opts, CanCash: true, CanCard: true})
	for _, a := range addresses(apart) {
		if strings.HasSuffix(a, ":"+PayCash) {
			t.Errorf("cash offered while apart: %q", a)
		}
	}
	together := Pay(c, PayView{PayeeCode: "K7Q2M9A", Together: true, CashOptions: opts, CardOptions: opts, CanCash: true, CanCard: true})
	if !hasAddress(together, AddrPay+":K7Q2M9A:100:"+PayCash) {
		t.Errorf("no cash while together: %v", addresses(together))
	}
}

// The confirm button never goes missing: when the group a payment started in
// does not fit Telegram's 64 bytes beside the largest amount, the group is
// left off, not the button.
func TestConfirmButtonSurvivesALongOrigin(t *testing.T) {
	c := ctx(t, "en", 0)
	resp := PayConfirm(c, PayConfirmView{PayeeCode: "K7Q2M9A", Method: PayCard, Amount: 999_999_999,
		Total: 999_999_999, Nonce: "abcdef012345", Origin: "-1001234567890"})
	found := false
	for _, a := range addresses(resp) {
		if strings.HasPrefix(a, AddrPaySend+":") {
			found = true
			if len(a) > 64 {
				t.Errorf("address over 64 bytes: %q", a)
			}
		}
	}
	if !found {
		t.Fatalf("no confirm button: %v", addresses(resp))
	}
}

// Every bank refusal reads as its own sentence, with its numbers.
func TestBankRefusalsHaveSentences(t *testing.T) {
	c := ctx(t, "en", 0)
	for _, err := range []error{
		application.ErrBankNotInCity,
		application.ErrNotTogether,
		application.ErrSelfPayment,
		application.ErrPayeeNotFound,
		application.ErrInvalidMoneyAmount,
		application.ErrAmountBelowMinimum.WithDetail("min", int64(10)),
		application.ErrAmountAboveMaximum.WithDetail("max", int64(1000)),
		application.ErrNotEnoughCash.WithDetail("available", int64(5)),
		application.ErrNotEnoughInBank.WithDetail("available", int64(5)).WithDetail("needed", int64(1010)),
		application.ErrInsufficientFunds,
		application.ErrBankPolicyUnavailable,
	} {
		key, _, ok := bankRefusal(c, err)
		if !ok || !strings.HasPrefix(key, "bank.error.") {
			t.Errorf("%v has no bank sentence (key %q)", err, key)
			continue
		}
		resp := Error(c, err)
		assertRendered(t, resp)
	}
	resp := Error(c, application.ErrNotEnoughInBank.WithDetail("available", int64(5)).WithDetail("needed", int64(1010)))
	if !strings.Contains(resp.Text, "1,010") {
		t.Errorf("the refusal does not say how much is needed: %q", resp.Text)
	}
}
