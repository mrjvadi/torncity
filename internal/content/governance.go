package content

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// This file holds the governance content of docs/adr/0015-player-held-offices.md:
// the LEVELS of authority (village to union), the JURISDICTIONS above the
// city, the LEVERS — every political or economic decision a player may take
// over — and the OFFICES that hold them.
//
// It is content like any other and moves through the same pipeline: parsed by
// Load from governance.yml, judged by Validate, written as part of a numbered
// version by the content store. There is no second mechanism.
//
// # What the operator writes here, and what a player decides
//
// The operator writes the constitution: which levels and offices exist, which
// levers exist, the bounds each may move within, how often and with how much
// warning it may change, and who decides it. The value inside those bounds is
// the office holder's. When nobody holds the office, or nobody has ever moved
// the lever, its default applies, and the default is the behaviour the game
// had before the lever existed. See ADR 0015, sections 1 and 2.
//
// # Stored now, behaviour later
//
// Several fields describe real-world mechanics whose behaviour does not exist
// yet: collective decisions (majority, supermajority, quorum), appointment
// chains and confirmation, terms and term limits, removal, vetoes and their
// override, overlay jurisdictions. They are parsed, validated and stored now so
// that the content format and the schema never have to be broken to add them.
// What IS enforced today is listed on each field.

// WorldLevel is the root of the jurisdiction tree: the operator's level. It is
// implicit — never declared in levels — and holds no levers and no offices.
const WorldLevel = "world"

// CityLevel is the level every entry of cities.yml is. It must be declared in
// levels whenever the pack has cities.
const CityLevel = "city"

// CountryLevel is the level a city's `country:` must name a jurisdiction of.
const CountryLevel = "country"

// Lever value types: a CLOSED set, because code interprets each one.
//
// The SCALAR types hold one integer, bounded by min and max, and are the only
// ones with behaviour today: the resolver answers them and SetPolicy changes
// them.
//
// The STRUCTURED types (ADR 0016, G2) hold a document: a yes/no, one of a list,
// a table of choices, a division of a whole. They are declared, validated and
// stored now so the content format and the schema never have to break to add
// them; the resolver and SetPolicy refuse them with
// application.ErrLeverKindUnsupported until their behaviour is built.
const (
	// LeverBPS is a rate in basis points, 10000 = 100%. Bounds must lie in
	// 0..10000: a rate beyond the whole is not a rate.
	LeverBPS = "bps"
	// LeverMoney is an amount in minor currency units.
	LeverMoney = "money"
	// LeverInt is a plain count.
	LeverInt = "int"

	// LeverBool is yes or no. default: true | false.
	LeverBool = "bool"
	// LeverEnum is one of `options`. default: one of them.
	LeverEnum = "enum"
	// LeverMap assigns one of `options` to each `key` (an item code, say):
	// item legality. default: a mapping, possibly empty.
	LeverMap = "map"
	// LeverAllocation divides a whole (10000 bps) among `categories`: a
	// budget. default: a mapping of category to bps summing to 10000.
	LeverAllocation = "allocation"
	// LeverElectionLaw is one elected office's election law (ADR 0015
	// section 2; internal/domain/election.Fields): who may stand and vote,
	// and how the count runs. Unlike bool, enum and map it HAS behaviour: the
	// resolver answers it and the legislature changes it, one field at a
	// time, like an allocation's shares. default: a mapping with exactly
	// election.Fields as keys. Only an office acquired_by: election may have
	// one, exactly one, at a lever code of `<jurisdiction>.election_law.<office>`.
	LeverElectionLaw = "election_law"
)

// Value kinds: where a lever's value is stored. A scalar lever's value is an
// integer column; a structured lever's is a JSON document.
const (
	ValueKindScalar     = "scalar"
	ValueKindStructured = "structured"
)

// LeverValueKind returns the value kind of a lever type, or "" for a type
// that is not one of the closed set.
func LeverValueKind(leverType string) string {
	switch leverType {
	case LeverBPS, LeverMoney, LeverInt:
		return ValueKindScalar
	case LeverBool, LeverEnum, LeverMap, LeverAllocation, LeverElectionLaw:
		return ValueKindStructured
	}
	return ""
}

