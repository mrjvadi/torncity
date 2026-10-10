package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// This file holds the village-level screens
// (docs/adr/0031-knowledge-and-village-progression.md): the settlement
// overview, its knowledge list (research/buy), its build menu and its
// construction progress. Like group founding (settlements.go), these are
// GROUP screens, never private (the same rule: a settlement's growth is a
// decision the whole group should see argued and decided, docs/repo-notes
// group-first) — the settlement belongs to the group that founded it, not
// to whichever member happens to press a button.
//
// Percent, coverage and progress values below are always ALREADY COMPUTED
// by the use case that builds the view, the same convention SkillLine.Percent
// already sets (skills.go): a screen lays out facts, it never derives one.

// SettlementKnowledgeName is a settlement knowledge item's display name:
// the catalogue key "settlement_knowledge.<code>" if a translator has
// written one, else the item's own authored name
// (configs/content/settlement_knowledge.yml's name: field) — the same
// catalogue-first, authored-name-fallback rule TechName already applies to
// a technology, so the 40-item v1 catalogue reads in polished Persian from
// day one without first requiring 40 hand-written catalogue lines.
func (c Context) SettlementKnowledgeName(n Named) string {
	return c.named("settlement_knowledge."+n.Code, n.Name)
}

// SettlementBuildingName is a building type's display name, the identical
// rule for configs/content/settlement_buildings.yml. Distinct from
// BuildingName (settlements.go), which only ever names the two founding-kit
// buildings and has no authored name to fall back to.
func (c Context) SettlementBuildingName(n Named) string {
	return c.named("settlement_building."+n.Code, n.Name)
}

// ---------------------------------------------------------------------
// Refusals (K2/W5 command handlers)
// ---------------------------------------------------------------------

// VillageRefusal renders a refused village command.
func VillageRefusal(c Context, v VillageRefusalView) *presenter.Response {
	return c.withView(renderVillageRefusal(c, v), ScreenVillageRefusal, v)
}

func renderVillageRefusal(c Context, v VillageRefusalView) *presenter.Response {
	if v.Kind == VillagePersonal {
		back := v.Back.Address()
		if back == "" {
			back = AddrVillageOverview
		}
		kb := keyboards.New()
		kb.Nav(c.nav(keyboards.Nav{BackData: back}))
		return c.respond(c.T("village.refusal.personal", map[string]any{"needs": personalList(c, v.Personal)}), kb.Build())
	}
	if len(v.Needs) > 0 {
		return renderVillageNeeds(c, v)
	}
	kind := v.Kind
	switch kind {
	case VillageNoSettlement, VillageNotOfficeHolder, VillageInsufficient, VillageBusy, VillageAlreadyOwned,
		VillageNotAvailable, VillageTerrain, VillagePrerequisite, VillageLiteracy, VillageNotFound,
		VillageOccupied, VillageUnbuildable, VillageOutOfBounds, VillageConcurrentCap, VillageNotDemolishable, VillageMaterials,
		VillageNotCancellable, VillageAlreadyResident, VillageNotResident, VillageResidenceWait, VillageHoldsOffice, VillageNoHome,
		VillageDonateRange, VillageDonateNoCash, VillageBatch, VillageNoRoad, VillagePromotionTop,
		VillageStorageFull, VillageNotEnough, VillageAlreadyWorking, VillageWorkplaceFull, VillageNotWorkplace,
		LaborNoJob, LaborNotHere, LaborFullyStaffed, LaborBudgetSpent, LaborNotEmployer, LaborNoNPC, LaborWageTooLow,
		LaborEmployerBroke, LaborNoSite, village.LaborNeedsRepair, village.LaborNoFood, village.VillageDeskEmpty, village.VillageDeskNoSUP, village.VillageDeskFunds, village.VillageDeskMoved, village.ReserveFunds, village.ReserveNoUnits, village.ReserveNoExcess, village.ReserveBudget, village.ReserveWinding, village.ReserveNotWind, village.ReserveNothing, village.ReserveInvalid, village.ReserveNotFound, village.LotNotYours, village.LotNotBuilt, village.LotBusy, village.LotNoFunction, village.LotNoModule, village.LotNoArea, village.LotSlotFull, village.LotNotBuilable, village.LotStoreys, village.LotNothing, village.LotTemplates, village.LotNoTemplate, village.LotInvalid, village.LotKeepOne, village.LotNoKeeper, village.LotKeeperNone, village.LotKeeperTerms, village.VillageRoadReserved, village.VillageReserved, village.ResearchNoPost, village.ResearchPostHeld, village.ResearchNoPostHeld, village.ResearchPactOpen, village.ResearchPactSelf, village.ResearchPactNotFound, village.ResearchNoSlot:
	default:
		if isRoadRefusal(kind) {
			break
		}
		if isCitizenRefusal(kind) {
			return renderCitizenRefusal(c, v)
		}
		if strings.HasPrefix(kind, "charter_") {
			break
		}
		kind = VillageNotFound
	}
	back := v.Back.Address()
	if back == "" {
		back = AddrVillageOverview
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("village.refusal."+kind, map[string]any{
		"time": FormatDuration(c, v.Remaining), "min": FormatMoney(c, v.Min), "max": FormatMoney(c, v.Max),
		"lots": batchFailureList(c, v.Lots), "missing": FormatNumber(c, v.Missing),
	}), kb.Build())
}

