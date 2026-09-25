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
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// lifeCommand is `admin life`: a character's life (docs/adr/0025).
func lifeCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "backfill" {
		lifeUsage()
		os.Exit(2)
	}
	return lifeBackfill(ctx, args[1:])
}

func lifeUsage() {
	fmt.Fprint(os.Stderr, `usage: admin life backfill --by NAME --reason TEXT

  backfill   write into every player's life history what the older records
             still tell — joining, jobs, courses and certificates, companies,
             homes, elections and offices, jail, hospital stays, operations
             commanded, achievements — each marked as reconstructed. It is
             safe to run again: a moment already written is not written twice.
`)
}

// lifeBackfill reconstructs the life histories of players who lived before
// the timeline began.
func lifeBackfill(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("life backfill", flag.ExitOnError)
	by := fs.String("by", "", "who runs it (required)")
	reason := fs.String("reason", "", "why (required)")
	fs.Usage = lifeUsage
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*by) == "" || strings.TrimSpace(*reason) == "" {
		return errors.New("life backfill: --by and --reason are required")
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	snap, err := activeSnapshot(ctx, pool)
	if err != nil {
		return err
	}
	admin := postgres.NewEconomyAdmin(pool)
	rows, err := admin.BackfillRows(ctx)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	firstJob := map[string]bool{}
	uow := postgres.NewUnitOfWork(pool, "fa")
	err = uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		for _, r := range rows {
			kind := r.Kind
			d := application.HistoryData{Code: r.Code, Name: r.Name, PlaceKind: r.PlaceKind, PlaceCode: r.PlaceCode,
				PlaceName: r.PlaceName, Amount: r.Amount, Number: r.Number}
			switch kind {
			case application.HistoryHired:
				if !firstJob[r.PlayerID] {
					firstJob[r.PlayerID] = true
					if n, err := tx.Life().CountHistory(ctx, r.PlayerID, application.HistoryFirstJob); err != nil {
						return err
					} else if n == 0 {
						kind = application.HistoryFirstJob
					}
				}
				if def, ok := snap.CareerDef(r.Code); ok && len(def.Tiers) > 0 {
					d.Sub, d.Name = def.Tiers[0].Rank, def.Tiers[0].Title
				}
			case application.HistoryCourse, application.HistoryCertificate:
				if def, ok := snap.CourseDef(r.Code); ok {
					d.Name = def.Name
				}
			case application.HistoryPropertyBought:
				if t, ok := snap.PropertyType(r.Code); ok {
					d.Name = t.Name
				}
			case application.HistoryAchievement:
				if a, ok := snap.AchievementDef(r.Code); ok {
					d.Name = a.Name
				}
			case application.HistoryJoined:
				d = application.HistoryData{}
			}
			fresh, err := tx.Life().AddHistory(ctx, application.HistoryEntry{PlayerID: r.PlayerID, Kind: kind, At: r.At,
				Public: r.Public, Backfilled: true, Source: "backfill:" + r.Kind + ":" + r.Ref, Data: d})
			if err != nil {
				return err
			}
			if fresh {
				counts[kind]++
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := admin.AuditBackfill(ctx, *by, *reason, counts, time.Now().UTC()); err != nil {
		return err
	}
	total := 0
	for kind, n := range counts {
		fmt.Printf("  %-16s %d\n", kind, n)
		total += n
	}
	fmt.Printf("backfilled %d moments from %d older records\n", total, len(rows))
	return nil
}
