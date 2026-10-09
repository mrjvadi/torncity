package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// The function-and-content model of a lot (docs/adr/0045 phase B1, section 3; migration 0133). One table,
// building_functions, holds what ADR 0035 called a use and ADR 0045 a function: one row per building. Its children
// are the modules inside, the generated look, the owner's orders and the saved templates.

// Building reference kinds of a function row.
const (
	BuildingRefSettlement = "settlement_building"
	BuildingRefProperty   = "property"
)

// Function statuses (content.FunctionStatuses).
const (
	FunctionActive = "active"
	FunctionFitout = "fitout"
)

// Work (order) statuses.
const (
	WorkOpen = "open"
	WorkDone = "done"
)

// LaborKindFitout is the job and shift kind of an owner's order: modules, a level, storeys or a conversion are
// raised by shifts of the hiring board, like a building under construction.
const LaborKindFitout = "fitout"

// Ledger reason and references of the function model.
const (
	// ReasonUseChangeFee is the fee a lot owner pays the treasury when the lot changes function, either way
	// (docs/adr/0044 section 5.5, lever settlement.use_change_fee_bps): a transfer, once per conversion.
	ReasonUseChangeFee Reason = "use_change_fee"
	// BuildingWorkReference is the reference_type of an order's movements and payments; the id is the order.
	BuildingWorkReference = "building_works"
)

// Item reasons of the function model.
const (
	// ItemFitoutMaterials is an end: the materials of an order, taken from the owner's home store when the order is
	// placed; the movement's reference is the order.
	ItemFitoutMaterials ItemReason = "fitout_materials"
	// ItemSalvage is an origin, bounded below what was consumed: a share of the materials of a module taken out, put
	// in the owner's holding slot (docs/adr/0045 3.2; config building.salvage_bps).
	ItemSalvage ItemReason = "salvage"
)

// Refusals of the function model, coded.
var (
	ErrFunctionNotFound = errors.Sentinel(errors.CodeNotFound, "application.ErrFunctionNotFound", "that building has no function yet")
	ErrWorkOpen         = errors.Sentinel(errors.CodeConflict, "application.ErrWorkOpen", "the building already has an order being built")
	ErrTemplateNotFound = errors.Sentinel(errors.CodeNotFound, "application.ErrTemplateNotFound", "no such template")
)

// BuildingFunction is one building_functions row with its modules.
type BuildingFunction struct {
	RefKind      string
	RefID        string
	SettlementID string
	Function     string
	Level        int
	Storeys      int
	W, D         int
	Status       string
	Market       string
	Since        time.Time
	PermitID     string
	CompanyID    string
	OpID         string
	ConditionBPS int
	// Modules are the counts inside, the ones the level includes too.
	Modules map[string]int
}

// BuildingLook is a building's stored look.
type BuildingLook struct {
	RefKind    string
	RefID      string
	Seed       uint32
	Reroll     int
	Descriptor []byte
	Version    int
	UpdatedAt  time.Time
}

// BuildingWork is an owner's order.
type BuildingWork struct {
	ID            string
	SettlementID  string
	BuildingID    string
	Status        string
	Adds          map[string]int
	LevelTo       int
	StoreysTo     int
	ConvertTo     string
	ShiftsTotal   int
	WorkRequired  int64
	WorkDone      int64
	CostMoney     int64
	CostMaterials map[string]int64
	FeePaid       int64
	// FeeTx is the ledger transaction of the use-change fee (or of its local payment), empty when none was paid.
	FeeTx     string
	OrderedBy string
	OrderedAt     time.Time
	DoneAt        *time.Time
}

// FunctionConversion is one change of use, kept for good.
type FunctionConversion struct {
	ID                  string
	SettlementID        string
	BuildingID          string
	From, To            string
	Fee                 int64
	LedgerTransactionID string
	WorkID              string
	At                  time.Time
}

