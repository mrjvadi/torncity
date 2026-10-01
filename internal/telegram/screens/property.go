package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Property (docs/adr/0024-property-and-politics.md): what a city sells at its
// land registry and what its owners offer for sale or to let; one kind of
// property with its price; one offer; the player's own properties and the
// home they rent; resting at home; and the notices of a lease that ended and
// a property the city took back.

// PropertyTypeName names a kind of property.
func (c Context) PropertyTypeName(n Named) string { return c.named("property_type."+n.Code, n.Name) }

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
	return c.withView(renderPropertyMarket(c, v), ScreenPropertyMarket, v)
}

func renderPropertyMarket(c Context, v PropertyMarketView) *presenter.Response {
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

// PropertyType renders one kind of property.
func PropertyType(c Context, v PropertyTypeView) *presenter.Response {
	return c.withView(renderPropertyType(c, v), ScreenPropertyType, v)
}

func renderPropertyType(c Context, v PropertyTypeView) *presenter.Response {
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

// PropertyOffer renders one offer.
func PropertyOffer(c Context, v PropertyOfferView) *presenter.Response {
	return c.withView(renderPropertyOffer(c, v), ScreenPropertyOffer, v)
}

func renderPropertyOffer(c Context, v PropertyOfferView) *presenter.Response {
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
	return c.withView(renderPropertyMine(c, v), ScreenPropertyMine, v)
}

func renderPropertyMine(c Context, v PropertyMineView) *presenter.Response {
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

// Property renders one of the viewer's properties.
func Property(c Context, v PropertyView) *presenter.Response {
	return c.withView(renderProperty(c, v), ScreenProperty, v)
}

func renderProperty(c Context, v PropertyView) *presenter.Response {
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

// PropertyLeave asks to confirm leaving.
func PropertyLeave(c Context, v PropertyLeaveView) *presenter.Response {
	return c.withView(renderPropertyLeave(c, v), ScreenPropertyLeave, v)
}

func renderPropertyLeave(c Context, v PropertyLeaveView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("property.button.leave_confirm", nil), AddrPropertyLeave, strconv.FormatInt(v.LeaseNo, 10), PropertyYes)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrPropertyMine}))
	return c.respond(c.T("property.leave_confirm", map[string]any{"type": c.PropertyTypeName(v.Type),
		"city": c.PlaceName(v.City)}), kb.Build()).MarkPrivate()
}

// PropertyRefusal renders a refused request.
func PropertyRefusal(c Context, v PropertyRefusalView) *presenter.Response {
	return c.withView(renderPropertyRefusal(c, v), ScreenPropertyRefusal, v)
}

func renderPropertyRefusal(c Context, v PropertyRefusalView) *presenter.Response {
	kb := keyboards.New()
	back := AddrPropertyMine
	if v.Back.Command != "" {
		back = v.Back.Address()

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
	return c.withView(renderPropertyNotice(c, v), ScreenPropertyNotice, v)
}

func renderPropertyNotice(c Context, v PropertyNoticeView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("property.button.mine", nil), AddrPropertyMine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	p := v.Player
	return c.respond(c.T("property.notice_event."+v.Kind, map[string]any{"type": c.PropertyTypeName(v.Type),
		"no": FormatNumber(c, v.No), "city": c.PlaceName(v.City), "player": c.govPlayer(&p),
		"amount": FormatMoney(c, v.Amount)}), kb.Build())
}
