package handlers

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/presentation/society"
)

// govRefusalOf classifies err as one of the governance refusals the society
// screens have a sentence for, with a cooldown turned into a wait at now.
func govRefusalOf(err error, now time.Time) (society.GovRefusal, bool) {
	r, ok := application.ClassifyGovernanceRefusal(err)
	if !ok {
		return society.GovRefusal{}, false
	}
	return society.GovRefusal{Kind: r.Kind, Office: r.Office, Wait: r.Wait(now)}, true
}

// isGovernanceRefusal reports whether err is one of them.
func isGovernanceRefusal(err error) bool {
	_, ok := application.ClassifyGovernanceRefusal(err)
	return ok
}
