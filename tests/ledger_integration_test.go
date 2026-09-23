//go:build integration

// Integration tests for the money core (migration 0006, docs/adr/0009).
//
// Each one is about a guarantee that lives in PostgreSQL: row locks that
// serialise concurrent posts to one account, a trigger that refuses to edit
// history, a partial unique index that lets exactly one of several racing
// starting grants through, a deferred trigger that refuses an unbalanced
// transaction at commit. A fake would pass all of them and prove nothing.
//
// Cleaning up an append-only ledger. The rows these tests write cannot be
// deleted through the normal path — that is the point of the table. The
// cleanup therefore disables the append-only triggers, removes exactly the
// rows the test wrote, takes their amounts back off every cached balance they
// moved (system_source included), and re-enables the triggers, all in ONE
// transaction: no other session ever sees the table unguarded, and the
// ledger is left as if the test never ran, which is what lets
// economy_invariants_test.go pass afterwards.
package tests

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// requireLedger skips unless migration 0006 has been applied.
func requireLedger(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT to_regclass('public.ledger_entries') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("checking for the ledger: %v", err)
	}
	if !exists {
		t.Skip("ledger_entries does not exist; apply migration 0006_ledger first")
	}
}

// ledgerPlayer creates a player and registers a cleanup that removes every
// ledger row involving them BEFORE the player row itself is deleted
// (t.Cleanup runs last-registered-first).
func ledgerPlayer(t *testing.T, pool *postgres.Pool) *application.Player {
	t.Helper()
	p := insertPlayer(t, pool)
	t.Cleanup(func() { purgeLedgerFor(t, pool, p.ID) })
	return p
}

