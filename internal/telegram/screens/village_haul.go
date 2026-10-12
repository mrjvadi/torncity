package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «گاری و جاده» (docs/adr/0075 6), drawn for Telegram: the carts of the town, its roads to other towns, the goods on the road and the
// carts that could be built.

// Screens and addresses.
const (
	ScreenHaul = village.ScreenHaul
	AddrHaul   = village.AddrHaul
)

// Haul renders the haul screen.
func Haul(c Context, v village.HaulView) *presenter.Response {
	return c.withView(renderHaul(c, v), ScreenHaul, v)
}

func renderHaul(c Context, v village.HaulView) *presenter.Response {
	kb := keyboards.New()
	blocks := []string{c.T("haul.title", map[string]any{"name": v.Village})}
	if v.Notice != "" {
		blocks = append(blocks, c.T("haul.notice."+v.Notice, nil))
	}
	if !v.Open {
		blocks = append(blocks, c.T("haul.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrHaul}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	var cs []string
	for _, x := range v.Carriers {
		cs = append(cs, c.T("haul.carrier", map[string]any{"mode": c.named("haul_mode."+x.Mode.Code, x.Mode.Name), "kg": FormatNumber(c, x.CapacityKg), "status": c.T("haul.carrier_status."+x.Status, nil), "where": x.Settlement.Name}))
	}
	if len(cs) == 0 {
		blocks = append(blocks, c.T("haul.no_carriers", nil))
	} else {
		blocks = append(blocks, c.T("haul.carriers_head", nil)+"\n"+body(cs...))
	}
	var ls []string
	for _, x := range v.Links {
		ls = append(ls, c.T("haul.link", map[string]any{"other": x.Other.Name, "class": c.named("road_class."+x.Class, x.Class), "km": FormatNumber(c, x.LengthM/1000), "status": c.T("haul.link_status."+x.Status, nil)}))
	}
	if len(ls) == 0 {
		blocks = append(blocks, c.T("haul.no_links", nil))
	} else {
		blocks = append(blocks, c.T("haul.links_head", nil)+"\n"+body(ls...))
	}
	var ds []string
	for _, x := range v.Loads {
		ds = append(ds, c.T("haul.load", map[string]any{"qty": FormatNumber(c, x.Qty), "item": x.Item.Name, "from": x.From.Name, "to": x.To.Name, "left": FormatDuration(c, x.Left)}))
	}
	if len(ds) > 0 {
		blocks = append(blocks, c.T("haul.loads_head", nil)+"\n"+body(ds...))
	}
	if v.MayOrder {
		for _, m := range v.Modes {
			if m.CanOrder {
				addBtn(kb, c.T("haul.button.carrier", map[string]any{"mode": c.named("haul_mode."+m.Mode.Code, m.Mode.Name), "kg": strconv.FormatInt(m.CapacityKg, 10)}), village.AddrHaul, village.HaulActionCarrier, m.Mode.Code)
			}
		}
	}
	if v.MayDraw {
		for _, t := range v.Targets {
			addBtn(kb, c.T("haul.button.link", map[string]any{"name": t.Name}), village.AddrHaul, village.HaulActionLink, t.Code, "path")
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrHaul}))
	return c.respond(paragraphs(blocks...), kb.Build())
}
