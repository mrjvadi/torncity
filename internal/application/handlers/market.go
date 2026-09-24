package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// LeverMarketFee is the city's fee on a trade or an auction sale, in basis
// points of the price, paid by the seller (governance.yml).
const LeverMarketFee = "city.market_fee"

// MarketLimits bound orders (config trade.market_*).
type MarketLimits struct {
	OrderTTL    time.Duration
	MaxOpen     int
	MaxQuantity int64
	MaxPrice    int64
}

// MarketHandler serves the player market (internal/domain/market): a city's
// books, one good's book, placing and cancelling an order, a player's own
// orders, and an order's expiry from the scheduler.
//
// # The market is per city, at its market place
//
// Goods have no location of their own: they are where their holder is. An
// order is placed in the city the player stands in, at the place the market
// is (the bazaar), and rests on that city's book. When it fills — now or
// later, wherever its owner has gone — the goods go into the buyer's bag and
// the money into the seller's bank.
//
// # Escrow
//
// A buy sets its whole reserve (quantity × its price) aside in the player's
// escrow account, from the purse they chose; a sell sets its goods aside in
// the 'escrow' holding. A trade pays the seller the resting price times the
// quantity less the city's market fee (rounded up, the seller's), and gives
// a buyer back any price improvement. A cancel or an expiry gives back
// exactly what is still set aside, to where it came from.
type MarketHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	limits  MarketLimits

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewMarketHandler wires the handler.
func NewMarketHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, limits MarketLimits,
	pageSize int, idempotencyTTL time.Duration, now func() time.Time,
) *MarketHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewMarketHandler requires content, cities, policy and ids")
	}
	if limits.OrderTTL <= 0 || limits.MaxOpen < 1 || limits.MaxQuantity < 1 || limits.MaxPrice < 1 || idempotencyTTL <= 0 {
		panic("handlers: NewMarketHandler requires positive limits")
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &MarketHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy, scale: scale,
		limits: limits, pageSize: pageSize, idempotencyTTL: idempotencyTTL, now: now}
}

