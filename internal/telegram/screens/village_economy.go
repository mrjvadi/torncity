package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The village economy (docs/adr/0033-village-first-progression-and-village-
// currencies.md sections 4.1 and 5, phase E1): the stock and Support's market
// where a village buys the materials it cannot yet make, the workplaces where
// residents work shifts, and the refusal that says exactly what is missing and
// where to get it - the flow is always "get the prerequisite first", and the
// game shows the path to it.

// Addresses.
const (
	AddrMaterials    = "settlement:materials"
	AddrMaterialsBuy = "settlement:materials.buy"
	AddrWork         = "settlement:work"
)

// Structured screens (clients).
const (
	ScreenVillageMaterials   = "village_materials"
	ScreenVillageBuyConfirm  = "village_materials_buy_confirm"
	ScreenVillageWork        = "village_work"
	ScreenVillageWorkStarted = "village_work_started"
)

// Village refusal kinds of the loop.
const (
	// VillageStorageFull: the stock has no room for what was asked (a granary
	// adds room).
	VillageStorageFull = "storage_full"
	// VillageAlreadyWorking: the resident already works a shift.
	VillageAlreadyWorking = "already_working"
	// VillageWorkplaceFull: every place at the workplace is taken.
	VillageWorkplaceFull = "workplace_full"
	// VillageNotWorkplace: the building cannot be worked in (yet).
	VillageNotWorkplace = "not_workplace"
)

// What a refusal's need is.
const (
	NeedMaterial  = "material"
	NeedKnowledge = "knowledge"
	NeedBuilding  = "building"
)

// What the refused command was about, for the refusal's title.
const (
	NeedsForBuild    = "build"
	NeedsForResearch = "research"
	NeedsForWork     = "work"
)

// VillageMaker is a building that makes a material: where to get it.
type VillageMaker struct {
	Building Named
	// Built reports that the village already has one standing.
	Built bool
}

// VillageNeed is one thing a refused command is missing and where it comes
// from (ADR 0033 section 5): named exactly, one hop only.
type VillageNeed struct {
	Kind string
	// Item is the material or the knowledge; Options are the alternatives when
	// any of several would do (the knowledge that provides a capability, the
	// buildings of a role).
	Item    Named
	Options []Named
	// Have and Need are the stock and the quantity of a material.
	Have, Need int64
	// Makers are the workplaces that make the material; Price is what Support
	// asks per unit, zero when the village cannot buy it.
	Makers []VillageMaker
	Price  int64
}

// villageNeeds renders a refusal that carries needs.
func renderVillageNeeds(c Context, v VillageRefusalView) *presenter.Response {
	var subject string
	switch v.Action {
	case NeedsForResearch:
		subject = c.SettlementKnowledgeName(v.Subject)
	default:
		subject = c.SettlementBuildingName(v.Subject)
	}
	action := v.Action
	if action != NeedsForResearch && action != NeedsForWork {
		action = NeedsForBuild
	}

	var lines []string
	kb := keyboards.New()
	seen := map[string]bool{}
	add := func(label, addr string, args ...string) {
		key := addr
		for _, a := range args {
			key += ":" + a
		}
		if seen[key] {
			return
		}
		seen[key] = true
		if btn, ok := keyboards.Button(label, append([]string{addr}, args...)...); ok {
			kb.Row(btn)
		}
	}
	for _, n := range v.Needs {
		switch n.Kind {
		case NeedMaterial:
			var parts []string
			for _, m := range n.Makers {
				key := "village.needs.source.made_unbuilt"
				if m.Built {
					key = "village.needs.source.made_built"
				}
				parts = append(parts, c.T(key, map[string]any{"building": c.SettlementBuildingName(m.Building)}))
			}
			if n.Price > 0 {
				parts = append(parts, c.T("village.needs.source.buy", map[string]any{"price": FormatMoney(c, n.Price)}))
			}
			lines = append(lines, c.T("village.needs.material", map[string]any{
				"need": n.Need, "name": c.ComponentName(n.Item), "have": n.Have,
				"source": joinOr(c, parts),
			}))
			for _, m := range n.Makers {
				if m.Built {
					add(c.T("village.needs.button.work", map[string]any{"building": c.SettlementBuildingName(m.Building)}), AddrWork)
				} else {
					add(c.T("village.needs.button.build", map[string]any{"building": c.SettlementBuildingName(m.Building)}), AddrBuildLots, m.Building.Code)
				}
			}
			if n.Price > 0 {
				short := n.Need - n.Have
				if short < 1 {
					short = 1
				}
				add(c.T("village.needs.button.buy", map[string]any{"quantity": short, "name": c.ComponentName(n.Item)}),
					AddrMaterialsBuy, n.Item.Code, strconv.FormatInt(short, 10))
			}
		case NeedKnowledge:
			if len(n.Options) > 0 {
				names := make([]string, 0, len(n.Options))
				for _, o := range n.Options {
					names = append(names, c.SettlementKnowledgeName(o))
				}
				lines = append(lines, c.T("village.needs.knowledge_any", map[string]any{"names": c.list(names)}))
			} else {
				lines = append(lines, c.T("village.needs.knowledge", map[string]any{"name": c.SettlementKnowledgeName(n.Item)}))
			}
			add(c.T("village.needs.button.knowledge", nil), AddrKnowledgeList)
		case NeedBuilding:
			names := make([]string, 0, len(n.Options))
			for _, o := range n.Options {
				names = append(names, c.SettlementBuildingName(o))
			}
			lines = append(lines, c.T("village.needs.building", map[string]any{"names": c.list(names)}))
			for i, o := range n.Options {
				if i == 3 {
					break
				}
				add(c.T("village.needs.button.build", map[string]any{"building": c.SettlementBuildingName(o)}), AddrBuildLots, o.Code)
			}
		}
	}
	back := v.Back
	if back == "" {
		back = AddrVillageOverview
	}
	add(c.T("village.needs.button.materials", nil), AddrMaterials)
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	text := paragraphs(c.T("village.needs.title."+action, map[string]any{"subject": subject}), body(lines...))
	return c.respond(text, kb.Build())
}

// joinOr joins alternatives with the language's own "or".
func joinOr(c Context, parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += c.T("village.needs.or", nil)
		}
		out += p
	}
	return out
}

