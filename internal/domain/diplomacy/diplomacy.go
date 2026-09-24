// Package diplomacy holds the rules between countries
// (docs/adr/0022-military-and-diplomacy.md): sanctions — five measures, each
// blocking one kind of cross-border action, imposed by one country on
// another, in force after their notice, lifted no sooner than their least
// duration — and treaties — proposed by one country, accepted once by the
// other, ended by either.
//
// THE ONE SANCTIONS CHECK. Blocks is the only function that decides whether
// a cross-border action is blocked; every place such an action happens asks
// it (through application.CheckSanctions) and nothing else. A sanction binds
// both ways: an embargo stops trade from the target as well as to it.
//
// No clock and no randomness: every function is given its now.
package diplomacy

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Sentinel errors.
var (
	// ErrSelf means a country tried to sanction, or make a treaty with,
	// itself.
	ErrSelf = errors.New("diplomacy: a country and itself")
	// ErrNoMeasures means a sanction with nothing in it.
	ErrNoMeasures = errors.New("diplomacy: a sanction needs at least one measure")
	// ErrUnknownMeasure means a measure outside the five, or a mask with
	// bits beyond them.
	ErrUnknownMeasure = errors.New("diplomacy: unknown sanction measure")
	// ErrAlreadySanctioned means the imposer has a sanction on the target
	// already: lift it first.
	ErrAlreadySanctioned = errors.New("diplomacy: already sanctioned")
	// ErrNotInForce means a sanction that is lifted already.
	ErrNotInForce = errors.New("diplomacy: sanction is not in force")
	// ErrTooSoon means a sanction lifted before its least duration.
	ErrTooSoon = errors.New("diplomacy: sanction lifted too soon")
	// ErrNotParty means a country acted on a sanction or treaty that is not
	// its to act on.
	ErrNotParty = errors.New("diplomacy: not a party")
	// ErrTreatyOpen means the two countries have an open or active treaty
	// of that kind already.
	ErrTreatyOpen = errors.New("diplomacy: a treaty of that kind is open already")
	// ErrTreatyState means a transition the treaty's state does not allow:
	// accepting an expired offer, ending a declined one.
	ErrTreatyState = errors.New("diplomacy: the treaty is not in a state for that")
)

// Measure is one kind of sanction. The set is closed and lives in code:
// each measure is enforced at a place in the game, and a measure invented
// in content would block nothing.
type Measure string

const (
	// Trade blocks the market, the auction house and companies' sales
	// between the two countries' players and companies.
	Trade Measure = "trade"
	// Arms blocks procurement of either's arms by the other's state.
	Arms Measure = "arms"
	// Technology blocks licensing of technology between their companies.
	Technology Measure = "technology"
	// Travel blocks journeys between their cities.
	Travel Measure = "travel"
	// Financial blocks card payments between their players.
	Financial Measure = "financial"
)

var measures = []Measure{Trade, Arms, Technology, Travel, Financial}

// Measures lists the five in their fixed order. The slice is a copy.
func Measures() []Measure { return append([]Measure(nil), measures...) }

// bit is the measure's place in a mask.
func (m Measure) bit() (int, bool) {
	for i, x := range measures {
		if x == m {
			return 1 << i, true
		}
	}
	return 0, false
}

// Valid reports whether m is one of the five.
func (m Measure) Valid() bool { _, ok := m.bit(); return ok }

// Mask packs measures into the bits a button carries. Unknown measures are
// ErrUnknownMeasure.
func Mask(ms []Measure) (int, error) {
	out := 0
	for _, m := range ms {
		b, ok := m.bit()
		if !ok {
			return 0, fmt.Errorf("%w: %q", ErrUnknownMeasure, string(m))
		}
		out |= b
	}
	return out, nil
}

// FullMask is every measure at once.
func FullMask() int { return 1<<len(measures) - 1 }

