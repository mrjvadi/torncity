// Package bank holds the rules of a player's money: cash on hand and a bank
// balance, the fees a city's bank charges, and when money may change hands.
//
// It is pure. It never reads a fee rate or a limit — the rate is a policy
// lever read by the application through the policy resolver, the limits are
// configuration — it only receives them as arguments and applies them.
//
// # Cash and the bank
//
// A player's money lives in two places, as in the real world. Cash is what
// they carry: it can be handed to someone standing next to them, and later it
// is what a mugging takes. The bank balance is safe from that, and can be
// sent by card to anyone, anywhere — but it is reached through a bank, and a
// bank is a service of a city: nobody deposits or withdraws on the road.
package bank

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// MaxFeeBps bounds a fee rate: 10000 basis points is 100%. It bounds the
// input, like market.MaxFeeBps; the rate itself is a policy lever and always
// arrives as an argument.
const MaxFeeBps = 10000

// Domain errors. They are plain sentinels; the application classifies them.
var (
	// ErrInvalidAmount is an amount of zero or less, or one that is not a
	// whole number of minor units.
	ErrInvalidAmount = errors.New("bank: amount must be a positive whole number")
	// ErrBelowMinimum is an amount under the configured minimum.
	ErrBelowMinimum = errors.New("bank: amount is below the minimum")
	// ErrAboveMaximum is an amount over the configured maximum.
	ErrAboveMaximum = errors.New("bank: amount is above the maximum")
	// ErrInvalidFeeRate is a rate outside 0..MaxFeeBps.
	ErrInvalidFeeRate = errors.New("bank: fee rate out of range")
	// ErrInvalidLimits is a minimum above the maximum, or a limit at or
	// below zero.
	ErrInvalidLimits = errors.New("bank: invalid limits")
	// ErrNotInCity means the player is not standing in a city: travelling,
	// or nowhere yet. The bank is a city service.
	ErrNotInCity = errors.New("bank: the bank is only reachable in a city")
	// ErrNotTogether means two players are not in the same place, so cash
	// cannot pass from hand to hand.
	ErrNotTogether = errors.New("bank: cash changes hands only face to face")
	// ErrSelfPayment is a payment to oneself.
	ErrSelfPayment = errors.New("bank: a player cannot pay themselves")
	// ErrUnknownMethod is a payment method that does not exist.
	ErrUnknownMethod = errors.New("bank: unknown payment method")
	// ErrOverflow is an amount so large the fee arithmetic cannot hold it.
	ErrOverflow = errors.New("bank: amount too large")
)

// Limits bound the amount of one bank operation or payment.
type Limits struct {
	Min money.Amount
	Max money.Amount
}

// NewLimits validates a pair of limits: both above zero, min not above max.
func NewLimits(minAmount, maxAmount int64) (Limits, error) {
	if minAmount <= 0 || maxAmount <= 0 || minAmount > maxAmount {
		return Limits{}, fmt.Errorf("%w: min %d, max %d", ErrInvalidLimits, minAmount, maxAmount)
	}
	return Limits{Min: money.FromMinor(minAmount), Max: money.FromMinor(maxAmount)}, nil
}

// Check refuses an amount that is not positive or lies outside the limits.
func (l Limits) Check(amount money.Amount) error {
	switch {
	case amount.IsZero() || amount.IsNegative():
		return ErrInvalidAmount
	case amount.Minor() < l.Min.Minor():
		return ErrBelowMinimum
	case amount.Minor() > l.Max.Minor():
		return ErrAboveMaximum
	}
	return nil
}

// Clamp returns amount pulled down to the maximum. It is for offering a
// button ("all of it"), never for changing what a player asked for.
func (l Limits) Clamp(amount money.Amount) money.Amount {
	if amount.Minor() > l.Max.Minor() {
		return l.Max
	}
	return amount
}

// Fee returns the fee on amount at feeBps basis points.
//
// ROUNDING: UP, to the next whole minor unit, exactly as the market engine
// rounds its fee (market.Fee): any non-zero amount at a non-zero rate pays
// at least one minor unit, so splitting a withdrawal into many tiny ones
// cannot round the fee away. The arithmetic is integer only and cannot
// overflow: amount = q×10000 + r, fee = q×bps + ceil(r×bps/10000).
func Fee(amount money.Amount, feeBps int64) (money.Amount, error) {
	if feeBps < 0 || feeBps > MaxFeeBps {
		return money.Amount{}, fmt.Errorf("%w: %d bps", ErrInvalidFeeRate, feeBps)
	}
	n := amount.Minor()
	if n < 0 {
		return money.Amount{}, ErrInvalidAmount
	}
	q, r := n/MaxFeeBps, n%MaxFeeBps
	return money.FromMinor(q*feeBps + (r*feeBps+MaxFeeBps-1)/MaxFeeBps), nil
}

// Quote is what one fee-bearing movement costs: the amount that arrives, the
// fee on top of it, and the total that leaves the payer's account.
type Quote struct {
	Amount money.Amount
	Fee    money.Amount
	Total  money.Amount
}