// Ways an office is acquired (ADR 0015 section 3).
const (
	AcquiredByElection    = "election"
	AcquiredByAppointment = "appointment"
	AcquiredByFounding    = "founding"
	AcquiredByConquest    = "conquest"
)

// Decision rules: how the office holding a lever reaches a decision.
//
// Only DecisionSingle has behaviour today — the holder decides alone, through
// application.SetPolicy. The others describe a vote of a body; they are stored
// so the constitution can say so now, and SetPolicy refuses a lever that needs
// one with application.ErrPolicyRequiresVote until voting exists.
const (
	DecisionSingle        = "single"
	DecisionMajority      = "majority"
	DecisionSupermajority = "supermajority"
	DecisionUnanimous     = "unanimous"
)

// Reserved words can_be_removed_by accepts besides an office code: removal by
// a recall vote of the residents (ADR 0015 section 4), and by the operator,
// who can always remove but whose power the file then states in public
// (ADR 0016 section 8).
const (
	RemovableByResidents = "residents"
	RemovableByOperator  = "operator"
)

// CityDefaultTaxRate is the one per-city default source that exists: a
// lever that names it takes its default, for each city, from that city's
// tax_rate_bps in cities.yml.
//
// # Why the tax default is read from the city and not written twice
//
// Every city already states its tax rate, and each is different: Ostmarch
// 120 bps, Vantor Reach 1450. The lever must reproduce exactly that on the day
// it ships, or turning tax into a lever silently changes seven cities' taxes.
// Writing seven overrides into governance.yml would put every number in two
// files, and the day one is edited and the other is not, the loaded world
// disagrees with one of them. So the number stays where it is, cities.yml
// stays the one place a city's baseline rate is authored, and the lever
// declares that its default comes from there. The lever's own `default` is
// still required and still bounded; it is the value for a city that has no
// such field, which today is none.
//
// The set of sources is CLOSED, like the skill codes: the resolver reads the
// named column in SQL, so a source nobody wired would be a default nothing
// can find. Adding one means adding it here, to the resolver and to the
// lever_definitions CHECK in the same change.
const CityDefaultTaxRate = "tax_rate_bps"

// LevelDef is one level of authority: village, town, city, province, country,
// union, and overlays such as port_authority and free_zone. Levels are
// content, so adding one needs no migration and no code change. Each overlay
// is a level of its own rather than one shared "special" level, because
// levers attach to a level: a free zone's tax override must not also be a
// port's (ADR 0016 section 1.4).
type LevelDef struct {
	// Code is the level's identifier. "world" is reserved for the root.
	Code string `yaml:"code"`
	// Parents lists the levels a jurisdiction of this level may sit
	// directly under; "world" means it may be a root-level jurisdiction.
	// Enforced for every jurisdiction a load writes.
	Parents []string `yaml:"parents"`
	// Overlay marks a level whose jurisdictions overlay others instead of
	// partitioning their parent — a port authority inside a city, a free
	// zone across several. Stored; no behaviour yet.
	Overlay bool `yaml:"overlay"`
}

// JurisdictionDef is one jurisdiction declared in governance.yml: anything
// above, beside or below a city that is not itself a city. Cities are the
// entries of cities.yml and are never declared here.
type JurisdictionDef struct {
	// Code is the stable identifier, unique across governance.yml and
	// distinct from every city code, so a parent reference is never
	// ambiguous. Never changed after it ships.
	Code string `yaml:"code"`
	// Name is display text.
	Name string `yaml:"name"`
	// Level is one of the declared levels, never "world" or "city".
	Level string `yaml:"level"`
	// Parent is the code of the jurisdiction this one sits under: another
	// declared jurisdiction or a city. Empty means the world.
	Parent string `yaml:"parent"`
}

// ParentCode returns the parent, with the world spelled out.
func (j JurisdictionDef) ParentCode() string {
	if j.Parent == "" {
		return WorldLevel
	}
	return j.Parent
}

