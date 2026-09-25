package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/domain/shop"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// LeverSalesTax is the city's tax on a shop purchase, in basis points,
// charged on top of the price and paid into its treasury (governance.yml).
const LeverSalesTax = "city.sales_tax"

// ShopsHandler serves the city shops (shops.yml, internal/domain/shop): the
// shops of the player's city, one shop's shelves, buying at the counter and
// selling a good back.
//
// # Money and goods
//
// A purchase is paid from the purse the player chooses (payments.yml,
// service shop): the price leaves the economy into the NPC economy
// (shop_purchase) and the city's sales tax on it goes to the treasury
// (sales_tax). The goods enter the world named after that sale — the shop's
// sale IS their origin. Selling back is the reverse at a spread: the shop
// pays from the NPC economy (shop_buyback) into the player's cash and the
// goods leave the world.
type ShopsHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	dice    Dice

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewShopsHandler wires the handler. A missing dependency is a wiring mistake
// and panics.
func NewShopsHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, dice Dice,
	idempotencyTTL time.Duration, now func() time.Time,
) *ShopsHandler {
	if source == nil || cities == nil || policy == nil || ids == nil || dice == nil {
		panic("handlers: NewShopsHandler requires content, cities, policy, ids and dice")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 {
		panic("handlers: NewShopsHandler requires a game clock and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &ShopsHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy,
		scale: scale, dice: dice, idempotencyTTL: idempotencyTTL, now: now}
}

// ShopRequest names a shop, a good in it, a quantity, the way to pay and a
// one-time token.
type ShopRequest struct {
	Shop   string `json:"shop"`
	Item   string `json:"item"`
	Qty    string `json:"qty,omitempty"`
	Method string `json:"method,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
}

func (h *ShopsHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// shopRefusal carries a refused shop request out of a unit of work.
type shopRefusal struct{ view screens.ShopRefusalView }

func (r *shopRefusal) Error() string { return "handlers: shop refused: " + r.view.Kind }

func (h *ShopsHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *shopRefusal
	if stderrors.As(err, &r) {
		return screens.ShopRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return nil, err
}

func (h *ShopsHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	return id[:min(len(id), nonceLength)]
}

// where reads the player's city and place, refusing a player on the road.
func (h *ShopsHandler) where(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player) (whereabouts, error) {
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

// quote prices one good on a shop's shelf now: the shelf refilled, the
// demand-moved price, and what the shop would pay to buy one back.
func (h *ShopsHandler) quote(ctx context.Context, tx application.Tx, def content.ShopDef, sh content.ShelfDef,
	item content.ItemDef, cityID string, now time.Time,
) (screens.ShelfLine, shop.Shelf, error) {
	stored, err := tx.Shops().Shelf(ctx, cityID, def.Code, sh.Item)
	if err != nil {
		return screens.ShelfLine{}, shop.Shelf{}, err
	}
	shelf := sh.RestockRule().Refill(shop.Shelf{Stock: stored.Stock, RestockedAt: stored.RestockedAt}, now, h.scale)
	demand := def.Demand.Demand()
	recent, err := tx.Shops().RecentSales(ctx, cityID, def.Code, sh.Item, now.Add(-demand.Window))
	if err != nil {
		return screens.ShelfLine{}, shop.Shelf{}, err
	}
	base := sh.Price
	if base == 0 {
		base = item.BasePrice
	}
	price, err := shop.Price(money.FromMinor(base), demand, recent)
	if err != nil {
		return screens.ShelfLine{}, shop.Shelf{}, errors.Internal(err)
	}
	line := screens.ShelfLine{
		Item: named(item.Code, item.Name), Price: price.Minor(), Stock: shelf.Stock,
		Busy: demand.Multiplier(recent) > shop.BPS, NextRestock: sh.RestockRule().NextRestock(shelf, h.scale),
	}
	if sh.BuybackBPS > 0 {
		back, err := shop.Buyback(price, sh.BuybackBPS)
		if err != nil {
			return line, shelf, errors.Internal(err)
		}
		line.Buyback = back.Minor()
	}
	return line, shelf, nil
}

// List handles shop.list: the shops of the player's city.
func (h *ShopsHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ShopsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		w, err := h.where(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		view.CityCode, view.City = w.city.Code, w.city.Name
		for _, s := range snap.CityShops(w.city.Code) {
			view.Shops = append(view.Shops, screens.ShopLine{
				Shop: named(s.Code, s.Name), Place: placeNamed(snap, s.Place),
				Here: w.walk == nil && s.Place == w.here.Code,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.Shops(h.screen(meta, lang), view), nil
}

// View handles shop.view: one shop's shelves, priced now.
func (h *ShopsHandler) View(ctx context.Context, meta envelope.Metadata, req ShopRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ShopView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		w, err := h.where(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		def, ok := h.shopIn(snap, w, req.Shop)
		if !ok {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNoShop}}
		}
		now := h.now()
		view = screens.ShopView{Shop: named(def.Code, def.Name), Place: placeNamed(snap, def.Place),
			Here: w.walk == nil && def.Place == w.here.Code}
		if pl, ok := w.cmap.Find(def.Place); ok && !view.Here && w.walk == nil {
			view.Walk = h.scale.RealWait(pl.MoveTime)
		}
		for _, sh := range def.Shelves {
			item, _ := snap.ItemDef(sh.Item)
			line, _, err := h.quote(ctx, tx, def, sh, item, w.city.ID, now)
			if err != nil {
				return err
			}
			view.Shelves = append(view.Shelves, line)
		}
		if tax, err := h.policy.Get(ctx, w.city.JurisdictionID, LeverSalesTax); err == nil {
			view.TaxBPS = int(tax.Value)
		} else {
			return err
		}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.ShopDetail(h.screen(meta, lang), view), nil
}

// shopIn finds a shop the player's city has.
func (h *ShopsHandler) shopIn(snap *content.Snapshot, w whereabouts, code string) (content.ShopDef, bool) {
	for _, s := range snap.CityShops(w.city.Code) {
		if s.Code == code {
			return s, true
		}
	}
	return content.ShopDef{}, false
}

// atCounter refuses unless the player stands at the shop's place.
func (h *ShopsHandler) atCounter(w whereabouts, snap *content.Snapshot, def content.ShopDef, now time.Time) error {
	target, ok := w.cmap.Find(def.Place)
	if !ok {
		return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNoShop}}
	}
	err := needAt(w, snap, target, "place.need.shop", nil, h.scale, now)
	if n, ok := err.(*notHere); ok {
		n.view.Shop = named(def.Code, def.Name)
	}
	return thenFor(err, "shop.view", def.Code)
}

// Buy handles shop.buy: a checkout without a way to pay, the purchase with
// one.
func (h *ShopsHandler) Buy(ctx context.Context, meta envelope.Metadata, req ShopRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	qty := parseQty(req.Qty)
	var (
		checkout *screens.ShopCheckoutView
		bought   screens.ShopBoughtView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if chosen {
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
		w, err := h.where(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		def, ok := h.shopIn(snap, w, req.Shop)
		if !ok {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNoShop}}
		}
		sh, ok := def.Shelf(req.Item)
		item, ok2 := snap.ItemDef(req.Item)
		if !ok || !ok2 {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNotSold, Shop: named(def.Code, def.Name)}}
		}
		if err := h.atCounter(w, snap, def, now); err != nil {
			return err
		}
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		line, shelf, err := h.quote(ctx, tx, def, sh, item, w.city.ID, now)
		if err != nil {
			return err
		}
		if shelf.Stock < qty {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedSoldOut, Shop: named(def.Code, def.Name),
				Item: named(item.Code, item.Name), Stock: shelf.Stock, NextRestock: line.NextRestock}}
		}
		unit := money.FromMinor(line.Price)
		total, err := shop.Total(unit, qty)
		if err != nil {
			return errors.Internal(err)
		}
		taxLever, err := h.policy.Get(ctx, w.city.JurisdictionID, LeverSalesTax)
		if err != nil {
			return err
		}
		tax, err := shop.SalesTax(total, int(taxLever.Value))
		if err != nil {
			return errors.Internal(err)
		}
		due, err := total.Add(tax)
		if err != nil {
			return errors.Internal(err)
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(due, snap.ShopAccepts(def.Code))
		if !chosen {
			checkout = &screens.ShopCheckoutView{
				Shop: named(def.Code, def.Name), Item: named(item.Code, item.Name), Qty: qty, Unit: line.Price,
				Total: total.Minor(), Tax: tax.Minor(), TaxBPS: int(taxLever.Value), Stock: shelf.Stock,
				Payment: paymentChoice(plan, wallet), Nonce: h.nonce(),
			}
			return nil
		}
		back := []string{screens.AddrShop, def.Code}
		if err := checkMethod(plan, method, wallet, "shop.button.back_to_shop", back...); err != nil {
			return err
		}

		saleID := h.ids.NewID()
		// Stock first: the shelf row is the lock two buyers of its last
		// unit meet at.
		taken, err := shelf.Take(qty)
		if err != nil {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedSoldOut, Shop: named(def.Code, def.Name),
				Item: named(item.Code, item.Name), Stock: shelf.Stock}}
		}
		if err := tx.Shops().SaveShelf(ctx, application.ShopShelf{CityID: w.city.ID, Shop: def.Code, Item: item.Code,
			Stock: taken.Stock, RestockedAt: taken.RestockedAt}); err != nil {
			return err
		}
		txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
			Method: method, Accepted: plan.Accepted, Reason: application.ReasonShopPurchase,
			ReferenceType: "shop_sales", ReferenceID: saleID,
			To:        []application.LedgerEntry{{AccountID: application.SystemSinkAccountID, Amount: total}},
			CreatedAt: now,
		})
		if err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "shop.button.back_to_shop", back...)
			}
			return err
		}
		if !tax.IsZero() {
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, w.city.ID)
			if err != nil {
				return err
			}
			if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
				Method: method, Accepted: plan.Accepted, Reason: application.ReasonSalesTax,
				ReferenceType: "shop_sales", ReferenceID: saleID,
				To:        []application.LedgerEntry{{AccountID: treasury.ID, Amount: tax}},
				CreatedAt: now,
			}); err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "shop.button.back_to_shop", back...)
				}
				return err
			}
		}
		if err := tx.Shops().RecordSale(ctx, application.ShopSale{
			ID: saleID, PlayerID: p.ID, CityID: w.city.ID, Shop: def.Code, Item: item.Code, Direction: application.SaleBuy,
			Qty: qty, UnitPrice: line.Price, Total: total.Minor(), Tax: tax.Minor(), Method: string(method),
			LedgerTransactionID: txID, At: now,
		}); err != nil {
			return err
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		if _, err := bring(ctx, tx, snap, h.ids, h.dice, p.ID, item.Code, qty, -1,
			origin{kind: application.OriginSupply, reason: application.ItemShopPurchase, refType: "shop_sales", refID: saleID}, now); err != nil {
			return err
		}
		bought = screens.ShopBoughtView{Shop: named(def.Code, def.Name), Item: named(item.Code, item.Name), Qty: qty,
			Total: total.Minor(), Tax: tax.Minor(), Method: string(method)}
		return appendItemEvent(ctx, tx, meta, "bought", p.ID, map[string]any{
			"player_id": p.ID, "shop": def.Code, "item": item.Code, "qty": qty, "total": total.Minor(),
			"tax": tax.Minor(), "method": string(method), "sale_id": saleID, "ledger_transaction_id": txID,
			"content_version": snap.Version(),
		})
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	switch {
	case replayed:
		return h.View(ctx, meta, ShopRequest{Shop: req.Shop})
	case checkout != nil:
		return screens.ShopCheckout(h.screen(meta, lang), *checkout), nil
	}
	return screens.ShopBought(h.screen(meta, lang), bought), nil
}

// Offers handles shop.offers: the shops of the city that buy a good the
// player carries, and what each pays.
func (h *ShopsHandler) Offers(ctx context.Context, meta envelope.Metadata, req ShopRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.SellOffersView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		w, err := h.where(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		code, qty, _, err := held(ctx, tx, p.ID, req.Item)
		if err != nil {
			return err
		}
		if qty == 0 {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNotHeld, Item: itemNamed(snap, req.Item)}}
		}
		item, _ := snap.ItemDef(code)
		view = screens.SellOffersView{Item: named(item.Code, item.Name), Ref: req.Item}
		now := h.now()
		for _, def := range snap.CityShops(w.city.Code) {
			sh, ok := def.Shelf(code)
			if !ok || sh.BuybackBPS == 0 {
				continue
			}
			line, _, err := h.quote(ctx, tx, def, sh, item, w.city.ID, now)
			if err != nil {
				return err
			}
			view.Offers = append(view.Offers, screens.SellOffer{Shop: named(def.Code, def.Name),
				Place: placeNamed(snap, def.Place), Price: line.Buyback})
		}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.SellOffers(h.screen(meta, lang), view), nil
}

// Sell handles shop.sell: one unit or piece sold back to a shop, at its
// counter, for cash.
func (h *ShopsHandler) Sell(ctx context.Context, meta envelope.Metadata, req ShopRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ShopSoldView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		w, err := h.where(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		def, ok := h.shopIn(snap, w, req.Shop)
		if !ok {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNoShop}}
		}
		if err := h.atCounter(w, snap, def, now); err != nil {
			return err
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		code, qty, piece, err := held(ctx, tx, p.ID, req.Item)
		if err != nil {
			return err
		}
		if qty == 0 {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNotHeld, Item: itemNamed(snap, req.Item)}}
		}
		sh, ok := def.Shelf(code)
		item, _ := snap.ItemDef(code)
		if !ok || sh.BuybackBPS == 0 {
			return &shopRefusal{view: screens.ShopRefusalView{Kind: screens.ShopRefusedNoBuyback, Shop: named(def.Code, def.Name),
				Item: named(item.Code, item.Name)}}
		}
		line, _, err := h.quote(ctx, tx, def, sh, item, w.city.ID, now)
		if err != nil {
			return err
		}
		saleID := h.ids.NewID()
		move := application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: 1, From: p.ID, FromHolding: application.HoldCarried,
			Reason: application.ItemShopSale, ReferenceType: "shop_sales", ReferenceID: saleID, At: now}
		if piece != nil {
			move.PieceID = piece.ID
		}
		if err := tx.Items().Move(ctx, move); err != nil {
			return err
		}
		var txID string
		if line.Buyback > 0 {
			cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
			if err != nil {
				return err
			}
			if txID, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
				Reason: application.ReasonShopBuyback, ReferenceType: "shop_sales", ReferenceID: saleID,
				Entries: []application.LedgerEntry{
					{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-line.Buyback)},
					{AccountID: cash.ID, Amount: money.FromMinor(line.Buyback)},
				},
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		if err := tx.Shops().RecordSale(ctx, application.ShopSale{
			ID: saleID, PlayerID: p.ID, CityID: w.city.ID, Shop: def.Code, Item: code, Direction: application.SaleSell,
			Qty: 1, UnitPrice: line.Buyback, Total: line.Buyback, LedgerTransactionID: txID, At: now,
		}); err != nil {
			return err
		}
		view = screens.ShopSoldView{Shop: named(def.Code, def.Name), Item: named(item.Code, item.Name), Price: line.Buyback,
			Left: qty - 1}
		return appendItemEvent(ctx, tx, meta, "sold", p.ID, map[string]any{
			"player_id": p.ID, "shop": def.Code, "item": code, "qty": 1, "total": line.Buyback, "sale_id": saleID,
		})
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	if view.Item.Code == "" {
		return h.List(ctx, meta)
	}
	return screens.ShopSold(h.screen(meta, lang), view), nil
}

// isUniqueForm reports whether a good is held as pieces.
func isUniqueForm(def content.ItemDef) bool { return def.Form == string(inventory.Unique) }

var (
	_ = place.ServiceMarket
	_ = payment.Cash
)
