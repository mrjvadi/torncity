package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// A village's treasury has no income until its abstract sales exist (ADR
// 0028 section 8.3), yet research and construction are paid from it. Two
// faucets fill it meanwhile (migration 0052_village_treasury): the founding
// grant, once per settlement, and residents' donations.

// The ledger reasons of the village treasury's two faucets.
const (
	// ReasonSettlementGrant credits a settlement's treasury from
	// system_source, exactly once per settlement (settlement_grants, primary
	// key on the settlement): a faucet, bounded by settlement.founding_grant
	// times the number of villages.
	ReasonSettlementGrant Reason = "settlement_grant"
	// ReasonSettlementDonation moves a resident's own cash into their
	// village's treasury (a transfer: nothing is created or destroyed).
	ReasonSettlementDonation Reason = "settlement_donation"
	// ReasonSettlementTopup credits a settlement's treasury from
	// system_source on an operator's audited command (admin settlement
	// grant): a faucet, recorded in settlement_topups.
	ReasonSettlementTopup Reason = "settlement_topup"
)

// The two ways a grant row comes to exist.
const (
	SettlementGrantFounding = "founding"
	SettlementGrantBackfill = "backfill"
)

// The references ledger legs of this file point at.
const (
	SettlementGrantReference    = "settlement_grants"
	SettlementDonationReference = "settlement_donations"
	SettlementTopupReference    = "settlement_topups"
)

// ErrSettlementDonationRange means a donation is outside settlement.donation_min
// .. settlement.donation_max; the bounds travel as details "min" and "max".
var ErrSettlementDonationRange = errors.Sentinel(errors.CodeInvalidInput,
	"application.ErrSettlementDonationRange", "that amount cannot be donated")

// SettlementDonation is one resident's gift to their village.
type SettlementDonation struct {
	ID                  string
	SettlementID        string
	PlayerID            string
	Amount              int64
	LedgerTransactionID string
	CreatedAt           time.Time
}

// SettlementTreasuryRepository records the faucets of a village's treasury,
// reached through Tx.SettlementTreasury so each commits with its ledger
// transaction.
type SettlementTreasuryRepository interface {
	// The village economy's purchases and shifts (village_economy.go).
	SettlementEconomyRepository
	// RecordGrant inserts the settlement's one grant row and reports whether
	// it was inserted; false means the settlement already had its grant (the
	// primary key), whichever replica or run made it.
	RecordGrant(ctx context.Context, settlementID string, amount int64, ledgerTransactionID, source, grantedBy string, at time.Time) (bool, error)
	// RecordDonation inserts one donation row.
	RecordDonation(ctx context.Context, d SettlementDonation) error
	// RecordTopup inserts one operator top-up row.
	RecordTopup(ctx context.Context, id, settlementID string, amount int64, ledgerTransactionID, grantedBy, reason string, at time.Time) error
	// FoundedWithoutGrant lists, oldest first, every founded settlement that
	// has no grant row yet.
	FoundedWithoutGrant(ctx context.Context) ([]string, error)
}

// GrantSettlementTreasury credits a settlement's treasury with amount from
// system_source, once. The grant row is written first under the id the
// ledger transaction is then posted with, so a second call for the same
// settlement (a retry, another replica, a re-run of the backfill) inserts
// nothing and posts nothing. Both writes belong to the caller's transaction.
func GrantSettlementTreasury(ctx context.Context, tx Tx, settlementID string, amount int64, txID, source, grantedBy string, at time.Time) (bool, error) {
	if amount <= 0 {
		return false, ErrInvalidLedgerTransaction.WithDetail("problem", "a settlement grant must be above zero")
	}
	fresh, err := tx.SettlementTreasury().RecordGrant(ctx, settlementID, amount, txID, source, grantedBy, at)
	if err != nil || !fresh {
		return false, err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, AccountCityTreasury, settlementID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Ledger().Post(ctx, LedgerTransaction{
		ID: txID, Reason: ReasonSettlementGrant, CreatedAt: at,
		ReferenceType: SettlementGrantReference, ReferenceID: settlementID,
		Entries: []LedgerEntry{
			{AccountID: SystemSourceAccountID, Amount: money.FromMinor(-amount)},
			{AccountID: treasury.ID, Amount: money.FromMinor(amount)},
		},
	}); err != nil {
		return false, err
	}
	return true, nil
}

// TopupSettlementTreasury credits a settlement's treasury with amount from
// system_source on an operator's command; the row is written first under
// the id of the ledger transaction that follows it, both in the caller's
// transaction.
func TopupSettlementTreasury(ctx context.Context, tx Tx, settlementID string, amount int64, id, txID, grantedBy, reason string, at time.Time) error {
	if amount <= 0 {
		return ErrInvalidLedgerTransaction.WithDetail("problem", "a settlement top-up must be above zero")
	}
	if err := tx.SettlementTreasury().RecordTopup(ctx, id, settlementID, amount, txID, grantedBy, reason, at); err != nil {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, AccountCityTreasury, settlementID)
	if err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, LedgerTransaction{
		ID: txID, Reason: ReasonSettlementTopup, CreatedAt: at,
		ReferenceType: SettlementTopupReference, ReferenceID: id,
		Entries: []LedgerEntry{
			{AccountID: SystemSourceAccountID, Amount: money.FromMinor(-amount)},
			{AccountID: treasury.ID, Amount: money.FromMinor(amount)},
		},
	})
	return err
}
