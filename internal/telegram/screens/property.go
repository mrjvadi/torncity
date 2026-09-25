package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Property (docs/adr/0024-property-and-politics.md): what a city sells at its
// land registry and what its owners offer for sale or to let; one kind of
// property with its price; one offer; the player's own properties and the
// home they rent; resting at home; and the notices of a lease that ended and
// a property the city took back.

// Addresses of the property screens.
const (
	AddrPropertyMarket   = "property:list"
	AddrPropertyType     = "property:type"
	AddrPropertyPurchase = "property:purchase"
	AddrPropertyMine     = "property:mine"
	AddrProperty         = "property:view"
	AddrPropertySell     = "property:sell"
	AddrPropertyLet      = "property:let"
	AddrPropertyCancel   = "property:cancel"
	AddrPropertyOffer    = "property:offer"
	AddrPropertyBuy      = "property:buy"
	AddrPropertyRent     = "property:rent"
	AddrPropertyLeave    = "property:leave"
	AddrPropertyRest     = "property:rest"
)

// PropertyYes confirms leaving a rented home.
const PropertyYes = "yes"

// PropertyTypeName names a kind of property.
func (c Context) PropertyTypeName(n Named) string { return c.named("property_type."+n.Code, n.Name) }

// PropertyTypeLine is one kind of property a city sells.
type PropertyTypeLine struct {
	Type    Named
	Kind    string
	Size    int
	Quality int
	Price   int64
	Left    int
	Home    bool
}

// PropertyOfferLine is one owner's offer: a property for sale or to let.
type PropertyOfferLine struct {
	No         int64
	Kind       string
	Type       Named
	PropertyNo int64
	Price      int64
	Seller     GovPlayer
	// Mine says the viewer made it.
	Mine bool
}

// PropertyMarketView is a city's property market.
type PropertyMarketView struct {
	NoCity bool
	City   GovPlace
	Types  []PropertyTypeLine
	Offers []PropertyOfferLine
}

func (c Context) propertyFacts(kind string, size, quality int) string {
	return c.T("property.facts", map[string]any{"kind": c.T("property.kind."+kind, nil),
		"size": FormatNumber(c, int64(size)), "quality": FormatNumber(c, int64(quality))})
}

func (c Context) offerLine(o PropertyOfferLine) string {
	key := "property.offer_sale"
	if o.Kind == application.OfferRent {
		key = "property.offer_rent"
	}
	return c.T(key, map[string]any{"no": FormatNumber(c, o.PropertyNo), "type": c.PropertyTypeName(o.Type),
		"price": FormatMoney(c, o.Price), "seller": c.govPlayer(&o.Seller)})
}

// PropertyMarket renders a city's property market.
func PropertyMarket(c Context, v PropertyMarketView) *presenter.Response {
	kb := keyboards.New()
	if v.NoCity {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap}))
		return c.respond(c.T("property.no_city", nil), kb.Build())
	}
	head := c.T("property.market_title", map[string]any{"city": c.PlaceName(v.City)})
	var sold []string
	for _, t := range v.Types {
		key := "property.type_line"
		if t.Left <= 0 {
			key = "property.type_line_sold_out"
		}
		sold = append(sold, c.T(key, map[string]any{"type": c.PropertyTypeName(t.Type),
			"facts": c.propertyFacts(t.Kind, t.Size, t.Quality), "price": FormatMoney(c, t.Price),
			"left": FormatNumber(c, int64(t.Left))}))
		kb.Add(c.T("property.button.type", map[string]any{"type": c.PropertyTypeName(t.Type)}), AddrPropertyType, t.Type.Code)
	}
	if len(sold) == 0 {
		sold = append(sold, c.T("property.city_sells_none", nil))
	}
	sold = append([]string{c.T("property.city_sells", nil)}, sold...)
	offers := []string{c.T("property.offers", nil)}
	for _, o := range v.Offers {
		offers = append(offers, c.offerLine(o))
		kb.Add(c.T("property.button.offer", map[string]any{"no": FormatNumber(c, o.PropertyNo)}), AddrPropertyOffer,
			strconv.FormatInt(o.No, 10))
	}
	if len(v.Offers) == 0 {
		offers = append(offers, c.T("property.offers_none", nil))
	}
	kb.Add(c.T("property.button.mine", nil), AddrPropertyMine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap, RefreshData: AddrPropertyMarket}))
	return c.respond(paragraphs(head, body(sold...), body(offers...)), kb.Build())
}

