package main

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// goodsHandlers serve what players carry and trade: the inventory, the city
// shops, the player market and the auction house.
type goodsHandlers struct {
	inventory *handlers.InventoryHandler
	shops     *handlers.ShopsHandler
	market    *handlers.MarketHandler
	auctions  *handlers.AuctionsHandler
	elections *handlers.ElectionsHandler
}

// newGoodsHandlers builds the goods handlers. Goods are content read from
// the live registry; a city's sales tax and market fee only through the
// resolver (ADR 0015); every duration runs on the game clock.
func newGoodsHandlers(
	uow application.UnitOfWork,
	msgs handlers.Translator,
	registry *content.Registry,
	cities application.CityRepository,
	policy application.PolicyReader,
	scale gametime.Scale,
	crimeCfg config.Crime,
	trade config.Trade,
	idempotencyTTL time.Duration,
) goodsHandlers {
	reserves := make([]int, 0, len(trade.AuctionReservesBPS))
	for _, r := range trade.AuctionReservesBPS {
		reserves = append(reserves, int(r))
	}
	return goodsHandlers{
		inventory: handlers.NewInventoryHandler(uow, uuidGenerator{}, msgs, registry, cities, scale,
			crimeRules(crimeCfg).Nerve, handlers.DefaultPageSize, idempotencyTTL, nil),
		shops: handlers.NewShopsHandler(uow, uuidGenerator{}, msgs, registry, cities, policy, scale,
			cryptoDice{}, idempotencyTTL, nil),
		market: handlers.NewMarketHandler(uow, uuidGenerator{}, msgs, registry, cities, policy, scale,
			handlers.MarketLimits{
				OrderTTL: trade.MarketOrderTTL, MaxOpen: trade.MarketMaxOpenOrders,
				MaxQuantity: int64(trade.MarketMaxQuantity), MaxPrice: trade.MarketMaxPrice,
			}, handlers.DefaultPageSize, idempotencyTTL, nil),
		auctions: handlers.NewAuctionsHandler(uow, uuidGenerator{}, msgs, registry, cities, policy, scale,
			handlers.AuctionRules{
				Durations: trade.AuctionDurations, MaxReserve: trade.AuctionMaxReserve,
				StepBPS: trade.AuctionStepBPS, MinStep: trade.AuctionMinStep, MaxOpen: trade.AuctionMaxOpen,
				ReservesBPS: reserves,
			}, handlers.DefaultPageSize, idempotencyTTL, nil),
		elections: handlers.NewElectionsHandler(uow, uuidGenerator{}, msgs, registry, cities, scale,
			crimeRules(crimeCfg).Nerve, idempotencyTTL, nil),
	}
}

// decoded adapts a handler method taking a request to a commandFunc.
func decoded[R any](f func(context.Context, envelope.Metadata, R) (*presenter.Response, error)) commandFunc {
	return func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
		var req R
		if err := decode(env, &req); err != nil {
			return nil, err
		}
		return f(ctx, env.Metadata, req)
	}
}

// bare adapts a handler method taking no request to a commandFunc.
func bare(f func(context.Context, envelope.Metadata) (*presenter.Response, error)) commandFunc {
	return func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
		return f(ctx, env.Metadata)
	}
}

// bindGoods maps the goods commands to their handlers. bind merges it into
// the one table bindAll checks against the subscriptions.
func (h phaseHandlers) bindGoods() map[string]commandFunc {
	g := h.goods
	return map[string]commandFunc{
		"inventory.show": decoded(g.inventory.Show),
		"inventory.item": decoded(g.inventory.Item),
		"inventory.use":  decoded(g.inventory.Use),
		"inventory.give": decoded(g.inventory.Give),
		"inventory.drop": decoded(g.inventory.Drop),

		"shop.list":   decoded(g.shops.List),
		"shop.view":   decoded(g.shops.View),
		"shop.buy":    decoded(g.shops.Buy),
		"shop.offers": decoded(g.shops.Offers),
		"shop.sell":   decoded(g.shops.Sell),

		"market.list":   decoded(g.market.Books),
		"market.book":   decoded(g.market.Book),
		"market.order":  decoded(g.market.Order),
		"market.cancel": decoded(g.market.Cancel),
		"market.mine":   decoded(g.market.Mine),
		// The scheduler's dispatch payload, as for crime.resolve.
		"market.expire": decoded(g.market.Expire),

		"auction.list":  bare(g.auctions.List),
		"auction.view":  decoded(g.auctions.View),
		"auction.new":   decoded(g.auctions.New),
		"auction.bid":   decoded(g.auctions.Bid),
		"auction.mine":  bare(g.auctions.Mine),
		"auction.close": decoded(g.auctions.Close),

		"election.list":  bare(g.elections.List),
		"election.view":  decoded(g.elections.View),
		"election.stand": decoded(g.elections.Stand),
		"election.vote":  decoded(g.elections.Vote),
		// The scheduler's: an election's vote opening, its count, and the
		// next one opening.
		"election.voting": decoded(g.elections.Voting),
		"election.count":  decoded(g.elections.Count),
		"election.open":   decoded(g.elections.Open),
	}
}
