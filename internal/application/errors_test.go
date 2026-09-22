package application

import (
	stderrors "errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// namedSentinels is every sentinel a caller may branch on.
//
// Adding one here is the cheap half of adding one at all: the tests below turn
// the list into a guarantee that the new sentinel is actually distinguishable.
var namedSentinels = map[string]*errors.Error{
	"ErrPlayerNotFound":    ErrPlayerNotFound,
	"ErrCityNotFound":      ErrCityNotFound,
	"ErrNoActiveTravel":    ErrNoActiveTravel,
	"ErrAlreadyTravelling": ErrAlreadyTravelling,
	"ErrSkillNotFound":     ErrSkillNotFound,
	"ErrNotFriends":        ErrNotFriends,
	"ErrAlreadyFriends":    ErrAlreadyFriends,
}

// TestSentinelsAreDistinguishable is the regression test for a real defect.
//
// These sentinels were built with the per-code helpers, which carry no
// identity, so errors.Is matched on the code alone. ErrAlreadyTravelling and
// ErrAlreadyFriends are both conflicts, and every not-found matched every
// other not-found: a handler branching on one caught the rest and took the
// wrong branch, with no error and no log to show for it.
//
// If this fails, someone replaced an errors.Sentinel with a bare helper.
func TestSentinelsAreDistinguishable(t *testing.T) {
	for nameA, a := range namedSentinels {
		for nameB, b := range namedSentinels {
			if nameA == nameB {
				continue
			}
			if stderrors.Is(a, b) {
				t.Errorf("%s matches %s: a handler branching on one will also catch the other", nameA, nameB)
			}
		}
	}
}

// TestSentinelMatchesItself is the other half: distinguishable must not mean
// unmatchable.
func TestSentinelMatchesItself(t *testing.T) {
	for name, s := range namedSentinels {
		if !stderrors.Is(s, s) {
			t.Errorf("%s does not match itself", name)
		}
	}
}

// TestSentinelsStillMatchTheirClass keeps the broad question answerable.
//
// A class sentinel carries no id, so it must still match any error of its
// code. Losing that would mean "is this a conflict at all?" needs a list of
// every conflict in the program.
func TestSentinelsStillMatchTheirClass(t *testing.T) {
	tests := []struct {
		name     string
		sentinel *errors.Error
		class    error
	}{
		{"ErrPlayerNotFound", ErrPlayerNotFound, errors.ErrNotFound},
		{"ErrCityNotFound", ErrCityNotFound, errors.ErrNotFound},
		{"ErrNoActiveTravel", ErrNoActiveTravel, errors.ErrNotFound},
		{"ErrAlreadyTravelling", ErrAlreadyTravelling, errors.ErrConflict},
		{"ErrAlreadyFriends", ErrAlreadyFriends, errors.ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !stderrors.Is(tt.sentinel, tt.class) {
				t.Errorf("%s no longer matches its class", tt.name)
			}
		})
	}
}

// TestWrappingKeepsIdentity proves a sentinel survives WithCause.
//
// A repository that adds the driver error for the logs must still return
// something the handler recognises, or adding context would silently break
// every branch downstream.
func TestWrappingKeepsIdentity(t *testing.T) {
	cause := stderrors.New("connection reset")
	wrapped := ErrAlreadyTravelling.WithCause(cause)

	if !stderrors.Is(wrapped, ErrAlreadyTravelling) {
		t.Error("wrapping lost the sentinel identity")
	}
	if stderrors.Is(wrapped, ErrAlreadyFriends) {
		t.Error("wrapping widened the match to another conflict")
	}
	if !stderrors.Is(wrapped, cause) {
		t.Error("the cause is no longer reachable")
	}
}

// TestWrappingDoesNotMutateTheSentinel is the second defect this fixes.
//
// A sentinel is a package-level variable shared by every goroutine. If
// WithCause mutated it in place, one request attaching its database error
// would attach it to every other request's copy too, and race while doing it.
func TestWrappingDoesNotMutateTheSentinel(t *testing.T) {
	before := ErrAlreadyTravelling.Error()
	_ = ErrAlreadyTravelling.WithCause(stderrors.New("a transient failure"))
	if after := ErrAlreadyTravelling.Error(); after != before {
		t.Errorf("the shared sentinel was mutated: %q became %q", before, after)
	}
}

// TestSentinelIDsAreUnique catches a copy-paste before it reaches a handler.
//
// Two sentinels sharing an id are interchangeable again, which is the exact
// bug the id exists to prevent, and it would pass every other test here.
func TestSentinelIDsAreUnique(t *testing.T) {
	seen := make(map[string]string, len(namedSentinels))
	for name, s := range namedSentinels {
		id := s.ID()
		if id == "" {
			t.Errorf("%s has no id, so it matches every other error of its code", name)
			continue
		}
		if other, dup := seen[id]; dup {
			t.Errorf("%s and %s share the id %q", name, other, id)
		}
		seen[id] = name
	}
}
