package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
)

// the village tags of configs/content/availability.yml, and a town one.
var crimeTags = []content.AvailabilityDef{
	{Kind: "crime", Code: "pickpocketing", Stage: "village", Requires: &content.AvailabilityNeeds{
		Buildings: []content.AvailabilityBuilding{{Role: "market", Tier: 1}},
		Personal:  []content.AvailabilityPersonal{{Kind: content.PersonalLevel, Min: 1}}}},
	{Kind: "crime", Code: "bag_snatching", Stage: "town", Requires: &content.AvailabilityNeeds{
		Personal: []content.AvailabilityPersonal{{Kind: content.PersonalLevel, Min: 2}}}},
}

func crimeAt(level, stage int, built ...content.AvailabilityBuilding) crimeStanding {
	return crimeStanding{level: level, stageRank: stage, stands: func(b content.AvailabilityBuilding) bool {
		for _, have := range built {
			if have.Role == b.Role && have.Tier >= b.Tier {
				return true
			}
		}
		return false
	}}
}

func TestAVillageWithAMarketOffersCrimeToALevelOnePlayer(t *testing.T) {
	v := judgeCrimes(crimeTags, crimeAt(1, content.StageRank("village"), content.AvailabilityBuilding{Role: "market", Tier: 1}))
	if v.Available != 1 || v.Empty != "" {
		t.Errorf("got %+v, want one crime available", v)
	}
}

func TestAVillageWithoutAMarketOffersNoCrimeAndNamesTheBuilding(t *testing.T) {
	v := judgeCrimes(crimeTags, crimeAt(1, content.StageRank("village")))
	if v.Available != 0 || v.Empty != "no_venue" || v.Need == nil || v.Need.Role != "market" {
		t.Errorf("got %+v, want no_venue asking for a market", v)
	}
}

func TestACrimeBehindALevelNamesTheLevel(t *testing.T) {
	v := judgeCrimes(crimeTags, crimeAt(1, content.StageRank("town")))
	// pickpocketing needs a market; bag snatching needs level 2 only
	if v.Available != 0 || v.Empty != "level_too_low" || v.MinLevel != 2 {
		t.Errorf("got %+v, want level_too_low at 2", v)
	}
}

func TestTheCentralCityNeverOffersCrime(t *testing.T) {
	// even with every building and level, the neutral city is refused before any tag is read
	r := ActivityRules{NeutralCity: "support"}
	v, err := r.crimeListing(nil, nil, nil, 99, &application.City{Code: "support", Tier: "city"})
	if err != nil || v.Empty != "no_venue" || v.Available != 0 {
		t.Errorf("got %+v %v, want no_venue", v, err)
	}
}

func TestAVillageCrimeListHoldsOnlyVillageTaggedCrimesWhoseRequirementsAreMet(t *testing.T) {
	tags := append([]content.AvailabilityDef{}, crimeTags...)
	tags = append(tags, content.AvailabilityDef{Kind: "crime", Code: "street_scam", Stage: "town", Requires: &content.AvailabilityNeeds{
		Buildings: []content.AvailabilityBuilding{{Role: "market", Tier: 2}},
		Personal:  []content.AvailabilityPersonal{{Kind: content.PersonalLevel, Min: 2}}}})
	market := content.AvailabilityBuilding{Role: "market", Tier: 2}
	v := judgeCrimes(tags, crimeAt(5, content.StageRank("village"), market))
	if !v.allows("pickpocketing") || v.allows("street_scam") || v.allows("bag_snatching") {
		t.Errorf("a village offers only its village-tagged crimes: %+v", v.Allowed)
	}
	if bare := judgeCrimes(tags, crimeAt(5, content.StageRank("village"))); bare.allows("pickpocketing") {
		t.Errorf("a village without a market must not offer pickpocketing: %+v", bare.Allowed)
	}
}
