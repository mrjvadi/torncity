package main

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/workers/scheduler"
)

// The reaper is switched on in run by a type assertion on the repository, so
// the only thing that can quietly turn it back off is the postgres adapter
// drifting away from scheduler.ClaimReaper — a renamed method, a changed
// signature. This line makes that a compile error in the one package that
// knows both sides, instead of a scheduler that logs "reaper":false and
// strands every player whose dispatch is ever interrupted.
//
// It lives here, and not beside either type, because neither side may import
// the other: the adapter must not depend on a worker, and the worker must not
// depend on one particular database.
var _ scheduler.ClaimReaper = (*postgres.GameActionRepository)(nil)

// TestRepositoryEnablesTheReaper runs the exact assertion run performs, on the
// value it performs it on, so a reaper hidden behind a wrapper type would be
// caught as well as a missing method.
func TestRepositoryEnablesTheReaper(t *testing.T) {
	actions := postgres.NewGameActionRepository(&postgres.Pool{})
	if _, ok := any(actions).(scheduler.ClaimReaper); !ok {
		t.Fatal("the postgres game action repository no longer satisfies scheduler.ClaimReaper; the scheduler would start with no reaper")
	}
}
