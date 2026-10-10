package screens

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «مدیریت قطعهٔ من» (docs/adr/0045 phase B1, section 3.3), drawn for Telegram: the list of my buildings, one
// building's function, floor and contents, the acts as buttons (add a module, take one out, the next level, a
// storey, a change of use, a saved layout), and the ask and done stages. The look is generated; nothing here asks
// for a wall.

// Screen and address.
const (
	ScreenLotManage = village.ScreenLotManage
	AddrLotManage   = village.AddrLotManage
)

// LotManage renders the screen.
func LotManage(c Context, v village.LotManageView) *presenter.Response {
	return c.withView(renderLotManage(c, v), ScreenLotManage, v)
}

func (c Context) moduleName(n Named) string { return c.named("module_kind."+n.Code, n.Name) }
func (c Context) functionName(code, authored string) string {
	return c.named("building_function."+code, authored)
}

func lotCount(n int) string { return strconv.Itoa(n) }

func itemLines(c Context, lines []village.WorkItemLine) string {
	var out []string
	for _, l := range lines {
		out = append(out, c.T("village.lot.item", map[string]any{"item": c.named("component."+l.Item.Code, l.Item.Name), "qty": FormatNumber(c, l.Qty)}))
	}
	return strings.Join(out, "، ")
}

func lotAdds(c Context, adds []village.LotWorkAdd) string {
	var out []string
	for _, a := range adds {
		out = append(out, c.T("village.lot.add_line", map[string]any{"name": c.moduleName(a.Module), "n": FormatNumber(c, int64(a.Count))}))
	}
	return strings.Join(out, "، ")
}

func renderLotManage(c Context, v village.LotManageView) *presenter.Response {
	kb := keyboards.New()
	switch v.Stage {
	case village.LotMenu:
		lines := []string{c.T("village.lot.title", map[string]any{"village": v.Village})}
		if len(v.Buildings) == 0 {
			lines = append(lines, c.T("village.lot.none", nil))
		}
		for _, b := range v.Buildings {
			state := c.functionName(b.Function, b.FunctionName)
			if !b.Built {
				state = c.T("village.lot.going_up", nil)
			}
			label := c.T("village.lot.menu_line", map[string]any{"name": c.SettlementBuildingName(b.Building), "row": FormatNumber(c, int64(b.Y+1)),
				"col": FormatNumber(c, int64(b.X+1)), "state": state})
			if bt, ok := keyboards.Button(label, AddrLotManage, b.ID); ok {
				kb.Row(bt)
			}
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrMine, RefreshData: AddrLotManage}))
		return c.respond(paragraphs(lines...), kb.Build())
	case village.LotAsk, village.LotDone:
		return renderLotAct(c, v, kb)
	}
	return renderLotDetail(c, v, kb)
}

