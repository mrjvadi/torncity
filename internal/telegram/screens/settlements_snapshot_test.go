package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Group founding (docs/adr/0028-world-and-settlements.md section 3) joins
// the snapshot harness as its own area, testdata/snapshots/<language>/
// settlements.txt. Founding is always a group screen (section 9.5), so
// every snapshot here is rendered shared, the way the group actually sees
// it — never private, unlike most of the rest of this package.
func init() { snapshotAreas["settlements"] = settlementSnapshots }

func settlementSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)

	// internal/domain/settlement.GenerateName renders a village's name and
	// internal/domain/settlement.NearbyFeature the nearest named river or
	// continent in whichever script this language reads (SettlementsHandler
	// picks Persian for "fa", Latin otherwise) — sample names here follow
	// the same rule, so the Persian snapshot never shows a Latin word.
	villageName, secondVillageName, thirdVillageName := "Korendal", "Ashvale", "Milgrave"
	riverName, seaName := "Vestara", "the Northern Sea"
	if c.Lang == "fa" {
		villageName, secondVillageName, thirdVillageName = "کورندال", "اشوال", "میلگریو"
		riverName, seaName = "وستارا", "دریای شمالی"
	}

	add("Settlement founded · river nearby, both buildings placed", SettlementFounded(g, SettlementFoundedView{
		Name: villageName, BiomeCode: "temperate_forest", NearbyFeature: riverName,
		Buildings: []string{"civic_hall", "road"}, ProtectedUntil: snapshotNow.Add(168 * time.Hour),
	}))
	add("Settlement founded · no named feature nearby", SettlementFounded(g, SettlementFoundedView{
		Name: secondVillageName, BiomeCode: "desert", Buildings: []string{"civic_hall", "road"},
		ProtectedUntil: snapshotNow.Add(168 * time.Hour),
	}))
	add("Settlement founded · only the civic hall fit", SettlementFounded(g, SettlementFoundedView{
		Name: thirdVillageName, BiomeCode: "tundra", NearbyFeature: seaName,
		Buildings: []string{"civic_hall"}, ProtectedUntil: snapshotNow.Add(168 * time.Hour),
	}))

	add("Settlement refused · only works in a group", SettlementRefusal(c, SettlementRefusalView{Kind: "group_only"}))
	add("Settlement refused · no world yet", SettlementRefusal(g, SettlementRefusalView{Kind: "no_world"}))
	add("Settlement refused · this group already founded one", SettlementRefusal(g, SettlementRefusalView{
		Kind: "already", Name: villageName,
	}))
}
