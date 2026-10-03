package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The city shops: which shops the city has and where, one shop's shelves
// priced now, the checkout with a button per way to pay, a purchase, and
// selling a good back. A shop's name is shop_name.<code>, a good's
// item_name.<code>. Prices are public; balances show only in the player's own
// chat (PaymentChoice).

// Shops renders the shops of the city.
func Shops(c Context, v ShopsView) *presenter.Response {
	return c.withView(renderShops(c, v), ScreenShops, v)
}

func renderShops(c Context, v ShopsView) *presenter.Response {
	kb := keyboards.New()
	var lines []string
	city := c.CityName(v.CityCode, v.City)
	title := c.T("shop.title", map[string]any{"city": city})
	refresh := keyboards.Data(AddrShops)
	if v.Place != nil {
		title = c.T("shop.title_at", map[string]any{"place": c.SpotName(*v.Place), "city": city})
		refresh = keyboards.Data(AddrShops, v.Place.Code)
	}
	if len(v.Shops) == 0 {
		if v.Place != nil {
			lines = append(lines, c.T("shop.none_at", map[string]any{"place": c.SpotName(*v.Place)}))
		} else {
			lines = append(lines, c.T("shop.none", nil))
		}
	}
	var buttons []presenter.Button
	for _, s := range v.Shops {
		key := "shop.line"
		if s.Here {
			key = "shop.line_here"
		}
		lines = append(lines, c.T(key, map[string]any{"shop": c.ShopName(s.Shop), "place": c.SpotName(s.Place)}))
		if btn, ok := keyboards.Button(c.T("shop.button.open", map[string]any{"shop": c.ShopName(s.Shop)}), AddrShop, s.Shop.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	kb.Grid(2, buttons...)
	if v.Place != nil {
		if all, ok := keyboards.Button(c.T("shop.button.all", nil), AddrShops); ok {
			kb.Row(all)
		}
	}
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(bag)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap, RefreshData: refresh}))
	return c.respond(paragraphs(htmlBold(htmlEscape(title)), htmlEscape(body(lines...))), kb.Build()).AsHTML()
}

// ShopDetail renders a shop's shelves, with a buy button per good in stock
// when the player stands at the counter.
func ShopDetail(c Context, v ShopView) *presenter.Response {
	return c.withView(renderShopDetail(c, v), ScreenShopDetail, v)
}

func renderShopDetail(c Context, v ShopView) *presenter.Response {
	kb := keyboards.New()
	lines := make([]string, 0, len(v.Shelves))
	var buttons []presenter.Button
	for _, s := range v.Shelves {
		name := c.ItemName(s.Item)
		args := map[string]any{"item": name, "price": FormatMoney(c, s.Price), "stock": FormatNumber(c, s.Stock)}
		key := "shop.shelf"
		switch {
		case s.Stock == 0:
			key = "shop.shelf_empty"
		case s.Busy:
			key = "shop.shelf_busy"
		}
		line := c.T(key, args)
		if s.Buyback > 0 {
			line = c.T("shop.shelf_buys", map[string]any{"line": line, "buyback": FormatMoney(c, s.Buyback)})
		}
		lines = append(lines, line)
		if v.Here && s.Stock > 0 {
			if btn, ok := keyboards.Button(c.T("shop.button.buy", map[string]any{"item": name}), AddrShopBuy, v.Shop.Code, s.Item.Code, "1"); ok {
				buttons = append(buttons, btn)
			}
		}
	}
	kb.Grid(2, buttons...)
	where := c.T("shop.at_counter", nil)
	if !v.Here {
		where = c.T("shop.away", map[string]any{"place": c.SpotName(v.Place)})
		if v.Walk > 0 {
			c.wayButton(kb, &Way{Place: v.Place, Walk: v.Walk}, "shop.view", v.Shop.Code)
		}
	}
	var tax string
	if v.TaxBPS > 0 {
		tax = c.T("shop.tax", map[string]any{"pct": PercentFromBPS(c, v.TaxBPS)})
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrShops, RefreshData: keyboards.Data(AddrShop, v.Shop.Code)}))
	title := htmlBold(htmlEscape(c.T("shop.detail_title", map[string]any{"shop": c.ShopName(v.Shop), "place": c.SpotName(v.Place)})))
	return c.respond(paragraphs(
		title, htmlEscape(body(lines...)), htmlEscape(body(where, tax)),
	), kb.Build()).AsHTML()
}

// ShopCheckout renders the checkout.
func ShopCheckout(c Context, v ShopCheckoutView) *presenter.Response {
	return c.withView(renderShopCheckout(c, v), ScreenShopCheckout, v)
}

