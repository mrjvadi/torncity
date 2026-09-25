package application

import (
	"context"
	"time"
)

// This file holds the ports of achievements (migration 0026,
// docs/adr/0024-property-and-politics.md): each player's progress toward
// each achievement, the achievements earned, and the consumer's inbox. The
// rules are internal/domain/achievement; what exists is content
// (achievements.yml).

// PlayerAchievement is a player_achievements row.
type PlayerAchievement struct {
	PlayerID  string
	Code      string
	AwardedAt time.Time
	Cash      int64
	Withheld  int64
}

// AchievementRepository persists achievements. Reach it through
// Tx.Achievements, so progress commits with the event that moved it.
type AchievementRepository interface {
	// Lock serialises one player's achievements until the transaction ends.
	Lock(ctx context.Context, playerID string) error
	// LockDay serialises achievement payouts, for the day's caps.
	LockDay(ctx context.Context) error
	// MarkEvent records that an event moved a player's achievements; false
	// when it already had.
	MarkEvent(ctx context.Context, eventID, playerID, subject string, at time.Time) (bool, error)
	// Progress returns the player's counts, by achievement.
	Progress(ctx context.Context, playerID string) (map[string]int64, error)
	// SaveProgress writes one count.
	SaveProgress(ctx context.Context, playerID, code string, count int64, at time.Time) error
	// Earned returns what the player has earned, newest first.
	Earned(ctx context.Context, playerID string) ([]PlayerAchievement, error)
	// Award records an achievement earned; false when it already was.
	Award(ctx context.Context, a PlayerAchievement) (bool, error)
	// PaidSince is achievement cash paid since a time: to one player, or to
	// everyone for "".
	PaidSince(ctx context.Context, playerID string, since time.Time) (int64, error)
}
