package society

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The city hall page links to the charter for a founded settlement and only for one.
func TestCityGovernanceOffersTheCharter(t *testing.T) {
	has := func(v CityGovView) bool {
		r := CityGovernance(presentation.Ctx{Lang: "fa"}, v)
		for _, a := range r.Actions {
			if a.Command == "settlement.charter.view" {
				return true
			}
		}
		return false
	}
	if !has(CityGovView{Charter: true}) {
		t.Error("a founded settlement's page does not link to its charter")
	}
	if has(CityGovView{}) {
		t.Error("the neutral city's page links to a charter it does not have")
	}
}
