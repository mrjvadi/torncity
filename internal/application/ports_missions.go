package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of missions (migrations/0023,
// docs/adr/0023-health-missions-factions.md): the missions players took from
// a city's boards, their progress and reward, and the inbox that moves them
// once per event. The rules are internal/domain/mission; the missions are
// content (missions.yml).

// MissionReference is the reference_type of a mission's reward: its ledger
// transaction and the goods it gave.
const MissionReference = "mission_assignments"

// Mission statuses, exactly as mission_assignments_status_check spells them.
const (
	MissionActive    = "active"
	MissionCompleted = "completed"
	MissionAbandoned = "abandoned"
	MissionExpired   = "expired"
)

// ItemMissionDelivery is goods handed in at a board for a mission: units
// leaving the world. The mission is the reference.
const ItemMissionDelivery ItemReason = "mission_delivery"

func init() { itemReasons[ItemMissionDelivery] = true }

// MissionAssignment is a mission_assignments row.
type MissionAssignment struct {
	ID       string
	No       int64
	PlayerID string
	Mission  string
	CityID   string
	Board    string
	Status   string
	// Progress is one count per objective, in the content's order.
	Progress   []int64
	AcceptedAt time.Time
	// ExpiresAt is when a time-limited mission runs out; zero for none.
	ExpiresAt time.Time
	EndedAt   *time.Time
	// What the completion paid, and what the caps withheld.
	RewardCash     int64
	RewardWithheld int64
	RewardXP       int64
	RewardGrantID  string
	ContentVersion int
}

// MissionRepository persists missions. Reach it through Tx.Missions, so a
// mission moves with the goods and money its completion moved.
type MissionRepository interface {
	// Lock serialises every change to one player's missions: taken before
	// reading them with a view to changing them.
	Lock(ctx context.Context, playerID string) error
	// LockDay serialises every mission payout, so the economy's daily cap is
	// read and spent by one completion at a time.
	LockDay(ctx context.Context) error

	// Active lists the player's active missions, oldest first.
	Active(ctx context.Context, playerID string) ([]MissionAssignment, error)
	// Recent lists the player's missions, most recent first.
	Recent(ctx context.Context, playerID string, limit int) ([]MissionAssignment, error)
	// ByNo returns one of the player's missions by its public number, or
	// ErrMissionNotFound.
	ByNo(ctx context.Context, playerID string, no int64) (*MissionAssignment, error)
	// Accept records a mission taken and returns it with its number. The
	// same mission active twice is ErrMissionActive.
	Accept(ctx context.Context, a MissionAssignment) (MissionAssignment, error)
	// SaveProgress writes an active mission's progress.
	SaveProgress(ctx context.Context, id string, progress []int64) error
	// End closes an active mission with its status, end and reward, or
	// returns ErrMissionNotFound when it is not active any more.
	End(ctx context.Context, a MissionAssignment) error
	// Completions is when the player last completed each mission.
	Completions(ctx context.Context, playerID string) (map[string]time.Time, error)
	// PaidSince is the mission cash the player (or, with playerID empty,
	// everyone) was paid since.
	PaidSince(ctx context.Context, playerID string, since time.Time) (int64, error)

	// MarkEvent records that an event moved a player's missions, and
	// reports whether this is the first time (the inbox).
	MarkEvent(ctx context.Context, eventID, playerID, subject string, at time.Time) (bool, error)
}

// Mission refusals.
var (
	ErrMissionNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrMissionNotFound", "no such mission")

	ErrMissionActive = errors.Sentinel(errors.CodeConflict,
		"application.ErrMissionActive", "that mission is already taken")
)
