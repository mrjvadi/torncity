package content

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/world"
)

// Governance validation failures (ADR 0015). Wrapped with the offender, like
// every other content failure.
var (
	// ErrEmptyLevelCode means a level arrived without its code.
	ErrEmptyLevelCode = errors.New("content: level code is required")

	// ErrDuplicateLevel means two levels claimed the same code.
	ErrDuplicateLevel = errors.New("content: duplicate level code")

	// ErrReservedLevel means a level, or a jurisdiction, used "world", which
	// is the implicit root and belongs to the operator.
	ErrReservedLevel = errors.New(`content: the level "world" is reserved for the root`)

	// ErrUnknownLevel means a jurisdiction, lever or office named a level
	// that levels does not declare — or the pack has cities and does not
	// declare the "city" level.
	ErrUnknownLevel = errors.New("content: unknown jurisdiction level")

	// ErrLevelParents means a level listed no parents, or a parent that is
	// neither "world" nor a declared level.
	ErrLevelParents = errors.New("content: level parents must be world or declared levels")

	// ErrLevelCycle means the levels' parent rules loop, so no jurisdiction
	// tree could satisfy them.
	ErrLevelCycle = errors.New("content: level parents form a cycle")

	// ErrEmptyJurisdictionCode means a jurisdiction arrived without its code.
	ErrEmptyJurisdictionCode = errors.New("content: jurisdiction code is required")

	// ErrDuplicateJurisdictionCode means two jurisdictions claimed one code,
	// or a jurisdiction claimed a city's code: a parent reference must never
	// be ambiguous.
	ErrDuplicateJurisdictionCode = errors.New("content: duplicate jurisdiction code")

	// ErrCityJurisdictionDeclared means governance.yml declared a
	// jurisdiction of level "city". Cities are the entries of cities.yml.
	ErrCityJurisdictionDeclared = errors.New("content: city jurisdictions are declared in cities.yml, not governance.yml")

	// ErrUnknownJurisdictionParent means a parent names nothing declared.
	ErrUnknownJurisdictionParent = errors.New("content: jurisdiction parent is not declared")

	// ErrJurisdictionParentLevel means a jurisdiction sits under a level its
	// own level's parent rule does not allow.
	ErrJurisdictionParentLevel = errors.New("content: jurisdiction parent is of a level its level does not allow")

	// ErrCityCountryRequired means a city named no country. Every city
	// belongs to one: country levers and offices reach a city through it.
	ErrCityCountryRequired = errors.New("content: city country is required")

	// ErrUnknownCityCountry means a city named a country governance.yml does
	// not declare as a jurisdiction of level "country".
	ErrUnknownCityCountry = errors.New("content: city references an unknown country")

	// ErrEmptyLeverCode means a lever arrived without its code.
	ErrEmptyLeverCode = errors.New("content: lever code is required")

	// ErrDuplicateLeverCode means two levers claimed the same code.
	ErrDuplicateLeverCode = errors.New("content: duplicate lever code")

	// ErrInvalidJurisdictionLevel means a lever or an office named the world
	// level, or none. The world is the operator's: it holds no levers.
	ErrInvalidJurisdictionLevel = errors.New("content: levers and offices need a declared level other than world")

	// ErrLeverCodePrefix means a lever's code does not start with its level,
	// as in "city.tax_rate". The prefix is what lets a reader of any call
	// site see which jurisdiction a lever is asked of.
	ErrLeverCodePrefix = errors.New("content: lever code must start with its jurisdiction level")

	// ErrInvalidLeverType means a type outside the closed set.
	ErrInvalidLeverType = errors.New("content: lever type must be bps, money, int, bool, enum, map or allocation")

	// ErrLeverBoundRequired means a scalar lever omitted default, min or max,
	// or gave a default that is not a whole number. All three are required
	// because zero is a meaningful value for each.
	ErrLeverBoundRequired = errors.New("content: scalar lever default, min and max are required whole numbers")

	// ErrInvalidLeverShape means a structured lever whose default or
	// parameters do not fit its type — an enum default not among its options,
	// an allocation that does not add up to the whole — or a lever carrying
	// parameters its type does not have (min on a map, options on a rate).
	ErrInvalidLeverShape = errors.New("content: lever default or parameters do not fit its type")

	// ErrLeverBoundsInverted means min is above max: no value would be
	// allowed at all.
	ErrLeverBoundsInverted = errors.New("content: lever min must not exceed max")

	// ErrLeverBoundsOutOfRange means a bps lever's bounds leave 0..10000.
	ErrLeverBoundsOutOfRange = errors.New("content: bps lever bounds must lie within 0..10000")

	// ErrLeverDefaultOutOfBounds means a default — the lever's own, or one
	// a city supplies — lies outside [min, max]. The world would start in a
	// state no office holder is allowed to put it in.
	ErrLeverDefaultOutOfBounds = errors.New("content: lever default must lie within [min, max]")

	// ErrUnknownCityDefault means city_default names a source that does not
	// exist, or is set on a lever that is not a city lever.
	ErrUnknownCityDefault = errors.New("content: unknown city_default source")

	// ErrUnknownLeverOffice means held_by names no declared office.
	ErrUnknownLeverOffice = errors.New("content: lever held_by references an unknown office")

	// ErrLeverOfficeLevel means a lever is held by an office of another
	// level: a mayor cannot hold a country's lever.
	ErrLeverOfficeLevel = errors.New("content: lever and the office holding it must share a jurisdiction level")

	// ErrInvalidLeverDuration means change_cooldown or notice is missing,
	// unparseable, negative or not whole seconds.
	ErrInvalidLeverDuration = errors.New("content: invalid lever duration")

	// ErrInvalidDecisionRule means a decision rule, threshold, quorum or
	// veto override that is not one of the rules, or does not fit the office
	// deciding: a single decision needs a one-seat office (and one-seat
	// deputies), a vote needs a body of at least two seats.
	ErrInvalidDecisionRule = errors.New("content: invalid decision rule")

	// ErrEmptyOfficeCode means an office arrived without its code.
	ErrEmptyOfficeCode = errors.New("content: office code is required")

	// ErrDuplicateOfficeCode means two offices claimed the same code.
	ErrDuplicateOfficeCode = errors.New("content: duplicate office code")

	// ErrInvalidOfficeSeats means an office with fewer than one seat, or more
	// than MaxOfficeSeats.
	ErrInvalidOfficeSeats = errors.New("content: office seats must be at least 1")

	// ErrInvalidAcquisition means acquired_by is not election, appointment,
	// founding or conquest.
	ErrInvalidAcquisition = errors.New("content: office acquired_by must be election, appointment, founding or conquest")

	// ErrOfficeLeverMismatch means an office's levers list and the levers'
	// held_by disagree.
	ErrOfficeLeverMismatch = errors.New("content: office levers and lever held_by disagree")

	// ErrInvalidDeputy means a deputy that is not a declared office of the
	// same level, or the office itself.
	ErrInvalidDeputy = errors.New("content: invalid deputy")

	// ErrDeputyCycle means deputies loop back to an office already in the
	// chain. The resolver walks the chain while offices are vacant, and a
	// loop has no end.
	ErrDeputyCycle = errors.New("content: deputy chain forms a cycle")

	// ErrInvalidAppointment means appointed_by or requires_confirmation_by
	// names no declared office or the office itself, appointed_by is set on
	// an office not acquired by appointment, or a confirmation is required of
	// an appointment nobody makes.
	ErrInvalidAppointment = errors.New("content: invalid appointment chain")

	// ErrInvalidOfficeTerm means a term that is not a positive whole-second
	// duration, or a term limit below one or without a term.
	ErrInvalidOfficeTerm = errors.New("content: invalid office term")

	// ErrUnknownOfficeReference means can_be_removed_by, veto_over, veto_by
	// or incompatible_with names something that is not declared.
	ErrUnknownOfficeReference = errors.New("content: reference to an undeclared office or lever")
)

