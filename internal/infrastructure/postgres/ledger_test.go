package postgres

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// TestPostRefusesBeforeTouchingTheDatabase builds a repository with no
// connection at all. If Post reached the database for an invalid
// transaction it would dereference the nil querier and panic; returning the
// sentinel proves the refusal happens first.
func TestPostRefusesBeforeTouchingTheDatabase(t *testing.T) {
	repo := &LedgerRepository{q: nil}

	tests := []struct {
		name string
		tx   application.LedgerTransaction
		want error
	}{
		{"unbalanced", application.LedgerTransaction{
			Reason: application.ReasonStartingGrant,
			Entries: []application.LedgerEntry{
				{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-100)},
				{AccountID: "00000000-0000-4000-8000-0000000000aa", Amount: money.FromMinor(101)},
			},
		}, application.ErrUnbalancedTransaction},
		{"unknown reason", application.LedgerTransaction{
			Reason: "free_money",
			Entries: []application.LedgerEntry{
				{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-100)},
				{AccountID: "00000000-0000-4000-8000-0000000000aa", Amount: money.FromMinor(100)},
			},
		}, application.ErrUnknownReason},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.Post(context.Background(), tc.tx)
			if !stderrors.Is(err, tc.want) {
				t.Fatalf("Post() = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestLedgerNamesMatchTheMigration keeps the constraint names this package
// maps, and the system account ids, in step with migration 0006.
func TestLedgerNamesMatchTheMigration(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "0006_ledger.up.sql"))
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}
	sql := string(raw)
	for _, name := range []string{
		accountsBalanceNonNegativeCheck,
		"reward_grants_one_starting_grant_idx",
		application.SystemSourceAccountID,
		application.SystemSinkAccountID,
		"'IRR'", // the legacy label; migration 0047 renames it to DefaultCurrency
	} {
		if !strings.Contains(sql, name) {
			t.Errorf("migration 0006 no longer contains %q", name)
		}
	}
	// The account key this package maps was widened by 0047 (currencies,
	// shard_id); the default currency the code opens accounts in is the
	// one that migration renamed IRR to.
	raw47, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "0047_currencies.up.sql"))
	if err != nil {
		t.Fatalf("reading migration 0047: %v", err)
	}
	for _, name := range []string{accountsKindOwnerCurrencyKey, "'" + application.DefaultCurrency + "'"} {
		if !strings.Contains(string(raw47), name) {
			t.Errorf("migration 0047 no longer contains %q", name)
		}
	}
	// reward_grants.player_id is declared inline, so its foreign key takes
	// PostgreSQL's default name <table>_<column>_fkey.
	if rewardGrantsPlayerFkey != "reward_grants_player_id_fkey" ||
		!strings.Contains(sql, "player_id             uuid        NOT NULL REFERENCES players (id)") {
		t.Error("reward_grants.player_id foreign key is no longer the inline one this package maps")
	}
}
