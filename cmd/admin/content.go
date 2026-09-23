package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
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
  load --reason "why" [--by NAME]
                            validate, then store as a new active version
  status                    report the active version, read back from the database

TORN_CONTENT_DIR overrides the content directory (default `+defaultContentDir+`).
--by names the operator in the audit row; it defaults to $`+operatorEnv+`, then
$USER, and the load is refused when none of them names anybody.
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
	fmt.Printf("spawn weights:     %s\n", spawnSummary(pack))
	fmt.Printf("levels:            %d\n", len(pack.Levels))
	fmt.Printf("jurisdictions:     %d declared, plus one per city\n", len(pack.Jurisdictions))
	fmt.Printf("levers:            %d\n", len(pack.Levers))
	fmt.Printf("offices:           %d\n", len(pack.Offices))
	fmt.Printf("careers:           %d\n", len(pack.Careers))
	fmt.Printf("courses:           %d\n", len(pack.Courses))

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

// spawnSummary lists the cities new players may start in, with their weights,
// ordered by code — the order the pick walks them in. Relative weights only
// mean something next to each other, so they are printed on one line.
func spawnSummary(pack *content.Pack) string {
	candidates := pack.SpawnCandidates()
	if len(candidates) == 0 {
		return "none"
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Code < candidates[j].Code })
	parts := make([]string, 0, len(candidates))
	for _, c := range candidates {
		parts = append(parts, fmt.Sprintf("%s %d", c.Code, c.Weight))
	}
	return strings.Join(parts, ", ")
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
	operator := addOperatorFlags(fs)
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

	who, err := operator.resolve("content load", os.LookupEnv)
	if err != nil {
		return err
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
	fmt.Printf("players placed:    %d (had no city, now in their spawn city)\n", applied.PlayersPlaced)
	fmt.Printf("residences set:    %d (had no residence, now live where they stand)\n", applied.ResidencesSet)
	fmt.Printf("jurisdictions:     %d written\n", applied.Jurisdictions)
	fmt.Printf("office seats:      %d created, all vacant (existing seats and holders untouched)\n", applied.OfficesCreated)
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

// cityTaxLever is the lever a city's tax rate is read through.
const cityTaxLever = "city.tax_rate"

// cityTax is the city's tax rate in force, as the resolver answers it, with
// where it came from. A city the resolver cannot answer for (content loaded
// before governance existed) says so instead of falling back to the column.
func cityTax(ctx context.Context, admin *postgres.GovernanceAdmin, policy application.PolicyReader, code string) string {
	j, err := admin.JurisdictionByCode(ctx, "city", code)
	if err != nil {
		return "n/a (no jurisdiction)"
	}
	v, err := policy.Get(ctx, j.ID, cityTaxLever)
	if err != nil {
		return "n/a (" + err.Error() + ")"
	}
	source := "default"
	if v.Source == application.PolicyFromOffice {
		source = "set"
	}
	return fmt.Sprintf("%5d bps (%s)", v.Value, source)
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
	fmt.Printf("spawn weights:     %s\n", spawnSummary(pack))
	fmt.Printf("levels:            %d\n", len(pack.Levels))
	fmt.Printf("jurisdictions:     %d declared, plus one per city\n", len(pack.Jurisdictions))
	fmt.Printf("levers:            %d\n", len(pack.Levers))
	fmt.Printf("offices:           %d\n", len(pack.Offices))
	fmt.Printf("careers:           %d\n", len(pack.Careers))
	fmt.Printf("courses:           %d\n", len(pack.Courses))

	// The tax shown is the rate IN FORCE, through the one resolver every
	// reader uses (ADR 0015) — a mayor's rate once its notice has passed, the
	// city's default otherwise — never the content column read directly.
	admin := postgres.NewGovernanceAdmin(pool)
	policy := postgres.NewPolicyReader(pool, nil)
	for _, c := range snap.Cities() {
		fmt.Printf("  %-16s %-16s tax %s  cost %6d  id %s\n",
			c.Code, c.Name, cityTax(ctx, admin, policy, c.Code), c.CostOfLiving, c.ID)
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
