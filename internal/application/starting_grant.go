package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// ErrInvalidStartingCash refuses a starting grant of zero or less. The
// configuration refuses such a value on load; this catches a caller that
// bypassed it.
var ErrInvalidStartingCash = errors.Sentinel(errors.CodeInternal,
	"application.ErrInvalidStartingCash", "starting cash must be above zero")

// GrantStartingCash gives a player their starting money, once.
//
// The money comes from system_source, through a reward_grants row with
// source 'starting_grant' and a ledger transaction with reason
// 'starting_grant' that references it — the only route ADR 0009 allows money
// to appear by. It is idempotent: the grant row is written first, and
// reward_grants_one_starting_grant_idx makes a second one for the same player
// conflict, so a repeat — or a concurrent twin in another transaction — writes
// nothing and reports granted=false.
//
// It must run on the ledger of the caller's unit of work (tx.Ledger()), so the
// grant row and the money it pays commit together or not at all.
func GrantStartingCash(ctx context.Context, ledger LedgerRepository, playerID string,
	amount money.Amount, grantedBy string, now time.Time,
) (granted bool, err error) {
	if amount.IsZero() || amount.IsNegative() {
		return false, ErrInvalidStartingCash.WithDetail("amount", amount.Minor())
	}

	// Opened before the grant row, so an unknown player fails here with
	// ErrPlayerNotFound rather than as a foreign-key error on the grant.
	account, err := ledger.AccountFor(ctx, AccountPlayerCash, playerID)
	if err != nil {
		return false, err
	}

	grant, fresh, err := ledger.RecordGrant(ctx, RewardGrant{
		PlayerID:  playerID,
		Source:    RewardStartingGrant,
		Amount:    amount,
		GrantedBy: grantedBy,
		CreatedAt: now,
	})
	if err != nil || !fresh {
		return false, err
	}

	debit, err := amount.Neg()
	if err != nil {
		return false, err
	}
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID:            grant.LedgerTransactionID,
		Reason:        ReasonStartingGrant,
		ReferenceType: "reward_grants",
		ReferenceID:   grant.ID,
		Entries: []LedgerEntry{
			{AccountID: SystemSourceAccountID, Amount: debit},
			{AccountID: account.ID, Amount: amount},
		},
		CreatedAt: now,
	}); err != nil {
		return false, err
	}
	return true, nil
}
