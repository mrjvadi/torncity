package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The production economy (docs/adr/0021-production-economy.md): privately, a
// company's owner hears that a research finished, an order came out, a
// reverse engineering ended, a license of theirs was bought and goods of
// theirs were sold; in a city's groups, a technology published for everyone
// and a new product's first units. No public line carries an amount.

// productionEvent is the payload the production handler writes.
type productionEvent struct {
	CompanyID string `json:"company_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	OwnerID   string `json:"owner_id"`
	CityID    string `json:"city_id"`
	Tech      string `json:"tech"`
	TechName  string `json:"tech_name"`
	Item      string `json:"item"`
	Qty       int64  `json:"qty"`
	Quality   int    `json:"quality"`
	Component bool   `json:"component"`
	Design    string `json:"design"`
	DesignNo  int64  `json:"design_no"`
	Source    string `json:"source"`
	Succeeded bool   `json:"succeeded"`
	Buyer     string `json:"buyer"`
	Price     int64  `json:"price"`
}

func decodeProduction(env *envelope.Envelope, name string) (productionEvent, error) {
	var ev productionEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("company." + name + " payload is unreadable").WithCause(err)
	}
	if ev.CompanyID == "" {
		return ev, apperrors.InvalidInput("company." + name + " names no company")
	}
	return ev, nil
}

func (e productionEvent) ref() screens.CompanyRef {
	return screens.CompanyRef{Code: e.Code, Name: e.Name, Type: screens.Named{Code: e.Type, Name: e.Type}}
}

// good names the event's good. The item's authored name is not in the
// payload; the locale names it by code.
func (e productionEvent) good() screens.Good {
	return screens.Good{Component: e.Component, Item: screens.Named{Code: e.Item, Name: e.Item}, Design: e.Design,
		DesignNo: e.DesignNo}
}

// renderProduction renders one private notice of the production economy.
func renderProduction(name string) Renderer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
		ev, err := decodeProduction(env, name)
		if err != nil || ev.OwnerID == "" {
			return nil, err
		}
		view := screens.ProductionNoticeView{Company: ev.ref(), Tech: screens.Named{Code: ev.Tech, Name: ev.TechName},
			Good: ev.good(), Qty: ev.Qty, Quality: ev.Quality, Design: ev.Design, DesignNo: ev.DesignNo, Buyer: ev.Buyer,
			Price: ev.Price}
		switch name {
		case "researched":
			view.Kind = screens.ProductionNoticeResearched
		case "produced":
			view.Kind = screens.ProductionNoticeProduced
		case "reversed":
			view.Kind = screens.ProductionNoticeReversedBad
			view.Good = screens.Good{Item: screens.Named{Code: ev.Item, Name: ev.Item}, Design: ev.Source}
			if ev.Succeeded && ev.DesignNo > 0 {
				view.Kind = screens.ProductionNoticeReversedOK
			}
		case "license_sold":
			view.Kind = screens.ProductionNoticeLicenseSold
		case "sold":
			view.Kind = screens.ProductionNoticeSold
		}
		return &Draft{PlayerID: ev.OwnerID, Screen: func(c screens.Context) *presenter.Response {
			return screens.ProductionNotice(c, view)
		}}, nil
	}
}

// techPublishedAnnouncement: a company in a city published a technology.
func techPublishedAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeProduction(env, "tech_published")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{CityID: ev.CityID, Line: func(c screens.Context, _ string) string {
		return screens.TechPublishedAnnouncement(c, ev.ref(), screens.Named{Code: ev.Tech, Name: ev.TechName}, city.Code, city.Name)
	}}, nil
}

// productLaunchedAnnouncement: a company in a city made the first units of
// a new product.
func productLaunchedAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeProduction(env, "product_launched")
	if err != nil || ev.CityID == "" || ev.Design == "" {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{CityID: ev.CityID, Line: func(c screens.Context, _ string) string {
		return screens.ProductLaunchedAnnouncement(c, ev.ref(), ev.Design, screens.Named{Code: ev.Item, Name: ev.Item},
			city.Code, city.Name)
	}}, nil
}
