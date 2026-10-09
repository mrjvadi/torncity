package content

import "errors"

// This file is the ONE content schema of roadmap step 0.5 (ADR 0041 phase W0,
// ADR 0045 phase B0, ADR 0042 phase T0): the building FUNCTIONS and their
// content modules, storage classes and item storage fields, recipes, climate,
// settlement raids and detectors, rail and haul modes, and the road classes
// the router reads. Files: building_functions.yml, recipes.yml, climate.yml,
// settlement_raids.yml, rail.yml, roads.yml.
//
// ONE CONCEPT, NOT TWO. ADR 0035 3.1 and ADR 0040 Part B defined a "use" of a
// building (`building_uses`: use_code, status, since); ADR 0045 3.2 defined a
// "function" with modules (`building_functions`, `building_modules`). They are
// the same thing: a use_code IS a function code, the use status IS the
// function's status, and the "_let" variants (room_let, workshop_let,
// stall_let) are a LEASE on the function (commercial_leases), not functions of
// their own. A table named `building_uses` is never created. The decision and
// the migration plan are in ADR 0045 "as built" (docs/adr/0045-...md); the
// fields a use carried (market, family, permit, inspection, tax class,
// demand class, fit-out) are fields of BuildingFunctionDef below.
//
// THE GATE IS `requires`, NEVER A STAGE. Every row of every list here has a
// `requires` block (what the settlement must have: researched knowledge,
// buildings, staff; what the player must have: literacy, certificate, skill,
// rank, level). `requires: {}` says nothing is needed and must be written
// out. There is no `stage` key on any of these types: the strict decoder
// refuses it. A prerequisite that names something the catalogues do not have
// yet goes in `planned_knowledge` (the research line is named by an ADR but is
// not in settlement_knowledge.yml); the lint refuses a planned name that has
// since been added, so the list can only shrink. A gate that waits for the
// charters of ADR 0044 phase G2 says `deferred: charter`.
//
// EVERY ROW CITES ITS SOURCE. `source` names the ADR section the row comes
// from ("ADR 0041 8.4"). Where an ADR leaves a number open the row says so in
// `needs_research` instead of inventing one; the shipped-content test counts
// those entries so they can only shrink too.
//
// Player-facing names live in the locales (fa.yml, en.yml) under a section per
// kind, keyed on the row's code; the lint (locale test) refuses a row with no
// Persian name. `name` here is only a fallback.

// ErrInvalidBuildingSchemaContent means one of the building schema files is
// unusable.
var ErrInvalidBuildingSchemaContent = errors.New("content: invalid building schema content")

// Head is what every row of the schema carries.
type Head struct {
	Code string `yaml:"code" json:"code"`
	// Source cites where the row comes from: an ADR section ("ADR 0041 8.4") or,
	// for the rows the missing-prerequisites research proposed and the owner
	// accepted, the research section ("RESEARCH 2026-10-03 5.5"). Required.
	Source string `yaml:"source" json:"source"`
	// Evidence is the verification tag of the row's numbers: adr (the ADR's own
	// draft value, config), E_read (a source was opened and says it), E_read_in_part,
	// I (our inference) or D (a draft number with no calibration source, to be
	// proven by `admin economy verify` or a balance run before it ships).
	// REQUIRED. `unverified` is not a value: a number nothing supports is not
	// written, it goes to needs_research.
	Evidence string `yaml:"evidence" json:"evidence"`
	// Requires is the gate: what the settlement and the player must have.
	// REQUIRED (a nil block is refused; write `requires: {}` for "nothing").
	Requires *AvailabilityNeeds `yaml:"requires" json:"requires"`
	// PlannedKnowledge names research lines the ADR gives this row but the
	// knowledge catalogue does not have yet ("needs research" for the
	// catalogue owner). Each is an additional AND prerequisite.
	PlannedKnowledge []string `yaml:"planned_knowledge,omitempty" json:"planned_knowledge,omitempty"`
	// Deferred names the later work that owns a part of the gate
	// (growthDeferrals: charter, building_missing, finance).
	Deferred string `yaml:"deferred,omitempty" json:"deferred,omitempty"`
	// NeedsResearch lists the fields the ADR leaves open: flagged, not invented.
	NeedsResearch []string `yaml:"needs_research,omitempty" json:"needs_research,omitempty"`
	Note          string   `yaml:"note,omitempty" json:"note,omitempty"`
}

