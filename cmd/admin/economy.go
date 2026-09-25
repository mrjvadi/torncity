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
  grant --player CODE --amount N --reason "why" [--by NAME]
                                  pay N (minor units) into one player's cash from
                                  system_source, as an audited operator grant;
                                  each run is one grant

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
	case "grant":
		return economyGrant(ctx, args[1:])
	default:
		economyUsage()
		os.Exit(2)
		return nil
	}
}

// loadConfig reads the configuration the way the services do.
func loadConfig() (*config.Config, error) {
	path := os.Getenv("TORN_CONFIG")
	if path == "" {
		path = config.DefaultPath
	}
	return config.Load(path)
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

	if v.Goods {
		fmt.Printf("%s  every stack of goods matches the item journal\n", mark(len(v.DriftedStacks) == 0))
		for _, d := range v.DriftedStacks {
			fmt.Printf("        %s of player %s (%s): holds %d, the journal says %d\n", d.Item, d.PlayerID, d.Holding, d.Held, d.Journal)
		}
		fmt.Printf("%s  every unique piece came from a recorded origin (%d without)\n", mark(v.OrphanPieces == 0), v.OrphanPieces)
	}

	if v.Companies {
		c := v.CompanyInvariants
		fmt.Printf("%s  every company treasury belongs to a company (%d orphans)\n", mark(c.OrphanCompanyAccounts == 0), c.OrphanCompanyAccounts)
		fmt.Printf("%s  no company holds less than the wages its running shifts reserved\n", mark(len(c.Underfunded) == 0))
		for _, u := range c.Underfunded {
			fmt.Printf("        %s\n", u)
		}
		fmt.Printf("%s  every closed company holds nothing\n", mark(len(c.DissolvedWithMoney) == 0))
		for _, d := range c.DissolvedWithMoney {
			fmt.Printf("        %s\n", d)
		}
		fmt.Printf("%s  every settled period paid within its budget, as its companies' rows say\n", mark(len(c.OverBudget) == 0))
		for _, o := range c.OverBudget {
			fmt.Printf("        %s\n", o)
		}
		fmt.Printf("%s  NPC revenue in the ledger matches the settled periods (%d = %d)\n",
			mark(c.NPCRevenue == c.PeriodRevenue), c.NPCRevenue, c.PeriodRevenue)
	}

	if v.Production {
		p := v.ProductionInvariants
		fmt.Printf("%s  every organisation's goods belong to a company that exists (%d without)\n",
			mark(p.OrphanHolders == 0), p.OrphanHolders)
		fmt.Printf("%s  license payments in the ledger match the licenses (%d = %d), each paid to its licensor (%d not)\n",
			mark(p.LicenseLedger == p.LicenseRows && p.UnpaidLicenses == 0), p.LicenseLedger, p.LicenseRows, p.UnpaidLicenses)
		fmt.Printf("%s  company sales in the ledger match the sales (%d = %d)\n",
			mark(p.SaleLedger == p.SaleRows), p.SaleLedger, p.SaleRows)
		fmt.Printf("%s  supplier purchases in the ledger match the purchases (%d = %d)\n",
			mark(p.SupplyLedger == p.SupplyRows), p.SupplyLedger, p.SupplyRows)
		fmt.Printf("%s  research costs in the ledger match the research (%d = %d)\n",
			mark(p.ResearchLedger == p.ResearchRows), p.ResearchLedger, p.ResearchRows)
		fmt.Printf("%s  goods sold to the population left the warehouses as the settled periods say (%d = %d)\n",
			mark(p.NPCStockJournal == p.NPCStockPeriods), p.NPCStockJournal, p.NPCStockPeriods)
		fmt.Printf("%s  every production order's journal is the order: inputs taken, output made\n", mark(len(p.Orders) == 0))
		for _, o := range p.Orders {
			fmt.Printf("        order %s\n", o)
		}
	}

	if v.Military {
		m := v.MilitaryInvariants
		fmt.Printf("%s  every national treasury and defence fund belongs to a country (%d orphans)\n",
			mark(m.OrphanStateAccounts == 0), m.OrphanStateAccounts)
		fmt.Printf("%s  every piece a state holds is a military asset of its country, and every asset such a piece (%d, %d without)\n",
			mark(m.OrphanStateHoldings == 0 && m.OrphanAssets == 0), m.OrphanStateHoldings, m.OrphanAssets)
		fmt.Printf("%s  the cities' national levy in the ledger matches the defence periods (%d = %d)\n",
			mark(m.LevyLedger == m.LevyRows), m.LevyLedger, m.LevyRows)
		fmt.Printf("%s  defence appropriations in the ledger match the defence periods (%d = %d)\n",
			mark(m.AppropriationLedger == m.AppropriationRows), m.AppropriationLedger, m.AppropriationRows)
		fmt.Printf("%s  military upkeep in the ledger matches the defence periods (%d = %d)\n",
			mark(m.UpkeepLedger == m.UpkeepRows), m.UpkeepLedger, m.UpkeepRows)
		fmt.Printf("%s  arms payments in the ledger match the procurements (%d = %d), and so do the pieces delivered (%d = %d)\n",
			mark(m.ProcurementLedger == m.ProcurementRows && m.Procured == m.ProcuredRows), m.ProcurementLedger,
			m.ProcurementRows, m.Procured, m.ProcuredRows)
	}

	if v.War {
		w := v.WarInvariants
		fmt.Printf("%s  every piece lost in war has left the world, once, through the item journal (%d still there; %d journal rows = %d lost)\n",
			mark(w.LostNotGone == 0 && w.LostJournal == w.LostRows), w.LostNotGone, w.LostJournal, w.LostRows)
		fmt.Printf("%s  every piece committed belongs to an operation under way (%d stray)\n",
			mark(w.StrayCommitted == 0), w.StrayCommitted)
		fmt.Printf("%s  every occupied city stands under the country that holds it (%d misplaced)\n",
			mark(w.MisplacedCities == 0), w.MisplacedCities)
		fmt.Printf("%s  war levies in the ledger match the defence periods (%d = %d)\n",
			mark(w.WarLevyLedger == w.WarLevyRows), w.WarLevyLedger, w.WarLevyRows)
		fmt.Printf("%s  repairs in the ledger match the defence periods (%d = %d)\n",
			mark(w.RepairLedger == w.RepairRows), w.RepairLedger, w.RepairRows)
	}

	d := v.DefenceInvariants
	fmt.Printf("%s  every military wage left a defence fund for a soldier's cash (%d stray legs), and matches the shifts it paid (%d = %d)\n",
		mark(d.StrayWageLegs == 0 && d.WageLedger == d.WageRows), d.StrayWageLegs, d.WageLedger, d.WageRows)

	capsOK := true
	if v.StageE {
		s := v.StageEInvariants
		fmt.Printf("%s  hospital fees in the ledger match the city hospital's treatments (%d = %d)\n",
			mark(s.HospitalFeeLedger == s.HospitalFeeRows), s.HospitalFeeLedger, s.HospitalFeeRows)
		fmt.Printf("%s  clinic fees in the ledger match the clinics' treatments (%d = %d), each paid (%d not)\n",
			mark(s.TreatmentFeeLedger == s.TreatmentFeeRows && s.UnpaidTreatments == 0), s.TreatmentFeeLedger,
			s.TreatmentFeeRows, s.UnpaidTreatments)
		fmt.Printf("%s  medicine clinics used left the world through the item journal (%d = %d)\n",
			mark(s.MedicineJournal == s.MedicineRows), s.MedicineJournal, s.MedicineRows)
		fmt.Printf("%s  every faction bank belongs to a faction (%d orphans), a disbanded one holds nothing (%d do), and moves by its own reasons only (%d stray)\n",
			mark(s.OrphanFactionAccounts == 0 && s.DisbandedWithMoney == 0 && s.StrayFactionMoves == 0),
			s.OrphanFactionAccounts, s.DisbandedWithMoney, s.StrayFactionMoves)
		fmt.Printf("%s  organised crime takes in the ledger match the operations (%d = %d), and the faction cuts (%d = %d)\n",
			mark(s.HeistLedger == s.HeistRows && s.CutLedger == s.CutRows), s.HeistLedger, s.HeistRows, s.CutLedger, s.CutRows)
		fmt.Printf("%s  mission rewards in the ledger match the completed missions and their grants (%d = %d = %d)\n",
			mark(s.MissionLedger == s.MissionRows && s.MissionLedger == s.MissionGrants), s.MissionLedger, s.MissionRows,
			s.MissionGrants)
		if cfg, err := loadConfig(); err == nil {
			capsOK = s.MissionPlayerDayMax <= cfg.Missions.PlayerDailyCap && s.MissionEconomyDayMax <= cfg.Missions.EconomyDailyCap
			fmt.Printf("%s  no day paid a player more mission cash than its cap (%d <= %d), nor everyone (%d <= %d)\n",
				mark(capsOK), s.MissionPlayerDayMax, cfg.Missions.PlayerDailyCap, s.MissionEconomyDayMax,
				cfg.Missions.EconomyDailyCap)
		}
		fmt.Printf("%s  held payments in the ledger match the payments still held (%d = %d), each settled one settled (%d not)\n",
			mark(s.HeldLedger == s.HeldRows && s.UnsettledHolds == 0), s.HeldLedger, s.HeldRows, s.UnsettledHolds)
	}

	if v.StageF {
		f := v.StageFInvariants
		fmt.Printf("%s  budget spending in the ledger matches the cities' budget periods (%d = %d), and defence contributions (%d = %d)\n",
			mark(f.BudgetLedger == f.BudgetRows && f.DefenceLedger == f.DefenceRows), f.BudgetLedger, f.BudgetRows,
			f.DefenceLedger, f.DefenceRows)
		fmt.Printf("%s  every property the cities sold was paid for once (%d purchases = %d properties)\n",
			mark(f.PurchaseTransactions == f.PropertyRows), f.PurchaseTransactions, f.PropertyRows)
		fmt.Printf("%s  property sales in the ledger match the offers sold (%d = %d)\n",
			mark(f.PropertySaleLedger == f.PropertySaleRows), f.PropertySaleLedger, f.PropertySaleRows)
		fmt.Printf("%s  property tax and upkeep in the ledger match the period charges (%d = %d, %d = %d), no debt below zero (%d)\n",
			mark(f.PropertyTaxLedger == f.PropertyTaxRows && f.PropertyUpkeepLedger == f.PropertyUpkeepRows && f.NegativeDebts == 0), f.PropertyTaxLedger,
			f.PropertyTaxRows, f.PropertyUpkeepLedger, f.PropertyUpkeepRows, f.NegativeDebts)
		fmt.Printf("%s  rent in the ledger matches the rent payments (%d = %d)\n",
			mark(f.RentLedger == f.RentRows), f.RentLedger, f.RentRows)
		fmt.Printf("%s  fuel in the ledger matches the journeys driven in players' own vehicles (%d = %d)\n",
			mark(f.FuelLedger == f.FuelRows), f.FuelLedger, f.FuelRows)
		if f.Tariffs {
			fmt.Printf("%s  border tariffs in the ledger match the tariffed trades (%d = %d)\n",
				mark(f.TariffLedger == f.TariffRows), f.TariffLedger, f.TariffRows)
		}
		if f.Achievements {
			fmt.Printf("%s  achievement rewards in the ledger match the awards and their grants (%d = %d = %d)\n",
				mark(f.AchievementLedger == f.AchievementRows && f.AchievementLedger == f.AchievementGrants),
				f.AchievementLedger, f.AchievementRows, f.AchievementGrants)
			if cfg, err := loadConfig(); err == nil {
				ok := f.AchievementPlayerDayMax <= cfg.Achievements.PlayerDailyCap &&
					f.AchievementEconomyDayMax <= cfg.Achievements.EconomyDailyCap
				capsOK = capsOK && ok
				fmt.Printf("%s  no day paid a player more achievement cash than its cap (%d <= %d), nor everyone (%d <= %d)\n",
					mark(ok), f.AchievementPlayerDayMax, cfg.Achievements.PlayerDailyCap, f.AchievementEconomyDayMax,
					cfg.Achievements.EconomyDailyCap)
			}
		}
	}

	if !v.OK() || !capsOK {
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

// economyGrant pays one operator grant into one player's cash.
//
// The player is named by public code, as players name each other.
func economyGrant(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("economy grant", flag.ExitOnError)
	fs.Usage = economyUsage
	code := fs.String("player", "", "the player's public code (required)")
	minor := fs.Int64("amount", 0, "amount in minor units, above zero (required)")
	reason := fs.String("reason", "", "why this grant is being made (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	*reason = strings.TrimSpace(*reason)
	switch {
	case strings.TrimSpace(*code) == "":
		return errors.New("economy grant: --player is required")
	case *minor <= 0:
		return errors.New("economy grant: --amount must be above zero")
	case *reason == "":
		return errors.New("economy grant: --reason is required; " +
			"money created without a recorded reason cannot be accounted for later")
	}
	who, err := operator.resolve("economy grant", os.LookupEnv)
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
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	admin := postgres.NewEconomyAdmin(pool)
	playerID, label, err := admin.PlayerByCode(ctx, *code)
	if err != nil {
		return err
	}
	amount := money.FromMinor(*minor)
	grantedBy := "admin:" + who
	now := time.Now()

	// The audit row is written first, as grant-starting does, so the intent
	// is on record even if the grant itself fails; the grant row names the
	// operator too, so every unit of money traces back to this run.
	if err := admin.AppendAudit(ctx, postgres.AuditEntry{
		Actor:      who,
		Action:     "economy.grant",
		TargetType: "reward_grants",
		NewValue:   map[string]any{"player": playerID, "amount": amount.Minor()},
		Reason:     *reason,
		At:         now,
	}); err != nil {
		return err
	}

	var grant application.RewardGrant
	uow := postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage)
	err = uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		grant, err = application.GrantAdminCash(ctx, tx.Ledger(), playerID, amount, grantedBy, now)
		return err
	})
	if err != nil {
		return err
	}

	fmt.Printf("granted:        %s minor units\n", amount)
	fmt.Printf("to:             %s\n", label)
	fmt.Printf("grant:          %s\n", grant.ID)
	fmt.Printf("granted by:     %s\n", grantedBy)
	fmt.Printf("reason:         %s\n", *reason)
	return nil
}
