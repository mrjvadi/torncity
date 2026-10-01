package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The production economy (docs/adr/0021-production-economy.md): a company's
// warehouse and the NPC suppliers it buys basic inputs from, its research
// lab and the technologies it owns and licenses, its design studio, its
// production orders, its reverse-engineering lab, and its goods for sale —
// and the city's company goods a player or a company buys.
//
// Running a company's floor is the owner's or the manager's business, in
// their private chat; what a company sells is public in its city. A
// component is named component.<code>, a technology technology.<code>, a
// supplier supplier.<code>, a design's slot design_slot.<name> and an
// attribute attribute.<name>; a design by the name its company gave it.

// The commands a «✏️» button of the production screens asks a typed value
// for (configs/commands.yml, section input).
const (
	commandSupply      = "company.supply"
	commandTechMode    = "company.techmode"
	commandDesignQty   = "company.dqty"
	commandDesignName  = "company.dname"
	commandSell        = "company.sell"
	commandCompanyBuy  = "company.buy"
	commandProduceSize = "company.produce"
)

// DesignTarget is the argument that names a design as a target.
func DesignTarget(no int64) string { return presentation.DesignTarget(no) }

// ComponentName is a component's display name.
func (c Context) ComponentName(n Named) string { return c.named("component."+n.Code, n.Name) }

// TechName is a technology's display name.
func (c Context) TechName(n Named) string { return c.named("technology."+n.Code, n.Name) }

// SupplierName is an NPC supplier's display name.
func (c Context) SupplierName(n Named) string { return c.named("supplier."+n.Code, n.Name) }

// SlotName is a design slot's display name.
func (c Context) SlotName(slot string) string { return c.named("design_slot."+slot, slot) }

// AttributeName is a product attribute's display name.
func (c Context) AttributeName(name string) string { return c.named("attribute."+name, name) }

// GoodName renders a good: a design's name with its kind, or the plain name.
func (c Context) GoodName(g Good) string {
	if g.Component {
		return c.ComponentName(g.Item)
	}
	if g.Design != "" {
		return c.T("production.good_designed", map[string]any{"design": g.Design, "item": c.ItemName(g.Item)})
	}
	return c.ItemName(g.Item)
}

// productionBack is the navigation back to a company's warehouse.
func (c Context) productionNav(kb *keyboards.Builder, back []string, refresh ...string) {
	n := keyboards.Nav{BackData: keyboards.Data(back...)}
	if len(refresh) > 0 {
		n.RefreshData = keyboards.Data(refresh...)
	}
	kb.Nav(c.nav(n))
}

// ---------------------------------------------------------------------------
// The warehouse.

// Warehouse renders a company's warehouse, the hub of its floor.
func Warehouse(c Context, v WarehouseView) *presenter.Response {
	head := body(c.T("production.warehouse_title", map[string]any{"name": v.Ref.Name}),
		c.T("production.warehouse_status", map[string]any{
			"orders": FormatNumber(c, int64(v.Running)), "listings": FormatNumber(c, int64(v.Listings))}))
	if v.Researching {
		head = body(head, c.T("production.warehouse_researching", nil))
	}
	var lines []string
	var sell []presenter.Button
	for _, l := range v.Lines {
		key := "production.stock_line"
		args := map[string]any{"good": c.GoodName(l.Good), "qty": FormatNumber(c, l.Qty)}
		if l.Quality > 0 {
			key = "production.stock_line_pieces"
			args["quality"] = FormatNumber(c, int64(l.Quality))
		}
		line := c.T(key, args)
		if l.Listed > 0 {
			line += c.T("production.stock_listed", map[string]any{"qty": FormatNumber(c, l.Listed)})
		}
		lines = append(lines, line)
		if l.Sellable {
			if btn, ok := keyboards.Button(c.T("production.button.sell", map[string]any{"good": c.GoodName(l.Good)}),
				AddrSell, v.Ref.Code, l.Good.TargetArg()); ok {
				sell = append(sell, btn)
			}
		}
	}
	stock := body(append([]string{c.T("production.warehouse_stock", nil)}, lines...)...)
	switch {
	case len(lines) == 0 && v.Next != nil:
		// The next step says what to do about it.
		stock = c.T("production.warehouse_empty_short", nil)
	case len(lines) == 0:
		stock = c.T("production.warehouse_empty", nil)
	}
	kb := keyboards.New()
	next := c.nextStep(kb, v.Ref.Code, v.Next)
	kb.Grid(2, sell...)
	var hub []presenter.Button
	add := func(key string, parts ...string) {
		if btn, ok := keyboards.Button(c.T(key, nil), parts...); ok {
			hub = append(hub, btn)
		}
	}
	add("production.button.suppliers", AddrSuppliers, v.Ref.Code)
	add("production.button.orders", AddrOrders, v.Ref.Code)
	add("production.button.studio", AddrStudio, v.Ref.Code)
	if v.CanResearch {
		add("production.button.lab", AddrLab, v.Ref.Code)
	}
	add("production.button.reverse_lab", AddrReverseLab, v.Ref.Code)
	add("production.button.listings", AddrListings, v.Ref.Code)
	kb.Grid(2, hub...)
	c.productionNav(kb, []string{AddrCompanyManage, v.Ref.Code}, AddrWarehouse, v.Ref.Code)
	return c.respond(paragraphs(v.Notice, head, next, stock), kb.Build())
}

