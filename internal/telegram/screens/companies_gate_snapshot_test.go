package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The "not available here" state of the kinds of business
// (testdata/snapshots/<language>/companies_gate.txt): what a player in a
// village reads for a kind that starts at a larger stage, what a settlement
// needs to run it and the nearest place that has it.
func init() { snapshotAreas["companies_gate"] = companiesGateSnapshots }

func companiesGateSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	support := &Named{Code: "brennhaven", Name: "Brennhaven"}
	town := &economy.Unavailable{Service: "restaurant", Stage: "town", Here: "village",
		Requires: []economy.NeedBuilding{{Role: "market", Tier: 1}}, Nearest: support}
	city := &economy.Unavailable{Service: "factory", Stage: "city", Here: "village",
		Requires: []economy.NeedBuilding{{Code: "factory"}}, Nearest: support}
	country := &economy.Unavailable{Service: "aerospace", Stage: "country", Here: "village",
		Requires: []economy.NeedBuilding{{Role: "craft", Tier: 4}}}
	only := &economy.Unavailable{Service: "pharma", Stage: "support", Here: "town", Nearest: support}
	types := CompanyTypesView{CityCode: "ostmarch", City: "Ostmarch", Owned: 0, Max: 3, Types: []CompanyTypeLine{
		{Type: Named{Code: "grocery", Name: "Grocery"}, Fee: 1000, Upkeep: 50},
		{Type: Named{Code: "restaurant", Name: "Restaurant"}, Fee: 2000, Upkeep: 80, Unavailable: town},
		{Type: Named{Code: "factory", Name: "Factory"}, Fee: 9000, Upkeep: 300, Unavailable: city},
		{Type: Named{Code: "aerospace", Name: "Aerospace"}, Fee: 90000, Upkeep: 2000, Licensed: true, Unavailable: country},
	}}
	add("Company types · a village: what a larger settlement has is listed, not hidden", CompanyTypes(c, types))
	detail := func(u *economy.Unavailable, code string) CompanyTypeView {
		return CompanyTypeView{Type: Named{Code: code, Name: code}, CityCode: "ostmarch", City: "Ostmarch", Place: Named{Code: "city_hall", Name: "City hall"}, Careers: []JobRef{{CareerCode: "retail", CareerName: "Retail"}},
			Fee: 2000, Upkeep: 80, MaxStaff: 8, Period: time.Hour, NameMin: 3, NameMax: 24, Blocked: CompanyBlockedStage, Unavailable: u}
	}
	add("Company type · not reached, a stage and a building role", CompanyTypeDetail(c, detail(town, "restaurant")))
	add("Company type · not reached, a building", CompanyTypeDetail(c, detail(city, "factory")))
	add("Company type · only in the central city", CompanyTypeDetail(c, detail(only, "pharma")))
	add("Company type · nowhere known to go", CompanyTypeDetail(c, detail(country, "aerospace")))
}
