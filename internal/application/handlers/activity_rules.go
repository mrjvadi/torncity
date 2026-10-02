package handlers

import (
	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// ActivityRules is the tuning that decides which activities a player is
// offered where (ADR 0038 section 3): crime is listed from a player level and
// in a settlement that has reached a stage, and never in the neutral city.
// All of it comes from config (crime.min_level, crime.min_stage,
// settlement.home_city_code); a zero value lists crime everywhere.
type ActivityRules struct {
	CrimeMinLevel int
	CrimeMinStage string
	// NeutralCity is the code of the neutral city (settlement.home_city_code).
	NeutralCity string
}

// crimeEmpty says why crime has nothing to offer a player of this level
// standing in city, or "" when crime is listed to them. A player standing
// nowhere (on the road) has no venue.
func (r ActivityRules) crimeEmpty(level int, city *application.City) string {
	if level < r.CrimeMinLevel {
		return screens.CrimeEmptyLevelTooLow
	}
	if city == nil {
		return screens.CrimeEmptyNoVenue
	}
	if r.NeutralCity != "" && city.Code == r.NeutralCity {
		return screens.CrimeEmptyNoVenue
	}
	if need := content.StageRank(r.CrimeMinStage); need > 0 && content.StageRank(tierStage(city.Tier)) < need {
		return screens.CrimeEmptyNoVenue
	}
	return ""
}
