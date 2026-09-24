// Package payment holds the rule of how a player pays for something: with
// the cash they carry, or with their bank card.
//
// ONE MECHANISM FOR EVERY CHARGE. A course's tuition, a fare, a police fee,
// bail, a shop, a market order, an auction bid: each is a sum the player owes
// to somebody, and each is paid from exactly one of the player's two purses.
// The player chooses which. The service decides which it accepts — that is
// content (a street vendor may take cash only), never a branch in code — and
// this package decides which of the accepted methods can actually cover the
// sum, so a screen offers only what will work and a refusal is shown only
// when nothing will.
//
// The package is pure: it reads no balance and moves no money. The balances
// arrive as values and the ledger posting is the application layer's.
package payment

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Method is how a charge is paid. The set is closed: code branches on it, to
// pick the account a charge is taken from.
type Method string

const (
	// Cash is the money the player carries (the player_cash account). It can
	// be stolen, and it is what a face-to-face hand-over moves.
	Cash Method = "cash"
	// Card is the player's bank balance (the player_bank account), paid by
	// card. A withdrawal is not needed first.
	Card Method = "card"
)

// methods is every method, in the order a screen offers them.
var methods = []Method{Cash, Card}

// Methods returns every method in display order. The slice is a copy.
func Methods() []Method { return append([]Method(nil), methods...) }

// ErrUnknownMethod means a method outside the closed set.
var ErrUnknownMethod = errors.New("payment: unknown method")

// Parse reads a method as it is written in content and in a button address.
func Parse(s string) (Method, error) {
	m := Method(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range methods {
		if known == m {
			return m, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownMethod, s)
}

// Valid reports whether m is one of the methods.
func (m Method) Valid() bool {
	for _, known := range methods {
		if known == m {
			return true
		}
	}
	return false
}

// Service names a kind of charge — tuition, a fare, bail — so content can
// say which methods it accepts. The set of services is the content package's
// (content.ServiceTuition…); this package only carries the name.
type Service string

// Accepts is the set of methods a service takes. The zero value — nothing
// stated — accepts every method: a service that says nothing takes both.
type Accepts []Method

// Normalize returns the accepted methods in display order without
// duplicates, every method when none is stated.
func (a Accepts) Normalize() Accepts {
	if len(a) == 0 {
		return Methods()
	}
	out := make(Accepts, 0, len(methods))
	for _, m := range methods {
		for _, x := range a {
			if x == m {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// Has reports whether the service takes m.
func (a Accepts) Has(m Method) bool {
	for _, x := range a.Normalize() {
		if x == m {
			return true
		}
	}
	return false
}

// ParseAccepts reads an authored list of methods. An empty list is "every
// method"; an unknown or repeated method is an error, so a typo in content
// fails the load instead of silently narrowing what a service takes.
func ParseAccepts(raw []string) (Accepts, error) {
	var out Accepts
	seen := map[Method]bool{}
	var errs []error
	for _, s := range raw {
		m, err := Parse(s)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if seen[m] {
			errs = append(errs, fmt.Errorf("payment: method %q listed twice", s))
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Wallet is what a player can pay with: their cash and their bank balance.
type Wallet struct {
	Cash money.Amount
	Bank money.Amount
}

// Balance is what the purse behind m holds.
func (w Wallet) Balance(m Method) money.Amount {
	if m == Card {
		return w.Bank
	}
	return w.Cash
}

// Covers reports whether m alone can pay amount.
func (w Wallet) Covers(m Method, amount money.Amount) bool {
	return !amount.IsNegative() && w.Balance(m).Minor() >= amount.Minor()
}

// Plan is what a player can do about one charge: which methods the service
// takes, and which of those would cover it.
type Plan struct {
	Amount   money.Amount
	Accepted Accepts
	// Usable is every accepted method that covers the amount, in display
	// order. Empty means the player cannot pay: that is the one case a
	// refusal is shown.
	Usable []Method
}

// PlanFor works out a charge's plan. A zero amount is usable by every
// accepted method: nothing is taken.
func PlanFor(amount money.Amount, accepted Accepts, w Wallet) Plan {
	p := Plan{Amount: amount, Accepted: accepted.Normalize()}
	for _, m := range p.Accepted {
		if w.Covers(m, amount) {
			p.Usable = append(p.Usable, m)
		}
	}
	return p
}

// CanPay reports whether m is accepted and covers the amount.
func (p Plan) CanPay(m Method) bool {
	for _, x := range p.Usable {
		if x == m {
			return true
		}
	}
	return false
}

// Payable reports whether any accepted method covers the amount.
func (p Plan) Payable() bool { return len(p.Usable) > 0 }

// Shortfall is how a charge was refused: the method asked for is not taken,
// or it does not cover the amount.
type Shortfall int

const (
	// ShortNone means the method can pay.
	ShortNone Shortfall = iota
	// ShortNotAccepted means the service does not take the method.
	ShortNotAccepted
	// ShortFunds means the purse behind the method holds less than the
	// amount.
	ShortFunds
)

// Check says whether m can pay the plan's amount, and why not.
func (p Plan) Check(m Method) Shortfall {
	switch {
	case !p.Accepted.Has(m):
		return ShortNotAccepted
	case !p.CanPay(m):
		return ShortFunds
	}
	return ShortNone
}
