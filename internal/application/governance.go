package application

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds player-held offices (docs/adr/0015-player-held-offices.md):
// the ports that read and change policy, the ONE function that decides which
// value of a lever wins, and the use cases that change a lever and seat or
// unseat an office holder.
//
// The rule every other feature follows from here on: no code reads a policy
// value — a tax rate, a fee, a tariff — from content or configuration. It asks
// PolicyReader.Get, which answers with the value in force: the office
// holder's, once its notice has passed, and the lever's default otherwise.

// WorldJurisdictionID is the root of the jurisdiction tree, created by
// migration 0008 with this fixed id, like the system accounts.
const WorldJurisdictionID = "00000000-0000-4000-8000-000000000100"

// DecisionSingle is the decision rule under which the office holder decides
// alone. It is the only rule with behaviour today; the others (majority,
// supermajority, unanimous) are a vote of a body, and SetPolicy refuses them
// with ErrPolicyRequiresVote until voting exists.
const DecisionSingle = "single"

// ValueKindScalar is the value kind of bps, money and int levers: one
// bounded integer.
const ValueKindScalar = "scalar"

// ValueKindStructured is the value kind of bool, enum, map and allocation
// levers: a document. Of these only the allocation has behaviour
// (docs/adr/0024-property-and-politics.md); the rest are declared and stored,
// and refused with ErrLeverKindUnsupported until theirs is built.
const ValueKindStructured = "structured"

// LeverAllocation is the lever type dividing a whole (10000 bps) among
// categories: a budget. Its value is a share per category; the shares sum to
// at most the whole, and what is not allocated is not spent.
const LeverAllocation = "allocation"

// AllocationWhole is the whole an allocation divides, in basis points.
const AllocationWhole = 10000

// AcquiredByAppointment is offices.acquired_by for a seat an appointment
// filled — today, always the operator's.
const AcquiredByAppointment = "appointment"

// AcquiredByElection is offices.acquired_by for a seat won at an election's
// count.
const AcquiredByElection = "election"

// Governance sentinels. Each is a refusal with its own identity, so a caller
// can tell "not yours to change" from "out of bounds" from "too soon".
var (
	// ErrUnknownLever means no lever of that code exists in the active
	// content. It is a caller's bug, never a vacancy: a vacant office still
	// has a lever, and the lever still has a default.
	ErrUnknownLever = errors.Sentinel(errors.CodeNotFound,
		"application.ErrUnknownLever", "no such policy")

	// ErrJurisdictionNotFound means the jurisdiction id names nothing.
	ErrJurisdictionNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrJurisdictionNotFound", "no such place")

	// ErrWrongJurisdiction means a lever or office was addressed at a
	// jurisdiction of another level: a city's tax asked of a country.
	ErrWrongJurisdiction = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrWrongJurisdiction", "that does not apply here")

	// ErrLeverKindUnsupported means the lever holds a structured value (a
	// yes/no, a choice, a table, an allocation), which content may declare
	// but nothing can read or change yet.
	ErrLeverKindUnsupported = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrLeverKindUnsupported", "that kind of policy is not supported yet")

	// ErrPolicyRequiresVote means the lever is decided by a vote of a body,
	// not by one holder. The cause names the body.
	ErrPolicyRequiresVote = errors.Sentinel(errors.CodeUnauthorized,
		"application.ErrPolicyRequiresVote", "that decision requires a vote")

	// ErrPolicyRequiresConfirmation means the change must be confirmed by a
	// vote of a body (the detail "body") before it is announced: it is
	// proposed to the legislature instead of set.
	ErrPolicyRequiresConfirmation = errors.Sentinel(errors.CodeUnauthorized,
		"application.ErrPolicyRequiresConfirmation", "that change must be confirmed by a vote")

	// ErrInvalidAllocation means an allocation naming a category the lever
	// does not divide among, a negative share, or shares summing to more
	// than the whole.
	ErrInvalidAllocation = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrInvalidAllocation", "that division of the budget is not allowed")

	// ErrNotOfficeHolder means the caller does not hold the office acting for
	// the lever in that jurisdiction: not its holder, nor — while it is
	// vacant — the deputy acting for it.
	ErrNotOfficeHolder = errors.Sentinel(errors.CodeUnauthorized,
		"application.ErrNotOfficeHolder", "only the office holder can change that")

	// ErrPolicyOutOfBounds means a value outside the lever's [min, max]. No
	// office holder can cross the operator's bounds.
	ErrPolicyOutOfBounds = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrPolicyOutOfBounds", "that value is outside the allowed range")

	// ErrPolicyCooldown means the lever changed too recently in that
	// jurisdiction. The detail "available_at" says when it may change again.
	ErrPolicyCooldown = errors.Sentinel(errors.CodeCooldown,
		"application.ErrPolicyCooldown", "that policy changed too recently")

	// ErrOfficeNotFound means no such office, or no such seat of it, in that
	// jurisdiction.
	ErrOfficeNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrOfficeNotFound", "no such office")

	// ErrOfficeOccupied means an appointment to a seat somebody else holds.
	// Vacate it first; an appointment never silently unseats anybody.
	ErrOfficeOccupied = errors.Sentinel(errors.CodeConflict,
		"application.ErrOfficeOccupied", "that office is already held")

	// ErrOfficeVacant means a vacate of a seat nobody holds.
	ErrOfficeVacant = errors.Sentinel(errors.CodeConflict,
		"application.ErrOfficeVacant", "that office is already vacant")

	// ErrAlreadyHoldsSeat means the player already holds a seat of this
	// office in this jurisdiction: one person, one seat on one council.
	ErrAlreadyHoldsSeat = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyHoldsSeat", "already holds that office")

	// ErrIncompatibleOffices means the player holds an office declared
	// incompatible with this one (in either direction), anywhere.
	ErrIncompatibleOffices = errors.Sentinel(errors.CodeConflict,
		"application.ErrIncompatibleOffices", "cannot hold both offices")
)

