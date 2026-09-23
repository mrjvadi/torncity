package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// verifyReportLimit bounds how many violations of each kind verify prints. A
// broken ledger can have thousands; the first twenty say what is wrong.
const verifyReportLimit = 20

func economyUsage() {
	fmt.Fprint(os.Stderr, `usage: admin economy <command>

  verify                          check the ledger invariants (ADR 0009); exits
                                  non-zero if any fails
  grant-starting --reason "why" [--by NAME]
                                  give every player who has not had it their
                                  starting cash (economy.starting_cash); a player
                                  already granted is skipped, so it is safe to
                                  run again

--by names the operator in the audit row; it defaults to $`+operatorEnv+`, then
$USER, and the grant is refused when none of them names anybody.

DATABASE_URL must be set. TORN_CONFIG overrides the configuration file
(default `+config.DefaultPath+`).
`)
}

// economyCommand dispatches the economy subcommands.
func economyCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		economyUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "verify":
		return economyVerify(ctx, args[1:])
	case "grant-starting":
		return economyGrantStarting(ctx, args[1:])
	default:
		economyUsage()
		os.Exit(2)
		return nil
	}
}

// errInvariantsBroken makes verify exit non-zero. The details are already
// printed; this only has to say that they are failures.
var errInvariantsBroken = errors.New("economy verify: ledger invariants FAILED — this is a bug, not a tuning problem")

// economyVerify runs the three invariants and prints what it found.
func economyVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("economy verify", flag.ExitOnError)
	fs.Usage = economyUsage
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, verifyReportLimit)
	if err != nil {
		return err
	}

	fmt.Printf("accounts:       %d\n", v.Accounts)
	fmt.Printf("transactions:   %d\n", v.Transactions)
	fmt.Printf("entries:        %d\n", v.Entries)
	fmt.Printf("money supply:   %s (sum of non-system balances)\n\n", v.MoneySupply)

	fmt.Printf("%s  ledger sums to zero (sum = %s)\n", mark(v.LedgerSum == "0"), v.LedgerSum)
	fmt.Printf("%s  every transaction balances\n", mark(len(v.Unbalanced) == 0))
	for _, u := range v.Unbalanced {
		fmt.Printf("        transaction %s sums to %s\n", u.TransactionID, u.Sum)
	}
	fmt.Printf("%s  cached balances match their entries\n", mark(len(v.Drifted) == 0))
	for _, d := range v.Drifted {
		fmt.Printf("        account %s (%s): cached %d, entries say %s\n", d.AccountID, d.Kind, d.Cached, d.Derived)
	}

	if !v.OK() {
		return errInvariantsBroken
	}
	fmt.Println("\nall ledger invariants hold")
	return nil
}

func mark(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// economyGrantStarting gives the starting grant to every player who lacks it.
//
// Each player is granted in a unit of work of their own, through the same
// application.GrantStartingCash the first-contact path uses, so there is one
// way money enters a player's hands at start and not two that could drift.
// One transaction per player also means a failure part-way leaves every
// grant already made complete and correct, and a rerun finishes the rest.
func economyGrantStarting(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("economy grant-starting", flag.ExitOnError)
	fs.Usage = economyUsage
	reason := fs.String("reason", "", "why this grant is being made (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// ADR 0009 section 7: --reason is mandatory. Checked before anything is
	// read or dialled.
	*reason = strings.TrimSpace(*reason)
	if *reason == "" {
		return errors.New("economy grant-starting: --reason is required; " +
			"money created without a recorded reason cannot be accounted for later")
	}
	who, err := operator.resolve("economy grant-starting", os.LookupEnv)
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
	amount := money.FromMinor(cfg.Economy.StartingCash)

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	admin := postgres.NewEconomyAdmin(pool)
	players, err := admin.PlayersWithoutStartingGrant(ctx)
	if err != nil {
		return err
	}

	// The audit row is written first, so the intent is on record even if
	// the run is interrupted. Each grant row also names the operator in
	// granted_by, so every unit of money traces back to this run.
	if err := admin.AppendAudit(ctx, postgres.AuditEntry{
		Actor:      who,
		Action:     "economy.grant_starting",
		TargetType: "reward_grants",
		NewValue: map[string]any{
			"amount":     amount.Minor(),
			"candidates": len(players),
		},
		Reason: *reason,
		At:     time.Now(),
	}); err != nil {
		return err
	}

	uow := postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage)
	grantedBy := "admin:" + who
	granted, skipped := 0, 0
	for _, playerID := range players {
		var ok bool
		err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			var err error
			ok, err = application.GrantStartingCash(ctx, tx.Ledger(), playerID, amount, grantedBy, time.Now())
			return err
		})
		if err != nil {
			return fmt.Errorf("granting player %s (after %d granted): %w", playerID, granted, err)
		}
		if ok {
			granted++
		} else {
			skipped++
		}
	}

	fmt.Printf("starting cash:  %s minor units per player\n", amount)
	fmt.Printf("candidates:     %d\n", len(players))
	fmt.Printf("granted:        %d\n", granted)
	fmt.Printf("skipped:        %d (granted meanwhile by another path)\n", skipped)
	fmt.Printf("granted by:     %s\n", grantedBy)
	fmt.Printf("reason:         %s\n", *reason)
	return nil
}
