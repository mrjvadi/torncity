package content

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func i64(v int64) *int64 { return &v }

// structuredLevers are one lever of each structured type, all valid.
func structuredLevers() []LeverDef {
	return []LeverDef{
		{Code: "city.curfew", Jurisdiction: CityLevel, Type: LeverBool, Default: false,
			HeldBy: "city_council", DecisionRule: DecisionMajority, ChangeCooldown: "24h", Notice: "24h"},
		{Code: "city.transit_mode", Jurisdiction: CityLevel, Type: LeverEnum, Default: "bus",
			Options: []string{"bus", "tram", "none"},
			HeldBy:  "city_council", DecisionRule: DecisionMajority, ChangeCooldown: "24h", Notice: "24h"},
		{Code: "city.item_legality", Jurisdiction: CityLevel, Type: LeverMap, Key: "item",
			Options: []string{"legal", "restricted", "illegal"},
			Default: map[string]any{"assault_rifle": "illegal"},
			HeldBy:  "city_council", DecisionRule: DecisionMajority, ChangeCooldown: "24h", Notice: "24h"},
		{Code: "city.budget", Jurisdiction: CityLevel, Type: LeverAllocation,
			Categories: []string{"police", "roads", "reserve"},
			Default:    map[string]any{"police": 5000, "roads": 3000, "reserve": 2000},
			HeldBy:     "city_council", DecisionRule: DecisionMajority, ChangeCooldown: "24h", Notice: "24h"},
	}
}

// withStructured is governedPack plus one lever of each structured type.
func withStructured() *Pack {
	p := governedPack()
	p.Levers = append(p.Levers, structuredLevers()...)
	council := office(p, "city_council")
	for _, l := range structuredLevers() {
		council.Levers = append(council.Levers, l.Code)
	}
	return p
}

func TestValidateAcceptsStructuredLevers(t *testing.T) {
	if err := withStructured().Validate(); err != nil {
		t.Fatalf("valid structured levers were rejected: %v", err)
	}
	for _, l := range structuredLevers() {
		if l.ValueKind() != ValueKindStructured {
			t.Errorf("%s (%s) is %q, want structured", l.Code, l.Type, l.ValueKind())
		}
	}
	for _, typ := range []string{LeverBPS, LeverMoney, LeverInt} {
		if LeverValueKind(typ) != ValueKindScalar {
			t.Errorf("%s is %q, want scalar", typ, LeverValueKind(typ))
		}
	}
	if LeverValueKind("percent") != "" {
		t.Error("an unknown type has a value kind")
	}
}

// Each case breaks one structured lever.
func TestValidateRejectsStructuredLevers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *Pack)
	}{
		{"bool default not a bool", func(p *Pack) { lever(p, "city.curfew").Default = "no" }},
		{"structured lever with bounds", func(p *Pack) { lever(p, "city.curfew").Max = i64(1) }},
		{"structured lever with no default", func(p *Pack) { lever(p, "city.curfew").Default = nil }},
		{"enum default not an option", func(p *Pack) { lever(p, "city.transit_mode").Default = "ferry" }},
		{"enum with repeated options", func(p *Pack) { lever(p, "city.transit_mode").Options = []string{"bus", "bus"} }},
		{"enum with a key", func(p *Pack) { lever(p, "city.transit_mode").Key = "line" }},
		{"map with no key", func(p *Pack) { lever(p, "city.item_legality").Key = "" }},
		{"map default not a mapping", func(p *Pack) { lever(p, "city.item_legality").Default = "legal" }},
		{"map default value not an option", func(p *Pack) {
			lever(p, "city.item_legality").Default = map[string]any{"bread": "encouraged"}
		}},
		{"allocation beyond the whole", func(p *Pack) {
			lever(p, "city.budget").Default = map[string]any{"police": 5000, "roads": 6000}
		}},
		{"allocation of a negative share", func(p *Pack) {
			lever(p, "city.budget").Default = map[string]any{"police": -1}
		}},
		{"allocation to an undeclared category", func(p *Pack) {
			lever(p, "city.budget").Default = map[string]any{"police": 5000, "parks": 5000}
		}},
		{"allocation of a fraction", func(p *Pack) {
			lever(p, "city.budget").Default = map[string]any{"police": 5000.5, "roads": 4999.5}
		}},
		{"scalar lever with options", func(p *Pack) { lever(p, "city.tax_rate").Options = []string{"low"} }},
		{"scalar default not a number", func(p *Pack) { lever(p, "city.tax_rate").Default = "high" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := withStructured()
			tc.mutate(p)
			err := p.Validate()
			if !errors.Is(err, ErrInvalidLeverShape) && !errors.Is(err, ErrLeverBoundRequired) {
				t.Fatalf("Validate() = %v, want ErrInvalidLeverShape or ErrLeverBoundRequired", err)
			}
		})
	}
}

