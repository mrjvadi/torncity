package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Property (docs/adr/0024-property-and-politics.md), privately: a seller
// hears their property was bought, a landlord that it was let or that their
// tenant left or was evicted, a tenant that they were evicted, an owner that
// the city took a property back for its debt.

// propertyEvent is the payload the property handlers write.
type propertyEvent struct {
	Kind       string `json:"kind"`
	PlayerID   string `json:"player_id"`
	PropertyNo int64  `json:"property_no"`
	Type       string `json:"type"`
	TypeName   string `json:"type_name"`
	CityCode   string `json:"city_code"`
	CityName   string `json:"city_name"`
	OtherName  string `json:"other_name"`
	OtherCode  string `json:"other_code"`
	Amount     int64  `json:"amount"`
}

// renderPropertyNotice tells a player what happened to their property or
// their home.
func renderPropertyNotice(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev propertyEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("property event payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Kind == "" {
		return nil, apperrors.InvalidInput("property event names nobody")
	}
	view := screens.PropertyNoticeView{Kind: ev.Kind, Type: screens.Named{Code: ev.Type, Name: ev.TypeName},
		No: ev.PropertyNo, City: screens.GovPlace{Kind: "city", Code: ev.CityCode, Name: ev.CityName},
		Player: screens.GovPlayer{Name: ev.OtherName, Code: ev.OtherCode}, Amount: ev.Amount}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.PropertyNotice(c, view)
	}}, nil
}
