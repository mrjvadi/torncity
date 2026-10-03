package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// A service whose building a founded settlement lacks is closed there (storage
// and market audit F4, CLAUDE.md rule 2): a settlement offers a place service
// only when the matching building stands. This is the fail-closed twin of
// needService, which passes for a city without a place map. The neutral city
// and every content city (no settlement row) keep their services; they have
// buildings of their own kind, not a settlement's.

// serviceBuilding is the building a settlement needs standing for a service.
// The Code is what the "not available here" screen names to the head.
type serviceBuilding struct {
	service string
	// any is the codes of the buildings that open it; one standing is enough.
	any []string
}

// marketNeed is the market: the market post or the market hall. The shop
// («دکان») shares the role but is not a place where residents trade.
var marketNeed = serviceBuilding{service: "market", any: []string{"barter_post", "market"}}

// closedIn reports why the service is closed in the settlement of city, or nil
// when it is open (the neutral city, a content city, or the building stands).
func closedIn(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City,
	homeCityCode string, sb serviceBuilding,
) (*economy.Unavailable, error) {
	if city == nil {
		return nil, nil
	}
	if founded, err := tx.Settlements().IsFounded(ctx, city.ID); err != nil || !founded {
		return nil, err // a content city has its own places, not a settlement's buildings
	}
	here, err := judgeSettlementOf(ctx, tx, snap, city, homeCityCode)
	if err != nil {
		return nil, err
	}
	if here.content || here.neutral || here.stands == nil {
		return nil, nil
	}
	for _, code := range sb.any {
		if here.stands(content.AvailabilityBuilding{Code: code}) {
			return nil, nil
		}
	}
	return &economy.Unavailable{
		Service: sb.service, Stage: content.StageVillage, Here: tierStage(city.Tier),
		Requires: []economy.NeedBuilding{{Code: sb.any[0]}},
	}, nil
}

// withNearest names the neutral city, the nearest place that has the service,
// on a closed answer.
func withNearest(ctx context.Context, cities application.CityRepository, homeCityCode string, un *economy.Unavailable) *economy.Unavailable {
	if un == nil || cities == nil || homeCityCode == "" {
		return un
	}
	if home, err := cities.ByCode(ctx, homeCityCode); err == nil {
		un.Nearest = &presentation.Named{Code: home.Code, Name: home.Name}
	}
	return un
}