// ---------------------------------------------------------------------------
// NPC suppliers.

// Suppliers renders the NPC suppliers a company buys basic inputs from.
func Suppliers(c Context, v SuppliersView) *presenter.Response {
	notice := ""
	if b := v.Bought; b != nil {
		notice = c.T("production.supply_bought", map[string]any{"qty": FormatNumber(c, b.Qty),
			"component": c.ComponentName(b.Component), "total": FormatMoney(c, b.Total)})
	}
	head := body(c.T("production.suppliers_title", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		c.T("production.available", map[string]any{"amount": FormatMoney(c, v.Available)}))
	kb := keyboards.New()
	var lines []string
	for _, o := range v.Offers {
		name := c.ComponentName(o.Component)
		lines = append(lines, c.T("production.supply_line", map[string]any{"component": name,
			"supplier": c.SupplierName(o.Supplier), "price": FormatMoney(c, o.Price), "stock": FormatNumber(c, o.Stock)}))
		var row []presenter.Button
		for _, q := range SupplyPresets {
			if q > o.Stock {
				continue
			}
			if btn, ok := keyboards.Button(c.T("production.button.supply", map[string]any{"qty": FormatNumber(c, q), "component": name}),
				AddrSupply, v.Ref.Code, o.Component.Code, strconv.FormatInt(q, 10)); ok {
				row = append(row, btn)
			}
		}
		if o.Stock > 0 {
			if btn, ok := askButton(c.T("production.button.supply_other", map[string]any{"component": name}),
				commandSupply, v.Ref.Code, o.Component.Code); ok {
				row = append(row, btn)
			}
		}
		kb.Row(row...)
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("production.suppliers_none", nil)
	}
	c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code}, AddrSuppliers, v.Ref.Code)
	return c.respond(paragraphs(notice, head, list, c.T("production.suppliers_hint", nil)), kb.Build())
}

// ---------------------------------------------------------------------------
// Refusals.

// ProductionRefusal renders a refused production command.
func ProductionRefusal(c Context, v ProductionRefusalView) *presenter.Response {
	args := map[string]any{"name": v.Ref.Name, "level": FormatNumber(c, int64(v.Level)),
		"have": FormatNumber(c, int64(v.Have)), "need": FormatMoney(c, v.Need), "money": FormatMoney(c, v.HaveMoney),
		"max": FormatNumber(c, int64(v.Max)), "city": c.CityName(v.CityCode, v.City)}
	if v.Skill != "" {
		args["skill"] = c.SkillName(v.Skill)
	}
	if len(v.Techs) > 0 {
		names := make([]string, 0, len(v.Techs))
		for _, t := range v.Techs {
			names = append(names, c.TechName(t))
		}
		args["techs"] = c.list(names)
	}
	text := c.T("production.refused."+v.Kind, args)
	if len(v.Shortages) > 0 {
		lines := []string{text}
		for _, s := range v.Shortages {
			lines = append(lines, c.T("production.shortage_line", map[string]any{"component": c.ComponentName(s.Component),
				"need": FormatNumber(c, s.Need), "have": FormatNumber(c, s.Have)}))
		}
		text = body(lines...)
	}
	kb := keyboards.New()
	back := v.Back
	if len(back) == 0 {
		back = []string{AddrCompanyMine}
		if v.Ref.Code != "" {
			back = []string{AddrWarehouse, v.Ref.Code}
		}
	}
	if v.Kind == ProductionRefusedShortage && v.Ref.Code != "" {
		kb.Add(c.T("production.button.suppliers", nil), AddrSuppliers, v.Ref.Code)
	}
	if v.Kind == ProductionRefusedSkill {
		text = body(text, c.skillGap(kb, v.Gap))
	}
	c.productionNav(kb, back)
	return c.respond(text, kb.Build())
}

// list joins names with the language's list separator.
func (c Context) list(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += c.T("production.list_separator", nil)
		}
		out += n
	}
	return out
}

// countdown is how long is left until at, from now, never below a second.
func countdown(at, now time.Time) time.Duration {
	if d := at.Sub(now); d > time.Second {
		return d
	}
	return time.Second
}

// SkillName is a skill's display name.
func (c Context) SkillName(code string) string { return c.T("skill."+code, nil) }