// MaxOfficeSeats bounds seats so a typo (500 instead of 5) cannot create
// hundreds of office rows in every city on the next load.
const MaxOfficeSeats = 100

// validateGovernance checks everything ADR 0015 adds, in dependency order:
// levels, then the jurisdictions and cities placed on them, then offices,
// then the levers that name offices, then the links between offices.
func (p *Pack) validateGovernance(problems *[]error) {
	levels := p.validateLevels(problems)
	declared := p.validateJurisdictions(levels, problems)
	p.validateCityCountries(levels, declared, problems)
	offices := p.validateOffices(levels, problems)
	leverCodes := p.validateLevers(levels, offices, problems)
	p.validateOfficeLinks(offices, leverCodes, problems)
	p.validateActions(levels, offices, problems)
}

// validateLevels checks the level list and returns the valid ones by code.
func (p *Pack) validateLevels(problems *[]error) map[string]LevelDef {
	levels := make(map[string]LevelDef, len(p.Levels))
	for i, l := range p.Levels {
		where := fmt.Sprintf("levels[%d]", i)
		switch {
		case l.Code == "":
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrEmptyLevelCode, where))
			continue
		case l.Code == WorldLevel:
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrReservedLevel, where))
			continue
		}
		if _, dup := levels[l.Code]; dup {
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateLevel, l.Code, where))
			continue
		}
		levels[l.Code] = l
	}

	for i, l := range p.Levels {
		if _, ok := levels[l.Code]; !ok {
			continue
		}
		if len(l.Parents) == 0 {
			*problems = append(*problems, fmt.Errorf("%w: levels[%d] %q lists none", ErrLevelParents, i, l.Code))
		}
		for _, parent := range l.Parents {
			if _, ok := levels[parent]; (!ok && parent != WorldLevel) || parent == l.Code {
				*problems = append(*problems, fmt.Errorf("%w: levels[%d] %q lists %q",
					ErrLevelParents, i, l.Code, parent))
			}
		}
	}

	if cycle := levelCycle(levels); cycle != "" {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrLevelCycle, cycle))
	}

	if _, ok := levels[CityLevel]; len(p.Cities) > 0 && !ok {
		*problems = append(*problems, fmt.Errorf("%w: the pack has cities, so levels must declare %q",
			ErrUnknownLevel, CityLevel))
	}
	return levels
}

