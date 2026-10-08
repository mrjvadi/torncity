package application

import (
	"context"
	"time"
)

// The head's tools over the reserve and the macro readings (roadmap 2.19 phase 4, docs/adr/0033 sections 6.3 to
// 6.8, 6.11 to 6.13, 7): the data they keep. The rules are in reserve.go.

// Statuses of an intervention, a withdrawal.
const (
	ReservePending   = "pending"
	ReserveDone      = "done"
	ReserveRefused   = "refused"
	ReserveCancelled = "cancelled"
)

// Intervention is the head's request to buy the money on the book with the reserve pot, or to sell the
// units the treasury holds (docs/adr/0033 6.7). It is public, and executes at the first period close after
// ExecuteAfter, so a head cannot front-run their own defence.
type Intervention struct {
	ID, SettlementID, Side string
	Units, Price           int64
	PostedBy               string
	PostedAt, ExecuteAfter time.Time
	Status                 string
	ExecutedAt             *time.Time
	OrderID                string
	// SUPUsed is the SUP the pot set aside for a purchase; Refusal why a request was refused at its time.
	SUPUsed int64
	Refusal string
}

// Withdrawal is an excess withdrawal announced to the public, executed after the Reserve Bank's notice.
type Withdrawal struct {
	ID, SettlementID string
	SUP              int64
	RequestedBy      string
	RequestedAt      time.Time
	ExecuteAfter     time.Time
	Status           string
	ExecutedAt       *time.Time
	TransactionID    string
	Refusal          string
}

// Claim is a holder's claim of a share of the pot in a wind-down.
type Claim struct {
	ID, SettlementID, PlayerID             string
	Units, SUP, PotBefore, ClaimableBefore int64
	SUPTransactionID, BurnTransactionID    string
	At                                     time.Time
}

// MacroRow is one period's macro reading of a money (migration 0131).
type MacroRow struct {
	SettlementID                                      string
	PeriodNo                                          int64
	SupplyUnits, Stabilisation, M, Y                  int64
	XRefPPM, Tradable, NonTradable, Price, PiLocalBPS int64
	CoverageBPS                                       *int64
	SupplyGrowthBPS, PotSUP, BasisSUP                 int64
	At                                                time.Time
}

// ReserveRepository is the port of the reserve tools, part of the currency repository.
type ReserveRepository interface {
	// ReserveBankJurisdiction is the id of the Reserve Bank's jurisdiction (its levers are set there), "" when the
	// content has none yet.
	ReserveBankJurisdiction(ctx context.Context) (string, error)

	InsertIntervention(ctx context.Context, i Intervention) error
	SaveIntervention(ctx context.Context, i Intervention) error
	// DueInterventions lists pending interventions whose time has come, locked, at most limit.
	DueInterventions(ctx context.Context, now time.Time, limit int) ([]Intervention, error)
	InterventionsOf(ctx context.Context, settlementID string, limit int) ([]Intervention, error)
	// InterventionUse sums what done interventions used since: SUP of purchases and units of sales.
	InterventionUse(ctx context.Context, settlementID string, since time.Time) (supBought, unitsSold int64, err error)
	// HasPendingIntervention says whether the player has a request waiting (the head's blackout on the book).
	HasPendingIntervention(ctx context.Context, settlementID, playerID string) (bool, error)
	// StabilisationUnits are the units the head's purchases brought in net of its sales: the intervention stock.
	StabilisationUnits(ctx context.Context, settlementID string) (int64, error)

	InsertWithdrawal(ctx context.Context, w Withdrawal) error
	SaveWithdrawal(ctx context.Context, w Withdrawal) error
	DueWithdrawals(ctx context.Context, now time.Time, limit int) ([]Withdrawal, error)
	WithdrawalsOf(ctx context.Context, settlementID string, limit int) ([]Withdrawal, error)
	// PendingWithdrawalSUP is what pending requests ask for in all.
	PendingWithdrawalSUP(ctx context.Context, settlementID string) (int64, error)
	// AddReleased raises the counter of SUP that left the pot for good (a withdrawal, a claim, the remainder).
	AddReleased(ctx context.Context, settlementID string, sup int64) error

	// BeginWindDown moves a chartered money to wind_down; false when it already is or was not chartered.
	BeginWindDown(ctx context.Context, settlementID, reason string, at, ends time.Time) (bool, error)
	// Retire moves a money in wind-down to retired; false when it was not winding down.
	Retire(ctx context.Context, settlementID string, at time.Time) (bool, error)
	// InWindDown lists the settlements whose money is winding down.
	InWindDown(ctx context.Context) ([]string, error)
	// ClaimableUnits are the units holders other than the treasury hold of the money.
	ClaimableUnits(ctx context.Context, settlementID, code string) (int64, error)
	InsertClaim(ctx context.Context, c Claim) error
	ClaimsOf(ctx context.Context, settlementID string, limit int) ([]Claim, error)

	// MacroRows lists a money's readings, newest first.
	MacroRows(ctx context.Context, settlementID string, limit int) ([]MacroRow, error)
	// RecordMacro writes a period's reading; false when the period already has one.
	RecordMacro(ctx context.Context, r MacroRow) (bool, error)
	// Output is the period's output in SUP: what employers paid in wages, valued in SUP.
	Output(ctx context.Context, settlementID string, from, to time.Time) (int64, error)
}