func renderLotDetail(c Context, v village.LotManageView, kb *keyboards.Builder) *presenter.Response {
	name := c.SettlementBuildingName(v.Building)
	lines := []string{c.T("village.lot.head", map[string]any{"name": name, "village": v.Village})}
	if !v.Built {
		lines = append(lines, c.T("village.lot.not_built", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrLotManage, RefreshData: AddrLotManage + ":" + v.ID}))
		return c.respond(paragraphs(lines...), kb.Build())
	}
	f := v.Function
	lines = append(lines, c.T("village.lot.function", map[string]any{"fn": c.functionName(f.Code, f.Name), "level": FormatNumber(c, int64(f.Level)),
		"max": FormatNumber(c, int64(f.MaxLevel))}))
	lines = append(lines, c.T("village.lot.floor", map[string]any{"storeys": FormatNumber(c, int64(v.Storeys)), "max": FormatNumber(c, int64(v.MaxStoreys)),
		"stability": PercentFromBPS(c, v.StabilityBPS), "used": FormatNumber(c, int64(v.AreaUsed)), "cap": FormatNumber(c, int64(v.AreaCapacity))}))
	var inside []string
	for _, m := range v.Modules {
		inside = append(inside, c.T("village.lot.module_line", map[string]any{"name": c.moduleName(m.Module), "n": FormatNumber(c, int64(m.Count)),
			"inc": FormatNumber(c, int64(m.Included)), "max": FormatNumber(c, int64(m.Max))}))
	}
	if len(inside) > 0 {
		lines = append(lines, c.T("village.lot.inside", nil)+"\n"+body(inside...))
	}
	var gives []string
	if v.HousingCapacity > 0 {
		gives = append(gives, c.T("village.lot.gives_housing", map[string]any{"n": FormatNumber(c, v.HousingCapacity)}))
	}
	if v.PersonalStorage > 0 {
		gives = append(gives, c.T("village.lot.gives_storage", map[string]any{"n": FormatNumber(c, v.PersonalStorage)}))
	}
	if v.StallSlots > 0 {
		gives = append(gives, c.T("village.lot.gives_stall", map[string]any{"n": FormatNumber(c, v.StallSlots)}))
	}
	if len(gives) > 0 {
		lines = append(lines, strings.Join(gives, "\n"))
	}
	if w := v.Work; w != nil {
		what := lotAdds(c, w.Adds)
		if w.LevelTo > 0 {
			what = strings.Trim(what+"، "+c.T("village.lot.what_level", map[string]any{"n": FormatNumber(c, int64(w.LevelTo))}), "، ")
		}
		if w.StoreysTo > 0 {
			what = strings.Trim(what+"، "+c.T("village.lot.what_storey", map[string]any{"n": FormatNumber(c, int64(w.StoreysTo))}), "، ")
		}
		if w.ConvertTo.Code != "" {
			what = strings.Trim(what+"، "+c.T("village.lot.what_convert", map[string]any{"name": c.functionName(w.ConvertTo.Code, w.ConvertTo.Name)}), "، ")
		}
		lines = append(lines, c.T("village.lot.work", map[string]any{"what": what, "progress": PercentFromBPS(c, int(w.ProgressBPS))}))
		if !w.JobOpen {
			lines = append(lines, c.T("village.lot.work_no_job", nil))
		}
	}
	if l := v.Look; l != nil {
		lines = append(lines, c.T("village.lot.look", map[string]any{"material": c.T("village.lot.material."+l.Material, nil),
			"roof": c.T("village.lot.roof."+l.Roof, nil), "windows": FormatNumber(c, int64(l.Windows)), "storeys": FormatNumber(c, int64(l.Storeys))}))
	}
	if cb := craftBlock(c, v, kb); len(cb) > 0 {
		lines = append(lines, body(cb...))
	}
	lines = append(lines, c.T("village.lot.cash", map[string]any{"cash": FormatMoney(c, v.Cash)}))

	if v.CanManage && v.Work == nil {
		btn := func(label string, parts ...string) {
			if b, ok := keyboards.Button(label, append([]string{AddrLotManage, v.ID}, parts...)...); ok {
				kb.Row(b)
			}
		}
		for _, a := range v.Additions {
			if a.Can {
				btn(c.T("village.lot.button.add", map[string]any{"name": c.moduleName(a.Module), "mats": itemLines(c, a.Materials)}), village.LotActionAdd, a.Module.Code, "1")
			}
		}
		for _, m := range v.Modules {
			if m.Removable {
				btn(c.T("village.lot.button.remove", map[string]any{"name": c.moduleName(m.Module)}), village.LotActionRemove, m.Module.Code, "1")
			}
		}
		if u := v.Upgrade; u != nil && u.Can {
			btn(c.T("village.lot.button.level", map[string]any{"n": FormatNumber(c, int64(u.To))}), village.LotActionLevel)
		}
		if s := v.StoreyUp; s != nil && s.Can {
			btn(c.T("village.lot.button.storey", map[string]any{"n": FormatNumber(c, int64(s.To))}), village.LotActionStorey)
		}
		for _, fn := range v.Functions {
			if fn.Available && !fn.Current {
				btn(c.T("village.lot.button.function", map[string]any{"name": c.functionName(fn.Function.Code, fn.Function.Name)}), village.LotActionFunction, fn.Function.Code)
			}
		}
		for _, t := range v.Templates {
			if t.Applicable {
				btn(c.T("village.lot.button.template_apply", map[string]any{"name": t.Name}), village.LotActionTemplateApply, t.ID)
			}
		}
		btn(c.T("village.lot.button.template_save", nil), village.LotActionTemplateSave)
	}
	for _, t := range v.Templates {
		if t.Mine {
			if b, ok := keyboards.Button(c.T("village.lot.button.template_delete", map[string]any{"name": t.Name}), AddrLotManage, v.ID, village.LotActionTemplateDelete, t.ID); ok {
				kb.Row(b)
			}
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLotManage, RefreshData: AddrLotManage + ":" + v.ID}))
	return c.respond(paragraphs(lines...), kb.Build())
}