// purgeLedgerFor removes everything the ledger holds for one player, and
// reverses its effect on every other account. See the file comment.
func purgeLedgerFor(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	for _, stmt := range []string{
		`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`,
		`ALTER TABLE reward_grants DISABLE TRIGGER reward_grants_append_only`,

		// Every transaction that touched one of the player's accounts.
		`CREATE TEMP TABLE purge_tx ON COMMIT DROP AS
		   SELECT DISTINCT e.transaction_id FROM ledger_entries e
		     JOIN accounts a ON a.id = e.account_id
		    WHERE a.owner_id = $1::uuid`,

		// Take those transactions' legs back off every balance they moved.
		`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta
		           FROM ledger_entries
		          WHERE transaction_id IN (SELECT transaction_id FROM purge_tx)
		          GROUP BY account_id) d
		  WHERE a.id = d.account_id`,

		`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_tx)`,
		`DELETE FROM reward_grants WHERE player_id = $1::uuid`,
		`DELETE FROM accounts WHERE owner_id = $1::uuid`,

		`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`,
		`ALTER TABLE reward_grants ENABLE TRIGGER reward_grants_append_only`,
	} {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{playerID}
		}
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(stmt), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// derivedBalance is the ledger's own answer for an account.
func derivedBalance(t *testing.T, pool *postgres.Pool, accountID string) int64 {
	t.Helper()
	var sum int64
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE account_id = $1`,
		accountID).Scan(&sum); err != nil {
		t.Fatalf("summing entries: %v", err)
	}
	return sum
}

func cachedBalance(t *testing.T, pool *postgres.Pool, accountID string) int64 {
	t.Helper()
	b, err := postgres.NewLedgerRepository(pool).Balance(testCtx(t), accountID)
	if err != nil {
		t.Fatalf("reading balance: %v", err)
	}
	return b.Minor()
}

func cashAccount(t *testing.T, pool *postgres.Pool, playerID string) application.Account {
	t.Helper()
	acct, err := postgres.NewLedgerRepository(pool).AccountFor(testCtx(t), application.AccountPlayerCash, playerID)
	if err != nil {
		t.Fatalf("opening the cash account: %v", err)
	}
	return acct
}

func transfer(from, to string, minor int64, reason application.Reason) application.LedgerTransaction {
	return application.LedgerTransaction{
		Reason: reason,
		Entries: []application.LedgerEntry{
			{AccountID: from, Amount: money.FromMinor(-minor)},
			{AccountID: to, Amount: money.FromMinor(minor)},
		},
		CreatedAt: time.Now(),
	}
}

// TestLedgerConcurrentPostsKeepCacheEqualToEntries hammers one account from
// many goroutines, each in its own unit of work, mixing credits and debits.
// The cached balance must end equal to the sum of the entries AND to the
// exact arithmetic total: a lost update would show up as a difference.
func TestLedgerConcurrentPostsKeepCacheEqualToEntries(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)

	p := ledgerPlayer(t, pool)
	acct := cashAccount(t, pool, p.ID)

	// Enough opening money that no debit below can hit zero, whatever the
	// interleaving.
	const opening = 10_000
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		_, err := tx.Ledger().Post(ctx, transfer(application.SystemSourceAccountID, acct.ID, opening, application.ReasonAdminGrant))
		return err
	}); err != nil {
		t.Fatalf("funding: %v", err)
	}

	const n = 40
	var want int64 = opening
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			want += int64(i + 1)
		} else {
			want -= int64(i)
		}
	}

	_, errs := runConcurrently(t, n, func(i int) (bool, error) {
		lt := transfer(application.SystemSourceAccountID, acct.ID, int64(i+1), application.ReasonAdminGrant)
		if i%2 == 1 {
			lt = transfer(acct.ID, application.SystemSinkAccountID, int64(i), application.ReasonTax)
		}
		err := uow.Do(context.Background(), func(ctx context.Context, tx application.Tx) error {
			_, err := tx.Ledger().Post(ctx, lt)
			return err
		})
		return err == nil, err
	})
	for i, err := range errs {
		if err != nil {
			t.Errorf("post %d: %v", i, err)
		}
	}

	cached, derived := cachedBalance(t, pool, acct.ID), derivedBalance(t, pool, acct.ID)
	if cached != derived {
		t.Errorf("cached balance %d, entries sum to %d: the cache drifted under concurrency", cached, derived)
	}
	if cached != want {
		t.Errorf("balance %d, want exactly %d: a concurrent post was lost or doubled", cached, want)
	}
}

// TestLedgerEntriesAreAppendOnly: history cannot be edited. Each attempt runs
// in a transaction that is rolled back whatever happens, so even a broken
// guard could not damage the shared ledger.
func TestLedgerEntriesAreAppendOnly(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)

	// A starting grant writes both append-only tables: a reward_grants row
	// and the ledger legs that pay it.
	p := ledgerPlayer(t, pool)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		_, err := application.GrantStartingCash(ctx, tx.Ledger(), p.ID, money.FromMinor(50), "integration-test", time.Now())
		return err
	}); err != nil {
		t.Fatalf("granting: %v", err)
	}
	acct := cashAccount(t, pool, p.ID)
	var txID string
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT transaction_id::text FROM ledger_entries WHERE account_id = $1`, acct.ID).Scan(&txID); err != nil {
		t.Fatalf("finding the posted transaction: %v", err)
	}

	for _, stmt := range []string{
		`UPDATE ledger_entries SET amount = amount * 2 WHERE transaction_id = '` + txID + `'`,
		`DELETE FROM ledger_entries WHERE transaction_id = '` + txID + `'`,
		`TRUNCATE ledger_entries`,
		`UPDATE reward_grants SET amount = 1 WHERE player_id = '` + p.ID + `'`,
		`DELETE FROM reward_grants WHERE player_id = '` + p.ID + `'`,
		`TRUNCATE reward_grants`,
	} {
		t.Run(firstLine(stmt)[:20], func(t *testing.T) {
			ctx := testCtx(t)
			tx, err := pool.Raw().Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

			_, err = tx.Exec(ctx, stmt)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("%q succeeded (err = %v); ledger history is editable", stmt, err)
			}
			if pgErr.Code != "23001" || !strings.Contains(pgErr.Message, "append-only") {
				t.Fatalf("%q failed with %s %q, want 23001 naming the append-only rule", stmt, pgErr.Code, pgErr.Message)
			}
		})
	}

	// And the posted rows are still there.
	if got := derivedBalance(t, pool, acct.ID); got != 50 {
		t.Errorf("entries now sum to %d, want 50", got)
	}
}

