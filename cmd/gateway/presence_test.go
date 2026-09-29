package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type beatRecorder struct {
	ids []string
	err error
}

func (b *beatRecorder) Beat(_ context.Context, id string, _ time.Time) error {
	b.ids = append(b.ids, id)
	return b.err
}

// A command proves the player is here; a failing or missing presence store
// never gets in the command's way.
func TestGatewayBeatsPresence(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	rec := &beatRecorder{}
	g := &gateway{presence: rec}
	g.beat(context.Background(), "p1", log)
	g.beat(context.Background(), "", log)
	if len(rec.ids) != 1 || rec.ids[0] != "p1" {
		t.Errorf("beats = %v, want exactly one for p1 (a player-less update beats nobody)", rec.ids)
	}

	rec.err = errors.New("redis down")
	g.beat(context.Background(), "p2", log) // must not panic or return

	(&gateway{}).beat(context.Background(), "p3", log) // no store configured
}
