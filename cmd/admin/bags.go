package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Operator tooling for the bags (docs/adr/0046-bags-merchants-currency-
// exchange.md section 4, migrations/0108_bags and 0110_starting_bag_grant).

func bagsUsage() {
	fmt.Fprint(os.Stderr, `usage: admin bags <command>

  grant-starting --by WHO --reason WHY [--page N] [--max N]
                  give every player who has not had it one free shoulder sack,
                  and put it on (the rollout of the bag limit: nobody ends up
                  with less room than before). Safe to run again and from two
                  places at once: each player is fenced by a row of their own
                  and gets exactly one. --max stops after N grants.

DATABASE_URL must be set; the content must be loaded (admin content load).
`)
}

func bagsCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "grant-starting" {
		bagsUsage()
		os.Exit(2)
	}
	return bagsGrantStarting(ctx, args[1:])
}

func bagsGrantStarting(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bags grant-starting", flag.ExitOnError)
	by := fs.String("by", "", "who runs it (required)")
	reason := fs.String("reason", "", "why (required)")
	page := fs.Int("page", 100, "players read per page")
	max := fs.Int("max", 0, "stop after this many grants (0: all)")
	fs.Usage = bagsUsage
	if err := fs.Parse(args); err != nil {
		return err
	}
	who := strings.TrimSpace(*by)
	if who == "" || strings.TrimSpace(*reason) == "" {
		return errors.New("bags grant-starting: --by and --reason are required")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	clock, err := cfg.GameClock()
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	pack, err := postgres.NewContentStore(pool).LoadActive(ctx)
	if err != nil {
		return fmt.Errorf("bags grant-starting: loading the active content: %w", err)
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return err
	}
	admin := postgres.NewEconomyAdmin(pool)
	if err := admin.AppendAudit(ctx, postgres.AuditEntry{
		Actor: who, Action: "bags.grant_starting", TargetType: "starting_bag_grants",
		NewValue: map[string]any{"item": handlers.StartingBagItem, "max": *max}, Reason: *reason, At: time.Now(),
	}); err != nil {
		return err
	}
	granter := handlers.NewBagGranter(postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage), adminIDs{},
		staticContent{snap}, cfg.CarryRules(), clock, nil)
	rep, err := granter.GrantAll(ctx, *page, *max, func(r handlers.GrantReport) {
		fmt.Printf("  ... %d granted\n", r.Granted)
	})
	fmt.Printf("granted:  %d\n", rep.Granted)
	fmt.Printf("skipped:  %d (had one already)\n", rep.Skipped)
	return err
}

// staticContent is one snapshot as a content source.
type staticContent struct{ snap *content.Snapshot }

func (s staticContent) Current() *content.Snapshot { return s.snap }

// adminIDs makes version 4 identifiers, as the game service does.
type adminIDs struct{}

func (adminIDs) NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("admin: no randomness available for identifier generation: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf[:])
}