// levelCycle returns a description of one cycle in the parent rules, or "".
func levelCycle(levels map[string]LevelDef) string {
	codes := make([]string, 0, len(levels))
	for c := range levels {
		codes = append(codes, c)
	}
	sort.Strings(codes)

	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(levels))
	var path []string
	var visit func(code string) string
	visit = func(code string) string {
		switch state[code] {
		case visiting:
			return strings.Join(append(path, code), " -> ")
		case done:
			return ""
		}
		state[code] = visiting
		path = append(path, code)
		for _, parent := range levels[code].Parents {
			if _, ok := levels[parent]; !ok || parent == code {
				continue // reported as ErrLevelParents
			}
			if c := visit(parent); c != "" {
				return c
			}
		}
		path = path[:len(path)-1]
		state[code] = done
		return ""
	}
	for _, code := range codes {
		if c := visit(code); c != "" {
			return c
		}
	}
	return ""
}

// validateJurisdictions checks governance.yml's jurisdictions and returns the
// valid ones by code.
func (p *Pack) validateJurisdictions(levels map[string]LevelDef, problems *[]error) map[string]JurisdictionDef {
	cityCodes := make(map[string]struct{}, len(p.Cities))
	for _, c := range p.Cities {
		cityCodes[c.Code] = struct{}{}
	}

	declared := make(map[string]JurisdictionDef, len(p.Jurisdictions))
	for i, j := range p.Jurisdictions {
		where := fmt.Sprintf("jurisdictions[%d]", i)
		if j.Code == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrEmptyJurisdictionCode, where))
			continue
		}
		if _, dup := declared[j.Code]; dup {
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateJurisdictionCode, j.Code, where))
			continue
		}
		if _, clash := cityCodes[j.Code]; clash {
			*problems = append(*problems, fmt.Errorf("%w: %s %q is also a city code", ErrDuplicateJurisdictionCode, where, j.Code))
			continue
		}
		declared[j.Code] = j

		switch _, ok := levels[j.Level]; {
		case j.Level == WorldLevel:
			*problems = append(*problems, fmt.Errorf("%w: %s %q", ErrReservedLevel, where, j.Code))
		case j.Level == CityLevel:
			*problems = append(*problems, fmt.Errorf("%w: %s %q", ErrCityJurisdictionDeclared, where, j.Code))
		case !ok:
			*problems = append(*problems, fmt.Errorf("%w: %s %q has level %q", ErrUnknownLevel, where, j.Code, j.Level))
		}
	}

	for i, j := range p.Jurisdictions {
		if d, ok := declared[j.Code]; !ok || d.Level != j.Level || d.Parent != j.Parent {
			continue
		}
		level, ok := levels[j.Level]
		if !ok {
			continue
		}
		parentLevel := WorldLevel
		if j.Parent != "" {
			if parent, ok := declared[j.Parent]; ok {
				parentLevel = parent.Level
			} else if _, ok := cityCodes[j.Parent]; ok {
				parentLevel = CityLevel
			} else {
				*problems = append(*problems, fmt.Errorf("%w: jurisdictions[%d] %q names %q",
					ErrUnknownJurisdictionParent, i, j.Code, j.Parent))
				continue
			}
		}
		if !contains(level.Parents, parentLevel) {
			*problems = append(*problems, fmt.Errorf("%w: jurisdictions[%d] %q is a %s under a %s; %s may sit under %v",
				ErrJurisdictionParentLevel, i, j.Code, j.Level, parentLevel, j.Level, level.Parents))
		}
	}
	return declared
}