// A structured default survives the JSON the store keeps it as.
func TestStructuredDefaultRoundTripsAsJSON(t *testing.T) {
	for _, l := range structuredLevers() {
		raw, err := l.DefaultJSON()
		if err != nil {
			t.Fatalf("%s: %v", l.Code, err)
		}
		var back any
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("%s: %v", l.Code, err)
		}
		l.Default = back
		p := withStructured()
		*lever(p, l.Code) = l
		if err := p.Validate(); err != nil {
			t.Errorf("%s read back from JSON no longer validates: %v", l.Code, err)
		}
	}
}

func intp(v int) *int { return &v }

// electionLawBounds is a usable ElectionLawBounds for a test fixture.
func electionLawBounds() *ElectionLawBounds {
	return &ElectionLawBounds{
		CandidacyHours:         FieldBound{Min: 6, Max: 336},
		VotingHours:            FieldBound{Min: 6, Max: 336},
		MinLevel:               FieldBound{Min: 0, Max: 50},
		MinResidencyHours:      FieldBound{Min: 0, Max: 2160},
		VoterMinResidencyHours: FieldBound{Min: 0, Max: 720},
		Deposit:                FieldBound{Min: 0, Max: 20000},
		RefundShareBPS:         FieldBound{Min: 0, Max: 10000},
		ReopenAfterHours:       FieldBound{Min: 24, Max: 2160},
		EndorsementsRequired:   FieldBound{Min: 0, Max: 100},
		TermLimitConsecutive:   FieldBound{Min: 0, Max: 6},
		TermLimitTotal:         FieldBound{Min: 0, Max: 12},
		MinAge:                 FieldBound{Min: 0, Max: 99},
		EndorsementsMaxBPS:     2000,
		ExclusionMaxBPS:        3000,
	}
}

// electionLawDoc is a usable election_law default document for a test
// fixture: every election.Fields key, well inside electionLawBounds.
func electionLawDoc() map[string]any {
	return map[string]any{
		"candidacy_hours": 48, "voting_hours": 48, "min_level": 5, "min_residency_hours": 168,
		"clean_record": 1, "voter_min_residency_hours": 24, "deposit": 5000, "refund_share_bps": 1000,
		"reopen_after_hours": 168, "endorsements_required": 0, "term_limit_consecutive": 0,
		"term_limit_total": 0, "education_rank": 0, "min_age": 0,
	}
}

