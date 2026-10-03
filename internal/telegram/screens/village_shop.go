package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The village shop on Telegram (docs/adr/0046 section 5; web map: the shop panel).
// A group screen under «انبار و بازار» (the stock-and-market screen): the shelf
// of a limited daily delivery, the buyer's room, and the head's two levers.
// Nothing is worded here that the core did not send as a code or a number.

const (
	ScreenVillageShop         = village.ScreenVillageShop
	ScreenVillageShopCheckout = village.ScreenVillageShopCheckout
	ScreenVillageShopRefusal  = village.ScreenVillageShopRefusal
	AddrShopHere              = village.AddrShop
	AddrShopHereBuy           = village.AddrShopBuy
	AddrShopHereCap           = village.AddrShopCap
	AddrShopHereTax           = village.AddrShopTax
	AddrShopHereRepair        = village.AddrShopRepair
)

// shopItemName words a good or a material by its kind.
func (c Context) shopItemName(n Named, kind string) string {
	if kind == "component" {
		return c.ComponentName(n)
	}
	return c.ItemName(n)
}

// VillageShop renders the shop.
func VillageShop(c Context, v village.ShopView) *presenter.Response {
	return c.withView(renderVillageShop(c, v), ScreenVillageShop, v)
}

func renderVillageShop(c Context, v village.ShopView) *presenter.Response {
	status := c.T("village.shop.open", nil)
	if v.Closed != village.ShopOpen {
		status = c.T("village.shop.closed."+v.Closed, nil)
	}
	var lines []string
	kb := keyboards.New()
	for _, l := range v.Lines {
		name := c.shopItemName(l.Item, l.Kind)
		lines = append(lines, c.T("village.shop.line", map[string]any{
			"name": name, "price": FormatMoney(c, l.Price), "stock": FormatNumber(c, l.Stock), "left": FormatNumber(c, l.LeftToday),
		}))
		var row []presenter.Button
		for _, q := range v.Presets {
			if l.MaxBuy < q {
				continue
			}
			label := c.T("village.shop.button.buy", map[string]any{"name": name, "qty": FormatNumber(c, q)})
			if b, ok := keyboards.Button(label, AddrShopHereBuy, l.Item.Code, strconv.FormatInt(q, 10)); ok {
				row = append(row, b)
			}
		}
		kb.Row(row...)
	}
	shelf := body(lines...)
	if shelf == "" {
		shelf = c.T("village.shop.empty", nil)
	}
	var locked []string
	for _, l := range v.Locked {
		var needs []string
		for _, b := range l.NeedsBuildings {
			needs = append(needs, c.SettlementBuildingName(b))
		}
		for _, k := range l.NeedsKnowledge {
			needs = append(needs, c.SettlementKnowledgeName(k))
		}
		locked = append(locked, c.T("village.shop.locked_line", map[string]any{
			"name": c.shopItemName(l.Item, l.Kind), "needs": joinWith(c, needs)}))
	}
	var lockedText string
	if len(locked) > 0 {
		lockedText = c.T("village.shop.locked_title", nil) + "\n" + body(locked...)
	}
	var repairs []string
	for _, r := range v.Repairs {
		repairs = append(repairs, c.T("village.shop.repair_line", map[string]any{
			"name": c.ItemName(r.Item), "wear": FormatNumber(c, int64(r.Wear)), "max": FormatNumber(c, int64(r.WearMax)),
			"cost": FormatMoney(c, r.Cost)}))
		if v.CanRepair {
			if b, ok := keyboards.Button(c.T("village.shop.button.repair", map[string]any{"name": c.ItemName(r.Item), "cost": FormatMoney(c, r.Cost)}),
				AddrShopHereRepair, r.Serial); ok {
				kb.Row(b)
			}
		}
	}
	var repairText string
	if len(repairs) > 0 {
		repairText = c.T("village.shop.repair_title", nil) + "\n" + body(repairs...)
	}
	var bought, mended, notHere string
	if b := v.Bought; b != nil {
		bought = c.T("village.shop.bought", map[string]any{"qty": FormatNumber(c, b.Qty), "name": c.shopItemName(b.Item, b.Kind),
			"total": FormatMoney(c, b.Total), "tax": FormatMoney(c, b.Tax)})
	}
	if m := v.Mended; m != nil {
		mended = c.T("village.shop.mended", map[string]any{"name": c.ItemName(m.Item), "cost": FormatMoney(c, m.Cost)})
	}
	if !v.Resident {
		notHere = c.T("village.shop.not_here", nil)
	}
	var terms string
	if v.CanSetCap {
		terms = c.T("village.shop.terms", map[string]any{"cap": PercentFromBPS(c, int(v.PriceCapBPS)), "tax": PercentFromBPS(c, int(v.TaxBPS)),
			"wage": FormatMoney(c, v.Wage)})
		var capRow, taxRow []presenter.Button
		for _, p := range v.CapPresets {
			if p == v.PriceCapBPS {
				continue
			}
			if b, ok := keyboards.Button(c.T("village.shop.button.cap", map[string]any{"pct": PercentFromBPS(c, int(p))}), AddrShopHereCap,
				strconv.FormatInt(p, 10)); ok {
				capRow = append(capRow, b)
			}
		}
		for _, p := range v.TaxPresets {
			if p == v.TaxBPS {
				continue
			}
			if b, ok := keyboards.Button(c.T("village.shop.button.tax", map[string]any{"pct": PercentFromBPS(c, int(p))}), AddrShopHereTax,
				strconv.FormatInt(p, 10)); ok {
				taxRow = append(taxRow, b)
			}
		}
		kb.Row(capRow...)
		kb.Row(taxRow...)
	}
	text := paragraphs(
		bought, mended,
		c.T("village.shop.title", map[string]any{"village": v.Village}),
		c.T("village.shop.intro", map[string]any{"hour": FormatNumber(c, int64(v.DeliveryHour))}),
		status,
		clockLine(c, "village.shop.next", v.NextDelivery),
		c.T("village.shop.room", map[string]any{"free": FormatNumber(c, v.FreeSpace), "capacity": FormatNumber(c, v.Capacity),
			"kg": FormatNumber(c, v.FreeG/1000)}),
		notHere,
		c.T("village.shop.shelf_title", nil)+"\n"+shelf,
		lockedText, repairText, terms,
	)
	kb.Row(villageButtons(c, "village.button.materials", AddrMaterials, "village.button.build", AddrBuildMenu)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMaterials, RefreshData: AddrShopHere}))
	return c.respond(text, kb.Build())
}

