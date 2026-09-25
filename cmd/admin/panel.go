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

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// The operator's panel (docs/adr/0024-property-and-politics.md): the
// economy's dashboard, one player looked up by their code, one city's
// overview, and an announcement to every linked group. Only the
// announcement changes anything, and it writes an audit row with it.

func panelUsage() {
	fmt.Fprint(os.Stderr, `usage:
  admin dashboard [--days N]
                          money held by account kind, what entered and left
                          the economy over the last N days (default 7) by
                          reason, and the price index against the N days before
  admin player CODE       one player: balances, where they are, residence,
                          job, companies, homes, offices, open watch flags
  admin city show --city CODE
                          one city: treasury, budget allocation and the last
                          period's spending, people, companies, homes, damage,
                          offices
  admin announce --text "..." [--text-en "..."] --reason "why" [--by NAME]
                          post the text in every city's linked groups, once
                          each; --text-en is what English groups read instead

DATABASE_URL must be set.
`)
}

// activeSnapshot builds the content in force, for the reference prices and
// the budget lever.
func activeSnapshot(ctx context.Context, pool *postgres.Pool) (*content.Snapshot, error) {
	pack, err := postgres.NewContentStore(pool).LoadActive(ctx)
	if err != nil {
		return nil, err
	}
	return content.BuildSnapshot(pack.Version, pack)
}

func dashboardCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	fs.Usage = panelUsage
	days := fs.Int("days", 7, "the window, in days")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *days < 1 || *days > 365 {
		return errors.New("dashboard: --days must be between 1 and 365")
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
	d, err := postgres.NewEconomyAdmin(pool).EconomyDashboard(ctx, time.Duration(*days)*24*time.Hour, snap, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Println("money supply (minor units), by account kind:")
	for _, f := range d.Supply {
		fmt.Printf("  %-22s %16d\n", f.Reason, f.Amount)
	}
	fmt.Printf("  %-22s %16d\n", "total", d.Total)
	var in, out int64
	fmt.Printf("\nfaucets since %s (money created):\n", d.Since.Format(time.RFC3339))
	for _, f := range d.Faucets {
		fmt.Printf("  %-22s %16d\n", f.Reason, f.Amount)
		in += f.Amount
	}
	fmt.Printf("  %-22s %16d\n", "total", in)
	fmt.Println("\ndrains (money destroyed):")
	for _, f := range d.Drains {
		fmt.Printf("  %-22s %16d\n", f.Reason, f.Amount)
		out += f.Amount
	}
	fmt.Printf("  %-22s %16d\n", "total", out)
	fmt.Printf("\nnet into the economy:   %16d\n", in-out)
	fmt.Println("\nprice index (traded price against reference, 100.00 = at reference):")
	fmt.Printf("  last %d days:  %s\n", *days, index(d.PriceIndex))
	fmt.Printf("  the %d before: %s\n", *days, index(d.PriorIndex))
	if d.PriceIndex > 0 && d.PriorIndex > 0 {
		change := (d.PriceIndex - d.PriorIndex) * 10000 / d.PriorIndex
		fmt.Printf("  inflation proxy: %+d.%02d%%\n", change/100, abs(change%100))
	}
	return nil
}

func index(bps int64) string {
	if bps == 0 {
		return "no trades"
	}
	return fmt.Sprintf("%d.%02d", bps/100, bps%100)
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func playerCommand(ctx context.Context, args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		panelUsage()
		os.Exit(2)
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	c, err := postgres.NewEconomyAdmin(pool).PlayerCard(ctx, args[0])
	if errors.Is(err, postgres.ErrNoSuchPlayer) {
		return fmt.Errorf("player: no player has code %s", args[0])
	}
	if err != nil {
		return err
	}
	state := "here"
	switch {
	case c.Jailed:
		state = "in jail"
	case c.InHospital:
		state = "in hospital"
	case c.Travelling:
		state = "travelling"
	}
	fmt.Printf("player:       %s %s (id %s)\n", c.Code, c.Name, c.ID)
	fmt.Printf("joined:       %s\n", c.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Printf("location:     %s %s (%s)\n", or(c.City, "-"), c.Place, state)
	fmt.Printf("residence:    %s\n", or(c.Residence, "none"))
	fmt.Printf("job:          %s\n", or(c.Job, "none"))
	fmt.Printf("renting:      %s\n", or(c.Renting, "no"))
	fmt.Println("balances (minor units):")
	for _, b := range c.Balances {
		fmt.Printf("  %-22s %16d\n", b.Reason, b.Amount)
	}
	list("companies", c.Companies)
	list("homes owned", c.Properties)
	list("offices", c.Offices)
	fmt.Printf("achievements: %d\n", c.Achievements)
	fmt.Printf("open flags:   %d (see: admin watch)\n", c.OpenFlags)
	return nil
}

func list(title string, items []string) {
	if len(items) == 0 {
		fmt.Printf("%-13s none\n", title+":")
		return
	}
	fmt.Printf("%s:\n", title)
	for _, it := range items {
		fmt.Printf("  %s\n", it)
	}
}

func or(s, empty string) string {
	if s == "" {
		return empty
	}
	return s
}

func cityShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("city show", flag.ExitOnError)
	fs.Usage = panelUsage
	code := fs.String("city", "", "the city's content code (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*code) == "" {
		return errors.New("city show: --city is required")
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	c, err := postgres.NewEconomyAdmin(pool).CityCard(ctx, strings.TrimSpace(*code))
	if errors.Is(err, postgres.ErrNoSuchCity) {
		return fmt.Errorf("city show: no city has code %s", *code)
	}
	if err != nil {
		return err
	}
	fmt.Printf("city:         %s %s (id %s), country %s\n", c.Code, c.Name, c.ID, or(c.Country, "-"))
	fmt.Printf("treasury:     %d minor units\n", c.Treasury)
	fmt.Printf("people:       %d residents, %d here now\n", c.Residents, c.Present)
	fmt.Printf("companies:    %d active\n", c.Companies)
	fmt.Printf("homes owned:  %d\n", c.Properties)
	fmt.Printf("war damage:   %d.%02d%%\n", c.DamageBPS/100, c.DamageBPS%100)
	fmt.Printf("groups:       %d linked\n", c.Groups)

	// The allocation IN FORCE, through the one resolver every reader uses.
	if snap, err := activeSnapshot(ctx, pool); err == nil && c.JurisdictionID != "" {
		if def, ok := snap.Budget(); ok {
			v, err := postgres.NewPolicyReader(pool, nil).Get(ctx, c.JurisdictionID, def.Lever)
			if err != nil {
				return err
			}
			fmt.Printf("allocation:   %s\n", allocation(v.Allocation))
		}
	}
	if c.LastBudget == nil {
		fmt.Println("last budget:  no period settled yet")
	} else {
		fmt.Printf("last budget:  %d spent\n", *c.LastBudget)
		for _, l := range c.LastBudgetLines {
			fmt.Printf("  %-22s %16d\n", l.Reason, l.Amount)
		}
	}
	list("offices", c.Offices)
	return nil
}

func allocation(a map[string]int64) string {
	if len(a) == 0 {
		return "nothing allocated"
	}
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d.%02d%%", k, a[k]/100, a[k]%100))
	}
	return strings.Join(parts, ", ")
}

func announceCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("announce", flag.ExitOnError)
	fs.Usage = panelUsage
	text := fs.String("text", "", "the announcement (required)")
	textEN := fs.String("text-en", "", "what English groups read instead")
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Checked before the database is dialled, like every audited command.
	switch {
	case strings.TrimSpace(*reason) == "":
		return errors.New("announce: --reason is required")
	case strings.TrimSpace(*text) == "":
		return errors.New("announce: --text is required")
	case len(*text) > 3000 || len(*textEN) > 3000:
		return errors.New("announce: the text is longer than a Telegram message allows")
	}
	who, err := operator.resolve("announce", os.LookupEnv)
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	a := postgres.OperatorAnnouncement{Text: strings.TrimSpace(*text), Actor: who,
		Reason: strings.TrimSpace(*reason), At: time.Now().UTC()}
	if t := strings.TrimSpace(*textEN); t != "" {
		a.Texts = map[string]string{"en": t}
	}
	id, cities, err := postgres.Announce(ctx, pool, a)
	if err != nil {
		return fmt.Errorf("announce: %w", err)
	}
	fmt.Printf("announcement %s queued for the groups of %d cities\nby:     %s\nreason: %s\n", id, cities, who, a.Reason)
	return nil
}
