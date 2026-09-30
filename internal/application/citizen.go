package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// The citizen loop (docs/adr/0033 sections 4.4 and 4.5, phase H1, migration
// 0058_citizen_loop): a resident buys a lot from their village, builds a
// PRIVATE building on it from their own cash, pays a permit and a property
// tax to the treasury, and lives in the house.
//
// Every lot is commons (the village's own) until a settlement_lots row says
// it is freehold. A private building is an ordinary settlement_buildings row
// plus a settlement_private_buildings row that names its owner. Reached
// through Tx.Citizens so every row commits with the money that moved for it.

// The ledger reasons of the citizen loop. Two credit the village treasury
// (a transfer from the citizen: nothing is created), two end money (the
// building's own cost and its bought materials leave the world into
// system_sink, the same shape as a village's construction cost), and the
// property tax is a transfer to the treasury. None of them is a faucet: no
// reason here credits a player.
const (
	ReasonSettlementLotSale     Reason = "settlement_lot_sale"
	ReasonSettlementPermitFee   Reason = "settlement_permit_fee"
	ReasonCitizenConstruction   Reason = "citizen_construction"
	ReasonCitizenMaterials      Reason = "citizen_materials"
	ReasonSettlementPropertyTax Reason = "settlement_property_tax"
)

// The references the citizen loop's ledger legs point at.
const (
	SettlementLotReference     = "settlement_lots"
	PrivateBuildingReference   = "settlement_private_buildings"
	SettlementPropertyTaxTable = "settlement_property_tax"
)

// Lot tenures.
const (
	TenureCommons  = "commons"
	TenureFreehold = "freehold"
	TenureLeased   = "leased"
)

// Citizen-loop sentinels.
var (
	// ErrLotTaken means the lot already has an owner: another buyer won the
	// race the in-memory check could not see.
	ErrLotTaken = errors.Sentinel(errors.CodeConflict, "application.ErrLotTaken", "that lot already has an owner")
	// ErrPrivateBuildingNotFound means no private building has that id.
	ErrPrivateBuildingNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrPrivateBuildingNotFound", "no such private building")
)

// SettlementLot is one settlement_lots row.
type SettlementLot struct {
	ID                  string
	SettlementID        string
	X, Y                int
	Tenure              string
	OwnerID             string
	Price               int64
	LedgerTransactionID string
	AcquiredAt          time.Time
}

// LotTerms are the head's levers. A Has flag says the lever was set (so a
// head can set the permit or the tax to zero); an unset lever is the
// configured default.
type LotTerms struct {
	LotPrice  int64
	PermitFee int64
	HasPermit bool
	TaxBPS    int
	HasTax    bool
}

// PrivateBuilding is one settlement_private_buildings row.
type PrivateBuilding struct {
	BuildingID          string
	SettlementID        string
	OwnerID             string
	PermitFee           int64
	ConstructionPaid    int64
	MaterialsPaid       int64
	AssessedValue       int64
	LedgerTransactionID string
	LastRestAt          *time.Time
	CreatedAt           time.Time
}

// PropertyTax is one settlement_property_tax row.
type PropertyTax struct {
	ID                  string
	SettlementID        string
	PlayerID            string
	PeriodNo            int64
	AssessedValue       int64
	TaxBPS              int
	Due                 int64
	PaidAt              *time.Time
	LedgerTransactionID string
	CreatedAt           time.Time
}

// Paid reports whether the period's tax has been paid.
func (t PropertyTax) Paid() bool { return t.PaidAt != nil }

// CitizenRepository is the transactional port of the citizen loop.
type CitizenRepository interface {
	// Lots lists every owned lot of a settlement.
	Lots(ctx context.Context, settlementID string) ([]SettlementLot, error)
	// InsertLot records a bought lot; ErrLotTaken if it has an owner.
	InsertLot(ctx context.Context, l SettlementLot) error

	// Terms reads the head's levers, the zero value when none were set.
	Terms(ctx context.Context, settlementID string) (LotTerms, error)
	// SetTerms writes the head's levers.
	SetTerms(ctx context.Context, settlementID string, t LotTerms, by string, at time.Time) error

	// RecordPrivateBuilding writes the ownership row of a new building.
	RecordPrivateBuilding(ctx context.Context, b PrivateBuilding) error
	// PrivateBuildings lists a settlement's private buildings.
	PrivateBuildings(ctx context.Context, settlementID string) ([]PrivateBuilding, error)
	// PrivateBuilding returns one by building id, or ErrPrivateBuildingNotFound.
	PrivateBuilding(ctx context.Context, buildingID string) (*PrivateBuilding, error)
	// MarkRested records when a house's owner last rested at home.
	MarkRested(ctx context.Context, buildingID string, at time.Time) error

	// InsertTax writes one period's tax row and reports whether it was
	// inserted; false means the period was already charged (the fence).
	InsertTax(ctx context.Context, t PropertyTax) (bool, error)
	// UnpaidTax lists a player's unpaid tax rows in a settlement, oldest first.
	UnpaidTax(ctx context.Context, settlementID, playerID string) ([]PropertyTax, error)
	// SettlementDebt lists every unpaid row of a settlement.
	SettlementDebt(ctx context.Context, settlementID string) ([]PropertyTax, error)
	// MarkTaxPaid records that a period's tax was paid by a ledger transaction.
	MarkTaxPaid(ctx context.Context, id, ledgerTransactionID string, at time.Time) error
}

// Assessment is what one owner's property is worth to the tax: their lots at
// their price and their buildings at their cost.
type Assessment struct {
	PlayerID string
	Value    int64
}

// AssessOwners sums, per owner, the assessed value of their lots and of their
// private buildings that still stand (alive says which), in order of first
// appearance.
func AssessOwners(lots []SettlementLot, buildings []PrivateBuilding, alive func(buildingID string) bool) []Assessment {
	index := map[string]int{}
	var out []Assessment
	add := func(player string, v int64) {
		i, ok := index[player]
		if !ok {
			i = len(out)
			index[player] = i
			out = append(out, Assessment{PlayerID: player})
		}
		out[i].Value += v
	}
	for _, l := range lots {
		add(l.OwnerID, l.Price)
	}
	for _, b := range buildings {
		if alive != nil && !alive(b.BuildingID) {
			continue
		}
		add(b.OwnerID, b.AssessedValue)
	}
	return out
}

// PropertyTaxDue is the tax a period charges on an assessed value: at least 1
// for property that is worth something under a tax above zero.
func PropertyTaxDue(assessed int64, bps int) int64 {
	if assessed <= 0 || bps <= 0 {
		return 0
	}
	due := assessed * int64(bps) / 10_000
	if due < 1 {
		due = 1
	}
	return due
}