// LeverDefinition is one lever of the active content: the part of the
// constitution the resolver and SetPolicy need.
type LeverDefinition struct {
	Code string
	// Jurisdiction is the level the lever is set at ("city", "country", …).
	Jurisdiction string
	// Type is the lever type (bps, money, int, bool, enum, map, allocation).
	Type string
	// ValueKind is ValueKindScalar or "structured". Default, Min and Max are
	// meaningful only for a scalar lever.
	ValueKind         string
	Default, Min, Max int64
	// HeldBy is the office deciding the lever.
	HeldBy string
	// DecisionRule is how HeldBy decides; see DecisionSingle. Threshold and
	// Quorum are its fractions ("2/3"), empty when not set.
	DecisionRule      string
	Threshold, Quorum string
	ChangeCooldown    time.Duration
	Notice            time.Duration
	// CityDefault names the per-city default source, or is empty.
	CityDefault string

	// Categories are what an allocation divides the whole among, and
	// DefaultAllocation its default shares; empty for another type.
	Categories        []string
	DefaultAllocation map[string]int64

	// RequiresConfirmationBy is the body whose vote confirms a change, with
	// its rule, threshold and quorum; empty for none. ConfirmAbove, when
	// set, spares a change of at most that much from it.
	RequiresConfirmationBy                                      string
	ConfirmationRule, ConfirmationThreshold, ConfirmationQuorum string
	ConfirmAbove                                                *int64
}

// IsAllocation reports whether the lever divides a budget.
func (l LeverDefinition) IsAllocation() bool { return l.Type == LeverAllocation }

// ByVote reports whether the lever is decided by a vote of the body holding
// it.
func (l LeverDefinition) ByVote() bool {
	return l.DecisionRule != "" && l.DecisionRule != DecisionSingle
}

// OfficeDefinition is one office of the active content.
type OfficeDefinition struct {
	Code         string
	Jurisdiction string
	Seats        int
	Deputy       string
	// Term is one tenure; zero means at pleasure.
	Term             time.Duration
	IncompatibleWith []string
	// AcquiredBy is how the office is normally obtained; AppointedBy the
	// office whose holder appoints to it, empty for none; CanBeRemovedBy
	// the offices (or "residents", "operator") that may remove its holder.
	AcquiredBy     string
	AppointedBy    string
	CanBeRemovedBy []string
}

// Jurisdiction is one node of the tree: the world, a country, a city, ….
type Jurisdiction struct {
	ID string
	// Kind is the level code, or "world" for the root.
	Kind     string
	Code     string
	Name     string
	ParentID string
}

// Office is one seat, and who holds it.
type Office struct {
	ID             string
	OfficeCode     string
	JurisdictionID string
	Seat           int
	// HolderPlayerID is empty while the seat is vacant.
	HolderPlayerID string
	TermEndsAt     *time.Time
	// AcquiredBy is how the current holder got the seat; empty while vacant.
	AcquiredBy string
	// Since is when the current state — the tenure or the vacancy — began.
	Since time.Time
}

// Vacant reports whether nobody holds the seat.
func (o Office) Vacant() bool { return o.HolderPlayerID == "" }

// OfficeLink is one office of a lever's acting chain in one jurisdiction,
// with its seats. The chain is the lever's office, then its deputy, then the
// deputy's deputy, and so on.
type OfficeLink struct {
	OfficeCode string
	Seats      []Office
}

// PolicySetting is one value an office holder set: a policy_values row.
type PolicySetting struct {
	ID             string
	JurisdictionID string
	LeverCode      string
	Value          int64
	// Allocation is an allocation lever's shares; nil for a scalar.
	Allocation    map[string]int64
	SetByPlayerID string
	// OfficeID is the seat the change was made from.
	OfficeID    string
	SetAt       time.Time
	EffectiveAt time.Time
}

