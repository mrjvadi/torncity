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
	"github.com/mrjvadi/torncity/internal/domain/currency"
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
  refund-national-levy --reason "why" [--by NAME]
                          give every founded settlement back what the national levy took from its
                          treasury (ADR 0022 taxed settlements for a country nobody made): paid from the
                          country's state treasury and defence fund in proportion, the rest from the
                          system source, reason levy_refund. Prints each refund. Audited; a second
                          run refunds nothing
  charter-currencies --reason "why" [--by NAME]
                          charter, once, the money of every founded settlement that named one and
                          has none yet: the fee (currency.charter_fee) and a deposit of the smaller
                          of currency.charter_min_deposit and currency.auto_charter_share_bps of the
                          treasury left after the fee, so no treasury is stripped; under
                          currency.auto_charter_floor the settlement is skipped and its head keeps
                          the offer. Audited per settlement; a rerun charters nothing twice
  backfill-functions      give every finished building of every founded settlement the function
                          (and the modules its level includes) its catalogue code stands for (docs/adr/0045
                          step 0.5, phase B1); idempotent, batched per settlement, safe to rerun. Reports
                          the catalogue codes no function replaces
  backfill-timezones      give every founded settlement with no time zone the one its
                          longitude on the world gives (one hour per 15 degrees); never
                          touches a zone a charter set. Run it before game.clock_cutover
  check-sites             read-only: every founded settlement's cell, grid
                          slide, buildable-lot share and founding kit, and
                          whether it meets settlement.min_buildable_lot_share_bps
  landlocked              read-only: every private bare lot no road touches, how a
                          road could reach it and at what price (docs/adr/0043).
                          Changes nothing: each owner chooses in the game between
                          the connection, a road through their own land and a
                          refund (the world is regenerated, about 15 seconds)
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
	case "landlocked":
		return settlementLandlocked(ctx)
	case "backfill-timezones":
		return settlementBackfillTimezones(ctx)
	case "backfill-functions":
		return settlementBackfillFunctions(ctx)
	case "charter-currencies":
		return settlementCharterCurrencies(ctx, args[1:])
	case "refund-national-levy":
		return settlementRefundLevy(ctx, args[1:])
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

func settlementCharterCurrencies(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settlement charter-currencies", flag.ExitOnError)
	fs.Usage = settlementUsage
	reason := fs.String("reason", "", "why (required)")
	op := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	*reason = strings.TrimSpace(*reason)
	if *reason == "" {
		return errors.New("settlement charter-currencies: --reason is required; money created without a recorded reason cannot be accounted for later")
	}
	who, err := op.resolve("settlement charter-currencies", os.LookupEnv)
	if err != nil {
		return err
	}
	ops, cfg, closeFn, err := settlementOps(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	c := cfg.Currency
	rules := application.CurrencyRules{
		CharterR0:  c.CharterR0,
		Terms:      currency.Terms{Fee: c.CharterFee, MinDeposit: c.CharterMinDeposit, ShareBPS: c.AutoCharterShareBPS, Floor: c.AutoCharterFloor},
		MintFeeBPS: c.MintFeeBPS, DeskSlippageBPS: c.DeskSlippageBPS, DeskPresets: c.DeskPresets,
	}
	reports, err := ops.CharterCurrencies(ctx, rules, operator.Actor{Name: who, Reason: *reason, At: time.Now()})
	done := 0
	for _, r := range reports {
		if r.Result.Done {
			done++
			fmt.Printf("chartered %s (%s): fee %d, deposit %d, %d units at r0 %d; treasury %d -> %d\n",
				r.Name, r.SettlementID, r.Result.Fee, r.Result.Deposit, r.Result.Units, r.Result.R0, r.TreasuryBefore, r.TreasuryAfter)
			continue
		}
		fmt.Printf("skipped  %s (%s): %s; treasury %d\n", r.Name, r.SettlementID, r.Result.Reason, r.TreasuryBefore)
	}
	if err != nil {
		return err
	}
	fmt.Printf("settlements chartered: %d of %d\nrun by:               %s\nreason:               %s\n", done, len(reports), who, *reason)
	return nil
}

func settlementRefundLevy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settlement refund-national-levy", flag.ExitOnError)
	fs.Usage = settlementUsage
	reason := fs.String("reason", "", "why (required)")
	op := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	*reason = strings.TrimSpace(*reason)
	if *reason == "" {
		return errors.New("settlement refund-national-levy: --reason is required; money moved without a recorded reason cannot be accounted for later")
	}
	who, err := op.resolve("settlement refund-national-levy", os.LookupEnv)
	if err != nil {
		return err
	}
	ops, _, closeFn, err := settlementOps(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	refunds, err := ops.RefundNationalLevy(ctx, operator.Actor{Name: who, Reason: *reason, At: time.Now()})
	if err != nil {
		return err
	}
	var total, state, fund, source int64
	for _, r := range refunds {
		fmt.Printf("refunded %s minor units to %s (%s): %s from the state treasury, %s from the defence fund, %s from the system source\n",
			money.FromMinor(r.Levy), r.Name, r.SettlementID, money.FromMinor(r.FromStateTreasury), money.FromMinor(r.FromDefenceFund), money.FromMinor(r.FromSource))
		total += r.Levy
		state += r.FromStateTreasury
		fund += r.FromDefenceFund
		source += r.FromSource
	}
	fmt.Printf("settlements refunded: %d\ntotal:                %s (state treasury %s, defence fund %s, system source %s)\nby:                   %s\nreason:               %s\n",
		len(refunds), money.FromMinor(total), money.FromMinor(state), money.FromMinor(fund), money.FromMinor(source), who, *reason)
	return nil
}
