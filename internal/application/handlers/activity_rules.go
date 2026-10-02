package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// ActivityRules is the little the Activities hub needs besides content: the
// code of the neutral city (settlement.home_city_code), where crime is never
// offered. Which crime a player can attempt where is not tuned here: it is the
// availability tags of configs/content/availability.yml (stage, standing
// buildings, the player's level), read by crimeListing.
type ActivityRules struct {
	// NeutralCity is the code of the neutral city (settlement.home_city_code).
	NeutralCity string
}

// crimeStanding is what decides which crimes a player may attempt where they
// stand: the stage of the settlement, the level of the player and a way to ask
// whether a building stands there.
type crimeStanding struct {
	level     int
	stageRank int
	stands    func(content.AvailabilityBuilding) bool
}

// crimeVerdict is how crime stands for one player in one settlement.
type crimeVerdict struct {
	// Available counts the crimes the player may attempt now.
	Available int
	// Empty is why there are none ("" when there are some): level_too_low,
	// no_venue; MinLevel the lowest level that unlocks one, Need the building
	// that would.
	Empty    string
	MinLevel int
	Need     *content.AvailabilityBuilding
}

// judgeCrimes weighs the tags of the crimes against a standing. A crime is
// available when the settlement has reached its stage, every building it
// requires stands and the player has its level. When none is, the nearest
// unlock is named: the lowest level that would open one, else the building.
func judgeCrimes(tags []content.AvailabilityDef, s crimeStanding) crimeVerdict {
	var v crimeVerdict
	for _, tag := range tags {
		need := content.StageRank(tag.Stage)
		if need == 0 || s.stageRank < need {
			continue // not offered at this stage, or the stage is undecided
		}
		var missing *content.AvailabilityBuilding
		minLevel := 0
		if tag.Requires != nil {
			for _, b := range tag.Requires.Buildings {
				if !s.stands(b) {
					b := b
					missing = &b
					break
				}
			}
			for _, p := range tag.Requires.Personal {
				if p.Kind == content.PersonalLevel && p.Min > s.level && p.Min > minLevel {
					minLevel = p.Min
				}
			}
		}
		switch {
		case missing == nil && minLevel == 0:
			v.Available++
		case missing == nil:
			if v.MinLevel == 0 || minLevel < v.MinLevel {
				v.MinLevel = minLevel
			}
		case minLevel == 0 && v.Need == nil:
			v.Need = missing
		}
	}
	switch {
	case v.Available > 0:
	case v.MinLevel > 0:
		v.Empty = plife.CrimeEmptyLevelTooLow
	default:
		v.Empty = plife.CrimeEmptyNoVenue
	}
	return v
}

// crimeListing reads how crime stands for the player in the city they stand in.
// The neutral city and the road never offer it.
func (r ActivityRules) crimeListing(ctx context.Context, tx application.Tx, snap *content.Snapshot, level int, city *application.City) (crimeVerdict, error) {
	if city == nil || (r.NeutralCity != "" && city.Code == r.NeutralCity) {
		return crimeVerdict{Empty: plife.CrimeEmptyNoVenue}, nil
	}
	rows, err := tx.SettlementBuildings().List(ctx, city.ID)
	if err != nil {
		return crimeVerdict{}, err
	}
	stands := func(b content.AvailabilityBuilding) bool {
		for _, row := range rows {
			if row.Status != "complete" {
				continue
			}
			d, ok := snap.SettlementBuildingDef(row.TypeCode)
			if !ok {
				continue
			}
			if b.Code != "" && d.Code == b.Code {
				return true
			}
			if b.Role != "" && d.Def().Role == b.Role && d.Def().Tier >= b.Tier {
				return true
			}
		}
		return false
	}
	var tags []content.AvailabilityDef
	for _, def := range snap.Crimes() {
		if tag, ok := snap.AvailabilityTag("crime", def.Code); ok {
			tags = append(tags, tag)
		}
	}
	return judgeCrimes(tags, crimeStanding{level: level, stageRank: content.StageRank(tierStage(city.Tier)), stands: stands}), nil
}
