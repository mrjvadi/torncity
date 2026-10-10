package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// The village economy's first loop (docs/adr/0033-village-first-progression-
// and-village-currencies.md section 4.1, phase E1; migration
// 0055_village_economy): a village buys basic materials from Support with SUP
// from its treasury, and its residents work shifts in workplaces that turn
// inputs into goods in the village stock and are paid from the treasury.
// Nothing is produced without a worker and residency alone pays nothing.

// The ledger reasons of the loop.
const (
	// ReasonSettlementMaterial pays Support's market for materials a village
	// bought (treasury to system_sink): a drain, recorded in
	// settlement_material_purchases.
	ReasonSettlementMaterial Reason = "settlement_material_purchase"
	// ReasonSettlementWage pays a resident for a finished shift from the
	// village treasury to their cash (a transfer: nothing is created or
	// destroyed), recorded in settlement_shifts.
	ReasonSettlementWage Reason = "settlement_wage"
)

// The references the loop's ledger legs point at.
const (
	SettlementMaterialReference = "settlement_material_purchases"
	SettlementShiftReference    = "settlement_shifts"
)

// The item journal's reference type for the village's own goods.
const (
	SettlementMaterialItemReference = "settlement_material_purchase"
	SettlementShiftItemReference    = "settlement_shift"
)

// Shift statuses.
const (
	SettlementShiftWorking = "working"
	SettlementShiftDone    = "done"
)

// Village economy sentinels.
var (
	// ErrWorkplaceFull means every worker place of the building is taken.
	ErrWorkplaceFull = errors.Sentinel(errors.CodeConflict,
		"application.ErrWorkplaceFull", "every place at this workplace is taken")
	// ErrAlreadyWorking means the resident already works a shift somewhere.
	ErrAlreadyWorking = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyWorking", "you already work a shift")
)

// SettlementMaterialPurchase is one buy of materials from Support.
type SettlementMaterialPurchase struct {
	ID                  string
	SettlementID        string
	Item                string
	Quantity            int64
	UnitPrice           int64
	Total               int64
	LedgerTransactionID string
	BoughtBy            string
	CreatedAt           time.Time
}

// SettlementShift is one resident's shift at a village workplace.
type SettlementShift struct {
	ID           string
	SettlementID string
	BuildingID   string
	PlayerID     string
	Status       string
	// Wage is what the shift was worth when it started; WagePaid what the
	// treasury could pay when it ended (never more than Wage).
	Wage, WagePaid int64
	// Produced and Consumed are the goods the shift moved, component code ->
	// quantity, fixed when it starts.
	Produced, Consumed map[string]int64
	// LandKind is what the shift did to the land (docs/adr/0065): "tree" (felled one), "rock" (worked a rock), "sapling" (planted
	// one) or "" (nothing); LandX and LandY name the lot and LandOwner the citizen whose lot it was (the yield goes to him and he
	// paid the wage).
	LandKind     string
	LandX, LandY int
	LandOwner    string
	// FarmCycle and FarmPhase say which crop and which work a farm shift did (sow, tend, harvest; docs/adr/0067); CustomFor is
	// the citizen whose own grain a mill shift ground (FarmPhase grind).
	FarmCycle, FarmPhase, CustomFor string
	// Recipe is the recipe the shift made ("" the station's standard shift; docs/adr/0068).
	Recipe string
	// Board is the food the hands of a citizen's own workplace ate out of the owner's home store (item reason board_eaten).
	Board        map[string]int64
	GameActionID string
	// Kind is "production" (the workplace loop) or "construction" (ADR 0037);
	// WorkerKind "player" or "npc" (then PlayerID is empty); JobID the hiring-
	// board job it was taken from; WorkPoints the work a construction shift
	// adds; PayerKind/PayerID who pays the wage; Fee the village's levy.
	// MealPoints is the food the worker ate when the shift started (0: exempt or hungry),
	// Fed whether they were fed, OutputBPS the productivity the output is scaled by
	// (rung x fed, ADR 0041 6.3): 10000 is the full base.
	// ConditionGain is the condition (bps) a repair shift restores when it finishes.
	ConditionGain           int64
	MealPoints              int64
	Fed                     bool
	OutputBPS               int64
	Kind, WorkerKind, JobID string
	WorkPoints              int64
	PayerKind, PayerID      string
	Fee                     int64
	StartedAt               time.Time
	FinishAt                time.Time
	FinishedAt              *time.Time
}