// PropertyTypeView is one kind of property the city sells, and its price.
type PropertyTypeView struct {
	City       GovPlace
	Type       Named
	Kind       string
	Size       int
	Quality    int
	Upkeep     int64
	Home       bool
	RestEnergy int
	Place      Named
	Price      int64
	Left       int
	TaxBPS     int64
	// Payment offers the ways to pay, when the viewer may buy here now.
	Payment *PaymentChoice
	// Blocked says why they may not: sold_out, too_many.
	Blocked string
	Max     int
	// Way is the walk to the land registry, when they are elsewhere.
	Way *Way
	// Bought says the viewer just bought one: its number.
	Bought int64
}

// PropertyType renders one kind of property.
func PropertyType(c Context, v PropertyTypeView) *presenter.Response {
	kb := keyboards.New()
	var notice string
	if v.Bought > 0 {
		notice = c.T("property.bought", map[string]any{"no": FormatNumber(c, v.Bought), "type": c.PropertyTypeName(v.Type)})
	}
	facts := []string{
		c.T("property.type_title", map[string]any{"type": c.PropertyTypeName(v.Type), "city": c.PlaceName(v.City)}),
		c.propertyFacts(v.Kind, v.Size, v.Quality),
		c.T("property.where", map[string]any{"place": c.SpotName(v.Place)}),
		c.T("property.price", map[string]any{"price": FormatMoney(c, v.Price), "left": FormatNumber(c, int64(v.Left))}),
		c.T("property.upkeep", map[string]any{"upkeep": FormatMoney(c, v.Upkeep)}),
		c.T("property.tax", map[string]any{"tax": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.TaxBPS))})}),
	}
	if v.Home {
		facts = append(facts, c.T("property.home_note", map[string]any{"energy": FormatNumber(c, int64(v.RestEnergy))}))
	}
	var action string
	switch {
	case v.Blocked != "":
		action = c.T("property.refused."+v.Blocked, map[string]any{"max": FormatNumber(c, int64(v.Max))})
	case v.Way != nil:
		action = c.T("property.at_registry", map[string]any{"place": c.SpotName(v.Way.Place)})
		c.wayButton(kb, v.Way, "property.type", v.Type.Code)
	case v.Payment != nil:
		action = c.paymentNote(*v.Payment)
		c.paymentButtons(kb, *v.Payment, func(m string) []string {
			return []string{AddrPropertyPurchase, v.Type.Code, m}
		})
	}
	kb.Add(c.T("property.button.mine", nil), AddrPropertyMine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrPropertyMarket, RefreshData: keyboards.Data(AddrPropertyType, v.Type.Code)}))
	return c.respond(paragraphs(notice, body(facts...), action), kb.Build())
}

// PropertyOfferView is one owner's offer, and what taking it costs.
type PropertyOfferView struct {
	Offer   PropertyOfferLine
	City    GovPlace
	Kind    string
	Size    int
	Quality int
	Upkeep  int64
	Home    bool
	Payment *PaymentChoice
	// Blocked says why the viewer may not take it: own, renting,
	// too_many, taken.
	Blocked string
	Max     int
	Way     *Way
}