// PolicyInputs is everything ResolvePolicy needs about one lever in one
// jurisdiction. The repository loads it; the precedence is decided only by
// ResolvePolicy.
type PolicyInputs struct {
	Lever        LeverDefinition
	Jurisdiction Jurisdiction
	// CityDefault is the city's own default for a lever that names one, nil
	// otherwise.
	CityDefault *int64
	// Chain is the acting chain, the lever's office first.
	Chain []OfficeLink
	// Settings holds the most recent setting in effect at the time asked,
	// if any, and every setting not yet in effect. Nothing older is needed.
	Settings []PolicySetting
	// LastChangeAt is when the lever last changed here, in effect or not;
	// nil if never. The cooldown runs from it.
	LastChangeAt *time.Time
}

// PolicySource says where a resolved value came from.
type PolicySource string

const (
	// PolicyFromDefault means nobody's setting is in effect: the lever's
	// content default (or the city's own default) applies.
	PolicyFromDefault PolicySource = "default"
	// PolicyFromOffice means an office holder's setting is in effect.
	PolicyFromOffice PolicySource = "office"
)

// ActingOffice is the office currently entitled to change a lever: the
// lever's own office if any seat is held, otherwise the first deputy in the
// chain with a held seat.
type ActingOffice struct {
	OfficeCode string
	// Deputy is true when the lever's own office is vacant and this office
	// acts for it.
	Deputy bool
	// Holders are the held seats of the acting office.
	Holders []Office
}

// PolicyValue is the answer to "what is this lever here, now?".
type PolicyValue struct {
	JurisdictionID string
	Lever          string
	Type           string
	Value          int64
	// Allocation is an allocation lever's shares in force, a category
	// missing from it having none; nil for a scalar lever.
	Allocation map[string]int64
	Source     PolicySource
	// InForce is the setting whose value applies, nil for a default.
	InForce *PolicySetting
	// Pending is the next announced change still inside its notice, if any.
	Pending *PolicySetting
	// Acting is who may change the lever now; nil when the office and every
	// deputy are vacant, so the default behaviour runs and nobody can move
	// it. A nil Acting is never an error.
	Acting *ActingOffice
	// Clamped is true when the value was pulled into the lever's current
	// bounds: a value set under older, wider bounds never escapes new ones.
	Clamped bool
}

// PolicyChange is one change of a lever: the setting and its public record.
type PolicyChange struct {
	// ID is the policy_changes row; Setting.ID the policy_values row. Both
	// are assigned by the repository.
	ID         string
	Setting    PolicySetting
	OfficeCode string
	// OldValue is the value in force when the change was made;
	// OldAllocation the shares, for an allocation lever.
	OldValue      int64
	OldAllocation map[string]int64
}

// PolicyReader is the ONE way code reads a policy value.
//
// Get answers with the value in force for the lever in that jurisdiction. A
// vacant office is never an error: it answers the default. It fails only for
// a lever or jurisdiction that does not exist, or a lever asked of a
// jurisdiction of the wrong level — which are bugs in the caller, not states
// of the world.
type PolicyReader interface {
	Get(ctx context.Context, jurisdictionID, leverCode string) (PolicyValue, error)
}

// GovernanceRepository is the transactional port behind SetPolicy and the
// office use cases. Reach it through Tx.Governance, so a change and its
// public record commit together.
type GovernanceRepository interface {
	// LockLever serialises changes of one lever in one jurisdiction until
	// the transaction ends, so two changes cannot both pass the cooldown.
	LockLever(ctx context.Context, jurisdictionID, leverCode string) error
	// PolicyInputs loads what ResolvePolicy needs, with the chain's seats
	// locked against a concurrent appointment or vacancy. It returns
	// ErrUnknownLever or ErrJurisdictionNotFound.
	PolicyInputs(ctx context.Context, jurisdictionID, leverCode string, now time.Time) (PolicyInputs, error)
	// RecordPolicy writes the setting and its public record and returns the
	// change with its ids.
	RecordPolicy(ctx context.Context, change PolicyChange) (PolicyChange, error)

	// Jurisdiction returns one jurisdiction, or ErrJurisdictionNotFound.
	Jurisdiction(ctx context.Context, id string) (Jurisdiction, error)
	// OfficeDefinitions returns every office of the active content.
	OfficeDefinitions(ctx context.Context) ([]OfficeDefinition, error)
	// Seat returns one seat locked for update, or ErrOfficeNotFound.
	Seat(ctx context.Context, officeCode, jurisdictionID string, seat int) (Office, error)
	// SeatsHeldBy returns every seat the player holds, anywhere.
	SeatsHeldBy(ctx context.Context, playerID string) ([]Office, error)
	// AssignSeat writes a seat's holder, acquisition, term and since. A
	// vacant seat has an empty holder and acquisition and no term.
	AssignSeat(ctx context.Context, seat Office) error
	// ActingChain is an office followed by its deputies, each with its
	// seats in the jurisdiction, locked against a concurrent appointment
	// or vacancy: what Authorize walks.
	ActingChain(ctx context.Context, officeCode, jurisdictionID string) ([]OfficeLink, error)
}