// LeverDef is one lever: a decision an office may make, within bounds only the
// operator sets.
//
// For a scalar lever Default, Min and Max are all REQUIRED and zero is a
// meaningful value for each, which is why they are not plain integers: an
// omitted `max` must be an error, not a ceiling of zero that makes the file
// look right while the lever can never move. Default is `any` because a
// structured lever's default is a document, not a number.
type LeverDef struct {
	// Code is the stable identifier code asks the resolver for, and it starts
	// with the jurisdiction level: "city.tax_rate" is a city lever. Never
	// changed after it ships: policy values and their public history refer to
	// it.
	Code string `yaml:"code"`
	// Jurisdiction is the level the lever is set at.
	Jurisdiction string `yaml:"jurisdiction"`
	// Type is one of the closed set above.
	Type string `yaml:"type"`
	// Default applies while nobody has set the lever: an integer for a
	// scalar type, a document of the type's shape for a structured one.
	Default any `yaml:"default"`
	// Options lists the allowed values of an enum, or of each entry of a map.
	Options []string `yaml:"options"`
	// Key names what a map's keys are (for example "item"). Map only.
	Key string `yaml:"key"`
	// Categories lists what an allocation divides the whole among.
	Categories []string `yaml:"categories"`
	// CityDefault, when set, names a per-city default source; see
	// CityDefaultTaxRate. Only a city lever may have one.
	CityDefault string `yaml:"city_default"`
	// Min and Max bound every value a scalar lever may ever hold, default
	// included. Enforced by application.SetPolicy and by the database. A
	// structured lever has neither.
	Min *int64 `yaml:"min"`
	Max *int64 `yaml:"max"`
	// HeldBy is the office — one person or a body — that decides the lever.
	// This is the lever's "decided by". Enforced.
	HeldBy string `yaml:"held_by"`
	// DecisionRule is how HeldBy decides: single (the default when omitted),
	// majority, supermajority or unanimous. Only single has behaviour today.
	DecisionRule string `yaml:"decision_rule"`
	// Threshold is the fraction of votes a supermajority needs, as "2/3".
	// Required for supermajority, refused otherwise. Stored.
	Threshold string `yaml:"threshold"`
	// Quorum is the fraction of seats that must vote for a collective
	// decision to count, as "1/2". Optional, and only for a body. Stored.
	Quorum string `yaml:"quorum"`
	// VetoBy lists the offices that may veto a change of this lever. Stored.
	VetoBy []string `yaml:"veto_by"`
	// OverrideRule is how HeldBy overrides a veto: majority, supermajority or
	// unanimous; empty means a veto cannot be overridden. Only with VetoBy.
	// Stored.
	OverrideRule string `yaml:"override_rule"`
	// OverrideThreshold is the supermajority fraction for OverrideRule.
	// Stored.
	OverrideThreshold string `yaml:"override_threshold"`
	// ChangeCooldown is the least time between two changes of the lever in
	// one jurisdiction, as a Go duration ("72h"). Required; "0s" for none.
	// Enforced.
	ChangeCooldown string `yaml:"change_cooldown"`
	// Notice is how long after a change is announced it takes effect.
	// Required; "0s" for immediately. Enforced.
	Notice string `yaml:"notice"`

	// RequiresConfirmationBy is the office — a body, usually — whose vote
	// must confirm a change the holder proposes before it is announced: a
	// council approving the mayor's budget. Empty: the holder decides alone.
	// Enforced: SetPolicy refuses such a change with
	// application.ErrPolicyRequiresConfirmation while the body has a seated
	// member, and the legislature puts it to the body's vote
	// (docs/adr/0024-property-and-politics.md). A body with no member
	// seated confirms nothing and blocks nothing.
	RequiresConfirmationBy string `yaml:"requires_confirmation_by"`
	// ConfirmationRule is how the confirming body decides: majority (the
	// default), supermajority or unanimous.
	ConfirmationRule string `yaml:"confirmation_rule"`
	// ConfirmationThreshold is the supermajority fraction, as "2/3".
	ConfirmationThreshold string `yaml:"confirmation_threshold"`
	// ConfirmationQuorum is the fraction of the body's seats that must
	// vote for the vote to count, as "1/2". Optional.
	ConfirmationQuorum string `yaml:"confirmation_quorum"`
	// ConfirmAbove, on a scalar lever, asks for confirmation only of a
	// change that moves the value by more than this much; smaller changes
	// the holder decides alone. Omitted: every change is confirmed.
	ConfirmAbove *int64 `yaml:"confirm_above"`

	// EducationOptions lists, election_law only, the certificates (course
	// codes of education.yml courses with certifies: true) this office's law
	// may require, in ascending rank; rank 0, "none", is implicit and never
	// listed. A candidate's education_rank field names the least rank of a
	// certificate they must hold among these.
	EducationOptions []string `yaml:"education_options"`
	// Bounds is, election_law only, the safety bounds the operator sets so
	// that a legislature can never lock every resident out of the office
	// (ADR 0015 section 2).
	Bounds *ElectionLawBounds `yaml:"bounds"`
}

