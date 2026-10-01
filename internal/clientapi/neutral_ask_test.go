package clientapi

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// An action the player types the last value of reaches the web as an input:
// the client asks for the value and sends it under the input's field, with the
// fixed arguments named as the command's input table names them.
func TestNeutralAskActionBecomesAnInput(t *testing.T) {
	policy, err := groups.LoadPolicy("../../configs/commands.yml")
	if err != nil {
		t.Fatal(err)
	}
	pay := economy.Pay(presentation.Ctx{Lang: "fa"}, economy.PayView{PayeeName: "Sara", PayeeCode: "AB12", Together: true, CanCash: true,
		CashOptions: []economy.AmountOption{{Amount: 100, Nonce: "n"}}, Origin: "-100123"})
	out := ScreenOf(pay, "bank.pay", policy, nil)
	var typed *Action
	for i := range out.Actions {
		if out.Actions[i].ID == "pay.cash_custom" {
			typed = &out.Actions[i]
		}
	}
	if typed == nil || typed.Input == nil || typed.Input.Field != "amount" || typed.Command != "bank.pay" {
		t.Fatalf("the custom amount must be an input action: %+v", typed)
	}
	if typed.Args["to"] != "AB12" || typed.Args["method"] != "cash" || typed.Args["origin"] != "-100123" {
		t.Errorf("the fixed arguments must be named as the input table names them: %v", typed.Args)
	}
	b := &Bridge{Policy: policy}
	if got := b.askAddress(typed); got != "ask:bank.pay:AB12:cash:-100123" {
		t.Errorf("the legacy label is found by %q", got)
	}
}
