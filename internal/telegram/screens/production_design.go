package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The design studio: a company's designs, starting one for a kind of good,
// filling its slots one by one with components — a quantity typed where the
// slot takes a range — watching its attributes and its cost floor as it
// grows, naming it and finishing it.

// Design statuses and origins as screens take them.
const (
	DesignDraft             = "draft"
	DesignFinal             = "final"
	DesignAuthored          = "authored"
	DesignReverseEngineered = "reverse_engineered"
)

// DesignLine is one design of the studio.
type DesignLine struct {
	No     int64
	Name   string
	Item   Named
	Status string
	Origin string
}

// StudioView is a company's design studio.
type StudioView struct {
	Ref     CompanyRef
	Designs []DesignLine
	// Kinds are the goods the company may design now; Next those one
	// research or one license away, each with the way in. Hidden is set
	// when goods further away are kept out of sight until then
	// (docs/adr/0021, section 14).
	Kinds  []Named
	Next   []StudioKind
	Hidden bool
	// CanResearch is the owner, who alone runs the lab: the locked goods
	// lead there.
	CanResearch bool
	// Skill is the craft the best designer is measured in, per kind; the
	// studio shows the company's best level and the level needed.
	Need int
	// CanDesign is false when the company is at its design limit.
	CanDesign bool
	Max       int
}

// Studio renders the design studio.
func Studio(c Context, v StudioView) *presenter.Response {
	head := c.T("production.studio_title", map[string]any{"name": v.Ref.Name})
	kb := keyboards.New()
	var lines []string
	var open []presenter.Button
	for _, d := range v.Designs {
		name := d.Name
		if name == "" {
			name = c.T("production.design_unnamed", map[string]any{"no": FormatNumber(c, d.No)})
		}
		key := "production.design_line." + d.Status
		if d.Origin == DesignReverseEngineered {
			key = "production.design_line.copy"
		}
		lines = append(lines, c.T(key, map[string]any{"name": name, "item": c.ItemName(d.Item)}))
		if btn, ok := keyboards.Button(c.T("production.button.design", map[string]any{"name": name}),
			AddrDesign, strconv.FormatInt(d.No, 10)); ok {
			open = append(open, btn)
		}
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("production.studio_none", nil)
	}
	kb.Grid(2, open...)
	start := ""
	switch {
	case v.CanDesign && len(v.Kinds) > 0:
		start = c.T("production.studio_new", nil)
		var kinds []presenter.Button
		for _, k := range v.Kinds {
			if btn, ok := keyboards.Button(c.T("production.button.new_design", map[string]any{"item": c.ItemName(k)}),
				AddrDesignNew, v.Ref.Code, k.Code); ok {
				kinds = append(kinds, btn)
			}
		}
		kb.Grid(3, kinds...)
	case len(v.Kinds) == 0 && len(v.Next) == 0:
		start = c.T("production.studio_no_kinds", nil)
	case len(v.Kinds) == 0:
		start = c.T("production.studio_none_ready", nil)
	default:
		start = c.T("production.studio_at_max", map[string]any{"max": FormatNumber(c, int64(v.Max))})
	}
	next := ""
	if len(v.Next) > 0 {
		lines := []string{c.T("production.studio_next", nil)}
		for _, k := range v.Next {
			lines = append(lines, c.T("production.studio_next_line", map[string]any{"item": c.ItemName(k.Item),
				"hint": c.unlockHint(k.Steps)}))
		}
		next = body(lines...)
		if v.CanResearch {
			kb.Add(c.T("production.button.lab", nil), AddrLab, v.Ref.Code)
		}
	}
	later := ""
	if v.Hidden {
		later = c.T("production.studio_later", nil)
	}
	c.productionNav(kb, []string{AddrWarehouse, v.Ref.Code}, AddrStudio, v.Ref.Code)
	return c.respond(paragraphs(head, list, start, next, later), kb.Build())
}

// SlotLine is one slot of a design as the editor shows it.
type SlotLine struct {
	Slot     string
	Optional bool
	// Min and Max are the quantity it takes; Unit its display unit, which
	// has a locale entry (production.unit.<unit>) when it has one at all.
	Min, Max int64
	Unit     string
	// Component and Qty fill it; an empty component is an empty slot.
	Component Named
	Qty       int64
}

