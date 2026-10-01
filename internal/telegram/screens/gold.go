package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The gold dealer (docs/adr/0026-finance.md section 6): its price per gram,
// buying and selling, and how the price moved.

// Gold renders the gold dealer.
func Gold(c Context, v GoldView) *presenter.Response {
	return c.withView(renderGold(c, v), ScreenGold, v)
}

func renderGold(c Context, v GoldView) *presenter.Response {
	if v.Unavailable != nil {
		return renderUnavailable(c, v.Unavailable, AddrHome)
	}
	var notice string
	if v.Notice != "" {
		notice = c.T("gold.notice."+v.Notice, moneyArgs(c, v.NoticeArgs))
	}
	head := []string{
		c.T("gold.title", nil),
		c.T("gold.price", map[string]any{"buy": FormatMoney(c, v.Buy), "sell": FormatMoney(c, v.Sell),
			"move": c.moveText(v.Mid, v.Prev)}),
		c.T("gold.stock", map[string]any{"grams": FormatNumber(c, v.Stock)}),
	}
	if !v.NextAt.IsZero() {
		head = append(head, c.T("gold.next", map[string]any{"time": FormatClock(c, v.NextAt)}))
	}
	var history []string
	if len(v.History) > 0 {
		history = append(history, c.T("gold.history", nil))
		for _, p := range v.History {
			history = append(history, c.T("gold.history_line", map[string]any{"price": FormatMoney(c, p.Price),
				"time": FormatClock(c, p.At)}))
		}
	}
	var mine string
	if !c.Shared {
		mine = c.T("gold.holding", map[string]any{"grams": FormatNumber(c, v.Grams),
			"value": FormatMoney(c, v.Grams*v.Sell), "cost": FormatMoney(c, v.Cost)})
	}
	kb := keyboards.New()
	if !c.Shared {
		var buys, sells []presenter.Button
		for _, g := range v.Options {
			if btn, ok := keyboards.Button(c.T("gold.button.buy", map[string]any{"grams": FormatNumber(c, g),
				"price": FormatMoney(c, g*v.Buy)}), AddrGoldBuy, strconv.FormatInt(g, 10)); ok {
				buys = append(buys, btn)
			}
			if g <= v.Grams {
				if btn, ok := keyboards.Button(c.T("gold.button.sell", map[string]any{"grams": FormatNumber(c, g),
					"price": FormatMoney(c, g*v.Sell)}), AddrGoldSell, strconv.FormatInt(g, 10)); ok {
					sells = append(sells, btn)
				}
			}
		}
		kb.Grid(2, buys...)
		kb.Grid(2, sells...)
	}
	kb.Add(c.T("finance.button.portfolio", nil), AddrPortfolio)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrGold}))
	return c.respond(paragraphs(notice, body(head...), body(history...), mine), kb.Build())
}

// GoldTrade renders a purchase's price with the ways to pay, or a sale's
// confirmation.
func GoldTrade(c Context, v GoldTradeView) *presenter.Response {
	return c.withView(renderGoldTrade(c, v), ScreenGoldTrade, v)
}

func renderGoldTrade(c Context, v GoldTradeView) *presenter.Response {
	args := map[string]any{"grams": FormatNumber(c, v.Grams), "price": FormatMoney(c, v.Price),
		"total": FormatMoney(c, v.Total)}
	kb := keyboards.New()
	text := c.T("gold.confirm_"+v.Side, args)
	if v.Side == "buy" {
		c.paymentButtons(kb, v.Payment, func(m string) []string {
			return []string{AddrGoldBuy, strconv.FormatInt(v.Grams, 10), m, v.Nonce}
		})
		text = paragraphs(text, c.paymentNote(v.Payment))
	} else {
		kb.Add(c.T("gold.button.sell_yes", args), AddrGoldSell, strconv.FormatInt(v.Grams, 10), v.Nonce)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGold}))
	return c.respond(text, kb.Build()).MarkPrivate()
}
