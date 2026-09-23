//go:build integration

package tests

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The ledger invariants from docs/adr/0009-economic-control.md.
//
// These run against a live database and are cheap enough to run on every
// deploy. They exist now, while the ledger is still empty, on purpose: the
// first financial bug is far cheaper to find the day it lands than months
// later with thousands of transactions stacked on top of it.
//
// A failure here is a BUG, never a tuning problem. Money appearing from
// nowhere is not a balance issue to be adjusted; it is a defect.

func economyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(envDSN)
	if dsn == "" {
		t.Skipf("%s not set; skipping economy invariants", envDSN)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// tableExists lets these tests pass before the ledger migration lands, so
// they can be committed now and start guarding the moment the table appears.
func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema='public' AND table_name=$1)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("checking for %s: %v", name, err)
	}
	return exists
}

// TestLedgerSumsToZero is the fundamental accounting identity: every unit of
// money is in exactly one account. Because the ledger is double entry, that
// is not a claim, it is a checkable constraint.
//
// If this fails, some code path wrote one side of a transaction without the
// other, and money has been created or destroyed.
func TestLedgerSumsToZero(t *testing.T) {
	pool := economyPool(t)
	if !tableExists(t, pool, "ledger_entries") {
		t.Skip("ledger_entries does not exist yet; this guard activates with it")
	}

	var total int64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(amount), 0) FROM ledger_entries`).Scan(&total); err != nil {
		t.Fatalf("summing ledger: %v", err)
	}
	if total != 0 {
		t.Errorf("ledger sums to %d, want 0 — money was created or destroyed", total)
	}
}

// TestEveryTransactionBalances checks the same identity per transaction.
//
// The whole-table sum can be zero while individual transactions are broken:
// one entry short in one direction and one long in another cancel out. This
// catches what the global check misses.
func TestEveryTransactionBalances(t *testing.T) {
	pool := economyPool(t)
	if !tableExists(t, pool, "ledger_entries") {
		t.Skip("ledger_entries does not exist yet; this guard activates with it")
	}

	rows, err := pool.Query(context.Background(),
		`SELECT transaction_id, SUM(amount) AS total
		   FROM ledger_entries
		  GROUP BY transaction_id
		 HAVING SUM(amount) <> 0
		  LIMIT 20`)
	if err != nil {
		t.Fatalf("grouping ledger: %v", err)
	}
	defer rows.Close()

	unbalanced := 0
	for rows.Next() {
		var id string
		var total int64
		if err := rows.Scan(&id, &total); err != nil {
			t.Fatalf("scan: %v", err)
		}
		unbalanced++
		t.Errorf("transaction %s sums to %d, want 0", id, total)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	if unbalanced == 0 {
		t.Log("every transaction balances")
	}
}

// TestAccountBalancesMatchLedger checks the cached balance against the
// entries it is derived from.
//
// accounts.balance exists so a screen does not have to sum a player's whole
// history. A cache that disagrees with its source is worse than no cache: it
// is wrong confidently, and every downstream number inherits the error.
func TestAccountBalancesMatchLedger(t *testing.T) {
	pool := economyPool(t)
	if !tableExists(t, pool, "accounts") || !tableExists(t, pool, "ledger_entries") {
		t.Skip("accounts or ledger_entries does not exist yet; this guard activates with them")
	}

	rows, err := pool.Query(context.Background(),
		`SELECT a.id, a.balance, COALESCE(SUM(e.amount), 0) AS derived
		   FROM accounts a
		   LEFT JOIN ledger_entries e ON e.account_id = a.id
		  GROUP BY a.id, a.balance
		 HAVING a.balance <> COALESCE(SUM(e.amount), 0)
		  LIMIT 20`)
	if err != nil {
		t.Fatalf("comparing balances: %v", err)
	}
	defer rows.Close()

	drifted := 0
	for rows.Next() {
		var id string
		var cached, derived int64
		if err := rows.Scan(&id, &cached, &derived); err != nil {
			t.Fatalf("scan: %v", err)
		}
		drifted++
		t.Errorf("account %s: cached balance %d, ledger says %d", id, cached, derived)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	if drifted == 0 {
		t.Log("every account balance matches its entries")
	}
}

// TestNoItemInstanceWithoutOrigin is the integration invariant from
// 24_PHONE_END_TO_END.md, as a query.
//
// "It must not be possible to add a phone to a player's inventory with a
// single INSERT." Every instance traces to a production order or to a formal
// reward grant. Nothing else may bring an item into existence.
func TestNoItemInstanceWithoutOrigin(t *testing.T) {
	pool := economyPool(t)
	if !tableExists(t, pool, "item_instances") {
		t.Skip("item_instances does not exist yet; this guard activates with it")
	}

	var orphans int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM item_instances i
		  WHERE i.production_order_id IS NULL
		    AND NOT EXISTS (
		          SELECT 1 FROM reward_grants r
		           WHERE r.item_id = i.item_id)`).Scan(&orphans)
	if err != nil {
		t.Fatalf("checking item origins: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d item instances exist with no production order and no reward grant", orphans)
	}
}
