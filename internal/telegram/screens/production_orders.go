package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Production orders — what the company can make, the plan of one order with
// what it consumes and how long it runs (or every input it is short of), the
// orders running and done — and the reverse-engineering lab.

// Orders renders the production floor.
func Orders(c Context, v OrdersView) *presenter.Response {
	head := body(c.T("production.orders_title", map[string]any{"name": v.Ref.Name}),
		c.T("production.orders_crew", map[string]any{"crew": FormatNumber(c, int64(v.Crew)),
			"running": FormatNumber(c, int64(v.Running)), "max": FormatNumber(c, int64(v.Max))}))
	var running, done []string
	for _, o := range v.Orders {
		args := map[string]any{"no": FormatNumber(c, o.No), "good": c.GoodName(o.Good), "qty": FormatNumber(c, o.Output),
			"quality": FormatNumber(c, int64(o.Quality)), "time": FormatClock(c, o.FinishAt), "duration": FormatDuration(c, o.Left)}
		if o.Done {
			done = append(done, c.T("production.order_done", args))
		} else {
			running = append(running, c.T("production.order_running", args))
		}
	}
	orders := ""
	if len(running) > 0 {
		orders = body(append([]string{c.T("production.orders_running", nil)}, running...)...)
	}
	if len(done) > 0 {
		orders = paragraphs(orders, body(append([]string{c.T("production.orders_done", nil)}, done...)...))
	}
	if orders == "" {
		orders = c.T("production.orders_none", nil)
	}
	kb := keyboards.New()
	var buttons []presenter.Button
	for _, t := range v.Targets {
		if btn, ok := keyboards.Button(c.T("production.button.produce", map[string]any{"good": c.GoodName(t.Good)}),
			AddrProduce, v.Ref.Code, t.Good.TargetArg()); ok {
			buttons = append(buttons, btn)
		}
	}
	targets := c.T("production.orders_targets", nil)
	if len(buttons) == 0 {
		targets = c.T("production.orders_no_targets", nil)
	}
	kb.Grid(2, buttons...)
	locked := ""
	if len(v.Locked) > 0 {
		lines := []string{c.T("production.orders_locked", nil)}
		for _, l := range v.Locked {
			lines = append(lines, c.T("production.orders_locked_line", map[string]any{"good": c.GoodName(l.Good),
				"hint": c.unlockHint(l.Steps)}))
		}
		locked = body(lines...)
	}
	c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code}, AddrOrders, v.Ref.Code)
	return c.respond(paragraphs(head, orders, targets, locked), kb.Build())
}

// Produce renders the plan of an order.
func Produce(c Context, v ProduceView) *presenter.Response {
	good := c.GoodName(v.Target.Good)
	target := v.Target.Good.TargetArg()
	addr := AddrProduce
	if v.Kit {
		addr = AddrProduceKit
	}
	if p := v.Placed; p != nil {
		text := paragraphs(c.T("production.placed", map[string]any{"no": FormatNumber(c, p.No), "good": good,
			"qty": FormatNumber(c, p.Output), "time": FormatClock(c, p.FinishAt), "duration": FormatDuration(c, p.Left)}),
			c.T("production.placed_hint", nil))
		kb := keyboards.New()
		kb.Add(c.T("production.button.orders", nil), AddrOrders, v.Ref.Code)
		c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code})
		return c.respond(text, kb.Build())
	}
	head := c.T("production.produce_title", map[string]any{"good": good})
	if v.Target.Batch > 0 {
		head = body(head, c.T("production.produce_batch", map[string]any{"batch": FormatNumber(c, v.Target.Batch)}))
	}
	var lines []string
	for _, r := range v.Recipe {
		args := map[string]any{"component": c.ComponentName(r.Component), "per": FormatNumber(c, r.Per),
			"need": FormatNumber(c, r.Need), "have": FormatNumber(c, r.Have)}
		key := "production.recipe_line"
		if v.Qty > 0 {
			key = "production.recipe_line_order"
			if r.Have < r.Need {
				key = "production.recipe_line_short"
			}
		}
		lines = append(lines, c.T(key, args))
	}
	recipe := body(append([]string{c.T("production.recipe_title", nil)}, lines...)...)
	kb := keyboards.New()
	var plan string
	switch {
	case v.Qty > 0 && len(v.Short) > 0:
		lines := []string{c.T("production.plan_short", map[string]any{"qty": FormatNumber(c, v.Qty)})}
		for _, sh := range v.Short {
			if sh.Source == "" {
				continue
			}
			lines = append(lines, c.T("production.short_source."+sh.SourceKey(), map[string]any{
				"component": c.ComponentName(sh.Component), "qty": FormatNumber(c, sh.Need-sh.Have)}))
		}
		plan = body(lines...)
		c.shortageButton(kb, v, target)
	case v.Qty > 0:
		plan = body(c.T("production.plan", map[string]any{"qty": FormatNumber(c, v.Output), "good": good,
			"duration": FormatDuration(c, v.Duration), "crew": FormatNumber(c, int64(v.Crew))}),
			c.T("production.plan_consumes", nil))
		kb.Add(c.T("production.button.start_order", map[string]any{"qty": FormatNumber(c, v.Output)}),
			addr, v.Ref.Code, target, strconv.FormatInt(v.Qty, 10), ProductionConfirm)
	default:
		plan = c.T("production.plan_choose", map[string]any{"max": FormatNumber(c, v.MaxQty)})
	}
	var sizes []presenter.Button
	for _, q := range ProducePresets {
		label := c.T("production.button.size", map[string]any{"qty": FormatNumber(c, q)})
		if v.Target.Batch > 0 {
			label = c.T("production.button.size_batch", map[string]any{"qty": FormatNumber(c, q)})
		}
		if btn, ok := keyboards.Button(label, addr, v.Ref.Code, target, strconv.FormatInt(q, 10)); ok {
			sizes = append(sizes, btn)
		}
	}
	if v.MaxQty > 0 && !contains64(ProducePresets, v.MaxQty) {
		if btn, ok := keyboards.Button(c.T("production.button.size_max", map[string]any{"qty": FormatNumber(c, v.MaxQty)}),
			addr, v.Ref.Code, target, strconv.FormatInt(v.MaxQty, 10)); ok {
			sizes = append(sizes, btn)
		}
	}
	kb.Grid(4, sizes...)
	c.productionNav(kb, []string{AddrOrders, v.Ref.Code})
	bought := ""
	if v.Bought > 0 {
		bought = c.T("production.stock_up_done", map[string]any{"total": FormatMoney(c, v.Bought)})
	}
	return c.respond(paragraphs(bought, head, recipe, plan), kb.Build())
}

