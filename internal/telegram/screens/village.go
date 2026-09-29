package screens

import (
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
	AddrVillageOverview      = "settlement:overview"
	AddrKnowledgeList        = "settlement:knowledge"
	AddrKnowledgeResearch    = "settlement:knowledge.research"
	AddrKnowledgeBuy         = "settlement:knowledge.buy"
	AddrBuildMenu            = "settlement:build"
	AddrBuildPlace           = "settlement:build.place"
	AddrConstructionProgress = "settlement:build.progress"
)

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
	Treasury        int64
	Buildings       []VillageRoleLine
}

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
	kb.Add(c.T("village.button.knowledge", nil), AddrKnowledgeList)
	kb.Add(c.T("village.button.build", nil), AddrBuildMenu)
	kb.Add(c.T("village.button.progress", nil), AddrConstructionProgress)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrVillageOverview}))

	return c.respond(paragraphs(head, population, treasury, coverage, buildings), kb.Build())
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
		if l.State == BuildLocked {
			key = "build.line.locked"
			if len(l.Missing) > 0 {
				names := make([]string, 0, len(l.Missing))
				for _, m := range l.Missing {
					names = append(names, c.SettlementKnowledgeName(m))
				}
				args["missing"] = c.list(names)
			}
		}
		lines = append(lines, c.T(key, args))
		if l.State == BuildAvailable {
			if btn, ok := keyboards.Button(c.T("build.button.place", map[string]any{"building": c.SettlementBuildingName(l.Building)}),
				AddrBuildPlace, l.Building.Code); ok {
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

// Where a queued placement stands.
const (
	ConstructionQueued   = "queued"
	ConstructionBuilding = "building"
)

// ConstructionLine is one placement in the settlement's own queue.
type ConstructionLine struct {
	Building   Named
	LotX, LotY int
	State      string
	FinishAt   time.Time
	Left       time.Duration
}

// ConstructionProgressView is the settlement's own construction queue.
type ConstructionProgressView struct {
	Name  string
	Lines []ConstructionLine
}

// ConstructionProgress renders the settlement's construction queue.
func ConstructionProgress(c Context, v ConstructionProgressView) *presenter.Response {
	return c.withView(renderConstructionProgress(c, v), ScreenConstructionProgress, v)
}

func renderConstructionProgress(c Context, v ConstructionProgressView) *presenter.Response {
	head := c.T("construction.title", map[string]any{"name": v.Name})

	var lines []string
	for _, l := range v.Lines {
		key := "construction.line." + l.State
		lines = append(lines, c.T(key, map[string]any{
			"building": c.SettlementBuildingName(l.Building),
			"time":     FormatClock(c, l.FinishAt),
			"duration": FormatDuration(c, l.Left),
		}))
	}
	list := body(lines...)
	if list == "" {
		list = c.T("construction.empty", nil)
	}

	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrConstructionProgress}))

	return c.respond(paragraphs(head, list), kb.Build())
}
