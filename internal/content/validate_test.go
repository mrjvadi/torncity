package content

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/world"
)

// ptr is the shorthand for a *bool literal, which RouteDef.Bidirectional
// needs and Go has no syntax for.
func ptr(b bool) *bool { return &b }

// city builds a valid city, so a table case has to state only what it is
// making wrong. A case that spells out four correct fields to test the fifth
// buries the thing it is testing.
func city(code string) CityDef {
	return CityDef{Code: code, Name: strings.ToUpper(code), TaxRateBPS: 500, CostOfLiving: 1000, SpawnWeight: 10, Country: "home"}
}

// homeLevels and homeCountry are the smallest governance every fixture needs:
// cities sit under a country, and every fixture city is in "home".
var (
	homeLevels = []LevelDef{
		{Code: CountryLevel, Parents: []string{WorldLevel}},
		{Code: CityLevel, Parents: []string{CountryLevel}},
	}
	homeCountry = []JurisdictionDef{{Code: "home", Name: "Home", Level: CountryLevel}}
)

// validPack is a small world that passes every rule: two cities that new
// players may both start in, one route between them, one real skill.
func validPack() *Pack {
	return &Pack{
		Schema:        1,
		Cities:        []CityDef{city("alpha"), city("bravo")},
		Routes:        []RouteDef{{From: "alpha", To: "bravo", Distance: 100}},
		Skills:        []SkillDef{{Code: "driving", Name: "Driving", Category: "technical"}},
		Levels:        homeLevels,
		Jurisdictions: homeCountry,
	}
}

func TestValidateAcceptsAValidPack(t *testing.T) {
	if err := validPack().Validate(); err != nil {
		t.Fatalf("a valid pack was rejected: %v", err)
	}
}

