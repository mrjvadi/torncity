package postgres

import (
	"context"
	"errors"
	"testing"
)

// Processed reads the same key MarkProcessed writes, and only reads it: a
// check that claimed the row would record a notification before it was sent.
func TestSelectProcessedReadsTheCompositeKey(t *testing.T) {
	sql := normalize(selectProcessed)
	if sql != "SELECT EXISTS (SELECT 1 FROM inbox_messages WHERE message_id = $1 AND consumer = $2)" {
		t.Errorf("unexpected inbox read:\n%s", sql)
	}
}

func TestProcessedAnswersFromTheRow(t *testing.T) {
	for _, want := range []bool{true, false} {
		q := &fakeQuerier{rowVals: []any{want}}
		got, err := (&InboxStore{q: q}).Processed(context.Background(), "req-1", "notifier-travel-completed")
		if err != nil {
			t.Fatalf("Processed: %v", err)
		}
		if got != want {
			t.Errorf("Processed = %v, want %v", got, want)
		}
		call := q.last()
		if call.sql != selectProcessed || len(call.args) != 2 || call.args[0] != "req-1" || call.args[1] != "notifier-travel-completed" {
			t.Errorf("Processed sent %q with %v", call.sql, call.args)
		}
	}
}

// An empty half of the key would answer for every other empty key, as it
// would collide in MarkProcessed; neither is sent to the server.
func TestProcessedRefusesAnEmptyKey(t *testing.T) {
	for _, key := range [][2]string{{"", "c"}, {"m", ""}, {"", ""}} {
		q := &fakeQuerier{}
		if _, err := (&InboxStore{q: q}).Processed(context.Background(), key[0], key[1]); err == nil {
			t.Errorf("Processed(%q, %q) was accepted", key[0], key[1])
		}
		if len(q.calls) != 0 {
			t.Errorf("Processed(%q, %q) reached the database", key[0], key[1])
		}
	}
}

func TestProcessedReportsFailures(t *testing.T) {
	boom := errors.New("boom")
	done, err := (&InboxStore{q: &fakeQuerier{rowErr: boom}}).Processed(context.Background(), "req-1", "c")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want one wrapping the driver's", err)
	}
	if done {
		t.Error("a failed read reported the message processed")
	}
}