// SettlementEconomyRepository is the village economy's transactional port; it
// is reached through Tx.SettlementTreasury, whose repository implements it too.
type SettlementEconomyRepository interface {
	// RecordMaterialPurchase inserts one purchase row.
	RecordMaterialPurchase(ctx context.Context, p SettlementMaterialPurchase) error

	// StartShift inserts a working shift. It takes the building's row lock, so
	// two replicas cannot both fill its last place, and refuses
	// ErrWorkplaceFull when `workers` shifts already run there and
	// ErrAlreadyWorking when the player already works one.
	StartShift(ctx context.Context, s SettlementShift, workers int) error
	// Shift returns one shift, or ErrShiftNotFound.
	Shift(ctx context.Context, id string) (*SettlementShift, error)
	// FinishShift ends a working shift and records what it really produced (the
	// stock may have had less room than the shift was worth) and what was paid, and reports
	// whether this call did it (false: it was already done, so the caller must
	// not produce or pay again).
	FinishShift(ctx context.Context, id string, produced map[string]int64, wagePaid int64, ledgerTransactionID string, at time.Time) (bool, error)
	// PrivateTakings sums the finished shifts a citizen's workplace has worked for him since an instant: how many, what they
	// made, the wages paid and the levy (docs/adr/0066).
	PrivateTakings(ctx context.Context, buildingID string, since time.Time) (PrivateTakings, error)
	// FinishPrivateShift is FinishShift for a shift in a privately owned workplace: it also records the employer levy
	// (docs/adr/0066).
	FinishPrivateShift(ctx context.Context, id string, produced map[string]int64, wagePaid, fee int64, ledgerTransactionID string, at time.Time) (bool, error)
	// WorkingShifts lists a settlement's shifts in progress, oldest first.
	WorkingShifts(ctx context.Context, settlementID string) ([]SettlementShift, error)
	// PlayerShift is the player's shift in progress, or nil.
	PlayerShift(ctx context.Context, playerID string) (*SettlementShift, error)

	// Pot is the village kitchen's food points not yet eaten, under the settlement's row lock
	// (the row is made on first sight).
	Pot(ctx context.Context, settlementID string) (int64, error)
	// Eat takes `points` from the pot after opening the given food units into it: one row per
	// opening (the item journal's meal_eaten movements reference them). It refuses a draw the
	// pot cannot cover.
	Eat(ctx context.Context, settlementID, shiftID string, points int64, openings []MealOpening, at time.Time) error
	// Carry is a workplace's undelivered fractions (item -> ten-thousandths of a unit), under
	// the building's row lock; SetCarry writes them back.
	Carry(ctx context.Context, buildingID string) (map[string]int64, error)
	SetCarry(ctx context.Context, buildingID string, carry map[string]int64) error
}

// MealOpening is food opened from the stock into the kitchen pot: Units of Item, each worth
// PointsEach food points; ID is the reference of its item movement.
type MealOpening struct {
	ID         string
	Item       string
	Units      int64
	PointsEach int64
}

// ErrShiftNotFound means the shift named does not exist.
var ErrShiftNotFound = errors.Sentinel(errors.CodeNotFound,
	"application.ErrShiftNotFound", "no such shift")

// CharterBallotActionType is the game_actions.action_type of a charter ballot reaching its closing time (election, recall or amendment vote); CharterBallotReference its reference type.
const (
	CharterBallotActionType = "charter_ballot"
	CharterBallotReference  = "charter_ballot"
)

// SettlementWorkActionType is the game_actions.action_type of a shift ending
// (internal/workers/scheduler/routes.go carries the same literal).
const SettlementWorkActionType = "settlement_work"

// PrivateTakings is what a citizen's workplace has done over a span.
type PrivateTakings struct {
	Shifts   int64
	Produced map[string]int64
	Wages    int64
	Levy     int64
}