// TestLedgerRefusesUnbalancedInsertAtCommit is the database's own line of
// defence, for a writer that bypasses the repository.
func TestLedgerRefusesUnbalancedInsertAtCommit(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	ctx := testCtx(t)

	p := ledgerPlayer(t, pool)
	acct := cashAccount(t, pool, p.ID)

	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO ledger_entries (id, transaction_id, account_id, amount, currency, reason, created_at)
		 VALUES ($1, $2, $3, 100, 'IRR', 'admin_grant', now())`,
		newUUID(t), newUUID(t), acct.ID); err != nil {
		t.Fatalf("the single leg was refused before commit: %v", err)
	}
	err = tx.Commit(ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "ledger_entries_transaction_balances" {
		t.Fatalf("commit of an unbalanced transaction = %v, want a violation of ledger_entries_transaction_balances", err)
	}
}

// TestLedgerRefusesOverdraft: a player cannot spend money they do not have,
// and the refusal leaves the caller's unit of work usable.
func TestLedgerRefusesOverdraft(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)

	p := ledgerPlayer(t, pool)
	acct := cashAccount(t, pool, p.ID)

	err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		if _, err := tx.Ledger().Post(ctx, transfer(application.SystemSourceAccountID, acct.ID, 10, application.ReasonAdminGrant)); err != nil {
			return err
		}
		_, err := tx.Ledger().Post(ctx, transfer(acct.ID, application.SystemSinkAccountID, 11, application.ReasonTravelFare))
		if !errors.Is(err, application.ErrInsufficientFunds) {
			t.Errorf("overdraft = %v, want ErrInsufficientFunds", err)
		}
		// The savepoint rolled back only the refused post.
		_, err = tx.Ledger().Post(ctx, transfer(acct.ID, application.SystemSinkAccountID, 4, application.ReasonTravelFare))
		return err
	})
	if err != nil {
		t.Fatalf("unit of work: %v", err)
	}
	if got := cachedBalance(t, pool, acct.ID); got != 6 {
		t.Errorf("balance = %d, want 6", got)
	}
}

// TestStartingGrantAppliedOnceUnderConcurrency races several grants for one
// player. Exactly one pays; the others find the grant and write nothing.
func TestStartingGrantAppliedOnceUnderConcurrency(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)

	p := ledgerPlayer(t, pool)
	const amount = 5000

	oks, errs := runConcurrently(t, 8, func(int) (bool, error) {
		var granted bool
		err := uow.Do(context.Background(), func(ctx context.Context, tx application.Tx) error {
			var err error
			granted, err = application.GrantStartingCash(ctx, tx.Ledger(), p.ID,
				money.FromMinor(amount), "integration-test", time.Now())
			return err
		})
		return granted, err
	})

	paid := 0
	for i := range oks {
		if errs[i] != nil {
			t.Errorf("grant %d: %v", i, errs[i])
		}
		if oks[i] {
			paid++
		}
	}
	if paid != 1 {
		t.Fatalf("%d grants reported paying, want exactly 1", paid)
	}

	if n := countRows(t, pool,
		`SELECT count(*) FROM reward_grants WHERE player_id = $1 AND source = 'starting_grant'`, p.ID); n != 1 {
		t.Errorf("%d starting grants recorded, want 1", n)
	}
	acct := cashAccount(t, pool, p.ID)
	if n := countRows(t, pool, `SELECT count(*) FROM ledger_entries WHERE account_id = $1`, acct.ID); n != 1 {
		t.Errorf("%d ledger legs on the player's account, want 1", n)
	}
	if got := cachedBalance(t, pool, acct.ID); got != amount {
		t.Errorf("balance = %d, want %d", got, amount)
	}

	// And a later, sequential request is also a no-op.
	var again bool
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		var err error
		again, err = application.GrantStartingCash(ctx, tx.Ledger(), p.ID, money.FromMinor(amount), "integration-test", time.Now())
		return err
	}); err != nil || again {
		t.Errorf("a repeated grant = %v, %v; want false, nil", again, err)
	}
}