// TestValidateRejects covers every rejection validate.go implements. Each case
// starts from a valid pack and breaks exactly one thing, so a failure names
// the rule that stopped working rather than a pack that was wrong in several
// ways at once.
func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Pack)
		want   error
	}{
		{
			name:   "no cities at all",
			mutate: func(p *Pack) { p.Cities = nil; p.Routes = nil },
			want:   ErrNoCities,
		},
		{
			name:   "empty city code",
			mutate: func(p *Pack) { p.Cities[1].Code = "" },
			want:   ErrEmptyCityCode,
		},
		{
			name:   "duplicate city code",
			mutate: func(p *Pack) { p.Cities = append(p.Cities, city("alpha")) },
			want:   ErrDuplicateCityCode,
		},
		{
			name:   "tax rate below zero",
			mutate: func(p *Pack) { p.Cities[0].TaxRateBPS = -1 },
			want:   ErrInvalidTaxRate,
		},
		{
			name:   "tax rate above one hundred percent",
			mutate: func(p *Pack) { p.Cities[0].TaxRateBPS = world.BasisPointsScale + 1 },
			want:   ErrInvalidTaxRate,
		},
		{
			name:   "cost of living is zero",
			mutate: func(p *Pack) { p.Cities[0].CostOfLiving = 0 },
			want:   ErrInvalidCostOfLiving,
		},
		{
			name:   "cost of living is negative",
			mutate: func(p *Pack) { p.Cities[0].CostOfLiving = -1 },
			want:   ErrInvalidCostOfLiving,
		},
		{
			name:   "route from an empty code",
			mutate: func(p *Pack) { p.Routes[0].From = "" },
			want:   ErrEmptyRouteEndpoint,
		},
		{
			name:   "route to an empty code",
			mutate: func(p *Pack) { p.Routes[0].To = "" },
			want:   ErrEmptyRouteEndpoint,
		},
		{
			name:   "route to a city no file declares",
			mutate: func(p *Pack) { p.Routes[0].To = "nowhere" },
			want:   ErrUnknownRouteCity,
		},
		{
			name:   "route from a city no file declares",
			mutate: func(p *Pack) { p.Routes[0].From = "nowhere" },
			want:   ErrUnknownRouteCity,
		},
		{
			name:   "route from a city to itself",
			mutate: func(p *Pack) { p.Routes[0].To = p.Routes[0].From },
			want:   ErrSelfRoute,
		},
		{
			name: "the same route written twice",
			mutate: func(p *Pack) {
				p.Routes = append(p.Routes, RouteDef{From: "alpha", To: "bravo", Distance: 100})
			},
			want: ErrDuplicateRoute,
		},
		{
			// The pair is UNORDERED: bravo -> alpha is the same road as
			// alpha -> bravo, not its other direction. world.NewRoutes agrees,
			// and a rule here that did not would pass content the domain then
			// refuses to build.
			name: "the same route written backwards",
			mutate: func(p *Pack) {
				p.Routes = append(p.Routes, RouteDef{From: "bravo", To: "alpha", Distance: 250})
			},
			want: ErrDuplicateRoute,
		},
		{
			// Even a route the author marked one-way collides with its
			// reverse. The flag records intent about one edge; it does not
			// create a second edge that may carry its own distance.
			name: "a one-way route colliding with its reverse",
			mutate: func(p *Pack) {
				p.Routes[0].Bidirectional = ptr(false)
				p.Routes = append(p.Routes, RouteDef{
					From: "bravo", To: "alpha", Distance: 250, Bidirectional: ptr(false)})
			},
			want: ErrDuplicateRoute,
		},
		{
			// The domain builds every edge both ways, so a one-way flag would
			// be a key the file states and the world ignores.
			name:   "a route marked one-way",
			mutate: func(p *Pack) { p.Routes[0].Bidirectional = ptr(false) },
			want:   ErrOneWayRouteUnsupported,
		},
		{
			name:   "distance of zero",
			mutate: func(p *Pack) { p.Routes[0].Distance = 0 },
			want:   ErrInvalidDistance,
		},
		{
			name:   "negative distance",
			mutate: func(p *Pack) { p.Routes[0].Distance = -1 },
			want:   ErrInvalidDistance,
		},
		{
			name:   "distance beyond what the route builder accepts",
			mutate: func(p *Pack) { p.Routes[0].Distance = world.MaxEdgeDistance + 1 },
			want:   ErrInvalidDistance,
		},
		{
			name:   "a skill code the domain does not declare",
			mutate: func(p *Pack) { p.Skills[0].Code = "telepathy" },
			want:   ErrUnknownSkillCode,
		},
		{
			name:   "a skill with no code",
			mutate: func(p *Pack) { p.Skills[0].Code = "" },
			want:   ErrUnknownSkillCode,
		},
		{
			name: "the same skill declared twice",
			mutate: func(p *Pack) {
				p.Skills = append(p.Skills, SkillDef{Code: "driving", Name: "Driving Again"})
			},
			want: ErrDuplicateSkillCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPack()
			tt.mutate(p)

			err := p.Validate()
			if err == nil {
				t.Fatalf("Validate accepted content it should have refused")
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v\nwant an error matching %v", err, tt.want)
			}
		})
	}
}

// TestValidateAcceptsTheBoundaries pins the ends of the allowed ranges. A rule
// stated as "between 0 and 10000" is ambiguous about its endpoints, and the
// endpoints are where an off-by-one lives.
func TestValidateAcceptsTheBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Pack)
	}{
		{"tax of zero", func(p *Pack) { p.Cities[0].TaxRateBPS = 0 }},
		{"tax of exactly one hundred percent", func(p *Pack) { p.Cities[0].TaxRateBPS = world.BasisPointsScale }},
		{"cost of living of one", func(p *Pack) { p.Cities[0].CostOfLiving = 1 }},
		{"distance of one", func(p *Pack) { p.Routes[0].Distance = 1 }},
		{"the longest allowed distance", func(p *Pack) { p.Routes[0].Distance = world.MaxEdgeDistance }},
		{"a route stated bidirectional explicitly", func(p *Pack) { p.Routes[0].Bidirectional = ptr(true) }},
		{"no routes at all", func(p *Pack) { p.Routes = nil }},
		{"no skills at all", func(p *Pack) { p.Skills = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPack()
			tt.mutate(p)
			if err := p.Validate(); err != nil {
				t.Fatalf("Validate refused content at the edge of the allowed range: %v", err)
			}
		})
	}
}

