package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	ops "github.com/mrjvadi/torncity/internal/operator"
)

// The watch's desk for operators (docs/adr/0023-health-missions-factions.md,
// internal/domain/watch): the flags its behavioural rules raised, with their
// evidence, and the payments it held for review. An operator clears a flag,
// and releases a held payment to its payee or returns it to its payer.
// Nothing here bans anyone, and every change leaves an audit row.

func watchUsage() {
	fmt.Fprint(os.Stderr, `usage: admin watch <command>

  flags [--cleared] [--limit N]   open flags, most recent first (or cleared)
  show NO                          one flag, with its evidence
  clear NO --note TEXT             clear a flag an operator has looked into
  holds [--limit N]                payments held for review, oldest first
  release NO --note TEXT           pay a held payment on to its payee
  return NO --note TEXT            give a held payment back to its payer

Every change takes --by NAME (or $TORN_OPERATOR). DATABASE_URL must be set.
`)
}

// watchCommand dispatches the watch subcommands.
func watchCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		watchUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "flags":
		return watchFlags(ctx, args[1:])
	case "show":
		if len(args) != 2 {
			watchUsage()
			os.Exit(2)
		}
		return watchShow(ctx, args[1])
	case "clear":
		return watchClear(ctx, args[1:])
	case "holds":
		return watchHolds(ctx, args[1:])
	case "release", "return":
		return watchSettle(ctx, args[0] == "release", args[1:])
	}
	watchUsage()
	os.Exit(2)
	return nil
}

// codeOf names a player by their public code, for an operator.
func codeOf(ctx context.Context, players *postgres.PlayerRepository, id string) string {
	if id == "" {
		return "-"
	}
	if p, err := players.GetByID(ctx, id); err == nil {
		return p.PublicCode
	}
	return id
}

func flagLine(ctx context.Context, players *postgres.PlayerRepository, f application.WatchFlag) string {
	return fmt.Sprintf("#%-5d %-18s %-8s other %-8s score %-5d hits %-4d %s  %s", f.No, f.Rule,
		codeOf(ctx, players, f.PlayerID), codeOf(ctx, players, f.OtherPlayerID), f.Score, f.Hits, f.Status,
		f.UpdatedAt.Format(time.RFC3339))
}

func watchFlags(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch flags", flag.ExitOnError)
	fs.Usage = watchUsage
	cleared := fs.Bool("cleared", false, "list cleared flags instead of open ones")
	limit := fs.Int("limit", 50, "the most flags to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	status := application.FlagOpen
	if *cleared {
		status = application.FlagCleared
	}
	flags, err := postgres.NewWatchRepository(pool).Flags(ctx, status, max(*limit, 1))
	if err != nil {
		return err
	}
	if len(flags) == 0 {
		fmt.Println("no flags")
		return nil
	}
	players := postgres.NewPlayerRepository(pool, "fa")
	for _, f := range flags {
		fmt.Println(flagLine(ctx, players, f))
	}
	return nil
}

func watchShow(ctx context.Context, raw string) error {
	no, err := strconv.ParseInt(strings.TrimPrefix(raw, "#"), 10, 64)
	if err != nil {
		return fmt.Errorf("watch show: %q is not a flag number", raw)
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	f, err := postgres.NewWatchRepository(pool).FlagByNo(ctx, no)
	if err != nil {
		return err
	}
	players := postgres.NewPlayerRepository(pool, "fa")
	fmt.Println(flagLine(ctx, players, *f))
	evidence, _ := json.MarshalIndent(f.Evidence, "", "  ")
	fmt.Printf("first seen:  %s\n", f.CreatedAt.Format(time.RFC3339))
	fmt.Printf("evidence:    %s\n", evidence)
	if f.ClearedAt != nil {
		fmt.Printf("cleared:     %s by %s: %s\n", f.ClearedAt.Format(time.RFC3339), f.ClearedBy, f.Note)
	}
	return nil
}

func watchClear(ctx context.Context, args []string) error {
	if len(args) == 0 {
		watchUsage()
		os.Exit(2)
	}
	no, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
	if err != nil {
		return fmt.Errorf("watch clear: %q is not a flag number", args[0])
	}
	fs := flag.NewFlagSet("watch clear", flag.ExitOnError)
	fs.Usage = watchUsage
	note := fs.String("note", "", "what the operator found (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*note) == "" {
		return errors.New("watch clear: --note is required; a flag cleared without a reason cannot be reviewed later")
	}
	who, err := operator.resolve("watch clear", os.LookupEnv)
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := (ops.Ops{Pool: pool, Language: "fa"}).ClearFlag(ctx, no, ops.Actor{Name: who, Reason: *note, At: time.Now()}); err != nil {
		return err
	}
	fmt.Printf("flag #%d cleared by %s\n", no, who)
	return nil
}

func watchHolds(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch holds", flag.ExitOnError)
	fs.Usage = watchUsage
	limit := fs.Int("limit", 50, "the most held payments to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	holds, err := postgres.NewWatchRepository(pool).Holds(ctx, application.HoldHeld, max(*limit, 1))
	if err != nil {
		return err
	}
	if len(holds) == 0 {
		fmt.Println("no payments held")
		return nil
	}
	players := postgres.NewPlayerRepository(pool, "fa")
	for _, h := range holds {
		fmt.Printf("#%-5d %s -> %s  %d (%s)  since %s\n", h.No, codeOf(ctx, players, h.PayerID),
			codeOf(ctx, players, h.PayeeID), h.Amount, h.Method, h.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

func watchSettle(ctx context.Context, release bool, args []string) error {
	verb := "return"
	if release {
		verb = "release"
	}
	if len(args) == 0 {
		watchUsage()
		os.Exit(2)
	}
	no, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
	if err != nil {
		return fmt.Errorf("watch %s: %q is not a held payment's number", verb, args[0])
	}
	fs := flag.NewFlagSet("watch "+verb, flag.ExitOnError)
	fs.Usage = watchUsage
	note := fs.String("note", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*note) == "" {
		return fmt.Errorf("watch %s: --note is required", verb)
	}
	who, err := operator.resolve("watch "+verb, os.LookupEnv)
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	held, err := (ops.Ops{Pool: pool, Language: "fa"}).SettleHold(ctx, no, release, ops.Actor{Name: who, Reason: *note, At: time.Now()})
	if err != nil {
		return err
	}
	fmt.Printf("held payment #%d %s: %d (%s), transaction %s\n", held.No, held.Status, held.Amount, held.Method,
		held.SettleTransactionID)
	return nil
}
