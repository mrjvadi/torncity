package screens

import (
	"strconv"
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

// Village overview, knowledge list, build menu and construction progress
// addresses.
const (
	AddrVillageHome          = "settlement:home"
	AddrVillageOverview      = "settlement:overview"
	AddrKnowledgeList        = "settlement:knowledge"
	AddrKnowledgeResearch    = "settlement:knowledge.research"
	AddrKnowledgeBuy         = "settlement:knowledge.buy"
	AddrBuildMenu            = "settlement:build"
	AddrBuildLots            = "settlement:build.lots"
	AddrBuildPlace           = "settlement:build.place"
	AddrConstructionProgress = "settlement:build.progress"
	AddrBuildDemolish        = "settlement:build.demolish"
)

// VillageBuildConfirm is the "confirm" argument's value a placement's
// second press carries, exactly ProductionConfirm's own role for a
// technology's publish confirmation.
const VillageBuildConfirm = "confirm"

// ---------------------------------------------------------------------
// Refusals (K2/W5 command handlers)
// ---------------------------------------------------------------------

// Village refusal kinds.
const (
	VillageNoSettlement    = "no_settlement"
	VillageNotOfficeHolder = "not_office_holder"
	VillageInsufficient    = "insufficient_funds"
	VillageBusy            = "busy"
	VillageAlreadyOwned    = "already_owned"
	VillageNotAvailable    = "not_available"
	VillageTerrain         = "terrain"
	VillagePrerequisite    = "prerequisite"
	VillageLiteracy        = "literacy"
	VillageNotFound        = "not_found"
	VillageOccupied        = "occupied"
	VillageUnbuildable     = "unbuildable"
	VillageOutOfBounds     = "out_of_bounds"
	VillageConcurrentCap   = "concurrent_cap"
	VillageNotDemolishable = "not_demolishable"
	VillageMaterials       = "materials"
	VillageNotCancellable  = "not_cancellable"
	// VillageBatch is a batch placement refused: Lots names every lot that
	// stopped it, each with its own kind.
	VillageBatch = "batch"
	// VillageNoRoad is a building no road could ever reach.
	VillageNoRoad = "no_road"
	// VillageGridMax is the technical bound on a grid's side.
	VillageGridMax = "grid_max"
	// Residence (village_residence.go).
	VillageAlreadyResident = "already_resident"
	VillageNotResident     = "not_resident"
	VillageResidenceWait   = "residence_cooldown"
	VillageHoldsOffice     = "holds_office"
	VillageNoHome          = "no_home"
	// Donating (village_donate.go).
	VillageDonateRange  = "donate_range"
	VillageDonateNoCash = "donate_no_cash"
)

// VillageRefusalView is a K2/W5 command refused before it changed anything.
type VillageRefusalView struct {
	Kind string
	// Back is where the refusal's own button leads; empty means the
	// village overview.
	Back string
	// Remaining is how long a residence cool-down still runs.
	Remaining time.Duration
	// Min and Max are the bounds of a donation the amount fell outside.
	Min, Max int64
	// Lots are the lots of a refused batch, with their reasons.
	Lots []BatchLotFailure
	// Action, Subject and Needs name what the refused command was about and
	// exactly what it is missing, each with where it comes from
	// (village_economy.go); empty for a refusal that has nothing to fetch.
	Action  string        `json:"action,omitempty"`
	Subject Named         `json:"subject,omitempty"`
	Needs   []VillageNeed `json:"needs,omitempty"`
}

// VillageRefusal renders a refused village command.
func VillageRefusal(c Context, v VillageRefusalView) *presenter.Response {
	return c.withView(renderVillageRefusal(c, v), ScreenVillageRefusal, v)
}

func renderVillageRefusal(c Context, v VillageRefusalView) *presenter.Response {
	if len(v.Needs) > 0 {
		return renderVillageNeeds(c, v)
	}
	kind := v.Kind
	switch kind {
	case VillageNoSettlement, VillageNotOfficeHolder, VillageInsufficient, VillageBusy, VillageAlreadyOwned,
		VillageNotAvailable, VillageTerrain, VillagePrerequisite, VillageLiteracy, VillageNotFound,
		VillageOccupied, VillageUnbuildable, VillageOutOfBounds, VillageConcurrentCap, VillageNotDemolishable, VillageMaterials,
		VillageNotCancellable, VillageAlreadyResident, VillageNotResident, VillageResidenceWait, VillageHoldsOffice, VillageNoHome,
		VillageDonateRange, VillageDonateNoCash, VillageBatch, VillageNoRoad, VillageGridMax, VillagePromotionTop,
		VillageStorageFull, VillageAlreadyWorking, VillageWorkplaceFull, VillageNotWorkplace,
		LaborNoJob, LaborNotHere, LaborFullyStaffed, LaborBudgetSpent, LaborNotEmployer, LaborNoNPC, LaborWageTooLow,
		LaborEmployerBroke, LaborNoSite:
	default:
		if isCitizenRefusal(kind) {
			return renderCitizenRefusal(c, v)
		}
		kind = VillageNotFound
	}
	back := v.Back
	if back == "" {
		back = AddrVillageOverview
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("village.refusal."+kind, map[string]any{
		"time": FormatDuration(c, v.Remaining), "min": FormatMoney(c, v.Min), "max": FormatMoney(c, v.Max),
		"lots": batchFailureList(c, v.Lots),
	}), kb.Build())
}

// ---------------------------------------------------------------------
// Village overview
// ---------------------------------------------------------------------

// VillageRoleLine is one role's own standing building(s), for the
// overview's short summary (the full catalogue is the build menu's job).
type VillageRoleLine struct {
	Role     string
	Building Named
	// Tier is the highest tier standing at this role.
	Tier int
}

// VillageOverviewView is a settlement's own status screen (ADR 0028 section
// 8.1's coverage numbers, ADR 0031 section 4.4's literacy).
type VillageOverviewView struct {
	Name string
	// Tier is "village", "town" or "city" (ADR 0028 section 4).
	Tier                      string
	Population, PopulationCap int64
	// FoodPercent .. SecurityPercent are ADR 0028 section 8.1's coverage
	// terms, 0-100 (or above, an over-provisioned settlement is not
	// clamped for display).
	FoodPercent, JobPercent, ServicePercent, HappinessPercent, SecurityPercent int
	// LiteracyPercent is ADR 0031 section 4.4's literacy_share, 0-100.
	LiteracyPercent int
	// Resident reports that the viewer lives here (their home is this
	// village); a non-resident is offered the join button. SettlementID
	// addresses the village for a client.
	Resident     bool
	SettlementID string
	Treasury     int64
	Buildings    []VillageRoleLine
	// IsHead is set when the viewer holds the village's top office: only
	// they place civic buildings and set the land terms. A resident who is
	// not the head is offered the citizen actions instead (docs/adr/0033
	// section 4.4): buy land, build a house, work, help the treasury.
	IsHead bool
	// Support is where the services the village does not have yet are:
	// the starter city. The village is home; its bank, market, jobs,
	// knowledge shop, hospital and jail are a journey away. Nil when no
	// such city is configured.
	Support *VillageSupport `json:"support,omitempty"`
	// Promotion is the way forward: the goals of the next tier and the
	// settlement's progress on each. Nil at the top of the ladder.
	Promotion *PromotionView `json:"promotion,omitempty"`
}

// VillageSupport names the city a village's residents travel to for the
// services the village cannot offer yet.
type VillageSupport struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// villageSupportServices are the services a village's home screen lists as
// «در Support - سفر کنید», each a journey to the same city through the
// existing travel flow (travel:options).
var villageSupportServices = []string{"bank", "market", "jobs", "knowledge", "hospital", "jail"}

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
	blocks := []string{head, population, treasury, coverage, buildings}
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
		if v.Resident {
			kb.Add(c.T("village.button.leave", nil), AddrVillageLeave)
		}
	} else {
		// Private: the village is home; the civic side is run in its group.
		blocks = append(blocks, c.T("village.private_hint", nil))
	}
	if v.Support != nil {
		args := map[string]any{"city": v.Support.Name}
		blocks = append(blocks, c.T("village.support.title", args))
		var row []presenter.Button
		for _, code := range villageSupportServices {
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

// Where the settlement stands on a knowledge item.
const (
	KnowledgeHeld        = "held"
	KnowledgeResearching = "researching"
	KnowledgeAvailable   = "available"
	KnowledgeLocked      = "locked"
)

// KnowledgeLine is one item of the knowledge list.
type KnowledgeLine struct {
	Knowledge Named
	State     string
	// ResearchCost/ResearchTime: what starting research now would take.
	ResearchCost int64
	ResearchTime time.Duration
	// BuyPrice is Support's own scarcity price for it (ADR 0031 section
	// 10 point 3); zero when Support does not sell it (restricted, or not
	// mode-eligible).
	BuyPrice int64
	// Missing are the prerequisites (exact codes or capabilities) it has
	// not unlocked, and TerrainOK whether its own terrain gate is met.
	Missing   []Named
	TerrainOK bool
}

// KnowledgeResearchLine is the research running now, if any.
type KnowledgeResearchLine struct {
	Knowledge Named
	FinishAt  time.Time
	Left      time.Duration
}

// KnowledgeListView is a settlement's own knowledge list.
type KnowledgeListView struct {
	Name            string
	Treasury        int64
	LiteracyPercent int
	Running         *KnowledgeResearchLine
	Lines           []KnowledgeLine
	// Hidden is how many items further away are kept out of sight until
	// the settlement comes closer (mirrors production.yml's own lab_later
	// shape, ADR 0021 section 14).
	Hidden int
}

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
	running := ""
	if r := v.Running; r != nil {
		running = c.T("knowledge.running", map[string]any{
			"knowledge": c.SettlementKnowledgeName(r.Knowledge), "time": FormatClock(c, r.FinishAt), "duration": FormatDuration(c, r.Left),
		})
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
		lines = append(lines, c.T(key, args))

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

// Where a building type stands for placement.
const (
	BuildAvailable = "available"
	BuildLocked    = "locked"
)

// BuildLine is one building type of the menu.
type BuildLine struct {
	Building  Named
	Role      string
	State     string
	CostMoney int64
	BuildTime time.Duration
	// Missing are unmet knowledge or role/tier prerequisites.
	Missing []Named
	// MissingBuildings are the buildings of the role a promotion still needs.
	MissingBuildings []Named `json:"missing_buildings,omitempty"`
	// Materials is what the building's construction takes from the stock and
	// Short the part of it the stock lacks (the attempt view names where to get
	// it).
	Materials []MaterialLine `json:"materials,omitempty"`
	Short     []MaterialLine `json:"short,omitempty"`
}

// BuildMenuView is a settlement's own construction menu.
type BuildMenuView struct {
	Name                         string
	Treasury                     int64
	RunningBuilds, ConcurrentCap int
	Lines                        []BuildLine
}

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
			"time":     FormatDuration(c, l.BuildTime),
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
	if b, ok := keyboards.Button(c.T("build.button.grow", nil), AddrGridGrow); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrBuildMenu}))

	return c.respond(paragraphs(head, list), kb.Build())
}