// ---------------------------------------------------------------------
// Village overview
// ---------------------------------------------------------------------

// VillageOverview renders a settlement's own status screen.
func VillageOverview(c Context, v VillageOverviewView) *presenter.Response {
	return c.withView(renderVillageOverview(c, v), ScreenVillageOverview, v)
}

func renderVillageOverview(c Context, v VillageOverviewView) *presenter.Response {
	head := c.T("village.title."+tierOr(v.Tier), map[string]any{"name": v.Name})
	population := c.T("village.population", map[string]any{
		"population": FormatNumber(c, v.Population), "cap": FormatNumber(c, v.PopulationCap),
	})
	treasury := c.T("village.treasury", map[string]any{"amount": FormatMoney(c, v.Treasury)})
	coverage := body(
		c.T("village.coverage.food", map[string]any{"percent": v.FoodPercent}),
		c.T("village.coverage.job", map[string]any{"percent": v.JobPercent}),
		c.T("village.coverage.service", map[string]any{"percent": v.ServicePercent}),
		c.T("village.coverage.happiness", map[string]any{"percent": v.HappinessPercent}),
		c.T("village.coverage.security", map[string]any{"percent": v.SecurityPercent}),
		c.T("village.coverage.literacy", map[string]any{"percent": v.LiteracyPercent}),
	)

	var buildingLines []string
	for _, b := range v.Buildings {
		buildingLines = append(buildingLines, c.T("village.building_line", map[string]any{
			"building": c.SettlementBuildingName(b.Building),
		}))
	}
	buildings := body(buildingLines...)
	if buildings == "" {
		buildings = c.T("village.no_buildings", nil)
	}

	kb := keyboards.New()
	blocks := []string{head, population, treasury, coverage, serviceNote(c, v), buildings}
	if v.Promotion != nil {
		blocks = append(blocks, promotionBlock(c, *v.Promotion))
	}
	if c.Shared && !v.Resident {
		kb.Add(c.T("village.button.join", nil), AddrVillageJoin)
	}
	if v.Resident {
		// What a normal resident can do (docs/adr/0033 section 4.4): the
		// village is not only its head's. Work is the village economy's own
		// workplaces (settlement.work).
		blocks = append(blocks, c.T("citizen.hub.hint", nil))
		kb.Row(villageButtons(c, "citizen.button.buy_land", AddrLand, "citizen.button.build_house", AddrPrivateMenu)...)
		kb.Row(villageButtons(c, "village.button.work", AddrWork, "village.button.donate", AddrVillageDonate)...)
		if c.Shared {
			kb.Row(villageButtons(c, "citizen.button.mine", AddrMine, "village.button.who", AddrSettlementWho)...)
		} else {
			kb.Add(c.T("citizen.button.mine", nil), AddrMine)
		}
	}
	if c.Shared {
		kb.Row(villageButtons(c, "village.button.knowledge", AddrKnowledgeList, "village.button.progress", AddrConstructionProgress)...)
		kb.Add(c.T("village.button.materials", nil), AddrMaterials)
		if v.IsHead {
			kb.Row(villageButtons(c, "village.button.build", AddrBuildMenu, "citizen.button.terms", AddrVillageTerms)...)
		}
		if !v.Resident {
			kb.Add(c.T("village.button.who", nil), AddrSettlementWho)
		}
		if v.Promotion != nil {
			promotionButton(c, kb, *v.Promotion)
		}
		if v.Development {
			developmentButton(c, kb)
		}
		kb.Add(c.T("village.charter.button", nil), AddrVillageCharter)
		if v.Resident {
			kb.Add(c.T("village.button.leave", nil), AddrVillageLeave)
		}
	} else {
		// Private: the village is home; the civic side is run in its group.
		blocks = append(blocks, c.T("village.private_hint", nil))
	}
	if v.Support != nil {
		args := map[string]any{"city": c.CityName(v.Support.Code, v.Support.Name)}
		blocks = append(blocks, c.T("village.support.title", args))
		var row []presenter.Button
		for _, code := range v.Support.Services {
			if b, ok := keyboards.Button(c.T("village.support.service."+code, args), AddrTravelOptions, v.Support.Code); ok {
				row = append(row, b)
			}
			if len(row) == 2 {
				kb.Row(row...)
				row = nil
			}
		}
		if len(row) > 0 {
			kb.Row(row...)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrVillageOverview}))

	return c.respond(paragraphs(blocks...), kb.Build())
}

