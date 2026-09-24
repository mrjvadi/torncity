package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The notices of goods: a gift received, a market order that traded or ran
// out while its owner was away, an auction's news. Every one is private and
// renders from the payload alone: codes and authored names, never an id to
// look up.

// goodsNotice is the payload shape the goods handlers write: every field any
// of these notices uses.
type goodsNotice struct {
	PlayerID string `json:"player_id"`
	Item     string `json:"item"`
	ItemName string `json:"item_name"`
	FromName string `json:"from_name"`
	Side     string `json:"side"`
	Qty      int64  `json:"qty"`
	Price    int64  `json:"price"`
	Amount   int64  `json:"amount"`
	Fee      int64  `json:"fee"`
	Left     int64  `json:"left"`
	No       int64  `json:"no"`
	CityCode string `json:"city_code"`
	CityName string `json:"city_name"`
}

func decodeGoods(env *envelope.Envelope, name string) (goodsNotice, error) {
	var ev goodsNotice
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput(name + " payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Item == "" {
		return ev, apperrors.InvalidInput(name + " names no player or no good")
	}
	return ev, nil
}

func (e goodsNotice) item() screens.Named { return screens.Named{Code: e.Item, Name: e.ItemName} }

// renderItemGiven tells a player a friend gave them something.
func renderItemGiven(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeGoods(env, "inventory.given")
	if err != nil {
		return nil, err
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.ItemReceivedNotice(c, ev.item(), ev.FromName)
	}}, nil
}

// renderMarketFilled tells an order's owner it traded while they were away.
func renderMarketFilled(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeGoods(env, "market.filled")
	if err != nil {
		return nil, err
	}
	view := screens.MarketFilledView{Side: ev.Side, Item: ev.item(), Qty: ev.Qty, Price: ev.Price, Amount: ev.Amount,
		Fee: ev.Fee, CityCode: ev.CityCode, City: ev.CityName}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.MarketFilledNotice(c, view)
	}}, nil
}

// renderMarketExpired tells an order's owner its time ran out and what came
// back.
func renderMarketExpired(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeGoods(env, "market.expired")
	if err != nil {
		return nil, err
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.MarketExpiredNotice(c, ev.Side, ev.item(), ev.Left, ev.No)
	}}, nil
}

// renderAuction tells a seller or a bidder an auction's news: kind is
// outbid, won, sold or unsold.
func renderAuction(kind string) Renderer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
		ev, err := decodeGoods(env, "auction."+kind)
		if err != nil {
			return nil, err
		}
		view := screens.AuctionNoticeView{Kind: kind, No: ev.No, Item: ev.item(), Amount: ev.Amount, Fee: ev.Fee}
		return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
			return screens.AuctionNotice(c, view)
		}}, nil
	}
}
