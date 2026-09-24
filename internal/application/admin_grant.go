package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// ErrInvalidAdminGrant refuses an operator grant of zero or less.
var ErrInvalidAdminGrant = errors.Sentinel(errors.CodeInvalidInput,
	"application.ErrInvalidAdminGrant", "an operator grant must be above zero")

// GrantAdminCash pays an operator grant into a player's cash.
//
// It is the same route as the starting grant (ADR 0009): a reward_grants row
// with source 'admin', naming who granted it, and a ledger transaction with
// reason 'admin_grant' from system_source that references that row. Unlike
// the starting grant it is not once-per-player: each call is one grant, so
// the operator command runs it exactly once per invocation.
//
// It must run on the ledger of the caller's unit of work, so the grant row
// and the money commit together or not at all.
func GrantAdminCash(ctx context.Context, ledger LedgerRepository, playerID string,
	amount money.Amount, grantedBy string, now time.Time,
) (RewardGrant, error) {
	if amount.IsZero() || amount.IsNegative() {
		return RewardGrant{}, ErrInvalidAdminGrant.WithDetail("amount", amount.Minor())
	}
	account, err := ledger.AccountFor(ctx, AccountPlayerCash, playerID)
	if err != nil {
		return RewardGrant{}, err
	}
	grant, _, err := ledger.RecordGrant(ctx, RewardGrant{
		PlayerID:  playerID,
		Source:    RewardAdmin,
		Amount:    amount,
		GrantedBy: grantedBy,
		CreatedAt: now,
	})
	if err != nil {
		return RewardGrant{}, err
	}
	debit, err := amount.Neg()
	if err != nil {
		return RewardGrant{}, err
	}
	if _, err := ledger.Post(ctx, LedgerTransaction{
		ID:            grant.LedgerTransactionID,
		Reason:        ReasonAdminGrant,
		ReferenceType: "reward_grants",
		ReferenceID:   grant.ID,
		Entries: []LedgerEntry{
			{AccountID: SystemSourceAccountID, Amount: debit},
			{AccountID: account.ID, Amount: amount},
		},
		CreatedAt: now,
	}); err != nil {
		return RewardGrant{}, err
	}
	return grant, nil
}