// villageButtons builds a row from two label/address pairs.
func villageButtons(c Context, labelA, addrA, labelB, addrB string) []presenter.Button {
	var row []presenter.Button
	if b, ok := keyboards.Button(c.T(labelA, nil), addrA); ok {
		row = append(row, b)
	}
	if b, ok := keyboards.Button(c.T(labelB, nil), addrB); ok {
		row = append(row, b)
	}
	return row
}

func tierOr(tier string) string {
	switch tier {
	case "town", "city":
		return tier
	default:
		return "village"
	}
}

// ---------------------------------------------------------------------
// Knowledge list: research and buy (ADR 0031 sections 4.1, 4.2)
// ---------------------------------------------------------------------

// KnowledgeList renders the settlement's knowledge list.
func KnowledgeList(c Context, v KnowledgeListView) *presenter.Response {
	return c.withView(renderKnowledgeList(c, v), ScreenKnowledgeList, v)
}

func renderKnowledgeList(c Context, v KnowledgeListView) *presenter.Response {
	head := body(
		c.T("knowledge.title", map[string]any{"name": v.Name}),
		c.T("knowledge.treasury", map[string]any{"amount": FormatMoney(c, v.Treasury)}),
		c.T("knowledge.literacy", map[string]any{"percent": v.LiteracyPercent}),
	)
	var runs []string
	for _, r := range v.Projects {
		runs = append(runs, c.T("knowledge.running", map[string]any{
			"knowledge": c.SettlementKnowledgeName(r.Knowledge), "time": FormatClock(c, r.FinishAt), "duration": FormatDuration(c, r.Left),
		}))
	}
	if len(runs) == 0 && v.Running != nil {
		r := v.Running
		runs = append(runs, c.T("knowledge.running", map[string]any{
			"knowledge": c.SettlementKnowledgeName(r.Knowledge), "time": FormatClock(c, r.FinishAt), "duration": FormatDuration(c, r.Left),
		}))
	}
	running := body(runs...)
	if v.Capacity > 0 {
		running = body(c.T("knowledge.capacity", map[string]any{"running": FormatNumber(c, int64(len(runs))), "capacity": FormatNumber(c, int64(v.Capacity))}), running)
	}

	var lines []string
	var buttons []presenter.Button
	for _, l := range v.Lines {
		args := map[string]any{
			"knowledge": c.SettlementKnowledgeName(l.Knowledge),
			"cost":      FormatMoney(c, l.ResearchCost),
			"time":      FormatDuration(c, l.ResearchTime),
			"buy_price": FormatMoney(c, l.BuyPrice),
		}
		key := "knowledge.line." + knowledgeStateOr(l.State)
		if l.State == KnowledgeLocked {
			if !l.TerrainOK {
				key = "knowledge.line.locked_terrain"
			} else if len(l.Missing) > 0 {
				names := make([]string, 0, len(l.Missing))
				for _, m := range l.Missing {
					names = append(names, c.SettlementKnowledgeName(m))
				}
				args["missing"] = c.list(names)
			}
		}
		line := c.T(key, args)
		if l.State == KnowledgeAvailable {
			if notes := researchNotes(c, l.SpeedBPS, l.AheadBPS, l.DiscountBPS, l.ShareBPS); notes != "" {
				line += " (" + notes + ")"
			}
		}
		lines = append(lines, line)

		if l.State == KnowledgeAvailable {
			if btn, ok := keyboards.Button(c.T("knowledge.button.research", map[string]any{"knowledge": c.SettlementKnowledgeName(l.Knowledge)}),
				AddrKnowledgeResearch, l.Knowledge.Code); ok {
				buttons = append(buttons, btn)
			}
			if l.BuyPrice > 0 {
				if btn, ok := keyboards.Button(c.T("knowledge.button.buy", map[string]any{"knowledge": c.SettlementKnowledgeName(l.Knowledge)}),
					AddrKnowledgeBuy, l.Knowledge.Code); ok {
					buttons = append(buttons, btn)
				}
			}
		}
	}
	list := body(lines...)
	if list == "" {
		list = c.T("knowledge.empty", nil)
	}
	later := ""
	if v.Hidden > 0 {
		later = c.T("knowledge.hidden", map[string]any{"count": FormatNumber(c, int64(v.Hidden))})
	}

	kb := keyboards.New()
	kb.Grid(2, buttons...)
	if desk, ok := keyboards.Button(c.T("knowledge.button.desk", nil), AddrResearchDesk); ok {
		kb.Row(desk)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrKnowledgeList}))

	return c.respond(paragraphs(head, running, list, later), kb.Build())
}

