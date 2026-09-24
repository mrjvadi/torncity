package application

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// This file is the one way a player pays a service: a course, a fare, a
// police fee, bail, a shop, a market order, an auction bid. The player
// chooses the method — the cash they carry, or their bank card — and the
// charge is ONE ledger transaction under the charge's own reason, debiting
// the account behind that method. A card payment is therefore visible in the
// ledger as what it is: the same reason, taken from player_bank rather than
// player_cash. No per-method reason exists or is needed.
//
// Which methods a service accepts is content (payments.yml, and a course's or
// a mode's own narrowing), read through content.Snapshot; what a player can
// use is internal/domain/payment.PlanFor. Nothing here decides either.

// Wallet is a player's two purses, opened in the caller's transaction.
type Wallet struct {
	PlayerID string
	Cash     Account
	Bank     Account
}

// OpenWallet reads the player's cash and bank accounts, opening them on first
// use, in the caller's transaction.
func OpenWallet(ctx context.Context, ledger LedgerRepository, playerID string) (Wallet, error) {
	cash, err := ledger.AccountFor(ctx, AccountPlayerCash, playerID)
	if err != nil {
		return Wallet{}, err
	}
	bank, err := ledger.AccountFor(ctx, AccountPlayerBank, playerID)
	if err != nil {
		return Wallet{}, err
	}
	return Wallet{PlayerID: playerID, Cash: cash, Bank: bank}, nil
}

// Balances is the wallet as the payment rule reads it.
func (w Wallet) Balances() payment.Wallet {
	return payment.Wallet{Cash: w.Cash.Balance, Bank: w.Bank.Balance}
}

// Account is the account behind a method.
func (w Wallet) Account(m payment.Method) Account {
	if m == payment.Card {
		return w.Bank
	}
	return w.Cash
}

// Plan works out what the player can do about a charge of amount to a
// service accepting accepted.
func (w Wallet) Plan(amount money.Amount, accepted payment.Accepts) payment.Plan {
	return payment.PlanFor(amount, accepted, w.Balances())
}

// Charge is one payment by a player to a service.
type Charge struct {
	// Method is how the player chose to pay.
	Method payment.Method
	// Accepted is what the service takes; a method outside it is refused
	// with ErrPaymentNotAccepted before anything is written.
	Accepted payment.Accepts
	// Reason is the charge's own reason (service_fee, transit_fare, bail…),
	// the same whichever method pays.
	Reason        Reason
	ReferenceType string
	ReferenceID   string
	// To are the credits: who is paid, and how much each. Their sum is the
	// amount taken from the player.
	To        []LedgerEntry
	CreatedAt time.Time
}

// Amount is the sum of the credits.
func (c Charge) Amount() (money.Amount, error) {
	amounts := make([]money.Amount, 0, len(c.To))
	for _, e := range c.To {
		if !e.Amount.IsNegative() && !e.Amount.IsZero() {
			amounts = append(amounts, e.Amount)
		}
	}
	return money.Sum(amounts...)
}

// Pay takes a charge from the player's chosen purse, as one ledger
// transaction, and returns its id — or "" when the charge is for nothing.
//
// Refusals, all before anything is written:
//   - ErrPaymentNotAccepted: the service does not take the method;
//   - ErrPaymentDeclined: the purse behind it does not cover the amount
//     (details "method", "amount", "cash", "bank"). A balance that moved
//     between the read and the post is refused by the ledger itself, and
//     reported the same way.
func (w *Wallet) Pay(ctx context.Context, ledger LedgerRepository, c Charge) (string, error) {
	if !c.Method.Valid() {
		return "", ErrPaymentNotAccepted.WithDetail("method", string(c.Method))
	}
	if !c.Accepted.Has(c.Method) {
		return "", ErrPaymentNotAccepted.WithDetail("method", string(c.Method))
	}
	amount, err := c.Amount()
	if err != nil {
		return "", errors.Internal(err)
	}
	if amount.IsZero() {
		return "", nil
	}
	declined := func() error {
		return ErrPaymentDeclined.
			WithDetail("method", string(c.Method)).
			WithDetail("amount", amount.Minor()).
			WithDetail("cash", w.Cash.Balance.Minor()).
			WithDetail("bank", w.Bank.Balance.Minor())
	}
	if !w.Balances().Covers(c.Method, amount) {
		return "", declined()
	}
	debit, err := amount.Neg()
	if err != nil {
		return "", errors.Internal(err)
	}
	entries := make([]LedgerEntry, 0, len(c.To)+1)
	entries = append(entries, LedgerEntry{AccountID: w.Account(c.Method).ID, Amount: debit})
	for _, e := range c.To {
		if e.Amount.IsZero() {
			continue
		}
		if e.Amount.IsNegative() {
			return "", errors.Internal(fmt.Errorf("application: a charge credits %s a negative %s", e.AccountID, e.Amount))
		}
		entries = append(entries, e)
	}
	id, err := ledger.Post(ctx, LedgerTransaction{
		Reason:        c.Reason,
		ReferenceType: c.ReferenceType,
		ReferenceID:   c.ReferenceID,
		Entries:       entries,
		CreatedAt:     c.CreatedAt,
	})
	if stderrors.Is(err, ErrInsufficientFunds) {
		return "", declined()
	}
	if err != nil {
		return "", err
	}
	// Keep the wallet honest for a second charge in the same transaction.
	if c.Method == payment.Card {
		w.Bank.Balance, _ = w.Bank.Balance.Sub(amount)
	} else {
		w.Cash.Balance, _ = w.Cash.Balance.Sub(amount)
	}
	return id, nil
}

// Payment refusals.
var (
	// ErrPaymentNotAccepted means the service does not take the method the
	// player chose, or the method is not one at all. A forged button, or
	// content that changed under an open screen.
	ErrPaymentNotAccepted = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrPaymentNotAccepted", "that payment method is not accepted here")

	// ErrPaymentDeclined means the purse behind the chosen method does not
	// cover the charge. Details: "method", "amount", "cash", "bank".
	ErrPaymentDeclined = errors.Sentinel(errors.CodeInsufficientFunds,
		"application.ErrPaymentDeclined", "the chosen payment method does not cover the charge")
)