// QuoteFor prices amount at feeBps. The fee is charged ON TOP: whoever
// receives the amount receives all of it, and the payer's account is debited
// the total. A withdrawal of 1,000 hands over 1,000 in cash.
func QuoteFor(amount money.Amount, feeBps int64) (Quote, error) {
	if amount.IsZero() || amount.IsNegative() {
		return Quote{}, ErrInvalidAmount
	}
	fee, err := Fee(amount, feeBps)
	if err != nil {
		return Quote{}, err
	}
	total, err := amount.Add(fee)
	if err != nil {
		return Quote{}, ErrOverflow
	}
	return Quote{Amount: amount, Fee: fee, Total: total}, nil
}

// MaxAffordable returns the largest amount whose total, fee included, fits in
// balance at feeBps: the "all of it" of a withdrawal or a card payment. Zero
// when not even one minor unit fits.
//
// Total(x) = x + Fee(x) grows with x, so the answer is found by bisection
// over [0, balance]; no closed form is needed and none can disagree with Fee.
func MaxAffordable(balance money.Amount, feeBps int64) (money.Amount, error) {
	if feeBps < 0 || feeBps > MaxFeeBps {
		return money.Amount{}, fmt.Errorf("%w: %d bps", ErrInvalidFeeRate, feeBps)
	}
	lo, hi := int64(0), balance.Minor()
	if hi <= 0 {
		return money.Amount{}, nil
	}
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		q, err := QuoteFor(money.FromMinor(mid), feeBps)
		if err == nil && q.Total.Minor() <= balance.Minor() {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return money.FromMinor(lo), nil
}

// Share returns bps basis points of amount, rounded DOWN, for the quick
// amount buttons (25%, 50%, 100%). Rounding down keeps a share affordable.
func Share(amount money.Amount, bps int64) money.Amount {
	n := amount.Minor()
	if n <= 0 || bps <= 0 {
		return money.Amount{}
	}
	if bps >= MaxFeeBps {
		return amount
	}
	q, r := n/MaxFeeBps, n%MaxFeeBps
	return money.FromMinor(q*bps + r*bps/MaxFeeBps)
}

// ParseAmount reads an amount a player typed.
//
// It accepts ASCII, Persian (۰-۹) and Arabic-Indic (٠-٩) digits, and ignores
// the grouping marks a player naturally types (",", the Arabic "٬" and
// "_"). Anything else — a sign, a decimal point, a letter — is refused: money
// has no fractions in this game.
func ParseAmount(raw string) (money.Amount, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return money.Amount{}, ErrInvalidAmount
	}
	var n int64
	digits := 0
	for _, r := range s {
		var d int64
		switch {
		case r >= '0' && r <= '9':
			d = int64(r - '0')
		case r >= '۰' && r <= '۹':
			d = int64(r - '۰')
		case r >= '٠' && r <= '٩':
			d = int64(r - '٠')
		case r == ',' || r == '٬' || r == '_':
			continue
		default:
			return money.Amount{}, ErrInvalidAmount
		}
		if n > (1<<63-1-d)/10 {
			return money.Amount{}, ErrOverflow
		}
		n = n*10 + d
		digits++
	}
	if digits == 0 || n <= 0 {
		return money.Amount{}, ErrInvalidAmount
	}
	return money.FromMinor(n), nil
}

// Method is how money passes from one player to another.
type Method string

const (
	// MethodCash hands cash over, face to face: from the payer's cash to the
	// payee's cash. Both must be in the same city and neither travelling.
	MethodCash Method = "cash"
	// MethodCard sends money bank to bank, from anywhere.
	MethodCard Method = "card"
)

// Valid reports whether m is a method that exists.
func (m Method) Valid() bool { return m == MethodCash || m == MethodCard }

// Presence is where a player is, as far as money is concerned.
type Presence struct {
	// CityID is the city the player is in, or last left; empty when they
	// have never been anywhere.
	CityID string
	// Travelling is true while a journey is in progress.
	Travelling bool
}

// InCity reports whether the player is standing in a city.
func (p Presence) InCity() bool { return p.CityID != "" && !p.Travelling }

// CheckAtBank refuses a bank operation away from a city.
func CheckAtBank(p Presence) error {
	if !p.InCity() {
		return ErrNotInCity
	}
	return nil
}

// Together reports whether two players are face to face: both standing in
// the same city.
func Together(a, b Presence) bool {
	return a.InCity() && b.InCity() && a.CityID == b.CityID
}

// CheckPayment refuses a payment the rules do not allow, before any money is
// looked at: a payment to oneself, an unknown method, and cash between two
// players who are not together. Card payments work from anywhere.
func CheckPayment(payerID, payeeID string, method Method, payer, payee Presence) error {
	if payerID == payeeID {
		return ErrSelfPayment
	}
	switch method {
	case MethodCash:
		if !Together(payer, payee) {
			return ErrNotTogether
		}
	case MethodCard:
	default:
		return ErrUnknownMethod
	}
	return nil
}
