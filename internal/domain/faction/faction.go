// Package faction holds the rules of factions — gangs and organisations
// players found and run: the ranks and what each may do, who may act on
// whom, how leadership passes, and how an organised crime's take is split
// between the faction and its crew.
//
// Which rights each rank holds, the rank shares and the faction's cut are
// content (configs/content/factions.yml); the set of ranks and of rights is
// closed here, because each right is a check the code makes. Nothing here
// reads or writes anything.
package faction

import (
	"errors"
	"fmt"
)

// Rank is a member's rank.
type Rank string

const (
	Leader  Rank = "leader"
	Officer Rank = "officer"
	Member  Rank = "member"
)

// Ranks lists the ranks from the top.
func Ranks() []Rank { return []Rank{Leader, Officer, Member} }

// Valid reports whether r is a rank.
func (r Rank) Valid() bool { return r == Leader || r == Officer || r == Member }

// level orders the ranks: higher outranks lower.
func (r Rank) level() int {
	switch r {
	case Leader:
		return 3
	case Officer:
		return 2
	case Member:
		return 1
	}
	return 0
}

// Outranks reports whether r stands above o.
func (r Rank) Outranks(o Rank) bool { return r.level() > o.level() }

// Right is something a rank may do.
type Right string

const (
	// Invite sends an invitation; Decide answers an application.
	Invite Right = "invite"
	Decide Right = "decide"
	// Kick removes a member of lower rank.
	Kick Right = "kick"
	// Promote raises a member to officer or lowers an officer to member.
	Promote Right = "promote"
	// Deposit puts money into the faction's bank; Withdraw takes it out to
	// the one withdrawing.
	Deposit  Right = "deposit"
	Withdraw Right = "withdraw"
	// Link ties the faction to a Telegram group.
	Link Right = "link"
	// Plan plans an organised crime and calls it off; Launch sets it going
	// once the crew is there; Join takes part in one.
	Plan   Right = "plan"
	Launch Right = "launch"
	Join   Right = "join"
)

var rights = map[Right]bool{Invite: true, Decide: true, Kick: true, Promote: true, Deposit: true, Withdraw: true,
	Link: true, Plan: true, Launch: true, Join: true}

// Valid reports whether r is a known right.
func (r Right) Valid() bool { return rights[r] }

// AllRights lists every right.
func AllRights() []Right {
	return []Right{Invite, Decide, Kick, Promote, Deposit, Withdraw, Link, Plan, Launch, Join}
}

// Charter is what each rank may do. The leader may do everything, whatever
// the content says: a faction nobody can run would be stuck forever.
type Charter map[Rank][]Right

// ErrInvalidCharter means a charter names an unknown rank or right.
var ErrInvalidCharter = errors.New("faction: invalid charter")

// Validate checks every rank and right is known.
func (c Charter) Validate() error {
	for r, list := range c {
		if !r.Valid() {
			return fmt.Errorf("%w: rank %q", ErrInvalidCharter, r)
		}
		for _, x := range list {
			if !x.Valid() {
				return fmt.Errorf("%w: %s: right %q", ErrInvalidCharter, r, x)
			}
		}
	}
	return nil
}

// Can reports whether a member of rank may exercise right.
func (c Charter) Can(rank Rank, right Right) bool {
	if rank == Leader {
		return true
	}
	for _, x := range c[rank] {
		if x == right {
			return true
		}
	}
	return false
}

// Refusals of an act on another member.
var (
	ErrNotAllowed = errors.New("faction: the rank does not allow it")
	ErrOutranked  = errors.New("faction: the target does not rank below")
	ErrSelf       = errors.New("faction: not on oneself")
	ErrNoChange   = errors.New("faction: the rank is already that")
)

// CheckKick says whether a member of rank actor may remove one of rank
// target: they must hold the right and outrank them. Nobody kicks the
// leader, nor themself.
func (c Charter) CheckKick(actor, target Rank, self bool) error {
	switch {
	case self:
		return ErrSelf
	case !c.Can(actor, Kick):
		return ErrNotAllowed
	case !actor.Outranks(target):
		return ErrOutranked
	}
	return nil
}

// CheckRank says whether actor may move a member from rank from to rank to.
// Only officer and member are moved this way (leadership passes with
// Succeed); the actor must hold Promote and outrank both.
func (c Charter) CheckRank(actor, from, to Rank, self bool) error {
	switch {
	case self:
		return ErrSelf
	case !c.Can(actor, Promote):
		return ErrNotAllowed
	case to == Leader || from == Leader || !to.Valid():
		return ErrOutranked
	case from == to:
		return ErrNoChange
	case !actor.Outranks(from) || !actor.Outranks(to):
		return ErrOutranked
	}
	return nil
}

// Shares is the weight of each rank in a take's split.
type Shares map[Rank]int64

// ErrInvalidShares means a share is negative or every one is zero.
var ErrInvalidShares = errors.New("faction: invalid shares")

// Validate checks the shares.
func (s Shares) Validate() error {
	var sum int64
	for r, w := range s {
		if !r.Valid() || w < 0 || w > 100 {
			return fmt.Errorf("%w: %s = %d", ErrInvalidShares, r, w)
		}
		sum += w
	}
	if sum == 0 {
		return fmt.Errorf("%w: all zero", ErrInvalidShares)
	}
	return nil
}

// Split divides total between the faction and a crew: first the faction's
// cut (cutBPS, rounded down), then the rest in proportion to each member's
// rank share, rounded down, the units left over going one each to the crew
// in order (the planner first). A crew whose shares are all zero splits
// evenly. Nothing is created or lost: cut + Σ shares = total.
func Split(total int64, cutBPS int64, crew []Rank, s Shares) (cut int64, each []int64) {
	each = make([]int64, len(crew))
	if total <= 0 {
		return 0, each
	}
	cutBPS = min(max(cutBPS, 0), 10_000)
	cut = total * cutBPS / 10_000
	if len(crew) == 0 {
		return total, each
	}
	rest := total - cut
	weights := make([]int64, len(crew))
	var sum int64
	for i, r := range crew {
		weights[i] = s[r]
		sum += weights[i]
	}
	if sum == 0 {
		for i := range weights {
			weights[i] = 1
		}
		sum = int64(len(weights))
	}
	var given int64
	for i, w := range weights {
		each[i] = rest * w / sum
		given += each[i]
	}
	for i := 0; given < rest; i = (i + 1) % len(each) {
		each[i]++
		given++
	}
	return cut, each
}
