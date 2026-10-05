package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// The labour market (docs/adr/0037-labor-market.md, migration 0059): a
// building under construction is raised by the work of shifts, citizens and the
// village post jobs on a hiring board, players and NPC labourers take them, and
// the wage is paid by whoever posted the job.

// The ledger reasons of the labour market.
const (
	// ReasonLaborEscrow sets a citizen employer's wage for a started shift
	// aside (their cash to their escrow): a transfer, recorded in
	// settlement_shifts (payer_kind player).
	ReasonLaborEscrow Reason = "labor_escrow"
	// ReasonLaborWage pays a player worker for a finished construction shift
	// from the employer (the village treasury, or the citizen's escrow), less the
	// village's levy on a citizen's wage, which goes to the treasury: a transfer.
	ReasonLaborWage Reason = "labor_wage"
	// ReasonLaborWageNPC pays an NPC labourer, who is not a player: the wage
	// leaves the economy to the sink (the labourer's own spending is outside the
	// game), less the levy, which goes to the treasury.
	ReasonLaborWageNPC Reason = "labor_wage_npc"
)

// The reference of the labour legs is the shift row.
const LaborShiftReference = SettlementShiftReference

// Job kinds, shift kinds, employers and workers.
const (
	LaborKindConstruction = "construction"
	LaborKindProduction   = "production"

	LaborEmployerSettlement = "settlement"
	LaborEmployerPlayer     = "player"

	LaborWorkerPlayer = "player"
	LaborWorkerNPC    = "npc"

	LaborJobOpen   = "open"
	LaborJobClosed = "closed"
)

// Labour sentinels.
var (
	// ErrJobNotFound means the job named does not exist.
	ErrJobNotFound = errors.Sentinel(errors.CodeNotFound, "application.ErrJobNotFound", "no such job")
	// ErrJobExists means the building already has an open job.
	ErrJobExists = errors.Sentinel(errors.CodeConflict, "application.ErrJobExists", "this site already has an open job")
)

// LaborJob is one posting on the hiring board.
type LaborJob struct {
	ID           string
	SettlementID string
	BuildingID   string
	Kind         string
	// EmployerKind is "settlement" (the treasury pays) or "player" (EmployerID
	// pays from their cash).
	EmployerKind string
	EmployerID   string
	// Wage is paid per finished shift to a player who takes the job.
	Wage int64
	// ShiftsTotal is the budget of shifts the employer will pay for and
	// ShiftsStarted how many have begun (players and NPCs alike).
	ShiftsTotal, ShiftsStarted int
	// NPCCrew is how many NPC labourers the employer keeps on the site.
	NPCCrew int
	// Priority is 1 (first) to 4 (last): when the pool is short the crews of the lower
	// numbers are filled first. Paused is the reason the crew stopped (a shift could not
	// start: no_staff, no_input, storage_full, employer_broke, budget_spent); empty when
	// it is running. A refilled crew clears it.
	Priority  int
	Paused    string
	Status    string
	CreatedBy string
	CreatedAt time.Time
	ClosedAt  *time.Time
}

// Left is the shifts the budget still pays for.
func (j LaborJob) Left() int { return j.ShiftsTotal - j.ShiftsStarted }

// LaborWorker is a player's experience.
type LaborWorker struct {
	PlayerID string
	Shifts   int64
	Earned   int64
}

// LaborRepository is the labour market's transactional port; it is reached
// through Tx.SettlementTreasury(), whose repository implements it too.
type LaborRepository interface {
	// PostJob writes an open job; ErrJobExists when the building has one.
	PostJob(ctx context.Context, j LaborJob) error
	// LockJob reads a job under a row lock: shifts of one job queue here, no
	// other row is contended. ErrJobNotFound when it does not exist.
	LockJob(ctx context.Context, id string) (*LaborJob, error)
	// Job reads a job without a lock.
	Job(ctx context.Context, id string) (*LaborJob, error)
	// OpenJobs lists a settlement's open jobs, oldest first.
	OpenJobs(ctx context.Context, settlementID string) ([]LaborJob, error)
	// JobOfBuilding is the building's open job, or nil.
	JobOfBuilding(ctx context.Context, buildingID string) (*LaborJob, error)
	// PauseJob records why a job's crew stopped; an empty reason clears it.
	PauseJob(ctx context.Context, jobID, reason string) error
	// NPCShiftsSince counts the NPC shifts a building started since an instant.
	NPCShiftsSince(ctx context.Context, buildingID string, since time.Time) (int64, error)
	// CountStarted adds one to the shifts a job has started.
	CountStarted(ctx context.Context, jobID string) error
	// UpdateJob sets an open job's wage, budget and crew.
	UpdateJob(ctx context.Context, id string, wage int64, shiftsTotal, npcCrew int) error
	// CloseJobs closes the open job of a building (nil when none).
	CloseJobOfBuilding(ctx context.Context, buildingID string, at time.Time) error
	// CloseJob closes one job.
	CloseJob(ctx context.Context, id string, at time.Time) error

	// StartLaborShift inserts a working construction (or job-taken production)
	// shift. Its uniqueness per player is the same index as StartShift's.
	StartLaborShift(ctx context.Context, s SettlementShift) error
	// FinishLaborShift ends a working shift exactly once, recording the wage
	// paid and the levy taken; false: it was already done.
	FinishLaborShift(ctx context.Context, id string, wagePaid, fee int64, ledgerTransactionID string, at time.Time) (bool, error)
	// SiteShifts lists a building's construction shifts in progress.
	SiteShifts(ctx context.Context, buildingID string) ([]SettlementShift, error)
	// WorkingCount is how many shifts of a settlement are in progress, and how
	// many of them are NPC labourers.
	WorkingCount(ctx context.Context, settlementID string) (all, npc int64, err error)
	// Vacancies is how many shifts open jobs of a settlement still pay for.
	Vacancies(ctx context.Context, settlementID string) (int64, error)

	// AddWork adds points to a building under construction and reports the
	// totals; never past what is required; ok false when the building is no
	// longer under construction.
	AddWork(ctx context.Context, buildingID string, points int64) (done, required int64, ok bool, err error)
	// Worker reads a player's experience (zero when none).
	Worker(ctx context.Context, playerID string) (LaborWorker, error)
	// RecordWorked adds a finished shift and what it earned to the player.
	RecordWorked(ctx context.Context, playerID string, earned int64, at time.Time) error
}