// ---------------------------------------------------------------------
// The stock and Support's market
// ---------------------------------------------------------------------

// MaterialStockLine is one good the village holds.
type MaterialStockLine struct {
	Item Named
	Qty  int64
}

// MaterialMarketLine is one material Support sells the village.
type MaterialMarketLine struct {
	Item  Named
	Price int64
}

// MaterialBought is what a purchase that was just made came to.
type MaterialBought struct {
	Item  Named
	Qty   int64
	Total int64
}

// MaterialsView is the village stock and Support's market.
type MaterialsView struct {
	Village  string
	Treasury int64
	Stock    []MaterialStockLine
	// Used and Capacity are the units held and the room there is (the base
	// capacity plus every standing building's storage).
	Used, Capacity int64
	Market         []MaterialMarketLine
	// CanBuy reports that the viewer may spend the treasury (the village head);
	// Presets are the quantities the buy buttons offer.
	CanBuy  bool
	Presets []int64
	// Bought is set on the screen shown right after a purchase.
	Bought *MaterialBought `json:"bought,omitempty"`
}

// VillageStock renders the stock and the market.
func VillageStock(c Context, v MaterialsView) *presenter.Response {
	return c.withView(renderVillageMaterials(c, v), ScreenVillageMaterials, v)
}

func renderVillageMaterials(c Context, v MaterialsView) *presenter.Response {
	var stock []string
	for _, l := range v.Stock {
		stock = append(stock, c.T("village.materials.stock_line", map[string]any{"name": c.ComponentName(l.Item), "qty": l.Qty}))
	}
	stockText := body(stock...)
	if stockText == "" {
		stockText = c.T("village.materials.stock_empty", nil)
	}
	var market []string
	for _, l := range v.Market {
		market = append(market, c.T("village.materials.market_line", map[string]any{
			"name": c.ComponentName(l.Item), "price": FormatMoney(c, l.Price),
		}))
	}
	var bought string
	if v.Bought != nil {
		bought = c.T("village.materials.bought", map[string]any{
			"qty": v.Bought.Qty, "name": c.ComponentName(v.Bought.Item), "total": FormatMoney(c, v.Bought.Total),
		})
	}
	text := paragraphs(
		bought,
		c.T("village.materials.title", map[string]any{"village": v.Village}),
		c.T("village.materials.treasury", map[string]any{"amount": FormatMoney(c, v.Treasury)}),
		c.T("village.materials.capacity", map[string]any{"used": v.Used, "capacity": v.Capacity}),
		c.T("village.materials.stock_title", nil)+"\n"+stockText,
		c.T("village.materials.market_title", nil)+"\n"+body(market...),
		c.T("village.materials.hint", nil),
	)

	kb := keyboards.New()
	if v.CanBuy {
		for _, l := range v.Market {
			var row []presenter.Button
			for _, q := range v.Presets {
				label := c.T("village.materials.button.buy", map[string]any{"name": c.ComponentName(l.Item), "quantity": q})
				if b, ok := keyboards.Button(label, AddrMaterialsBuy, l.Item.Code, strconv.FormatInt(q, 10)); ok {
					row = append(row, b)
				}
			}
			kb.Row(row...)
		}
	}
	kb.Row(villageButtons(c, "village.button.work", AddrWork, "village.button.build", AddrBuildMenu)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrMaterials}))
	return c.respond(text, kb.Build())
}

// MaterialBuyView is the confirm before a purchase.
type MaterialBuyView struct {
	Village  string
	Item     Named
	Qty      int64
	Unit     int64
	Total    int64
	Treasury int64
	// Free is the room left in the stock.
	Free int64
}

// VillageMaterialBuyConfirm renders the purchase confirm.
func VillageMaterialBuyConfirm(c Context, v MaterialBuyView) *presenter.Response {
	return c.withView(renderMaterialBuyConfirm(c, v), ScreenVillageBuyConfirm, v)
}