// validateCityCountries checks that every city names a declared country, and
// that a country is a parent the city level allows.
func (p *Pack) validateCityCountries(levels map[string]LevelDef, declared map[string]JurisdictionDef, problems *[]error) {
	cityLevel, haveCityLevel := levels[CityLevel]
	if haveCityLevel && len(p.Cities) > 0 && !contains(cityLevel.Parents, CountryLevel) {
		*problems = append(*problems, fmt.Errorf("%w: cities sit under their country, so level %q must allow %q as a parent",
			ErrJurisdictionParentLevel, CityLevel, CountryLevel))
	}
	for i, c := range p.Cities {
		where := fmt.Sprintf("cities[%d]", i)
		if c.Country == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s %q", ErrCityCountryRequired, where, c.Code))
			continue
		}
		if j, ok := declared[c.Country]; !ok || j.Level != CountryLevel {
			*problems = append(*problems, fmt.Errorf("%w: %s %q names %q, which governance.yml does not declare as a country",
				ErrUnknownCityCountry, where, c.Code, c.Country))
		}
	}
}

// validateOffices checks each office on its own and returns the valid ones by
// code. References between offices are checked by validateOfficeLinks, once
// every code is known.
func (p *Pack) validateOffices(levels map[string]LevelDef, problems *[]error) map[string]OfficeDef {
	offices := make(map[string]OfficeDef, len(p.Offices))
	for i, o := range p.Offices {
		where := fmt.Sprintf("offices[%d]", i)
		if o.Code == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrEmptyOfficeCode, where))
			continue
		}
		if _, dup := offices[o.Code]; dup {
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateOfficeCode, o.Code, where))
			continue
		}
		offices[o.Code] = o
		name := fmt.Sprintf("%s %q", where, o.Code)

		if _, ok := levels[o.Jurisdiction]; !ok || o.Jurisdiction == WorldLevel {
			*problems = append(*problems, fmt.Errorf("%w: %s has %q", ErrInvalidJurisdictionLevel, name, o.Jurisdiction))
		}
		if o.Seats < 1 || o.Seats > MaxOfficeSeats {
			*problems = append(*problems, fmt.Errorf("%w and at most %d: %s has %d",
				ErrInvalidOfficeSeats, MaxOfficeSeats, name, o.Seats))
		}
		switch o.AcquiredBy {
		case AcquiredByElection, AcquiredByAppointment, AcquiredByFounding, AcquiredByConquest:
		default:
			*problems = append(*problems, fmt.Errorf("%w: %s has %q", ErrInvalidAcquisition, name, o.AcquiredBy))
		}

		if o.Term != "" {
			if d, err := o.TermDuration(); err != nil || d <= 0 {
				*problems = append(*problems, fmt.Errorf("%w: %s term %q must be a positive whole-second duration",
					ErrInvalidOfficeTerm, name, o.Term))
			}
		}
		if o.TermLimit != nil && (*o.TermLimit < 1 || o.Term == "") {
			*problems = append(*problems, fmt.Errorf("%w: %s term_limit %d needs a term and must be at least 1",
				ErrInvalidOfficeTerm, name, *o.TermLimit))
		}
	}
	return offices
}