func renderLotAct(c Context, v village.LotManageView, kb *keyboards.Builder) *presenter.Response {
	name := c.SettlementBuildingName(v.Building)
	lines := []string{c.T("village.lot.head", map[string]any{"name": name, "village": v.Village})}
	q := v.Quote
	if v.Stage == village.LotDone {
		key := "village.lot.done." + v.Action
		text := c.T(key, map[string]any{"code": v.ShareCode, "name": v.Name})
		lines = append(lines, text)
		if v.Work != nil {
			lines = append(lines, c.T("village.lot.work", map[string]any{"what": lotAdds(c, v.Work.Adds), "progress": PercentFromBPS(c, int(v.Work.ProgressBPS))}))
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrLotManage + ":" + v.ID, RefreshData: AddrLotManage + ":" + v.ID}))
		return c.respond(paragraphs(lines...), kb.Build())
	}
	lines = append(lines, c.T("village.lot.ask."+v.Action, map[string]any{"code": v.Code, "name": v.Name, "n": FormatNumber(c, int64(v.N))}))
	if q != nil {
		if len(q.Adds) > 0 {
			lines = append(lines, c.T("village.lot.quote.builds", map[string]any{"what": lotAdds(c, q.Adds)}))
		}
		if q.LevelTo > 0 {
			lines = append(lines, c.T("village.lot.quote.level", map[string]any{"n": FormatNumber(c, int64(q.LevelTo))}))
		}
		if q.StoreysTo > 0 {
			lines = append(lines, c.T("village.lot.quote.storeys", map[string]any{"n": FormatNumber(c, int64(q.StoreysTo))}))
		}
		if q.ConvertTo.Code != "" {
			lines = append(lines, c.T("village.lot.quote.convert", map[string]any{"name": c.functionName(q.ConvertTo.Code, q.ConvertTo.Name)}))
		}
		for _, m := range q.Materials {
			lines = append(lines, c.T("village.lot.quote.material", map[string]any{"item": c.named("component."+m.Item.Code, m.Item.Name),
				"have": FormatNumber(c, m.Have), "need": FormatNumber(c, m.Need)}))
		}
		if q.Money > 0 {
			lines = append(lines, c.T("village.lot.quote.money", map[string]any{"money": FormatMoney(c, q.Money)}))
		}
		if q.FeeSUP > 0 {
			lines = append(lines, c.T("village.lot.quote.fee", map[string]any{"fee": FormatMoney(c, q.FeeSUP)}))
		}
		if q.Shifts > 0 {
			lines = append(lines, c.T("village.lot.quote.shifts", map[string]any{"n": FormatNumber(c, int64(q.Shifts)), "wages": FormatMoney(c, q.Wages)}))
		}
		if len(q.Salvage) > 0 {
			lines = append(lines, c.T("village.lot.quote.salvage", map[string]any{"items": itemLines(c, q.Salvage)}))
		}
		for _, sk := range q.Skipped {
			lines = append(lines, c.T("village.lot.quote.skipped", map[string]any{"name": c.moduleName(sk)}))
		}
	}
	if v.Reason != "" {
		lines = append(lines, c.T("village.lot.reason."+v.Reason, nil))
	} else if b, ok := keyboards.Button(c.T("village.lot.button.confirm", nil), AddrLotManage, v.ID, v.Action, v.Code, lotCount(v.N), v.Name, village.ResidenceConfirm); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLotManage + ":" + v.ID, RefreshData: AddrLotManage + ":" + v.ID}))
	return c.respond(paragraphs(lines...), kb.Build())
}