// governedPack is validPack plus a small, valid constitution: every level,
// a union above the country, one city lever whose default comes from each
// city's tax rate, one country lever, a mayor with a deputy, a council that
// votes, and a president — each elected office with the election_law lever
// election law now requires.
func governedPack() *Pack {
	p := validPack()
	p.Levels = []LevelDef{
		{Code: "union", Parents: []string{WorldLevel}},
		{Code: CountryLevel, Parents: []string{WorldLevel, "union"}},
		{Code: "province", Parents: []string{CountryLevel}},
		{Code: CityLevel, Parents: []string{CountryLevel, "province"}},
		{Code: "port_authority", Parents: []string{CountryLevel, CityLevel}, Overlay: true},
	}
	p.Jurisdictions = []JurisdictionDef{
		{Code: "bloc", Name: "Bloc", Level: "union"},
		{Code: "home", Name: "Home", Level: CountryLevel, Parent: "bloc"},
		{Code: "port", Name: "Port Authority", Level: "port_authority", Parent: "alpha"},
	}
	p.Levers = []LeverDef{
		{
			Code: "city.tax_rate", Jurisdiction: CityLevel, Type: LeverBPS,
			Default: 450, CityDefault: CityDefaultTaxRate, Min: i64(0), Max: i64(2500),
			HeldBy: "mayor", ChangeCooldown: "72h", Notice: "24h",
			VetoBy: []string{"president"}, OverrideRule: DecisionSupermajority, OverrideThreshold: "2/3",
		},
		{
			Code: "city.bylaw", Jurisdiction: CityLevel, Type: LeverInt,
			Default: 0, Min: i64(0), Max: i64(1),
			HeldBy: "city_council", DecisionRule: DecisionSupermajority, Threshold: "2/3", Quorum: "3/5",
			ChangeCooldown: "0s", Notice: "0s",
		},
		{
			Code: "country.border_tariff", Jurisdiction: CountryLevel, Type: LeverBPS,
			Default: 0, Min: i64(0), Max: i64(2500),
			HeldBy: "president", ChangeCooldown: "168h", Notice: "0s",
		},
		// Election law: the city council decides the city offices' law, and
		// (with no parliament in this minimal fixture) the president decides
		// its own, exactly as any other office may hold a lever naming it.
		{
			Code: "city.election_law.mayor", Jurisdiction: CityLevel, Type: LeverElectionLaw,
			Default: electionLawDoc(), Bounds: electionLawBounds(),
			HeldBy: "city_council", DecisionRule: DecisionMajority, ChangeCooldown: "24h", Notice: "24h",
		},
		{
			Code: "city.election_law.city_council", Jurisdiction: CityLevel, Type: LeverElectionLaw,
			Default: electionLawDoc(), Bounds: electionLawBounds(),
			HeldBy: "city_council", DecisionRule: DecisionMajority, ChangeCooldown: "24h", Notice: "24h",
		},
		{
			Code: "country.election_law.president", Jurisdiction: CountryLevel, Type: LeverElectionLaw,
			Default: electionLawDoc(), Bounds: electionLawBounds(),
			HeldBy: "president", ChangeCooldown: "24h", Notice: "24h",
		},
	}
	p.Offices = []OfficeDef{
		{
			Code: "mayor", Jurisdiction: CityLevel, Seats: 1, AcquiredBy: AcquiredByElection,
			Levers: []string{"city.tax_rate"}, Deputy: "deputy_mayor",
			Term: "2160h", TermLimit: intp(2), CanBeRemovedBy: []string{RemovableByResidents},
			IncompatibleWith: []string{"president"},
		},
		{
			Code: "deputy_mayor", Jurisdiction: CityLevel, Seats: 1, AcquiredBy: AcquiredByAppointment,
			AppointedBy: "mayor", RequiresConfirmationBy: "city_council", CanBeRemovedBy: []string{"mayor"},
		},
		{
			Code: "city_council", Jurisdiction: CityLevel, Seats: 5, AcquiredBy: AcquiredByElection,
			Levers:   []string{"city.bylaw", "city.election_law.mayor", "city.election_law.city_council"},
			VetoOver: []string{"mayor"},
		},
		{
			Code: "president", Jurisdiction: CountryLevel, Seats: 1, AcquiredBy: AcquiredByElection,
			Levers: []string{"country.border_tariff", "country.election_law.president"}, VetoOver: []string{"city.tax_rate"},
		},
	}
	return p
}

func TestValidateAcceptsAGovernedPack(t *testing.T) {
	if err := governedPack().Validate(); err != nil {
		t.Fatalf("a valid governed pack was rejected: %v", err)
	}
}

// office returns a pointer to the named office of p, for a case to break.
func office(p *Pack, code string) *OfficeDef {
	for i := range p.Offices {
		if p.Offices[i].Code == code {
			return &p.Offices[i]
		}
	}
	panic("no office " + code)
}