// validateLevers checks every lever against its bounds, its office, its
// decision rule and the cities that may supply its default. It returns the
// declared lever codes.
func (p *Pack) validateLevers(levels map[string]LevelDef, offices map[string]OfficeDef, problems *[]error) map[string]struct{} {
	seen := make(map[string]struct{}, len(p.Levers))

	for i, l := range p.Levers {
		where := fmt.Sprintf("levers[%d]", i)
		if l.Code == "" {
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrEmptyLeverCode, where))
			continue
		}
		if _, dup := seen[l.Code]; dup {
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateLeverCode, l.Code, where))
			continue
		}
		seen[l.Code] = struct{}{}
		name := fmt.Sprintf("%s %q", where, l.Code)

		if _, ok := levels[l.Jurisdiction]; !ok || l.Jurisdiction == WorldLevel {
			*problems = append(*problems, fmt.Errorf("%w: %s has %q", ErrInvalidJurisdictionLevel, name, l.Jurisdiction))
		} else if !strings.HasPrefix(l.Code, l.Jurisdiction+".") {
			*problems = append(*problems, fmt.Errorf("%w: %s is a %s lever and should be named %s.…",
				ErrLeverCodePrefix, name, l.Jurisdiction, l.Jurisdiction))
		}

		switch l.ValueKind() {
		case ValueKindScalar:
			p.validateLeverBounds(l, name, problems)
		case ValueKindStructured:
			validateStructuredLever(l, name, problems)
		default:
			*problems = append(*problems, fmt.Errorf("%w: %s has %q", ErrInvalidLeverType, name, l.Type))
		}

		for _, d := range [2][2]string{{"change_cooldown", l.ChangeCooldown}, {"notice", l.Notice}} {
			if _, err := parseLeverDuration(d[1]); err != nil {
				*problems = append(*problems, fmt.Errorf("%w: %s %s %v", ErrInvalidLeverDuration, name, d[0], err))
			}
		}

		office, ok := offices[l.HeldBy]
		switch {
		case !ok:
			*problems = append(*problems, fmt.Errorf("%w: %s is held by %q, which no office declares",
				ErrUnknownLeverOffice, name, l.HeldBy))
		case office.Jurisdiction != l.Jurisdiction:
			*problems = append(*problems, fmt.Errorf("%w: %s is a %s lever held by %q, a %s office",
				ErrLeverOfficeLevel, name, l.Jurisdiction, l.HeldBy, office.Jurisdiction))
		default:
			validateDecisionRule(l, name, offices, problems)
		}

		for _, v := range l.VetoBy {
			if _, ok := offices[v]; !ok {
				*problems = append(*problems, fmt.Errorf("%w: %s veto_by names %q", ErrUnknownOfficeReference, name, v))
			}
		}
		validateOverride(l, name, problems)
		validateConfirmation(confirmation{by: l.RequiresConfirmationBy, rule: l.ConfirmationRule,
			threshold: l.ConfirmationThreshold, quorum: l.ConfirmationQuorum, level: l.Jurisdiction, holder: l.HeldBy},
			name, offices, problems)
		switch {
		case l.ConfirmAbove != nil && l.RequiresConfirmationBy == "":
			*problems = append(*problems, fmt.Errorf("%w: %s has confirm_above but nobody confirms it", ErrInvalidDecisionRule, name))
		case l.ConfirmAbove != nil && LeverValueKind(l.Type) != ValueKindScalar:
			*problems = append(*problems, fmt.Errorf("%w: %s has confirm_above, which only a scalar lever has", ErrInvalidDecisionRule, name))
		case l.ConfirmAbove != nil && *l.ConfirmAbove < 0:
			*problems = append(*problems, fmt.Errorf("%w: %s has a negative confirm_above", ErrInvalidDecisionRule, name))
		}
		if l.RequiresConfirmationBy != "" && l.Rule() != DecisionSingle {
			*problems = append(*problems, fmt.Errorf("%w: %s is decided by a vote and also needs confirmation; declare one",
				ErrInvalidDecisionRule, name))
		}
	}
	return seen
}

// validateDecisionRule checks how the holding office decides.
func validateDecisionRule(l LeverDef, name string, offices map[string]OfficeDef, problems *[]error) {
	office := offices[l.HeldBy]
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s "+format, append([]any{ErrInvalidDecisionRule, name}, args...)...))
	}

	switch l.Rule() {
	case DecisionSingle:
		// One person decides, so every office that may act for the lever —
		// the holder and each deputy in turn — must be one seat. A
		// five-seat council "deciding alone" is a vote nobody wrote down.
		for _, code := range deputyChain(l.HeldBy, offices) {
			if o := offices[code]; o.Seats != 1 {
				bad("is decided by one person, but %q (in its acting chain) has %d seats; declare a vote instead", code, o.Seats)
			}
		}
		if l.Quorum != "" {
			bad("has a quorum, which only a vote has")
		}
	case DecisionMajority, DecisionSupermajority, DecisionUnanimous:
		if office.Seats < 2 {
			bad("needs a vote of %q, which has %d seat", l.HeldBy, office.Seats)
		}
		if l.Quorum != "" {
			if _, _, err := ParseFraction(l.Quorum); err != nil {
				bad("quorum %v", err)
			}
		}
	default:
		bad("decision_rule %q is not single, majority, supermajority or unanimous", l.DecisionRule)
		return
	}
	validateThreshold(l.Rule(), l.Threshold, "threshold", bad)
}

