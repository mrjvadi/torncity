package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Operator tooling for a village's treasury (migration 0052). Both commands
// write an audit row and a closed-reason ledger transaction, and
// `admin economy verify` proves them.

func settlementUsage() {
	fmt.Fprint(os.Stderr, `usage: admin settlement <command>

  backfill-grants --reason "why" [--by NAME]
                          give every founded village that never received its
                          founding grant (settlement.founding_grant, from
                          config) that grant, once. Repeating it, or running
                          it on two machines at once, grants nothing twice
  grant --id SETTLEMENT_UUID --amount MINOR --reason "why" [--by NAME]
                          top up one founded village's treasury from the
                          system source; audited
  check-sites             read-only: every founded settlement's cell, grid
                          slide, buildable-lot share and founding kit, and
                          whether it meets settlement.min_buildable_lot_share_bps
  relocate --id UUID --reason "why" [--by NAME]
                          move a settlement to a valid site nearby and lay its
                          founding kit on buildable lots, in one transaction,
                          audited. Refused when the village holds anything
                          beyond its founding kit or has construction in
                          progress. Clients are told to refetch the layout.
                          (check-sites and relocate regenerate the world from
                          the active seed, about 15 seconds.)

--by names the operator in the audit row; it defaults to $`+operatorEnv+`, then
$USER. DATABASE_URL must be set.
`)
}

func settlementCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		settlementUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "backfill-grants":
		return settlementBackfill(ctx, args[1:])
	case "grant":
		return settlementGrant(ctx, args[1:])
	case "check-sites":
		return settlementCheckSites(ctx)
	case "relocate":
		return settlementRelocate(ctx, args[1:])
	}
	settlementUsage()
	os.Exit(2)
	return nil
}

func settlementOps(ctx context.Context) (operator.Ops, *config.Config, func(), error) {
	cfgPath := os.Getenv("TORN_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultPath
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return operator.Ops{}, nil, nil, err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return operator.Ops{}, nil, nil, err
	}
	return operator.Ops{Pool: pool, Language: cfg.Player.DefaultLanguage}, cfg, pool.Close, nil
}

func settlementBackfill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settlement backfill-grants", flag.ExitOnError)
	fs.Usage = settlementUsage
	reason := fs.String("reason", "", "why (required)")
	op := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	*reason = strings.TrimSpace(*reason)
	if *reason == "" {
		return errors.New("settlement backfill-grants: --reason is required; money created without a recorded reason cannot be accounted for later")
	}
	who, err := op.resolve("settlement backfill-grants", os.LookupEnv)
	if err != nil {
		return err
	}
	ops, cfg, closeFn, err := settlementOps(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	granted, err := ops.BackfillSettlementGrants(ctx, cfg.Settlement.FoundingGrant, operator.Actor{Name: who, Reason: *reason, At: time.Now()})
	for _, id := range granted {
		fmt.Printf("granted %s minor units to %s\n", money.FromMinor(cfg.Settlement.FoundingGrant), id)
	}
	if err != nil {
		return err
	}
	fmt.Printf("villages granted: %d\ngranted by:       %s\nreason:           %s\n", len(granted), who, *reason)
	return nil
}

func settlementGrant(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settlement grant", flag.ExitOnError)
	fs.Usage = settlementUsage
	id := fs.String("id", "", "the settlement's id (required)")
	minor := fs.Int64("amount", 0, "amount in minor units, above zero (required)")
	reason := fs.String("reason", "", "why (required)")
	op := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	*reason = strings.TrimSpace(*reason)
	switch {
	case strings.TrimSpace(*id) == "":
		return errors.New("settlement grant: --id is required")
	case *minor <= 0:
		return errors.New("settlement grant: --amount must be above zero")
	case *reason == "":
		return errors.New("settlement grant: --reason is required; money created without a recorded reason cannot be accounted for later")
	}
	who, err := op.resolve("settlement grant", os.LookupEnv)
	if err != nil {
		return err
	}
	ops, _, closeFn, err := settlementOps(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	g, err := ops.GrantSettlement(ctx, strings.TrimSpace(*id), *minor, operator.Actor{Name: who, Reason: *reason, At: time.Now()})
	if err != nil {
		return err
	}
	fmt.Printf("granted:        %s minor units\nto:             %s (%s)\ntop-up:         %s\ngranted by:     %s\nreason:         %s\n",
		money.FromMinor(g.Amount), g.Name, g.SettlementID, g.ID, g.GrantedBy, *reason)
	return nil
}