// Function kinds (ADR 0041 3.1).
const (
	FunctionProduction     = "production"
	FunctionExtraction     = "extraction"
	FunctionService        = "service"
	FunctionStorage        = "storage"
	FunctionHousing        = "housing"
	FunctionInfrastructure = "infrastructure"
)

var functionKinds = map[string]bool{
	FunctionProduction: true, FunctionExtraction: true, FunctionService: true,
	FunctionStorage: true, FunctionHousing: true, FunctionInfrastructure: true,
}

// Function markets (ADR 0035 3.1). Shadow functions are revealed by
// underworld reputation and knowledge, never by a plain menu.
const (
	MarketLegal  = "legal"
	MarketShadow = "shadow"
)

// What a staffed function does with no one on shift (ADR 0041 N1, rule 1c).
const (
	// IfUnstaffedIdle: it produces and provides nothing.
	IfUnstaffedIdle = "idle"
	// IfUnstaffedBaseRoom: a store keeps only the base room of its class.
	IfUnstaffedBaseRoom = "base_room"
	// IfUnstaffedUnheld: a claiming post loses its hold after the grace period.
	IfUnstaffedUnheld = "unheld"
	// IfUnstaffedDegraded: it works at a reduced rate (a player's hungry watch).
	IfUnstaffedDegraded = "degraded"
	// IfUnstaffedDecays: it has no standing staff; repair jobs keep it up.
	IfUnstaffedDecays = "decays"
)

var ifUnstaffedKinds = map[string]bool{
	IfUnstaffedIdle: true, IfUnstaffedBaseRoom: true, IfUnstaffedUnheld: true,
	IfUnstaffedDegraded: true, IfUnstaffedDecays: true,
}

// Zone kinds a function may stand in (ADR 0042 3.3).
var zoneKinds = map[string]bool{
	"core": true, "residential": true, "farm": true, "industry": true,
	"military": true, "port": true, "site": true,
}

// Owner kinds (ADR 0045 3.1, parcels.owner_kind).
var ownerKinds = map[string]bool{"treasury": true, "player": true, "union": true, "npc_household": true}

// Function statuses a stored function row may have (ADR 0035 `building_uses`
// status, kept as the one status of the one table).
var FunctionStatuses = []string{"active", "fitout", "suspended", "sealed", "dormant"}

