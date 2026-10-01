package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A company's goods for sale: putting a line of its warehouse up at a price,
// its open listings, and the city's company goods a player — or a company —
// buys from.

// Sell renders putting a line up for sale: the quantity, then a typed price.
func Sell(c Context, v SellView) *presenter.Response {
	good := c.GoodName(v.Good)
	target := v.Good.TargetArg()
	kb := keyboards.New()
	text := c.T("production.sell_title", map[string]any{"good": good, "have": FormatNumber(c, v.Have),
		"reference": FormatMoney(c, v.Reference)})
	if v.Qty > 0 {
		text = paragraphs(text, c.T("production.sell_price", map[string]any{"qty": FormatNumber(c, v.Qty), "good": good}))
		kb.Row(mustAsk(c.T("production.button.price", nil), commandSell, v.Ref.Code, target, strconv.FormatInt(v.Qty, 10))...)
		if btn, ok := keyboards.Button(c.T("production.button.price_reference", map[string]any{"price": FormatMoney(c, v.Reference)}),
			AddrSell, v.Ref.Code, target, strconv.FormatInt(v.Qty, 10), strconv.FormatInt(v.Reference, 10)); ok {
			kb.Row(btn)
		}
	} else {
		text = paragraphs(text, c.T("production.sell_qty", nil))
		var sizes []presenter.Button
		for _, q := range SellPresets {
			if q >= v.Have {
				continue
			}
			if btn, ok := keyboards.Button(c.T("production.button.size", map[string]any{"qty": FormatNumber(c, q)}),
				AddrSell, v.Ref.Code, target, strconv.FormatInt(q, 10)); ok {
				sizes = append(sizes, btn)
			}
		}
		if btn, ok := keyboards.Button(c.T("production.button.sell_all", map[string]any{"qty": FormatNumber(c, v.Have)}),
			AddrSell, v.Ref.Code, target, strconv.FormatInt(v.Have, 10)); ok {
			sizes = append(sizes, btn)
		}
		kb.Grid(3, sizes...)
	}
	c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code})
	return c.respond(text, kb.Build())
}

// listingNotice words what just happened to a listing.
func (c Context) listingNotice(n *ListingNotice) string {
	if n == nil {
		return ""
	}
	l := n.Listing
	return c.T("production.listing_notice."+n.Kind, map[string]any{"good": c.GoodName(l.Good), "qty": FormatNumber(c, l.Left),
		"price": FormatMoney(c, l.Price)})
}

// Listings renders a company's open listings.
func Listings(c Context, v ListingsView) *presenter.Response {
	head := c.T("production.listings_title", map[string]any{"name": v.Ref.Name, "city": c.CityName(v.CityCode, v.City)})
	var lines []string
	var buttons []presenter.Button
	for _, l := range v.Listings {
		lines = append(lines, c.T("production.listing_line", map[string]any{"good": c.GoodName(l.Good),
			"qty": FormatNumber(c, l.Left), "price": FormatMoney(c, l.Price)}))
		if btn, ok := keyboards.Button(c.T("production.button.unlist", map[string]any{"good": c.GoodName(l.Good)}),
			AddrUnlist, strconv.FormatInt(l.No, 10)); ok {
			buttons = append(buttons, btn)
		}
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("production.listings_none", nil)
	}
	kb := keyboards.New()
	kb.Grid(1, buttons...)
	c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code}, AddrListings, v.Ref.Code)
	return c.respond(paragraphs(c.listingNotice(v.Notice), head, list, c.T("production.listings_hint", nil)), kb.Build())
}

// CompanyGoods renders the city's company goods.
func CompanyGoods(c Context, v GoodsView) *presenter.Response {
	kb := keyboards.New()
	if v.NoCity {
		c.productionNav(kb, []string{AddrMarket})
		return c.respond(paragraphs(c.T("production.goods_heading", nil), c.T("company.no_city", nil)), kb.Build())
	}
	head := c.T("production.goods_title", map[string]any{"city": c.CityName(v.CityCode, v.City)})
	var lines []string
	var buttons []presenter.Button
	for _, l := range v.Lines {
		lines = append(lines, c.goodsLine(l))
		if btn, ok := keyboards.Button(c.T("production.button.buy", map[string]any{"good": c.GoodName(l.Good)}),
			AddrCompanyBuy, strconv.FormatInt(l.No, 10)); ok {
			buttons = append(buttons, btn)
		}
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("production.goods_none", nil)
	}
	kb.Grid(2, buttons...)
	c.productionNav(kb, []string{AddrMarket}, AddrCompanyGoods)
	return c.respond(paragraphs(head, list), kb.Build())
}

