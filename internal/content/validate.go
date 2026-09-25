package content

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/world"
)

// Validation failures. Each is wrapped with the offending entry, so errors.Is
// answers "what kind of mistake" and the message answers "which one".
var (
	// ErrEmptyCityCode means a city arrived without its code. The code is the
	// only thing routes, stored player locations and past events refer to a
	// city by, so a city without one cannot be referred to at all.
	ErrEmptyCityCode = errors.New("content: city code is required")

	// ErrDuplicateCityCode means two cities claimed the same code. Keeping
	// either one silently would make the file say something the loaded world
	// does not.
	ErrDuplicateCityCode = errors.New("content: duplicate city code")

	// ErrInvalidTaxRate means a tax rate fell outside 0..10000 basis points.
	// Above 10000 a city takes more than the whole amount; below zero it pays
	// the player to be taxed.
	ErrInvalidTaxRate = errors.New("content: tax_rate_bps must be between 0 and 10000")

	// ErrInvalidCostOfLiving means a cost of living at or below zero.
	//
	// This is STRICTER than world.City.Validate, which only rejects a negative
	// one. The domain is right to allow zero — a rule must behave sensibly
	// whatever number it is handed — but authored content is a different
	// question: a city that costs nothing to live in is free rent forever and
	// is far more likely to be a forgotten field than a design decision. The
	// place to catch a suspicious number is the load, where a human is present
	// to be told.
	ErrInvalidCostOfLiving = errors.New("content: cost_of_living must be greater than zero")

	// ErrEmptyRouteEndpoint means a route named an endpoint with no code.
	ErrEmptyRouteEndpoint = errors.New("content: route endpoints are required")

	// ErrUnknownRouteCity means a route referenced a city that no content file
	// declares. This is the mistake ADR 0004 rule 1 names first: it must fail
	// the load, not a player's journey three hours later.
	ErrUnknownRouteCity = errors.New("content: route references an unknown city")

	// ErrSelfRoute means a route connected a city to itself. The distance from
	// a city to itself is zero by definition, so the line is either a typo or
	// an assertion that cannot be true.
	ErrSelfRoute = errors.New("content: route cannot connect a city to itself")

	// ErrDuplicateRoute means the same pair of cities was given a direct route
	// twice.
	//
	// THE PAIR IS UNORDERED. A -> B and B -> A are the SAME route written
	// twice, not two directions of one. That follows world.NewRoutes, which
	// builds a symmetric distance matrix and rejects the reverse as a
	// duplicate, and disagreeing with it here would mean content that passes
	// validation and then fails to build. The `bidirectional` flag records
	// authoring intent about the edge; it does not make the reverse a separate
	// edge that may be given its own distance, because two distances for one
	// road is the drift the single-row model exists to prevent.
	ErrDuplicateRoute = errors.New("content: duplicate route between the same two cities")

	// ErrInvalidDistance means a distance at or below zero, or beyond what the
	// domain's route builder accepts.
	ErrInvalidDistance = errors.New("content: route distance must be positive")

	// ErrOneWayRouteUnsupported means a route said `bidirectional: false`.
	//
	// The file format has the key and the ADR lists it, but the domain does
	// not honour it: world.Edge is bidirectional by definition and
	// world.NewRoutes builds a symmetric network whatever the flag says.
	// Accepting the key would therefore be the exact failure KnownFields(true)
	// exists to prevent — a file stating an intention the system silently
	// ignores, here a one-way road that players can drive both ways. It is
	// refused until the domain grows the extension point world.Edge describes;
	// stating `bidirectional: true`, or omitting the key, is fine.
	ErrOneWayRouteUnsupported = errors.New("content: one-way routes (bidirectional: false) are not supported yet")

	// ErrUnknownSkillCode means a skill code is not one of the codes
	// internal/domain/player declares. That set is closed because other
	// packages branch on individual members of it, so a code invented in a
	// file would be a skill a player can train and never use.
	ErrUnknownSkillCode = errors.New("content: unknown skill code")

	// ErrDuplicateSkillCode means two skill entries claimed the same code.
	ErrDuplicateSkillCode = errors.New("content: duplicate skill code")

	// ErrNoCities means the pack declared no cities at all. Every player has a
	// location, so a world with nowhere to be is not a world.
	ErrNoCities = errors.New("content: at least one city is required")

	// ErrNegativeSpawnWeight means a city's spawn_weight is below zero. A
	// weight is a share of newcomers; a negative share has no meaning.
	ErrNegativeSpawnWeight = errors.New("content: spawn_weight must not be negative")

	// ErrSpawnWeightTooLarge means a spawn_weight above MaxSpawnWeight. The
	// weights are summed when a newcomer is placed and stored in an int
	// column, so an absurd value is refused here rather than overflowing at
	// load time.
	ErrSpawnWeightTooLarge = errors.New("content: spawn_weight is too large")

	// ErrNoSpawnCity means cities were declared but none has a positive
	// spawn_weight. New players are placed by those weights at first contact,
	// so with none they would be placed nowhere, and a player with no city
	// cannot travel.
	ErrNoSpawnCity = errors.New("content: at least one city must have a positive spawn_weight")

	// ErrUnbuildableRoutes means the edges passed every rule here and the
	// domain's own route builder still refused them. It should be unreachable;
	// if it ever fires, a rule in this file has drifted away from
	// world.NewRoutes and the two must be reconciled.
	ErrUnbuildableRoutes = errors.New("content: routes cannot be built into a network")
)