// confirmation is what a lever or an action says about the body that must
// confirm it.
type confirmation struct {
	by, rule, threshold, quorum string
	// level is the lever's or action's level; holder the office deciding.
	level, holder string
}

// validateConfirmation checks requires_confirmation_by and its rule: the
// body is a declared office of the same level other than the holder, the rule
// is a vote, a supermajority states its threshold, a quorum is a fraction.
// With no body, none of the rest may be set.
func validateConfirmation(c confirmation, name string, offices map[string]OfficeDef, problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s "+format, append([]any{ErrInvalidDecisionRule, name}, args...)...))
	}
	if c.by == "" {
		if c.rule != "" || c.threshold != "" || c.quorum != "" {
			bad("has a confirmation rule, threshold or quorum but nobody confirms it")
		}
		return
	}
	o, ok := offices[c.by]
	switch {
	case !ok:
		bad("requires_confirmation_by names %q, which no office declares", c.by)
		return
	case o.Jurisdiction != c.level:
		bad("is confirmed by %q, an office of another level", c.by)
	case c.by == c.holder:
		bad("is confirmed by the office that decides it")
	}
	rule := c.rule
	if rule == "" {
		rule = DecisionMajority
	}
	switch rule {
	case DecisionMajority, DecisionSupermajority, DecisionUnanimous:
	default:
		bad("confirmation_rule %q is not majority, supermajority or unanimous", c.rule)
		return
	}
	validateThreshold(rule, c.threshold, "confirmation_threshold", bad)
	if c.quorum != "" {
		if _, _, err := ParseFraction(c.quorum); err != nil {
			bad("confirmation_quorum %v", err)
		}
	}
}

// validateOverride checks the veto override rule.
func validateOverride(l LeverDef, name string, problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s "+format, append([]any{ErrInvalidDecisionRule, name}, args...)...))
	}
	if l.OverrideRule == "" {
		if l.OverrideThreshold != "" {
			bad("has an override_threshold but no override_rule")
		}
		return
	}
	if len(l.VetoBy) == 0 {
		bad("has an override_rule but nobody may veto it")
	}
	switch l.OverrideRule {
	case DecisionMajority, DecisionSupermajority, DecisionUnanimous:
	default:
		bad("override_rule %q is not majority, supermajority or unanimous", l.OverrideRule)
		return
	}
	validateThreshold(l.OverrideRule, l.OverrideThreshold, "override_threshold", bad)
}

// validateThreshold requires a fraction above one half for a supermajority,
// and refuses one for every other rule.
func validateThreshold(rule, threshold, field string, bad func(string, ...any)) {
	if rule != DecisionSupermajority {
		if threshold != "" {
			bad("has a %s, which only a supermajority has", field)
		}
		return
	}
	num, den, err := ParseFraction(threshold)
	switch {
	case threshold == "":
		bad("is a supermajority with no %s", field)
	case err != nil:
		bad("%s %v", field, err)
	case 2*num <= den:
		bad("%s %s is not more than a half; that is a majority", field, threshold)
	}
}

// validateLeverBounds checks a scalar lever's min, max and every default it
// can take.
func (p *Pack) validateLeverBounds(l LeverDef, name string, problems *[]error) {
	if len(l.Options) > 0 || l.Key != "" || len(l.Categories) > 0 {
		*problems = append(*problems, fmt.Errorf("%w: %s is a %s lever and takes no options, key or categories",
			ErrInvalidLeverShape, name, l.Type))
	}
	def, whole := integer(l.Default)
	if !whole || l.Min == nil || l.Max == nil {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrLeverBoundRequired, name))
		return
	}
	lo, hi := *l.Min, *l.Max
	if lo > hi {
		*problems = append(*problems, fmt.Errorf("%w: %s has min %d and max %d", ErrLeverBoundsInverted, name, lo, hi))
		return
	}
	if l.Type == LeverBPS && (lo < 0 || hi > world.BasisPointsScale) {
		*problems = append(*problems, fmt.Errorf("%w: %s has [%d, %d]", ErrLeverBoundsOutOfRange, name, lo, hi))
	}
	if d := def; d < lo || d > hi {
		*problems = append(*problems, fmt.Errorf("%w: %s default %d is outside [%d, %d]",
			ErrLeverDefaultOutOfBounds, name, d, lo, hi))
	}

	if l.CityDefault == "" {
		return
	}
	if l.CityDefault != CityDefaultTaxRate || l.Jurisdiction != CityLevel {
		*problems = append(*problems, fmt.Errorf("%w: %s has city_default %q (the only source is %q, on a city lever)",
			ErrUnknownCityDefault, name, l.CityDefault, CityDefaultTaxRate))
		return
	}
	// Each city's own default must be a value the mayor could set: otherwise
	// the city starts somewhere the constitution forbids.
	for _, c := range p.Cities {
		if d := l.DefaultFor(c); d < lo || d > hi {
			*problems = append(*problems, fmt.Errorf("%w: %s default for city %q (%s %d) is outside [%d, %d]",
				ErrLeverDefaultOutOfBounds, name, c.Code, l.CityDefault, d, lo, hi))
		}
	}
}