func contains64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Reverse engineering.

// ReverseLab renders the reverse-engineering lab.
func ReverseLab(c Context, v ReverseLabView) *presenter.Response {
	kb := keyboards.New()
	if s := v.Confirm; s != nil {
		text := c.T("production.reverse_confirm", map[string]any{"good": c.GoodName(s.Good), "maker": s.Maker,
			"chance": PercentFromBPS(c, int(s.ChanceBPS)), "duration": FormatDuration(c, v.Time)})
		kb.Add(c.T("production.button.reverse_confirm", nil), AddrReverse, v.Ref.Code, s.Serial, ProductionConfirm)
		c.productionNav(kb, []string{AddrReverseLab, v.Ref.Code})
		return c.respond(text, kb.Build())
	}
	head := body(c.T("production.relab_title", map[string]any{"name": v.Ref.Name}),
		c.T("production.relab_engineer", map[string]any{"skill": c.SkillName(v.Skill), "level": FormatNumber(c, int64(v.Level))}))
	var samples []string
	var buttons []presenter.Button
	for _, s := range v.Samples {
		samples = append(samples, c.T("production.sample_line", map[string]any{"good": c.GoodName(s.Good), "maker": s.Maker,
			"quality": FormatNumber(c, int64(s.Quality)), "chance": PercentFromBPS(c, int(s.ChanceBPS))}))
		if btn, ok := keyboards.Button(c.T("production.button.reverse", map[string]any{"good": c.GoodName(s.Good)}),
			AddrReverse, v.Ref.Code, s.Serial); ok {
			buttons = append(buttons, btn)
		}
	}
	sampleBlock := body(append([]string{c.T("production.relab_samples", nil)}, samples...)...)
	if len(samples) == 0 {
		sampleBlock = c.T("production.relab_no_samples", nil)
	}
	var jobs []string
	for _, j := range v.Jobs {
		jobs = append(jobs, c.T("production.reverse_line."+j.Status, map[string]any{"no": FormatNumber(c, j.No),
			"good": c.GoodName(j.Good), "result": j.Result, "time": FormatClock(c, j.FinishAt), "duration": FormatDuration(c, j.Left)}))
		if j.ResultNo > 0 {
			if btn, ok := keyboards.Button(c.T("production.button.design", map[string]any{"name": j.Result}),
				AddrDesign, strconv.FormatInt(j.ResultNo, 10)); ok {
				buttons = append(buttons, btn)
			}
		}
	}
	jobBlock := ""
	if len(jobs) > 0 {
		jobBlock = body(append([]string{c.T("production.relab_jobs", nil)}, jobs...)...)
	}
	kb.Grid(2, buttons...)
	c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code}, AddrReverseLab, v.Ref.Code)
	started := ""
	if v.Started != nil {
		started = c.T("production.reverse_started", map[string]any{"good": c.GoodName(v.Started.Good),
			"time": FormatClock(c, v.Started.FinishAt), "duration": FormatDuration(c, v.Started.Left)})
	}
	return c.respond(paragraphs(started, head, sampleBlock, jobBlock, c.T("production.relab_hint", nil)), kb.Build())
}

// shortageButton is the one way forward from a short order: buy it all from
// the suppliers in one tap, make the missing part first, or buy it from
// other companies.
func (c Context) shortageButton(kb *keyboards.Builder, v ProduceView, target string) {
	if v.StockUp > 0 {
		kb.Add(c.T("production.button.stock_up", map[string]any{"total": FormatMoney(c, v.StockUp)}),
			AddrStockUp, v.Ref.Code, target, strconv.FormatInt(v.Qty, 10))
		return
	}
	for _, s := range v.Short {
		if s.Source == ShortMadeHere {
			kb.Add(c.T("production.button.produce", map[string]any{"good": c.ComponentName(s.Component)}),
				AddrProduce, v.Ref.Code, s.Component.Code)
			return
		}
	}
	for _, s := range v.Short {
		if s.Source == ShortFromCompanies {
			kb.Add(c.T("production.button.goods", nil), AddrCompanyGoods)
			return
		}
	}
	kb.Add(c.T("production.button.suppliers", nil), AddrSuppliers, v.Ref.Code)
}