// MarketRequest is the market's payload: a good, a side, a quantity, a price,
// the way to pay a buy, a one-time token; No names an order to cancel.
type MarketRequest struct {
	Item   string `json:"item,omitempty"`
	Side   string `json:"side,omitempty"`
	Qty    string `json:"qty,omitempty"`
	Price  string `json:"price,omitempty"`
	Method string `json:"method,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
	No     string `json:"no,omitempty"`
	Page   string `json:"page,omitempty"`
}

// MarketActionPayload is the jsonb an order's expiry carries.
type MarketActionPayload = CrimeActionPayload

func (h *MarketHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// marketRefusal carries a refused market request out of a unit of work.
type marketRefusal struct{ view screens.MarketRefusalView }

func (r *marketRefusal) Error() string { return "handlers: market refused: " + r.view.Kind }

func refuseMarket(kind string) *marketRefusal {
	return &marketRefusal{view: screens.MarketRefusalView{Kind: kind}}
}

func (h *MarketHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *marketRefusal
	if stderrors.As(err, &r) {
		return screens.MarketRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return nil, err
}

func (h *MarketHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	return id[:min(len(id), nonceLength)]
}

// city reads the player's city, refusing a player on the road.
func (h *MarketHandler) city(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player) (whereabouts, error) {
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return whereabouts{}, application.ErrAlreadyTravelling
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return whereabouts{}, err
	}
	w, err := locate(ctx, tx, h.cities, snap, p)
	if err != nil {
		return w, err
	}
	if w.city == nil {
		return w, application.ErrCityNotFound
	}
	return w, nil
}

// reference is a good's reference price on a book: its last trade, else its
// base price.
func reference(snap *content.Snapshot, code string, last int64) int64 {
	if last > 0 {
		return last
	}
	def, _ := snap.ItemDef(code)
	return max(def.BasePrice, 1)
}

// Books handles market.list: the city's books, and the goods the player
// could put on one.
func (h *MarketHandler) Books(ctx context.Context, meta envelope.Metadata, req MarketRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.MarketView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		w, err := h.city(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		view.CityCode, view.City = w.city.Code, w.city.Name
		books, err := tx.Market().Books(ctx, w.city.ID)
		if err != nil {
			return err
		}
		listed := map[string]bool{}
		for _, b := range books {
			if _, ok := snap.ItemDef(b.Item); !ok {
				continue
			}
			listed[b.Item] = true
			view.Books = append(view.Books, screens.BookSummary{Item: itemNamed(snap, b.Item), BestBid: b.BestBid,
				BestAsk: b.BestAsk, Last: b.LastPrice})
		}
		stacks, _, _, err := carried(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		for _, s := range stacks {
			if def, ok := snap.ItemDef(s.Item); ok && !listed[s.Item] && def.Item().Tradeable {
				view.Yours = append(view.Yours, itemNamed(snap, s.Item))
			}
		}
		view.AtMarket = h.atMarket(w)
		view.Way = wayTo(w, snap, place.ServiceMarket, h.scale)
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.Market(h.screen(meta, lang), view), nil
}

// atMarket reports whether the player stands where orders are placed.
func (h *MarketHandler) atMarket(w whereabouts) bool {
	if !w.placed() {
		return true
	}
	pl, ok := w.cmap.ForService(place.ServiceMarket)
	return !ok || (w.walk == nil && pl.Code == w.here.Code)
}

// Book handles market.book: one good's book in the player's city.
func (h *MarketHandler) Book(ctx context.Context, meta envelope.Metadata, req MarketRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.BookView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, ok := snap.ItemDef(req.Item)
		if !ok || !def.Item().Tradeable || isUniqueForm(def) {
			return refuseMarket(screens.MarketRefusedNotTraded)
		}
		w, err := h.city(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		view, err = h.bookView(ctx, tx, snap, w, p, def)
		return err
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.Book(h.screen(meta, lang), view), nil
}

// bookView reads one good's book and what the player can do on it.
func (h *MarketHandler) bookView(ctx context.Context, tx application.Tx, snap *content.Snapshot, w whereabouts,
	p *application.Player, def content.ItemDef,
) (screens.BookView, error) {
	orders, err := tx.Market().OpenOrders(ctx, w.city.ID, def.Code)
	if err != nil {
		return screens.BookView{}, err
	}
	trades, err := tx.Market().RecentTrades(ctx, w.city.ID, def.Code, 5)
	if err != nil {
		return screens.BookView{}, err
	}
	v := screens.BookView{Item: named(def.Code, def.Name), CityCode: w.city.Code, City: w.city.Name,
		AtMarket: h.atMarket(w), Way: wayTo(w, snap, place.ServiceMarket, h.scale), Nonce: h.nonce()}
	now := h.now()
	bids, asks := map[int64]int64{}, map[int64]int64{}
	for _, o := range orders {
		if !o.ExpiresAt.After(now) {
			continue
		}
		if o.Side == string(market.Buy) {
			bids[o.Price] += o.Qty - o.Filled
		} else {
			asks[o.Price] += o.Qty - o.Filled
		}
	}
	v.Bids, v.Asks = levels(bids, true), levels(asks, false)
	var last int64
	for i, t := range trades {
		if i == 0 {
			last = t.Price
		}
		v.Trades = append(v.Trades, screens.TradeLine{Qty: t.Qty, Price: t.Price, At: t.At})
	}
	v.Reference = reference(snap, def.Code, last)
	stacks, _, _, err := carried(ctx, tx, p.ID)
	if err != nil {
		return v, err
	}
	for _, s := range stacks {
		if s.Item == def.Code {
			v.Holding = s.Qty
		}
	}
	return v, nil
}

// levels sums a side of a book by price, best first, at most five levels.
func levels(qty map[int64]int64, bids bool) []screens.BookLevel {
	out := make([]screens.BookLevel, 0, len(qty))
	for p, q := range qty {
		out = append(out, screens.BookLevel{Price: p, Qty: q})
	}
	sort.Slice(out, func(i, j int) bool {
		if bids {
			return out[i].Price > out[j].Price
		}
		return out[i].Price < out[j].Price
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// domainOrder is a stored order as the matcher takes it.
func domainOrder(o application.MarketOrder, key market.AssetKey) market.Order {
	return market.Order{ID: o.ID, Side: market.Side(o.Side), Kind: market.Kind(o.Kind), Asset: key, Quantity: o.Qty,
		Filled: o.Filled, UnitPrice: money.FromMinor(o.Price), Owner: o.OwnerID, CreatedAt: o.CreatedAt, ExpiresAt: o.ExpiresAt}
}

// Order handles market.order: a limit order on a good's book, at the market
// place. A buy without a way to pay is answered with its checkout.
func (h *MarketHandler) Order(ctx context.Context, meta envelope.Metadata, req MarketRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	side := market.Side(req.Side)
	if side != market.Buy && side != market.Sell {
		return nil, errors.InvalidInput("market order side is neither buy nor sell")
	}
	qty := parseQty(req.Qty)
	price, err := strconv.ParseInt(strings.TrimSpace(req.Price), 10, 64)
	if err != nil || price < 1 {
		return nil, errors.InvalidInput("market order price is not a positive whole number")
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		checkout *screens.MarketCheckoutView
		placed   screens.OrderPlacedView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		ready := side == market.Sell || chosen
		if ready {
			key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
			if isNonce(req.Nonce) {
				key = idempotency.Derive(p.ID, meta.Command, req.Nonce)
			}
			fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		now := h.now()
		def, ok := snap.ItemDef(req.Item)
		if !ok || !def.Item().Tradeable || isUniqueForm(def) {
			return refuseMarket(screens.MarketRefusedNotTraded)
		}
		it := named(def.Code, def.Name)
		w, err := h.city(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		if err := needService(w, snap, place.ServiceMarket, h.scale, now); err != nil {
			return thenFor(err, "market.book", def.Code)
		}
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		if qty > h.limits.MaxQuantity || price > h.limits.MaxPrice {
			r := refuseMarket(screens.MarketRefusedTooBig)
			r.view.Item = it
			return r
		}
		reserve, err := market.Reserve(market.Order{Side: side, Quantity: qty, UnitPrice: money.FromMinor(price)})
		if err != nil {
			return refuseMarket(screens.MarketRefusedTooBig)
		}
		if side == market.Buy {
			wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return err
			}
			plan := wallet.Plan(reserve.Money, snap.Accepts(content.ServiceMarket))
			if !chosen {
				checkout = &screens.MarketCheckoutView{Item: it, Qty: qty, Price: price, Reserve: reserve.Money.Minor(),
					Payment: paymentChoice(plan, wallet), Nonce: h.nonce()}
				return nil
			}
			if err := checkMethod(plan, method, wallet, "market.button.back_to_book", screens.AddrMarketBook, def.Code); err != nil {
				return err
			}
		}
		open, err := tx.Market().CountOpen(ctx, p.ID)
		if err != nil {
			return err
		}
		if open >= h.limits.MaxOpen {
			r := refuseMarket(screens.MarketRefusedTooMany)
			r.view.Count = h.limits.MaxOpen
			return r
		}
		placed, err = h.place(ctx, tx, meta, snap, w.city, p, def, side, qty, price, method, reserve, now)
		return err
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	switch {
	case replayed:
		return h.Mine(ctx, meta, MarketRequest{})
	case checkout != nil:
		return screens.MarketCheckout(h.screen(meta, lang), *checkout), nil
	}
	return screens.OrderPlaced(h.screen(meta, lang), placed), nil
}

// place sets the order's escrow aside, matches it against the book, settles
// every fill, and rests what is left, in the caller's transaction.
func (h *MarketHandler) place(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	city *application.City, p *application.Player, def content.ItemDef, side market.Side, qty, price int64,
	method payment.Method, reserve market.Reservation, now time.Time,
) (screens.OrderPlacedView, error) {
	it := named(def.Code, def.Name)
	if err := tx.Market().LockBook(ctx, city.ID, def.Code); err != nil {
		return screens.OrderPlacedView{}, err
	}
	feeLever, err := h.policy.Get(ctx, city.JurisdictionID, LeverMarketFee)
	if err != nil {
		return screens.OrderPlacedView{}, err
	}
	order := application.MarketOrder{
		ID: h.ids.NewID(), CityID: city.ID, Item: def.Code, Side: string(side), Kind: string(market.Limit),
		Qty: qty, Price: price, OwnerID: p.ID, Status: application.OrderOpen,
		CreatedAt: now, ExpiresAt: now.Add(h.limits.OrderTTL),
	}
	// Escrow first: the money or the goods the order may trade.
	if side == market.Buy {
		order.Funding = string(method)
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return screens.OrderPlacedView{}, err
		}
		escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, p.ID)
		if err != nil {
			return screens.OrderPlacedView{}, err
		}
		if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
			Method: method, Accepted: snap.Accepts(content.ServiceMarket), Reason: application.ReasonMarketEscrow,
			ReferenceType: "market_orders", ReferenceID: order.ID,
			To: []application.LedgerEntry{{AccountID: escrow.ID, Amount: reserve.Money}}, CreatedAt: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				plan := wallet.Plan(reserve.Money, snap.Accepts(content.ServiceMarket))
				return screens.OrderPlacedView{}, declined(plan, wallet, "market.button.back_to_book", screens.AddrMarketBook, def.Code)
			}
			return screens.OrderPlacedView{}, err
		}
	} else {
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return screens.OrderPlacedView{}, err
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: def.Code, Qty: qty,
			From: p.ID, FromHolding: application.HoldCarried, To: p.ID, ToHolding: application.HoldEscrow,
			Reason: application.ItemMarketEscrow, ReferenceType: "market_orders", ReferenceID: order.ID, At: now}); err != nil {
			if isSentinel(err, application.ErrNotEnoughItems) {
				r := refuseMarket(screens.MarketRefusedNotEnough)
				r.view.Item = it
				return screens.OrderPlacedView{}, r
			}
			return screens.OrderPlacedView{}, err
		}
	}

	resting, err := tx.Market().OpenOrders(ctx, city.ID, def.Code)
	if err != nil {
		return screens.OrderPlacedView{}, err
	}
	key := market.AssetKey{Type: market.AssetItem, ID: def.Code, City: city.ID, Channel: market.ChannelPublic}
	book := market.NewBook(key)
	stored := map[string]application.MarketOrder{}
	for _, o := range resting {
		stored[o.ID] = o
		d := domainOrder(o, key)
		if d.Side == market.Buy {
			book.Bids = append(book.Bids, d)
		} else {
			book.Asks = append(book.Asks, d)
		}
	}
	res, err := market.Match(book, domainOrder(order, key), now)
	if err != nil {
		return screens.OrderPlacedView{}, errors.Internal(err)
	}
	order.Filled = res.Incoming.Filled
	if !res.Rests {
		order.Status = application.OrderFilled
		if order.Filled < order.Qty {
			order.Status = application.OrderCancelled
		}
	}
	if res.Rests {
		payload, err := json.Marshal(MarketActionPayload{ReferenceID: order.ID, PlayerID: p.ID})
		if err != nil {
			return screens.OrderPlacedView{}, err
		}
		order.GameActionID = h.ids.NewID()
		if err := tx.GameActions().Schedule(ctx, application.GameAction{
			ID: order.GameActionID, ActionType: application.MarketExpiryActionType, ActorType: "player", ActorID: p.ID,
			ReferenceType: "market_orders", ReferenceID: order.ID, Payload: payload, StartedAt: now, FinishAt: order.ExpiresAt,
		}); err != nil {
			return screens.OrderPlacedView{}, err
		}
	}
	placed, err := tx.Market().PlaceOrder(ctx, order)
	if err != nil {
		return screens.OrderPlacedView{}, err
	}

	var spent, got int64
	for _, t := range res.Trades {
		fee, err := h.settle(ctx, tx, meta, snap, city, def, t, stored, placed, int(feeLever.Value), now)
		if err != nil {
			return screens.OrderPlacedView{}, err
		}
		spent += t.Notional.Minor()
		got += t.Notional.Minor() - fee
	}
	for _, u := range res.Updated {
		if err := tx.Market().UpdateOrder(ctx, u.ID, u.Filled, application.OrderOpen, nil); err != nil {
			return screens.OrderPlacedView{}, err
		}
	}
	for _, r := range res.Removed {
		status := application.OrderFilled
		switch r.Reason {
		case market.RemovedExpired:
			status = application.OrderExpired
		case market.RemovedSelfTrade:
			status = application.OrderCancelled
		}
		closed := now
		if err := tx.Market().UpdateOrder(ctx, r.Order.ID, r.Order.Filled, status, &closed); err != nil {
			return screens.OrderPlacedView{}, err
		}
		if status != application.OrderFilled {
			if err := h.release(ctx, tx, stored[r.Order.ID], r.Order.Remaining(), now); err != nil {
				return screens.OrderPlacedView{}, err
			}
		}
	}
	// The incoming buy traded at resting prices at or under its own: give
	// the difference back now. A market order's unfilled part does not
	// arise (only limits are placed).
	if side == market.Buy && spent > 0 {
		reserved := order.Filled * price
		if improvement := reserved - spent; improvement > 0 {
			if err := h.refund(ctx, tx, placed, improvement, now); err != nil {
				return screens.OrderPlacedView{}, err
			}
		}
	}
	return screens.OrderPlacedView{Item: it, Side: string(side), Qty: qty, Filled: order.Filled, Price: price, No: placed.No,
		Rests: res.Rests, Spent: spent, Got: got, ExpiresAt: order.ExpiresAt, Method: order.Funding}, nil
}

// settle books one trade: the goods from the seller's escrow into the
// buyer's bag, the money from the buyer's escrow to the seller's bank less
// the fee, and a resting buyer's price improvement back. It returns the fee.
func (h *MarketHandler) settle(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	city *application.City, def content.ItemDef, t market.Trade, stored map[string]application.MarketOrder,
	incoming application.MarketOrder, feeBPS int, now time.Time,
) (int64, error) {
	s, err := market.Settle(t, int64(feeBPS))
	if err != nil {
		return 0, errors.Internal(err)
	}
	buyerEscrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, t.Buyer)
	if err != nil {
		return 0, err
	}
	sellerBank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, t.Seller)
	if err != nil {
		return 0, err
	}
	tradeID := h.ids.NewID()
	var txID string
	if !s.SellerReceives.IsZero() {
		neg, _ := s.SellerReceives.Neg()
		if txID, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
			Reason: application.ReasonMarketTrade, ReferenceType: "market_trades", ReferenceID: tradeID,
			Entries:   []application.LedgerEntry{{AccountID: buyerEscrow.ID, Amount: neg}, {AccountID: sellerBank.ID, Amount: s.SellerReceives}},
			CreatedAt: now,
		}); err != nil {
			return 0, err
		}
	}
	if !s.Fee.IsZero() {
		neg, _ := s.Fee.Neg()
		feeTx, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			Reason: application.ReasonMarketFee, ReferenceType: "market_trades", ReferenceID: tradeID,
			Entries:   []application.LedgerEntry{{AccountID: buyerEscrow.ID, Amount: neg}, {AccountID: application.SystemSinkAccountID, Amount: s.Fee}},
			CreatedAt: now,
		})
		if err != nil {
			return 0, err
		}
		if txID == "" {
			txID = feeTx
		}
	}
	// A resting buy reserved at its own price and traded at it: no
	// improvement. (The incoming side's improvement is given back once, by
	// the caller.)
	if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: def.Code, Qty: t.Quantity,
		From: t.Seller, FromHolding: application.HoldEscrow, To: t.Buyer, ToHolding: application.HoldCarried,
		Reason: application.ItemMarketTrade, ReferenceType: "market_trades", ReferenceID: tradeID, At: now}); err != nil {
		return 0, err
	}
	if err := tx.Market().RecordTrade(ctx, application.MarketTrade{
		ID: tradeID, CityID: city.ID, Item: def.Code, BuyOrder: t.BuyOrderID, SellOrder: t.SellOrderID,
		Buyer: t.Buyer, Seller: t.Seller, Qty: t.Quantity, Price: t.UnitPrice.Minor(), Notional: t.Notional.Minor(),
		Fee: s.Fee.Minor(), LedgerTransactionID: txID, At: now,
	}); err != nil {
		return 0, err
	}
	// The resting side's owner is told: their order filled while they were
	// elsewhere.
	restingOwner, restingSide := t.Seller, string(market.Sell)
	if t.SellOrderID == incoming.ID {
		restingOwner, restingSide = t.Buyer, string(market.Buy)
	}
	amount := s.SellerReceives.Minor()
	if restingSide == string(market.Buy) {
		amount = t.Notional.Minor()
	}
	return s.Fee.Minor(), appendMarketEvent(ctx, tx, meta, "filled", restingOwner, map[string]any{
		"player_id": restingOwner, "side": restingSide, "item": def.Code, "item_name": def.Name,
		"qty": t.Quantity, "price": t.UnitPrice.Minor(), "amount": amount, "fee": s.Fee.Minor(),
		"city_code": city.Code, "city_name": city.Name, "trade_id": tradeID,
	})
}

// release gives back what a closed order still holds: a buy's money to the
// purse it came from, a sell's goods to the bag.
func (h *MarketHandler) release(ctx context.Context, tx application.Tx, o application.MarketOrder, remaining int64, now time.Time) error {
	if remaining <= 0 {
		return nil
	}
	if o.Side == string(market.Buy) {
		return h.refund(ctx, tx, o, remaining*o.Price, now)
	}
	return tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: o.Item, Qty: remaining,
		From: o.OwnerID, FromHolding: application.HoldEscrow, To: o.OwnerID, ToHolding: application.HoldCarried,
		Reason: application.ItemMarketRelease, ReferenceType: "market_orders", ReferenceID: o.ID, At: now})
}

// refund moves money from an order owner's escrow back to its funding purse.
func (h *MarketHandler) refund(ctx context.Context, tx application.Tx, o application.MarketOrder, amount int64, now time.Time) error {
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, o.OwnerID)
	if err != nil {
		return err
	}
	kind := application.AccountPlayerCash
	if o.Funding == string(payment.Card) {
		kind = application.AccountPlayerBank
	}
	purse, err := tx.Ledger().AccountFor(ctx, kind, o.OwnerID)
	if err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: application.ReasonMarketRelease, ReferenceType: "market_orders", ReferenceID: o.ID,
		Entries: []application.LedgerEntry{
			{AccountID: escrow.ID, Amount: money.FromMinor(-amount)}, {AccountID: purse.ID, Amount: money.FromMinor(amount)},
		},
		CreatedAt: now,
	})
	return err
}

// Cancel handles market.cancel: the owner takes a resting order off the book
// and gets back what it still holds.
func (h *MarketHandler) Cancel(ctx context.Context, meta envelope.Metadata, req MarketRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	if err != nil {
		return nil, errors.InvalidInput("market order number is not a number")
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.OrderCancelledView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		o, err := tx.Market().OrderByNo(ctx, no)
		if isSentinel(err, application.ErrOrderNotFound) || (err == nil && o.OwnerID != p.ID) {
			return refuseMarket(screens.MarketRefusedNoOrder)
		}
		if err != nil {
			return err
		}
		if err := tx.Market().LockBook(ctx, o.CityID, o.Item); err != nil {
			return err
		}
		if o, err = tx.Market().Order(ctx, o.ID); err != nil {
			return err
		}
		if o.Status != application.OrderOpen {
			return refuseMarket(screens.MarketRefusedClosed)
		}
		now := h.now()
		if err := tx.Market().UpdateOrder(ctx, o.ID, o.Filled, application.OrderCancelled, &now); err != nil {
			return err
		}
		if o.Side == string(market.Sell) {
			if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
				return err
			}
		}
		if err := h.release(ctx, tx, *o, o.Qty-o.Filled, now); err != nil {
			return err
		}
		view = screens.OrderCancelledView{Item: itemNamed(snap, o.Item), Side: o.Side, No: o.No,
			Left: o.Qty - o.Filled, Refund: (o.Qty - o.Filled) * o.Price}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.OrderCancelled(h.screen(meta, lang), view), nil
}

// Mine handles market.mine: the player's orders.
func (h *MarketHandler) Mine(ctx context.Context, meta envelope.Metadata, req MarketRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.MyOrdersView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		orders, err := tx.Market().PlayerOrders(ctx, p.ID, 20)
		if err != nil {
			return err
		}
		for _, o := range orders {
			city, err := h.cities.ByID(ctx, o.CityID)
			if err != nil {
				return err
			}
			view.Orders = append(view.Orders, screens.OrderLine{No: o.No, Item: itemNamed(snap, o.Item), Side: o.Side,
				Qty: o.Qty, Filled: o.Filled, Price: o.Price, Status: o.Status, CityCode: city.Code, City: city.Name,
				ExpiresAt: o.ExpiresAt})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.MyOrders(h.screen(meta, lang), view), nil
}

// Expire ends a resting order whose time is up. It arrives from the
// SCHEDULER and runs exactly once: the key is derived from the order, and
// only an order still open moves.
func (h *MarketHandler) Expire(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	playerID, orderID, err := req.ids()
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(playerID, meta.Command, orderID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		o, err := tx.Market().Order(ctx, orderID)
		if isSentinel(err, application.ErrOrderNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tx.Market().LockBook(ctx, o.CityID, o.Item); err != nil {
			return err
		}
		if o, err = tx.Market().Order(ctx, orderID); err != nil {
			return err
		}
		now := h.now()
		if o.Status != application.OrderOpen {
			return nil
		}
		if now.Before(o.ExpiresAt) {
			return errors.Internal(stderrors.New("handlers: an order expired before its time"))
		}
		if err := tx.Market().UpdateOrder(ctx, o.ID, o.Filled, application.OrderExpired, &now); err != nil {
			return err
		}
		if o.Side == string(market.Sell) {
			if err := tx.Items().LockOwner(ctx, o.OwnerID); err != nil {
				return err
			}
		}
		if err := h.release(ctx, tx, *o, o.Qty-o.Filled, now); err != nil {
			return err
		}
		def, _ := snap.ItemDef(o.Item)
		return appendMarketEvent(ctx, tx, meta, "expired", o.OwnerID, map[string]any{
			"player_id": o.OwnerID, "side": o.Side, "item": o.Item, "item_name": def.Name,
			"left": o.Qty - o.Filled, "no": o.No,
		})
	})
}

// appendMarketEvent writes a market event to the outbox.
func appendMarketEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("market."+name, "player", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("market", name), Metadata: meta, Payload: ev.Payload,
	})
}