// MaterialsConfirm is the second press's argument.
const MaterialsConfirm = "confirm"

func renderMaterialBuyConfirm(c Context, v MaterialBuyView) *presenter.Response {
	args := map[string]any{
		"qty": v.Qty, "name": c.ComponentName(v.Item), "unit": FormatMoney(c, v.Unit), "total": FormatMoney(c, v.Total),
		"treasury": FormatMoney(c, v.Treasury), "village": v.Village,
	}
	text := paragraphs(c.T("village.materials.ask_title", args), c.T("village.materials.ask_body", args))
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T("village.materials.button.yes", args), AddrMaterialsBuy, v.Item.Code,
		strconv.FormatInt(v.Qty, 10), MaterialsConfirm); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMaterials}))
	return c.respond(text, kb.Build())
}

// ---------------------------------------------------------------------
// Workplaces
// ---------------------------------------------------------------------

// WorkplaceLine is one standing building a resident can work in.
type WorkplaceLine struct {
	ID       string
	Building Named
	// Produces and Consumes are what one shift makes and uses.
	Produces, Consumes []MaterialLine
	Wage               int64
	Shift              time.Duration
	// Workers is how many shifts may run at once and Busy how many run now.
	Workers, Busy int
	// Ready reports that the stock holds the inputs of one shift.
	Ready bool
}

// WorkShiftLine is a shift in progress.
type WorkShiftLine struct {
	Building Named
	FinishAt time.Time
	Left     time.Duration
	Wage     int64
	Produces []MaterialLine
}

// WorkView is the workplaces of the village and the viewer's own shift.
type WorkView struct {
	Village  string
	Resident bool
	Places   []WorkplaceLine
	Mine     *WorkShiftLine
	// Suggest are the workplaces the village could build now, when it has none.
	Suggest []Named
	// Started is set on the screen shown right after a shift began.
	Started bool
	// Used and Capacity are the stock's units and room.
	Used, Capacity int64
}

// VillageWork renders the workplaces.
func VillageWork(c Context, v WorkView) *presenter.Response {
	screen := ScreenVillageWork
	if v.Started {
		screen = ScreenVillageWorkStarted
	}
	return c.withView(renderVillageWork(c, v), screen, v)
}

func materialsText(c Context, lines []MaterialLine) string {
	var parts []string
	for _, l := range lines {
		parts = append(parts, c.T("village.work.material", map[string]any{"qty": l.Quantity, "name": c.ComponentName(l.Component)}))
	}
	return c.list(parts)
}

func renderVillageWork(c Context, v WorkView) *presenter.Response {
	var mine string
	if v.Mine != nil {
		mine = c.T("village.work.mine", map[string]any{
			"building": c.SettlementBuildingName(v.Mine.Building), "time": FormatClock(c, v.Mine.FinishAt),
			"duration": FormatDuration(c, v.Mine.Left), "produces": materialsText(c, v.Mine.Produces),
			"wage": FormatMoney(c, v.Mine.Wage),
		})
	}
	var lines []string
	kb := keyboards.New()
	for _, p := range v.Places {
		key := "village.work.place"
		if len(p.Consumes) > 0 {
			key = "village.work.place_inputs"
		}
		lines = append(lines, c.T(key, map[string]any{
			"building": c.SettlementBuildingName(p.Building), "produces": materialsText(c, p.Produces),
			"consumes": materialsText(c, p.Consumes), "wage": FormatMoney(c, p.Wage),
			"shift": FormatDuration(c, p.Shift), "busy": p.Busy, "workers": p.Workers,
		}))
		if v.Resident && v.Mine == nil {
			if b, ok := keyboards.Button(c.T("village.work.button.start", map[string]any{"building": c.SettlementBuildingName(p.Building)}),
				AddrWork, p.ID); ok {
				kb.Row(b)
			}
		}
	}
	placesText := body(lines...)
	if placesText == "" {
		var names []string
		for _, n := range v.Suggest {
			names = append(names, c.SettlementBuildingName(n))
		}
		placesText = c.T("village.work.none", map[string]any{"names": c.list(names)})
		for i, n := range v.Suggest {
			if i == 3 {
				break
			}
			if b, ok := keyboards.Button(c.T("village.needs.button.build", map[string]any{"building": c.SettlementBuildingName(n)}),
				AddrBuildLots, n.Code); ok {
				kb.Row(b)
			}
		}
	}
	var notResident string
	if !v.Resident {
		notResident = c.T("village.work.not_resident", nil)
	}
	text := paragraphs(
		c.T("village.work.title", map[string]any{"village": v.Village}),
		mine, notResident, placesText,
		c.T("village.work.capacity", map[string]any{"used": v.Used, "capacity": v.Capacity}),
		c.T("village.work.hint", nil),
	)
	kb.Row(villageButtons(c, "village.button.materials", AddrMaterials, "village.button.build", AddrBuildMenu)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrWork}))
	return c.respond(text, kb.Build())
}
