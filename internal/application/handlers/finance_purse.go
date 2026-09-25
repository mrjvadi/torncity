package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// purse is where finance takes a payment from, in order: a player's bank
// and then their cash, or a company's free money. Nothing is ever taken
// beyond what it holds, so no balance goes below zero.
type purse struct {
	accounts []application.Account
}

// playerPurse is a player's bank, then their cash.
func playerPurse(ctx context.Context, tx application.Tx, playerID string) (purse, error) {
	w, err := application.OpenWallet(ctx, tx.Ledger(), playerID)
	if err != nil {
		return purse{}, err
	}
	return purse{accounts: []application.Account{w.Bank, w.Cash}}, nil
}

// companyPurse is a company's free money: its treasury less the wages its
// running shifts reserved. Read under the company's lock.
func companyPurse(ctx context.Context, tx application.Tx, c application.Company) (purse, error) {
	books, acct, err := companyBooks(ctx, tx, c)
	if err != nil {
		return purse{}, err
	}
	acct.Balance = books.Available()
	return purse{accounts: []application.Account{acct}}, nil
}

// total is what the purse holds.
func (p purse) total() int64 {
	var t int64
	for _, a := range p.accounts {
		t += max(a.Balance.Minor(), 0)
	}
	return t
}

// pay takes amount into to as one ledger transaction under reason, drawing
// the accounts in order, and returns its id ("" for nothing). id, when set,
// is the transaction's id.
func (p *purse) pay(ctx context.Context, tx application.Tx, amount int64, reason application.Reason, refType, refID,
	to, id string, now time.Time,
) (string, error) {
	if amount <= 0 {
		return "", nil
	}
	if amount > p.total() {
		return "", errors.Internal(application.ErrInsufficientFunds)
	}
	entries := []application.LedgerEntry{{AccountID: to, Amount: money.FromMinor(amount)}}
	left := amount
	for i := range p.accounts {
		take := min(left, max(p.accounts[i].Balance.Minor(), 0))
		if take <= 0 {
			continue
		}
		entries = append(entries, application.LedgerEntry{AccountID: p.accounts[i].ID, Amount: money.FromMinor(-take)})
		p.accounts[i].Balance = money.FromMinor(p.accounts[i].Balance.Minor() - take)
		left -= take
		if left == 0 {
			break
		}
	}
	return tx.Ledger().Post(ctx, application.LedgerTransaction{ID: id, Reason: reason, ReferenceType: refType,
		ReferenceID: refID, Entries: entries, CreatedAt: now})
}

// move posts one transfer between two accounts under reason.
func move(ctx context.Context, tx application.Tx, from, to string, amount int64, reason application.Reason,
	refType, refID, id string, now time.Time,
) (string, error) {
	if amount <= 0 {
		return "", nil
	}
	return tx.Ledger().Post(ctx, application.LedgerTransaction{ID: id, Reason: reason, ReferenceType: refType,
		ReferenceID: refID, CreatedAt: now, Entries: []application.LedgerEntry{
			{AccountID: from, Amount: money.FromMinor(-amount)}, {AccountID: to, Amount: money.FromMinor(amount)}}})
}

// moneyOf is minor units as an amount.
func moneyOf(minor int64) money.Amount { return money.FromMinor(minor) }
