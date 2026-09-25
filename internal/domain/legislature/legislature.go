// Package legislature holds the rules of a body deciding by vote: a council
// approving a mayor's budget, a national assembly approving a declaration of
// war, a council setting a lever it holds itself. A proposal is put to the
// seated members of the body; each seat votes once, yes or no; the vote
// closes when its window ends, or earlier the moment the outcome can no
// longer change.
//
// Which bodies exist, how many seats each has and what each must confirm is
// CONTENT (configs/content/governance.yml: decision_rule and
// requires_confirmation_by); who sits in them is the governance package's.
// The package reads no clock and no file.
//
// # Real time
//
// A vote's window is REAL time, like a lever's notice and an election
// (docs/adr/0018-game-clock.md): the days a member has to vote are a promise
// on their own calendar.
//
// # The rules
//
//   - majority: more yes than no among the votes cast;
//   - supermajority: yes at least the threshold of the votes cast, e.g. 2/3;
//   - unanimous: every seated member voted yes.
//
// A quorum, when the body has one, is the share of the body's SEATS that must
// have voted for any outcome but failure. Without one, at least one vote must
// be cast. A tie under majority fails: a change needs more support than
// opposition.
package legislature

import (
	"errors"
	"fmt"
)

// Rule kinds, exactly as content spells them.
const (
	Majority      = "majority"
	Supermajority = "supermajority"
	Unanimous     = "unanimous"
)

// ErrInvalidRule means a rule the package cannot decide by.
var ErrInvalidRule = errors.New("legislature: invalid rule")

// Rule is how a body decides.
type Rule struct {
	Kind string
	// Num/Den is a supermajority's threshold; zero otherwise.
	Num, Den int
	// QuorumNum/QuorumDen is the share of seats that must vote; zero for
	// none.
	QuorumNum, QuorumDen int
}

// Validate checks the rule.
func (r Rule) Validate() error {
	switch r.Kind {
	case Majority, Unanimous:
		if r.Num != 0 || r.Den != 0 {
			return fmt.Errorf("%w: %s takes no threshold", ErrInvalidRule, r.Kind)
		}
	case Supermajority:
		if r.Den <= 0 || r.Num <= 0 || r.Num > r.Den || 2*r.Num <= r.Den {
			return fmt.Errorf("%w: supermajority threshold %d/%d is not above a half", ErrInvalidRule, r.Num, r.Den)
		}
	default:
		return fmt.Errorf("%w: %q", ErrInvalidRule, r.Kind)
	}
	if (r.QuorumNum == 0) != (r.QuorumDen == 0) || r.QuorumNum < 0 || r.QuorumNum > r.QuorumDen {
		return fmt.Errorf("%w: quorum %d/%d", ErrInvalidRule, r.QuorumNum, r.QuorumDen)
	}
	return nil
}

// Tally is the state of a vote.
type Tally struct {
	// Seats is how many seats the body has; Held how many are held now.
	Seats, Held int
	// Yes and No are the votes cast.
	Yes, No int
}

// Outcome is where a vote stands.
type Outcome string

// Outcomes.
const (
	Pending Outcome = "pending"
	Passed  Outcome = "passed"
	Failed  Outcome = "failed"
)

// quorumNeeded is how many votes must be cast for the vote to count.
func (r Rule) quorumNeeded(seats int) int {
	if r.QuorumDen == 0 {
		return 1
	}
	n := (seats*r.QuorumNum + r.QuorumDen - 1) / r.QuorumDen
	return max(n, 1)
}

// passes reports whether yes and no, as cast, carry the rule; held is the
// seated members, for unanimity.
func (r Rule) passes(yes, no, held int) bool {
	switch r.Kind {
	case Majority:
		return yes > no
	case Supermajority:
		return yes*r.Den >= r.Num*(yes+no) && yes > 0
	case Unanimous:
		return no == 0 && yes > 0 && yes >= held
	}
	return false
}

// Decide says where a vote stands. final is true once the window has closed:
// then it is Passed or Failed, never Pending. Before that it is decided early
// only when no vote still to come could change it: every seated member who
// has not voted is counted both ways.
func Decide(r Rule, t Tally, final bool) Outcome {
	cast := t.Yes + t.No
	remaining := max(t.Held-cast, 0)
	need := r.quorumNeeded(t.Seats)
	if final {
		if cast < need || !r.passes(t.Yes, t.No, t.Held) {
			return Failed
		}
		return Passed
	}
	// Early failure: even if every remaining member votes yes, it cannot
	// pass, or the quorum cannot be reached.
	if cast+remaining < need || !r.passes(t.Yes+remaining, t.No, t.Held) {
		return Failed
	}
	// Early pass: even if every remaining member votes no, it passes, and
	// the quorum is met already.
	if cast >= need && remaining == 0 && r.passes(t.Yes, t.No, t.Held) {
		return Passed
	}
	if cast >= need && r.Kind != Unanimous && r.passes(t.Yes, t.No+remaining, t.Held) {
		return Passed
	}
	return Pending
}

// Needs is how many yes votes carry the vote if every seated member votes:
// for the screen, «۳ رأی موافق از ۵».
func Needs(r Rule, held int) int {
	for yes := 0; yes <= held; yes++ {
		if r.passes(yes, held-yes, held) {
			return yes
		}
	}
	return held + 1
}
