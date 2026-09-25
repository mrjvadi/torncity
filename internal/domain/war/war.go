// Package war holds the rules of war between countries
// (docs/adr/0022-military-and-diplomacy.md, part two): the state of a war —
// declared with a notice, active, suspended by a ceasefire, ended by a peace —
// who is party to it on which side, what a ceasefire or a peace proposal may
// be; and the battles fought in it — an air or missile strike through a
// layered air defence, a ground assault on a city — and what a strike does to
// a city's economy and how fast it recovers.
//
// Everything here is integer arithmetic with no clock, no randomness of its
// own and no I/O: a caller passes its now, and a battle its seed. The same
// inputs always give the same outcome, so an operation resolved twice
// resolves the same, and a test can name the dice.
package war

import (
	"errors"
	"fmt"
	"time"
)

// BPSWhole is one hundred percent in basis points.
const BPSWhole = 10_000

// Sentinel errors.
var (
	// ErrInvalid means an input no caller should pass.
	ErrInvalid = errors.New("war: invalid input")
	// ErrSelf means a country declared war on itself, or joined a war
	// against itself.
	ErrSelf = errors.New("war: a country and itself")
	// ErrAtWar means the two countries are already at war, or one is
	// declaring a war it is already party to.
	ErrAtWar = errors.New("war: already at war")
	// ErrNotParty means a country acted on a war it is not party to, or on
	// the wrong side of it.
	ErrNotParty = errors.New("war: not a party")
	// ErrState means a transition the war's state does not allow: a
	// ceasefire in a war that is not being fought, a peace in one that is
	// over, an operation before the notice has run.
	ErrState = errors.New("war: the war is not in a state for that")
	// ErrProposalOpen means a proposal of that kind waits for an answer
	// already.
	ErrProposalOpen = errors.New("war: a proposal of that kind is open already")
)

// Status is a war's state.
type Status string

const (
	// Declared is a war announced whose notice has not run: no operation
	// may be launched yet (Hague Convention III, 1907: no hostilities
	// without previous and explicit warning).
	Declared Status = "declared"
	// Active is a war being fought.
	Active Status = "active"
	// Ceasefire is a war whose operations are suspended by agreement
	// (Hague Regulations art. 36): it is not over, and either party may
	// resume it after the same notice as a declaration.
	Ceasefire Status = "ceasefire"
	// Ended is a war closed by a peace.
	Ended Status = "ended"
)

// Side is the side a party fights on.
type Side string

const (
	Attacker Side = "attacker"
	Defender Side = "defender"
)

// Other is the opposing side.
func (s Side) Other() Side {
	if s == Attacker {
		return Defender
	}
	return Attacker
}

// Party is one country in a war, on one side.
type Party struct {
	Country string
	Side    Side
}

// War is one war as the rules take it.
type War struct {
	ID string
	// Attacker declared it on Defender: the principal parties, the only
	// ones who may agree a ceasefire or a peace.
	Attacker, Defender string
	// Status is the stored state; ActiveAt is when a declared war's notice
	// runs out (StatusAt reads Declared as Active from then).
	Status   Status
	ActiveAt time.Time
	// Parties are every party, the principals included; allies joined.
	Parties []Party
}

// StatusAt is the war's state at now: a declared war whose notice has run
// is active.
func (w War) StatusAt(now time.Time) Status {
	if w.Status == Declared && !now.Before(w.ActiveAt) {
		return Active
	}
	return w.Status
}

// Open reports whether the war is not over.
func (w War) Open() bool { return w.Status != Ended }

// SideOf is the side the country fights on, if it is a party.
func (w War) SideOf(country string) (Side, bool) {
	switch country {
	case "":
		return "", false
	case w.Attacker:
		return Attacker, true
	case w.Defender:
		return Defender, true
	}
	for _, p := range w.Parties {
		if p.Country == country {
			return p.Side, true
		}
	}
	return "", false
}

// Principal is the principal party of a side.
func (w War) Principal(s Side) string {
	if s == Attacker {
		return w.Attacker
	}
	return w.Defender
}

// Opposed reports whether a and b are parties on opposite sides.
func (w War) Opposed(a, b string) bool {
	sa, oka := w.SideOf(a)
	sb, okb := w.SideOf(b)
	return oka && okb && sa != sb
}

// Hostile reports whether a and b may fight each other at now: parties on
// opposite sides of a war that is active.
func (w War) Hostile(a, b string, now time.Time) bool {
	return w.StatusAt(now) == Active && w.Opposed(a, b)
}

// Between finds the war in which a and b are on opposite sides and that is
// not over. There is at most one: a second declaration between two parties
// already opposed is refused.
func Between(wars []War, a, b string) (War, bool) {
	for _, w := range wars {
		if w.Open() && w.Opposed(a, b) {
			return w, true
		}
	}
	return War{}, false
}

// AtWar reports whether the country is party to a war being fought at now.
func AtWar(wars []War, country string, now time.Time) bool {
	for _, w := range wars {
		if _, ok := w.SideOf(country); ok && w.StatusAt(now) == Active {
			return true
		}
	}
	return false
}