// lever returns a pointer to the named lever of p, for a case to break.
func lever(p *Pack, code string) *LeverDef {
	for i := range p.Levers {
		if p.Levers[i].Code == code {
			return &p.Levers[i]
		}
	}
	panic("no lever " + code)
}

// Each case breaks exactly one governance rule, starting from a valid pack.
func TestValidateRejectsGovernance(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *Pack)
		want   error
	}{
		// Levels.
		{"level with no code", func(p *Pack) { p.Levels = append(p.Levels, LevelDef{Parents: []string{WorldLevel}}) }, ErrEmptyLevelCode},
		{"world declared as a level", func(p *Pack) {
			p.Levels = append(p.Levels, LevelDef{Code: WorldLevel, Parents: []string{WorldLevel}})
		}, ErrReservedLevel},
		{"duplicate level", func(p *Pack) { p.Levels = append(p.Levels, p.Levels[0]) }, ErrDuplicateLevel},
		{"level with no parents", func(p *Pack) { p.Levels[2].Parents = nil }, ErrLevelParents},
		{"level with an undeclared parent", func(p *Pack) { p.Levels[2].Parents = []string{"empire"} }, ErrLevelParents},
		{"level cycle", func(p *Pack) { p.Levels[0].Parents = []string{CountryLevel} }, ErrLevelCycle},
		{"cities without the city level", func(p *Pack) {
			p.Levels = []LevelDef{p.Levels[0], p.Levels[1]}
			p.Jurisdictions = p.Jurisdictions[:2]
			p.Offices = []OfficeDef{*office(p, "president")}
			p.Levers = []LeverDef{*lever(p, "country.border_tariff")}
			office(p, "president").VetoOver = nil
		}, ErrUnknownLevel},

		// Jurisdictions and cities.
		{"jurisdiction with no code", func(p *Pack) {
			p.Jurisdictions = append(p.Jurisdictions, JurisdictionDef{Level: "union"})
		}, ErrEmptyJurisdictionCode},
		{"duplicate jurisdiction", func(p *Pack) { p.Jurisdictions = append(p.Jurisdictions, p.Jurisdictions[0]) }, ErrDuplicateJurisdictionCode},
		{"jurisdiction sharing a city code", func(p *Pack) {
			p.Jurisdictions = append(p.Jurisdictions, JurisdictionDef{Code: "alpha", Level: "union"})
		}, ErrDuplicateJurisdictionCode},
		{"city declared in governance", func(p *Pack) {
			p.Jurisdictions = append(p.Jurisdictions, JurisdictionDef{Code: "gamma", Level: CityLevel, Parent: "home"})
		}, ErrCityJurisdictionDeclared},
		{"jurisdiction at the world level", func(p *Pack) { p.Jurisdictions[0].Level = WorldLevel }, ErrReservedLevel},
		{"jurisdiction of an undeclared level", func(p *Pack) { p.Jurisdictions[0].Level = "empire" }, ErrUnknownLevel},
		{"jurisdiction under nothing declared", func(p *Pack) { p.Jurisdictions[1].Parent = "atlantis" }, ErrUnknownJurisdictionParent},
		{"jurisdiction under a level its rule forbids", func(p *Pack) { p.Jurisdictions[2].Parent = "bloc" }, ErrJurisdictionParentLevel},
		{"root jurisdiction whose level may not be a root", func(p *Pack) { p.Jurisdictions[2].Parent = "" }, ErrJurisdictionParentLevel},
		{"city level that may not sit under a country", func(p *Pack) { p.Levels[3].Parents = []string{"province"} }, ErrJurisdictionParentLevel},
		{"city with no country", func(p *Pack) { p.Cities[0].Country = "" }, ErrCityCountryRequired},
		{"city with an undeclared country", func(p *Pack) { p.Cities[1].Country = "atlantis" }, ErrUnknownCityCountry},
		{"city whose country is not a country", func(p *Pack) { p.Cities[1].Country = "bloc" }, ErrUnknownCityCountry},

		// Levers.
		{"lever with no code", func(p *Pack) { lever(p, "country.border_tariff").Code = "" }, ErrEmptyLeverCode},
		{"duplicate lever", func(p *Pack) { p.Levers = append(p.Levers, p.Levers[2]) }, ErrDuplicateLeverCode},
		{"lever at the world level", func(p *Pack) { lever(p, "country.border_tariff").Jurisdiction = WorldLevel }, ErrInvalidJurisdictionLevel},
		{"lever at an undeclared level", func(p *Pack) { lever(p, "country.border_tariff").Jurisdiction = "empire" }, ErrInvalidJurisdictionLevel},
		{"lever code without its level", func(p *Pack) {
			lever(p, "country.border_tariff").Code = "border_tariff"
			office(p, "president").Levers = []string{"border_tariff"}
		}, ErrLeverCodePrefix},
		{"unknown lever type", func(p *Pack) { lever(p, "city.tax_rate").Type = "percent" }, ErrInvalidLeverType},
		{"default omitted", func(p *Pack) { lever(p, "country.border_tariff").Default = nil }, ErrLeverBoundRequired},
		{"max omitted", func(p *Pack) { lever(p, "country.border_tariff").Max = nil }, ErrLeverBoundRequired},
		{"min above max", func(p *Pack) { lever(p, "country.border_tariff").Min = i64(3000) }, ErrLeverBoundsInverted},
		{"bps bound above the whole", func(p *Pack) { lever(p, "country.border_tariff").Max = i64(10001) }, ErrLeverBoundsOutOfRange},
		{"bps bound below zero", func(p *Pack) { lever(p, "country.border_tariff").Min = i64(-1) }, ErrLeverBoundsOutOfRange},
		{"default above max", func(p *Pack) { lever(p, "country.border_tariff").Default = 2501 }, ErrLeverDefaultOutOfBounds},
		{"default below min", func(p *Pack) { lever(p, "country.border_tariff").Min = i64(100) }, ErrLeverDefaultOutOfBounds},
		{"a city's own default above max", func(p *Pack) { p.Cities[1].TaxRateBPS = 3000 }, ErrLeverDefaultOutOfBounds},
		{"unknown city default source", func(p *Pack) { lever(p, "city.tax_rate").CityDefault = "cost_of_living" }, ErrUnknownCityDefault},
		{"city default on a country lever", func(p *Pack) {
			lever(p, "country.border_tariff").CityDefault = CityDefaultTaxRate
		}, ErrUnknownCityDefault},
		{"held by an undeclared office", func(p *Pack) { lever(p, "city.tax_rate").HeldBy = "governor" }, ErrUnknownLeverOffice},
		{"held by an office of another level", func(p *Pack) {
			lever(p, "country.border_tariff").HeldBy = "mayor"
			office(p, "mayor").Levers = []string{"city.tax_rate", "country.border_tariff"}
			office(p, "president").Levers = nil
		}, ErrLeverOfficeLevel},
		{"cooldown missing", func(p *Pack) { lever(p, "city.tax_rate").ChangeCooldown = "" }, ErrInvalidLeverDuration},
		{"cooldown unparseable", func(p *Pack) { lever(p, "city.tax_rate").ChangeCooldown = "three days" }, ErrInvalidLeverDuration},
		{"notice negative", func(p *Pack) { lever(p, "city.tax_rate").Notice = "-1h" }, ErrInvalidLeverDuration},
		{"notice not whole seconds", func(p *Pack) { lever(p, "city.tax_rate").Notice = "1500ms" }, ErrInvalidLeverDuration},

		// Decision rules.
		{"unknown decision rule", func(p *Pack) { lever(p, "city.bylaw").DecisionRule = "consensus" }, ErrInvalidDecisionRule},
		{"single decision by a body", func(p *Pack) {
			l := lever(p, "city.bylaw")
			l.DecisionRule, l.Threshold, l.Quorum = "", "", ""
		}, ErrInvalidDecisionRule},
		{"single decision with a multi-seat deputy", func(p *Pack) { office(p, "deputy_mayor").Seats = 3 }, ErrInvalidDecisionRule},
		{"vote of a one-seat office", func(p *Pack) { lever(p, "city.tax_rate").DecisionRule = DecisionMajority }, ErrInvalidDecisionRule},
		{"supermajority with no threshold", func(p *Pack) { lever(p, "city.bylaw").Threshold = "" }, ErrInvalidDecisionRule},
		{"supermajority of a half", func(p *Pack) { lever(p, "city.bylaw").Threshold = "1/2" }, ErrInvalidDecisionRule},
		{"threshold that is not a fraction", func(p *Pack) { lever(p, "city.bylaw").Threshold = "two thirds" }, ErrInvalidDecisionRule},
		{"threshold on a majority", func(p *Pack) { lever(p, "city.bylaw").DecisionRule = DecisionMajority }, ErrInvalidDecisionRule},
		{"quorum above the whole", func(p *Pack) { lever(p, "city.bylaw").Quorum = "6/5" }, ErrInvalidDecisionRule},
		{"quorum on a single decision", func(p *Pack) { lever(p, "city.tax_rate").Quorum = "1/2" }, ErrInvalidDecisionRule},
		{"veto by an undeclared office", func(p *Pack) { lever(p, "city.tax_rate").VetoBy = []string{"king"} }, ErrUnknownOfficeReference},
		{"override with nobody to veto", func(p *Pack) { lever(p, "city.tax_rate").VetoBy = nil }, ErrInvalidDecisionRule},
		{"override by a single person", func(p *Pack) {
			l := lever(p, "city.tax_rate")
			l.OverrideRule, l.OverrideThreshold = DecisionSingle, ""
		}, ErrInvalidDecisionRule},
		{"override supermajority with no threshold", func(p *Pack) { lever(p, "city.tax_rate").OverrideThreshold = "" }, ErrInvalidDecisionRule},

		// Offices.
		{"office with no code", func(p *Pack) {
			p.Offices = append(p.Offices, OfficeDef{Jurisdiction: CityLevel, Seats: 1, AcquiredBy: AcquiredByElection})
		}, ErrEmptyOfficeCode},
		{"duplicate office", func(p *Pack) { p.Offices = append(p.Offices, *office(p, "city_council")) }, ErrDuplicateOfficeCode},
		{"office at the world level", func(p *Pack) { office(p, "city_council").Jurisdiction = WorldLevel }, ErrInvalidJurisdictionLevel},
		{"office with no seats", func(p *Pack) { office(p, "city_council").Seats = 0 }, ErrInvalidOfficeSeats},
		{"office with absurd seats", func(p *Pack) { office(p, "city_council").Seats = MaxOfficeSeats + 1 }, ErrInvalidOfficeSeats},
		{"unknown acquisition", func(p *Pack) { office(p, "mayor").AcquiredBy = "inheritance" }, ErrInvalidAcquisition},
		{"office lists a lever it does not hold", func(p *Pack) {
			office(p, "city_council").Levers = []string{"city.bylaw", "city.tax_rate"}
		}, ErrOfficeLeverMismatch},
		{"office omits a lever it holds", func(p *Pack) { office(p, "mayor").Levers = nil }, ErrOfficeLeverMismatch},
		{"deputy undeclared", func(p *Pack) { office(p, "mayor").Deputy = "vice" }, ErrInvalidDeputy},
		{"deputy of itself", func(p *Pack) { office(p, "mayor").Deputy = "mayor" }, ErrInvalidDeputy},
		{"deputy at another level", func(p *Pack) { office(p, "mayor").Deputy = "president" }, ErrInvalidDeputy},
		{"deputy cycle", func(p *Pack) { office(p, "deputy_mayor").Deputy = "mayor" }, ErrDeputyCycle},
		{"appointed by an undeclared office", func(p *Pack) { office(p, "deputy_mayor").AppointedBy = "king" }, ErrInvalidAppointment},
		{"appointed but elected", func(p *Pack) { office(p, "deputy_mayor").AcquiredBy = AcquiredByElection }, ErrInvalidAppointment},
		{"confirmation by an undeclared body", func(p *Pack) { office(p, "deputy_mayor").RequiresConfirmationBy = "senate" }, ErrInvalidAppointment},
		{"confirmation of nobody's appointment", func(p *Pack) { office(p, "deputy_mayor").AppointedBy = "" }, ErrInvalidAppointment},
		{"term not a duration", func(p *Pack) { office(p, "mayor").Term = "four years" }, ErrInvalidOfficeTerm},
		{"term of zero", func(p *Pack) { office(p, "mayor").Term = "0s" }, ErrInvalidOfficeTerm},
		{"term limit without a term", func(p *Pack) { office(p, "mayor").Term = "" }, ErrInvalidOfficeTerm},
		{"term limit of zero", func(p *Pack) { office(p, "mayor").TermLimit = intp(0) }, ErrInvalidOfficeTerm},
		{"removable by an undeclared office", func(p *Pack) { office(p, "deputy_mayor").CanBeRemovedBy = []string{"king"} }, ErrUnknownOfficeReference},
		{"veto over something undeclared", func(p *Pack) { office(p, "president").VetoOver = []string{"city.curfew"} }, ErrUnknownOfficeReference},
		{"incompatible with an undeclared office", func(p *Pack) { office(p, "mayor").IncompatibleWith = []string{"judge"} }, ErrUnknownOfficeReference},
		{"incompatible with itself", func(p *Pack) { office(p, "mayor").IncompatibleWith = []string{"mayor"} }, ErrUnknownOfficeReference},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := governedPack()
			tc.mutate(p)
			err := p.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// A lever with no per-city source gives every city its own default; one with
// the tax source gives each city the rate cities.yml states for it.
func TestLeverDefaultFor(t *testing.T) {
	p := governedPack()
	p.Cities[1].TaxRateBPS = 1450
	tax, _ := p.Lever("city.tax_rate")
	if got := tax.DefaultFor(p.Cities[0]); got != 500 {
		t.Errorf("tax default for alpha = %d, want its tax_rate_bps 500", got)
	}
	if got := tax.DefaultFor(p.Cities[1]); got != 1450 {
		t.Errorf("tax default for bravo = %d, want its tax_rate_bps 1450", got)
	}
	tariff, _ := p.Lever("country.border_tariff")
	if got := tariff.DefaultFor(p.Cities[0]); got != 0 {
		t.Errorf("tariff default = %d, want the lever default 0", got)
	}
}

func TestLeverRuleDefaultsToSingle(t *testing.T) {
	if got := (LeverDef{}).Rule(); got != DecisionSingle {
		t.Errorf("an omitted decision_rule reads as %q, want %q", got, DecisionSingle)
	}
}

func TestLeverDurationsRoundTrip(t *testing.T) {
	for _, d := range []time.Duration{0, time.Second, 24 * time.Hour, 72 * time.Hour, 90 * time.Minute} {
		raw := FormatLeverDuration(int64(d / time.Second))
		got, err := parseLeverDuration(raw)
		if err != nil || got != d {
			t.Errorf("%v rendered as %q parses back to %v, %v", d, raw, got, err)
		}
	}
}

func TestParseFraction(t *testing.T) {
	for raw, want := range map[string][2]int{"2/3": {2, 3}, " 3 / 5 ": {3, 5}, "1/1": {1, 1}} {
		n, d, err := ParseFraction(raw)
		if err != nil || n != want[0] || d != want[1] {
			t.Errorf("ParseFraction(%q) = %d/%d, %v", raw, n, d, err)
		}
	}
	for _, raw := range []string{"", "2", "0/3", "4/3", "-1/2", "1/0", "a/b", "1/1001"} {
		if _, _, err := ParseFraction(raw); err == nil {
			t.Errorf("ParseFraction(%q) accepted", raw)
		}
	}
}

// governance.yml is parsed strictly like every other file: a misspelled key
// in a lever fails the load instead of loading as zero.
func TestLoadRejectsAMisspelledLeverKey(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"governance.yml": `version: 1
levers:
  - code: city.tax_rate
    jurisdiction: city
    type: bps
    defualt: 450
`,
	})
	_, err := Load(dir)
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("Load() = %v, want ErrUnknownField", err)
	}
	if !strings.Contains(err.Error(), "defualt") {
		t.Errorf("the error does not name the key: %v", err)
	}
}