// PropertyOffer renders one offer.
func PropertyOffer(c Context, v PropertyOfferView) *presenter.Response {
	kb := keyboards.New()
	o := v.Offer
	no := strconv.FormatInt(o.No, 10)
	facts := []string{
		c.T("property.offer_title", map[string]any{"no": FormatNumber(c, o.PropertyNo), "type": c.PropertyTypeName(o.Type),
			"city": c.PlaceName(v.City)}),
		c.propertyFacts(v.Kind, v.Size, v.Quality),
		c.offerLine(o),
	}
	if o.Kind == application.OfferRent {
		facts = append(facts, c.T("property.rent_terms", map[string]any{"rent": FormatMoney(c, o.Price)}))
	} else {
		facts = append(facts, c.T("property.upkeep", map[string]any{"upkeep": FormatMoney(c, v.Upkeep)}))
	}
	if v.Home {
		facts = append(facts, c.T("property.home_residence", nil))
	}
	var action string
	switch {
	case v.Blocked != "":
		action = c.T("property.refused."+v.Blocked, map[string]any{"max": FormatNumber(c, int64(v.Max))})
		if v.Blocked == "own" {
			kb.Add(c.T("property.button.cancel", nil), AddrPropertyCancel, strconv.FormatInt(o.PropertyNo, 10))
		}
	case v.Way != nil:
		action = c.T("property.at_registry", map[string]any{"place": c.SpotName(v.Way.Place)})
		c.wayButton(kb, v.Way, "property.offer", no)
	case v.Payment != nil:
		action = c.paymentNote(*v.Payment)
		addr := AddrPropertyBuy
		if o.Kind == application.OfferRent {
			addr = AddrPropertyRent
		}
		c.paymentButtons(kb, *v.Payment, func(m string) []string { return []string{addr, no, m} })
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrPropertyMarket, RefreshData: keyboards.Data(AddrPropertyOffer, no)}))
	return c.respond(paragraphs(body(facts...), action), kb.Build())
}

// PropertyLine is one property the viewer owns.
type PropertyLine struct {
	No            int64
	Type          Named
	Kind          string
	Size, Quality int
	City          GovPlace
	Value         int64
	Debt          int64
	UnpaidPeriods int
	Home          bool
	// Offer is its open offer; Tenant and Rent its lease.
	Offer   *PropertyOfferLine
	Tenant  *GovPlayer
	Rent    int64
	Arrears int
}

// RentedHomeLine is the home the viewer rents.
type RentedHomeLine struct {
	LeaseNo  int64
	Property PropertyLine
	Landlord GovPlayer
	Rent     int64
	Arrears  int
}

// PropertyMineView is the viewer's property.
type PropertyMineView struct {
	Owned  []PropertyLine
	Rented *RentedHomeLine
	// Residence is the city they live in; Grace how many periods of debt a
	// property may run before the city takes it back.
	Residence GovPlace
	Grace     int
	// Rest: a home here to rest at, how long until they may, what it gives.
	CanRest    bool
	RestIn     time.Duration
	RestEnergy int
	Notice     string
	NoticeArgs map[string]any
}

// Notices above the viewer's property.
const (
	PropertyNoticeListed    = "listed"
	PropertyNoticeCancelled = "cancelled"
	PropertyNoticeRented    = "rented"
	PropertyNoticeLeft      = "left"
	PropertyNoticeRested    = "rested"
	PropertyNoticeBought    = "bought_offer"
)

func (c Context) ownedLine(p PropertyLine) string {
	lines := []string{c.T("property.owned_line", map[string]any{"no": FormatNumber(c, p.No),
		"type": c.PropertyTypeName(p.Type), "city": c.PlaceName(p.City), "value": FormatMoney(c, p.Value)})}
	if p.Debt > 0 {
		lines = append(lines, "  "+c.T("property.debt", map[string]any{"debt": FormatMoney(c, p.Debt),
			"periods": FormatNumber(c, int64(p.UnpaidPeriods))}))
	}
	switch {
	case p.Tenant != nil:
		lines = append(lines, "  "+c.T("property.let_to", map[string]any{"tenant": c.govPlayer(p.Tenant),
			"rent": FormatMoney(c, p.Rent)}))
	case p.Offer != nil && p.Offer.Kind == application.OfferRent:
		lines = append(lines, "  "+c.T("property.offered_rent", map[string]any{"price": FormatMoney(c, p.Offer.Price)}))
	case p.Offer != nil:
		lines = append(lines, "  "+c.T("property.offered_sale", map[string]any{"price": FormatMoney(c, p.Offer.Price)}))
	case p.Home:
		lines = append(lines, "  "+c.T("property.lived_in", nil))
	}
	return body(lines...)
}