// Candidate is a component that fits the slot being filled.
type Candidate struct {
	Component Named
	Price     int64
	// Locked means the company can neither make it nor design with it:
	// the technology it requires is neither owned nor licensed.
	Locked bool
	// Quality is its own quality.
	Quality int
}

// AttributeLine is one computed attribute of a design.
type AttributeLine struct {
	Name  string
	Value int64
	// Observable attributes are what a buyer sees on the market.
	Observable bool
}

// DesignView is one design: the editor while it is a draft, the record once
// it is final.
type DesignView struct {
	Ref    CompanyRef
	No     int64
	Name   string
	Item   Named
	Status string
	Origin string
	// Source names the design a copy was taken from.
	Source string
	Slots  []SlotLine
	// Choosing is the slot whose candidates are shown.
	Choosing   string
	Candidates []Candidate
	Attributes []AttributeLine
	// CostFloor is what one unit's inputs cost at reference prices.
	CostFloor      int64
	QualityLossBPS int64
	OverheadBPS    int64
	// Complete is a draft whose required slots are all filled.
	Complete bool
	// Locked are the technologies a draft's components need and the
	// company lacks.
	Locked []Named
	// Version is this design's generation within its lineage, 1 for the
	// first ever authored. PrevAttributes, when non-nil, are the version
	// before's computed attributes, for the ▲▼ delta a revision's page
	// shows next to each number.
	Version        int64
	PrevAttributes map[string]int64
	Notice         string
}