// ElectionLawBounds bounds every field of an election_law lever, and the
// two safety valves that look past a single field at the whole law: a
// legislature may set each field anywhere in its own [min, max], but an
// endorsements_required beyond EndorsementsMaxBPS of a jurisdiction's
// residents, or a law application.CheckElectionLawExclusion estimates would
// leave more than ExclusionMaxBPS of residents unable to stand, is refused
// however the fields alone would allow it.
type ElectionLawBounds struct {
	CandidacyHours         FieldBound `yaml:"candidacy_hours"`
	VotingHours            FieldBound `yaml:"voting_hours"`
	MinLevel               FieldBound `yaml:"min_level"`
	MinResidencyHours      FieldBound `yaml:"min_residency_hours"`
	VoterMinResidencyHours FieldBound `yaml:"voter_min_residency_hours"`
	Deposit                FieldBound `yaml:"deposit"`
	RefundShareBPS         FieldBound `yaml:"refund_share_bps"`
	ReopenAfterHours       FieldBound `yaml:"reopen_after_hours"`
	EndorsementsRequired   FieldBound `yaml:"endorsements_required"`
	TermLimitConsecutive   FieldBound `yaml:"term_limit_consecutive"`
	TermLimitTotal         FieldBound `yaml:"term_limit_total"`
	MinAge                 FieldBound `yaml:"min_age"`
	// EndorsementsMaxBPS caps endorsements_required at this share of the
	// jurisdiction's residents (10000 = all of them), on top of its own
	// [min, max] above.
	EndorsementsMaxBPS int64 `yaml:"endorsements_max_bps"`
	// ExclusionMaxBPS is the greatest share of residents an election law may
	// estimate to exclude from standing at once.
	ExclusionMaxBPS int64 `yaml:"exclusion_max_bps"`
}

// FieldBound is one field's [min, max] within an ElectionLawBounds. Both are
// required; zero is a meaningful value for each (a field that may not move
// at all still has a min and a max, both zero).
type FieldBound struct {
	Min int64 `yaml:"min"`
	Max int64 `yaml:"max"`
}

// Bound looks a field up by code (an election.Field* constant), the
// derived bound for clean_record (always 0..1) and education_rank (always
// 0..len(EducationOptions)), or ok false for an unknown field.
func (b ElectionLawBounds) Bound(field string, educationOptions []string) (lo, hi int64, ok bool) {
	switch field {
	case "candidacy_hours":
		return b.CandidacyHours.Min, b.CandidacyHours.Max, true
	case "voting_hours":
		return b.VotingHours.Min, b.VotingHours.Max, true
	case "min_level":
		return b.MinLevel.Min, b.MinLevel.Max, true
	case "min_residency_hours":
		return b.MinResidencyHours.Min, b.MinResidencyHours.Max, true
	case "clean_record":
		return 0, 1, true
	case "voter_min_residency_hours":
		return b.VoterMinResidencyHours.Min, b.VoterMinResidencyHours.Max, true
	case "deposit":
		return b.Deposit.Min, b.Deposit.Max, true
	case "refund_share_bps":
		return b.RefundShareBPS.Min, b.RefundShareBPS.Max, true
	case "reopen_after_hours":
		return b.ReopenAfterHours.Min, b.ReopenAfterHours.Max, true
	case "endorsements_required":
		return b.EndorsementsRequired.Min, b.EndorsementsRequired.Max, true
	case "term_limit_consecutive":
		return b.TermLimitConsecutive.Min, b.TermLimitConsecutive.Max, true
	case "term_limit_total":
		return b.TermLimitTotal.Min, b.TermLimitTotal.Max, true
	case "education_rank":
		return 0, int64(len(educationOptions)), true
	case "min_age":
		return b.MinAge.Min, b.MinAge.Max, true
	}
	return 0, 0, false
}

