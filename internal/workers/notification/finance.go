package notification

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/notices"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// Finance (docs/adr/0026-finance.md), privately: a borrower hears that an
// instalment is due they cannot cover, that one was missed, that a loan
// defaulted (and its property was taken) or was repaid; a policyholder that
// a claim was paid or a policy ended; an investor that an order filled, a
// dividend arrived, or control of a company changed hands.

// financeEvent is the payload the finance handlers write.
type financeEvent struct {
	Kind        string    `json:"kind"`
	PlayerID    string    `json:"player_id"`
	No          int64     `json:"no"`
	Product     string    `json:"product"`
	ProductName string    `json:"product_name"`
	Amount      int64     `json:"amount"`
	Other       int64     `json:"other"`
	Count       int64     `json:"count"`
	At          time.Time `json:"at"`
	PropertyNo  int64     `json:"property_no"`
	TypeCode    string    `json:"property_type"`
	TypeName    string    `json:"property_type_name"`
	CityCode    string    `json:"city_code"`
	CityName    string    `json:"city_name"`
	Value       int64     `json:"value"`
}

// renderFinanceNotice tells a borrower or a policyholder what happened.
func renderFinanceNotice(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev financeEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("finance event payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Kind == "" {
		return nil, apperrors.InvalidInput("finance event names nobody")
	}
	view := notices.FinanceView{Kind: ev.Kind, No: ev.No, Product: notices.Named{Code: ev.Product, Name: ev.ProductName},
		Amount: ev.Amount, Other: ev.Other, Count: ev.Count, At: ev.At}
	if ev.PropertyNo > 0 {
		view.Pledge = &notices.Pledge{No: ev.PropertyNo, Type: notices.Named{Code: ev.TypeCode, Name: ev.TypeName},
			City: notices.Place{Kind: "city", Code: ev.CityCode, Name: ev.CityName}, Value: ev.Value}
	}
	return &Draft{PlayerID: ev.PlayerID, Notice: func(c presentation.Ctx) *presentation.Response {
		return notices.FinanceNotice(c, view)
	}}, nil
}

// stockEvent is the payload the exchange writes.
type stockEvent struct {
	Kind        string `json:"kind"`
	PlayerID    string `json:"player_id"`
	Side        string `json:"side"`
	CompanyCode string `json:"company_code"`
	CompanyName string `json:"company_name"`
	Qty         int64  `json:"qty"`
	Price       int64  `json:"price"`
	Amount      int64  `json:"amount"`
	Gained      bool   `json:"gained"`
}

// renderStockNotice tells an investor what happened on the exchange.
func renderStockNotice(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev stockEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("stock event payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Kind == "" {
		return nil, apperrors.InvalidInput("stock event names nobody")
	}
	kind := ev.Kind
	if kind == "takeover" && !ev.Gained {
		kind = "takeover_lost"
	}
	view := notices.StockView{Kind: kind, Company: notices.Named{Code: ev.CompanyCode, Name: ev.CompanyName},
		Side: ev.Side, Qty: ev.Qty, Price: ev.Price, Amount: ev.Amount}
	return &Draft{PlayerID: ev.PlayerID, Notice: func(c presentation.Ctx) *presentation.Response {
		return notices.StockNotice(c, view)
	}}, nil
}