// StorageClassDef is a storage class (ADR 0041 6.4): the room goods occupy.
type StorageClassDef struct {
	Head `yaml:",inline"`
	// BaseRoom is the room without any building (the open yard, the bins).
	BaseRoom int `yaml:"base_room" json:"base_room"`
	// Name is the fallback display name.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// ItemStorageDef is the W0 item field set for one item (ADR 0041 phase W0, 6.4,
// 6.5): the storage class it occupies, its bulk (spaces per unit) and the food
// points a unit gives a worker. Code is the item (component) code. A row with
// Planned set is an item the ADRs introduce that items.yml does not have yet.
type ItemStorageDef struct {
	Head       `yaml:",inline"`
	Class      string `yaml:"class" json:"class"`
	Bulk       int    `yaml:"bulk" json:"bulk"`
	FoodPoints int    `yaml:"food_points,omitempty" json:"food_points,omitempty"`
	Planned    bool   `yaml:"planned,omitempty" json:"planned,omitempty"`
	// Raw marks a planned item gathered from the land, a field, a pasture or
	// the water (made by no function of this schema): clay, fleece, hide, fish.
	Raw bool `yaml:"raw,omitempty" json:"raw,omitempty"`
}

// Module effects are real mechanics (ADR 0045 3.2), never a flat percentage:
// each effect code is read by exactly one rule in code.
var moduleEffects = map[string]bool{
	"housing_capacity": true, "warmth_shelter": true, "cooking": true, "water_supply": true,
	"personal_storage": true, "workbench": true, "forge": true, "loom": true, "kiln": true,
	"oven": true, "millstone": true, "shelves": true, "staff_post": true, "field_plot": true,
	"barn": true, "shaft_level": true, "hoist": true, "ore_bin": true, "rail_siding": true,
	"signal_fire": true, "lookout_platform": true, "foundation": true, "storeroom": true,
	"vault": true, "record_vault": true, "transient_bed": true,
}

// ModuleKindDef is one kind of module the owner puts inside a lot (ADR 0045
// 3.2): a room, an equipment piece, a line, a store, a staff post.
type ModuleKindDef struct {
	Head   `yaml:",inline"`
	Name   string `yaml:"name,omitempty" json:"name,omitempty"`
	Effect string `yaml:"effect" json:"effect"`
	// Provides: what one module gives (housing_capacity: 2, a storage class's
	// spaces, personal_storage spaces).
	Provides map[string]int `yaml:"provides,omitempty" json:"provides,omitempty"`
	// Consumes per game day (a hearth burns firewood).
	Consumes      map[string]int   `yaml:"consumes,omitempty" json:"consumes,omitempty"`
	CostMaterials map[string]int64 `yaml:"cost_materials,omitempty" json:"cost_materials,omitempty"`
	// BuildShifts is the work in builder shifts (ADR 0037 labour market).
	BuildShifts int `yaml:"build_shifts,omitempty" json:"build_shifts,omitempty"`
	// DecayBPSPerDay is the daily condition decay (ADR 0041 6.10).
	DecayBPSPerDay int `yaml:"decay_bps_per_day,omitempty" json:"decay_bps_per_day,omitempty"`
	// Area is the floor area the module takes in a building (ADR 0045 3.2: "usable module area follows from the
	// footprint and the storeys"); a cellar, under the floor, takes none. The area a footprint cell gives per
	// storey is config building.area_per_cell.
	Area int `yaml:"area,omitempty" json:"area,omitempty"`
}

// ModuleSlotDef allows up to Max modules of one kind in a function.
type ModuleSlotDef struct {
	Module string `yaml:"module" json:"module"`
	Max    int    `yaml:"max" json:"max"`
}

// FunctionLevelDef is one rung of a function's level ladder (what each level adds and
// requires: the old `tier`, now a gate by what the settlement has).
type FunctionLevelDef struct {
	Level            int                `yaml:"level" json:"level"`
	Adds             []string           `yaml:"adds,omitempty" json:"adds,omitempty"`
	Requires         *AvailabilityNeeds `yaml:"requires" json:"requires"`
	PlannedKnowledge []string           `yaml:"planned_knowledge,omitempty" json:"planned_knowledge,omitempty"`
	// Building is the catalogue building (settlement_buildings.yml, citizen_buildings.yml) the lot stands as at
	// this level: the old tier is a level, and the legacy code is what the effects and the map still read until
	// ADR 0044 G7 drops it. It must be one of the function's `replaces`.
	Building      string           `yaml:"building,omitempty" json:"building,omitempty"`
	CostMoney     int64            `yaml:"cost_money,omitempty" json:"cost_money,omitempty"`
	CostMaterials map[string]int64 `yaml:"cost_materials,omitempty" json:"cost_materials,omitempty"`
	BuildHours    int              `yaml:"build_hours,omitempty" json:"build_hours,omitempty"`
}

// StaffSlotDef is a post at the function (ADR 0041 5): a role, how many, the
// shift length in game hours and the wage as basis points of the market wage
// (0: the role's class).
type StaffSlotDef struct {
	Role       string `yaml:"role" json:"role"`
	Slots      int    `yaml:"slots" json:"slots"`
	ShiftHours int    `yaml:"shift_hours,omitempty" json:"shift_hours,omitempty"`
	WageBPS    int    `yaml:"wage_bps,omitempty" json:"wage_bps,omitempty"`
	// MinLevel is the function level at which the post exists (a hotel's
	// housekeepers are level 2); 0 or 1: from the first level.
	MinLevel int `yaml:"min_level,omitempty" json:"min_level,omitempty"`
}

// ConsumesDef is what a shift uses (ADR 0041 N2, N3, 6.6, 6.8).
type ConsumesDef struct {
	// Inputs per cycle (item -> quantity).
	Inputs map[string]int `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	// Fuel per shift (item -> quantity: firewood).
	Fuel map[string]int `yaml:"fuel,omitempty" json:"fuel,omitempty"`
	// FoodPoints a shift draws from its owner's food store.
	FoodPoints int `yaml:"food_points,omitempty" json:"food_points,omitempty"`
	// Water: the function needs a well within reach or a river lot.
	Water bool `yaml:"water,omitempty" json:"water,omitempty"`
	// ToolWearBPS per cycle.
	ToolWearBPS int `yaml:"tool_wear_bps,omitempty" json:"tool_wear_bps,omitempty"`
}

// ProducesDef is what a cycle gives and where it goes (ADR 0041 N3, N4).
type ProducesDef struct {
	// Outputs per cycle (item -> quantity).
	Outputs map[string]int `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	// Store is the storage class the outputs go to.
	Store string `yaml:"store,omitempty" json:"store,omitempty"`
	// Service names the service it provides while staffed (coverage, teaching,
	// treatment, security, housing): a code the presentation reads.
	Service string `yaml:"service,omitempty" json:"service,omitempty"`
}

// StorageDef is room provided (class -> spaces) and room needed free per cycle.
type StorageDef struct {
	Provides map[string]int `yaml:"provides,omitempty" json:"provides,omitempty"`
	// Communal is the share of Provides (class -> spaces) the residents look after
	// themselves, by a rota, with no paid keeper: a small village store is kept by its
	// own people. It always counts; a hired keeper adds the rest of Provides and the
	// lower spoilage. Never more than Provides.
	Communal map[string]int `yaml:"communal,omitempty" json:"communal,omitempty"`
	Needs    map[string]int `yaml:"needs,omitempty" json:"needs,omitempty"`
}

// MaintenanceDef replaces the abstract money upkeep (ADR 0041 N9, 6.10).
type MaintenanceDef struct {
	DecayBPSPerDay  int              `yaml:"decay_bps_per_day,omitempty" json:"decay_bps_per_day,omitempty"`
	RepairMaterials map[string]int64 `yaml:"repair_materials,omitempty" json:"repair_materials,omitempty"`
	RepairLabour    int              `yaml:"repair_labour,omitempty" json:"repair_labour,omitempty"`
}

// CoverageDef is the coverage content of a claiming or watching building (ADR
// 0042 4.4, 8; read by territory.Compute). Lengths in metres.
type CoverageDef struct {
	Bins            int `yaml:"bins" json:"bins"`
	ObserverHeightM int `yaml:"observer_height_m" json:"observer_height_m"`
	ClaimBaseM      int `yaml:"claim_base_m" json:"claim_base_m"`
	ClaimCapM       int `yaml:"claim_cap_m" json:"claim_cap_m"`
	SightM          int `yaml:"sight_m" json:"sight_m"`
}

// LinksDef names the chain a function belongs to: the functions whose output
// it eats and the functions that eat its output (ADR 0041 6, 9.1).
type LinksDef struct {
	From []string `yaml:"from,omitempty" json:"from,omitempty"`
	To   []string `yaml:"to,omitempty" json:"to,omitempty"`
}

// FitoutDef is the cost of changing a lot to this function (ADR 0035 3.2, 3.3:
// materials and labour hours by trade, plus the use-change fee of ADR 0044).
type FitoutDef struct {
	Materials map[string]int64 `yaml:"materials,omitempty" json:"materials,omitempty"`
	// LabourHours by trade (carpentry, masonry, electrics).
	LabourHours map[string]int `yaml:"labour_hours,omitempty" json:"labour_hours,omitempty"`
}

// BuildingFunctionDef is one function a lot can be (ADR 0045 3.2): the single
// concept that ADR 0035's `use` and ADR 0045's `function` merge into. The 40
// settlement buildings and the citizen buildings become functions with slot
// lists (`replaces`); none is lost.
type BuildingFunctionDef struct {
	Head `yaml:",inline"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Kind is ADR 0041 3.1's node kind.
	Kind string `yaml:"kind" json:"kind"`
	// Market is legal (default) or shadow (ADR 0035 3.1).
	Market string `yaml:"market,omitempty" json:"market,omitempty"`
	// Family groups functions for permits and presentation (ADR 0035 3.1):
	// the building role family (craft, food, security, ...).
	Family string `yaml:"family" json:"family"`
	// Permit is the permit class a private owner pays for (ADR 0033 4.5):
	// residential, craft, food, commerce, lodging; empty for public buildings.
	Permit string `yaml:"permit,omitempty" json:"permit,omitempty"`
	// Zones are the zone kinds it may stand in (ADR 0042 7.1).
	Zones []string `yaml:"zones" json:"zones"`
	// TerrainTags and TerrainMode: terrain the footprint must (required) or
	// should (preferred) satisfy: a biome code of world.yml or a tag of
	// terrain_tags (river_lot, coastal_lot, harbour_water, highland_tropics).
	TerrainTags []string `yaml:"terrain_tags,omitempty" json:"terrain_tags,omitempty"`
	TerrainMode string   `yaml:"terrain_mode,omitempty" json:"terrain_mode,omitempty"`
	// Footprint is the least and greatest size in cells: [minW, minD, maxW, maxD].
	Footprint [4]int `yaml:"footprint" json:"footprint"`
	// Owners are the owner kinds that may hold it (ADR 0045 3.1).
	Owners []string `yaml:"owners" json:"owners"`
	// Slots are the module kinds it takes, each with a count limit.
	Slots []ModuleSlotDef `yaml:"slots,omitempty" json:"slots,omitempty"`
	// Levels is the level ladder.
	Levels []FunctionLevelDef `yaml:"levels" json:"levels"`
	// Staff are the posts (rule 1c: who works there); IfUnstaffed says what
	// stands idle without them.
	Staff       []StaffSlotDef  `yaml:"staff,omitempty" json:"staff,omitempty"`
	IfUnstaffed string          `yaml:"if_unstaffed,omitempty" json:"if_unstaffed,omitempty"`
	Consumes    *ConsumesDef    `yaml:"consumes,omitempty" json:"consumes,omitempty"`
	Produces    *ProducesDef    `yaml:"produces,omitempty" json:"produces,omitempty"`
	Storage     *StorageDef     `yaml:"storage,omitempty" json:"storage,omitempty"`
	Maintenance *MaintenanceDef `yaml:"maintenance,omitempty" json:"maintenance,omitempty"`
	Coverage    *CoverageDef    `yaml:"coverage,omitempty" json:"coverage,omitempty"`
	Links       *LinksDef       `yaml:"links,omitempty" json:"links,omitempty"`
	// LedgerReasons are the ledger reasons its money flows use (rule 1c).
	LedgerReasons []string `yaml:"ledger_reasons,omitempty" json:"ledger_reasons,omitempty"`
	// Inspection, TaxClass, DemandClass and Fitout are the fields ADR 0035's
	// use carried; they keep their meaning.
	Inspection  string     `yaml:"inspection,omitempty" json:"inspection,omitempty"`
	TaxClass    string     `yaml:"tax_class,omitempty" json:"tax_class,omitempty"`
	DemandClass string     `yaml:"demand_class,omitempty" json:"demand_class,omitempty"`
	Fitout      *FitoutDef `yaml:"fitout,omitempty" json:"fitout,omitempty"`
	// Replaces lists the catalogue codes (settlement_buildings.yml,
	// citizen_buildings.yml) this function takes over: the data migration plan.
	Replaces []string `yaml:"replaces,omitempty" json:"replaces,omitempty"`
}

// Terrain tag origins.
const (
	TagOriginLotFlag  = "lot_flag" // a lot flag the application layer synthesises (ADR 0028 6.1)
	TagOriginDeposit  = "deposit"  // set when the territory covers a deposit cell
	TagOriginWorldgen = "worldgen" // derived from the generated terrain (T0 tile queries)
)

var tagOrigins = map[string]bool{TagOriginLotFlag: true, TagOriginDeposit: true, TagOriginWorldgen: true}

// TerrainTagDef is one terrain tag the schema's rows may name (besides the
// biome codes of world.yml). Status is "built" (the application layer or
// worldgen already sets it) or "planned" (named by an accepted research; the
// derivation rule is open).
type TerrainTagDef struct {
	Head   `yaml:",inline"`
	Origin string `yaml:"origin" json:"origin"`
	Status string `yaml:"status" json:"status"`
}

// PlannedSkillDef is a skill an ADR's role trains that the skill set in code
// (player.SkillCodes, a rule not content) does not have yet (audit M-04). It
// is data for the decision, not a skill: the lint refuses a name that has
// since been added.
type PlannedSkillDef struct {
	Code     string `yaml:"code" json:"code"`
	Source   string `yaml:"source" json:"source"`
	Evidence string `yaml:"evidence" json:"evidence"`
}

// RecipeDef is one recipe (ADR 0045 4.3): what a station turns inputs into.
// The research that gates it is `requires.knowledge`.
type RecipeDef struct {
	Head `yaml:",inline"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Stations are the functions where it can be made.
	Stations []string `yaml:"stations" json:"stations"`
	// Inputs and Outputs per cycle (item -> quantity).
	Inputs  map[string]int `yaml:"inputs" json:"inputs"`
	Outputs map[string]int `yaml:"outputs" json:"outputs"`
	// Skill is the trade whose experience the craft trains (the role's skill).
	Skill string `yaml:"skill,omitempty" json:"skill,omitempty"`
	// CycleGameHours overrides the station's shift when the ADR fixes it.
	CycleGameHours int `yaml:"cycle_game_hours,omitempty" json:"cycle_game_hours,omitempty"`
	// NeedsWater: the station needs water (a well, a spring or a river).
	NeedsWater bool `yaml:"needs_water,omitempty" json:"needs_water,omitempty"`
	// ToolTier is the least tool tier T0..T3 (ADR 0045 4.2).
	ToolTier int `yaml:"tool_tier,omitempty" json:"tool_tier,omitempty"`
}

// ClimateBandDef is one temperature band (ADR 0045 5.2): from MinTempC up to
// the next band, with the insulation points a person needs.
type ClimateBandDef struct {
	Code               string `yaml:"code" json:"code"`
	MinTempC           int    `yaml:"min_temp_c" json:"min_temp_c"`
	RequiredInsulation int    `yaml:"required_insulation" json:"required_insulation"`
}

// ClimateDef is climate.yml (ADR 0045 5.1, 5.2): the warmth and thirst rules'
// numbers. The world's base temperature comes from worldgen (biome, latitude)
// and the seasonal swing from the game calendar; these are the bands and
// modifiers on top.
type ClimateDef struct {
	Head  `yaml:",inline"`
	Bands []ClimateBandDef `yaml:"bands" json:"bands"`
	// RainInsulation and WindInsulation add to the requirement; HeavyWork
	// lowers it while working (and marks the base layer wet).
	RainInsulation      int `yaml:"rain_insulation" json:"rain_insulation"`
	WindInsulation      int `yaml:"wind_insulation" json:"wind_insulation"`
	HeavyWorkInsulation int `yaml:"heavy_work_insulation" json:"heavy_work_insulation"`
	// ThirstPerGameDay is the baseline rise (points of 100).
	ThirstPerGameDay int `yaml:"thirst_per_game_day" json:"thirst_per_game_day"`
	// GameYearDays and Seasons: the calendar the swing follows.
	GameYearDays int `yaml:"game_year_days" json:"game_year_days"`
	Seasons      int `yaml:"seasons" json:"seasons"`
}

// SettlementRaidDef is settlement_raids.yml's tuning (ADR 0045 7): lead time,
// loot caps, frequency and grace, all `raid.*` config in the ADR's draft
// values. The tables it feeds are `settlement_raids*`.
type SettlementRaidDef struct {
	Head `yaml:",inline"`
	// MaxLeadHours: the raider picks the start inside this many hours (48).
	MaxLeadHours int `yaml:"max_lead_hours" json:"max_lead_hours"`
	// Loot caps in basis points of exposed stock (7.3).
	LootTreasuryBPS        int `yaml:"loot_treasury_bps" json:"loot_treasury_bps"`
	LootStorehouseStackBPS int `yaml:"loot_storehouse_stack_bps" json:"loot_storehouse_stack_bps"`
	LootPersonalStorageBPS int `yaml:"loot_personal_storage_bps" json:"loot_personal_storage_bps"`
	// DamageMaxBPS: a raid damages buildings up to this much condition (2000).
	DamageMaxBPS int `yaml:"damage_max_bps" json:"damage_max_bps"`
	// Frequency (7.3).
	CooldownHours        int `yaml:"cooldown_hours" json:"cooldown_hours"`
	MaxPerWeek           int `yaml:"max_per_week" json:"max_per_week"`
	AfterLossShieldHours int `yaml:"after_loss_shield_hours" json:"after_loss_shield_hours"`
	// ShieldLootCutBPS: a settlement under shield loses this much of the loot
	// cap against other attackers (50 percent).
	ShieldLootCutBPS int `yaml:"shield_loot_cut_bps" json:"shield_loot_cut_bps"`
	// GraceDays: founding grace in time, not presence (7.5).
	GraceDays int `yaml:"grace_days" json:"grace_days"`
	// Concealment research (7.2): the stealth per level and the levels.
	StealthBPSPerLevel int `yaml:"stealth_bps_per_level" json:"stealth_bps_per_level"`
	StealthLevels      int `yaml:"stealth_levels" json:"stealth_levels"`
}

// SettlementRaidDetectorDef is one detection source (ADR 0045 7.2): its range
// and chance. Function is the building function that stands for it; a source
// whose function is not in the catalogue yet names PlannedFunction.
type SettlementRaidDetectorDef struct {
	Head            `yaml:",inline"`
	Function        string `yaml:"function,omitempty" json:"function,omitempty"`
	PlannedFunction string `yaml:"planned_function,omitempty" json:"planned_function,omitempty"`
	RangeM          int    `yaml:"range_m" json:"range_m"`
	// DetectChanceBPS is the chance a staffed, in-condition detector notices.
	DetectChanceBPS int `yaml:"detect_chance_bps" json:"detect_chance_bps"`
	// Relays: a beacon chain relays a sighting to the settlement at once.
	Relays bool `yaml:"relays,omitempty" json:"relays,omitempty"`
	// AlongRoute: a patrol covers only its route.
	AlongRoute bool `yaml:"along_route,omitempty" json:"along_route,omitempty"`
}

// RoadClassDef is one road class (ADR 0042 6.1): the planner's numbers and
// the build's. Lengths in metres, speeds in km/h.
type RoadClassDef struct {
	Head           `yaml:",inline"`
	Name           string `yaml:"name,omitempty" json:"name,omitempty"`
	MaxGradeBPS    int    `yaml:"max_grade_bps" json:"max_grade_bps"`
	SpeedKMH       int    `yaml:"speed_kmh" json:"speed_kmh"`
	WorkHoursPerKM int    `yaml:"work_hours_per_km" json:"work_hours_per_km"`
	// MaterialsPerKM (item -> quantity).
	MaterialsPerKM map[string]int `yaml:"materials_per_km,omitempty" json:"materials_per_km,omitempty"`
	// BridgeMaxSpanM is the longest span the class carries; Fords: a stream is
	// forded (path), otherwise culverted.
	BridgeMaxSpanM int  `yaml:"bridge_max_span_m" json:"bridge_max_span_m"`
	Fords          bool `yaml:"fords,omitempty" json:"fords,omitempty"`
}

// RoadPlannerDef is roads.yml's planner block (ADR 0042 6.2, 6.3).
type RoadPlannerDef struct {
	Head                `yaml:",inline"`
	SearchMarginM       int `yaml:"search_margin_m" json:"search_margin_m"`
	MaxExpansions       int `yaml:"max_expansions" json:"max_expansions"`
	BridgeCostRatio     int `yaml:"bridge_cost_ratio" json:"bridge_cost_ratio"`
	StreamCrossingSteps int `yaml:"stream_crossing_steps" json:"stream_crossing_steps"`
	// TerrainBPS maps a biome code to its clearing multiplier (10000 = 1).
	TerrainBPS map[string]int `yaml:"terrain_bps" json:"terrain_bps"`
	// River width classes in metres (ADR 0042 6.3).
	StreamWidthM     int `yaml:"stream_width_m" json:"stream_width_m"`
	RiverWidthM      int `yaml:"river_width_m" json:"river_width_m"`
	GreatRiverWidthM int `yaml:"great_river_width_m" json:"great_river_width_m"`
	// HighMountain: ground above this elevation costs the multiplier.
	HighMountainM   int `yaml:"high_mountain_m" json:"high_mountain_m"`
	HighMountainBPS int `yaml:"high_mountain_bps" json:"high_mountain_bps"`
}

// RailClassDef is a rail class (ADR 0045 8.5): the road router with a gentler
// slope limit. A class whose limit the ADR does not fix lists it in
// needs_research and has MaxGradeBPS 0, which the router refuses.
type RailClassDef struct {
	Head           `yaml:",inline"`
	Name           string `yaml:"name,omitempty" json:"name,omitempty"`
	MaxGradeBPS    int    `yaml:"max_grade_bps,omitempty" json:"max_grade_bps,omitempty"`
	BridgeMaxSpanM int    `yaml:"bridge_max_span_m,omitempty" json:"bridge_max_span_m,omitempty"`
	// Haul is the haul mode (HaulModeDef code) that runs on it.
	Haul string `yaml:"haul" json:"haul"`
	// Materials per km (item -> quantity): rails, sleepers, ballast of gravel.
	MaterialsPerKM map[string]int `yaml:"materials_per_km,omitempty" json:"materials_per_km,omitempty"`
}

// HaulModeDef is one rung of the hauling ladder (ADR 0045 8.4): the capacity a
// carrier moves on a surface. Cost scales with weight and distance.
type HaulModeDef struct {
	Head       `yaml:",inline"`
	Name       string `yaml:"name,omitempty" json:"name,omitempty"`
	CapacityKG int    `yaml:"capacity_kg" json:"capacity_kg"`
	// Surface is the road class or rail class the mode runs on ("" = anywhere).
	Surface string `yaml:"surface,omitempty" json:"surface,omitempty"`
}

// RailDef is rail.yml's rules block (ADR 0045 8.5): what the simulation and
// the freight rules need that the ADR fixes.
type RailDef struct {
	Head `yaml:",inline"`
	// SiteKinds are the stop kinds a route has (station, siding, passing loop).
	StopKinds []string `yaml:"stop_kinds" json:"stop_kinds"`
	// FreightUnit: freight is priced per tonne-kilometre.
	FreightUnit string `yaml:"freight_unit" json:"freight_unit"`
	// Owners that may hold a line (open question 6, recommended: all three).
	Owners []string `yaml:"owners" json:"owners"`
}