// ---------------------------------------------------------------------
// Construction progress (ADR 0028 section 6.3)
// ---------------------------------------------------------------------

// Where a queued placement stands.
const (
	ConstructionQueued   = "queued"
	ConstructionBuilding = "building"
)

// ConstructionLine is one placement in the settlement's own queue.
type ConstructionLine struct {
	// ID is the placed building's id: the button under the line opens its panel.
	ID         string
	Building   Named
	LotX, LotY int
	State      string
	FinishAt   time.Time
	Left       time.Duration
	// ProgressBPS and LeftMinutes describe a building raised by work (ADR
	// 0037): ByWork is set and FinishAt/Left are empty.
	ByWork     bool
	ProgressBPS int64
	LeftMinutes int64
}

// ConstructionProgressView is the settlement's own construction queue.
type ConstructionProgressView struct {
	Name  string
	Lines []ConstructionLine
	// Standing are the finished buildings (roads left out) whose panels the
	// screen opens.
	Standing []StandingLine
}

// StandingLine is one finished building, for a button that opens its panel.
type StandingLine struct {
	ID         string
	Building   Named
	LotX, LotY int
}

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

// LotToken is one lot's own compact callback argument: its coordinates,
// and whether the building being placed there is rotated — one token
// instead of three separate callback segments, so a 5x5 grid of buttons
// still fits Telegram's 64-byte callback_data budget with room to spare.
func LotToken(x, y int, rotated bool) string {
	t := strconv.Itoa(x) + "-" + strconv.Itoa(y)
	if rotated {
		t += "-r"
	}
	return t
}

