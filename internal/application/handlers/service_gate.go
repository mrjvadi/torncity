package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// ServiceGate decides whether a service is offered in the settlement a player
// stands in (CLAUDE.md section 2: nothing exists in a village, town or city by
// default; docs/audit/2026-10-01-rules-audit.md). The rule is data: the stage
// each service starts at, what it needs and where else it can be had are the
// tags of configs/content/availability.yml, which the content snapshot indexes
// (content.Snapshot.ServiceTag). A service nothing tags is offered everywhere.
//
// It only says so: a screen of a service that is not offered here carries the
// answer (economy.Unavailable) instead of its facts, and each edge words it. It
// does not change what a command does.
type ServiceGate struct {
	cities application.CityRepository
	// home is the code of the neutral city, the nearest place that has any
	// service the settlement lacks (settlement.home_city_code).
	home string
}

// NewServiceGate builds the gate. A gate without a city repository never
// closes a service.
func NewServiceGate(cities application.CityRepository, homeCityCode string) *ServiceGate {
	return &ServiceGate{cities: cities, home: homeCityCode}
}

// tierStage is the stage a settlement's tier is: a content city, and any row
// that names none, is a city (application.City.Tier).
func tierStage(tier string) string {
	if tier == "" {
		return content.StageCity
	}
	return tier
}

// Check reports why the service is not offered in the city the player stands
// in, or nil when it is (or when it cannot be said: no city, no tag).
func (g *ServiceGate) Check(ctx context.Context, snap *content.Snapshot, cityID, service string) (*economy.Unavailable, error) {
	if g == nil || g.cities == nil || cityID == "" || snap == nil {
		return nil, nil
	}
	tag, ok := snap.ServiceTag(service)
	if !ok {
		return nil, nil
	}
	return g.judge(ctx, cityID, tag, service)
}

// CheckTag is Check for an entry that availability.yml tags itself, by kind
// and code (a company_type, a building, a place): whether the settlement the
// player stands in reaches it. An entry nothing tags is reachable everywhere.
func (g *ServiceGate) CheckTag(ctx context.Context, snap *content.Snapshot, cityID, kind, code string) (*economy.Unavailable, error) {
	if g == nil || g.cities == nil || cityID == "" || snap == nil {
		return nil, nil
	}
	tag, ok := snap.AvailabilityTag(kind, code)
	if !ok {
		return nil, nil
	}
	return g.judge(ctx, cityID, tag, code)
}

// judge applies one tag to the city the player stands in.
func (g *ServiceGate) judge(ctx context.Context, cityID string, tag content.AvailabilityDef, service string) (*economy.Unavailable, error) {
	here, err := g.cities.ByID(ctx, cityID)
	if err != nil {
		return nil, err
	}
	stage := tierStage(here.Tier)
	switch {
	case tag.Stage == content.StageSupport:
		if here.Code == g.home {
			return nil, nil
		}
	case content.StageRank(tag.Stage) == 0:
		// undecided, or a stage nothing here can judge: nothing is guessed.
		return nil, nil
	case content.StageRank(stage) >= content.StageRank(tag.Stage):
		return nil, nil
	}
	out := &economy.Unavailable{Service: service, Stage: tag.Stage, Here: stage}
	if tag.Requires != nil {
		for _, b := range tag.Requires.Buildings {
			out.Requires = append(out.Requires, economy.NeedBuilding{Code: b.Code, Role: b.Role, Tier: b.Tier})
		}
	}
	if g.home != "" && g.hasElsewhere(tag) {
		if support, err := g.cities.ByCode(ctx, g.home); err == nil {
			out.Nearest = &presentation.Named{Code: support.Code, Name: support.Name}
		}
	}
	return out, nil
}

// hasElsewhere says the tag names the neutral city as a place the service can
// be had: every tag of a service the player can reach does.
func (g *ServiceGate) hasElsewhere(tag content.AvailabilityDef) bool {
	for _, e := range tag.Elsewhere {
		if e.Where == content.StageSupport {
			return true
		}
	}
	return len(tag.Elsewhere) == 0 && tag.Stage == content.StageSupport
}