// validateStructuredLever checks a structured lever's default against its
// type's shape. Nothing reads these values yet; checking them now means the
// day their behaviour is built, every stored default is already one it can
// use.
func validateStructuredLever(l LeverDef, name string, problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s "+format, append([]any{ErrInvalidLeverShape, name}, args...)...))
	}
	if l.Min != nil || l.Max != nil || l.CityDefault != "" {
		bad("is a %s lever and has no min, max or city_default", l.Type)
	}
	if l.Default == nil {
		bad("has no default")
		return
	}
	if l.Type != LeverEnum && l.Type != LeverMap && len(l.Options) > 0 {
		bad("is a %s lever and takes no options", l.Type)
	}
	if l.Type != LeverMap && l.Key != "" {
		bad("is a %s lever and takes no key", l.Type)
	}
	if l.Type != LeverAllocation && len(l.Categories) > 0 {
		bad("is a %s lever and takes no categories", l.Type)
	}

	switch l.Type {
	case LeverBool:
		if _, ok := l.Default.(bool); !ok {
			bad("default %v is not true or false", l.Default)
		}
	case LeverEnum:
		if !distinct(l.Options) {
			bad("needs distinct, non-empty options")
		}
		if v, ok := l.Default.(string); !ok || !contains(l.Options, v) {
			bad("default %v is not one of %v", l.Default, l.Options)
		}
	case LeverMap:
		if l.Key == "" {
			bad("needs a key naming what it maps")
		}
		if !distinct(l.Options) {
			bad("needs distinct, non-empty options")
		}
		m, ok := asMap(l.Default)
		if !ok {
			bad("default %v is not a mapping", l.Default)
			return
		}
		for k, v := range m {
			if s, ok := v.(string); !ok || !contains(l.Options, s) {
				bad("default maps %q to %v, which is not one of %v", k, v, l.Options)
			}
		}
	case LeverAllocation:
		if !distinct(l.Categories) {
			bad("needs distinct, non-empty categories")
		}
		m, ok := asMap(l.Default)
		if !ok {
			bad("default %v is not a mapping of category to bps", l.Default)
			return
		}
		var total int64
		for k, v := range m {
			n, whole := integer(v)
			switch {
			case !contains(l.Categories, k):
				bad("default allocates to %q, which is not one of %v", k, l.Categories)
			case !whole || n < 0:
				bad("default gives %q %v, which is not a whole number of bps", k, v)
			default:
				total += n
			}
		}
		if total > world.BasisPointsScale {
			bad("default allocates %d bps; an allocation divides at most the whole, %d", total, world.BasisPointsScale)
		}
	}
}

// asMap reads a decoded mapping: yaml and JSON both decode one with string
// keys as map[string]any.
func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// distinct reports whether list is non-empty with no empty or repeated entry.
func distinct(list []string) bool {
	if len(list) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(list))
	for _, s := range list {
		if _, dup := seen[s]; dup || s == "" {
			return false
		}
		seen[s] = struct{}{}
	}
	return true
}

