package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// VillageBuildConfirm is the "confirm" argument's value a placement's
// second press carries, exactly ProductionConfirm's own role for a
// technology's publish confirmation.
const VillageBuildConfirm = "confirm"

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
	// Back is where the refusal leads back to; the zero value means the
	// village overview.
	Back presentation.Ref
	// Remaining is how long a residence cool-down still runs.
	Remaining time.Duration
	// Min and Max are the bounds of a donation the amount fell outside.
	Min, Max int64
	// Lots are the lots of a refused batch, with their reasons.
	Lots []BatchLotFailure
	// Action, Subject and Needs name what the refused command was about and
	// exactly what it is missing, each with where it comes from
	// (village_economy.go); empty for a refusal that has nothing to fetch.
	Action  string             `json:"action,omitempty"`
	Subject presentation.Named `json:"subject,omitempty"`
	Needs   []VillageNeed      `json:"needs,omitempty"`
}

// VillageRoleLine is one role's own standing building(s), for the
// overview's short summary (the full catalogue is the build menu's job).
type VillageRoleLine struct {
	Role     string
	Building presentation.Named
	// Tier is the highest tier standing at this role.
	Tier int
}

// VillageOverviewView is a settlement's own status screen (ADR 0028 section
// 8.1's coverage numbers, ADR 0031 section 4.4's literacy).
type VillageOverviewView struct {
	Name string
	// Tier is "village", "town" or "city" (ADR 0028 section 4).
	Tier string
	// Population is the players who live here; PopulationCap is the homes the
	// village has: the households it starts with plus what its standing
	// buildings add (the same number the labour market's housing is).
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

// Where the settlement stands on a knowledge item.
const (
	KnowledgeHeld        = "held"
	KnowledgeResearching = "researching"
	KnowledgeAvailable   = "available"
	KnowledgeLocked      = "locked"
)

// KnowledgeLine is one item of the knowledge list.
type KnowledgeLine struct {
	Knowledge presentation.Named
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
	Missing   []presentation.Named
	TerrainOK bool
}

// KnowledgeResearchLine is the research running now, if any.
type KnowledgeResearchLine struct {
	Knowledge presentation.Named
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

// Where a building type stands for placement.
const (
	BuildAvailable = "available"
	BuildLocked    = "locked"
)

// BuildLine is one building type of the menu.
type BuildLine struct {
	Building  presentation.Named
	Role      string
	State     string
	CostMoney int64
	BuildTime time.Duration
	// Missing are unmet knowledge or role/tier prerequisites.
	Missing []presentation.Named
	// MissingBuildings are the buildings of the role a promotion still needs.
	MissingBuildings []presentation.Named `json:"missing_buildings,omitempty"`
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

// Where a queued placement stands.
const (
	ConstructionQueued   = "queued"
	ConstructionBuilding = "building"
)

// ConstructionLine is one placement in the settlement's own queue.
type ConstructionLine struct {
	// ID is the placed building's id: the button under the line opens its panel.
	ID         string
	Building   presentation.Named
	LotX, LotY int
	State      string
	FinishAt   time.Time
	Left       time.Duration
	// ProgressBPS, DoneMinutes, RequiredMinutes and LeftMinutes describe a
	// building raised by work (ADR 0037): ByWork is set and FinishAt/Left are
	// empty. The minutes are worker-minutes.
	ByWork          bool
	ProgressBPS     int64
	DoneMinutes     int64
	RequiredMinutes int64
	LeftMinutes     int64
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
	Building   presentation.Named
	LotX, LotY int
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
	// Own says the lot is the viewer's (the private lot grid).
	Own bool
	// Fits says whether the building currently being placed could go here
	// — settlementbuilding.CanPlace's own answer, precomputed by the use
	// case so this package never re-derives a placement rule (skills.go's
	// own convention, applied here).
	Fits bool
}

// LotGridView is a settlement's own placement grid for one building type.
type LotGridView struct {
	SettlementName string
	Building       presentation.Named
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

// The line-picking steps of a run of one-lot buildings on the grid.
const (
	LineStart = "line"
	LineEnd   = "end"
)

// MaterialLine is one component a building's own construction cost needs.
type MaterialLine struct {
	Component presentation.Named
	Quantity  int64
}

// LotConfirmView is the cost-and-time confirmation between choosing a lot
// and actually placing a building there.
type LotConfirmView struct {
	SettlementName string
	Building       presentation.Named
	X, Y           int
	Rotated        bool
	CostMoney      int64
	Materials      []MaterialLine
	BuildTime      time.Duration
	// AutoRoads is how many lots of road the game lays with the building to
	// connect it (0: it already touches the network).
	AutoRoads int
}

// The panel's modes: the plain panel, the upgrade disclosure, and the two
// confirmations of the destructive actions.
const (
	BuildingModeUpgrade  = "up"
	BuildingModeDemolish = "dm"
	BuildingModeCancel   = "cx"
)

// The kinds of panel a client draws. A kind a client does not know is drawn
// as "generic" (name, description, effects, upkeep).
const (
	BuildingKindRoad      = "road"
	BuildingKindCivicHall = "civic_hall"
	BuildingKindStorage   = "storage"
	BuildingKindSchool    = "school"
	BuildingKindSecurity  = "security"
	BuildingKindGeneric   = "generic"
)

// Where a placed building stands.
const (
	BuildingStateBuilding = "building"
	BuildingStateComplete = "complete"
)

// BuildingEffectLine is one number the building adds to the village, in the
// content's own unit (basis points, except housing_capacity).
type BuildingEffectLine struct {
	Target string
	Value  int64
}

// BuildingStockLine is one good the village store holds.
type BuildingStockLine struct {
	Item presentation.Named
	// Kind is "component" or "item".
	Kind string
	Qty  int64
}

// BuildingResearchLine is the research running now.
type BuildingResearchLine struct {
	Knowledge presentation.Named
	FinishAt  time.Time
	Left      time.Duration
}

// BuildingUpgradeLine is one building of the next tier of the role.
type BuildingUpgradeLine struct {
	Building  presentation.Named
	Tier      int
	CostMoney int64
	BuildTime time.Duration
	// Available is false while a prerequisite is missing; Missing names the
	// knowledge items that are.
	Available bool
	Missing   []presentation.Named
	// NeedsTier is the settlement tier ("town", "city") this building opens
	// at, when the settlement has not reached it yet; empty otherwise.
	NeedsTier string
}

// BuildingView is one placed building's own panel.
type BuildingView struct {
	ID       string
	Building presentation.Named
	Role     string
	Tier     int
	// Kind picks the panel a client draws (the BuildingKind constants).
	Kind       string
	State      string
	Mode       string
	X, Y, W, H int
	Rotated    bool
	Upkeep     int64
	Effects    []BuildingEffectLine
	// CanManage is true for the head: only the head cancels, demolishes and
	// upgrades.
	CanManage bool

	// Under construction.
	StartedAt time.Time
	FinishAt  time.Time
	Left      time.Duration
	// ProgressPercent is 0..100, already computed.
	ProgressPercent int

	// Storage: what the village store holds. The store has no capacity in
	// the content yet; none is invented here.
	Stock []BuildingStockLine
	// StockUsed and StockCapacity are the store's use and its room, in units.
	StockUsed, StockCapacity int64

	// School: the village's literacy, and whether diffusion is running.
	LiteracyPercent int
	Teaching        bool

	// Civic hall.
	Treasury   int64
	Population int
	Research   *BuildingResearchLine

	// Upgrades is set only in mode "up"; HasUpgrade tells the plain panel
	// whether the button is worth showing.
	HasUpgrade bool
	Upgrades   []BuildingUpgradeLine
}

// LotBatchLot is one lot of a batch.
type LotBatchLot struct{ X, Y int }

// LotBatchConfirmView is the total cost and time of a batch, between choosing
// the lots and starting them all.
type LotBatchConfirmView struct {
	SettlementName string
	Building       presentation.Named
	Lots           []LotBatchLot
	Count          int
	CostMoney      int64
	Materials      []MaterialLine
	BuildTime      time.Duration
}

// BatchLotFailure is one lot of a batch that could not be built, and why
// (a village refusal kind, the same set a single placement uses).
type BatchLotFailure struct {
	X, Y int
	Kind string
}

// GridGrowView is the price and yield of the next expansion.
type GridGrowView struct {
	SettlementName string
	Side, NewSide  int
	LotsGained     int
	// BuildableGained is how many of the new lots are dry buildable ground.
	BuildableGained int
	Price           int64
	Treasury        int64
}

// Refusals of the citizen loop; their text is citizen.refusal.<kind>.
const (
	CitizenLotTaken    = "citizen_lot_taken"
	CitizenLotLimit    = "citizen_lot_limit"
	CitizenZoning      = "citizen_zoning"
	CitizenNotOwner    = "citizen_not_owner"
	CitizenNoCash      = "citizen_no_cash"
	CitizenPrivateOnly = "citizen_private_only"
	CitizenLotPrivate  = "citizen_lot_private"
	CitizenRestWait    = "citizen_rest_wait"
	CitizenNoHouse     = "citizen_no_house"
	CitizenTermsRange  = "citizen_terms_range"
	CitizenNoDebt      = "citizen_no_debt"
	CitizenOff         = "citizen_off"
	CitizenNoLots      = "citizen_no_lots"
)

// Cell states of the land grid.
const (
	LandFree     = "free"
	LandMine     = "mine"
	LandTaken    = "taken"
	LandBuilding = "building"
	LandRoad     = "road"
	LandWater    = "water"
	LandSteep    = "steep"
)

// LandCell is one lot of the land grid. Owner is who holds a lot that is not
// the viewer's, for a member to read; Building is the code standing on it.
type LandCell struct {
	X, Y     int
	State    string
	Owner    string `json:"owner,omitempty"`
	Building string `json:"building,omitempty"`
}

// LandView is the village's land as a resident sees it.
type LandView struct {
	Village      string
	SettlementID string
	GridLots     int
	Rows         [][]LandCell
	// Price is what a free lot costs; Cash the viewer's own money.
	Price, Cash int64
	// Owned is how many lots the viewer holds, Max the most one may hold.
	Owned, Max int
	// CanBuy is whether the viewer may buy another lot now.
	CanBuy bool
	// FreeLots counts the lots on offer.
	FreeLots int
}

// LotBuyView is the confirm of a purchase and its result.
type LotBuyView struct {
	Village      string
	SettlementID string
	X, Y         int
	Price        int64
	// Cash is the buyer's money now (after the purchase, on the result).
	Cash     int64
	Treasury int64
}

// PrivateMaterial is one material a private building needs, and how it is met.
type PrivateMaterial struct {
	Component presentation.Named
	Need      int64
	// Have is what the builder carries; Buy is how many units are bought at
	// the reference price, BuyCost what they cost.
	Have, Buy, BuyCost int64
}

// PrivateLine is one building of the citizen catalogue the village can build now.
type PrivateLine struct {
	Building   presentation.Named
	Home       bool
	Class      string
	CostMoney  int64
	PermitFee  int64
	Materials  []PrivateMaterial
	BuildTime  time.Duration
	FootprintW int
	FootprintH int
	// Total is everything the builder pays in cash: cost, permit and bought
	// materials.
	Total      int64
	Affordable bool
}

// PrivateMenuView is the citizen catalogue.
type PrivateMenuView struct {
	Village      string
	SettlementID string
	Cash         int64
	OwnedLots    int
	FreeLots     int
	Lines        []PrivateLine
}

// PrivateLotsView is the grid a private building's lot is chosen from: a cell
// fits only where every lot of the footprint is the builder's own and free.
type PrivateLotsView struct {
	Village   string
	Building  presentation.Named
	CanRotate bool
	Rotated   bool
	GridLots  int
	Rows      [][]LotCell
}

// PrivateConfirmView is the bill of a private building before it is built.
type PrivateConfirmView struct {
	Village       string
	Building      presentation.Named
	X, Y          int
	Rotated       bool
	CostMoney     int64
	PermitFee     int64
	Materials     []PrivateMaterial
	MaterialsCost int64
	Total         int64
	Cash          int64
	BuildTime     time.Duration
}

// MineLot is one of the viewer's lots.
type MineLot struct {
	X, Y int
	// Building is the code standing on it, empty for a bare lot; State is
	// the building's state (under_construction, built), empty for a bare lot.
	Building string
	State    string
	FinishAt time.Time
	Left     time.Duration
}

// MineView is a resident's own property page.
type MineView struct {
	Village      string
	SettlementID string
	Cash         int64
	Lots         []MineLot
	// Home is the building the viewer lives in, nil before a house stands.
	Home *presentation.Named
	// CanRest says the viewer may rest at home now; RestWait is what is left
	// of the cool-down otherwise.
	CanRest  bool
	RestWait time.Duration
	// Assessed is the value the property tax is charged on, TaxBPS the rate
	// and TaxPerPeriod the tax one period charges.
	Assessed, TaxPerPeriod int64
	TaxBPS                 int
	// Debt is the unpaid tax and DebtPeriods the periods it covers.
	Debt        int64
	DebtPeriods int
	// Notice is a line about what just happened (rested, tax paid).
	Notice string
}

// TermsView is the head's levers over land and permits.
type TermsView struct {
	Village                        string
	SettlementID                   string
	LotPrice, LotPriceMin          int64
	LotPriceMax                    int64
	PermitFee, PermitFeeMax        int64
	TaxBPS, TaxBPSMax              int
	LotPresets, PermitPresets      []int64
	TaxPresets                     []int
	DefaultLotPrice, DefaultPermit int64
	DefaultTaxBPS                  int
}

// DonateView is the amounts screen, the confirm and the result.
type DonateView struct {
	Village string
	// Amount is what is being (or was) given; zero on the amounts screen.
	Amount int64
	// Presets are the amounts the buttons offer; Min and Max the bounds of
	// any gift (a client types its own).
	Presets  []int64
	Min, Max int64
	// Treasury is the village treasury (after the gift, on the result).
	Treasury int64
	// Cash is the donor's own cash (after the gift, on the result).
	Cash int64
	// SettlementID addresses the village for a client.
	SettlementID string
}

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
	Building presentation.Named
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
	Item    presentation.Named
	Options []presentation.Named
	// Have and Need are the stock and the quantity of a material.
	Have, Need int64
	// Makers are the workplaces that make the material; Price is what Support
	// asks per unit, zero when the village cannot buy it.
	Makers []VillageMaker
	Price  int64
}

// MaterialStockLine is one good the village holds.
type MaterialStockLine struct {
	Item presentation.Named
	Qty  int64
}

// MaterialMarketLine is one material Support sells the village.
type MaterialMarketLine struct {
	Item  presentation.Named
	Price int64
}

// MaterialBought is what a purchase that was just made came to.
type MaterialBought struct {
	Item  presentation.Named
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

// MaterialBuyView is the confirm before a purchase.
type MaterialBuyView struct {
	Village  string
	Item     presentation.Named
	Qty      int64
	Unit     int64
	Total    int64
	Treasury int64
	// Free is the room left in the stock.
	Free int64
}

// MaterialsConfirm is the second press's argument.
const MaterialsConfirm = "confirm"

// WorkplaceLine is one standing building a resident can work in.
type WorkplaceLine struct {
	ID       string
	Building presentation.Named
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
	Building presentation.Named
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
	Suggest []presentation.Named
	// Started is set on the screen shown right after a shift began.
	Started bool
	// Used and Capacity are the stock's units and room.
	Used, Capacity int64
}

// Labour refusal kinds (village.refusal.<kind>).
const (
	LaborNoJob         = "labor_no_job"
	LaborNotHere       = "labor_not_here"
	LaborFullyStaffed  = "labor_fully_staffed"
	LaborBudgetSpent   = "labor_budget_spent"
	LaborNotEmployer   = "labor_not_employer"
	LaborNoNPC         = "labor_no_npc"
	LaborWageTooLow    = "labor_wage_too_low"
	LaborEmployerBroke = "labor_employer_broke"
	LaborNoSite        = "labor_no_site"
)

// Market levels.
const (
	MarketSlack    = "slack"
	MarketBalanced = "balanced"
	MarketTight    = "tight"
	MarketShort    = "short"
)

// LaborMarketLine is the village's labour market at a glance.
type LaborMarketLine struct {
	// Housing is the homes' capacity (base plus buildings); Pool the NPC
	// labourers who live here, Available those not on a shift now.
	Housing, Pool, Available int64
	// Working is every shift in progress; Vacancies the shifts open jobs still
	// pay for.
	Working, Vacancies int64
	// TightnessBPS is demand over the labour force; Level names the band.
	TightnessBPS int64
	Level        string
	// NPCWage is what an NPC labourer asks for a shift now; MinWage the
	// statutory floor of the village's tier.
	NPCWage, MinWage int64
}

// LaborJobLine is one job on the board.
type LaborJobLine struct {
	ID         string
	BuildingID string
	Building   presentation.Named
	Kind       string
	// EmployerKind is "settlement" or "player"; Employer the player's name
	// (empty for the village).
	EmployerKind string
	Employer     string
	Wage         int64
	Left         int
	Total        int
	// ProgressBPS and LeftMinutes are the site's progress and the work left,
	// worker-minutes; zero for a production job.
	ProgressBPS int64
	LeftMinutes int64
	Workers     int
	NPCCrew     int
	// CanTake reports that the viewer may take this job now; Mine that the
	// viewer is the employer.
	CanTake bool
	Mine    bool
	// Points is the work one shift of the viewer adds.
	Points int64
}

// LaborBoardView is the hiring board.
type LaborBoardView struct {
	Village string
	Jobs    []LaborJobLine
	Market  LaborMarketLine
	// Working is the viewer's shift in progress, or nil.
	Working *LaborShiftLine
	// Resident reports that the viewer lives here.
	Resident bool
	// Sites are the buildings under construction with no open job, which the
	// employer can post one for.
	Sites []LaborSiteRef
}

// LaborSiteRef is a building under construction.
type LaborSiteRef struct {
	ID          string
	Building    presentation.Named
	ProgressBPS int64
}

// LaborShiftLine is a shift in progress.
type LaborShiftLine struct {
	ID        string
	Building  presentation.Named
	Kind      string
	Worker    string
	WorkerNPC bool
	Level     string
	FinishAt  time.Time
	Left      time.Duration
	Wage      int64
	Points    int64
}

// LaborPreset is a wage the employer may set: a share of the market wage.
type LaborPreset struct {
	Percent int
	Wage    int64
}

// LaborSiteView is the panel of one construction site.
type LaborSiteView struct {
	Village  string
	Building presentation.Named
	ID       string
	// Status is "building" or "complete".
	Status      string
	ProgressBPS int64
	// RequiredMinutes, DoneMinutes and LeftMinutes are worker-minutes.
	RequiredMinutes, DoneMinutes, LeftMinutes int64
	// Job is the open job of the site, nil when it has none.
	Job     *LaborJobLine
	Workers []LaborShiftLine
	Market  LaborMarketLine
	// CanWork: the viewer may work a shift here now (the wage they get and the
	// work it adds are WorkWage and WorkPoints); Working the viewer's shift.
	CanWork    bool
	WorkWage   int64
	WorkPoints int64
	Working    *LaborShiftLine
	// CanEmploy: the viewer is the employer. HirePresets are the crew sizes
	// offered, WagePresets the wages; NPCAvailable how many labourers are free.
	CanEmploy bool
	// CanPost: the site has no open job and the viewer may post one.
	CanPost      bool
	HirePresets  []int
	WagePresets  []LaborPreset
	NPCAvailable int64
	NPCWage      int64
	// Just says what the last press did: "worked", "hired", "wage", "posted".
	Just string
}

// LaborMineView is the viewer's own labour status.
type LaborMineView struct {
	Village string
	Shifts  int64
	Earned  int64
	// Level names the skill level, ProductivityBPS its productivity; NextLevel
	// and NextShifts the next rung and the shifts still needed for it (empty and
	// zero at the top).
	Level           string
	ProductivityBPS int64
	NextLevel       string
	NextShifts      int64
	Working         *LaborShiftLine
	Market          LaborMarketLine
}

// VillagePromoteConfirm is the "confirm" argument's value of the second press.
const VillagePromoteConfirm = ResidenceConfirm

// VillagePromotionTop is the refusal kind for a settlement with no tier above.
const VillagePromotionTop = "promotion_top"

// PromotionCriterionView is one goal and the settlement's progress on it.
// Current and Required are in the goal's own unit: people, basis points of
// literacy, buildings, things learned, minor units of money, or a tier for a
// role.
type PromotionCriterionView struct {
	Kind     string `json:"kind"`
	Role     string `json:"role,omitempty"`
	Current  int64  `json:"current"`
	Required int64  `json:"required"`
	Met      bool   `json:"met"`
}

// PromotionView is the way forward from a settlement's tier.
type PromotionView struct {
	Village string `json:"village"`
	// From and To are the tiers the step joins.
	From string `json:"from"`
	To   string `json:"to"`
	// Criteria are every goal with its progress; Met when all are.
	Criteria []PromotionCriterionView `json:"criteria"`
	Met      bool                     `json:"met"`
	// CanPromote is set when the viewer holds the settlement's head office;
	// only the head takes the step.
	CanPromote bool `json:"can_promote"`
	// Office is the office code the head holds once the step is taken.
	Office       string `json:"office,omitempty"`
	SettlementID string `json:"settlement_id,omitempty"`
}

// ResidenceConfirm is the "confirm" argument's value the second press carries.
const ResidenceConfirm = "confirm"

// ResidenceView is the confirm screen and the result of moving home.
type ResidenceView struct {
	// Leaving is true for settlement.leave.
	Leaving bool
	// Village is the village joined or left; Home the name of the city
	// returned to and HomeCode its content code, which a client words from the
	// catalogue.
	Village, Home string
	HomeCode      string
	// Cooldown is how long the player cannot move again.
	Cooldown time.Duration
	// Population is the village's residents after the move.
	Population int64
	// SettlementID addresses the village for a client (a group's own is
	// resolved from the chat).
	SettlementID string
}

// WhoLine is one player on the settlement screen.
type WhoLine struct {
	Name string
	// Activity is the presence activity code (idle, working, ...).
	Activity string
	// Place is the venue code the player stands at, "" for the centre.
	Place string
}

// SettlementWhoView is the group screen of who is around in a settlement.
type SettlementWhoView struct {
	Name string
	// Online are the players online now, the ones worth naming; Offline
	// counts everyone else, only counted.
	Online  []WhoLine
	Offline int
}
