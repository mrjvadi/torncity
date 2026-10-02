package handlers

import (
	"context"
	stderrors "errors"

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
	// PropertyMinStage is the smallest stage from which the Economy hub lists
	// the property market (settlement.property_hub_min_stage).
	PropertyMinStage string
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
	// Allowed holds the codes of the crimes available here; nil means every
	// crime is (a content city has no settlement buildings to judge by).
	Allowed map[string]bool
}

// allows reports whether a crime is available here.
func (v crimeVerdict) allows(code string) bool { return v.Allowed == nil || v.Allowed[code] }

// judgeCrimes weighs the tags of the crimes against a standing. A crime is
// available when the settlement has reached its stage, every building it
// requires stands and the player has its level. When none is, the nearest
// unlock is named: the lowest level that would open one, else the building.
func judgeCrimes(tags []content.AvailabilityDef, s crimeStanding) crimeVerdict {
	v := crimeVerdict{Allowed: map[string]bool{}}
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
			v.Allowed[tag.Code] = true
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
		return crimeVerdict{Empty: plife.CrimeEmptyNoVenue, Allowed: map[string]bool{}}, nil
	}
	if _, err := tx.Settlements().ByID(ctx, city.ID); stderrors.Is(err, application.ErrCityNotFound) {
		// a content city: no settlement buildings to judge by, every crime is its own
		return crimeVerdict{Available: len(snap.Crimes())}, nil
	} else if err != nil {
		return crimeVerdict{}, err
	}
	rows, err := tx.SettlementBuildings().List(ctx, city.ID)
	if err != nil {
		return crimeVerdict{}, err
	}
	stands := standsIn(snap, rows)
	var tags []content.AvailabilityDef
	var untagged []string
	for _, def := range snap.Crimes() {
		if tag, ok := snap.AvailabilityTag("crime", def.Code); ok {
			tags = append(tags, tag)
		} else {
			untagged = append(untagged, def.Code)
		}
	}
	v := judgeCrimes(tags, crimeStanding{level: level, stageRank: content.StageRank(tierStage(city.Tier)), stands: stands})
	for _, code := range untagged {
		v.Allowed[code] = true
		v.Available++
	}
	if v.Available > 0 {
		v.Empty = ""
	}
	return v, nil
}

// standsIn tells whether a building an availability tag asks for stands among a settlement's buildings: a finished one
// of that code, or of that role at that tier or better.
func standsIn(snap *content.Snapshot, rows []application.SettlementBuildingInstance) func(content.AvailabilityBuilding) bool {
	return func(b content.AvailabilityBuilding) bool {
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
}