// validateOfficeLinks checks what offices say about each other and about
// levers: the levers each holds, deputies, appointment chains, removal,
// vetoes and incompatibilities.
func (p *Pack) validateOfficeLinks(offices map[string]OfficeDef, levers map[string]struct{}, problems *[]error) {
	heldBy := map[string][]string{}
	for _, l := range p.Levers {
		if _, ok := offices[l.HeldBy]; ok && l.Code != "" {
			heldBy[l.HeldBy] = append(heldBy[l.HeldBy], l.Code)
		}
	}

	for i, o := range p.Offices {
		if d, ok := offices[o.Code]; !ok || d.Seats != o.Seats || d.Jurisdiction != o.Jurisdiction {
			continue
		}
		name := fmt.Sprintf("offices[%d] %q", i, o.Code)
		add := func(err error, format string, args ...any) {
			*problems = append(*problems, fmt.Errorf("%w: %s "+format, append([]any{err, name}, args...)...))
		}

		listed := append([]string(nil), o.Levers...)
		actual := append([]string(nil), heldBy[o.Code]...)
		sort.Strings(listed)
		sort.Strings(actual)
		if strings.Join(listed, ",") != strings.Join(actual, ",") {
			add(ErrOfficeLeverMismatch, "lists [%s] but the levers held by it are [%s]",
				strings.Join(listed, ", "), strings.Join(actual, ", "))
		}

		if o.Deputy != "" {
			d, ok := offices[o.Deputy]
			switch {
			case !ok || o.Deputy == o.Code:
				add(ErrInvalidDeputy, "names %q", o.Deputy)
			case d.Jurisdiction != o.Jurisdiction:
				add(ErrInvalidDeputy, "is a %s office with a %s deputy %q", o.Jurisdiction, d.Jurisdiction, o.Deputy)
			}
		}

		if o.AppointedBy != "" {
			if _, ok := offices[o.AppointedBy]; !ok || o.AppointedBy == o.Code {
				add(ErrInvalidAppointment, "appointed_by names %q", o.AppointedBy)
			}
			if o.AcquiredBy != AcquiredByAppointment {
				add(ErrInvalidAppointment, "has appointed_by but is acquired by %s", o.AcquiredBy)
			}
		}
		if o.RequiresConfirmationBy != "" {
			if _, ok := offices[o.RequiresConfirmationBy]; !ok || o.RequiresConfirmationBy == o.Code {
				add(ErrInvalidAppointment, "requires_confirmation_by names %q", o.RequiresConfirmationBy)
			}
			if o.AppointedBy == "" {
				add(ErrInvalidAppointment, "requires confirmation of an appointment nobody makes (no appointed_by)")
			}
		}

		for _, r := range o.CanBeRemovedBy {
			if _, ok := offices[r]; !ok && r != RemovableByResidents && r != RemovableByOperator {
				add(ErrUnknownOfficeReference, "can_be_removed_by names %q", r)
			}
		}
		for _, v := range o.VetoOver {
			_, isOffice := offices[v]
			_, isLever := levers[v]
			if !isOffice && !isLever {
				add(ErrUnknownOfficeReference, "veto_over names %q, which is neither an office nor a lever", v)
			}
		}
		for _, x := range o.IncompatibleWith {
			if _, ok := offices[x]; !ok || x == o.Code {
				add(ErrUnknownOfficeReference, "incompatible_with names %q", x)
			}
		}
	}

	if cycle := deputyCycle(offices); cycle != "" {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrDeputyCycle, cycle))
	}
}

// deputyChain is code followed by its deputy, the deputy's deputy and so on,
// stopping at the first office already seen, so it terminates on a cycle
// (which deputyCycle reports).
func deputyChain(code string, offices map[string]OfficeDef) []string {
	var chain []string
	seen := map[string]struct{}{}
	for code != "" {
		if _, loop := seen[code]; loop {
			break
		}
		o, ok := offices[code]
		if !ok {
			break
		}
		seen[code] = struct{}{}
		chain = append(chain, code)
		code = o.Deputy
	}
	return chain
}

// deputyCycle returns a description of one deputy cycle, or "".
func deputyCycle(offices map[string]OfficeDef) string {
	codes := make([]string, 0, len(offices))
	for c := range offices {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, start := range codes {
		path := []string{start}
		seen := map[string]struct{}{start: {}}
		for next := offices[start].Deputy; next != ""; next = offices[next].Deputy {
			if _, ok := offices[next]; !ok {
				break
			}
			path = append(path, next)
			if _, loop := seen[next]; loop {
				return strings.Join(path, " -> ")
			}
			seen[next] = struct{}{}
		}
	}
	return ""
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Lever returns the lever with this code, if the pack declares one.
func (p *Pack) Lever(code string) (LeverDef, bool) {
	for _, l := range p.Levers {
		if l.Code == code {
			return l, true
		}
	}
	return LeverDef{}, false
}

// Office returns the office with this code, if the pack declares one.
func (p *Pack) Office(code string) (OfficeDef, bool) {
	for _, o := range p.Offices {
		if o.Code == code {
			return o, true
		}
	}
	return OfficeDef{}, false
}
