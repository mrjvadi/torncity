package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/life"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/domain/property"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// PropertyHandler serves property (docs/adr/0024-property-and-politics.md):
// a city's market — the units its land registry sells and its owners'
// offers — buying from the city and from a player, offering a property for
// sale or to let, taking a lease and leaving it, a player's own property,
// resting at home; and, in each city period's settlement (CityHandler),
// every property's upkeep and tax and every lease's rent, exactly once.
//
// A deed moves in the same transaction as the money that paid for it, under
// the property's and the offer's row locks, and only from an open offer. A
// home — bought or rented — makes its city the player's residence: where
// they vote and stand, pay income tax and may take its jobs. Losing a home
// does not take the residence away; buying or renting one elsewhere moves it.
type PropertyHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	rules   PropertyRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// PropertyRules is the tuning of property (config property.*).
type PropertyRules struct {
	ForeclosurePeriods int
	EvictionPeriods    int
	MaxOwned           int
	MaxPrice, MaxRent  int64
	// RestCooldown is GAME time.
	RestCooldown time.Duration
	// ListSize is how many offers the market lists.
	ListSize int
}

// LeverPropertyTax is the city's property tax: bps of a property's value,
// every city period.
const LeverPropertyTax = "city.property_tax"

// NewPropertyHandler builds the handler.
func NewPropertyHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, rules PropertyRules,
	idempotencyTTL time.Duration, now func() time.Time,
) *PropertyHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewPropertyHandler requires content, cities, a policy reader and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.ForeclosurePeriods < 1 || rules.EvictionPeriods < 1 ||
		rules.MaxOwned < 1 || rules.MaxPrice < 1 || rules.MaxRent < 1 || rules.RestCooldown <= 0 || rules.ListSize < 1 {
		panic("handlers: NewPropertyHandler requires a game clock, an idempotency ttl and valid rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PropertyHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy,
		scale: scale, rules: rules, idempotencyTTL: idempotencyTTL, now: now}
}

