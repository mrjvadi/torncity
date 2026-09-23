package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// defaultContentDir is where authored content is read from.
//
// This one path comes from the environment rather than from the config file,
// for the same reason DATABASE_URL does: something has to say where the
// content lives before any content has been read. Everything else about the
// world — the cities, the routes, the skills — is in the files this points at,
// and none of it is in this binary.
const defaultContentDir = "configs/content"

// contentDir resolves TORN_CONTENT_DIR, falling back to the default.
func contentDir() string {
	if dir := os.Getenv("TORN_CONTENT_DIR"); dir != "" {
		return dir
	}
	return defaultContentDir
}

func contentUsage() {
	fmt.Fprint(os.Stderr, `usage: admin content <command>

  validate                  parse and check configs/content, writing nothing
  load --reason "why"       validate, then store as a new active version
  status                    report the active version, read back from the database

TORN_CONTENT_DIR overrides the content directory (default `+defaultContentDir+`).
load and status also require DATABASE_URL.
`)
}

// contentCommand dispatches the content subcommands.
func contentCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		contentUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "validate":
		return contentValidate(args[1:])
	case "load":
		return contentLoad(ctx, args[1:])
	case "status":
		return contentStatus(ctx, args[1:])
	default:
		contentUsage()
		os.Exit(2)
		return nil
	}
}

// loadAndValidate is the step both subcommands share: read the directory,
// check it, and report what was found.
//
// Both go through it because `validate` must answer exactly the question
// `load` asks. If they could differ, `validate` would be a check that passes
// on content the loader then refuses, which is worse than no check at all.
func loadAndValidate(dir string) (*content.Pack, error) {
	pack, err := content.Load(dir)
	if err != nil {
		return nil, err
	}
	if err := pack.Validate(); err != nil {
		return nil, err
	}
	return pack, nil
}

// describe prints what a pack contains, and anything suspicious about it.
//
// The warnings go to stderr and the summary to stdout: a warning is for a
// human, while the summary is the kind of line somebody pipes somewhere.
func describe(pack *content.Pack) {
	fmt.Printf("content directory: %s\n", contentDir())
	fmt.Printf("schema version:    %d\n", pack.Schema)
	fmt.Printf("source checksum:   %s\n", pack.Checksum)
	fmt.Printf("cities:            %d\n", len(pack.Cities))
	fmt.Printf("routes:            %d\n", len(pack.Routes))
	fmt.Printf("skills:            %d\n", len(pack.Skills))

	warnings := pack.Warnings()
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n%d warning(s):\n", len(warnings))
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "  WARNING: %s\n", w)
	}
	// Deliberately not an error. A warning describes content that loads and
	// works; see content.Pack.Warnings for why an unreachable city must not
	// stop a load.
}

// contentValidate checks the files and writes nothing.
//
// It touches no database at all, which is the point: it is what runs in CI and
// on a laptop that has never seen the production DSN.
func contentValidate(args []string) error {
	fs := flag.NewFlagSet("content validate", flag.ExitOnError)
	fs.Usage = contentUsage
	if err := fs.Parse(args); err != nil {
		return err
	}

	pack, err := loadAndValidate(contentDir())
	if err != nil {
		return err
	}
	describe(pack)
	fmt.Println("\ncontent is valid; nothing was written")
	return nil
}

// contentLoad validates the files and stores them as a new active version.
func contentLoad(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("content load", flag.ExitOnError)
	fs.Usage = contentUsage
	reason := fs.String("reason", "", "why this load is happening (required)")
	actor := fs.String("actor", "", "operator identity (defaults to $USER)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Checked before the files are read and before the database is dialled,
	// so the refusal arrives immediately rather than after a connection
	// timeout. ADR 0009: a change whose reason was not recorded is a change
	// nobody can evaluate six months later, and content_versions.notes is
	// NOT NULL for exactly that reason.
	*reason = strings.TrimSpace(*reason)
	if *reason == "" {
		return errors.New("content load: --reason is required; " +
			"a content change with no recorded reason cannot be understood later")
	}

	who := *actor
	if who == "" {
		who = os.Getenv("USER")
	}
	if who == "" {
		who = "unknown"
	}

	pack, err := loadAndValidate(contentDir())
	if err != nil {
		return err
	}
	describe(pack)

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	store := postgres.NewContentStore(pool)
	applied, err := store.Apply(ctx, pack, postgres.ApplyRequest{Actor: who, Reason: *reason})
	if err != nil {
		return err
	}

	fmt.Printf("\nloaded content version %d\n", applied.Version)
	fmt.Printf("version id:        %s\n", applied.VersionID)
	fmt.Printf("stored checksum:   %s\n", applied.Checksum)
	fmt.Printf("loaded by:         %s\n", who)
	fmt.Printf("reason:            %s\n", *reason)
	return nil
}

// contentPool dials the database for the subcommands that need one.
func contentPool(ctx context.Context) (*postgres.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := postgres.New(dialCtx, dsn)
	if err != nil {
		// The DSN carries a password; never echo it.
		return nil, fmt.Errorf("connect to database: %w", redactDSN(err))
	}
	return pool, nil
}

// contentStatus reports the active version, read back from the database.
//
// It goes through LoadActive and BuildSnapshot — the exact path a booting
// service takes (ADR 0004 rule 6) — rather than only reading the version row,
// so a status that prints successfully is proof that a service could boot on
// what is stored, not merely that a row says it is active.
func contentStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("content status", flag.ExitOnError)
	fs.Usage = contentUsage
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := postgres.NewContentStore(pool)

	row, err := store.Active(ctx)
	if errors.Is(err, postgres.ErrNoActiveVersion) {
		return errors.New("no content has been loaded yet; run: admin content load --reason \"...\"")
	}
	if err != nil {
		return err
	}

	pack, err := store.LoadActive(ctx)
	if err != nil {
		return err
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return fmt.Errorf("the active version does not build: %w", err)
	}

	fmt.Printf("active version:    %d\n", pack.Version)
	fmt.Printf("version id:        %s\n", row.ID)
	fmt.Printf("loaded at:         %s\n", row.LoadedAt.UTC().Format(time.RFC3339))
	fmt.Printf("loaded by:         %s\n", row.LoadedBy)
	fmt.Printf("reason:            %s\n", row.Notes)
	fmt.Printf("stored checksum:   %s\n", row.Checksum)
	fmt.Printf("cities:            %d\n", len(pack.Cities))
	fmt.Printf("routes:            %d\n", len(pack.Routes))
	fmt.Printf("skills:            %d\n", len(pack.Skills))
	for _, c := range snap.Cities() {
		fmt.Printf("  %-16s %-16s tax %5d bps  cost %6d  id %s\n",
			c.Code, c.Name, c.TaxRateBPS, c.CostOfLiving, c.ID)
	}

	// Whether the checkout in front of the operator is what is running. Only
	// compared, never required: status must work on a machine with no files.
	if local, err := content.Load(contentDir()); err == nil {
		if local.Checksum == row.Checksum {
			fmt.Printf("\n%s matches the active version\n", contentDir())
		} else {
			fmt.Printf("\n%s DIFFERS from the active version (local checksum %s)\n", contentDir(), local.Checksum)
		}
	}
	return nil
}