// Validate reports everything wrong with a pack, or nil.
//
// # Why every problem, not the first one
//
// The errors are joined rather than returned one at a time. A content author
// runs this, fixes what it names and runs it again; reporting one mistake per
// run turns a file with four typos into four round trips. errors.Is still
// answers correctly through the join, so a caller testing for a specific
// failure is unaffected.
//
// # Why this is not a method on the domain
//
// Most of these constraints exist in internal/domain too, on City and in
// NewRoutes, and the domain is where they belong for the rules that use them.
// They are repeated here because the domain checks one value at a time and
// this checks a whole authored set: duplicates, cross-references and the
// relationship between the city list and the route list are properties of the
// collection, not of any member of it.
//
// # What Validate cannot see
//
// ADR 0004 rule 7 — content that is in use must not disappear — is NOT checked
// here, and deliberately so. Whether a player is standing in a city or
// travelling towards it is a fact about the live database, not about the
// files, and a pure function over a Pack cannot know it. That check lives in
// the apply step, inside the same transaction that writes the new version, and
// it must: anything checked outside the transaction can become untrue between
// the check and the write. See postgres.ContentStore.Apply.
func (p *Pack) Validate() error {
	var problems []error

	known := p.validateCities(&problems)
	p.validateRoutes(known, &problems)
	p.validateSkills(&problems)

	// Governance (ADR 0015): levels, jurisdictions, offices, levers.
	p.validateGovernance(&problems)

	// Work and study: careers, courses and the certifications between them.
	p.validateJobs(&problems)

	// Transport: facilities, modes, and which modes serve which route.
	p.validateTransport(&problems)

	// Crime: tiers, venues, categories and crimes, against the modes,
	// careers, courses and facilities above.
	p.validateCrimes(&problems)

	// Payments: which methods each service, course and mode accepts.
	p.validatePayments(&problems)

	// Places: the places of a city, against its cities and facilities.
	p.validatePlaces(&problems)

	// Items: components, archetypes, goods and the shops that sell them.
	p.validateItems(&problems)

	// Elections: how each elected office is elected.
	p.validateElections(&problems)

	// Companies: kinds of business against careers and places, and each
	// city's NPC market.
	p.validateCompanies(&problems)

	// Production: method timings, the technology tree, how each component
	// is made, and the NPC suppliers of basic inputs.
	p.validateProduction(&problems)
	p.validateMilitary(&problems)
	p.validateDefenceLicence(&problems)
	p.validateWar(&problems)
	p.validateHealth(&problems)
	p.validateMissions(&problems)
	p.validateFactions(&problems)
	p.validateBudget(&problems)
	p.validateProperty(&problems)
	p.validateAchievements(&problems)
	p.validateLife(&problems)
	p.validateVehicles(&problems)

	if len(problems) > 0 {
		return errors.Join(problems...)
	}

	// Last: build what the domain will actually build. Everything above is
	// this package's understanding of the rules; this is the rules themselves,
	// and it is cheap insurance against the two drifting apart.
	if _, err := world.NewRoutes(p.Edges()); err != nil {
		return fmt.Errorf("%w: %w", ErrUnbuildableRoutes, err)
	}
	return nil
}

// validateCities checks the city list and returns the set of declared codes.
func (p *Pack) validateCities(problems *[]error) map[string]struct{} {
	if len(p.Cities) == 0 {
		*problems = append(*problems, ErrNoCities)
	}

	known := make(map[string]struct{}, len(p.Cities))
	spawnable := 0
	for i, c := range p.Cities {
		// The index is reported alongside the code because a city whose code
		// is empty cannot be identified any other way.
		where := fmt.Sprintf("cities[%d]", i)

		if c.Code == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrEmptyCityCode, where))
		} else if _, seen := known[c.Code]; seen {
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateCityCode, c.Code, where))
		} else {
			known[c.Code] = struct{}{}
		}

		if c.TaxRateBPS < 0 || c.TaxRateBPS > world.BasisPointsScale {
			*problems = append(*problems, fmt.Errorf("%w: %s %q has %d",
				ErrInvalidTaxRate, where, c.Code, c.TaxRateBPS))
		}
		if c.CostOfLiving <= 0 {
			*problems = append(*problems, fmt.Errorf("%w: %s %q has %d",
				ErrInvalidCostOfLiving, where, c.Code, c.CostOfLiving))
		}
		switch {
		case c.SpawnWeight < 0:
			*problems = append(*problems, fmt.Errorf("%w: %s %q has %d",
				ErrNegativeSpawnWeight, where, c.Code, c.SpawnWeight))
		case c.SpawnWeight > MaxSpawnWeight:
			*problems = append(*problems, fmt.Errorf("%w: %s %q has %d, the most is %d",
				ErrSpawnWeightTooLarge, where, c.Code, c.SpawnWeight, MaxSpawnWeight))
		case c.SpawnWeight > 0:
			spawnable++
		}
	}

	// Only checked when there are cities at all: an empty list is already
	// ErrNoCities, and naming the missing spawn city as well would report one
	// mistake twice.
	if len(p.Cities) > 0 && spawnable == 0 {
		*problems = append(*problems, ErrNoSpawnCity)
	}
	return known
}