// PropertyMine renders the viewer's property.
func PropertyMine(c Context, v PropertyMineView) *presenter.Response {
	kb := keyboards.New()
	var notice string
	if v.Notice != "" {
		notice = c.T("property.notice."+v.Notice, v.NoticeArgs)
	}
	head := []string{c.T("property.mine_title", nil)}
	if v.Residence.Code != "" {
		head = append(head, c.T("property.residence", map[string]any{"city": c.PlaceName(v.Residence)}))
	}
	var owned []string
	for _, p := range v.Owned {
		owned = append(owned, c.ownedLine(p))
		kb.Add(c.T("property.button.manage", map[string]any{"no": FormatNumber(c, p.No),
			"type": c.PropertyTypeName(p.Type)}), AddrProperty, strconv.FormatInt(p.No, 10))
	}
	if len(owned) == 0 {
		owned = append(owned, c.T("property.owned_none", nil))
	} else {
		owned = append(owned, c.T("property.grace", map[string]any{"periods": FormatNumber(c, int64(v.Grace))}))
	}
	var rented string
	if r := v.Rented; r != nil {
		lines := []string{c.T("property.renting", map[string]any{"type": c.PropertyTypeName(r.Property.Type),
			"city": c.PlaceName(r.Property.City), "landlord": c.govPlayer(&r.Landlord), "rent": FormatMoney(c, r.Rent)})}
		if r.Arrears > 0 {
			lines = append(lines, "  "+c.T("property.arrears", map[string]any{"periods": FormatNumber(c, int64(r.Arrears))}))
		}
		rented = body(lines...)
		kb.Add(c.T("property.button.leave", nil), AddrPropertyLeave, strconv.FormatInt(r.LeaseNo, 10))
	}
	var rest string
	switch {
	case v.CanRest && v.RestIn > 0:
		rest = c.T("property.rest_wait", map[string]any{"wait": FormatDuration(c, v.RestIn)})
	case v.CanRest:
		rest = c.T("property.rest_ready", map[string]any{"energy": FormatNumber(c, int64(v.RestEnergy))})
		kb.Add(c.T("property.button.rest", nil), AddrPropertyRest)
	}
	kb.Add(c.T("property.button.market", nil), AddrPropertyMarket)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrPropertyMine}))
	return c.respond(paragraphs(notice, body(head...), body(owned...), rented, rest), kb.Build()).MarkPrivate()
}

// PropertyView is one of the viewer's properties, and what they can do with
// it.
type PropertyView struct {
	Property PropertyLine
	Place    Named
	Upkeep   int64
	TaxBPS   int64
	MaxPrice int64
	MaxRent  int64
	// Notice is what just happened (listed, cancelled).
	Notice string
}

