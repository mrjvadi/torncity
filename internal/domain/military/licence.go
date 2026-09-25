package military

import (
	"errors"
	"fmt"
	"time"
)

// The defence licence (docs/adr/0022-military-and-diplomacy.md, section
// 2.14). In the real world arms are made by people the state trusts: its
// own officers, or firms whose technology the armed forces rely on. So is it
// here. A company of the defence sector is founded only by one of two
// people:
//
//	a serving member of the armed forces of at least a content rank, or
//	the owner of a company holding a defence contractor licence.
//
// A civilian company earns a contractor licence by its standing in
// technology (content: how many technologies it owns, how deep in the tree)
// and the defence minister's approval. Every licence is public, and the
// minister may revoke one — with notice, never overnight: a revocation takes
// effect when its notice runs out, and until then the licence is in force.

// LicenceStatus is where a licence stands.
type LicenceStatus string

const (
	// LicencePending is an application waiting for the minister.
	LicencePending LicenceStatus = "pending"
	// LicenceActive is a licence in force.
	LicenceActive LicenceStatus = "active"
	// LicenceRevoking is a licence the minister has revoked, in force
	// until its notice runs out.
	LicenceRevoking LicenceStatus = "revoking"
	// LicenceRevoked is a licence no longer in force.
	LicenceRevoked LicenceStatus = "revoked"
	// LicenceRejected is an application the minister turned down.
	LicenceRejected LicenceStatus = "rejected"
)

// Sentinel errors of the licence.
var (
	// ErrLicenceState means the licence is not in the state the step
	// needs: an application decided twice, a revoked licence revoked again.
	ErrLicenceState = errors.New("military: the licence is not in a state for that")
	// ErrNoDefenceLicence means a player may not found a defence company:
	// neither of the ranks nor the owner of a licensed contractor.
	ErrNoDefenceLicence = errors.New("military: no defence licence")
	// ErrNotEligible means a company's standing in technology is below
	// what a contractor licence asks.
	ErrNotEligible = errors.New("military: not eligible for a contractor licence")
)

// Licence is a licence's state as the rules read it.
type Licence struct {
	Status LicenceStatus
	// EffectiveAt is when a revocation takes effect; zero otherwise.
	EffectiveAt time.Time
}

// InForce reports whether the licence lets its holder act as a defence
// company now: active, or revoked with its notice still running.
func (l Licence) InForce(now time.Time) bool {
	switch l.Status {
	case LicenceActive:
		return true
	case LicenceRevoking:
		return now.Before(l.EffectiveAt)
	}
	return false
}

// Settled is the status as of now: a revocation whose notice has run out is
// revoked. Nothing needs to run on a clock for it.
func (l Licence) Settled(now time.Time) LicenceStatus {
	if l.Status == LicenceRevoking && !now.Before(l.EffectiveAt) {
		return LicenceRevoked
	}
	return l.Status
}

// Open reports whether the licence still stands in the way of a new one for
// the same company: an application waiting, or a licence in force.
func (l Licence) Open(now time.Time) bool {
	return l.Status == LicencePending || l.InForce(now)
}

// Decide answers an application: approved, the licence is in force;
// otherwise it is rejected. Only a pending application is decided, once.
func Decide(l Licence, approve bool) (Licence, error) {
	if l.Status != LicencePending {
		return l, fmt.Errorf("%w: deciding a %s licence", ErrLicenceState, l.Status)
	}
	if approve {
		return Licence{Status: LicenceActive}, nil
	}
	return Licence{Status: LicenceRejected}, nil
}

// Revoke revokes a licence in force: it stays in force for notice more, then
// lapses. The notice is a governance commitment on the real clock, never
// negative.
func Revoke(l Licence, now time.Time, notice time.Duration) (Licence, error) {
	if l.Status != LicenceActive {
		return l, fmt.Errorf("%w: revoking a %s licence", ErrLicenceState, l.Status)
	}
	if notice < 0 {
		return l, fmt.Errorf("%w: notice %s", ErrInvalid, notice)
	}
	return Licence{Status: LicenceRevoking, EffectiveAt: now.Add(notice)}, nil
}

// Basis is what a defence company's licence rests on.
type Basis string

const (
	// BasisRank is its founder's rank in the armed forces.
	BasisRank Basis = "rank"
	// BasisContractor is its founder's licensed contractor company.
	BasisContractor Basis = "contractor"
	// BasisMinister is a contractor licence the minister approved.
	BasisMinister Basis = "minister"
	// BasisGrandfathered is a defence company that existed before
	// licences did.
	BasisGrandfathered Basis = "grandfathered"
)

// Founder is a player about to found a defence company, as the rules read
// them.
type Founder struct {
	// Serving is whether they hold a post in the armed forces now, and
	// RankTier their tier on its ladder (0 the most junior).
	Serving  bool
	RankTier int
	// Contractor is whether a company of theirs holds a contractor licence
	// in force.
	Contractor bool
}

// MayFound answers whether a founder may found a defence company, and on
// what basis; minRankTier is the lowest tier of the armed forces that may
// (content). A rank is the first basis: a serving officer needs no company.
func MayFound(f Founder, minRankTier int) (Basis, error) {
	if minRankTier < 0 {
		return "", fmt.Errorf("%w: rank tier %d", ErrInvalid, minRankTier)
	}
	if f.Serving && f.RankTier >= minRankTier {
		return BasisRank, nil
	}
	if f.Contractor {
		return BasisContractor, nil
	}
	return "", ErrNoDefenceLicence
}

// ContractorRule is what a civilian company's standing in technology must
// be to apply for a contractor licence (content): at least MinTechnologies
// technologies of its own, and one of them at least MinTier deep in the
// tree. A zero bound asks nothing.
type ContractorRule struct {
	MinTechnologies int
	MinTier         int
}

// Validate refuses a negative bound.
func (r ContractorRule) Validate() error {
	if r.MinTechnologies < 0 || r.MinTier < 0 {
		return fmt.Errorf("%w: contractor rule %+v", ErrInvalid, r)
	}
	return nil
}

// Eligible answers whether a company that owns owned technologies, the
// deepest of tier topTier, may apply.
func (r ContractorRule) Eligible(owned, topTier int) error {
	if owned < r.MinTechnologies || topTier < r.MinTier {
		return fmt.Errorf("%w: %d technologies to tier %d, the rule asks %d to tier %d",
			ErrNotEligible, owned, topTier, r.MinTechnologies, r.MinTier)
	}
	return nil
}
