package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// Operator reads of player companies (migrations/0019,
// docs/adr/0020-companies.md). Nothing here changes a company: its money
// moves only through the game, where every movement has its reason.

func companyUsage() {
	fmt.Fprint(os.Stderr, `usage: admin company <command>

  list [--limit N]   every company, newest first: code, name, kind, city,
                     status, owner, treasury, debt and staff
  show CODE          one company: its books, shares, openings, staff and
                     its last settled periods

DATABASE_URL must be set.
`)
}

// companyCommand dispatches the company subcommands.
func companyCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		companyUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		return companyList(ctx, args[1:])
	case "show":
		if len(args) != 2 {
			companyUsage()
			os.Exit(2)
		}
		return companyShow(ctx, args[1])
	}
	companyUsage()
	os.Exit(2)
	return nil
}

// companyReads are the reads the company subcommands share.
type companyReads struct {
	companies *postgres.CompanyRepository
	ledger    *postgres.LedgerRepository
	cities    *postgres.CityRepository
	players   *postgres.PlayerRepository
}

func (r companyReads) line(ctx context.Context, c application.Company) (string, error) {
	city := c.CityID
	if row, err := r.cities.ByID(ctx, c.CityID); err == nil {
		city = row.Code
	}
	owner := c.OwnerID
	if p, err := r.players.GetByID(ctx, c.OwnerID); err == nil {
		owner = p.PublicCode
	}
	acct, err := r.ledger.AccountFor(ctx, application.AccountCompanyTreasury, c.ID)
	if err != nil {
		return "", err
	}
	staff, err := r.companies.Staff(ctx, c.ID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%-8s %-24s %-16s %-14s %-9s owner %s  treasury %d  debt %d  staff %d",
		c.Code, c.Name, c.TypeCode, city, c.Status, owner, acct.Balance.Minor(), c.Debt, len(staff)), nil
}

func openCompanyReads(ctx context.Context) (companyReads, func(), error) {
	pool, err := contentPool(ctx)
	if err != nil {
		return companyReads{}, nil, err
	}
	return companyReads{
		companies: postgres.NewCompanyRepository(pool),
		ledger:    postgres.NewLedgerRepository(pool),
		cities:    postgres.NewCityRepository(pool),
		players:   postgres.NewPlayerRepository(pool, "fa"),
	}, pool.Close, nil
}

// companyList prints every company.
func companyList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("company list", flag.ExitOnError)
	fs.Usage = companyUsage
	limit := fs.Int("limit", 100, "the most companies to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, done, err := openCompanyReads(ctx)
	if err != nil {
		return err
	}
	defer done()
	list, err := r.companies.All(ctx, max(*limit, 1))
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no companies")
		return nil
	}
	for _, c := range list {
		line, err := r.line(ctx, c)
		if err != nil {
			return err
		}
		fmt.Println(line)
	}
	return nil
}

// companyShow prints one company in full.
func companyShow(ctx context.Context, code string) error {
	r, done, err := openCompanyReads(ctx)
	if err != nil {
		return err
	}
	defer done()
	c, err := r.companies.ByCode(ctx, playercode.Normalize(code))
	if err != nil {
		return fmt.Errorf("company %s: %w", code, err)
	}
	line, err := r.line(ctx, *c)
	if err != nil {
		return err
	}
	fmt.Println(line)
	reserved, err := r.companies.Reserved(ctx, c.ID)
	if err != nil {
		return err
	}
	fmt.Printf("\nfounded %s  price %d bps  auto-accept %v  arrears %d  rating %d bps  reserved wages %d\n",
		c.FoundedAt.Format("2006-01-02 15:04 MST"), c.PriceBPS, c.AutoAccept, c.Arrears, c.RatingBPS, reserved)
	if c.ClosedAt != nil {
		fmt.Printf("closed %s (%s)\n", c.ClosedAt.Format("2006-01-02 15:04 MST"), c.CloseReason)
	}
	if c.ManagerID != "" {
		if p, err := r.players.GetByID(ctx, c.ManagerID); err == nil {
			fmt.Printf("manager %s\n", p.PublicCode)
		}
	}
	holders, err := r.companies.Shareholders(ctx, c.ID)
	if err != nil {
		return err
	}
	fmt.Printf("\nshares (%d issued):\n", c.TotalShares)
	for _, h := range holders {
		who := h.PlayerID
		if p, err := r.players.GetByID(ctx, h.PlayerID); err == nil {
			who = p.PublicCode
		}
		fmt.Printf("  %s  %d\n", who, h.Shares)
	}
	openings, err := r.companies.Openings(ctx, c.ID)
	if err != nil {
		return err
	}
	fmt.Println("\nopen openings:")
	for _, o := range openings {
		fmt.Printf("  #%d  %s  wage %d  %d of %d filled\n", o.No, o.CareerCode, o.Wage, o.Filled, o.Positions)
	}
	staff, err := r.companies.Staff(ctx, c.ID)
	if err != nil {
		return err
	}
	fmt.Println("\nstaff:")
	for _, e := range staff {
		who := e.PlayerID
		if p, err := r.players.GetByID(ctx, e.PlayerID); err == nil {
			who = p.PublicCode
		}
		fmt.Printf("  %s  %s tier %d  wage %d  %d shifts  working %v\n", who, e.CareerCode, e.Tier, e.Rate, e.TotalShifts, e.Working)
	}
	periods, err := r.companies.Periods(ctx, c.ID, 5)
	if err != nil {
		return err
	}
	fmt.Println("\nlast settled periods:")
	for _, p := range periods {
		fmt.Printf("  #%d  revenue %d  sales tax %d  wages %d  upkeep %d of %d  debt %d  shifts %d  quality %d  sold %d/%d (capacity %d)  balance %d%s\n",
			p.PeriodNo, p.Revenue, p.SalesTax, p.Wages, p.UpkeepPaid, p.UpkeepDue, p.Debt, p.Shifts, p.QualityBPS,
			p.SoldUnits, p.WantedUnits, p.CapacityUnits, p.BalanceAfter, strings.Repeat(" DISSOLVED", boolInt(p.Insolvent)))
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