// Design renders a design.
func Design(c Context, v DesignView) *presenter.Response {
	no := strconv.FormatInt(v.No, 10)
	name := v.Name
	if name == "" {
		name = c.T("production.design_unnamed", map[string]any{"no": FormatNumber(c, v.No)})
	}
	head := body(c.T("production.design_title", map[string]any{"name": name, "item": c.ItemName(v.Item)}),
		c.T("production.design_state."+v.Status, nil))
	if v.Version > 1 {
		head = body(head, c.T("production.design_version", map[string]any{"version": FormatNumber(c, v.Version)}))
	}
	if v.Origin == DesignReverseEngineered {
		head = body(head, c.T("production.design_copy", map[string]any{"source": v.Source,
			"loss": PercentFromBPS(c, int(v.QualityLossBPS)), "overhead": PercentFromBPS(c, int(v.OverheadBPS))}))
	}
	draft := v.Status == DesignDraft
	kb := keyboards.New()
	var slots []string
	var slotButtons []presenter.Button
	for _, s := range v.Slots {
		args := map[string]any{"slot": c.SlotName(s.Slot)}
		key := "production.slot_empty"
		if s.Optional {
			key = "production.slot_optional"
		}
		if s.Component.Code != "" {
			key = "production.slot_filled"
			args["component"] = c.ComponentName(s.Component)
			args["qty"] = c.quantity(s.Qty, s.Unit)
		}
		slots = append(slots, c.T(key, args))
		if draft {
			if btn, ok := keyboards.Button(c.T("production.button.slot", map[string]any{"slot": c.SlotName(s.Slot)}),
				AddrDesign, no, s.Slot); ok {
				slotButtons = append(slotButtons, btn)
			}
		}
	}
	choosing := ""
	if draft && v.Choosing != "" {
		var slot SlotLine
		for _, s := range v.Slots {
			if s.Slot == v.Choosing {
				slot = s
			}
		}
		lines := []string{c.T("production.choose_title", map[string]any{"slot": c.SlotName(slot.Slot)})}
		if slot.Min != slot.Max {
			lines = append(lines, c.T("production.choose_range", map[string]any{"min": c.quantity(slot.Min, slot.Unit),
				"max": c.quantity(slot.Max, slot.Unit)}))
		}
		var cands []presenter.Button
		for _, cd := range v.Candidates {
			key := "production.candidate"
			if cd.Locked {
				key = "production.candidate_locked"
			}
			lines = append(lines, c.T(key, map[string]any{"component": c.ComponentName(cd.Component),
				"price": FormatMoney(c, cd.Price), "quality": FormatNumber(c, int64(cd.Quality))}))
			if cd.Locked {
				continue
			}
			if btn, ok := keyboards.Button(c.T("production.button.candidate", map[string]any{"component": c.ComponentName(cd.Component)}),
				AddrDesignFill, no, slot.Slot, cd.Component.Code); ok {
				cands = append(cands, btn)
			}
		}
		if slot.Optional && slot.Component.Code != "" {
			if btn, ok := keyboards.Button(c.T("production.button.slot_clear", nil), AddrDesignFill, no, slot.Slot, SlotEmpty); ok {
				cands = append(cands, btn)
			}
		}
		kb.Grid(2, cands...)
		if slot.Min != slot.Max && slot.Component.Code != "" {
			kb.Row(mustAsk(c.T("production.button.slot_qty", map[string]any{"slot": c.SlotName(slot.Slot)}),
				commandDesignQty, no, slot.Slot)...)
		}
		choosing = body(lines...)
	} else if draft {
		kb.Grid(2, slotButtons...)
	}
	var attrs []string
	for _, a := range v.Attributes {
		key := "production.attribute"
		if a.Observable {
			key = "production.attribute_public"
		}
		args := map[string]any{"name": c.AttributeName(a.Name), "value": FormatNumber(c, a.Value)}
		if prev, ok := v.PrevAttributes[a.Name]; ok && prev != a.Value {
			arrow := "▲"
			diff := a.Value - prev
			if diff < 0 {
				arrow, diff = "▼", -diff
			}
			key += "_delta"
			args["delta"] = arrow + FormatNumber(c, diff)
		}
		attrs = append(attrs, c.T(key, args))
	}
	numbers := body(append(attrs, c.T("production.cost_floor", map[string]any{"cost": FormatMoney(c, v.CostFloor)}))...)
	status := ""
	switch {
	case draft && len(v.Locked) > 0:
		names := make([]string, 0, len(v.Locked))
		for _, t := range v.Locked {
			names = append(names, c.TechName(t))
		}
		status = c.T("production.design_locked", map[string]any{"techs": c.list(names)})
	case draft && !v.Complete:
		status = c.T("production.design_incomplete", nil)
	case draft:
		status = c.T("production.design_ready", nil)
	}
	if draft && v.Choosing == "" {
		var row []presenter.Button
		if btn, ok := askButton(c.T("production.button.name", nil), commandDesignName, no); ok {
			row = append(row, btn)
		}
		if v.Complete && v.Name != "" && len(v.Locked) == 0 {
			if btn, ok := keyboards.Button(c.T("production.button.finalize", nil), AddrDesignFinal, no); ok {
				row = append(row, btn)
			}
		}
		kb.Row(row...)
	}
	if v.Status == DesignFinal {
		kb.Add(c.T("production.button.produce_design", nil), AddrProduce, v.Ref.Code, DesignTarget(v.No))
		kb.Add(c.T("production.button.kit", nil), AddrProduceKit, v.Ref.Code, DesignTarget(v.No))
		if btn, ok := keyboards.Button(c.T("production.button.revise", nil), AddrDesignRevise, no); ok {
			kb.Row(btn)
		}
		var improve []presenter.Button
		for _, a := range v.Attributes {
			if btn, ok := keyboards.Button(c.T("production.button.improve", map[string]any{"name": c.AttributeName(a.Name)}),
				AddrImprovementStart, no, a.Name); ok {
				improve = append(improve, btn)
			}
		}
		kb.Grid(2, improve...)
		if btn, ok := keyboards.Button(c.T("production.button.retire", nil), AddrDesignRetire, no, ProductionConfirm); ok {
			kb.Row(btn)
		}
	}
	back := []string{AddrStudio, v.Ref.Code}
	refresh := []string{AddrDesign, no}
	if v.Choosing != "" {
		back = []string{AddrDesign, no}
		refresh = append(refresh, v.Choosing)
	}
	c.productionNav(kb, back, refresh...)
	return c.respond(paragraphs(v.Notice, head, body(slots...), choosing, numbers, status), kb.Build())
}

// quantity renders an amount in its unit, when the unit has a name.
func (c Context) quantity(qty int64, unit string) string {
	if unit == "" {
		return FormatNumber(c, qty)
	}
	return c.T("production.unit."+unit, map[string]any{"qty": FormatNumber(c, qty)})
}

// mustAsk is an ask button as a row, empty when it cannot be built.
func mustAsk(label, command string, args ...string) []presenter.Button {
	if btn, ok := askButton(label, command, args...); ok {
		return []presenter.Button{btn}
	}
	return nil
}