// ConfirmRule is the confirming body's rule with the default applied, ""
// for a lever needing no confirmation.
func (l LeverDef) ConfirmRule() string {
	if l.RequiresConfirmationBy == "" {
		return ""
	}
	if l.ConfirmationRule == "" {
		return DecisionMajority
	}
	return l.ConfirmationRule
}

// Rule is the lever's decision rule with the default applied.
func (l LeverDef) Rule() string {
	if l.DecisionRule == "" {
		return DecisionSingle
	}
	return l.DecisionRule
}

// ValueKind is scalar or structured, from the type; "" for an unknown type.
func (l LeverDef) ValueKind() string { return LeverValueKind(l.Type) }

// IsElectionLaw reports whether the lever is one office's election law.
func (l LeverDef) IsElectionLaw() bool { return l.Type == LeverElectionLaw }

// ElectionLawOffice is the office code an election_law lever's code names:
// "<jurisdiction>.election_law.<office>". "" for a lever whose code does not
// fit that shape (Validate refuses such a lever).
func (l LeverDef) ElectionLawOffice() string {
	prefix := l.Jurisdiction + ".election_law."
	if !l.IsElectionLaw() || !strings.HasPrefix(l.Code, prefix) {
		return ""
	}
	return strings.TrimPrefix(l.Code, prefix)
}

// DefaultElectionLawDoc reads an election_law lever's default as a document
// of int64 fields, in the shape election.ValidateFields checks.
func (l LeverDef) DefaultElectionLawDoc() (map[string]int64, bool) {
	m, ok := asMap(l.Default)
	if !ok {
		return nil, false
	}
	out := make(map[string]int64, len(m))
	for k, v := range m {
		n, whole := integer(v)
		if !whole {
			return nil, false
		}
		out[k] = n
	}
	return out, true
}

// DefaultValue is a scalar lever's default, zero when it is not an integer
// (which Validate refuses).
func (l LeverDef) DefaultValue() int64 {
	v, _ := integer(l.Default)
	return v
}

// DefaultJSON is a structured lever's default as the JSON document stored in
// lever_definitions.default_json.
func (l LeverDef) DefaultJSON() ([]byte, error) {
	return json.Marshal(l.Default)
}

// MinValue is the lever's lower bound, zero when omitted (refused by Validate).
func (l LeverDef) MinValue() int64 { return deref(l.Min) }

// MaxValue is the lever's upper bound, zero when omitted (refused by Validate).
func (l LeverDef) MaxValue() int64 { return deref(l.Max) }

// CooldownDuration parses ChangeCooldown. Validate has already refused a
// value this cannot parse, so on a validated pack the error is always nil.
func (l LeverDef) CooldownDuration() (time.Duration, error) {
	return parseLeverDuration(l.ChangeCooldown)
}

// NoticeDuration parses Notice, like CooldownDuration.
func (l LeverDef) NoticeDuration() (time.Duration, error) {
	return parseLeverDuration(l.Notice)
}

// DefaultFor is the lever's default in one city: the city's own value when
// the lever names a per-city source, the lever's default otherwise.
//
// Validation uses it to bound every default a city can actually receive. The
// resolver applies the same rule in SQL, where the per-city value is read
// from the stored city; see postgres.selectPolicyLever.
func (l LeverDef) DefaultFor(c CityDef) int64 {
	if l.CityDefault == CityDefaultTaxRate {
		return int64(c.TaxRateBPS)
	}
	return l.DefaultValue()
}