// FromMask unpacks a mask in the fixed order. A bit beyond the five is
// ErrUnknownMeasure; an empty mask is an empty list.
func FromMask(mask int) ([]Measure, error) {
	if mask < 0 || mask > FullMask() {
		return nil, fmt.Errorf("%w: mask %d", ErrUnknownMeasure, mask)
	}
	var out []Measure
	for i, m := range measures {
		if mask&(1<<i) != 0 {
			out = append(out, m)
		}
	}
	return out, nil
}

// Toggle flips one measure in a mask.
func Toggle(mask int, m Measure) (int, error) {
	b, ok := m.bit()
	if !ok {
		return mask, fmt.Errorf("%w: %q", ErrUnknownMeasure, string(m))
	}
	return mask ^ b, nil
}

// Sanction is one country's sanction on another.
type Sanction struct {
	ID       string
	Imposer  string
	Target   string
	Measures []Measure
	// ImposedAt is when it was announced; EffectiveAt when it binds.
	ImposedAt   time.Time
	EffectiveAt time.Time
	// LiftedAt is when it was lifted, nil while it stands.
	LiftedAt *time.Time
}

// Has reports whether the sanction includes the measure.
func (s Sanction) Has(m Measure) bool {
	for _, x := range s.Measures {
		if x == m {
			return true
		}
	}
	return false
}

// InForce reports whether the sanction binds at now: announced, its notice
// passed, not lifted.
func (s Sanction) InForce(now time.Time) bool {
	return s.LiftedAt == nil && !now.Before(s.EffectiveAt)
}

// between reports whether the sanction is between a and b, either way.
func (s Sanction) between(a, b string) bool {
	return (s.Imposer == a && s.Target == b) || (s.Imposer == b && s.Target == a)
}

// Blocks is THE sanctions check: whether an action of kind m between a
// party of country a and a party of country b is blocked at now, and by
// which sanction. Two parties of one country are never blocked, nor is a
// party of no country; a sanction binds both ways.
func Blocks(sanctions []Sanction, a, b string, m Measure, now time.Time) (Sanction, bool) {
	if a == "" || b == "" || a == b {
		return Sanction{}, false
	}
	for _, s := range sanctions {
		if s.between(a, b) && s.Has(m) && s.InForce(now) {
			return s, true
		}
	}
	return Sanction{}, false
}

// CheckImpose reports whether imposer may sanction target with these
// measures, given the sanctions the imposer has in place on the target
// (lifted ones may be passed and are ignored).
func CheckImpose(imposer, target string, ms []Measure, existing []Sanction) error {
	if imposer == target {
		return ErrSelf
	}
	if len(ms) == 0 {
		return ErrNoMeasures
	}
	if _, err := Mask(ms); err != nil {
		return err
	}
	for _, s := range existing {
		if s.Imposer == imposer && s.Target == target && s.LiftedAt == nil {
			return ErrAlreadySanctioned
		}
	}
	return nil
}

// CheckLift reports whether the imposer may lift s at now: its own, still
// standing, and imposed at least minDuration ago. The least duration keeps a
// sanction from being switched off for one trade and on again after.
func CheckLift(s Sanction, by string, now time.Time, minDuration time.Duration) error {
	switch {
	case s.Imposer != by:
		return ErrNotParty
	case s.LiftedAt != nil:
		return ErrNotInForce
	case now.Before(s.ImposedAt.Add(minDuration)):
		return fmt.Errorf("%w: until %s", ErrTooSoon, s.ImposedAt.Add(minDuration).UTC().Format(time.RFC3339))
	}
	return nil
}

// LiftableAt is when the imposer may first lift s.
func LiftableAt(s Sanction, minDuration time.Duration) time.Time { return s.ImposedAt.Add(minDuration) }

// Treaty statuses.
type Status string

const (
	Proposed   Status = "proposed"
	Active     Status = "active"
	Declined   Status = "declined"
	Withdrawn  Status = "withdrawn"
	Expired    Status = "expired"
	Terminated Status = "terminated"
)

// Treaty is one treaty between two countries: proposed by one, accepted
// (or not) by the other.
type Treaty struct {
	ID       string
	Kind     string
	Proposer string
	Partner  string
	Status   Status
	// ExpiresAt is when an unanswered proposal lapses.
	ExpiresAt time.Time
}

