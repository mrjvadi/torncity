package scheduler

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/commands"
)

// The education handler writes application.EducationActionType onto the
// schedule; the scheduler must route exactly that spelling to the command
// the game serves as a scheduled one, or a finished course is never
// completed.
func TestEducationActionIsRouted(t *testing.T) {
	route, ok := RouteFor(application.EducationActionType)
	if !ok {
		t.Fatalf("no route for %q", application.EducationActionType)
	}
	sub, ok := commands.Lookup(route.Command())
	if !ok || sub.Origin != commands.FromScheduler {
		t.Fatalf("%s is not a scheduled command in the table", route.Command())
	}
}

// The same contract for a shift's end: the jobs handler writes
// application.ShiftActionType, and the scheduler must publish exactly the
// command the game serves as a scheduled one.
func TestShiftActionIsRouted(t *testing.T) {
	if ActionTypeShift != application.ShiftActionType {
		t.Fatalf("scheduler spells %q, application %q", ActionTypeShift, application.ShiftActionType)
	}
	route, ok := RouteFor(application.ShiftActionType)
	if !ok {
		t.Fatalf("no route for %q", application.ShiftActionType)
	}
	sub, ok := commands.Lookup(route.Command())
	if !ok || sub.Origin != commands.FromScheduler {
		t.Fatalf("%s is not a scheduled command in the table", route.Command())
	}
}