// OfficeDef is one office: a seat, or several, a player can hold in each
// jurisdiction of its level.
type OfficeDef struct {
	// Code is the stable identifier. Never changed after it ships: office
	// rows and the public policy history refer to it.
	Code string `yaml:"code"`
	// Jurisdiction is the level of the office. One set of seats exists in
	// every jurisdiction of that level.
	Jurisdiction string `yaml:"jurisdiction"`
	// Seats is how many holders the office has in one jurisdiction: 1 for a
	// mayor, several for a council. Enforced: the loader creates exactly
	// these seats.
	Seats int `yaml:"seats"`
	// AcquiredBy is how the office is normally obtained. An operator may
	// still appoint to any office; this records the intended route.
	AcquiredBy string `yaml:"acquired_by"`
	// Levers lists the lever codes this office holds. It must agree exactly
	// with the levers whose held_by names this office: the two are written
	// in different places of the file on purpose, so that a reader of either
	// sees the whole authority, and Validate refuses them the moment they
	// disagree.
	Levers []string `yaml:"levers"`

	// Deputy is the office that acts for this one while it is vacant, before
	// the default applies. Chains (a deputy's deputy) are allowed; a cycle is
	// refused at load. Enforced: the resolver and SetPolicy walk the chain.
	Deputy string `yaml:"deputy"`
	// AppointedBy is the office that appoints the holder, or empty. Requires
	// acquired_by: appointment. Stored; the operator's `admin office appoint`
	// is the only appointment path today.
	AppointedBy string `yaml:"appointed_by"`
	// RequiresConfirmationBy is the office or body that must confirm an
	// appointment, or empty. Requires appointed_by. Stored.
	RequiresConfirmationBy string `yaml:"requires_confirmation_by"`
	// Term is how long one tenure lasts, as a Go duration; empty means at
	// pleasure. An appointment records when it ends; nothing ends it yet.
	Term string `yaml:"term"`
	// TermLimit is how many terms one player may serve, or nil for no limit.
	// Requires a term. Stored.
	TermLimit *int `yaml:"term_limit"`
	// CanBeRemovedBy lists the offices, or "residents", that may remove the
	// holder. Stored.
	CanBeRemovedBy []string `yaml:"can_be_removed_by"`
	// VetoOver lists the lever codes and office codes this office may veto.
	// Stored.
	VetoOver []string `yaml:"veto_over"`
	// IncompatibleWith lists offices one player may not hold at the same time
	// as this one, anywhere. The relation is symmetric: stating it on either
	// office binds both. Enforced by `admin office appoint`.
	IncompatibleWith []string `yaml:"incompatible_with"`
}

// TermDuration parses Term; zero means at pleasure. Validate has already
// refused a value this cannot parse.
func (o OfficeDef) TermDuration() (time.Duration, error) {
	if o.Term == "" {
		return 0, nil
	}
	return parseLeverDuration(o.Term)
}

// deref reads an optional number, zero when absent.
func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// integer reads a whole number out of a decoded yaml or JSON value. yaml
// decodes an integer as int (or uint64 when very large), JSON as float64; a
// float is accepted only when it is whole.
func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case float64:
		if n != math.Trunc(n) || n > math.MaxInt64 || n < math.MinInt64 {
			return 0, false
		}
		return int64(n), true
	}
	return 0, false
}

// parseLeverDuration reads a cooldown, a notice or a term. It must be
// present, a Go duration, not negative, and whole seconds — the store keeps
// seconds, and a fraction it would silently drop is a value the file states
// and the world does not honour.
func parseLeverDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("is required (write 0s for none)")
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 72h or 30m", raw)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative", raw)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("%q is not a whole number of seconds", raw)
	}
	return d, nil
}

// FormatLeverDuration renders seconds the way a file would state them, for a
// pack read back out of the database. The result parses back to the same
// duration.
func FormatLeverDuration(seconds int64) string {
	return (time.Duration(seconds) * time.Second).String()
}

// maxFractionDenominator keeps a fraction readable: "667/1000" is the finest
// a constitution needs.
const maxFractionDenominator = 1000

// ParseFraction reads "p/q" with 0 < p <= q <= 1000.
func ParseFraction(raw string) (num, den int, err error) {
	p, q, ok := strings.Cut(strings.TrimSpace(raw), "/")
	if !ok {
		return 0, 0, fmt.Errorf("%q is not a fraction such as 2/3", raw)
	}
	num, err1 := strconv.Atoi(strings.TrimSpace(p))
	den, err2 := strconv.Atoi(strings.TrimSpace(q))
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("%q is not a fraction such as 2/3", raw)
	}
	if num <= 0 || den <= 0 || num > den || den > maxFractionDenominator {
		return 0, 0, fmt.Errorf("%q must lie in (0, 1] with a denominator of at most %d", raw, maxFractionDenominator)
	}
	return num, den, nil
}