// ResolvePolicy decides which value of a lever wins. It is the ONLY place
// that decision is made.
//
// The precedence, highest first:
//
//  1. the most recent office holder's setting whose notice has passed (the
//     latest effective_at not after now; the later announcement breaks a tie);
//  2. the city's own default, for a lever that names a per-city source;
//  3. the lever's content default.
//
// A setting inside its notice never applies; it is reported as Pending. The
// result is always clamped into the lever's current [min, max].
//
// It also names who may change the lever now, by walking the acting chain:
// the lever's office if any seat is held, else its deputy, else the deputy's
// deputy … else nobody, and the default behaviour runs.
//
// A setting outlives the holder who made it — a law stays in force when the
// mayor who passed it leaves — so a vacancy changes who may act, never the
// value. It is total: it has no error, because a vacant office is a state of
// the world, not a failure.
func ResolvePolicy(in PolicyInputs, now time.Time) PolicyValue {
	v := PolicyValue{
		JurisdictionID: in.Jurisdiction.ID,
		Lever:          in.Lever.Code,
		Type:           in.Lever.Type,
		Value:          in.Lever.Default,
		Source:         PolicyFromDefault,
		Acting:         actingOffice(in.Chain),
	}
	if in.CityDefault != nil {
		v.Value = *in.CityDefault
	}

	for i := range in.Settings {
		s := in.Settings[i]
		if s.EffectiveAt.After(now) {
			if v.Pending == nil || s.EffectiveAt.Before(v.Pending.EffectiveAt) ||
				(s.EffectiveAt.Equal(v.Pending.EffectiveAt) && s.SetAt.After(v.Pending.SetAt)) {
				v.Pending = &s
			}
			continue
		}
		if v.InForce == nil || laterSetting(s, *v.InForce) {
			v.InForce = &s
		}
	}
	if v.InForce != nil {
		v.Value = v.InForce.Value
		v.Source = PolicyFromOffice
	}

	if in.Lever.IsAllocation() {
		// An allocation's value is its shares. One set under older content
		// that no longer fits — a category gone, the whole exceeded — never
		// escapes the lever as it stands: the default applies instead.
		v.Value = 0
		v.Allocation = copyAllocation(in.Lever.DefaultAllocation)
		if v.InForce != nil {
			if CheckAllocation(in.Lever, v.InForce.Allocation) == nil {
				v.Allocation = copyAllocation(v.InForce.Allocation)
			} else {
				v.Clamped = true
			}
		}
		return v
	}

	switch {
	case v.Value < in.Lever.Min:
		v.Value, v.Clamped = in.Lever.Min, true
	case v.Value > in.Lever.Max:
		v.Value, v.Clamped = in.Lever.Max, true
	}
	return v
}

// laterSetting reports whether a took effect after b. Equal instants fall to
// the later announcement, then to the id, so the order is total.
func laterSetting(a, b PolicySetting) bool {
	if !a.EffectiveAt.Equal(b.EffectiveAt) {
		return a.EffectiveAt.After(b.EffectiveAt)
	}
	if !a.SetAt.Equal(b.SetAt) {
		return a.SetAt.After(b.SetAt)
	}
	return a.ID > b.ID
}

// actingOffice walks the chain and returns the first office with a held seat.
func actingOffice(chain []OfficeLink) *ActingOffice {
	for depth, link := range chain {
		var held []Office
		for _, s := range link.Seats {
			if !s.Vacant() {
				held = append(held, s)
			}
		}
		if len(held) > 0 {
			return &ActingOffice{OfficeCode: link.OfficeCode, Deputy: depth > 0, Holders: held}
		}
	}
	return nil
}

// CheckLeverApplies refuses a lever asked of a jurisdiction of another level.
func CheckLeverApplies(in PolicyInputs) error {
	if in.Jurisdiction.Kind != in.Lever.Jurisdiction {
		return ErrWrongJurisdiction.WithCause(fmt.Errorf("lever %s is set per %s, and %s is a %s",
			in.Lever.Code, in.Lever.Jurisdiction, in.Jurisdiction.Code, in.Jurisdiction.Kind))
	}
	return nil
}

// copyAllocation copies shares, dropping the empty ones.
func copyAllocation(a map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(a))
	for k, v := range a {
		if v != 0 {
			out[k] = v
		}
	}
	return out
}

