package scheduler

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/commands"
)

// The crime handler writes three action types onto the schedule: a timed
// crime's end, an investigation's end and a sentence served. Each must be
// routed, under exactly the application's spelling, to a command the game
// serves as a scheduled one, or a burglary never ends, a report is never
// answered and a prisoner is never released.
func TestCrimeActionsAreRouted(t *testing.T) {
	for scheduler, app := range map[string]string{
		ActionTypeCrime:         application.CrimeActionType,
		ActionTypeInvestigation: application.InvestigationActionType,
		ActionTypeJailRelease:   application.JailReleaseActionType,
	} {
		if scheduler != app {
			t.Errorf("scheduler spells %q, application %q", scheduler, app)
		}
		route, ok := RouteFor(app)
		if !ok {
			t.Errorf("no route for %q", app)
			continue
		}
		sub, ok := commands.Lookup(route.Command())
		if !ok || sub.Origin != commands.FromScheduler {
			t.Errorf("%s is not a scheduled command in the table", route.Command())
		}
	}
}
