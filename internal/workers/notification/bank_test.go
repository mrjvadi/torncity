package notification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

func paymentRoute(t *testing.T) Route {
	t.Helper()
	for _, r := range Routes() {
		if r.Domain == "bank" && r.Event == "payment_received" {
			return r
		}
	}
	t.Fatal("no bank.payment_received route")
	return Route{}
}

func paymentEvent(t *testing.T, requestID string, receivedAt time.Time, payload map[string]any) *envelope.Envelope {
	t.Helper()
	env := arrivalEvent(t, requestID, receivedAt, nil)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	env.Metadata.Command = "bank.pay.send"
	env.Payload = raw
	return env
}

// The payee is told who paid them, how much and how, in their own language,
// and nothing internal reaches the screen: not the payer's record id, not the
// method's stored name.
func TestPaymentNoticeGoesToThePayee(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		t.Run(lang, func(t *testing.T) {
			p := englishPlayer()
			p.Language = lang
			r := newRig(t, p, link(botA, 1001))
			env := paymentEvent(t, "req-pay", r.now, map[string]any{
				"payer_id":   "9a9a9a9a-0000-4000-8000-000000000009",
				"payer_name": "Ada",
				"payer_code": "B3C4D5F",
				"payee_id":   playerID,
				"method":     "card",
				"amount":     12500,
				"fee":        125,
			})
			if err := r.w.Handle(context.Background(), paymentRoute(t), env); err != nil {
				t.Fatal(err)
			}
			if len(r.sender.sent) != 1 {
				t.Fatalf("sent %d notices, want 1", len(r.sender.sent))
			}
			text := r.sender.sent[0].notice.Response.Text
			amount := map[string]string{"fa": "12,500", "en": "12,500"}[lang]
			for _, want := range []string{"Ada", amount, "B3C4D5F"} {
				if !strings.Contains(text, want) {
					t.Errorf("notice lacks %q:\n%s", want, text)
				}
			}
			if m := uuidPattern.FindString(text); m != "" {
				t.Errorf("an identifier %q reached the payee: %q", m, text)
			}
			if strings.Contains(text, "card") && lang == "fa" {
				t.Errorf("the method's stored name reached the payee: %q", text)
			}
		})
	}
}

func TestUnreadablePaymentIsDropped(t *testing.T) {
	_, err := renderPaymentReceived(context.Background(), Deps{}, paymentEvent(t, "req-bad", time.Now(), map[string]any{"amount": 0}))
	if err == nil {
		t.Fatal("a payment naming nobody was rendered")
	}
}