// StatusAt is the treaty's status at now: a proposal nobody answered by its
// expiry is expired, whatever the row still says.
func (t Treaty) StatusAt(now time.Time) Status {
	if t.Status == Proposed && !now.Before(t.ExpiresAt) {
		return Expired
	}
	return t.Status
}

// Open reports whether the treaty stands or waits for an answer at now.
func (t Treaty) Open(now time.Time) bool {
	s := t.StatusAt(now)
	return s == Proposed || s == Active
}

// Member reports whether country is one of its parties.
func (t Treaty) Member(country string) bool { return t.Proposer == country || t.Partner == country }

// Other is the party that is not country.
func (t Treaty) Other(country string) string {
	if t.Proposer == country {
		return t.Partner
	}
	return t.Proposer
}

// CheckPropose reports whether proposer may propose a treaty of kind to
// partner, given the treaties between them.
func CheckPropose(proposer, partner, kind string, existing []Treaty, now time.Time) error {
	if proposer == partner {
		return ErrSelf
	}
	for _, t := range existing {
		if t.Kind == kind && t.Member(proposer) && t.Member(partner) && t.Open(now) {
			return ErrTreatyOpen
		}
	}
	return nil
}

// Answer is the partner accepting or declining a proposal at now: the new
// status, or why not. Only the partner answers, only a proposal still
// waiting.
func Answer(t Treaty, by string, accept bool, now time.Time) (Status, error) {
	if by != t.Partner {
		return t.Status, ErrNotParty
	}
	if t.StatusAt(now) != Proposed {
		return t.Status, fmt.Errorf("%w: %s", ErrTreatyState, t.StatusAt(now))
	}
	if accept {
		return Active, nil
	}
	return Declined, nil
}

// End is a party withdrawing its own proposal, or either party ending a
// treaty in force: the new status, or why not.
func End(t Treaty, by string, now time.Time) (Status, error) {
	switch s := t.StatusAt(now); {
	case s == Proposed && by == t.Proposer:
		return Withdrawn, nil
	case s == Active && t.Member(by):
		return Terminated, nil
	case !t.Member(by):
		return t.Status, ErrNotParty
	default:
		return t.Status, fmt.Errorf("%w: %s", ErrTreatyState, s)
	}
}

// TreatyType is what a kind of treaty does (content treaty_types).
type TreatyType struct {
	Code              string
	MutualDefence     bool
	NonAggression     bool
	ArmsPartner       bool
	TariffDiscountBPS int64
}

// Partners reports whether a and b have a treaty in force of a type for
// which want is true.
func Partners(treaties []Treaty, types map[string]TreatyType, a, b string, now time.Time, want func(TreatyType) bool) bool {
	if a == b {
		return false
	}
	for _, t := range treaties {
		if t.StatusAt(now) != Active || !t.Member(a) || !t.Member(b) {
			continue
		}
		if ty, ok := types[t.Kind]; ok && want(ty) {
			return true
		}
	}
	return false
}

// Tariff is the border tariff between a and b: the country's rate less the
// largest discount any treaty in force between them grants. Nothing charges
// the tariff yet; this is the hook the trade stage calls.
func Tariff(baseBPS int64, treaties []Treaty, types map[string]TreatyType, a, b string, now time.Time) int64 {
	var discount int64
	for _, t := range treaties {
		if t.StatusAt(now) != Active || !t.Member(a) || !t.Member(b) || a == b {
			continue
		}
		discount = max(discount, types[t.Kind].TariffDiscountBPS)
	}
	discount = min(max(discount, 0), 10_000)
	return baseBPS * (10_000 - discount) / 10_000
}

// SortSanctions orders sanctions newest first, then by id.
func SortSanctions(ss []Sanction) {
	sort.SliceStable(ss, func(i, j int) bool {
		if !ss[i].ImposedAt.Equal(ss[j].ImposedAt) {
			return ss[i].ImposedAt.After(ss[j].ImposedAt)
		}
		return ss[i].ID < ss[j].ID
	})
}