func renderShopCheckout(c Context, v ShopCheckoutView) *presenter.Response {
	name := c.ItemName(v.Item)
	facts := []string{
		c.T("shop.checkout_item", map[string]any{"item": name, "qty": FormatNumber(c, v.Qty), "unit": FormatMoney(c, v.Unit)}),
		c.T("shop.checkout_total", map[string]any{"total": FormatMoney(c, v.Total)}),
	}
	if v.Tax > 0 {
		facts = append(facts, c.T("shop.checkout_tax", map[string]any{"tax": FormatMoney(c, v.Tax), "pct": PercentFromBPS(c, v.TaxBPS)}))
		facts = append(facts, c.T("shop.checkout_due", map[string]any{"due": FormatMoney(c, v.Total+v.Tax)}))
	}
	kb := keyboards.New()
	var pay string
	qty := strconv.FormatInt(v.Qty, 10)
	if len(v.Payment.Usable) > 0 {
		pay = body(c.T("payment.choose", nil), c.paymentNote(v.Payment))
		c.paymentButtons(kb, v.Payment, func(m string) []string {
			return []string{AddrShopBuy, v.Shop.Code, v.Item.Code, qty, m, v.Nonce}
		})
	} else {
		pay = body(c.T("payment.cannot_afford", nil), c.paymentNote(v.Payment))
		if btn, ok := keyboards.Button(c.T("button.bank", nil), AddrBank); ok {
			kb.Row(btn)
		}
	}
	var more []presenter.Button
	for _, q := range economy.ShopQtyChoices {
		if q == v.Qty || q > v.Stock {
			continue
		}
		if btn, ok := keyboards.Button(c.T("shop.button.qty", map[string]any{"qty": FormatNumber(c, q)}),
			AddrShopBuy, v.Shop.Code, v.Item.Code, strconv.FormatInt(q, 10)); ok {
			more = append(more, btn)
		}
	}
	kb.Row(more...)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrShop, v.Shop.Code)}))
	return c.respond(paragraphs(c.T("shop.checkout_title", map[string]any{"shop": c.ShopName(v.Shop)}), body(facts...), pay), kb.Build())
}

// ShopBought renders a purchase.
func ShopBought(c Context, v ShopBoughtView) *presenter.Response {
	return c.withView(renderShopBought(c, v), ScreenShopBought, v)
}

func renderShopBought(c Context, v ShopBoughtView) *presenter.Response {
	lines := []string{c.T("shop.bought", map[string]any{
		"item": c.ItemName(v.Item), "qty": FormatNumber(c, v.Qty), "total": FormatMoney(c, v.Total+v.Tax),
	})}
	if v.Tax > 0 {
		lines = append(lines, c.T("shop.bought_tax", map[string]any{"tax": FormatMoney(c, v.Tax)}))
	}
	lines = append(lines, c.paidLine(v.Method))
	kb := keyboards.New()
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	again, _ := keyboards.Button(c.T("shop.button.back_to_shop", nil), AddrShop, v.Shop.Code)
	kb.Row(bag, again)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build())
}

// SellOffers renders the shops that buy a good.
func SellOffers(c Context, v SellOffersView) *presenter.Response {
	return c.withView(renderSellOffers(c, v), ScreenSellOffers, v)
}

func renderSellOffers(c Context, v SellOffersView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{c.T("shop.offers_title", map[string]any{"item": c.ItemName(v.Item)})}
	if len(v.Offers) == 0 {
		lines = append(lines, c.T("shop.offers_none", nil))
	}
	for _, o := range v.Offers {
		lines = append(lines, c.T("shop.offer", map[string]any{
			"shop": c.ShopName(o.Shop), "place": c.SpotName(o.Place), "price": FormatMoney(c, o.Price),
		}))
		if btn, ok := keyboards.Button(c.T("shop.button.sell_to", map[string]any{"shop": c.ShopName(o.Shop), "price": FormatMoney(c, o.Price)}),
			AddrShopSell, o.Shop.Code, v.Ref); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrItem, v.Ref)}))
	return c.respond(body(lines...), kb.Build())
}

// ShopSold renders a sale to a shop.
func ShopSold(c Context, v ShopSoldView) *presenter.Response {
	return c.withView(renderShopSold(c, v), ScreenShopSold, v)
}

func renderShopSold(c Context, v ShopSoldView) *presenter.Response {
	kb := keyboards.New()
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(bag)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(
		c.T("shop.sold", map[string]any{"item": c.ItemName(v.Item), "shop": c.ShopName(v.Shop), "price": FormatMoney(c, v.Price)}),
		c.T("item.left", map[string]any{"qty": FormatNumber(c, v.Left)}),
	), kb.Build())
}

// NoRoom renders a request refused for lack of room.
func NoRoom(c Context, v NoRoomView) *presenter.Response {
	return c.withView(renderNoRoom(c, v), ScreenNoRoom, v)
}

func renderNoRoom(c Context, v NoRoomView) *presenter.Response {
	key := "no_room.space"
	if v.Heavy {
		key = "no_room.heavy"
	}
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("no_room.button.bag", nil), AddrInventory); ok {
		kb.Row(btn)
	}
	back := v.Back
	if back == "" {
		back = AddrHome
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T(key, map[string]any{
		"item": c.ItemName(v.Item), "qty": FormatNumber(c, v.Qty), "need": FormatNumber(c, v.NeedSpace),
		"free": FormatNumber(c, v.FreeSpace), "short": FormatNumber(c, v.Short),
		"grams": FormatNumber(c, v.NeedG), "freeg": FormatNumber(c, v.FreeG),
	}), kb.Build())
}

// ShopRefusal renders a refused shop request.
func ShopRefusal(c Context, v ShopRefusalView) *presenter.Response {
	return c.withView(renderShopRefusal(c, v), ScreenShopRefusal, v)
}

func renderShopRefusal(c Context, v ShopRefusalView) *presenter.Response {
	lines := []string{c.T("shop.refused."+v.Kind, map[string]any{
		"shop": c.ShopName(v.Shop), "item": c.ItemName(v.Item), "stock": FormatNumber(c, v.Stock),
	})}
	if v.Kind == ShopRefusedSoldOut {
		lines = append(lines, clockLine(c, "shop.restock_at", v.NextRestock))
	}
	kb := keyboards.New()
	shops, _ := keyboards.Button(c.T("shop.button.shops", nil), AddrShops)
	kb.Row(shops)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build())
}