// CheckDeclare reports whether attacker may declare war on defender, given
// the wars not over that either is party to.
func CheckDeclare(attacker, defender string, open []War) error {
	if attacker == "" || defender == "" {
		return ErrInvalid
	}
	if attacker == defender {
		return ErrSelf
	}
	if _, ok := Between(open, attacker, defender); ok {
		return ErrAtWar
	}
	return nil
}

// CheckJoin reports whether country may join the war on side at now: it is
// no party yet, it is not the side's enemy, and the war is not over.
func CheckJoin(w War, country string, side Side, now time.Time) error {
	if side != Attacker && side != Defender {
		return ErrInvalid
	}
	if _, ok := w.SideOf(country); ok {
		return ErrAtWar
	}
	if country == w.Principal(side.Other()) {
		return ErrSelf
	}
	if !w.Open() {
		return ErrState
	}
	return nil
}

// ProposalKind is what a proposal between the principals would do.
type ProposalKind string

const (
	// ProposeCeasefire suspends the war's operations.
	ProposeCeasefire ProposalKind = "ceasefire"
	// ProposePeace ends the war.
	ProposePeace ProposalKind = "peace"
)

// Valid reports whether k is one of the two.
func (k ProposalKind) Valid() bool { return k == ProposeCeasefire || k == ProposePeace }

// Proposal statuses.
type ProposalStatus string

const (
	ProposalOpen      ProposalStatus = "proposed"
	ProposalAccepted  ProposalStatus = "accepted"
	ProposalDeclined  ProposalStatus = "declined"
	ProposalWithdrawn ProposalStatus = "withdrawn"
	ProposalExpired   ProposalStatus = "expired"
)

// Proposal is a ceasefire or a peace one principal offered the other.
type Proposal struct {
	ID        string
	Kind      ProposalKind
	Proposer  string
	Partner   string
	Status    ProposalStatus
	ExpiresAt time.Time
}

// StatusAt is the proposal's status at now: one nobody answered in time is
// expired.
func (p Proposal) StatusAt(now time.Time) ProposalStatus {
	if p.Status == ProposalOpen && !now.Before(p.ExpiresAt) {
		return ProposalExpired
	}
	return p.Status
}

// CheckPropose reports whether proposer may offer a proposal of kind in the
// war at now, given the war's proposals: only a principal, a ceasefire only
// while the war is fought (declared or active), a peace while it is not over,
// one open proposal of a kind at a time.
func CheckPropose(w War, proposer string, kind ProposalKind, existing []Proposal, now time.Time) error {
	if !kind.Valid() {
		return ErrInvalid
	}
	if proposer != w.Attacker && proposer != w.Defender {
		return ErrNotParty
	}
	switch s := w.StatusAt(now); {
	case s == Ended:
		return ErrState
	case kind == ProposeCeasefire && s == Ceasefire:
		return ErrState
	}
	for _, p := range existing {
		if p.Kind == kind && p.StatusAt(now) == ProposalOpen {
			return ErrProposalOpen
		}
	}
	return nil
}

// Other is the principal that is not proposer.
func (w War) Other(country string) string {
	if country == w.Attacker {
		return w.Defender
	}
	return w.Attacker
}

// Answer is the partner accepting or declining a proposal at now, in the
// war as it stands: the proposal's new status and the war's, or why not. A
// ceasefire accepted after the war already rests, or a peace after it
// ended, is refused as ErrState.
func Answer(w War, p Proposal, by string, accept bool, now time.Time) (ProposalStatus, Status, error) {
	if by != p.Partner {
		return p.Status, w.Status, ErrNotParty
	}
	if p.StatusAt(now) != ProposalOpen {
		return p.Status, w.Status, fmt.Errorf("%w: proposal %s", ErrState, p.StatusAt(now))
	}
	if !accept {
		return ProposalDeclined, w.Status, nil
	}
	switch s := w.StatusAt(now); {
	case s == Ended, p.Kind == ProposeCeasefire && s == Ceasefire:
		return p.Status, w.Status, fmt.Errorf("%w: war %s", ErrState, s)
	case p.Kind == ProposeCeasefire:
		return ProposalAccepted, Ceasefire, nil
	}
	return ProposalAccepted, Ended, nil
}

// Withdraw is the proposer taking back its own open proposal.
func Withdraw(p Proposal, by string, now time.Time) (ProposalStatus, error) {
	if by != p.Proposer {
		return p.Status, ErrNotParty
	}
	if p.StatusAt(now) != ProposalOpen {
		return p.Status, ErrState
	}
	return ProposalWithdrawn, nil
}

// CheckResume reports whether a principal may end a ceasefire: the war
// resumes after notice, as a declaration would (Hague Regulations art. 36:
// the enemy is warned).
func CheckResume(w War, by string) error {
	if by != w.Attacker && by != w.Defender {
		return ErrNotParty
	}
	if w.Status != Ceasefire {
		return ErrState
	}
	return nil
}