func knowledgeStateOr(state string) string {
	switch state {
	case KnowledgeHeld, KnowledgeResearching, KnowledgeAvailable, KnowledgeLocked:
		return state
	default:
		return KnowledgeLocked
	}
}

// ---------------------------------------------------------------------
// Build menu (ADR 0028 section 6, ADR 0031 section 3.2)
// ---------------------------------------------------------------------

// BuildMenu renders the settlement's build menu.
func BuildMenu(c Context, v BuildMenuView) *presenter.Response {
	return c.withView(renderBuildMenu(c, v), ScreenBuildMenu, v)
}

func renderBuildMenu(c Context, v BuildMenuView) *presenter.Response {
	head := body(
		c.T("build.title", map[string]any{"name": v.Name}),
		c.T("build.treasury", map[string]any{"amount": FormatMoney(c, v.Treasury)}),
		c.T("build.queue", map[string]any{"running": v.RunningBuilds, "cap": v.ConcurrentCap}),
	)

	var lines []string
	var buttons []presenter.Button
	for _, l := range v.Lines {
		args := map[string]any{
			"building": c.SettlementBuildingName(l.Building),
			"cost":     FormatMoney(c, l.CostMoney),
			"time":     FormatDuration(c, buildWait(l.ExpectedWait, l.BuildTime)),
		}
		key := "build.line.available"
		if len(l.Materials) > 0 {
			args["materials"] = materialsText(c, l.Materials)
		}
		if len(l.Short) > 0 {
			key = "build.line.short"
			args["short"] = materialsText(c, l.Short)
		}
		if l.State == BuildLocked {
			key = "build.line.locked_role"
			if len(l.Missing) > 0 {
				key = "build.line.locked"
				names := make([]string, 0, len(l.Missing))
				for _, m := range l.Missing {
					names = append(names, c.SettlementKnowledgeName(m))
				}
				args["missing"] = c.list(names)
			}
			if len(l.MissingBuildings) > 0 {
				names := make([]string, 0, len(l.MissingBuildings))
				for _, m := range l.MissingBuildings {
					names = append(names, c.SettlementBuildingName(m))
				}
				args["missing"] = c.list(names)
				key = "build.line.locked_building"
			}
		}
		lines = append(lines, c.T(key, args))
		if l.State == BuildAvailable {
			if btn, ok := keyboards.Button(c.T("build.button.place", map[string]any{"building": c.SettlementBuildingName(l.Building)}),
				AddrBuildLots, l.Building.Code); ok {
				buttons = append(buttons, btn)
			}
		}
	}
	list := body(lines...)
	if list == "" {
		list = c.T("build.empty", nil)
	}

	kb := keyboards.New()
	kb.Grid(2, buttons...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrBuildMenu}))

	return c.respond(paragraphs(head, list), kb.Build())
}

// ---------------------------------------------------------------------
// Construction progress (ADR 0028 section 6.3)
// ---------------------------------------------------------------------

