package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// Mining (docs/adr/0075): the deposit under the settlement, its mineheads (modules, levels, crew, what limits them), the works in
// progress and what could be ordered. Nothing here is worded or laid out.

// The mining screen.
const (
	ScreenMining = "settlement_mining"
	AddrMining   = "settlement:mining"
)

// The acts of the mining screen, the first argument of settlement.mining.
const (
	// MiningActionSurvey surveys the deposit (mine.run). MiningActionWorks orders a module of a minehead: code is the module, N the
	// minehead's building id, the metres of an adit are the next argument.
	MiningActionSurvey = "survey"
	MiningActionWorks  = "works"
)

// The notices and refusals of the mining screen.
const (
	MineNoticeSurveyed = "mine_surveyed"
	MineNoticeOrdered  = "mine_works_ordered"

	MineNotYet      = "mine_not_yet"
	MineNoDeposit   = "mine_no_deposit"
	MineNoOre       = "mine_no_ore"
	MineClosed      = "mine_closed"
	MineNoHoist     = "mine_no_hoist"
	MineLimited     = "mine_limited"
	MineRoleFull    = "mine_role_full"
	MineBadRole     = "mine_bad_role"
	MineNothingToDo = "mine_nothing_to_do"
	MineBadArgs     = "mine_bad_args"
	MineNoMine      = "mine_no_mine"
	MineSurveyed    = "mine_already_surveyed"
	MineModuleHave  = "mine_module_have"
	MineModuleOrder = "mine_module_order"
	MineNoOutlet    = "mine_no_outlet"
	MineNoMoney     = "mine_no_money"
	MineNoSurveyor  = "mine_no_surveyor"
)

// Limits of a mine's output (docs/adr/0075 5.4), the value of MineLine.Limit and of a refusal's subject.
const (
	MineLimitFace  = "face"
	MineLimitHoist = "hoist"
	MineLimitHaul  = "haul"
)

// MineDepositLine is the deposit under the settlement's cell. Before a survey only the resource is known ("probable ore"); after it
// the grade band, the estimate and what is left in each depth band.
type MineDepositLine struct {
	Resource presentation.Named
	Surveyed bool
	// The next fields are zero until the settlement has surveyed the deposit.
	GradePermille int32
	Band          string
	// CapacityEstimate is the ore the deposit holds in game units (the survey's estimate); TakenUnits what was taken so far.
	CapacityEstimate, TakenUnits int64
	// LeftBPS is the share of what a method reaches that is still there, for the surface, the first and the second shaft level.
	LeftBPS [3]int64
	// Item is the ore item the deposit's band brings up.
	Item presentation.Named
}

// MineLevelLine is a shaft level.
type MineLevelLine struct {
	Depth                  int
	DepthM                 int64
	ConditionBPS, WaterBPS int64
	// Status is ok, needs_repair or flooded; Drained says an adit built so far drains the level by gravity.
	Status  string
	Drained bool
	LeftBPS int64
}

// MineCrewLine is a post of a minehead.
type MineCrewLine struct {
	Role           presentation.Named
	Slots, Working int
}

// MineLine is a minehead.
type MineLine struct {
	Building   presentation.Named
	BuildingID string
	// Modules: the shaft levels opened, the hoist (-1 none, 0 windlass, 1 gin), the pump (-1 none, 1 gin), the bin, the ventilation shaft
	// and the metres of adit; FallM the drop to the nearest lower water and AditNeedM the adit a level at 30 m asks.
	Shafts, Hoist, Pump int
	OreBin, Vent        bool
	AditM, FallM        int64
	AditNeedM           [2]int64
	Levels              []MineLevelLine
	Crew                []MineCrewLine
	// The kilograms a half-hour shift can move: what the face breaks, what the hoist lifts, what the haulers carry, and which of the
	// three limits the output (empty when the mine cannot work).
	FaceKg, HoistKg, HaulKg, OutputKg int64
	Limit                             string
	// Reasons are the codes of what keeps the mine from working: no_deposit, no_ore, closed, no_hoist, no_haul, no_staff.
	Reasons []string
}

// MineWorkLine is a works order in progress.
type MineWorkLine struct {
	ID       string
	Module   presentation.Named
	Detail   int64
	DueAt    time.Time
	Left     time.Duration
	Building string
}

// MineModuleLine is a module a minehead could be ordered.
type MineModuleLine struct {
	Module presentation.Named
	// Have and Max count the modules of the kind built; Cost is for one (for each 50 m of adit); Wage the builders' wage.
	Have, Max int
	Cost      []MaterialLine
	Wage      int64
	// Unit is how much of the module one order buys (50 m of adit, else 1).
	Unit int64
	// Missing lists the research or the module the order lacks (empty when it can be ordered).
	Missing  []presentation.Named
	CanOrder bool
}

// MiningView is the mining screen.
type MiningView struct {
	Village string
	// Open says mining runs (the rule date has passed); StartsAt the rule date and PitsFrom the day the pits begin to draw on their deposit.
	Open     bool
	StartsAt *time.Time
	PitsFrom *time.Time
	// MayRun says the viewer holds mine.run.
	MayRun   bool
	Deposits []MineDepositLine
	Mines    []MineLine
	Works    []MineWorkLine
	// Modules is what could be ordered at the first minehead.
	Modules []MineModuleLine
	Notice  string
}

var screenMining = presentation.Define[MiningView](ScreenMining, "village")

// Mining is the mining screen.
func Mining(c presentation.Ctx, v MiningView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrMining)}
	if v.MayRun && v.Open && hasUnsurveyed(v.Deposits) {
		a = append(a, act(AddrMining, MiningActionSurvey).Named("mining.survey"))
	}
	if v.MayRun && v.Open {
		for _, m := range v.Mines {
			for _, mod := range v.Modules {
				if mod.CanOrder {
					a = append(a, act(AddrMining, MiningActionWorks, mod.Module.Code, m.BuildingID).Named("mining.works").About(mod.Module.Code+"/"+m.BuildingID))
				}
			}
		}
	}
	return screenMining.Response(c.Lang, v, a...)
}

func hasUnsurveyed(d []MineDepositLine) bool {
	for _, x := range d {
		if !x.Surveyed {
			return true
		}
	}
	return false
}
