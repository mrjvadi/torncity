// Package licence holds the pure rules of the licence market (docs/adr/0077 7): the requirement a charter creates and its grace, the sealed
// first-price auction of a right, the transfer fee and cooldown, the share cap, the term, the notice before a revocation and the refund of
// the unused part. It reads no database and no clock. The money and the rows are the application's.
package licence

import (
	"sort"
	"time"
)

// BPS is one whole, in basis points.
const BPS = 10_000

// Rules are the tuning of the market (config licence.*).
type Rules struct {
	Grace            time.Duration
	AuctionMin       time.Duration
	ReserveMin       int64
	TransferFeeBPS   int64
	TransferCooldown time.Duration
	MaxShareBPS      int64
	// ShareCapFrom is how many live licences of a kind a settlement must have before the share cap applies: a lone shop cannot be capped.
	ShareCapFrom int64
	Notice       time.Duration
	Nonuse       time.Duration
	TermMax      time.Duration
	ListingFee   int64
}

// Requirement is the rule a charter sets: from EffectiveAt nobody does the thing without the licence. EffectiveAt is the day the rule was
// made plus the grace, so the people already at work have time to get theirs.
type Requirement struct {
	Kind         string
	Mode         string // fixed or auction
	Fee, Reserve int64
	Term         time.Duration
	Transferable bool
	EffectiveAt  time.Time
}

// Modes of granting.
const (
	Fixed   = "fixed"
	Auction = "auction"
)

// EffectiveFrom is the instant a rule made at madeAt takes effect, after the grace.
func (r Rules) EffectiveFrom(madeAt time.Time) time.Time { return madeAt.Add(r.Grace) }

// InForce reports whether the requirement binds at now.
func (q Requirement) InForce(now time.Time) bool { return !now.Before(q.EffectiveAt) }

// Price is what a first grant costs a current operator: the posted fee, or the reserve for a kind that is auctioned (the incumbent's
// first grant at the reserve price, ADR 0077 section 14).
func (q Requirement) Price() int64 {
	if q.Mode == Auction {
		return q.Reserve
	}
	return q.Fee
}

// ValidTerm clamps a requested term to what the market allows. A zero or negative term is refused (false).
func (r Rules) ValidTerm(d time.Duration) (time.Duration, bool) {
	if d <= 0 {
		return 0, false
	}
	return min(d, r.TermMax), true
}

// ReserveOK reports whether a reserve is allowed: positive and at least the least reserve (a zero reserve yields nothing).
func (r Rules) ReserveOK(reserve int64) bool { return reserve > 0 && reserve >= r.ReserveMin }

// WindowOK reports whether an auction's window is long enough.
func (r Rules) WindowOK(opens, closes time.Time) bool { return closes.Sub(opens) >= r.AuctionMin }

// Bid is a sealed bid.
type Bid struct {
	ID     string
	Bidder string
	Amount int64
	At     time.Time
}

// Winner picks the winner of a sealed first-price auction: the highest bid at or above the reserve; a tie goes to the earliest bid and then
// to the lowest id, so every replica picks the same one. ok is false when nothing reaches the reserve.
func Winner(reserve int64, bids []Bid) (Bid, bool) {
	var live []Bid
	for _, b := range bids {
		if b.Amount >= reserve && b.Amount > 0 {
			live = append(live, b)
		}
	}
	if len(live) == 0 {
		return Bid{}, false
	}
	sort.Slice(live, func(i, j int) bool {
		a, b := live[i], live[j]
		if a.Amount != b.Amount {
			return a.Amount > b.Amount
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.ID < b.ID
	})
	return live[0], true
}

// TransferFee is the issuer's share of a sale and what the seller keeps. The fee is rounded down; the two always add up to the price.
func (r Rules) TransferFee(price int64) (fee, seller int64) {
	if price <= 0 {
		return 0, 0
	}
	fee = price * max(r.TransferFeeBPS, 0) / BPS
	return fee, price - fee
}

// TransferOpen reports whether a licence may change hands now: not within the cooldown of its last transfer (or its grant).
func (r Rules) TransferOpen(lastMoveAt, now time.Time) bool {
	return lastMoveAt.IsZero() || now.Sub(lastMoveAt) >= r.TransferCooldown
}

// OverShare reports whether giving a holder one more licence of a kind would take them past the cap. held is how many live licences of the
// kind the holder has already, total how many live licences the kind has in the settlement. Below ShareCapFrom licences the cap does not apply.
func (r Rules) OverShare(held, total int64) bool {
	if r.MaxShareBPS <= 0 || total+1 < r.ShareCapFrom {
		return false
	}
	return (held+1)*BPS/(total+1) > r.MaxShareBPS
}

// Term is when a licence granted at start ends.
func Term(start time.Time, d time.Duration) time.Time { return start.Add(d) }

// Expired reports whether a licence's term is over.
func Expired(termEnd, now time.Time) bool { return !now.Before(termEnd) }

// NoticeEnds is when a revocation for cause takes effect.
func (r Rules) NoticeEnds(now time.Time) time.Time { return now.Add(r.Notice) }

// Refund is the part of what was paid that the unused time of the term is worth: paid times the time left over the whole term, rounded
// down. A revocation without cause refunds it; one for cause does not.
func Refund(paid int64, termStart, termEnd, now time.Time) int64 {
	total := termEnd.Sub(termStart)
	if paid <= 0 || total <= 0 {
		return 0
	}
	left := termEnd.Sub(now)
	if left <= 0 {
		return 0
	}
	if left >= total {
		return paid
	}
	return int64(float64(paid) * float64(left) / float64(total))
}

// Unused reports a non-use breach: the licence has not been used for the non-use time, counted from the later of its grant and its last use.
func (r Rules) Unused(termStart time.Time, lastUsed *time.Time, now time.Time) bool {
	from := termStart
	if lastUsed != nil && lastUsed.After(from) {
		from = *lastUsed
	}
	return r.Nonuse > 0 && now.Sub(from) >= r.Nonuse
}

// Causes a revocation for cause may name.
const (
	CauseUnpaid    = "unpaid"
	CauseNonUse    = "non_use"
	CauseBreach    = "breach"
	CauseWithdrawn = "no_cause"
)

// ValidCause reports a known cause.
func ValidCause(c string) bool {
	switch c {
	case CauseUnpaid, CauseNonUse, CauseBreach:
		return true
	}
	return false
}