// ConstructionProgress renders the settlement's construction queue.
func ConstructionProgress(c Context, v ConstructionProgressView) *presenter.Response {
	return c.withView(renderConstructionProgress(c, v), ScreenConstructionProgress, v)
}

func renderConstructionProgress(c Context, v ConstructionProgressView) *presenter.Response {
	head := c.T("construction.title", map[string]any{"name": v.Name})

	var lines []string
	kb := keyboards.New()
	for _, l := range v.Lines {
		key := "construction.line." + l.State
		if l.Building.Code != "road" {
			if btn, ok := keyboards.Button(c.T("construction.button.open", map[string]any{"building": c.SettlementBuildingName(l.Building)}),
				AddrBuildingView, l.ID); ok && !l.ByWork {
				kb.Row(btn)
			}
		}
		if l.ByWork {
			key = "construction.line.work"
			if b, ok := keyboards.Button(c.T("village.labor.button.site", map[string]any{"building": c.SettlementBuildingName(l.Building)}),
				AddrLaborSite, l.ID); ok {
				kb.Row(b)
			}
		}
		lines = append(lines, c.T(key, map[string]any{
			"building": c.SettlementBuildingName(l.Building),
			"time":     FormatClock(c, l.FinishAt),
			"duration": FormatDuration(c, l.Left),
			"percent":  l.ProgressBPS / 100,
			"left":     workLeft(c, l.LeftMinutes),
		}))
	}
	list := body(lines...)
	if list == "" {
		list = c.T("construction.empty", nil)
	}
	if len(v.Standing) > 0 {
		var buttons []presenter.Button
		for _, st := range v.Standing {
			if btn, ok := keyboards.Button(c.SettlementBuildingName(st.Building), AddrBuildingView, st.ID); ok {
				buttons = append(buttons, btn)
			}
		}
		kb.Grid(2, buttons...)
		list = paragraphs(list, c.T("construction.standing_hint", nil))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrConstructionProgress}))

	return c.respond(paragraphs(head, list), kb.Build())
}

// ---------------------------------------------------------------------
// The lot grid: the leader's own choice of where a building goes (ADR 0028
// section 6). "First legal lot" is not this game's rule — the owner's own
// words — so this screen exists specifically to let a real choice be made.
// ---------------------------------------------------------------------

// LotToken and ParseLotToken are the lot argument a button carries
// (internal/presentation/village).
func LotToken(x, y int, rotated bool) string { return village.LotToken(x, y, rotated) }

// ParseLotToken reads a LotToken back.
func ParseLotToken(s string) (x, y int, rotated, ok bool) { return village.ParseLotToken(s) }

// lotWindow is the window the keyboard shows: the whole grid when it fits,
// else MaxLotButtons lots square with its corner clamped inside the grid.
func lotWindow(n, wx, wy int) (x0, y0, x1, y1 int) {
	if n <= MaxLotButtons {
		return 0, 0, n, n
	}
	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		if v > n-MaxLotButtons {
			return n - MaxLotButtons
		}
		return v
	}
	x0, y0 = clamp(wx), clamp(wy)
	return x0, y0, x0 + MaxLotButtons, y0 + MaxLotButtons
}

// LotGrid renders the settlement's placement grid for one building type.
// Its view travels even in a group (withGroupView, not withView): a lot's
// state is exactly what the whole group already reads off this same
// screen's own buttons, nothing private about any one player.
func LotGrid(c Context, v LotGridView) *presenter.Response {
	return c.withGroupView(renderLotGrid(c, v), ScreenLotGrid, v)
}

// lotEmoji is one cell's own label: whether the building being placed
// fits here first (a green check outweighs everything else, because that
// is the one fact that decides whether pressing this button places the
// building), then the terrain/occupancy reason it does not.
func lotEmoji(state string, fits bool) string {
	if fits {
		return "✅"
	}
	switch state {
	case LotWater:
		return "💧"
	case LotRoad:
		return "🛣"
	case LotOccupied:
		return "🏠"
	case LotSteep:
		return "⛰"
	default:
		return "⬜"
	}
}