// TestValidateReportsEveryProblem is the reason the errors are joined. An
// author with four typos should learn about four typos in one run.
func TestValidateReportsEveryProblem(t *testing.T) {
	p := validPack()
	p.Cities[0].TaxRateBPS = -5
	p.Cities[1].CostOfLiving = 0
	p.Routes[0].Distance = 0
	p.Skills[0].Code = "telepathy"

	err := p.Validate()
	if err == nil {
		t.Fatal("Validate accepted a pack with four problems")
	}
	for _, want := range []error{
		ErrInvalidTaxRate, ErrInvalidCostOfLiving, ErrInvalidDistance, ErrUnknownSkillCode,
	} {
		if !errors.Is(err, want) {
			t.Errorf("the joined error does not report %v:\n%v", want, err)
		}
	}
}

// TestValidateNamesTheOffender: an error that says a rule was broken without
// saying by what leaves the author grepping the file.
func TestValidateNamesTheOffender(t *testing.T) {
	p := validPack()
	p.Cities = append(p.Cities, city("alpha"))

	err := p.Validate()
	if err == nil {
		t.Fatal("expected a duplicate to be refused")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("the error does not name the duplicated code:\n%v", err)
	}
	if !strings.Contains(err.Error(), "cities[2]") {
		t.Errorf("the error does not say where the duplicate is:\n%v", err)
	}
}

// TestDisconnectedCityIsAWarningNotAnError is ADR 0004's position, restated as
// a test: a city has to be addable before its routes are authored. Refusing
// the load would turn a gap in content into a total outage, instead of one
// journey that fails with world.ErrNoRoute.
func TestDisconnectedCityIsAWarningNotAnError(t *testing.T) {
	p := validPack()
	p.Cities = append(p.Cities, city("charlie"))

	if err := p.Validate(); err != nil {
		t.Fatalf("a city with no routes must still load: %v", err)
	}

	warnings := p.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("got %d warning(s), want exactly 1: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "charlie") {
		t.Errorf("the warning does not name the isolated city: %q", warnings[0])
	}

	// And the world it produces really is usable everywhere else.
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatalf("a pack with an isolated city must still build: %v", err)
	}
	if _, err := snap.Routes().DistanceBetween("alpha", "charlie"); !errors.Is(err, world.ErrUnknownCity) {
		t.Errorf("travel to the isolated city should fail locally, got %v", err)
	}
}

func TestWarningsAreEmptyForAFullyConnectedWorld(t *testing.T) {
	if got := validPack().Warnings(); len(got) != 0 {
		t.Errorf("got %v, want no warnings", got)
	}
}

// TestWarningsAreStable: warnings are printed by a command, so an unstable
// order would make two identical runs look like two different results.
func TestWarningsAreStable(t *testing.T) {
	p := validPack()
	p.Cities = append(p.Cities, city("zulu"), city("charlie"), city("mike"))

	got := p.Warnings()
	if len(got) != 3 {
		t.Fatalf("got %d warning(s), want 3: %v", len(got), got)
	}
	for i, want := range []string{"charlie", "mike", "zulu"} {
		if !strings.Contains(got[i], want) {
			t.Errorf("warning %d is %q, want it to name %q", i, got[i], want)
		}
	}
}