// goodsLine renders one listing with its seller and what a buyer may see.
func (c Context) goodsLine(l GoodsLine) string {
	line := c.T("production.goods_line", map[string]any{"good": c.GoodName(l.Good), "company": l.Company.Name,
		"qty": FormatNumber(c, l.Left), "price": FormatMoney(c, l.Price)})
	var attrs []string
	for _, a := range l.Attributes {
		attrs = append(attrs, c.T("production.goods_attribute", map[string]any{"name": c.AttributeName(a.Name),
			"value": FormatNumber(c, a.Value)}))
	}
	if len(attrs) > 0 {
		line = body(line, c.T("production.goods_attributes", map[string]any{"attributes": c.list(attrs)}))
	}
	return line
}

// CompanyBuy renders buying from a listing.
func CompanyBuy(c Context, v BuyView) *presenter.Response {
	kb := keyboards.New()
	no := strconv.FormatInt(v.Line.No, 10)
	good := c.GoodName(v.Line.Good)
	if b := v.Bought; b != nil {
		key := "production.bought_player"
		if b.For != "" {
			key = "production.bought_company"
		}
		text := c.T(key, map[string]any{"qty": FormatNumber(c, b.Qty), "good": good, "total": FormatMoney(c, b.Total),
			"company": b.For, "seller": v.Line.Company.Name})
		if b.ForCode != "" {
			kb.Add(c.T("production.button.company_warehouse", map[string]any{"company": b.For}), AddrWarehouse, b.ForCode)
		} else {
			kb.Add(c.T("production.button.bag", nil), AddrInventory)
		}
		c.productionNav(kb, []string{AddrCompanyGoods})
		return c.respond(text, kb.Build())
	}
	qty := max(v.Qty, 1)
	total := qty * v.Line.Price
	text := paragraphs(c.goodsLine(v.Line), c.T("production.buy_total", map[string]any{"qty": FormatNumber(c, qty),
		"total": FormatMoney(c, total)}))
	var pay []presenter.Button
	if p := v.Payment; p != nil {
		for _, m := range p.Usable {
			if btn, ok := keyboards.Button(c.T("production.button.pay_"+m, map[string]any{"total": FormatMoney(c, total)}),
				AddrCompanyBuy, no, strconv.FormatInt(qty, 10), m); ok {
				pay = append(pay, btn)
			}
		}
	}
	for _, co := range v.Companies {
		if btn, ok := keyboards.Button(c.T("production.button.pay_company", map[string]any{"company": co.Name}),
			AddrCompanyBuy, no, strconv.FormatInt(qty, 10), co.Code); ok {
			pay = append(pay, btn)
		}
	}
	kb.Grid(1, pay...)
	if v.Line.Left > 1 {
		kb.Row(mustAsk(c.T("production.button.buy_qty", nil), commandCompanyBuy, no)...)
	}
	if len(pay) == 0 {
		text = paragraphs(text, c.T("production.buy_cannot_pay", nil))
	}
	c.productionNav(kb, []string{AddrCompanyGoods}, AddrCompanyBuy, no)
	return c.respond(text, kb.Build())
}

// ---------------------------------------------------------------------------
// Notices and group lines.

// ProductionNotice renders a private notice of the production economy.
func ProductionNotice(c Context, v ProductionNoticeView) *presenter.Response {
	text := c.T("production.notice."+v.Kind, map[string]any{"company": v.Company.Name, "tech": c.TechName(v.Tech),
		"good": c.GoodName(v.Good), "qty": FormatNumber(c, v.Qty), "quality": FormatNumber(c, int64(v.Quality)),
		"design": v.Design, "buyer": v.Buyer, "price": FormatMoney(c, v.Price)})
	kb := keyboards.New()
	switch v.Kind {
	case ProductionNoticeResearched:
		kb.Add(c.T("production.button.tech", map[string]any{"tech": c.TechName(v.Tech)}), AddrLab, v.Company.Code, v.Tech.Code)
	case ProductionNoticeReversedOK:
		kb.Add(c.T("production.button.design", map[string]any{"name": v.Design}), AddrDesign, strconv.FormatInt(v.DesignNo, 10))
	case ProductionNoticeProduced, ProductionNoticeSold:
		kb.Add(c.T("production.button.warehouse", nil), AddrWarehouse, v.Company.Code)
	default:
		kb.Add(c.T("production.button.lab", nil), AddrLab, v.Company.Code)
	}
	return presenter.Message(text, kb.Build())
}

// TechPublishedAnnouncement is a city group's line: a company published a
// technology for everyone.
func TechPublishedAnnouncement(c Context, company CompanyRef, tech Named, cityCode, city string) string {
	return c.T("production.announce.published", map[string]any{"company": company.Name, "tech": c.TechName(tech),
		"city": c.CityName(cityCode, city)})
}

// ProductLaunchedAnnouncement is a city group's line: a company made its
// first units of a new product.
func ProductLaunchedAnnouncement(c Context, company CompanyRef, design string, item Named, cityCode, city string) string {
	return c.T("production.announce.launched", map[string]any{"company": company.Name, "design": design,
		"item": c.ItemName(item), "city": c.CityName(cityCode, city)})
}
