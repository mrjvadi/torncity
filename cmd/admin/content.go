package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
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
load also requires DATABASE_URL.
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

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set")
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := postgres.New(dialCtx, dsn)
	if err != nil {
		// The DSN carries a password; never echo it.
		return fmt.Errorf("connect to database: %w", redactDSN(err))
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
