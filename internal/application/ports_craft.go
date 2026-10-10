package application

import (
	"context"
	"time"
)

// The port of crafting at home (migration 0145; docs/adr/0068): a citizen's timed craft at his home station. A craft job takes
// the goods out of his home store when it starts and puts the made goods into it when it ends, exactly once.

// CraftRules are the settings of the tool tiers and of crafting (config settlement.crafting_*).
type CraftRules struct {
	// RuleAt is when the tool tiers began to count: a worker's best tool tier lifts or lowers his output from RuleAt plus
	// GraceDays on; before that every worker with `tools` counts as having the tool the work needs, as it always did. The
	// zero value leaves the tiers off. Home crafting needs no rule date: it is new.
	RuleAt    time.Time
	GraceDays int64
}

// Enabled reports whether the tool tiers are configured.
func (r CraftRules) Enabled() bool { return !r.RuleAt.IsZero() }

// GraceUntil is when the tier multipliers begin to count.
func (r CraftRules) GraceUntil() time.Time {
	if r.RuleAt.IsZero() {
		return time.Time{}
	}
	return r.RuleAt.AddDate(0, 0, int(max(r.GraceDays, 0)))
}

// TiersOn reports whether the tier multipliers count at now.
func (r CraftRules) TiersOn(now time.Time) bool {
	return r.Enabled() && !now.Before(r.GraceUntil())
}

// CraftJob is a timed craft.
type CraftJob struct {
	ID, SettlementID, PlayerID, BuildingID, Recipe string
	Batches                                        int
	// Consumed is what left the home store at the start; Planned the goods the batches would make at the station's full
	// yield; Made what came in at the end.
	Consumed, Planned, Made map[string]int64
	OutputBPS               int
	ToolItem                string
	Status                  string
	GameActionID            string
	StartedAt, FinishAt     time.Time
	FinishedAt              *time.Time
}

// What the craft jobs are called in the item journal and the game clock.
const (
	CraftJobItemReference = "craft_job"
	CraftDoneActionType   = "craft_done"
	CraftJobWorking       = "working"
	CraftJobDone          = "done"
)

// CraftRepository persists the craft jobs. Reach it through Tx.Craft.
type CraftRepository interface {
	// Start stores a working job.
	Start(ctx context.Context, j CraftJob) error
	// Job reads a job.
	Job(ctx context.Context, id string) (*CraftJob, error)
	// Finish ends a working job exactly once (false when it was already done).
	Finish(ctx context.Context, id string, made map[string]int64, at time.Time) (bool, error)
	// Running lists the player's working jobs, oldest first.
	Running(ctx context.Context, playerID string) ([]CraftJob, error)
	// Lock serialises one player's craft starts.
	Lock(ctx context.Context, playerID string) error
}
