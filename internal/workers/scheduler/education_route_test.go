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
