package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/switches"
)

// The operator's runtime switches (migrations/0041_runtime_switches): flip
// telegram_play to send players to the web game instead of Telegram, or
// telegram_notices to also stop Telegram notices. The logic is
// internal/operator.Ops, the same the web panel's System > Switches page
// calls, so a switch flips in exactly one way with one audit trail whichever
// door it comes through.

func switchUsage() {
	fmt.Fprint(os.Stderr, `usage:
  admin switch set NAME VALUE --reason "..." [--by NAME]
                          set an operator switch; audited. telegram_play
                          takes on, groups_off or off; telegram_notices
                          takes on or off
  admin switch list       every switch's current value, who set it last, when
                          and why, and — when REDIS_URL is set — the cached
                          value the gateway is actually acting on and its age

DATABASE_URL must be set. REDIS_URL is optional and only used to report the
cache age; a switch list without it still works.
`)
}

func switchCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		switchUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "set":
		return switchSetCommand(ctx, args[1:])
	case "list":
		return switchListCommand(ctx, args[1:])
	default:
		switchUsage()
		os.Exit(2)
	}
	return nil
}

func switchSetCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("switch set", flag.ExitOnError)
	fs.Usage = switchUsage
	reason := fs.String("reason", "", "why, recorded in the audit row")
	opFlags := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 2 {
		switchUsage()
		return errors.New("switch set: a switch NAME and VALUE are required")
	}
	if strings.TrimSpace(*reason) == "" {
		return errors.New("switch set: --reason is required")
	}
	who, err := opFlags.resolve("switch set", os.LookupEnv)
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	st, err := operator.Ops{Pool: pool}.SetSwitch(ctx, strings.TrimSpace(rest[0]), strings.TrimSpace(rest[1]),
		operator.Actor{Name: who, Reason: *reason})
	if err != nil {
		return err
	}
	fmt.Printf("switch %s = %s (by %s, %s)\n", st.Key, st.Value, st.ChangedBy, st.ChangedAt.UTC().Format(time.RFC3339))
	return nil
}

func switchListCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("switch list", flag.ExitOnError)
	fs.Usage = switchUsage
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	rows, err := operator.Ops{Pool: pool}.Switches(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no switch has ever been set; every switch is at its built-in default " +
			"(telegram_play=on, telegram_notices=on)")
		return nil
	}

	reader := switchHealthReader(ctx, pool)
	for _, s := range rows {
		fmt.Printf("%-20s %-12s by %-16s %s\n", s.Key, s.Value, s.ChangedBy, s.ChangedAt.UTC().Format(time.RFC3339))
		fmt.Printf("%-20s reason: %s\n", "", s.Reason)
		if reader == nil {
			continue
		}
		effective, age, err := reader.Get(ctx, s.Key, s.Value)
		switch {
		case err != nil:
			fmt.Printf("%-20s cache:  unavailable (%s); the gateway is failing open to %q\n", "", err, effective)
		case age > 0:
			fmt.Printf("%-20s cache:  %s, cached %s ago\n", "", effective, age.Round(time.Second))
		default:
			fmt.Printf("%-20s cache:  %s, not cached (read straight from the database)\n", "", effective)
		}
	}
	return nil
}

// switchHealthReader wires a switches.Reader over REDIS_URL, when it is set,
// so switch list can print the cache age the gateway sees — the same Reader,
// Source and Cache types cmd/gateway wires. Without REDIS_URL, or when Redis
// cannot be reached, switch list still prints every switch's row from the
// database; it just cannot say how stale the gateway's cached copy is.
func switchHealthReader(ctx context.Context, pool *postgres.Pool) *switches.Reader {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		return nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rdb, err := infraredis.New(dialCtx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "switch list: cannot reach Redis for the cache age: %v\n", err)
		return nil
	}
	// Not closed: this process prints its report and exits right after.
	return &switches.Reader{Source: switchSource{postgres.NewSwitchOps(pool)}, Cache: infraredis.NewSwitchCache(rdb)}
}

// switchSource adapts the database's read to switches.Source, the same
// adapter cmd/gateway/switch.go defines for its own copy of the pool.
type switchSource struct{ ops *postgres.SwitchOps }

func (s switchSource) Get(ctx context.Context, key string) (string, bool, error) {
	return s.ops.Get(ctx, key)
}