// VillageShopCheckout renders the price and the ways to pay.
func VillageShopCheckout(c Context, v village.ShopCheckoutView) *presenter.Response {
	return c.withView(renderVillageShopCheckout(c, v), ScreenVillageShopCheckout, v)
}

func renderVillageShopCheckout(c Context, v village.ShopCheckoutView) *presenter.Response {
	name := c.shopItemName(v.Item, v.Kind)
	facts := []string{
		c.T("village.shop.checkout_item", map[string]any{"item": name, "qty": FormatNumber(c, v.Qty), "unit": FormatMoney(c, v.Unit)}),
		c.T("village.shop.checkout_total", map[string]any{"total": FormatMoney(c, v.Total)}),
	}
	if v.Tax > 0 {
		facts = append(facts, c.T("village.shop.checkout_tax", map[string]any{"tax": FormatMoney(c, v.Tax), "pct": PercentFromBPS(c, int(v.TaxBPS))}))
		facts = append(facts, c.T("village.shop.checkout_due", map[string]any{"due": FormatMoney(c, v.Total+v.Tax)}))
	}
	facts = append(facts, c.T("village.shop.checkout_room", map[string]any{"need": FormatNumber(c, v.Space), "free": FormatNumber(c, v.FreeSpace)}))
	kb := keyboards.New()
	var pay string
	qty := strconv.FormatInt(v.Qty, 10)
	if len(v.Payment.Usable) > 0 {
		pay = body(c.T("payment.choose", nil), c.paymentNote(v.Payment))
		c.paymentButtons(kb, v.Payment, func(m string) []string {
			return []string{AddrShopHereBuy, v.Item.Code, qty, m, v.Nonce}
		})
	} else {
		pay = body(c.T("payment.cannot_afford", nil), c.paymentNote(v.Payment))
		if btn, ok := keyboards.Button(c.T("button.bank", nil), AddrBank); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrShopHere}))
	return c.respond(paragraphs(c.T("village.shop.checkout_title", map[string]any{"village": v.Village}), body(facts...), pay), kb.Build())
}

// VillageShopRefusal renders a purchase or a mending refused.
func VillageShopRefusal(c Context, v village.ShopRefusalView) *presenter.Response {
	return c.withView(renderVillageShopRefusal(c, v), ScreenVillageShopRefusal, v)
}

func renderVillageShopRefusal(c Context, v village.ShopRefusalView) *presenter.Response {
	args := map[string]any{
		"item": c.ItemName(v.Item), "stock": FormatNumber(c, v.Stock), "left": FormatNumber(c, v.LeftToday),
		"free": FormatNumber(c, v.FreeSpace), "need": FormatNumber(c, v.NeedSpace),
		"freekg": FormatNumber(c, v.FreeG/1000), "needkg": FormatNumber(c, (v.NeedG+999)/1000),
		"min": PercentFromBPS(c, int(v.Min)), "max": PercentFromBPS(c, int(v.Max)),
	}
	lines := []string{c.T("village.shop.refused."+v.Kind, args)}
	if v.Kind == village.ShopRefusedClosed {
		lines = []string{c.T("village.shop.closed."+v.Closed, nil)}
	}
	if !v.NextDelivery.IsZero() {
		lines = append(lines, clockLine(c, "village.shop.next", v.NextDelivery))
	}
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T("village.shop.button.back", nil), AddrShopHere); ok {
		kb.Row(b)
	}
	if v.Kind == village.ShopRefusedNoSpace || v.Kind == village.ShopRefusedTooHeavy {
		if b, ok := keyboards.Button(c.T("item.button.bag", nil), AddrInventory); ok {
			kb.Row(b)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMaterials}))
	return c.respond(body(lines...), kb.Build())
}
