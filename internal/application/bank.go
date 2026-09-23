package application

import (
	"context"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the bank's ports and refusals: a player's cash on hand, their
// bank balance, and the payments between players.
//
// The money itself lives in the ledger (ports_ledger.go): cash is the
// player_cash account and the bank balance the player_bank account, and every
// movement between them is a ledger transaction with its own reason. What is
// here is what the ledger does not know: where the players are, which decides
// whether a bank is reachable and whether cash can change hands.

// The policy levers the bank reads (configs/content/governance.yml). A fee is
// a city's decision, set by its mayor inside the operator's bounds, and is
// read only through PolicyReader.Get — never from content or configuration.
const (
	// LeverBankWithdrawalFee is the fee, in basis points, a city's bank
	// charges on a withdrawal, on top of the amount withdrawn.
	LeverBankWithdrawalFee = "city.bank_withdrawal_fee"
	// LeverCardTransferFee is the fee, in basis points, a city's bank charges
	// on a card payment, on top of the amount sent. The payer's city charges
	// it: the city they are in, or the one their journey left.
	LeverCardTransferFee = "city.card_transfer_fee"
)

// BankReferenceLedgerTransaction is the ledger reference_type a bank fee is
// posted under: the fee points at the ledger transaction it was charged on.
const BankReferenceLedgerTransaction = "ledger_transaction"

// Presence is where one player is, as far as money is concerned.
type Presence struct {
	PlayerID string
	// CityID is the player's city: where they stand, or, while travelling,
	// the city their journey left. Empty when they have never been anywhere.
	CityID string
	// Travelling is true while a journey is in progress.
	Travelling bool
}

// BankRepository is the bank's own port. Reach it through Tx.Bank, so the
// locks it takes last exactly as long as the unit of work that moves money.
type BankRepository interface {
	// LockPresence locks the named players against moving — the player rows
	// and their condition rows, in id order — and returns where each one
	// is, in the order asked.
	//
	// The locks are what make "these two are together" still true at
	// commit. An arrival moves a player by updating their player row and a
	// departure spends energy on their condition row before it inserts the
	// journey, so either one waits for this transaction to end — and a
	// departure already under way makes this call wait for it, and then see
	// the journey. The caller must make sure the condition rows exist first
	// (StatsRepository.EnsureDefaults), or a player with none is not locked.
	//
	// A player that does not exist is ErrPlayerNotFound.
	LockPresence(ctx context.Context, playerIDs ...string) ([]Presence, error)
}

// Bank refusals. Each is something a player reaches by pressing a button, so
// each has its own sentence (internal/telegram/screens/bank.go).
var (
	// ErrBankNotInCity means the player is not standing in a city: the bank
	// is a city service, closed to someone on the road.
	ErrBankNotInCity = errors.Sentinel(errors.CodeConflict,
		"application.ErrBankNotInCity", "the bank is only reachable in a city")

	// ErrNotTogether means cash was offered to a player who is not in the
	// same city, or one of the two is travelling.
	ErrNotTogether = errors.Sentinel(errors.CodeConflict,
		"application.ErrNotTogether", "cash changes hands only face to face")

	// ErrSelfPayment is a payment to oneself.
	ErrSelfPayment = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrSelfPayment", "a player cannot pay themselves")

	// ErrPayeeNotFound means the player to be paid could not be found.
	ErrPayeeNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrPayeeNotFound", "no such player to pay")

	// ErrInvalidMoneyAmount is an amount that is not a positive whole
	// number.
	ErrInvalidMoneyAmount = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrInvalidMoneyAmount", "amount must be a positive whole number")

	// ErrAmountBelowMinimum is an amount under the configured minimum. The
	// detail "min" is the minimum, in minor units.
	ErrAmountBelowMinimum = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrAmountBelowMinimum", "amount is below the minimum")

	// ErrAmountAboveMaximum is an amount over the configured maximum. The
	// detail "max" is the maximum, in minor units.
	ErrAmountAboveMaximum = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrAmountAboveMaximum", "amount is above the maximum")

	// ErrNotEnoughCash means the player carries less cash than the amount.
	// The detail "available" is what they carry.
	ErrNotEnoughCash = errors.Sentinel(errors.CodeInsufficientFunds,
		"application.ErrNotEnoughCash", "not enough cash")

	// ErrNotEnoughInBank means the bank balance does not cover the amount
	// and its fee. The details "needed" and "available" say by how much.
	ErrNotEnoughInBank = errors.Sentinel(errors.CodeInsufficientFunds,
		"application.ErrNotEnoughInBank", "not enough money in the bank")

	// ErrBankPolicyUnavailable means the fee of the player's city could not
	// be read: the city is not placed in the jurisdiction tree, or the
	// active content has no such lever. An operator's problem, never the
	// player's; nothing is charged at a guessed rate.
	ErrBankPolicyUnavailable = errors.Sentinel(errors.CodeInternal,
		"application.ErrBankPolicyUnavailable", "the bank fee could not be read")
)