// The shipped constitution: every city is in the one shipped country, the
// seed levers and offices exist, the mayor has a deputy, and — the promise
// that makes turning tax into a lever a no-op on the day it ships — each
// city's tax default is exactly the rate cities.yml gives it.
func TestShippedGovernance(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("the shipped content does not parse: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("the shipped content does not validate: %v", err)
	}

	levels := map[string]bool{}
	for _, l := range pack.Levels {
		levels[l.Code] = true
	}
	for _, want := range []string{"village", "town", CityLevel, "province", CountryLevel, "union", "port_authority", "free_zone"} {
		if !levels[want] {
			t.Errorf("level %q is not shipped", want)
		}
	}

	// Two countries (docs/adr/0022-military-and-diplomacy.md): the far end
	// of the map, where nobody is born, is the Vantor Federation.
	if len(pack.Jurisdictions) != 2 || pack.Jurisdictions[0].Code != "default_country" ||
		pack.Jurisdictions[1].Code != "vantor_federation" ||
		pack.Jurisdictions[0].Level != CountryLevel || pack.Jurisdictions[1].Level != CountryLevel {
		t.Fatalf("shipped jurisdictions = %+v, want the countries default_country and vantor_federation", pack.Jurisdictions)
	}
	for _, c := range pack.Cities {
		want := "default_country"
		if c.Code == "calderis" || c.Code == "vantor_reach" {
			want = "vantor_federation"
		}
		if c.Country != want {
			t.Errorf("city %s is in %q, want %s", c.Code, c.Country, want)
		}
		if want == "vantor_federation" && c.SpawnWeight != 0 {
			t.Errorf("city %s of the Federation is a birthplace (spawn weight %d)", c.Code, c.SpawnWeight)
		}
	}

	for code, want := range map[string]struct{ level, typ, office string }{
		"city.tax_rate":         {CityLevel, LeverBPS, "mayor"},
		"city.immigration_fee":  {CityLevel, LeverMoney, "mayor"},
		"country.border_tariff": {CountryLevel, LeverBPS, "president"},
	} {
		l, ok := pack.Lever(code)
		if !ok {
			t.Errorf("lever %s is not shipped", code)
			continue
		}
		if l.Jurisdiction != want.level || l.Type != want.typ || l.HeldBy != want.office || l.Rule() != DecisionSingle {
			t.Errorf("lever %s = %s/%s held by %s (%s), want %s/%s held by %s (single)",
				code, l.Jurisdiction, l.Type, l.HeldBy, l.Rule(), want.level, want.typ, want.office)
		}
	}
	if l, _ := pack.Lever("country.border_tariff"); l.DefaultValue() != 0 {
		t.Errorf("border tariff default = %d, want 0", l.DefaultValue())
	}

	tax, _ := pack.Lever("city.tax_rate")
	for _, c := range pack.Cities {
		if got := tax.DefaultFor(c); got != int64(c.TaxRateBPS) {
			t.Errorf("city %s: tax default %d, want its tax_rate_bps %d", c.Code, got, c.TaxRateBPS)
		}
	}

	for code, seats := range map[string]int{"mayor": 1, "deputy_mayor": 1, "city_council": 5, "president": 1} {
		o, ok := pack.Office(code)
		if !ok || o.Seats != seats {
			t.Errorf("office %s: shipped %v with %d seats, want %d", code, ok, o.Seats, seats)
		}
	}
	if mayor, _ := pack.Office("mayor"); mayor.Deputy != "deputy_mayor" {
		t.Errorf("the mayor's deputy is %q, want deputy_mayor", mayor.Deputy)
	}
}