// ParseLotToken reads a LotToken back. ok is false for anything malformed
// or negative — a forged or stale button, refused as firmly as any other
// tampered callback argument, never trusted as a coordinate on its own.
func ParseLotToken(s string) (x, y int, rotated, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false, false
	}
	if strings.HasSuffix(s, "-r") {
		rotated = true
		s = strings.TrimSuffix(s, "-r")
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, false
	}
	xi, err1 := strconv.Atoi(parts[0])
	yi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || xi < 0 || yi < 0 {
		return 0, 0, false, false
	}
	return xi, yi, rotated, true
}

// Lot states, for the grid's own emoji per cell.
const (
	LotFree     = "free"
	LotOccupied = "occupied"
	LotRoad     = "road"
	LotWater    = "water"
	LotSteep    = "steep"
)

// LotCell is one lot of the grid, as the leader sees it choosing where a
// specific building goes.
type LotCell struct {
	X, Y int
	// State is the lot's own terrain/occupancy, independent of which
	// building is being placed.
	State string
	// Fits says whether the building currently being placed could go here
	// — settlementbuilding.CanPlace's own answer, precomputed by the use
	// case so this package never re-derives a placement rule (skills.go's
	// own convention, applied here).
	Fits bool
}

// LotGridView is a settlement's own placement grid for one building type.
type LotGridView struct {
	SettlementName string
	Building       Named
	// CanRotate says the building's footprint is not square, so a rotate
	// button makes sense; Rotated is whether THIS render is showing it
	// turned 90 degrees.
	CanRotate bool
	Rotated   bool
	// GridLots is the grid's own side length (ADR 0028 section 4: 5 for a
	// village). Rows is the full grid, row-major, Rows[y][x] — the
	// complete state, so a game client (cmd/clientapi) can draw its own
	// map from the identical facts this screen's buttons come from.
	GridLots int
	Rows     [][]LotCell
	// Multi says the building is one lot, so a run of them can be laid from
	// one lot to another; Line is where that picking stands: "" (one lot at
	// a time), LineStart (choose the first lot) or LineEnd (choose the last,
	// From being the first).
	Multi bool
	Line  string
	From  LotBatchLot
	// WinX and WinY are the north-west lot of the window a Telegram keyboard
	// shows when the grid is wider than a keyboard row can hold
	// (MaxLotButtons); a client draws the whole grid from Rows and ignores them.
	WinX, WinY int
}

