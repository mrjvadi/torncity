package application

import (
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// GovernanceRefusal classifies an error as one of the governance refusals the
// presentation layer has a sentence for. Kind is a stable code (the neutral
// contract's GovRefusal kinds); Office names the office or body the refusal is
// about, and AvailableAt is when a cooldown ends; each is empty when unknown.
type GovernanceRefusal struct {
	Kind        string
	Office      string
	AvailableAt time.Time
}

var governanceSentinelKinds = []struct {
	target error
	kind   string
}{
	{ErrUnknownLever, "unknown_lever"},
	{ErrJurisdictionNotFound, "unknown_place"},
	{ErrWrongJurisdiction, "wrong_place"},
	{ErrCityTierOnly, "city_only"},
	{ErrLeverKindUnsupported, "unsupported"},
	{ErrInvalidAllocation, "invalid_allocation"},
	{ErrOfficeNotFound, "office_not_found"},
	{ErrOfficeOccupied, "office_occupied"},
	{ErrOfficeVacant, "office_vacant"},
	{ErrAlreadyHoldsSeat, "already_holds"},
	{ErrIncompatibleOffices, "incompatible"},
}

// ClassifyGovernanceRefusal reports which governance refusal err is, if any.
func ClassifyGovernanceRefusal(err error) (GovernanceRefusal, bool) {
	switch {
	case stderrors.Is(err, ErrNotOfficeHolder):
		return GovernanceRefusal{Kind: "not_holder", Office: errorDetailString(err, "office")}, true
	case stderrors.Is(err, ErrPolicyRequiresConfirmation):
		return GovernanceRefusal{Kind: "requires_confirmation", Office: errorDetailString(err, "body")}, true
	case stderrors.Is(err, ErrPolicyRequiresVote):
		return GovernanceRefusal{Kind: "requires_vote", Office: errorDetailString(err, "body")}, true
	case stderrors.Is(err, ErrPolicyOutOfBounds):
		return GovernanceRefusal{Kind: "out_of_range"}, true
	case stderrors.Is(err, ErrPolicyCooldown):
		r := GovernanceRefusal{Kind: "cooldown"}
		var e *errors.Error
		if stderrors.As(err, &e) {
			r.AvailableAt, _ = e.Details["available_at"].(time.Time)
		}
		return r, true
	}
	for _, s := range governanceSentinelKinds {
		if stderrors.Is(err, s.target) {
			return GovernanceRefusal{Kind: s.kind}, true
		}
	}
	return GovernanceRefusal{}, false
}

func errorDetailString(err error, key string) string {
	var e *errors.Error
	if !stderrors.As(err, &e) {
		return ""
	}
	s, _ := e.Details[key].(string)
	return s
}

// Wait is how long a cooldown still runs at now; zero when it is over or the
// refusal is not a cooldown.
func (r GovernanceRefusal) Wait(now time.Time) time.Duration {
	if r.AvailableAt.IsZero() || !r.AvailableAt.After(now) {
		return 0
	}
	return r.AvailableAt.Sub(now)
}
