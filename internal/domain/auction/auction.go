// Package auction holds the rules of an English auction for one unique item:
// a reserve, bids that must beat the standing one by a step, and a close at
// a fixed time on the game clock that sells to the highest bid at or above
// the reserve, or returns the item.
//
// Every bid is money already set aside (escrow) and every outbid is handed
// back in full: this package decides who is ahead and by how much; moving
// the money and the item is the application's. It reads no clock.
package auction

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// BPS is one hundred percent in basis points.
const BPS = 10_000

// Failures.
var (
	// ErrInvalidAuction means the auction's terms are unusable.
	ErrInvalidAuction = errors.New("auction: invalid terms")
	// ErrClosed means the auction has ended.
	ErrClosed = errors.New("auction: closed")
	// ErrBidTooLow means a bid below the least that beats the standing one.
	ErrBidTooLow = errors.New("auction: bid too low")
	// ErrOwnAuction means the seller bid on their own item.
	ErrOwnAuction = errors.New("auction: the seller cannot bid")
	// ErrAlreadyWinning means the standing bid is already the bidder's.
	ErrAlreadyWinning = errors.New("auction: already the highest bidder")
)

// Terms are an auction's rules, fixed when it opens.
type Terms struct {
	Reserve money.Amount
	// StepBPS is how much a bid must beat the standing one by, at least
	// MinStep.
	StepBPS int
	MinStep money.Amount
	// OpensAt and EndsAt bound the bidding; EndsAt is the close, the game
	// clock's real instant.
	OpensAt, EndsAt time.Time
}

// Limits bound what a seller may ask for.
type Limits struct {
	MinDuration, MaxDuration time.Duration
	MaxReserve               money.Amount
}

// Validate checks the terms against the limits.
func (t Terms) Validate(l Limits) error {
	d := t.EndsAt.Sub(t.OpensAt)
	switch {
	case t.Reserve.Minor() < 1 || t.Reserve.Minor() > l.MaxReserve.Minor():
		return fmt.Errorf("%w: reserve %s is outside 1..%s", ErrInvalidAuction, t.Reserve, l.MaxReserve)
	case d < l.MinDuration || d > l.MaxDuration:
		return fmt.Errorf("%w: duration %s is outside %s..%s", ErrInvalidAuction, d, l.MinDuration, l.MaxDuration)
	case t.StepBPS < 0 || t.StepBPS > BPS || t.MinStep.Minor() < 1:
		return fmt.Errorf("%w: step %d bps, at least %s", ErrInvalidAuction, t.StepBPS, t.MinStep)
	}
	return nil
}

// Bid is the standing bid, or none.
type Bid struct {
	Bidder string
	Amount money.Amount
}

// None reports whether nobody has bid.
func (b Bid) None() bool { return b.Bidder == "" }

// MinNext is the least a new bid must be: the reserve while nobody has bid,
// otherwise the standing bid plus its step — StepBPS of it rounded up, at
// least MinStep.
func (t Terms) MinNext(high Bid) money.Amount {
	if high.None() {
		return t.Reserve
	}
	h := high.Amount.Minor()
	step := int64(0)
	if t.StepBPS > 0 {
		if h > math.MaxInt64/int64(t.StepBPS) {
			step = h / BPS * int64(t.StepBPS)
		} else {
			step = (h*int64(t.StepBPS) + BPS - 1) / BPS
		}
	}
	step = max(step, t.MinStep.Minor())
	if h > math.MaxInt64-step {
		return money.FromMinor(math.MaxInt64)
	}
	return money.FromMinor(h + step)
}

// PlaceBid checks a new bid against the standing one at now. On success the
// new bid stands and the previous bidder, if any, is outbid and owed their
// escrow back in full.
func PlaceBid(t Terms, seller string, high Bid, bidder string, amount money.Amount, now time.Time) (Bid, error) {
	switch {
	case !now.Before(t.EndsAt):
		return high, ErrClosed
	case bidder == seller:
		return high, ErrOwnAuction
	case !high.None() && high.Bidder == bidder:
		return high, ErrAlreadyWinning
	}
	if min := t.MinNext(high); amount.Minor() < min.Minor() {
		return high, fmt.Errorf("%w: %s asked, at least %s needed", ErrBidTooLow, amount, min)
	}
	return Bid{Bidder: bidder, Amount: amount}, nil
}

// Result is how an auction closed.
type Result string

const (
	// Sold: the standing bid met the reserve; the item goes to the bidder
	// and the money to the seller.
	Sold Result = "sold"
	// Unsold: nobody bid; the item goes back to the seller.
	Unsold Result = "unsold"
)

// Close decides an auction at its end. Every accepted bid already met the
// reserve, so a standing bid sells.
func Close(t Terms, high Bid, now time.Time) (Result, error) {
	if now.Before(t.EndsAt) {
		return "", fmt.Errorf("%w: closes at %s", ErrInvalidAuction, t.EndsAt.Format(time.RFC3339))
	}
	if high.None() {
		return Unsold, nil
	}
	return Sold, nil
}

// Fee is the house's fee on a sale, paid by the seller out of the price:
// feeBPS of it, rounded up, never more than the price.
func Fee(price money.Amount, feeBPS int) money.Amount {
	p := price.Minor()
	if feeBPS <= 0 || p <= 0 {
		return money.Amount{}
	}
	bps := int64(min(feeBPS, BPS))
	q, r := p/BPS, p%BPS
	return money.FromMinor(min(q*bps+(r*bps+BPS-1)/BPS, p))
}
