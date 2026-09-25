// Command admin applies database migrations and, later, operator tasks.
//
// The migration runner is deliberately tiny and dependency-free: it reads the
// .sql files in migrations/, applies the ones a schema_migrations table has
// not recorded, and records them. Everything runs inside one transaction per
// file, so a failing migration leaves no half-applied schema.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

const migrationsDir = "migrations"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "migrate":
		err = migrate(ctx, direction("up"))
	case "migrate-down":
		err = migrate(ctx, direction("down"))
	case "status":
		err = status(ctx)
	case "content":
		err = contentCommand(ctx, os.Args[2:])
	case "economy":
		err = economyCommand(ctx, os.Args[2:])
	case "office":
		err = officeCommand(ctx, os.Args[2:])
	case "policy":
		err = policyCommand(ctx, os.Args[2:])
	case "city":
		err = cityCommand(ctx, os.Args[2:])
	case "election":
		err = electionCommand(ctx, os.Args[2:])
	case "company":
		err = companyCommand(ctx, os.Args[2:])
	case "watch":
		err = watchCommand(ctx, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "admin: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: admin <command>

  migrate       apply every pending up migration
  migrate-down  roll back the most recently applied migration
  status        list applied and pending migrations
  content       validate or load the game content (see: admin content)
  economy       verify the ledger or grant starting cash (see: admin economy)
  office        appoint, vacate or list office holders (see: admin office)
  policy        show the policy in force in a place (see: admin policy)
  city          link a city to its Telegram group (see: admin city)
  election      open an election of an elected office (see: admin election)
  company       list player companies or show one (see: admin company)
  watch         the watch's flags and held payments (see: admin watch)

DATABASE_URL must be set, except for `+"`admin content validate`"+`, which
reads files only.
`)
}

type direction string

func connect(ctx context.Context) (*pgx.Conn, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		// The DSN carries a password; never echo it.
		return nil, fmt.Errorf("connect to database: %w", redactDSN(err))
	}
	return conn, nil
}

// redactDSN strips anything that looks like a credential out of a driver
// error. pgx errors can embed the connection string.
func redactDSN(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "://"); i >= 0 {
		if j := strings.Index(msg[i:], "@"); j >= 0 {
			msg = msg[:i+3] + "[REDACTED]" + msg[i+j:]
		}
	}
	return errors.New(msg)
}

const createSchemaMigrations = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     text        PRIMARY KEY,
    applied_at  timestamptz NOT NULL
)`

func migrate(ctx context.Context, dir direction) error {
	conn, err := connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))

	if _, err := conn.Exec(ctx, createSchemaMigrations); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	files, err := migrationFiles(string(dir))
	if err != nil {
		return err
	}

	if dir == "down" {
		return rollbackLast(ctx, conn, files, applied)
	}

	count := 0
	for _, f := range files {
		version := versionOf(f)
		if applied[version] {
			continue
		}
		if err := applyOne(ctx, conn, f, version, true); err != nil {
			return err
		}
		fmt.Printf("applied %s\n", version)
		count++
	}
	if count == 0 {
		fmt.Println("no pending migrations")
	}
	return nil
}

func rollbackLast(ctx context.Context, conn *pgx.Conn, downFiles []string, applied map[string]bool) error {
	// Roll back exactly one: the highest applied version. Rolling back
	// everything on one command is too easy to do by accident.
	var target string
	for _, f := range downFiles {
		if v := versionOf(f); applied[v] && v > target {
			target = v
		}
	}
	if target == "" {
		fmt.Println("nothing to roll back")
		return nil
	}
	for _, f := range downFiles {
		if versionOf(f) != target {
			continue
		}
		if err := applyOne(ctx, conn, f, target, false); err != nil {
			return err
		}
		fmt.Printf("rolled back %s\n", target)
	}
	return nil
}

// applyOne runs one migration file inside a single transaction together with
// the schema_migrations bookkeeping, so the record and the schema change can
// never disagree.
func applyOne(ctx context.Context, conn *pgx.Conn, path, version string, up bool) error {
	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply %s: %w", version, err)
	}

	if up {
		_, err = tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())
			 ON CONFLICT (version) DO NOTHING`, version)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version)
	}
	if err != nil {
		return fmt.Errorf("record %s: %w", version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", version, err)
	}
	return nil
}

func status(ctx context.Context) error {
	conn, err := connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))

	if _, err := conn.Exec(ctx, createSchemaMigrations); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}
	files, err := migrationFiles("up")
	if err != nil {
		return err
	}
	for _, f := range files {
		v := versionOf(f)
		state := "pending"
		if applied[v] {
			state = "applied"
		}
		fmt.Printf("%-10s %s\n", state, v)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func migrationFiles(dir string) ([]string, error) {
	pattern := filepath.Join(migrationsDir, "*."+dir+".sql")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no %s migrations found in %s/ (run from the repository root)", dir, migrationsDir)
	}
	sort.Strings(files)
	return files, nil
}

// versionOf turns migrations/0001_init.up.sql into 0001_init.
func versionOf(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".sql")
	base = strings.TrimSuffix(base, ".up")
	base = strings.TrimSuffix(base, ".down")
	return base
}