// PropertyRequest is the payload of the property commands; which fields a
// command reads is its own.
type PropertyRequest struct {
	Type    string `json:"type,omitempty"`
	No      string `json:"no,omitempty"`
	Method  string `json:"method,omitempty"`
	Price   string `json:"price,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

func (h *PropertyHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// propertyRefusal carries a refused request out of a unit of work.
type propertyRefusal struct{ view screens.PropertyRefusalView }

func (r *propertyRefusal) Error() string { return "handlers: property refused: " + r.view.Kind }

func refuseProperty(kind string, back ...string) *propertyRefusal {
	return &propertyRefusal{view: screens.PropertyRefusalView{Kind: kind, Back: back}}
}

func (h *PropertyHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *propertyRefusal
	if stderrors.As(err, &r) {
		return screens.PropertyRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	switch {
	case isSentinel(err, application.ErrPropertyNotFound):
		return screens.PropertyRefusal(h.screen(meta, lang), screens.PropertyRefusalView{Kind: screens.PropertyRefusedNotFound}), nil
	case isSentinel(err, application.ErrOfferNotFound):
		return screens.PropertyRefusal(h.screen(meta, lang), screens.PropertyRefusalView{Kind: screens.PropertyRefusedTaken,
			Back: []string{screens.AddrPropertyMarket}}), nil
	}
	return nil, err
}

func (h *PropertyHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

func (h *PropertyHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// hereCity is the city the player stands in, nil when travelling or
// nowhere.
func (h *PropertyHandler) hereCity(ctx context.Context, tx application.Tx, p *application.Player) (*application.City, error) {
	if p.CityID == nil || *p.CityID == "" {
		return nil, nil
	}
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return nil, nil
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return nil, err
	}
	return h.cities.ByID(ctx, *p.CityID)
}

// taxRate is a city's property tax, through the resolver.
func (h *PropertyHandler) taxRate(ctx context.Context, city *application.City) (int64, error) {
	if city.JurisdictionID == "" {
		return 0, nil
	}
	v, err := h.policy.Get(ctx, city.JurisdictionID, LeverPropertyTax)
	if err != nil {
		return 0, err
	}
	return v.Value, nil
}

// cityPrice is what a city asks for one more unit of a type now, and how
// many it has left.
func cityPrice(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City,
	t content.PropertyTypeDef,
) (price int64, left int, err error) {
	def, _ := snap.Property()
	market, ok := snap.PropertyMarket(city.Code)
	if !ok {
		return 0, 0, nil
	}
	sold, err := tx.Property().Owned(ctx, city.ID, t.Code)
	if err != nil {
		return 0, 0, err
	}
	return property.CityPrice(t.Type(), market.PriceBPS, sold, def.Demand()), max(market.Stock[t.Code]-sold, 0), nil
}

func govCity(c *application.City) screens.GovPlace {
	return screens.GovPlace{Kind: "city", Code: c.Code, Name: c.Name}
}

func propertyTypeNamed(t content.PropertyTypeDef) screens.Named { return named(t.Code, t.Name) }

// ---------------------------------------------------------------------------
// The market.

// Market handles property.list: what the city the player stands in sells,
// and its owners' offers.
func (h *PropertyHandler) Market(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.PropertyMarketView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		city, err := h.hereCity(ctx, tx, p)
		if err != nil {
			return err
		}
		if city == nil {
			view.NoCity = true
			return nil
		}
		view.City = govCity(city)
		if _, ok := snap.PropertyMarket(city.Code); ok {
			for _, t := range snap.PropertyTypes() {
				price, left, err := cityPrice(ctx, tx, snap, city, t)
				if err != nil {
					return err
				}
				view.Types = append(view.Types, screens.PropertyTypeLine{Type: propertyTypeNamed(t), Kind: t.Kind,
					Size: t.Size, Quality: t.Quality, Price: price, Left: left, Home: t.Home})
			}
		}
		offers, err := tx.Property().Listings(ctx, city.ID, h.rules.ListSize)
		if err != nil {
			return err
		}
		for _, o := range offers {
			line, err := h.offerLine(ctx, tx, snap, o, p.ID)
			if err != nil {
				return err
			}
			view.Offers = append(view.Offers, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.PropertyMarket(h.screen(meta, lang), view), nil
}

// offerLine is one offer for a screen.
func (h *PropertyHandler) offerLine(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	o application.PropertyListing, viewerID string,
) (screens.PropertyOfferLine, error) {
	pr, err := tx.Property().ByID(ctx, o.PropertyID, false)
	if err != nil {
		return screens.PropertyOfferLine{}, err
	}
	t, _ := snap.PropertyType(pr.TypeCode)
	seller, err := playerNamed(ctx, tx, o.SellerID)
	if err != nil {
		return screens.PropertyOfferLine{}, err
	}
	return screens.PropertyOfferLine{No: o.No, Kind: o.Kind, Type: named(pr.TypeCode, t.Name), PropertyNo: pr.No,
		Price: o.Price, Seller: seller, Mine: o.SellerID == viewerID}, nil
}

// Type handles property.type: one kind of property the city sells, its
// price, and the ways to pay at the land registry.
func (h *PropertyHandler) Type(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	return h.typeView(ctx, meta, req, 0)
}

func (h *PropertyHandler) typeView(ctx context.Context, meta envelope.Metadata, req PropertyRequest, bought int64,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.PropertyTypeView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		view, err = h.typeOffer(ctx, tx, snap, p, strings.TrimSpace(req.Type))
		view.Bought = bought
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.PropertyType(h.screen(meta, lang), view), nil
}

// typeOffer is what the city offers of one type to a player now.
func (h *PropertyHandler) typeOffer(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	p *application.Player, code string,
) (screens.PropertyTypeView, error) {
	var view screens.PropertyTypeView
	t, ok := snap.PropertyType(code)
	if !ok {
		return view, refuseProperty(screens.PropertyRefusedNotFound, screens.AddrPropertyMarket)
	}
	city, err := h.hereCity(ctx, tx, p)
	if err != nil {
		return view, err
	}
	if city == nil {
		return view, refuseProperty(screens.PropertyRefusedNotInCity, screens.AddrPropertyMarket)
	}
	price, left, err := cityPrice(ctx, tx, snap, city, t)
	if err != nil {
		return view, err
	}
	rate, err := h.taxRate(ctx, city)
	if err != nil {
		return view, err
	}
	view = screens.PropertyTypeView{City: govCity(city), Type: propertyTypeNamed(t), Kind: t.Kind, Size: t.Size,
		Quality: t.Quality, Upkeep: t.Upkeep, Home: t.Home, RestEnergy: t.RestEnergy, Place: placeNamed(snap, t.Place),
		Price: price, Left: left, TaxBPS: rate, Max: h.rules.MaxOwned}
	owned, err := tx.Property().OwnedBy(ctx, p.ID)
	if err != nil {
		return view, err
	}
	switch {
	case left <= 0:
		view.Blocked = screens.PropertyRefusedSoldOut
		return view, nil
	case owned >= h.rules.MaxOwned:
		view.Blocked = screens.PropertyRefusedTooMany
		return view, nil
	}
	w, err := locate(ctx, tx, h.cities, snap, p)
	if err != nil {
		return view, err
	}
	if way := wayTo(w, snap, place.ServiceCityHall, h.scale); way != nil {
		view.Way = way
		return view, nil
	}
	wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
	if err != nil {
		return view, err
	}
	choice := paymentChoice(wallet.Plan(money.FromMinor(price), snap.Accepts(content.ServiceProperty)), wallet)
	view.Payment = &choice
	return view, nil
}

// Purchase handles property.purchase: one unit bought from the city at the
// land registry, once — the stock locked, the price read again, the money
// into the treasury and the deed to the buyer in one transaction. A home
// makes its city the buyer's residence.
func (h *PropertyHandler) Purchase(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	method, err := payment.Parse(req.Method)
	if err != nil {
		return h.Type(ctx, meta, req)
	}
	snap := h.content.Current()
	code := strings.TrimSpace(req.Type)
	lang := meta.Language
	var (
		bought  int64
		replay  bool
		refusal error
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replay = true
			return nil
		}
		now := h.now()
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		t, ok := snap.PropertyType(code)
		if !ok {
			return refuseProperty(screens.PropertyRefusedNotFound, screens.AddrPropertyMarket)
		}
		city, err := h.hereCity(ctx, tx, p)
		if err != nil {
			return err
		}
		if city == nil {
			return refuseProperty(screens.PropertyRefusedNotInCity, screens.AddrPropertyMarket)
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if err := needService(w, snap, place.ServiceCityHall, h.scale, now); err != nil {
			return thenFor(err, "property.type", code)
		}
		if err := tx.Property().LockStock(ctx, city.ID, code); err != nil {
			return err
		}
		price, left, err := cityPrice(ctx, tx, snap, city, t)
		if err != nil {
			return err
		}
		if left <= 0 {
			return refuseProperty(screens.PropertyRefusedSoldOut, screens.AddrPropertyType, code)
		}
		owned, err := tx.Property().OwnedBy(ctx, p.ID)
		if err != nil {
			return err
		}
		if owned >= h.rules.MaxOwned {
			refusal = &propertyRefusal{view: screens.PropertyRefusalView{Kind: screens.PropertyRefusedTooMany,
				Max: int64(h.rules.MaxOwned), Back: []string{screens.AddrPropertyMine}}}
			return refusal
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(money.FromMinor(price), snap.Accepts(content.ServiceProperty))
		if err := checkMethod(plan, method, wallet, "property.button.back_to_type", screens.AddrPropertyType, code); err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return err
		}
		id := h.ids.NewID()
		if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{Method: method, Accepted: plan.Accepted,
			Reason: application.ReasonPropertyPurchase, ReferenceType: application.PropertyReference, ReferenceID: id,
			To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: money.FromMinor(price)}}, CreatedAt: now}); err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "property.button.back_to_type", screens.AddrPropertyType, code)
			}
			return err
		}
		stored, err := tx.Property().Create(ctx, application.Property{ID: id, CityID: city.ID, TypeCode: code, OwnerID: p.ID,
			Value: price, AcquiredAt: now, ContentVersion: snap.Version()})
		if err != nil {
			return err
		}
		bought = stored.No
		if t.Home {
			if err := h.settleIn(ctx, tx, p.ID, city.ID, now); err != nil {
				return err
			}
		}
		return appendDomainEvent(ctx, tx, meta, "property", "bought", id, map[string]any{
			"player_id": p.ID, "property_no": stored.No, "type": code, "city_id": city.ID, "price": price, "from": "city",
			"home": t.Home})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replay {
		return h.Mine(ctx, meta)
	}
	return h.typeView(ctx, meta, PropertyRequest{Type: code}, bought)
}

// settleIn makes a city the player's residence, from now, unless it already
// is.
func (h *PropertyHandler) settleIn(ctx context.Context, tx application.Tx, playerID, cityID string, now time.Time) error {
	home, err := tx.Employment().ResidenceCityID(ctx, playerID)
	if err != nil || home == cityID {
		return err
	}
	return tx.Property().SetResidence(ctx, playerID, cityID, now)
}

// ---------------------------------------------------------------------------
// The player's own.

// Mine handles property.mine: what the player owns, the home they rent,
// where they live, and whether they may rest at home.
func (h *PropertyHandler) Mine(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.mine(ctx, meta, "", nil)
}

func (h *PropertyHandler) mine(ctx context.Context, meta envelope.Metadata, notice string, args map[string]any,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	view := screens.PropertyMineView{Grace: h.rules.ForeclosurePeriods, Notice: notice, NoticeArgs: args}
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		owned, err := tx.Property().OfOwner(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, pr := range owned {
			line, err := h.propertyLine(ctx, tx, snap, pr)
			if err != nil {
				return err
			}
			view.Owned = append(view.Owned, line)
		}
		lease, err := tx.Property().TenantLease(ctx, p.ID)
		if err != nil {
			return err
		}
		if lease != nil {
			pr, err := tx.Property().ByID(ctx, lease.PropertyID, false)
			if err != nil {
				return err
			}
			line, err := h.propertyLine(ctx, tx, snap, *pr)
			if err != nil {
				return err
			}
			landlord, err := playerNamed(ctx, tx, lease.LandlordID)
			if err != nil {
				return err
			}
			view.Rented = &screens.RentedHomeLine{LeaseNo: lease.No, Property: line, Landlord: landlord, Rent: lease.Rent,
				Arrears: lease.Arrears}
		}
		home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
		if err != nil {
			return err
		}
		if home != "" {
			if c, err := h.cities.ByID(ctx, home); err == nil {
				view.Residence = govCity(c)
			}
		}
		now := h.now()
		t, _, ok, err := h.homeHere(ctx, tx, snap, p)
		if err != nil || !ok {
			return err
		}
		view.CanRest, view.RestEnergy = true, t.RestEnergy
		wait, err := h.restWait(ctx, tx, p.ID, now)
		view.RestIn = wait
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.PropertyMine(h.screen(meta, lang), view), nil
}

// propertyLine is one owned property for a screen.
func (h *PropertyHandler) propertyLine(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	pr application.Property,
) (screens.PropertyLine, error) {
	t, _ := snap.PropertyType(pr.TypeCode)
	city, err := h.cities.ByID(ctx, pr.CityID)
	if err != nil {
		return screens.PropertyLine{}, err
	}
	line := screens.PropertyLine{No: pr.No, Type: named(pr.TypeCode, t.Name), Kind: t.Kind, Size: t.Size,
		Quality: t.Quality, City: govCity(city), Value: pr.Value, Debt: pr.Debt(), UnpaidPeriods: pr.UnpaidPeriods,
		Home: t.Home}
	offer, err := tx.Property().ListingOf(ctx, pr.ID)
	if err != nil {
		return line, err
	}
	if offer != nil {
		line.Offer = &screens.PropertyOfferLine{No: offer.No, Kind: offer.Kind, Type: line.Type, PropertyNo: pr.No,
			Price: offer.Price, Mine: true}
	}
	lease, err := tx.Property().LeaseOf(ctx, pr.ID)
	if err != nil {
		return line, err
	}
	if lease != nil {
		tenant, err := playerNamed(ctx, tx, lease.TenantID)
		if err != nil {
			return line, err
		}
		line.Tenant, line.Rent, line.Arrears = &tenant, lease.Rent, lease.Arrears
	}
	return line, nil
}

// homeHere is the home the player may rest at in the city they stand in:
// one they own and have not let, or the one they rent. ok is false for
// none.
func (h *PropertyHandler) homeHere(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
) (content.PropertyTypeDef, *application.Property, bool, error) {
	city, err := h.hereCity(ctx, tx, p)
	if err != nil || city == nil {
		return content.PropertyTypeDef{}, nil, false, err
	}
	var best *application.Property
	var bestType content.PropertyTypeDef
	consider := func(pr application.Property) {
		t, ok := snap.PropertyType(pr.TypeCode)
		if ok && t.Home && pr.CityID == city.ID && (best == nil || t.RestEnergy > bestType.RestEnergy) {
			cp := pr
			best, bestType = &cp, t
		}
	}
	owned, err := tx.Property().OfOwner(ctx, p.ID)
	if err != nil {
		return bestType, nil, false, err
	}
	for _, pr := range owned {
		lease, err := tx.Property().LeaseOf(ctx, pr.ID)
		if err != nil {
			return bestType, nil, false, err
		}
		if lease == nil {
			consider(pr)
		}
	}
	lease, err := tx.Property().TenantLease(ctx, p.ID)
	if err != nil {
		return bestType, nil, false, err
	}
	if lease != nil {
		pr, err := tx.Property().ByID(ctx, lease.PropertyID, false)
		if err != nil {
			return bestType, nil, false, err
		}
		consider(*pr)
	}
	return bestType, best, best != nil, nil
}

// restWait is how long until the player may rest at home again, zero for
// now.
func (h *PropertyHandler) restWait(ctx context.Context, tx application.Tx, playerID string, now time.Time) (time.Duration, error) {
	last, err := tx.Property().RestedAt(ctx, playerID)
	if err != nil || last == nil {
		return 0, err
	}
	return max(last.Add(h.scale.RealWait(h.rules.RestCooldown)).Sub(now), 0), nil
}

// View handles property.view: one of the player's properties and what they
// can do with it.
func (h *PropertyHandler) View(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	return h.view(ctx, meta, req, "")
}

func (h *PropertyHandler) view(ctx context.Context, meta envelope.Metadata, req PropertyRequest, notice string,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	if !ok {
		return h.Mine(ctx, meta)
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.PropertyView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		pr, err := tx.Property().ByNo(ctx, no, false)
		if err != nil {
			return err
		}
		if pr.OwnerID != p.ID {
			return refuseProperty(screens.PropertyRefusedNotYours)
		}
		line, err := h.propertyLine(ctx, tx, snap, *pr)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, pr.CityID)
		if err != nil {
			return err
		}
		rate, err := h.taxRate(ctx, city)
		if err != nil {
			return err
		}
		t, _ := snap.PropertyType(pr.TypeCode)
		view = screens.PropertyView{Property: line, Place: placeNamed(snap, t.Place), Upkeep: t.Upkeep, TaxBPS: rate,
			MaxPrice: h.rules.MaxPrice, MaxRent: h.rules.MaxRent, Notice: notice}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Property(h.screen(meta, lang), view), nil
}

// Sell handles property.sell and Let property.let: the owner offers a
// property for sale at a price, or to let at a rent per period. A property
// in debt, let, or already offered cannot be.
func (h *PropertyHandler) Sell(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	return h.offer(ctx, meta, req, application.OfferSale)
}

// Let handles property.let; see Sell.
func (h *PropertyHandler) Let(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	return h.offer(ctx, meta, req, application.OfferRent)
}

func (h *PropertyHandler) offer(ctx context.Context, meta envelope.Metadata, req PropertyRequest, kind string,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	if !ok {
		return h.Mine(ctx, meta)
	}
	amount, err := bank.ParseAmount(req.Price)
	if err != nil || amount.IsNegative() || amount.IsZero() {
		return nil, errors.InvalidInput("property: an offer names no price")
	}
	price := amount.Minor()
	limit := h.rules.MaxPrice
	if kind == application.OfferRent {
		limit = h.rules.MaxRent
	}
	lang := meta.Language
	back := []string{screens.AddrProperty, strconv.FormatInt(no, 10)}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		if price > limit {
			return &propertyRefusal{view: screens.PropertyRefusalView{Kind: screens.PropertyRefusedPrice, Max: limit, Back: back}}
		}
		pr, err := tx.Property().ByNo(ctx, no, true)
		if err != nil {
			return err
		}
		if pr.OwnerID != p.ID {
			return refuseProperty(screens.PropertyRefusedNotYours)
		}
		if pr.Debt() > 0 {
			return refuseProperty(screens.PropertyRefusedInDebt, back...)
		}
		lease, err := tx.Property().LeaseOf(ctx, pr.ID)
		if err != nil {
			return err
		}
		if lease != nil {
			return refuseProperty(screens.PropertyRefusedLet, back...)
		}
		_, err = tx.Property().OpenListing(ctx, application.PropertyListing{ID: h.ids.NewID(), PropertyID: pr.ID,
			SellerID: p.ID, Kind: kind, Price: price, CreatedAt: h.now()})
		if isSentinel(err, application.ErrOfferExists) {
			return refuseProperty(screens.PropertyRefusedOffered, back...)
		}
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.view(ctx, meta, PropertyRequest{No: strconv.FormatInt(no, 10)}, screens.PropertyNoticeListed)
}

// Cancel handles property.cancel: the owner withdraws a property's offer.
func (h *PropertyHandler) Cancel(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	if !ok {
		return h.Mine(ctx, meta)
	}
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		pr, err := tx.Property().ByNo(ctx, no, true)
		if err != nil {
			return err
		}
		if pr.OwnerID != p.ID {
			return refuseProperty(screens.PropertyRefusedNotYours)
		}
		offer, err := tx.Property().ListingOf(ctx, pr.ID)
		if err != nil || offer == nil {
			return err
		}
		_, err = tx.Property().CloseListing(ctx, offer.ID, application.OfferCancelled, "", h.now())
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.view(ctx, meta, PropertyRequest{No: strconv.FormatInt(no, 10)}, screens.PropertyNoticeCancelled)
}

// ---------------------------------------------------------------------------
// Offers.

// Offer handles property.offer: one owner's offer, and the ways to take it
// at the land registry.
func (h *PropertyHandler) Offer(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	if !ok {
		return h.Market(ctx, meta)
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.PropertyOfferView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		o, err := tx.Property().ListingByNo(ctx, no, false)
		if err != nil {
			return err
		}
		pr, err := tx.Property().ByID(ctx, o.PropertyID, false)
		if err != nil {
			return err
		}
		t, _ := snap.PropertyType(pr.TypeCode)
		line, err := h.offerLine(ctx, tx, snap, *o, p.ID)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, pr.CityID)
		if err != nil {
			return err
		}
		view = screens.PropertyOfferView{Offer: line, City: govCity(city), Kind: t.Kind, Size: t.Size, Quality: t.Quality,
			Upkeep: t.Upkeep, Home: t.Home, Max: h.rules.MaxOwned}
		if view.Blocked, err = h.offerBlocked(ctx, tx, o, p); err != nil || view.Blocked != "" {
			return err
		}
		here, err := h.hereCity(ctx, tx, p)
		if err != nil {
			return err
		}
		if here == nil || here.ID != pr.CityID {
			view.Blocked = screens.PropertyRefusedNotInCity
			return nil
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if way := wayTo(w, snap, place.ServiceCityHall, h.scale); way != nil {
			view.Way = way
			return nil
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		choice := paymentChoice(wallet.Plan(money.FromMinor(o.Price), snap.Accepts(content.ServiceProperty)), wallet)
		view.Payment = &choice
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.PropertyOffer(h.screen(meta, lang), view), nil
}

// offerBlocked says why a player may not take an offer, "" when they may.
func (h *PropertyHandler) offerBlocked(ctx context.Context, tx application.Tx, o *application.PropertyListing,
	p *application.Player,
) (string, error) {
	switch {
	case o.Status != application.OfferOpen:
		return screens.PropertyRefusedTaken, nil
	case o.SellerID == p.ID:
		return screens.PropertyRefusedOwn, nil
	}
	if o.Kind == application.OfferRent {
		lease, err := tx.Property().TenantLease(ctx, p.ID)
		if err != nil || lease != nil {
			return screens.PropertyRefusedRenting, err
		}
		return "", nil
	}
	owned, err := tx.Property().OwnedBy(ctx, p.ID)
	if err != nil || owned >= h.rules.MaxOwned {
		return screens.PropertyRefusedTooMany, err
	}
	return "", nil
}

// takeOffer runs what taking an offer has in common: the player, the
// command's key, the offer and its property locked and checked, the player
// at the land registry of its city. It returns nil values for a replay.
func (h *PropertyHandler) takeOffer(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	lang *string, no int64, kind string, now time.Time,
) (*application.Player, *application.PropertyListing, *application.Property, *application.City, error) {
	p, err := h.player(ctx, tx, meta, lang)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	fresh, err := h.reserve(ctx, tx, p.ID, meta)
	if err != nil || !fresh {
		return nil, nil, nil, nil, err
	}
	if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
		return nil, nil, nil, nil, err
	}
	o, err := tx.Property().ListingByNo(ctx, no, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// Lock order everywhere: the city's clock, the property, the offer.
	pr, err := tx.Property().ByID(ctx, o.PropertyID, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if _, err := tx.CityPeriods().Clock(ctx, pr.CityID, now); err != nil {
		return nil, nil, nil, nil, err
	}
	if pr, err = tx.Property().ByID(ctx, o.PropertyID, true); err != nil {
		return nil, nil, nil, nil, err
	}
	if o, err = tx.Property().ListingByNo(ctx, no, true); err != nil {
		return nil, nil, nil, nil, err
	}
	back := []string{screens.AddrPropertyOffer, strconv.FormatInt(no, 10)}
	if o.Kind != kind || pr.OwnerID != o.SellerID || pr.Status != application.PropertyOwned {
		return nil, nil, nil, nil, refuseProperty(screens.PropertyRefusedTaken, screens.AddrPropertyMarket)
	}
	blocked, err := h.offerBlocked(ctx, tx, o, p)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if blocked != "" {
		r := &propertyRefusal{view: screens.PropertyRefusalView{Kind: blocked, Max: int64(h.rules.MaxOwned), Back: back}}
		return nil, nil, nil, nil, r
	}
	here, err := h.hereCity(ctx, tx, p)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if here == nil || here.ID != pr.CityID {
		return nil, nil, nil, nil, refuseProperty(screens.PropertyRefusedNotInCity, back...)
	}
	w, err := locate(ctx, tx, h.cities, snap, p)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := needService(w, snap, place.ServiceCityHall, h.scale, now); err != nil {
		return nil, nil, nil, nil, thenFor(err, "property.offer", strconv.FormatInt(no, 10))
	}
	return p, o, pr, here, nil
}

// Buy handles property.buy: a property bought from its owner at the land
// registry, once — the price from the buyer's chosen purse, less the
// city's market fee to the seller's bank, the deed to the buyer, the offer
// closed, in one transaction.
func (h *PropertyHandler) Buy(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	method, err := payment.Parse(req.Method)
	if !ok || err != nil {
		return h.Offer(ctx, meta, req)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		bought  int64
		replay  = true
		mineArg map[string]any
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		p, o, pr, city, err := h.takeOffer(ctx, tx, snap, meta, &lang, no, application.OfferSale, now)
		if err != nil || p == nil {
			return err
		}
		replay = false
		if pr.Debt() > 0 {
			return refuseProperty(screens.PropertyRefusedInDebt, screens.AddrPropertyMarket)
		}
		fee, err := h.policy.Get(ctx, city.JurisdictionID, LeverMarketFee)
		if err != nil {
			return err
		}
		cut := o.Price * fee.Value / 10000
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		back := []string{screens.AddrPropertyOffer, strconv.FormatInt(no, 10)}
		plan := wallet.Plan(money.FromMinor(o.Price), snap.Accepts(content.ServiceProperty))
		if err := checkMethod(plan, method, wallet, "property.button.back_to_offer", back...); err != nil {
			return err
		}
		sellerBank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, o.SellerID)
		if err != nil {
			return err
		}
		pay := func(reason application.Reason, to string, amount int64) error {
			if amount <= 0 {
				return nil
			}
			_, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{Method: method, Accepted: plan.Accepted,
				Reason: reason, ReferenceType: application.PropertyListingReference, ReferenceID: o.ID,
				To: []application.LedgerEntry{{AccountID: to, Amount: money.FromMinor(amount)}}, CreatedAt: now})
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "property.button.back_to_offer", back...)
			}
			return err
		}
		if err := pay(application.ReasonPropertySale, sellerBank.ID, o.Price-cut); err != nil {
			return err
		}
		if err := pay(application.ReasonMarketFee, application.SystemSinkAccountID, cut); err != nil {
			return err
		}
		if closed, err := tx.Property().CloseListing(ctx, o.ID, application.OfferTaken, p.ID, now); err != nil || !closed {
			if err == nil {
				err = refuseProperty(screens.PropertyRefusedTaken, screens.AddrPropertyMarket)
			}
			return err
		}
		pr.OwnerID, pr.Value, pr.AcquiredAt, pr.UnpaidPeriods = p.ID, o.Price, now, 0
		if err := tx.Property().Save(ctx, *pr); err != nil {
			return err
		}
		bought = pr.No
		t, _ := snap.PropertyType(pr.TypeCode)
		if t.Home {
			if err := h.settleIn(ctx, tx, p.ID, pr.CityID, now); err != nil {
				return err
			}
		}
		mineArg = map[string]any{"no": pr.No}
		seller, err := tx.Players().GetByID(ctx, o.SellerID)
		if err != nil {
			return err
		}
		if err := appendDomainEvent(ctx, tx, meta, "property", "bought", pr.ID, map[string]any{
			"player_id": p.ID, "property_no": pr.No, "type": pr.TypeCode, "city_id": pr.CityID, "price": o.Price,
			"from": "player", "home": t.Home}); err != nil {
			return err
		}
		return appendDomainEvent(ctx, tx, meta, "property", "sold", pr.ID, map[string]any{
			"kind": "sold", "player_id": seller.ID, "property_no": pr.No, "type": pr.TypeCode, "type_name": t.Name,
			"city_code": city.Code, "city_name": city.Name, "other_name": shownName(p), "other_code": p.PublicCode,
			"amount": o.Price - cut})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replay || bought == 0 {
		return h.Mine(ctx, meta)
	}
	return h.mine(ctx, meta, screens.PropertyNoticeBought, mineArg)
}

// Rent handles property.rent: a lease taken at the land registry, once — the
// first period's rent from the tenant's chosen purse to the landlord's bank,
// the lease begun, the offer closed, in one transaction. The rent of that
// period is recorded paid; each later period's falls due at its end. A home
// makes its city the tenant's residence.
func (h *PropertyHandler) Rent(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	method, err := payment.Parse(req.Method)
	if !ok || err != nil {
		return h.Offer(ctx, meta, req)
	}
	snap := h.content.Current()
	lang := meta.Language
	replay := true
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		p, o, pr, city, err := h.takeOffer(ctx, tx, snap, meta, &lang, no, application.OfferRent, now)
		if err != nil || p == nil {
			return err
		}
		replay = false
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		back := []string{screens.AddrPropertyOffer, strconv.FormatInt(no, 10)}
		plan := wallet.Plan(money.FromMinor(o.Price), snap.Accepts(content.ServiceProperty))
		if err := checkMethod(plan, method, wallet, "property.button.back_to_offer", back...); err != nil {
			return err
		}
		landlordBank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, o.SellerID)
		if err != nil {
			return err
		}
		lease, err := tx.Property().StartLease(ctx, application.PropertyLease{ID: h.ids.NewID(), PropertyID: pr.ID,
			LandlordID: o.SellerID, TenantID: p.ID, Rent: o.Price, StartedAt: now})
		if isSentinel(err, application.ErrLeaseExists) {
			return refuseProperty(screens.PropertyRefusedRenting, back...)
		}
		if err != nil {
			return err
		}
		if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{Method: method, Accepted: plan.Accepted,
			Reason: application.ReasonRent, ReferenceType: application.PropertyLeaseReference, ReferenceID: lease.ID,
			To: []application.LedgerEntry{{AccountID: landlordBank.ID, Amount: money.FromMinor(o.Price)}}, CreatedAt: now}); err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "property.button.back_to_offer", back...)
			}
			return err
		}
		clock, err := tx.CityPeriods().Clock(ctx, pr.CityID, now)
		if err != nil {
			return err
		}
		if err := tx.Property().RecordRent(ctx, application.RentPayment{LeaseID: lease.ID, PeriodNo: clock.PeriodNo,
			Rent: o.Price, Paid: o.Price, At: now}); err != nil {
			return err
		}
		if closed, err := tx.Property().CloseListing(ctx, o.ID, application.OfferTaken, p.ID, now); err != nil || !closed {
			if err == nil {
				err = refuseProperty(screens.PropertyRefusedTaken, screens.AddrPropertyMarket)
			}
			return err
		}
		t, _ := snap.PropertyType(pr.TypeCode)
		if t.Home {
			if err := h.settleIn(ctx, tx, p.ID, pr.CityID, now); err != nil {
				return err
			}
		}
		return appendDomainEvent(ctx, tx, meta, "property", "let", pr.ID, map[string]any{
			"kind": "let", "player_id": o.SellerID, "tenant_id": p.ID, "property_no": pr.No, "type": pr.TypeCode,
			"type_name": t.Name, "city_code": city.Code, "city_name": city.Name, "other_name": shownName(p),
			"other_code": p.PublicCode, "amount": o.Price})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replay {
		return h.Mine(ctx, meta)
	}
	return h.mine(ctx, meta, screens.PropertyNoticeRented, nil)
}

// Leave handles property.leave: the tenant leaves the home they rent, after
// confirming. What they paid for this period is not returned.
func (h *PropertyHandler) Leave(ctx context.Context, meta envelope.Metadata, req PropertyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	if !ok {
		return h.Mine(ctx, meta)
	}
	snap := h.content.Current()
	lang := meta.Language
	confirm := strings.TrimSpace(req.Confirm) == screens.PropertyYes
	var ask *screens.PropertyLeaveView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		lease, err := tx.Property().LeaseByNo(ctx, no, false)
		if err != nil {
			return err
		}
		if lease == nil || lease.TenantID != p.ID || lease.Status != application.LeaseActive {
			return refuseProperty(screens.PropertyRefusedNotYours)
		}
		pr, err := tx.Property().ByID(ctx, lease.PropertyID, false)
		if err != nil {
			return err
		}
		t, _ := snap.PropertyType(pr.TypeCode)
		city, err := h.cities.ByID(ctx, pr.CityID)
		if err != nil {
			return err
		}
		if !confirm {
			ask = &screens.PropertyLeaveView{LeaseNo: lease.No, Type: named(t.Code, t.Name), City: govCity(city)}
			return nil
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		if lease, err = tx.Property().LeaseByNo(ctx, no, true); err != nil || lease == nil || lease.Status != application.LeaseActive {
			return err
		}
		now := h.now()
		lease.Status, lease.EndedAt, lease.EndReason = application.LeaseEnded, &now, application.LeaseLeft
		if err := tx.Property().SaveLease(ctx, *lease); err != nil {
			return err
		}
		return appendDomainEvent(ctx, tx, meta, "property", "tenant_left", lease.ID, map[string]any{
			"kind": "tenant_left", "player_id": lease.LandlordID, "property_no": pr.No, "type": pr.TypeCode,
			"type_name": t.Name, "city_code": city.Code, "city_name": city.Name, "other_name": shownName(p),
			"other_code": p.PublicCode})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if ask != nil {
		return screens.PropertyLeave(h.screen(meta, lang), *ask), nil
	}
	return h.mine(ctx, meta, screens.PropertyNoticeLeft, nil)
}

// Rest handles property.rest: the player rests at a home they own (and have
// not let) or rent in the city they stand in, at its place, for the home's
// energy, once every property.rest_cooldown of game time.
func (h *PropertyHandler) Rest(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var gained, rested int
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		t, _, ok, err := h.homeHere(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		if !ok {
			return refuseProperty(screens.PropertyRefusedNoHome)
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if target, ok := w.cmap.Find(t.Place); ok {
			if err := needAt(w, snap, target, "place.need.home", nil, h.scale, now); err != nil {
				return thenFor(err, "property.mine")
			}
		}
		stats, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
		if err != nil {
			return err
		}
		wait, err := h.restWait(ctx, tx, p.ID, now)
		if err != nil {
			return err
		}
		if wait > 0 {
			return &propertyRefusal{view: screens.PropertyRefusalView{Kind: screens.PropertyRefusedTooSoon, Wait: wait}}
		}
		before := stats.Energy
		stats.Energy = min(stats.Energy+t.RestEnergy, stats.MaxEnergy)
		stats.UpdatedAt = now
		if err := tx.Stats().Save(ctx, *stats); err != nil {
			return err
		}
		gained = stats.Energy - before
		if err := tx.Property().SetRested(ctx, p.ID, now); err != nil {
			return err
		}
		// A night at home is the best sleep there is (life.yml sleep.home;
		// docs/adr/0025).
		if def, ok := snap.Life(); ok {
			home := def.Sleep.Home
			l, err := touchLife(ctx, tx, snap, h.scale, p, now, func(l *lifeNow) {
				before := l.needs.Sleep
				l.needs = l.needs.Change(0, -home.Rest, -home.Relief)
				rested = int((before - l.needs.Sleep + life.Milli/2) / life.Milli)
			})
			if err != nil || l == nil {
				return err
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.mine(ctx, meta, screens.PropertyNoticeRested, map[string]any{"energy": gained, "rest": rested})
}