// A city with no code is already an error; warning about it as well would
// print a line naming nothing.
func TestWarningsIgnoreCitiesWithNoCode(t *testing.T) {
	p := validPack()
	p.Cities = append(p.Cities, CityDef{CostOfLiving: 1})
	if got := p.Warnings(); len(got) != 0 {
		t.Errorf("got %v, want no warnings for an unnamed city", got)
	}
}

func TestEdgesAndWorldCities(t *testing.T) {
	p := validPack()
	p.CityIDs = map[string]string{"alpha": "id-alpha"}

	edges := p.Edges()
	if len(edges) != 1 || edges[0].From != "alpha" || edges[0].To != "bravo" || edges[0].Distance != 100 {
		t.Fatalf("Edges lost the route: %+v", edges)
	}

	cities := p.WorldCities()
	if len(cities) != 2 {
		t.Fatalf("got %d cities, want 2", len(cities))
	}
	if cities[0].ID != "id-alpha" {
		t.Errorf("a known id was not carried through: %+v", cities[0])
	}
	if cities[1].ID != "" {
		t.Errorf("an id was invented for a city that has none: %+v", cities[1])
	}
}

// IsBidirectional's default is the whole reason RouteDef.Bidirectional is a
// pointer: an omitted key must mean two-way, not one-way.
func TestRouteDirectionDefaultsToBidirectional(t *testing.T) {
	tests := []struct {
		name string
		flag *bool
		want bool
	}{
		{"key omitted", nil, true},
		{"stated true", ptr(true), true},
		{"stated false", ptr(false), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := RouteDef{From: "a", To: "b", Distance: 1, Bidirectional: tt.flag}
			if got := r.IsBidirectional(); got != tt.want {
				t.Errorf("got %t, want %t", got, tt.want)
			}
		})
	}
}

// Spawn weights: a negative one is meaningless, and a world where no city has
// a positive one places new players nowhere. Zero on some cities is fine —
// that is how a city says "nobody is born here".
func TestValidateSpawnWeights(t *testing.T) {
	t.Run("negative", func(t *testing.T) {
		p := validPack()
		p.Cities[1].SpawnWeight = -1
		err := p.Validate()
		if !errors.Is(err, ErrNegativeSpawnWeight) {
			t.Fatalf("got %v, want ErrNegativeSpawnWeight", err)
		}
		if !strings.Contains(err.Error(), "bravo") {
			t.Errorf("the error does not name the city: %v", err)
		}
	})

	t.Run("too large", func(t *testing.T) {
		p := validPack()
		p.Cities[0].SpawnWeight = MaxSpawnWeight + 1
		if err := p.Validate(); !errors.Is(err, ErrSpawnWeightTooLarge) {
			t.Fatalf("got %v, want ErrSpawnWeightTooLarge", err)
		}
	})

	t.Run("all zero", func(t *testing.T) {
		p := validPack()
		for i := range p.Cities {
			p.Cities[i].SpawnWeight = 0
		}
		if err := p.Validate(); !errors.Is(err, ErrNoSpawnCity) {
			t.Fatalf("got %v, want ErrNoSpawnCity", err)
		}
	})

	t.Run("some zero, one positive", func(t *testing.T) {
		p := validPack()
		p.Cities[0].SpawnWeight = 0
		if err := p.Validate(); err != nil {
			t.Fatalf("a pack with one spawn city was rejected: %v", err)
		}
		got := p.SpawnCandidates()
		if len(got) != 1 || got[0].Code != "bravo" {
			t.Errorf("SpawnCandidates = %+v, want only bravo", got)
		}
	})

	// No cities at all is one mistake, reported once.
	t.Run("no cities is not also a missing spawn city", func(t *testing.T) {
		p := validPack()
		p.Cities, p.Routes = nil, nil
		err := p.Validate()
		if !errors.Is(err, ErrNoCities) {
			t.Fatalf("got %v, want ErrNoCities", err)
		}
		if errors.Is(err, ErrNoSpawnCity) {
			t.Errorf("an empty city list was also reported as missing a spawn city: %v", err)
		}
	})
}
