package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of the watch (migrations/0023,
// docs/adr/0023-health-missions-factions.md): the flags its behavioural rules
// raise, with their evidence, and the payments it holds for review. The
// rules are internal/domain/watch; the thresholds are configuration
// (anticheat.*). Nothing here bans anyone.

// PaymentHoldReference is the reference_type of a held payment's ledger
// rows.
const PaymentHoldReference = "payment_holds"

// Statuses, exactly as the migration's CHECKs spell them.
const (
	FlagOpen    = "open"
	FlagCleared = "cleared"

	HoldHeld     = "held"
	HoldReleased = "released"
	HoldReturned = "returned"
)

// WatchFlag is a watch_flags row.
type WatchFlag struct {
	ID            string
	No            int64
	Rule          string
	PlayerID      string
	OtherPlayerID string
	Score         int
	Hits          int
	Evidence      map[string]int64
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ClearedAt     *time.Time
	ClearedBy     string
	Note          string
}

// PaymentHold is a payment_holds row.
type PaymentHold struct {
	ID                  string
	No                  int64
	PayerID             string
	PayeeID             string
	Method              string
	Amount              int64
	FlagID              string
	Status              string
	HoldTransactionID   string
	SettleTransactionID string
	CreatedAt           time.Time
	SettledAt           *time.Time
	SettledBy           string
	Note                string
}

// TransferFlow is value that moved one way between two players: payments
// and held payments, and its count.
type TransferFlow struct {
	Count int
	Total int64
}

// WatchRepository persists the watch. Reach it through Tx.Watch for what a
// command raises or holds in its own transaction.
type WatchRepository interface {
	// Raise records a finding: the open flag of its rule and pair gains a
	// hit, keeps the higher score and the latest evidence, or a new one
	// opens. It returns the flag as it now stands.
	Raise(ctx context.Context, f WatchFlag) (WatchFlag, error)
	// Linking returns an open flag that links two players, in either
	// direction, of a rule about a pair; or ErrFlagNotFound.
	Linking(ctx context.Context, a, b string) (*WatchFlag, error)
	// Flow is what moved from one player to another since.
	Flow(ctx context.Context, from, to string, since time.Time) (TransferFlow, error)
	// Outgoing counts a player's payments since, and those to their most
	// frequent payee.
	Outgoing(ctx context.Context, from string, since time.Time) (all int, partner string, toPartner int, err error)

	// Hold records a held payment with its number.
	Hold(ctx context.Context, h PaymentHold) (PaymentHold, error)
	// HoldByNo returns one held payment, locked, or ErrHoldNotFound.
	HoldByNo(ctx context.Context, no int64) (*PaymentHold, error)
	// SettleHold closes a held payment as released or returned, or returns
	// ErrHoldNotFound when it is not held any more.
	SettleHold(ctx context.Context, h PaymentHold) error

	// Flags lists flags of a status, most recent first; FlagByNo one.
	Flags(ctx context.Context, status string, limit int) ([]WatchFlag, error)
	FlagByNo(ctx context.Context, no int64) (*WatchFlag, error)
	// Clear clears an open flag, or returns ErrFlagNotFound.
	Clear(ctx context.Context, no int64, by, note string, at time.Time) error
	// Holds lists held payments of a status, oldest first.
	Holds(ctx context.Context, status string, limit int) ([]PaymentHold, error)
}

// WatchRecorder raises a flag outside any command's transaction: the
// command rate, which the game service counts around every command. Best
// effort, like the activity stamp.
type WatchRecorder interface {
	Raise(ctx context.Context, f WatchFlag) (WatchFlag, error)
}

// Watch refusals.
var (
	ErrFlagNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrFlagNotFound", "no such flag")
	ErrHoldNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrHoldNotFound", "no such held payment")
)
