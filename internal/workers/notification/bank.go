package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// paymentReceived is the payload BankHandler.PaySend writes. Only the fields
// the payee's notice needs are read; the payer's record id is never shown.
type paymentReceived struct {
	PayeeID   string `json:"payee_id"`
	PayerName string `json:"payer_name"`
	PayerCode string `json:"payer_code"`
	Method    string `json:"method"`
	Amount    int64  `json:"amount"`
}

// renderPaymentReceived tells a player that another player paid them, and
// how: cash handed over face to face, or a card payment into their bank.
//
// The payer is named by display name and public code, the two things a
// player can recognise and act on; a payer with no name worth showing reads
// as "a player", as on every other screen.
func renderPaymentReceived(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev paymentReceived
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("bank.payment_received payload is unreadable").WithCause(err)
	}
	if ev.PayeeID == "" || ev.Amount <= 0 {
		return nil, apperrors.InvalidInput("bank.payment_received names no payee or no amount")
	}
	view := screens.PaymentNoticeView{
		PayerName: ev.PayerName,
		PayerCode: ev.PayerCode,
		Method:    ev.Method,
		Amount:    ev.Amount,
	}
	return &Draft{
		PlayerID: ev.PayeeID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.PaymentNotice(c, view) },
	}, nil
}
