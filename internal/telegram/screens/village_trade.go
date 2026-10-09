package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «بازرگان» (docs/adr/0049), drawn for Telegram: the goods that may be put on sale to the travelling trader, what he
// pays, the head's orders as buttons and the last market day.

// Screen and address.
const (
	ScreenTradeDesk = village.ScreenTradeDesk
	AddrTradeDesk   = village.AddrTradeDesk
)

// TradeDeskView is the trade desk.
type TradeDeskView = village.TradeDeskView

// TradeDesk renders the trade desk.
func TradeDesk(c Context, v village.TradeDeskView) *presenter.Response {
	return c.withView(renderTradeDesk(c, v), ScreenTradeDesk, v)
}

func renderTradeDesk(c Context, v village.TradeDeskView) *presenter.Response {
	kb := keyboards.New()
	head := body(
		c.T("trade.title", map[string]any{"name": v.Name}),
		c.T("trade.rules", map[string]any{"percent": PercentFromBPS(c, int(v.PriceBPS)), "cap": FormatMoney(c, v.Cap)}),
	)
	if !v.HasPost {
		return c.respond(paragraphs(head, c.T("trade.no_post", nil)), navOnly(c, kb))
	}
	var lines []string
	for _, it := range v.Items {
		args := map[string]any{"item": c.ComponentName(it.Item), "stock": FormatNumber(c, it.Stock), "unit": FormatMoney(c, it.Unit),
			"ref": FormatMoney(c, it.Reference), "keep": FormatNumber(c, it.Keep), "surplus": FormatNumber(c, it.Surplus)}
		key := "trade.item.off"
		if it.On {
			key = "trade.item.on"
		}
		lines = append(lines, c.T(key, args))
		if !v.MayOrder {
			continue
		}
		name := c.ComponentName(it.Item)
		var row []presenter.Button
		if b, ok := keyboards.Button(c.T("trade.button.all", map[string]any{"item": name}), AddrTradeDesk, village.TradeActionKeep, it.Item.Code, "0"); ok {
			row = append(row, b)
		}
		for _, k := range v.KeepPresets {
			if b, ok := keyboards.Button(c.T("trade.button.keep", map[string]any{"n": FormatNumber(c, k)}), AddrTradeDesk, village.TradeActionKeep, it.Item.Code, strconv.FormatInt(k, 10)); ok {
				row = append(row, b)
			}
		}
		if it.On {
			if b, ok := keyboards.Button(c.T("trade.button.off", nil), AddrTradeDesk, village.TradeActionOff, it.Item.Code); ok {
				row = append(row, b)
			}
		}
		kb.Row(row...)
	}
	prospect := c.T("trade.prospect", map[string]any{"amount": FormatMoney(c, v.Prospect)})
	if !v.NextAt.IsZero() {
		prospect = paragraphs(prospect, c.T("trade.next", map[string]any{"time": FormatClock(c, v.NextAt), "date": FormatDate(c, v.NextAt)}))
	}
	if k := v.Clerk; k != nil {
		key := "trade.clerk.filled"
		if !k.Filled {
			key = "trade.clerk.empty"
		}
		prospect = paragraphs(prospect, c.T(key, map[string]any{"building": c.SettlementBuildingName(k.SeatBuilding), "wage": FormatMoney(c, k.Wage)}))
	}
	last := ""
	if l := v.Last; l != nil {
		var sold []string
		for _, s := range l.Lines {
			sold = append(sold, c.T("trade.sold_line", map[string]any{"item": c.ComponentName(s.Item), "qty": FormatNumber(c, s.Qty), "unit": FormatMoney(c, s.Unit)}))
		}
		last = c.T("trade.last."+l.Outcome, map[string]any{"gross": FormatMoney(c, l.Gross), "wage": FormatMoney(c, l.Wage), "time": FormatClock(c, l.At), "items": c.list(sold)})
	}
	return c.respond(paragraphs(head, c.T("trade.items_title", nil)+"\n"+body(lines...), prospect, last), navOnly(c, kb))
}

// navOnly closes the keyboard with the back and refresh row.
func navOnly(c Context, kb *keyboards.Builder) *presenter.Keyboard {
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMaterials, RefreshData: AddrTradeDesk}))
	return kb.Build()
}