// MaxLotButtons is how many lots a Telegram keyboard shows in a row and in a
// column (a row holds 8 buttons at most): a bigger grid - land can be bought
// without a tier's limit - is shown as a window that slides over it.
const MaxLotButtons = 8

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

// The line-picking steps of a run of one-lot buildings on the grid.
const (
	LineStart = "line"
	LineEnd   = "end"
)

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

// MaterialLine is one component a building's own construction cost needs.
type MaterialLine struct {
	Component Named
	Quantity  int64
}

// LotConfirmView is the cost-and-time confirmation between choosing a lot
// and actually placing a building there.
type LotConfirmView struct {
	SettlementName string
	Building       Named
	X, Y           int
	Rotated        bool
	CostMoney      int64
	Materials      []MaterialLine
	BuildTime      time.Duration
	// AutoRoads is how many lots of road the game lays with the building to
	// connect it (0: it already touches the network).
	AutoRoads int
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
			"cost": FormatMoney(c, v.CostMoney), "time": FormatDuration(c, v.BuildTime),
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

// AddrSettlementFound is the press that opens the founding draft, the same
// command as sending «ساخت روستا».
const AddrSettlementFound = "settlement:found"

// ScreenVillageHomeCall is the home screen of a group that has no village
// yet: the call to found one.
const ScreenVillageHomeCall = "village_home_call"

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
