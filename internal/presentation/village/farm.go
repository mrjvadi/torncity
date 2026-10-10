package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The farm cycle, the water works and the mill (docs/adr/0067). Data only: the Telegram and the web layers word it.

// Refusals of the farms, the mill and the pasture.
const (
	// VillageNeedsNear: the building must stand near something it does not have in reach (a canal near a river, a farm
	// near its water work).
	VillageNeedsNear = "needs_near"
	// FarmIdle: a farm of the cycle has no crop in the ground; the head (or the owner) orders the sowing.
	FarmIdle = "farm_idle"
	// FarmWaiting: the crop is growing and every tending shift is done; the work waits for the harvest.
	FarmWaiting = "farm_waiting"
	// FarmBusy: a sowing is ordered over a crop that is still in the ground.
	FarmBusy = "farm_busy"
	// FarmLegacy: the farm still works its flat shift until the rule date plus its grace; no sowing is ordered yet.
	FarmLegacy = "farm_legacy"
	// FarmNotFarm: the building is not a farm of the cycle.
	FarmNotFarm = "farm_not_farm"
	// PastureNoGrazing: fewer open lots than a herd needs lie round the pasture.
	PastureNoGrazing = "no_grazing"
	// MillTollRange: the toll asked is outside the statute's range.
	MillTollRange = "toll_range"
	// MillNoGrain: the citizen has no wheat in his store to grind.
	MillNoGrain = "mill_no_grain"
	// MillNotMill: the building does not grind grain.
	MillNotMill = "mill_not_mill"
)

// Screens and addresses of the farm and mill commands.
const (
	ScreenFarmSow  = "farm_sow"
	AddrFarmSow    = "settlement:farm.sow"
	ScreenMillToll = "mill_toll"
	AddrMillToll   = "settlement:mill.toll"
	AddrMillGrind  = "settlement:mill.grind"
)

// FarmLine is the crop of one farm: where it stands, what it asks, what it will give.
type FarmLine struct {
	// Stage is idle, sowing, growing, ripe, overripe, harvest, harvested or rotted.
	Stage string `json:"stage"`
	// Rainfed says the farm draws from no water work.
	Rainfed bool `json:"rainfed,omitempty"`
	// Legacy says the farm still works its flat shift (the rule date has not passed or its grace has not ended);
	// LegacyUntil is when the cycle takes over.
	Legacy      bool       `json:"legacy,omitempty"`
	LegacyUntil *time.Time `json:"legacy_until,omitempty"`
	// The work done and still to do in the stage: sowing shifts begun of SowNeed, tending shifts of TendMax, harvest shifts.
	SowDone     int `json:"sow_done"`
	SowNeed     int `json:"sow_need"`
	Tended      int `json:"tended"`
	TendMax     int `json:"tend_max"`
	HarvestDone int `json:"harvest_done"`
	HarvestNeed int `json:"harvest_need"`
	// RipeAt and SpoilAt are when the crop is ripe and when it starts to spoil (nil while it is being sown).
	RipeAt  *time.Time `json:"ripe_at,omitempty"`
	SpoilAt *time.Time `json:"spoil_at,omitempty"`
	// Seed is the wheat the sowing takes and SeedHave what the store holds.
	Seed     int64 `json:"seed"`
	SeedHave int64 `json:"seed_have"`
	// Expected is the harvest now, in wheat, and the factors behind it in basis points.
	Expected int64      `json:"expected"`
	Factors  FarmFactor `json:"factors"`
	// Water says what the farm draws from; nil for a rain-fed farm.
	Water *FarmWater `json:"water,omitempty"`
	// CanSow says the viewer may order the sowing now.
	CanSow bool `json:"can_sow,omitempty"`
}

// FarmFactor are the factors of the yield in basis points.
type FarmFactor struct {
	Soil    int64 `json:"soil"`
	Water   int64 `json:"water"`
	Tending int64 `json:"tending"`
	// Loss is the share spoiled past the window.
	Loss int64 `json:"loss,omitempty"`
}

// FarmWater is the water a farm draws from: its work, whether the work is in order and why not.
type FarmWater struct {
	Work *presentation.Named `json:"work,omitempty"`
	// Served says a work of the branch within reach serves this farm; Open that its water master is on duty today;
	// ConditionBPS the work's condition. Reason is no_work, not_served, no_master or worn when the water is short.
	Served       bool   `json:"served"`
	Open         bool   `json:"open"`
	ConditionBPS int64  `json:"condition_bps"`
	Reason       string `json:"reason,omitempty"`
}

// FarmSowView is the answer of settlement.farm.sow: the farm and its crop after the order.
type FarmSowView struct {
	Village string
	Farm    presentation.Named
	Line    FarmLine
}

// MillLine is what a mill adds to a workplace: the toll the settlement's statute asks and what the viewer may grind.
type MillLine struct {
	TollBPS int64 `json:"toll_bps"`
	MinBPS  int64 `json:"min_bps"`
	MaxBPS  int64 `json:"max_bps"`
	// CanSet says the viewer may change the statute.
	CanSet bool `json:"can_set,omitempty"`
	// Batch is the wheat one shift grinds; Have the wheat in the viewer's own store; TollUnits the flour a batch of his
	// own grain pays in toll.
	Batch     int64 `json:"batch"`
	Have      int64 `json:"have"`
	TollUnits int64 `json:"toll_units"`
}

// MillTollView is the answer of settlement.mill.toll.
type MillTollView struct {
	Village string
	TollBPS int64
	MinBPS  int64
	MaxBPS  int64
}

// WaterWork is a water work as the building panel shows it: the farms it serves and whether its master is on duty.
type WaterWork struct {
	// Open says the water master is on duty today; ConditionBPS the work's condition.
	Open         bool  `json:"open"`
	ConditionBPS int64 `json:"condition_bps"`
	// Serves are the farms the work waters (at most the content's number).
	Serves []presentation.Named `json:"serves,omitempty"`
}

// GrazingLine is the open land a pasture has: Open lots of the Need it asks for, within Radius lots.
type GrazingLine struct {
	Open   int `json:"open"`
	Need   int `json:"need"`
	Radius int `json:"radius"`
}

var (
	screenFarmSow  = presentation.Define[FarmSowView](ScreenFarmSow, "village")
	screenMillToll = presentation.Define[MillTollView](ScreenMillToll, "village")
)

// FarmSow is the screen of a sowing order.
func FarmSow(c presentation.Ctx, v FarmSowView) *presentation.Response {
	return screenFarmSow.Response(c.Lang, v, back(AddrWork))
}

// MillToll is the screen of the miller's toll statute.
func MillToll(c presentation.Ctx, v MillTollView) *presentation.Response {
	return screenMillToll.Response(c.Lang, v, back(AddrWork))
}
