package postgres

import (
	"context"
	"fmt"
	"time"
)

// InboxStore is the consumer-side deduplication table.
//
// JetStream delivers at least once: a message whose ACK was lost, or whose
// handler outran its ack wait, is delivered again. Retrying delivery is
// correct for the broker and wrong for a handler that moves money, so the
// gap is closed here. A consumer calls MarkProcessed first and does the work
// only when it gets fresh == true, which is what turns at-least-once delivery
// into effectively-once processing.
//
// The key is (message_id, consumer) and not message_id alone: one event is
// legitimately delivered to several consumers, and each of them has to process
// it exactly once. A key on message_id would let whichever consumer ran first
// silently swallow the message for all the others.
//
// It is not an application port. Nothing in the domain knows a message was
// redelivered; this lives entirely between the broker and the handler.
type InboxStore struct {
	q querier
}

// NewInboxStore returns a store over the pool.
func NewInboxStore(p *Pool) *InboxStore { return &InboxStore{q: p.Raw()} }

const markProcessed = `
INSERT INTO inbox_messages (message_id, consumer, processed_at)
VALUES ($1, $2, $3)
ON CONFLICT (message_id, consumer) DO NOTHING`

// MarkProcessed claims a message for one consumer.
//
// It returns fresh == true only when this call inserted the row, read from the
// command tag's RowsAffected. DO NOTHING reports no error whether it inserted
// or skipped, so the tag is the only thing that distinguishes a first delivery
// from a redelivery; treating "no error" as "fresh" would make every
// redelivery look new and reinstate the duplicate processing this table
// removes.
//
// Ordering matters at the call site: the claim must be committed in the same
// transaction as the work it guards. Claiming first and working in a separate
// transaction means a crash in between loses the work while keeping the claim,
// and the message is never processed by anyone.
func (s *InboxStore) MarkProcessed(ctx context.Context, messageID, consumer string) (bool, error) {
	if messageID == "" || consumer == "" {
		// Both columns are part of the primary key. An empty one would
		// collide with every other empty one and suppress unrelated messages.
		return false, fmt.Errorf("postgres: inbox mark processed: message id and consumer are both required")
	}

	tag, err := s.q.Exec(ctx, markProcessed, messageID, consumer, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: marking message %s processed by %s: %w", messageID, consumer, err)
	}

	return tag.RowsAffected() == 1, nil
}

const selectProcessed = `
SELECT EXISTS (SELECT 1 FROM inbox_messages WHERE message_id = $1 AND consumer = $2)`

// Processed reports whether consumer already recorded messageID, without
// claiming it.
//
// It is the read half of MarkProcessed, for a consumer whose work cannot share
// a transaction with the claim. The notifier is one: its work is a Telegram
// message, which no transaction can roll back, so it checks here before it
// sends and calls MarkProcessed only after the gateway confirms delivery. A
// claim written first and a send that then fails would lose the notification
// with no trace (internal/workers/notification).
func (s *InboxStore) Processed(ctx context.Context, messageID, consumer string) (bool, error) {
	if messageID == "" || consumer == "" {
		// The same guard as MarkProcessed: an empty key would answer for
		// every other empty key.
		return false, fmt.Errorf("postgres: inbox processed: message id and consumer are both required")
	}

	var done bool
	if err := s.q.QueryRow(ctx, selectProcessed, messageID, consumer).Scan(&done); err != nil {
		return false, fmt.Errorf("postgres: reading the inbox for message %s and %s: %w", messageID, consumer, err)
	}
	return done, nil
}