// PlanTemplate is a saved layout.
type PlanTemplate struct {
	ID        string
	OwnerID   string
	Name      string
	Code      string
	Function  string
	Level     int
	Storeys   int
	Modules   map[string]int
	CreatedAt time.Time
}

// BuildingFunctionRepository is the port of the function model; it is reached through
// Tx.SettlementBuildings(), whose repository implements it too.
type BuildingFunctionRepository interface {
	// FunctionOf reads a building's function with its modules, nil when it has none.
	FunctionOf(ctx context.Context, kind, id string) (*BuildingFunction, error)
	// FunctionsOfSettlement reads every function row of a settlement with its modules.
	FunctionsOfSettlement(ctx context.Context, settlementID string) ([]BuildingFunction, error)
	// FunctionsOfBuildings reads the function rows of some buildings (by id) with their modules.
	FunctionsOfBuildings(ctx context.Context, kind string, ids []string) ([]BuildingFunction, error)
	// EnsureFunction writes a function row and its modules when the building has none (ON CONFLICT DO NOTHING):
	// idempotent under any number of replicas. It reports whether this call wrote it.
	EnsureFunction(ctx context.Context, f BuildingFunction) (bool, error)
	// SetFunction rewrites a building's function row and replaces its modules (a conversion, a level, a storey).
	SetFunction(ctx context.Context, f BuildingFunction) error
	// SetTypeCode changes the catalogue building a lot stands as (a level reached, a conversion done): the old
	// type_code stays the key the map and the old effects read until ADR 0044 G7.
	SetTypeCode(ctx context.Context, buildingID, typeCode string) error
	// SetModuleCount sets one module's count (0 removes it).
	SetModuleCount(ctx context.Context, kind, id, module string, count int) error

	// Look reads a building's stored look, nil when none.
	Look(ctx context.Context, kind, id string) (*BuildingLook, error)
	// SaveLook writes (or replaces) a building's look.
	SaveLook(ctx context.Context, l BuildingLook) error
	// NeighbourLooks lists the descriptors (JSON) of the looks stored for the settlement's other buildings.
	NeighbourLooks(ctx context.Context, settlementID, exceptID string) ([][]byte, error)

	// OpenWork reads a building's open order, nil when none. LockOpenWork does so under a row lock.
	OpenWork(ctx context.Context, buildingID string) (*BuildingWork, error)
	LockOpenWork(ctx context.Context, buildingID string) (*BuildingWork, error)
	// Works lists a settlement's orders, newest first.
	WorksOf(ctx context.Context, buildingID string, limit int) ([]BuildingWork, error)
	// InsertWork writes a new order; ErrWorkOpen when the building has one open.
	InsertWork(ctx context.Context, w BuildingWork) error
	// AddWorkDone adds points to an open order, never past what it requires, and reports the totals; ok false when
	// the order is no longer open.
	AddWorkDone(ctx context.Context, workID string, points int64) (done, required int64, ok bool, err error)
	// CompleteWork closes an open order exactly once; false when it was already closed.
	CompleteWork(ctx context.Context, workID string, at time.Time) (bool, error)
	// RecordConversion appends a change of use.
	RecordConversion(ctx context.Context, c FunctionConversion) error

	// Templates lists a player's saved templates; Template reads one by id or by share code.
	Templates(ctx context.Context, ownerID string) ([]PlanTemplate, error)
	TemplateByCode(ctx context.Context, code string) (*PlanTemplate, error)
	TemplateByID(ctx context.Context, id string) (*PlanTemplate, error)
	InsertTemplate(ctx context.Context, t PlanTemplate) error
	DeleteTemplate(ctx context.Context, id, ownerID string) (bool, error)

	// OwnStallSlots is how many counters of their own a player's active stalls give on a settlement's book: the
	// shelves of every finished building whose function is a stall.
	OwnStallSlots(ctx context.Context, settlementID, playerID string) (int64, error)
}