// Property renders one of the viewer's properties.
func Property(c Context, v PropertyView) *presenter.Response {
	kb := keyboards.New()
	p := v.Property
	no := strconv.FormatInt(p.No, 10)
	var notice string
	if v.Notice != "" {
		notice = c.T("property.notice."+v.Notice, nil)
	}
	facts := []string{
		c.ownedLine(p),
		c.propertyFacts(p.Kind, p.Size, p.Quality),
		c.T("property.where", map[string]any{"place": c.SpotName(v.Place)}),
		c.T("property.upkeep", map[string]any{"upkeep": FormatMoney(c, v.Upkeep)}),
		c.T("property.tax", map[string]any{"tax": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.TaxBPS))})}),
	}
	switch {
	case p.Tenant != nil:
	case p.Offer != nil:
		kb.Add(c.T("property.button.cancel", nil), AddrPropertyCancel, no)
	case p.Debt > 0:
		facts = append(facts, c.T("property.debt_blocks", nil))
	default:
		kb.Add(c.T("property.button.sell", nil), AddrPropertySell, no)
		kb.Add(c.T("property.button.let", nil), AddrPropertyLet, no)
		facts = append(facts, c.T("property.offer_limits", map[string]any{"price": FormatMoney(c, v.MaxPrice),
			"rent": FormatMoney(c, v.MaxRent)}))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrPropertyMine, RefreshData: keyboards.Data(AddrProperty, no)}))
	return c.respond(paragraphs(notice, body(facts...)), kb.Build()).MarkPrivate()
}

// PropertyLeaveView asks the tenant to confirm leaving their rented home.
type PropertyLeaveView struct {
	LeaseNo int64
	Type    Named
	City    GovPlace
}

// PropertyLeave asks to confirm leaving.
func PropertyLeave(c Context, v PropertyLeaveView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("property.button.leave_confirm", nil), AddrPropertyLeave, strconv.FormatInt(v.LeaseNo, 10), PropertyYes)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrPropertyMine}))
	return c.respond(c.T("property.leave_confirm", map[string]any{"type": c.PropertyTypeName(v.Type),
		"city": c.PlaceName(v.City)}), kb.Build()).MarkPrivate()
}

// Refusals of the property screens.
const (
	PropertyRefusedNotFound = "not_found"
	PropertyRefusedNotYours = "not_yours"
	PropertyRefusedSoldOut  = "sold_out"
	PropertyRefusedTooMany  = "too_many"
	PropertyRefusedTaken    = "taken"
	PropertyRefusedOwn      = "own"
	PropertyRefusedRenting  = "renting"
	PropertyRefusedLet      = "let"
	PropertyRefusedOffered  = "offered"
	// PropertyRefusedPledged: it secures a running mortgage.
	PropertyRefusedPledged   = "pledged"
	PropertyRefusedInDebt    = "in_debt"
	PropertyRefusedPrice     = "price"
	PropertyRefusedNoHome    = "no_home"
	PropertyRefusedTooSoon   = "too_soon"
	PropertyRefusedNotInCity = "not_in_city"
)

// PropertyRefusalView is a refused request.
type PropertyRefusalView struct {
	Kind string
	Max  int64
	Wait time.Duration
	Back []string
}

// PropertyRefusal renders a refused request.
func PropertyRefusal(c Context, v PropertyRefusalView) *presenter.Response {
	kb := keyboards.New()
	back := AddrPropertyMine
	if len(v.Back) > 0 {
		back = keyboards.Data(v.Back...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("property.refused."+v.Kind, map[string]any{"max": FormatMoney(c, v.Max),
		"wait": FormatDuration(c, v.Wait)}), kb.Build())
}

// PropertyNoticeView is a notice about the player's property or home.
type PropertyNoticeView struct {
	// Kind is evicted, foreclosed, sold, let, tenant_left, evicted_tenant.
	Kind   string
	Type   Named
	No     int64
	City   GovPlace
	Player GovPlayer
	Amount int64
}

// PropertyNotice renders a notice.
func PropertyNotice(c Context, v PropertyNoticeView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("property.button.mine", nil), AddrPropertyMine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	p := v.Player
	return c.respond(c.T("property.notice_event."+v.Kind, map[string]any{"type": c.PropertyTypeName(v.Type),
		"no": FormatNumber(c, v.No), "city": c.PlaceName(v.City), "player": c.govPlayer(&p),
		"amount": FormatMoney(c, v.Amount)}), kb.Build())
}