// CheckAllocation refuses shares an allocation lever may not hold: a category
// it does not divide among, a negative share, a sum beyond the whole.
func CheckAllocation(l LeverDefinition, shares map[string]int64) error {
	var total int64
	for k, v := range shares {
		known := false
		for _, c := range l.Categories {
			if c == k {
				known = true
				break
			}
		}
		if !known {
			return ErrInvalidAllocation.WithDetail("category", k)
		}
		if v < 0 {
			return ErrInvalidAllocation.WithDetail("category", k).WithDetail("share", v)
		}
		total += v
	}
	if total > AllocationWhole {
		return ErrInvalidAllocation.WithDetail("total", total)
	}
	return nil
}

// SameAllocation reports whether two allocations give every category the
// same share.
func SameAllocation(a, b map[string]int64) bool {
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// CheckLeverSupported refuses a structured lever with no behaviour yet: a
// yes/no, a choice or a table. Scalars and allocations are usable.
func CheckLeverSupported(l LeverDefinition) error {
	if l.ValueKind != ValueKindScalar && !l.IsAllocation() {
		return ErrLeverKindUnsupported.WithCause(fmt.Errorf("lever %s is of type %s (%s), which has no behaviour yet",
			l.Code, l.Type, l.ValueKind))
	}
	return nil
}

// ProposedValue is a value proposed for a lever: an integer for a scalar
// lever, shares for an allocation.
type ProposedValue struct {
	Value      int64
	Allocation map[string]int64
}

// SetPolicy changes a scalar lever on behalf of the player who holds the
// office acting for it, and records the change publicly. It must run on the
// caller's unit of work, so the value and its public record commit together.
//
// It refuses, writing nothing, with a distinct sentinel for each case:
//
//   - ErrWrongJurisdiction: the lever is not set at this jurisdiction's level;
//   - ErrLeverKindUnsupported: the lever holds a structured value;
//   - ErrPolicyRequiresVote: the lever is decided by a vote of a body;
//   - ErrNotOfficeHolder: the player does not hold the acting office here —
//     the lever's office, or while that is vacant the deputy acting for it;
//   - ErrPolicyOutOfBounds: value outside the lever's [min, max];
//   - ErrPolicyCooldown: the lever changed here less than change_cooldown ago;
//   - ErrPolicyRequiresConfirmation: the change must first pass a vote of the
//     body the lever names (docs/adr/0024-property-and-politics.md).
//
// The change takes effect at now + notice: announced now, applied later, so
// no policy change is a surprise (ADR 0015 section 2).
func SetPolicy(ctx context.Context, tx Tx, holderPlayerID, jurisdictionID, leverCode string,
	value int64, now time.Time,
) (PolicyChange, error) {
	return ChangePolicy(ctx, tx, holderPlayerID, jurisdictionID, leverCode, ProposedValue{Value: value}, now)
}

// SetAllocation is SetPolicy for an allocation lever: the shares of each
// category, summing to at most the whole (ErrInvalidAllocation otherwise).
func SetAllocation(ctx context.Context, tx Tx, holderPlayerID, jurisdictionID, leverCode string,
	shares map[string]int64, now time.Time,
) (PolicyChange, error) {
	return ChangePolicy(ctx, tx, holderPlayerID, jurisdictionID, leverCode, ProposedValue{Allocation: shares}, now)
}

// ChangePolicy is SetPolicy and SetAllocation.
func ChangePolicy(ctx context.Context, tx Tx, holderPlayerID, jurisdictionID, leverCode string,
	v ProposedValue, now time.Time,
) (PolicyChange, error) {
	now = now.UTC()
	in, current, err := lockPolicy(ctx, tx, jurisdictionID, leverCode, now)
	if err != nil {
		return PolicyChange{}, err
	}
	if in.Lever.ByVote() {
		return PolicyChange{}, ErrPolicyRequiresVote.WithDetail("body", in.Lever.HeldBy).
			WithCause(fmt.Errorf("%s requires a %s vote of %s", in.Lever.Code, in.Lever.DecisionRule, in.Lever.HeldBy))
	}

	seat, ok := heldSeat(current.Acting, holderPlayerID)
	if !ok {
		return PolicyChange{}, ErrNotOfficeHolder.WithDetail("office", in.Lever.HeldBy)
	}
	if err := checkProposed(in, v, now); err != nil {
		return PolicyChange{}, err
	}
	need, err := NeedsConfirmation(ctx, tx, in.Lever, in.Jurisdiction.ID, current, v)
	if err != nil {
		return PolicyChange{}, err
	}
	if need {
		return PolicyChange{}, ErrPolicyRequiresConfirmation.WithDetail("body", in.Lever.RequiresConfirmationBy)
	}
	return recordChange(ctx, tx, in, current, v, holderPlayerID, seat.ID, seat.OfficeCode, now)
}

// lockPolicy serialises changes of one lever in one place, loads the
// resolver's inputs and answers the value in force, refusing a lever of
// another level or of a type with no behaviour.
func lockPolicy(ctx context.Context, tx Tx, jurisdictionID, leverCode string, now time.Time,
) (PolicyInputs, PolicyValue, error) {
	gov := tx.Governance()
	// First, so the cooldown and the current value read below cannot change
	// under this transaction before it writes.
	if err := gov.LockLever(ctx, jurisdictionID, leverCode); err != nil {
		return PolicyInputs{}, PolicyValue{}, err
	}
	in, err := gov.PolicyInputs(ctx, jurisdictionID, leverCode, now)
	if err != nil {
		return in, PolicyValue{}, err
	}
	if err := CheckLeverApplies(in); err != nil {
		return in, PolicyValue{}, err
	}
	if err := CheckLeverSupported(in.Lever); err != nil {
		return in, PolicyValue{}, err
	}
	return in, ResolvePolicy(in, now), nil
}

// checkProposed refuses a value outside the lever's bounds, or shares its
// allocation may not hold, and a change inside the cooldown.
func checkProposed(in PolicyInputs, v ProposedValue, now time.Time) error {
	if in.Lever.IsAllocation() != (v.Allocation != nil) {
		// A number for an allocation, or shares for a number: never a
		// value of this lever.
		return ErrInvalidAllocation.WithDetail("lever", in.Lever.Code)
	}
	if in.Lever.IsAllocation() {
		if err := CheckAllocation(in.Lever, v.Allocation); err != nil {
			return err
		}
	} else if v.Value < in.Lever.Min || v.Value > in.Lever.Max {
		return ErrPolicyOutOfBounds.
			WithDetail("min", in.Lever.Min).WithDetail("max", in.Lever.Max).WithDetail("value", v.Value)
	}
	if in.LastChangeAt != nil {
		if next := in.LastChangeAt.Add(in.Lever.ChangeCooldown); now.Before(next) {
			return ErrPolicyCooldown.WithDetail("available_at", next.UTC())
		}
	}
	return nil
}

// NeedsConfirmation reports whether a change must first pass a vote of the
// body the lever names: the lever names one, the change is larger than its
// confirm_above (a scalar lever), and the body has a member seated. A body
// nobody sits in confirms nothing and blocks nothing: the holder decides, as
// before the body existed (docs/adr/0024-property-and-politics.md).
func NeedsConfirmation(ctx context.Context, tx Tx, l LeverDefinition, jurisdictionID string, current PolicyValue,
	v ProposedValue,
) (bool, error) {
	if l.RequiresConfirmationBy == "" {
		return false, nil
	}
	if l.ConfirmAbove != nil && !l.IsAllocation() {
		diff := v.Value - current.Value
		if diff < 0 {
			diff = -diff
		}
		if diff <= *l.ConfirmAbove {
			return false, nil
		}
	}
	return BodySeated(ctx, tx, l.RequiresConfirmationBy, jurisdictionID)
}

// BodySeated reports whether any seat of an office is held in a
// jurisdiction.
func BodySeated(ctx context.Context, tx Tx, officeCode, jurisdictionID string) (bool, error) {
	chain, err := tx.Governance().ActingChain(ctx, officeCode, jurisdictionID)
	if err != nil || len(chain) == 0 {
		return false, err
	}
	for _, s := range chain[0].Seats {
		if !s.Vacant() {
			return true, nil
		}
	}
	return false, nil
}

// recordChange writes the change and its public record.
func recordChange(ctx context.Context, tx Tx, in PolicyInputs, current PolicyValue, v ProposedValue,
	playerID, officeID, officeCode string, now time.Time,
) (PolicyChange, error) {
	setting := PolicySetting{
		JurisdictionID: in.Jurisdiction.ID,
		LeverCode:      in.Lever.Code,
		Value:          v.Value,
		SetByPlayerID:  playerID,
		OfficeID:       officeID,
		SetAt:          now,
		EffectiveAt:    now.Add(in.Lever.Notice),
	}
	change := PolicyChange{Setting: setting, OfficeCode: officeCode, OldValue: current.Value}
	if in.Lever.IsAllocation() {
		change.Setting.Value = 0
		change.Setting.Allocation = copyAllocation(v.Allocation)
		change.OldAllocation = copyAllocation(current.Allocation)
		change.OldValue = 0
	}
	return tx.Governance().RecordPolicy(ctx, change)
}

// PolicyDraft is a change proposed to a vote: the lever, the place, the value,
// the seat it is proposed from, and the body that decides and how.
type PolicyDraft struct {
	Lever        LeverDefinition
	Jurisdiction Jurisdiction
	Value        ProposedValue
	Current      PolicyValue
	Seat         Office
	// Body is the office whose members vote; Rule, Threshold and Quorum how.
	Body, Rule, Threshold, Quorum string
}

// DraftPolicyVote checks a change before it goes to a vote, under the same
// lock SetPolicy takes: the lever applies here and has behaviour, the
// proposer may propose it — a member of the body that holds a lever decided
// by vote, or the holder acting for a lever that needs confirmation — the
// value fits and the cooldown is over. It writes nothing. A lever needing
// neither a vote nor, for this change, a confirmation is answered with
// ok false: set it directly.
func DraftPolicyVote(ctx context.Context, tx Tx, proposerID, jurisdictionID, leverCode string, v ProposedValue,
	now time.Time,
) (PolicyDraft, bool, error) {
	now = now.UTC()
	in, current, err := lockPolicy(ctx, tx, jurisdictionID, leverCode, now)
	if err != nil {
		return PolicyDraft{}, false, err
	}
	d := PolicyDraft{Lever: in.Lever, Jurisdiction: in.Jurisdiction, Value: v, Current: current}
	switch {
	case in.Lever.ByVote():
		chain, err := tx.Governance().ActingChain(ctx, in.Lever.HeldBy, jurisdictionID)
		if err != nil {
			return d, false, err
		}
		var seat Office
		ok := false
		if len(chain) > 0 {
			for _, s := range chain[0].Seats {
				if s.HolderPlayerID == proposerID && proposerID != "" {
					seat, ok = s, true
				}
			}
		}
		if !ok {
			return d, false, ErrNotOfficeHolder.WithDetail("office", in.Lever.HeldBy)
		}
		d.Seat, d.Body, d.Rule = seat, in.Lever.HeldBy, in.Lever.DecisionRule
		d.Threshold, d.Quorum = in.Lever.Threshold, in.Lever.Quorum
	default:
		seat, ok := heldSeat(current.Acting, proposerID)
		if !ok {
			return d, false, ErrNotOfficeHolder.WithDetail("office", in.Lever.HeldBy)
		}
		need, err := NeedsConfirmation(ctx, tx, in.Lever, jurisdictionID, current, v)
		if err != nil || !need {
			return d, false, err
		}
		d.Seat, d.Body, d.Rule = seat, in.Lever.RequiresConfirmationBy, in.Lever.ConfirmationRule
		if d.Rule == "" {
			d.Rule = "majority"
		}
		d.Threshold, d.Quorum = in.Lever.ConfirmationThreshold, in.Lever.ConfirmationQuorum
	}
	if err := checkProposed(in, v, now); err != nil {
		return d, false, err
	}
	return d, true, nil
}

// ApplyVotedPolicy announces a change a vote has passed, on behalf of the
// member who proposed it, from the seat they proposed it from. The notice
// runs from now. It refuses, writing nothing, what no longer fits the lever
// as it stands — its bounds or categories changed, or another change landed
// inside the cooldown while the vote ran — with SetPolicy's sentinels.
func ApplyVotedPolicy(ctx context.Context, tx Tx, proposerID, officeID, officeCode, jurisdictionID, leverCode string,
	v ProposedValue, now time.Time,
) (PolicyChange, error) {
	now = now.UTC()
	in, current, err := lockPolicy(ctx, tx, jurisdictionID, leverCode, now)
	if err != nil {
		return PolicyChange{}, err
	}
	if err := checkProposed(in, v, now); err != nil {
		return PolicyChange{}, err
	}
	return recordChange(ctx, tx, in, current, v, proposerID, officeID, officeCode, now)
}

// heldSeat finds the player's seat among the acting office's holders.
func heldSeat(acting *ActingOffice, playerID string) (Office, bool) {
	if acting == nil || playerID == "" {
		return Office{}, false
	}
	for _, s := range acting.Holders {
		if s.HolderPlayerID == playerID {
			return s, true
		}
	}
	return Office{}, false
}

// AppointToOffice seats a player in one seat of an office. It is the
// operator's path until elections exist; the caller records the audit row in
// the same transaction.
//
// It refuses with ErrOfficeNotFound (no such office or seat),
// ErrWrongJurisdiction (the office is not of this jurisdiction's level),
// ErrOfficeOccupied (somebody else holds the seat — vacate first),
// ErrAlreadyHoldsSeat (the player holds this seat, or another seat of this
// office here) and ErrIncompatibleOffices (the player holds an office
// declared incompatible with this one, in either direction, anywhere).
//
// The seat's term ends at now + the office's term, or never for an office
// held at pleasure. The term is real time (docs/adr/0018-game-clock.md); an
// elected office's next election is timed so its count ends the term.
func AppointToOffice(ctx context.Context, tx Tx, officeCode, jurisdictionID string, seat int,
	playerID string, now time.Time,
) (before, after Office, err error) {
	return fillSeat(ctx, tx, officeCode, jurisdictionID, seat, playerID, AcquiredByAppointment, now)
}

// AcquiredByConquest is offices.acquired_by for a seat taken in war: a
// city's military governor (docs/adr/0022, part two).
const AcquiredByConquest = "conquest"

// ConquerToOffice seats the commander who took a city in its conquest
// office, under the same checks as an appointment.
func ConquerToOffice(ctx context.Context, tx Tx, officeCode, jurisdictionID string, seat int,
	playerID string, now time.Time,
) (before, after Office, err error) {
	return fillSeat(ctx, tx, officeCode, jurisdictionID, seat, playerID, AcquiredByConquest, now)
}

// ElectToOffice seats the winner of an election's count, under the same
// checks as an appointment; the seat must be vacated first.
func ElectToOffice(ctx context.Context, tx Tx, officeCode, jurisdictionID string, seat int,
	playerID string, now time.Time,
) (before, after Office, err error) {
	return fillSeat(ctx, tx, officeCode, jurisdictionID, seat, playerID, AcquiredByElection, now)
}

// fillSeat gives a vacant seat a holder, by appointment or election.
func fillSeat(ctx context.Context, tx Tx, officeCode, jurisdictionID string, seat int,
	playerID, acquiredBy string, now time.Time,
) (before, after Office, err error) {
	gov := tx.Governance()
	now = now.UTC()

	defs, err := gov.OfficeDefinitions(ctx)
	if err != nil {
		return Office{}, Office{}, err
	}
	byCode := make(map[string]OfficeDefinition, len(defs))
	for _, d := range defs {
		byCode[d.Code] = d
	}
	def, ok := byCode[officeCode]
	if !ok {
		return Office{}, Office{}, ErrOfficeNotFound.WithCause(fmt.Errorf("no office %q in the active content", officeCode))
	}
	j, err := gov.Jurisdiction(ctx, jurisdictionID)
	if err != nil {
		return Office{}, Office{}, err
	}
	if j.Kind != def.Jurisdiction {
		return Office{}, Office{}, ErrWrongJurisdiction.WithCause(fmt.Errorf("%s is a %s office and %s is a %s",
			officeCode, def.Jurisdiction, j.Code, j.Kind))
	}

	before, err = gov.Seat(ctx, officeCode, jurisdictionID, seat)
	if err != nil {
		return Office{}, Office{}, err
	}
	switch before.HolderPlayerID {
	case "":
	case playerID:
		return before, Office{}, ErrAlreadyHoldsSeat
	default:
		return before, Office{}, ErrOfficeOccupied
	}

	held, err := gov.SeatsHeldBy(ctx, playerID)
	if err != nil {
		return before, Office{}, err
	}
	for _, h := range held {
		if h.OfficeCode == officeCode && h.JurisdictionID == jurisdictionID {
			return before, Office{}, ErrAlreadyHoldsSeat.WithDetail("seat", h.Seat)
		}
		if incompatible(def, byCode[h.OfficeCode]) {
			return before, Office{}, ErrIncompatibleOffices.WithCause(fmt.Errorf(
				"the player holds %s, which is incompatible with %s", h.OfficeCode, officeCode))
		}
	}

	after = before
	after.HolderPlayerID = playerID
	after.AcquiredBy = acquiredBy
	after.Since = now
	after.TermEndsAt = nil
	if def.Term > 0 {
		ends := now.Add(def.Term)
		after.TermEndsAt = &ends
	}
	if err := gov.AssignSeat(ctx, after); err != nil {
		return before, Office{}, err
	}
	return before, after, nil
}

// incompatible reports whether a and b may not be held by one player. The
// relation is symmetric: stating it on either office binds both.
func incompatible(a, b OfficeDefinition) bool {
	for _, x := range a.IncompatibleWith {
		if x == b.Code {
			return true
		}
	}
	for _, x := range b.IncompatibleWith {
		if x == a.Code {
			return true
		}
	}
	return false
}

// VacateOffice empties one seat. The values its holder set stay in force: a
// vacancy changes who may act, never the law. It refuses ErrOfficeNotFound
// and ErrOfficeVacant.
func VacateOffice(ctx context.Context, tx Tx, officeCode, jurisdictionID string, seat int,
	now time.Time,
) (before, after Office, err error) {
	gov := tx.Governance()
	before, err = gov.Seat(ctx, officeCode, jurisdictionID, seat)
	if err != nil {
		return Office{}, Office{}, err
	}
	if before.Vacant() {
		return before, Office{}, ErrOfficeVacant
	}
	after = before
	after.HolderPlayerID = ""
	after.AcquiredBy = ""
	after.TermEndsAt = nil
	after.Since = now.UTC()
	if err := gov.AssignSeat(ctx, after); err != nil {
		return before, Office{}, err
	}
	return before, after, nil
}