// validateRoutes checks the edge list against the declared cities.
func (p *Pack) validateRoutes(known map[string]struct{}, problems *[]error) {
	// Keyed on the unordered pair, so the reverse of an edge collides with it.
	seen := make(map[[2]string]int, len(p.Routes))

	for i, r := range p.Routes {
		where := fmt.Sprintf("routes[%d]", i)

		switch {
		case r.From == "" || r.To == "":
			*problems = append(*problems, fmt.Errorf("%w: %s (%q -> %q)",
				ErrEmptyRouteEndpoint, where, r.From, r.To))
		case r.From == r.To:
			*problems = append(*problems, fmt.Errorf("%w: %s %q", ErrSelfRoute, where, r.From))
		default:
			for _, code := range [2]string{r.From, r.To} {
				if _, ok := known[code]; !ok {
					*problems = append(*problems, fmt.Errorf("%w: %s references %q, which no content file declares",
						ErrUnknownRouteCity, where, code))
				}
			}

			key := pairKey(r.From, r.To)
			if first, dup := seen[key]; dup {
				*problems = append(*problems, fmt.Errorf("%w: %s repeats routes[%d] (%q and %q)",
					ErrDuplicateRoute, where, first, r.From, r.To))
			} else {
				seen[key] = i
			}
		}

		if !r.IsBidirectional() {
			*problems = append(*problems, fmt.Errorf("%w: %s (%q -> %q)",
				ErrOneWayRouteUnsupported, where, r.From, r.To))
		}

		if r.Distance <= 0 || r.Distance > world.MaxEdgeDistance {
			*problems = append(*problems, fmt.Errorf("%w and at most %d: %s (%q -> %q) has %d",
				ErrInvalidDistance, world.MaxEdgeDistance, where, r.From, r.To, r.Distance))
		}
	}
}

// validateSkills checks every skill entry against the domain's closed set.
func (p *Pack) validateSkills(problems *[]error) {
	seen := make(map[string]struct{}, len(p.Skills))
	for i, s := range p.Skills {
		where := fmt.Sprintf("skills[%d]", i)

		if err := player.Validate(s.SkillCode()); err != nil {
			*problems = append(*problems, fmt.Errorf("%w: %s %q is not one of the codes internal/domain/player declares",
				ErrUnknownSkillCode, where, s.Code))
			continue
		}
		if _, dup := seen[s.Code]; dup {
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateSkillCode, s.Code, where))
			continue
		}
		seen[s.Code] = struct{}{}
	}
}

// Warnings reports what is suspicious but loadable, in a stable order.
//
// # Why a city no route reaches is not an error
//
// world.NewRoutes accepts a disconnected network on purpose, and refusing one
// here would contradict it. The reasoning is worth restating: a city has to be
// addable before its routes are authored, so failing the load would turn a gap
// in content into a total outage instead of one refused journey. A player who
// tries to travel there gets world.ErrNoRoute, which is a clear, local,
// recoverable failure. The disconnection is still almost always a typo, so it
// is reported loudly — it just does not stop the load.
//
// A warning never prevents a load. Anything that should prevent one belongs in
// Validate.
func (p *Pack) Warnings() []string {
	reached := make(map[string]struct{}, len(p.Routes)*2)
	for _, r := range p.Routes {
		reached[r.From] = struct{}{}
		reached[r.To] = struct{}{}
	}

	var isolated []string
	for _, c := range p.Cities {
		if c.Code == "" {
			continue
		}
		if _, ok := reached[c.Code]; !ok {
			isolated = append(isolated, c.Code)
		}
	}
	sort.Strings(isolated)

	warnings := make([]string, 0, len(isolated))
	for _, code := range isolated {
		warnings = append(warnings, fmt.Sprintf(
			"city %q has no route to anywhere: it will load, but nobody can travel to or from it", code))
	}
	warnings = append(warnings, p.transportWarnings()...)
	return append(warnings, p.crimeWarnings()...)
}

// Edges returns the pack's routes as domain edge values, in file order.
func (p *Pack) Edges() []world.Edge {
	edges := make([]world.Edge, 0, len(p.Routes))
	for _, r := range p.Routes {
		edges = append(edges, r.Edge())
	}
	return edges
}

// WorldCities returns the pack's cities as domain values, in file order. IDs
// are filled in from CityIDs when the pack carries them.
func (p *Pack) WorldCities() []world.City {
	cities := make([]world.City, 0, len(p.Cities))
	for _, c := range p.Cities {
		city := c.City()
		city.ID = p.CityIDs[c.Code]
		cities = append(cities, city)
	}
	return cities
}

// pairKey orders two codes so that a pair and its reverse produce one key.
func pairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}