func renderLotGrid(c Context, v LotGridView) *presenter.Response {
	head := body(
		c.T("lots.title", map[string]any{"name": v.SettlementName}),
		c.T("lots.building", map[string]any{"building": c.SettlementBuildingName(v.Building)}),
	)
	legend := c.T("lots.legend", nil)

	kb := keyboards.New()
	x0, y0, x1, y1 := lotWindow(v.GridLots, v.WinX, v.WinY)
	for _, row := range v.Rows {
		var buttons []presenter.Button
		for _, cell := range row {
			if cell.X < x0 || cell.X >= x1 || cell.Y < y0 || cell.Y >= y1 {
				continue
			}
			label := lotEmoji(cell.State, cell.Fits)
			token := LotToken(cell.X, cell.Y, v.Rotated)
			var btn presenter.Button
			var ok bool
			switch v.Line {
			case LineStart:
				btn, ok = keyboards.Button(label, AddrBuildLots, v.Building.Code, "0", token)
			case LineEnd:
				btn, ok = keyboards.Button(label, AddrBuildPlaceMany, v.Building.Code, LotToken(v.From.X, v.From.Y, false), token)
			default:
				btn, ok = keyboards.Button(label, AddrBuildPlace, v.Building.Code, token)
			}
			if ok {
				buttons = append(buttons, btn)
			}
		}
		if len(buttons) > 0 {
			kb.Row(buttons...)
		}
	}
	if v.GridLots > MaxLotButtons {
		// slide the window: a step is half a window
		rotateArg, fromArg := "0", "-"
		if v.Rotated {
			rotateArg = "1"
		}
		switch v.Line {
		case LineStart:
			fromArg = LineStart
		case LineEnd:
			fromArg = LotToken(v.From.X, v.From.Y, false)
		}
		var moves []presenter.Button
		for _, m := range []struct {
			label  string
			dx, dy int
		}{{"◀️", -MaxLotButtons / 2, 0}, {"🔼", 0, -MaxLotButtons / 2}, {"🔽", 0, MaxLotButtons / 2}, {"▶️", MaxLotButtons / 2, 0}} {
			nx, ny := max(0, x0+m.dx), max(0, y0+m.dy)
			if btn, ok := keyboards.Button(m.label, AddrBuildLots, v.Building.Code, rotateArg, fromArg, LotToken(nx, ny, false)); ok {
				moves = append(moves, btn)
			}
		}
		kb.Row(moves...)
		legend = paragraphs(legend, c.T("lots.window", map[string]any{
			"x0": FormatNumber(c, int64(x0+1)), "x1": FormatNumber(c, int64(x1)),
			"y0": FormatNumber(c, int64(y0+1)), "y1": FormatNumber(c, int64(y1)),
			"n": FormatNumber(c, int64(v.GridLots)),
		}))
	}
	switch {
	case v.Multi && v.Line == "":
		if btn, ok := keyboards.Button(c.T("lots.button.line", nil), AddrBuildLots, v.Building.Code, "0", LineStart); ok {
			kb.Row(btn)
		}
	case v.Line == LineStart:
		legend = paragraphs(legend, c.T("lots.line_start", nil))
	case v.Line == LineEnd:
		legend = paragraphs(legend, c.T("lots.line_end", map[string]any{
			"y": FormatNumber(c, int64(v.From.Y+1)), "x": FormatNumber(c, int64(v.From.X+1)),
		}))
	}
	if v.CanRotate {
		rotateArg := "1"
		rotateKey := "lots.button.rotate"
		if v.Rotated {
			rotateArg = "0"
			rotateKey = "lots.button.reset_rotation"
		}
		if btn, ok := keyboards.Button(c.T(rotateKey, nil), AddrBuildLots, v.Building.Code, rotateArg); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrBuildMenu), RefreshData: keyboards.Data(AddrBuildLots, v.Building.Code)}))

	return c.respond(paragraphs(head, legend), kb.Build())
}

// LotConfirm renders the placement confirmation.
func LotConfirm(c Context, v LotConfirmView) *presenter.Response {
	return c.withGroupView(renderLotConfirm(c, v), ScreenLotConfirm, v)
}

