package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Operator tooling for the capability dual read (docs/adr/0044-organic-
// growth-alliances-countries.md phase G1): where what a settlement HAS differs
// from what its tier says. Read-only; nothing here changes the game.

func growthUsage() {
	fmt.Fprint(os.Stderr, `usage: admin growth <command>

  report [--limit N]        the disagreements the running game metered while
                            growth.capabilities was "shadow" (table
                            growth_disagreements): most seen first
  sweep [--settlement CODE] [--all]
                            ask every availability tag of every founded
                            settlement (or one) both ways right now, from the
                            database and the content files, and print where the
                            capability answer and the tier answer differ.
                            Compared rows only; --all prints the agreeing ones too

TORN_CONTENT_DIR overrides the content directory (default `+defaultContentDir+`).
DATABASE_URL must be set.
`)
}

func growthCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		growthUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "report":
		return growthReport(ctx, args[1:])
	case "sweep":
		return growthSweep(ctx, args[1:])
	}
	growthUsage()
	os.Exit(2)
	return nil
}

func growthReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("growth report", flag.ExitOnError)
	fs.Usage = growthUsage
	limit := fs.Int("limit", 200, "the most rows to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	rows, err := postgres.NewGrowthRepository(pool).List(ctx, *limit)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no disagreements metered (growth.capabilities is off, or nothing differs)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SEEN\tSITE\tENTRY\tTIER\tCAPABILITIES\tMISSING\tSETTLEMENT\tLAST")
	for _, r := range rows {
		fmt.Fprintf(w, "%d\t%s\t%s/%s\t%v\t%v\t%s\t%s\t%s\n", r.Count, r.Site, r.Kind, r.Code, r.TierAnswer, r.CapabilityAnswer,
			r.Missing, r.SettlementID, r.LastSeen.UTC().Format("2006-01-02 15:04"))
	}
	return w.Flush()
}

func growthSweep(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("growth sweep", flag.ExitOnError)
	fs.Usage = growthUsage
	only := fs.String("settlement", "", "one settlement by its code (default: every founded settlement)")
	all := fs.Bool("all", false, "also print the rows where the two answers agree")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	pack, err := content.Load(contentDir())
	if err != nil {
		return fmt.Errorf("growth sweep: loading content: %w", err)
	}
	snap, err := content.BuildSnapshot(0, pack)
	if err != nil {
		return fmt.Errorf("growth sweep: %w", err)
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := postgres.NewGrowthRepository(pool)

	dbRows, err := pool.Raw().Query(ctx, `SELECT id::text, code, tier FROM cities WHERE tier IS NOT NULL ORDER BY code`)
	if err != nil {
		return fmt.Errorf("growth sweep: listing settlements: %w", err)
	}
	var settlements []handlers.SweepSettlement
	for dbRows.Next() {
		var s handlers.SweepSettlement
		if err := dbRows.Scan(&s.ID, &s.Code, &s.Tier); err != nil {
			dbRows.Close()
			return err
		}
		if *only == "" || *only == s.Code {
			settlements = append(settlements, s)
		}
	}
	dbRows.Close()
	if err := dbRows.Err(); err != nil {
		return err
	}
	if len(settlements) == 0 {
		fmt.Println("no founded settlement to sweep")
		return nil
	}

	type key struct{ kind, code string }
	type agg struct {
		row             handlers.SweepRow
		tierNo, capNo   int
		total, settlers int
	}
	byEntry := map[key]*agg{}
	for _, s := range settlements {
		if s.Standing, err = repo.Standing(ctx, s.ID); err != nil {
			return err
		}
		rows := handlers.Sweep(snap, s, cfg.Growth.RuinedBPS)
		sum := handlers.Summarise(rows)
		fmt.Printf("%s (%s): %d compared, %d agree, %d disagree %v, %d not comparable yet\n",
			s.Code, s.Tier, sum.Compared, sum.Agree, sum.Disagree, sum.ByClass, sum.Skipped)
		for _, r := range rows {
			if !r.Compared || (!r.Disagrees() && !*all) {
				continue
			}
			a := byEntry[key{r.Kind, r.Code}]
			if a == nil {
				a = &agg{row: r}
				byEntry[key{r.Kind, r.Code}] = a
			}
			a.total++
			if !r.TierAnswer {
				a.tierNo++
			}
			if !r.CapabilityAnswer {
				a.capNo++
			}
		}
	}
	keys := make([]key, 0, len(byEntry))
	for k := range byEntry {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if byEntry[keys[i]].row.Class != byEntry[keys[j]].row.Class {
			return byEntry[keys[i]].row.Class < byEntry[keys[j]].row.Class
		}
		return keys[i].kind+"/"+keys[i].code < keys[j].kind+"/"+keys[j].code
	})
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CLASS\tENTRY\tSTAGE\tROW\tSETTLEMENTS\tTIER SAYS NO\tCAPABILITIES SAY NO")
	for _, k := range keys {
		a := byEntry[k]
		fmt.Fprintf(w, "%s\t%s/%s\t%s\t%s\t%d\t%d\t%d\n", a.row.Class, k.kind, k.code, a.row.Stage, rowText(a.row.Row), a.total, a.tierNo, a.capNo)
	}
	return w.Flush()
}

func rowText(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprint(n)
}
