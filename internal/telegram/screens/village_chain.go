package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «زنجیرهٔ ساخت» (docs/adr/0075 8), drawn for Telegram: how fast each kind of workplace makes and uses its goods, and for a target the
// chain of buildings it needs and the first link that cannot keep up.

// Screens and addresses.
const (
	ScreenChain = village.ScreenChain
	AddrChain   = village.AddrChain
)

// Chain renders the chain screen.
func Chain(c Context, v village.ChainView) *presenter.Response {
	return c.withView(renderChain(c, v), ScreenChain, v)
}

// perHour writes a rate in thousandths of a unit an hour as units an hour, one decimal at most.
func perHour(c Context, milli int64) string {
	whole, frac := milli/1000, (milli%1000)/100
	if frac == 0 {
		return FormatNumber(c, whole)
	}
	return FormatNumber(c, whole) + "." + FormatNumber(c, frac)
}

func renderChain(c Context, v village.ChainView) *presenter.Response {
	kb := keyboards.New()
	blocks := []string{c.T("chain.title", map[string]any{"name": v.Village})}
	if v.Target.Code != "" {
		blocks = append(blocks, c.T("chain.target", map[string]any{"item": v.Target.Name, "rate": perHour(c, v.PerHourMilli)}))
		var ls []string
		for _, l := range v.Lines {
			key := "chain.line"
			if l.Starved {
				key = "chain.line_starved"
			}
			ls = append(ls, c.T(key, map[string]any{"item": l.Item.Name, "node": c.SettlementBuildingName(l.Node), "need": perHour(c, l.NeedMilli), "have": perHour(c, l.HaveMilli),
				"count": FormatNumber(c, l.Count), "running": FormatNumber(c, l.Running), "add": FormatNumber(c, l.Add)}))
		}
		blocks = append(blocks, body(ls...))
		for _, r := range v.Raw {
			blocks = append(blocks, c.T("chain.raw", map[string]any{"item": r.Item.Name, "need": perHour(c, r.NeedMilli), "stock": FormatNumber(c, r.Stock)}))
		}
		if f := v.First; f != nil {
			blocks = append(blocks, c.T("chain.first", map[string]any{"item": f.Item.Name, "node": c.SettlementBuildingName(f.Node), "have": perHour(c, f.HaveMilli), "need": perHour(c, f.NeedMilli), "add": FormatNumber(c, f.Add)}))
		} else {
			blocks = append(blocks, c.T("chain.keeps_up", nil))
		}
	} else {
		var ns []string
		for _, n := range v.Nodes {
			var outs []string
			for _, o := range n.Out {
				outs = append(outs, perHour(c, o.Milli)+" "+o.Item.Name)
			}
			ns = append(ns, c.T("chain.node", map[string]any{"node": c.SettlementBuildingName(n.Node), "built": FormatNumber(c, int64(n.Built)), "running": FormatNumber(c, int64(n.Running)), "out": joinComma(outs)}))
		}
		if len(ns) == 0 {
			blocks = append(blocks, c.T("chain.no_nodes", nil))
		} else {
			blocks = append(blocks, body(ns...))
		}
		for _, t := range v.Targets {
			addBtn(kb, c.T("chain.button.target", map[string]any{"item": t.Name}), village.AddrChain, t.Code)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrChain}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

func joinComma(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += "، "
		}
		out += s
	}
	return out
}
