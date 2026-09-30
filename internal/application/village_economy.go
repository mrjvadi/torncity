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
	GameActionID       string
	StartedAt          time.Time
	FinishAt           time.Time
	FinishedAt         *time.Time
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
	// WorkingShifts lists a settlement's shifts in progress, oldest first.
	WorkingShifts(ctx context.Context, settlementID string) ([]SettlementShift, error)
	// PlayerShift is the player's shift in progress, or nil.
	PlayerShift(ctx context.Context, playerID string) (*SettlementShift, error)
}

// ErrShiftNotFound means the shift named does not exist.
var ErrShiftNotFound = errors.Sentinel(errors.CodeNotFound,
	"application.ErrShiftNotFound", "no such shift")

// SettlementWorkActionType is the game_actions.action_type of a shift ending
// (internal/workers/scheduler/routes.go carries the same literal).
const SettlementWorkActionType = "settlement_work"
