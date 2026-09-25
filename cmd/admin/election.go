package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	ops "github.com/mrjvadi/torncity/internal/operator"
)

// Operator tooling for elections (internal/domain/election). An operator
// opens an office's first election; after its count, an office with a term
// opens its next one on its own when the term runs out.

func electionUsage() {
	fmt.Fprint(os.Stderr, `usage: admin election open --office <code> (--city <code> | --country <code>) --reason <text>

  open  open an election of an elected office in one place: candidacy, then
        the vote, for the real-time periods governance.yml gives it; the vote
        opening and the count are scheduled. TORN_CONFIG (default
        `+config.DefaultPath+`) gives the announcement's default language.

Every election opened writes an audit row with --by (or TORN_OPERATOR) and
--reason.
`)
}

// electionCommand dispatches the election subcommands.
func electionCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "open" {
		electionUsage()
		os.Exit(2)
	}
	return electionOpen(ctx, args[1:])
}

func electionOpen(ctx context.Context, args []string) error {
	const name = "election open"
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = electionUsage
	office := fs.String("office", "", "office code, e.g. mayor")
	place := addPlaceFlags(fs)
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("%s: --reason is required", name)
	}
	if strings.TrimSpace(*office) == "" {
		return fmt.Errorf("%s: --office is required", name)
	}
	kind, code, ok, err := place.resolve()
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !ok {
		return fmt.Errorf("%s: name the place with --city or --country", name)
	}
	who, err := operator.resolve(name, os.LookupEnv)
	if err != nil {
		return err
	}
	cfgPath := os.Getenv("TORN_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultPath
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	opened, err := ops.Ops{Pool: pool, Language: cfg.Player.DefaultLanguage}.OpenElection(ctx,
		strings.TrimSpace(*office), kind, code, ops.Actor{Name: who, Reason: *reason, At: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	fmt.Printf("opened election %d: %s of %s %s\n", opened.No, opened.OfficeCode, kind, code)
	fmt.Printf("candidacy until: %s\n", opened.CandidacyEndsAt.Format(time.RFC3339))
	fmt.Printf("voting until:    %s (the count is scheduled then)\n", opened.VotingEndsAt.Format(time.RFC3339))
	fmt.Printf("by:              %s\n", who)
	return nil
}
