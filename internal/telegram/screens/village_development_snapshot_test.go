package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The development readout (docs/adr/0044 section 4.5), as its own snapshot
// area: testdata/snapshots/<language>/development.txt.
func init() { snapshotAreas["development"] = developmentSnapshots }

func developmentSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)
	name := villageNameFor(c)
	grown := village.DevelopmentView{
		Village: name,
		Dimensions: []village.DevelopmentDimension{
			{Code: village.DevelopmentPeople, Load: 14, Capacity: 24},
			{Code: village.DevelopmentBuildings, Load: 9},
			{Code: village.DevelopmentKnowledge, Load: 7},
		},
		Roles: []village.DevelopmentRole{{Role: "education", Level: 2}, {Role: "health", Level: 1}, {Role: "market", Level: 1}},
		Next: []village.DevelopmentNext{
			{Kind: "build", Code: "civic_hall"},
			{Kind: "build", Code: "private_shed"},
		},
	}
	fresh := village.DevelopmentView{
		Village: name,
		Dimensions: []village.DevelopmentDimension{
			{Code: village.DevelopmentPeople, Load: 2},
			{Code: village.DevelopmentBuildings, Load: 2},
			{Code: village.DevelopmentKnowledge, Load: 4},
		},
	}
	add("Development · a grown place with goals ahead", VillageDevelopment(g, grown))
	add("Development · a fresh place, nothing built, no goals", VillageDevelopment(g, fresh))
	add("Village overview · the readout button (flag on)", VillageOverview(g, VillageOverviewView{
		Name: name, Tier: "village", Population: 6, PopulationCap: 12, FoodPercent: 40, LiteracyPercent: 12, Treasury: 7_400,
		Development: true,
	}))
}