func renderLotConfirm(c Context, v LotConfirmView) *presenter.Response {
	var materialLines []string
	for _, m := range v.Materials {
		materialLines = append(materialLines, c.T("build.confirm.material_line", map[string]any{
			"component": c.ComponentName(m.Component), "quantity": m.Quantity,
		}))
	}
	// Lots are shown 1-based (لات ۱، ۱ instead of a raw 0-index): a
	// coordinate is content a player reads, never an internal identifier.
	text := paragraphs(
		c.T("build.confirm.title", map[string]any{"building": c.SettlementBuildingName(v.Building)}),
		c.T("build.confirm.body", map[string]any{
			"cost": FormatMoney(c, v.CostMoney), "time": FormatDuration(c, buildWait(v.ExpectedWait, v.BuildTime)),
			"x": v.X + 1, "y": v.Y + 1,
		}),
		body(materialLines...),
	)
	if v.AutoRoads > 0 {
		text = paragraphs(text, c.T("build.confirm.auto_roads", map[string]any{"count": FormatNumber(c, int64(v.AutoRoads))}))
	}

	kb := keyboards.New()
	token := LotToken(v.X, v.Y, v.Rotated)
	if btn, ok := keyboards.Button(c.T("build.confirm.button", nil), AddrBuildPlace, v.Building.Code, token, VillageBuildConfirm); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrBuildLots, v.Building.Code)}))

	return c.respond(text, kb.Build())
}

// ScreenVillageHomeCall is the home screen of a group that has no village
// yet: the call to found one.
const ScreenVillageHomeCall = village.ScreenVillageHomeCall

// VillageHomeCall is what a group without a village sees as its home: the
// village is the home of a group, so the first thing offered is founding it.
func VillageHomeCall(c Context) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("village.home.found_button", nil), AddrSettlementFound)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.withView(c.respond(paragraphs(c.T("village.home.call_title", nil), c.T("village.home.call_body", nil)), kb.Build()),
		ScreenVillageHomeCall, struct{}{})
}

// VillageHomeNone is what a player who lives in no village sees when they ask
// for their village in a private chat.
func VillageHomeNone(c Context) *presenter.Response {
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(paragraphs(c.T("village.home.none_title", nil), c.T("village.home.none_body", nil)), kb.Build())
}

// batchFailureList names the lots of a refused batch, one line each, with
// the reason in words (lots are shown 1-based, as the confirm screen does).
func batchFailureList(c Context, lots []BatchLotFailure) string {
	if len(lots) == 0 {
		return ""
	}
	lines := make([]string, 0, len(lots))
	for _, l := range lots {
		lines = append(lines, c.T("village.batch_lot", map[string]any{
			"y": FormatNumber(c, int64(l.Y+1)), "x": FormatNumber(c, int64(l.X+1)), "reason": c.T("village.batch_reason."+l.Kind, nil),
		}))
	}
	return body(lines...)
}

// buildWait is the wait Telegram prints for a building: the expected wait with the crew
// the settlement has, falling back to the build time when there is none.
func buildWait(w village.BuildWaitView, effort time.Duration) time.Duration {
	if w.Seconds > 0 {
		return time.Duration(w.Seconds) * time.Second
	}
	return effort
}

// serviceNote says which service posts stood idle today and why (an idle post covers nothing).
func serviceNote(c Context, v VillageOverviewView) string {
	var lines []string
	for _, w := range v.Services {
		switch {
		case w.Grace && !w.GraceUntil.IsZero():
			var needs []string
			for _, n := range w.Needs {
				needs = append(needs, c.T("village.watch.need", map[string]any{"item": c.ComponentName(n.Component), "qty": FormatNumber(c, n.Quantity)}))
			}
			lines = append(lines, c.T("village.watch.grace", map[string]any{"building": c.SettlementBuildingName(w.Building),
				"until": FormatDate(c, w.GraceUntil), "needs": c.list(needs)}))
		case !w.Held:
			lines = append(lines, c.T("village.watch.idle", map[string]any{"building": c.SettlementBuildingName(w.Building), "reason": c.T("village.watch.reason."+w.Idle, nil)}))
		}
	}
	return body(lines...)
}

// personalList words what a player lacks for a post, one need after another.
func personalList(c Context, needs []village.PersonalNeed) string {
	var out []string
	for _, n := range needs {
		args := map[string]any{"have": FormatNumber(c, n.Have), "need": FormatNumber(c, n.Need)}
		switch n.Kind {
		case village.PersonalSkill:
			args["item"] = c.named("skill."+n.Item.Code, n.Item.Name)
		case village.PersonalCertificate, village.PersonalLiteracy:
			args["item"] = c.named("course."+n.Item.Code, n.Item.Name)
		}
		out = append(out, c.T("village.personal."+n.Kind, args))
	}
	return c.list(out)
}
