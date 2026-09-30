package application

import (
	"context"
	"time"
)

// QueuedNews is a news item waiting on the queue.
type QueuedNews struct {
	// EventID makes queueing idempotent.
	EventID string    `json:"e"`
	Kind    string    `json:"k"`
	Code    string    `json:"c,omitempty"`
	Name    string    `json:"n,omitempty"`
	Percent int       `json:"p,omitempty"`
	// Amount is what a donation gave, minor units.
	Amount int64 `json:"m,omitempty"`
	At      time.Time `json:"a"`
}

// NewsBatch is everything one village's queue held when it was taken.
type NewsBatch struct {
	SettlementID string
	Items        []QueuedNews
}

// NewsQueue is the shared queue behind the village news
// (internal/infrastructure/redis.NewsQueue).
type NewsQueue interface {
	// Push queues the item once: a second push of the same EventID, even
	// after the item was posted, changes nothing.
	Push(ctx context.Context, settlementID string, item QueuedNews) error
	// ClaimDue atomically takes the queues that are due: the oldest item at
	// least window old, and no batch of that village taken within gap. At
	// most max batches.
	ClaimDue(ctx context.Context, now time.Time, window, gap time.Duration, max int) ([]NewsBatch, error)
	// Requeue puts a batch back after a failed send and lets the village be
	// claimed again at once.
	Requeue(ctx context.Context, batch NewsBatch) error
}

